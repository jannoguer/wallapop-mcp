package wallapop

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetItemStatus(t *testing.T) {
	f := newFake(t)
	rec := &recorder{}
	for _, route := range []string{"PUT /items/{id}/reserve", "PUT /items/{id}/sold", "DELETE /items/{id}"} {
		f.Routes[route] = rec.handler(http.StatusNoContent, "")
	}
	c := authClient(t, f)
	ctx := context.Background()

	tests := []struct {
		action, method, path, body, status string
	}{
		{"reserve", "PUT", "/items/abc/reserve", `{"reserved":true}`, "reserved"},
		{"unreserve", "PUT", "/items/abc/reserve", `{"reserved":false}`, "active"},
		{"sold", "PUT", "/items/abc/sold", `{}`, "sold"},
		{"delete", "DELETE", "/items/abc", ``, "deleted"},
	}
	for _, tt := range tests {
		got, err := c.SetItemStatus(ctx, StatusParams{ID: "abc", Action: tt.action})
		req := rec.last(t)
		if err != nil || req.r.Method != tt.method || req.r.URL.Path != tt.path || string(req.body) != tt.body || got.Status != tt.status {
			t.Errorf("%s: %s %s %s -> %+v, %v", tt.action, req.r.Method, req.r.URL.Path, req.body, got, err)
		}
	}

	if _, err := c.SetItemStatus(ctx, StatusParams{ID: "abc", Action: "pause"}); err == nil || !strings.Contains(err.Error(), "invalid action") {
		t.Errorf("bad action error %v", err)
	}
	f2 := newFake(t)
	f2.Routes["PUT /items/{id}/reserve"] = http.NotFound
	if _, err := authClient(t, f2).SetItemStatus(ctx, StatusParams{ID: "new", Action: "reserve"}); err == nil || !strings.Contains(err.Error(), "not ready yet") {
		t.Errorf("404 error %v", err)
	}
}

func TestEditItem(t *testing.T) {
	f := newFake(t)
	put, pics := &recorder{}, &recorder{}
	f.Routes["GET /items/{id}"] = serveFixture(t, "auth/item_own.json")
	f.Routes["PUT /items/{id}"] = put.handler(http.StatusNoContent, "")
	f.Routes["POST /items/{id}/picture2"] = pics.handler(http.StatusOK, "newphoto01")
	c := authClient(t, f)
	ctx := context.Background()

	got, err := c.EditItem(ctx, EditParams{
		ID: "p618pm4px2z5", Price: new(7.5), Attributes: map[string]string{"color": "blue"}, AddPhotos: []string{writeJPEG(t, "c.jpg")},
	})
	if err != nil || got.Status != "updated" {
		t.Fatalf("receipt %+v, %v", got, err)
	}
	req := put.last(t)
	if req.r.Header.Get("Accept") != acceptUploadV2 || req.r.Header.Get("Content-Type") != "application/json" {
		t.Errorf("headers %v", req.r.Header)
	}
	var body itemBody
	if err := json.Unmarshal(req.body, &body); err != nil {
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

	if _, err := c.EditItem(ctx, EditParams{ID: "x", Shippable: new(true)}); err == nil || !strings.Contains(err.Error(), "max_weight_kg") {
		t.Errorf("shipping without weight: %v", err)
	}

	f2 := newFake(t)
	f2.Routes["GET /items/{id}"] = serveFixture(t, "auth/item_own.json")
	f2.Routes["PUT /items/{id}"] = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }
	if _, err := authClient(t, f2).EditItem(ctx, EditParams{ID: "new", Price: new(1.0)}); err == nil || !strings.Contains(err.Error(), "not ready yet") {
		t.Errorf("403 error %v", err)
	}
}

func TestCreateListing(t *testing.T) {
	f := newFake(t)
	comp, pub, pics := &recorder{}, &recorder{}, &recorder{}
	f.Routes["POST /items/upload/components"] = comp.handler(http.StatusOK, string(fixture(t, "auth/components.json")))
	f.Routes["POST /items"] = pub.handler(http.StatusOK, `{"id":"newitem0001","flags":{}}`)
	f.Routes["POST /items/{id}/picture2"] = pics.handler(http.StatusOK, "photo2")
	c := authClient(t, f)

	got, err := c.CreateListing(context.Background(), CreateParams{
		Title: "iPhone 12", Description: "Funciona perfecto", CategoryID: 9447, Price: 199.5,
		Condition: "good", Attributes: map[string]string{"brand": "Apple"},
		Photos: []string{writeJPEG(t, "a.jpg"), writeJPEG(t, "b.jpg")}, Shippable: true, MaxWeightKg: 2,
	})
	if err != nil || got.ID != "newitem0001" || got.Status != "published" || got.Note != "" {
		t.Fatalf("receipt %+v, %v", got, err)
	}

	var form struct {
		Fields map[string]string `json:"fields"`
		Mode   map[string]string `json:"mode"`
	}
	json.Unmarshal(comp.last(t).body, &form)
	if form.Fields["category_leaf_id"] != "9447" || form.Fields["root_category_id"] != "24200" || form.Fields["summary"] != "iPhone 12" || form.Mode["id"] == "" {
		t.Errorf("components request %+v", form)
	}

	req := pub.last(t)
	if req.r.Header.Get("Accept") != acceptUploadV2 {
		t.Errorf("publish Accept %q", req.r.Header.Get("Accept"))
	}
	p := parts(t, req)
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
	f.Routes["POST /items/upload/components"] = (&recorder{}).handler(http.StatusOK, string(b))
	f.Routes["POST /items"] = pub.handler(http.StatusOK, `{"id":"x"}`)
	c := authClient(t, f)

	txt := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(txt, []byte("not an image"), 0o600)
	base := CreateParams{Title: "t", Description: "d", CategoryID: 9447, Price: 10, Condition: "good", Photos: []string{writeJPEG(t, "a.jpg")}}
	with := func(change func(*CreateParams)) CreateParams {
		p := base
		change(&p)
		return p
	}
	tests := []struct {
		p       CreateParams
		wantErr string
	}{
		{base, "missing required attributes: brand (apple, samsung)"},
		{with(func(p *CreateParams) { p.Photos = []string{txt} }), "not a jpeg or png"},
		{with(func(p *CreateParams) { p.Photos = nil }), "at least one photo"},
		{with(func(p *CreateParams) { p.Price = 0 }), "price must be positive"},
		{with(func(p *CreateParams) { p.Shippable = true }), "max_weight_kg is required"},
		{with(func(p *CreateParams) { p.CategoryID = 1 }), "category"},
	}
	for _, tt := range tests {
		if _, err := c.CreateListing(context.Background(), tt.p); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("%+v: error %v, want %q", tt.p, err, tt.wantErr)
		}
	}
	if len(pub.reqs) != 0 {
		t.Errorf("published despite validation errors: %d requests", len(pub.reqs))
	}
}
