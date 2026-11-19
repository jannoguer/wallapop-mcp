package main

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestGetItem(t *testing.T) {
	f := newFake(t)
	a := f.app(t, "")
	it, err := a.GetItem(context.Background(), "ejkml3e23yzx")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(f.requests("/items/ejkml3e23yzx")); n != 1 {
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

func TestGetItemSparse(t *testing.T) {
	f := newFake(t)
	f.wp["GET /items/{id}"] = func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"id":"x"}`)) }
	a := f.app(t, "")
	it, err := a.GetItem(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if it.ID != "x" || it.Attributes != nil || it.Condition != "" || it.Images == nil || it.Categories == nil || it.UpdatedAt != "" {
		t.Errorf("sparse item = %+v", it)
	}
}

func TestGetItemEscapesID(t *testing.T) {
	f := newFake(t)
	a := f.app(t, "")
	if _, err := a.GetItem(context.Background(), "../users/abc"); err != nil {
		t.Fatal(err)
	}
	if got := f.wpReqs[0].URL.EscapedPath(); got != "/items/..%2Fusers%2Fabc" {
		t.Errorf("path = %s, want the id escaped", got)
	}
}

func TestGetSeller(t *testing.T) {
	f := newFake(t)
	a := f.app(t, "")
	s, err := a.GetSeller(context.Background(), "w67dkw9py56x", 1)
	if err != nil {
		t.Fatal(err)
	}
	want := Seller{
		ID: "w67dkw9py56x", Name: "Seller A.", Type: "Private", Verified: true,
		MemberSince: "2024-11-20T19:23:19Z", City: "Barcelona", URL: "https://wallapop.com/user/tktkt-460679728",
		Rating: 5, Reviews: 2, Sales: 2, Purchases: 0, ActiveListings: 1, ReportsReceived: 1,
		RecentReviews: []Review{{Date: "2024-12-26T23:45:25Z", Stars: 5, Item: `Dell P2422H 24" Full HD (1920x1080) Monitor, 60Hz`, Role: "sell"}},
	}
	if !reflect.DeepEqual(s, want) {
		t.Errorf("seller\ngot  %+v\nwant %+v", s, want)
	}
}

func TestGetSellerWithoutSellerType(t *testing.T) {
	f := newFake(t)
	f.wp["GET /users/{id}"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"id":"u","micro_name":"Old A.","type":"normal","register_date":1475182528000}`))
	}
	a := f.app(t, "")
	s, err := a.GetSeller(context.Background(), "u", 0)
	if err != nil {
		t.Fatal(err)
	}
	if s.Type != "" || s.Verified || s.MemberSince != "2016-09-29T20:55:28Z" {
		t.Errorf("seller = %+v", s)
	}
}

func TestGetSellerReviewsNewestFirst(t *testing.T) {
	f := newFake(t)
	f.wp["GET /users/{id}/reviews"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`[{"review":{"date":1000,"rating_over_five":3}},{"review":{"date":3000,"rating_over_five":5}},{"review":{"date":2000,"rating_over_five":4}}]`))
	}
	a := f.app(t, "")
	s, err := a.GetSeller(context.Background(), "u", 20)
	if err != nil {
		t.Fatal(err)
	}
	var stars []int
	for _, r := range s.RecentReviews {
		stars = append(stars, r.Stars)
	}
	if !reflect.DeepEqual(stars, []int{5, 4, 3}) {
		t.Errorf("stars order = %v", stars)
	}
}

func TestGetSellerWithoutReviews(t *testing.T) {
	f := newFake(t)
	a := f.app(t, "")
	s, err := a.GetSeller(context.Background(), "u", 0)
	if err != nil {
		t.Fatal(err)
	}
	if s.RecentReviews != nil || len(f.requests("/users/u/reviews")) != 0 {
		t.Error("reviews fetched although 0 were requested")
	}
}

func TestDetailErrors(t *testing.T) {
	f := newFake(t)
	f.wp["GET /users/{id}/stats"] = func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", 500) }
	a := f.app(t, "")
	ctx := context.Background()
	if _, err := a.GetItem(ctx, ""); err == nil {
		t.Error("empty item id accepted")
	}
	if _, err := a.GetSeller(ctx, "", 0); err == nil {
		t.Error("empty seller id accepted")
	}
	for _, n := range []int{-1, 21} {
		if _, err := a.GetSeller(ctx, "u", n); err == nil {
			t.Errorf("reviews=%d accepted", n)
		}
	}
	if _, err := a.GetSeller(ctx, "u", 0); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("stats failure not reported: %v", err)
	}
}

func TestGetSellerReviewsUnavailable(t *testing.T) {
	f := newFake(t)
	f.wp["GET /users/{id}/reviews"] = http.NotFound
	a := f.app(t, "")
	if _, err := a.GetSeller(context.Background(), "u", 5); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("got %v", err)
	}
}
