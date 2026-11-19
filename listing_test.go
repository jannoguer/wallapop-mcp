package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// captured is a request body recorded by a fake handler.
type captured struct {
	r    *http.Request
	body []byte
}

type recorder struct {
	mu   sync.Mutex
	reqs []captured
}

func (rec *recorder) handler(status int, resp string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, captured{r, b})
		rec.mu.Unlock()
		w.WriteHeader(status)
		w.Write([]byte(resp))
	}
}

func (rec *recorder) last(t *testing.T) captured {
	t.Helper()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.reqs) == 0 {
		t.Fatal("no request recorded")
	}
	return rec.reqs[len(rec.reqs)-1]
}

func writeJPEG(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// parts parses a multipart request into field name -> content.
func parts(t *testing.T, c captured) map[string]string {
	t.Helper()
	_, params, err := mime.ParseMediaType(c.r.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	mr := multipart.NewReader(bytes.NewReader(c.body), params["boundary"])
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p)
		if p.FormName() == "image" {
			out["image"] = p.Header.Get("Content-Type")
			continue
		}
		out[p.FormName()] = string(b)
	}
}

func TestSetItemStatus(t *testing.T) {
	f := newFake(t)
	rec := &recorder{}
	for _, route := range []string{"PUT /items/{id}/reserve", "PUT /items/{id}/sold", "DELETE /items/{id}"} {
		f.wp[route] = rec.handler(http.StatusNoContent, "")
	}
	cs := connect(t, f.authApp(t))

	tests := []struct {
		action, method, path, body, status string
	}{
		{"reserve", "PUT", "/items/abc/reserve", `{"reserved":true}`, "reserved"},
		{"unreserve", "PUT", "/items/abc/reserve", `{"reserved":false}`, "active"},
		{"sold", "PUT", "/items/abc/sold", `{}`, "sold"},
		{"delete", "DELETE", "/items/abc", ``, "deleted"},
	}
	for _, tt := range tests {
		var got Receipt
		call(t, cs, "set_item_status", map[string]any{"id": "abc", "action": tt.action}, &got)
		c := rec.last(t)
		if c.r.Method != tt.method || c.r.URL.Path != tt.path || string(c.body) != tt.body || got.Status != tt.status {
			t.Errorf("%s: %s %s %s -> %+v", tt.action, c.r.Method, c.r.URL.Path, c.body, got)
		}
	}

	if msg := errorText(call(t, cs, "set_item_status", map[string]any{"id": "abc", "action": "pause"}, nil)); !strings.Contains(msg, "invalid action") {
		t.Errorf("bad action error %q", msg)
	}
	f2 := newFake(t)
	f2.wp["PUT /items/{id}/reserve"] = http.NotFound
	cs2 := connect(t, f2.authApp(t))
	if msg := errorText(call(t, cs2, "set_item_status", map[string]any{"id": "new", "action": "reserve"}, nil)); !strings.Contains(msg, "not ready yet") {
		t.Errorf("404 error %q", msg)
	}
}

func TestEditItem(t *testing.T) {
	f := newFake(t)
	put, pics := &recorder{}, &recorder{}
	f.wp["GET /items/{id}"] = serveFixture(t, "auth/item_own.json")
	f.wp["PUT /items/{id}"] = put.handler(http.StatusNoContent, "")
	f.wp["POST /items/{id}/picture2"] = pics.handler(http.StatusOK, "newphoto01")
	cs := connect(t, f.authApp(t))

	var got Receipt
	args := map[string]any{"id": "p618pm4px2z5", "price": 7.5, "attributes": map[string]string{"color": "blue"}, "add_photos": []string{writeJPEG(t, "c.jpg")}}
	call(t, cs, "edit_item", args, &got)
	if got.Status != "updated" {
		t.Fatalf("receipt %+v", got)
	}
	c := put.last(t)
	if c.r.Header.Get("Accept") != acceptUploadV2 || c.r.Header.Get("Content-Type") != "application/json" {
		t.Errorf("headers %v", c.r.Header)
	}
	var body itemBody
	if err := json.Unmarshal(c.body, &body); err != nil {
		t.Fatal(err)
	}
	a := body.Attributes
	if a["title"] != "Taza de ceramica azul" || a["description"] != "Taza de ceramica azul, sin uso. Recogida en mano." ||
		a["price_amount"] != 7.5 || a["condition"] != "as_good_as_new" || a["color"] != "blue" {
		t.Errorf("attributes %v", a)
	}
	if body.CategoryLeafID != "12485" || body.Location == nil || !body.Location.Approximated || body.Location.Latitude == 0 {
		t.Errorf("category/location %+v %+v", body.CategoryLeafID, body.Location)
	}
	if body.Delivery.AllowedByUser || body.Delivery.CostConfigurationID != costBuyerPays {
		t.Errorf("delivery %+v", body.Delivery)
	}
	if len(body.Pictures) != 2 || body.Pictures[0].ID != "p61vdxl3nl26" || body.Pictures[1].Order != 1 {
		t.Errorf("pictures %+v", body.Pictures)
	}
	if p := parts(t, pics.last(t)); p["order"] != "2" || p["image"] != "image/jpeg" {
		t.Errorf("added photo parts %v", p)
	}

	if msg := errorText(call(t, cs, "edit_item", map[string]any{"id": "x", "shippable": true}, nil)); !strings.Contains(msg, "max_weight_kg") {
		t.Errorf("shipping without weight: %q", msg)
	}

	f.wp["PUT /items/{id}"] = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }
	cs = connect(t, f.authApp(t))
	if msg := errorText(call(t, cs, "edit_item", map[string]any{"id": "new", "price": 1}, nil)); !strings.Contains(msg, "not ready yet") {
		t.Errorf("403 error %q", msg)
	}
}

func TestCreateListing(t *testing.T) {
	f := newFake(t)
	comp, pub, pics := &recorder{}, &recorder{}, &recorder{}
	f.wp["POST /items/upload/components"] = comp.handler(http.StatusOK, string(fixture(t, "auth/components.json")))
	f.wp["POST /items"] = pub.handler(http.StatusOK, `{"id":"newitem0001","flags":{}}`)
	f.wp["POST /items/{id}/picture2"] = pics.handler(http.StatusOK, "photo2")
	cs := connect(t, f.authApp(t))

	args := map[string]any{
		"title": "iPhone 12", "description": "Funciona perfecto", "category_id": 9447, "price": 199.5,
		"condition": "good", "attributes": map[string]string{"brand": "Apple"},
		"photos": []string{writeJPEG(t, "a.jpg"), writeJPEG(t, "b.jpg")}, "shippable": true, "max_weight_kg": 2,
	}
	var got Receipt
	call(t, cs, "create_listing", args, &got)
	if got.ID != "newitem0001" || got.Status != "published" || got.Note != "" {
		t.Fatalf("receipt %+v", got)
	}

	var form struct {
		Fields map[string]string `json:"fields"`
		Mode   map[string]string `json:"mode"`
	}
	json.Unmarshal(comp.last(t).body, &form)
	if form.Fields["category_leaf_id"] != "9447" || form.Fields["root_category_id"] != "24200" || form.Fields["summary"] != "iPhone 12" || form.Mode["id"] == "" {
		t.Errorf("components request %+v", form)
	}

	c := pub.last(t)
	if c.r.Header.Get("Accept") != acceptUploadV2 {
		t.Errorf("publish Accept %q", c.r.Header.Get("Accept"))
	}
	p := parts(t, c)
	var body itemBody
	if err := json.Unmarshal([]byte(p["item"]), &body); err != nil {
		t.Fatalf("item part %q: %v", p["item"], err)
	}
	if p["image"] != "image/jpeg" || body.CategoryLeafID != "9447" || body.Location != nil || body.UploadID != form.Mode["id"] {
		t.Errorf("publish body %+v, image %q", body, p["image"])
	}
	if body.Attributes["price_amount"] != 199.5 || body.Attributes["brand"] != "Apple" || body.Attributes["title"] != "iPhone 12" {
		t.Errorf("attributes %v", body.Attributes)
	}
	if !body.Delivery.AllowedByUser || body.Delivery.MaxWeightKg == nil || *body.Delivery.MaxWeightKg != 2 {
		t.Errorf("delivery %+v", body.Delivery)
	}
	if pp := parts(t, pics.last(t)); pp["order"] != "1" || pics.last(t).r.URL.Path != "/items/newitem0001/picture2" {
		t.Errorf("second photo %v", pp)
	}
}

func TestCreateListingValidation(t *testing.T) {
	f := newFake(t)
	var comps map[string]any
	json.Unmarshal(fixture(t, "auth/components.json"), &comps)
	comps["components"] = append(comps["components"].([]any), map[string]any{
		"id": "brand", "is_required": true, "type": "dropdown",
		"data": map[string]any{"source": map[string]any{"options": []any{map[string]any{"id": "apple"}, map[string]any{"id": "samsung"}}}},
	})
	b, _ := json.Marshal(comps)
	pub := &recorder{}
	f.wp["POST /items/upload/components"] = (&recorder{}).handler(http.StatusOK, string(b))
	f.wp["POST /items"] = pub.handler(http.StatusOK, `{"id":"x"}`)
	cs := connect(t, f.authApp(t))

	jpg := writeJPEG(t, "a.jpg")
	txt := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(txt, []byte("not an image"), 0o600)
	base := map[string]any{"title": "t", "description": "d", "category_id": 9447, "price": 10, "condition": "good", "photos": []string{jpg}}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for kk, vv := range base {
			m[kk] = vv
		}
		m[k] = v
		return m
	}
	tests := []struct {
		args    map[string]any
		wantErr string
	}{
		{base, "missing required attributes: brand (apple, samsung)"},
		{with("photos", []string{txt}), "not a jpeg or png"},
		{with("photos", []string{}), "at least one photo"},
		{with("price", 0), "price must be positive"},
		{with("shippable", true), "max_weight_kg is required"},
		{with("category_id", 1), "category"},
	}
	for _, tt := range tests {
		if msg := errorText(call(t, cs, "create_listing", tt.args, nil)); !strings.Contains(msg, tt.wantErr) {
			t.Errorf("%v: error %q, want %q", tt.args, msg, tt.wantErr)
		}
	}
	if len(pub.reqs) != 0 {
		t.Errorf("published despite validation errors: %d requests", len(pub.reqs))
	}
}
