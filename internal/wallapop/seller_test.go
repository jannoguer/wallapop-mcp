package wallapop

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestSellerByID(t *testing.T) {
	f := newFake(t)
	a := testClient(t, f, "")
	s, err := a.sellerByID(context.Background(), "w67dkw9py56x", 1)
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

func TestSellerByIDWithoutSellerType(t *testing.T) {
	f := newFake(t)
	f.Routes["GET /users/{id}"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"id":"u","micro_name":"Old A.","type":"normal","register_date":1475182528000}`))
	}
	a := testClient(t, f, "")
	s, err := a.sellerByID(context.Background(), "u", 0)
	if err != nil {
		t.Fatal(err)
	}
	if s.Type != "" || s.Verified || s.MemberSince != "2016-09-29T20:55:28Z" {
		t.Errorf("seller = %+v", s)
	}
}

func TestSellerByIDReviewsNewestFirst(t *testing.T) {
	f := newFake(t)
	f.Routes["GET /users/{id}/reviews"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`[{"review":{"date":1000,"rating_over_five":3}},{"review":{"date":3000,"rating_over_five":5}},{"review":{"date":2000,"rating_over_five":4}}]`))
	}
	a := testClient(t, f, "")
	s, err := a.sellerByID(context.Background(), "u", 20)
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

func TestSellerByIDWithoutReviews(t *testing.T) {
	f := newFake(t)
	a := testClient(t, f, "")
	s, err := a.sellerByID(context.Background(), "u", 0)
	if err != nil {
		t.Fatal(err)
	}
	if s.RecentReviews != nil || len(f.Requests("/users/u/reviews")) != 0 {
		t.Error("reviews fetched although 0 were requested")
	}
}

func TestDetailErrors(t *testing.T) {
	f := newFake(t)
	f.Routes["GET /users/{id}/stats"] = func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", 500) }
	a := testClient(t, f, "")
	ctx := context.Background()
	if _, err := a.itemByID(ctx, ""); err == nil {
		t.Error("empty item id accepted")
	}
	if _, err := a.sellerByID(ctx, "", 0); err == nil {
		t.Error("empty seller id accepted")
	}
	for _, n := range []int{-1, 21} {
		if _, err := a.sellerByID(ctx, "u", n); err == nil {
			t.Errorf("reviews=%d accepted", n)
		}
	}
	if _, err := a.sellerByID(ctx, "u", 0); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("stats failure not reported: %v", err)
	}
}

func TestSellerByIDReviewsUnavailable(t *testing.T) {
	f := newFake(t)
	f.Routes["GET /users/{id}/reviews"] = http.NotFound
	a := testClient(t, f, "")
	if _, err := a.sellerByID(context.Background(), "u", 5); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("got %v", err)
	}
}

func TestSeller(t *testing.T) {
	for _, ref := range []string{"w67dkw9py56x", "tktkt-460679728", "https://es.wallapop.com/user/tktkt-460679728"} {
		f := newFake(t)
		s, err := testClient(t, f, "").Seller(context.Background(), ref, 0)
		if err != nil {
			t.Fatalf("%s: %v", ref, err)
		}
		if s.ID != "w67dkw9py56x" || s.Sales != 2 || s.Shipments == nil || *s.Shipments != 3 || s.Error != "" {
			t.Errorf("%s: %+v", ref, s)
		}
		if u, p := len(f.Requests("/users/w67dkw9py56x")), len(f.Requests("/user/tktkt-460679728")); u != 1 || p != 1 {
			t.Errorf("%s: %d user and %d page requests, want 1 each", ref, u, p)
		}
	}
}

func TestSellerPageErrors(t *testing.T) {
	f := newFake(t)
	f.Routes["GET /user/{slug}"] = http.NotFound
	a := testClient(t, f, "")
	s, err := a.Seller(context.Background(), "w67dkw9py56x", 0)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name == "" || s.Shipments != nil || !strings.Contains(s.Error, "HTTP 404") {
		t.Errorf("page failure by id: %+v", s)
	}
	if _, err := a.Seller(context.Background(), "gone-1", 0); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("page failure by slug: %v", err)
	}
}
