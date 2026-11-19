package wallapop

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestItemByID(t *testing.T) {
	f := newFake(t)
	a := testClient(t, f, "")
	it, err := a.itemByID(context.Background(), "ejkml3e23yzx")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(f.Requests("/items/ejkml3e23yzx")); n != 1 {
		t.Fatalf("item requests = %d", n)
	}
	wantAttrs := map[string]string{
		"brand": "Apple", "model": "iPhone 13", "storage_capacity": "128 GB", "color": "Negro", "up_to_kg": "1.0",
	}
	if !reflect.DeepEqual(it.Attributes, wantAttrs) {
		t.Errorf("attributes = %v", it.Attributes)
	}
	checks := []struct {
		name      string
		got, want any
	}{
		{"id", it.ID, "ejkml3e23yzx"},
		{"title", it.Title, "iPhone 13 Negro"},
		{"price", it.Price, 170.0},
		{"currency", it.Currency, "EUR"},
		{"condition", it.Condition, "fair"},
		{"city", it.City, "Barcelona"},
		{"postal code", it.PostalCode, "08029"},
		{"shippable", it.Shippable, true},
		{"views", it.Views, 540},
		{"favorites", it.Favorites, 26},
		{"conversations", it.Conversations, 16},
		{"updated", it.UpdatedAt, "2026-10-12T11:52:40Z"},
		{"seller", it.SellerID, "w67dkw9py56x"},
		{"url", it.URL, "https://wallapop.com/item/iphone-13-negro-1311621547"},
		{"categories", len(it.Categories), 3},
		{"first category", it.Categories[0], "Tecnología y electrónica"},
		{"images", len(it.Images), 6},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if !strings.HasPrefix(it.Description, "iPhone 13 sin la caja original") {
		t.Errorf("description = %q", it.Description)
	}
	if !strings.HasPrefix(it.Images[0], "https://cdn.wallapop.com/") {
		t.Errorf("image = %q", it.Images[0])
	}
}

func TestItemByIDSparse(t *testing.T) {
	f := newFake(t)
	f.Routes["GET /items/{id}"] = func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"id":"x"}`)) }
	a := testClient(t, f, "")
	it, err := a.itemByID(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if it.ID != "x" || it.Attributes != nil || it.Condition != "" || it.Images == nil || it.Categories == nil || it.UpdatedAt != "" {
		t.Errorf("sparse item = %+v", it)
	}
}

func TestItemByIDEscapesID(t *testing.T) {
	f := newFake(t)
	a := testClient(t, f, "")
	if _, err := a.itemByID(context.Background(), "../users/abc"); err != nil {
		t.Fatal(err)
	}
	if got := f.Requests("")[0].URL.EscapedPath(); got != "/items/..%2Fusers%2Fabc" {
		t.Errorf("path = %s, want the id escaped", got)
	}
}

var fixtureShipping = []ShippingOption{
	{Method: "CARRIER_OFFICE", Carrier: "correos", Cost: 2.59},
	{Method: "BUYER_ADDRESS", Carrier: "correos", Cost: 3.75},
}

func TestItem(t *testing.T) {
	for _, ref := range []string{
		"ejkml3e23yzx",
		"iphone-13-negro-1311621547",
		"https://es.wallapop.com/item/iphone-13-negro-1311621547?utm_source=share#top",
	} {
		f := newFake(t)
		it, err := testClient(t, f, "").Item(context.Background(), ref)
		if err != nil {
			t.Fatalf("%s: %v", ref, err)
		}
		if it.ID != "ejkml3e23yzx" || it.Condition != "fair" || it.DeliveryDays != "3-7" || it.Error != "" ||
			!reflect.DeepEqual(it.ShippingOptions, fixtureShipping) {
			t.Errorf("%s: %+v", ref, it)
		}
		if a, p := len(f.Requests("/items/ejkml3e23yzx")), len(f.Requests("/item/iphone-13-negro-1311621547")); a != 1 || p != 1 {
			t.Errorf("%s: %d item and %d page requests, want 1 each", ref, a, p)
		}
	}
}

func TestItemNotShippable(t *testing.T) {
	f := newFake(t)
	f.Routes["GET /items/{id}"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"id":"x","slug":"x-1","shipping":{"item_is_shippable":true,"user_allows_shipping":false}}`))
	}
	it, err := testClient(t, f, "").Item(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if it.ShippingOptions != nil || len(f.Requests("/item/x-1")) != 0 {
		t.Error("shipping page fetched for an item that is not shippable")
	}
}

func TestItemPageErrors(t *testing.T) {
	f := newFake(t)
	f.Routes["GET /item/{slug}"] = func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", 500) }
	a := testClient(t, f, "")
	it, err := a.Item(context.Background(), "ejkml3e23yzx")
	if err != nil {
		t.Fatal(err)
	}
	if it.Title == "" || it.ShippingOptions != nil || !strings.Contains(it.Error, "shipping options:") || !strings.Contains(it.Error, "HTTP 500") {
		t.Errorf("page failure by id: %+v", it)
	}
	if _, err := a.Item(context.Background(), "iphone-13-negro-1311621547"); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("page failure by slug: %v", err)
	}

	pages := map[string]string{
		"no page data":               `<html><body>captcha</body></html>`,
		`listing "gone-1" not found`: `<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"categories":[]}}}</script>`,
		"decode page data":           `<script id="__NEXT_DATA__" type="application/json">{oops</script>`,
	}
	for want, body := range pages {
		f := newFake(t)
		f.Routes["GET /item/{slug}"] = func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) }
		if _, err := testClient(t, f, "").Item(context.Background(), "gone-1"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("got %v, want %q", err, want)
		}
	}
}
