package main

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

var testOrigin = &Place{Name: "Girona", Lat: 41.9793006, Lon: 2.8199439}

func TestBuildQuery(t *testing.T) {
	root := &Category{ID: 24200, RootID: 24200}
	sub := &Category{ID: 9447, RootID: 24200}
	tests := []struct {
		name   string
		p      SearchParams
		origin *Place
		cat    *Category
		want   url.Values
	}{
		{"empty", SearchParams{}, nil, nil, url.Values{"source": {"search_box"}}},
		{"all filters", SearchParams{
			Query: "iphone 13", RadiusKm: 12.5, MinPrice: 100, MaxPrice: 300,
			Conditions: []string{"new", "as_good_as_new"}, PostedWithin: "week",
			Sort: "price_asc", ShippableOnly: true,
		}, testOrigin, nil, url.Values{
			"source": {"search_box"}, "keywords": {"iphone 13"},
			"latitude": {"41.9793006"}, "longitude": {"2.8199439"}, "distance_in_km": {"12.5"},
			"min_sale_price": {"100"}, "max_sale_price": {"300"},
			"condition": {"new,as_good_as_new"}, "time_filter": {"lastWeek"},
			"order_by": {"price_low_to_high"}, "is_shippable": {"true"},
		}},
		{"root category", SearchParams{CategoryID: 24200}, nil, root,
			url.Values{"source": {"search_box"}, "category_id": {"24200"}}},
		{"subcategory maps to root plus subcategory_ids", SearchParams{CategoryID: 9447}, nil, sub,
			url.Values{"source": {"search_box"}, "category_id": {"24200"}, "subcategory_ids": {"9447"}}},
		{"cursor ignores other filters", SearchParams{Cursor: "abc", Query: "x", MaxPrice: 5, Sort: "bogus"}, testOrigin, nil,
			url.Values{"source": {"search_box"}, "next_page": {"abc"}}},
		{"min price only", SearchParams{MinPrice: 50}, nil, nil,
			url.Values{"source": {"search_box"}, "min_sale_price": {"50"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildQuery(tt.p, tt.origin, tt.cat)
			if err != nil {
				t.Fatal(err)
			}
			if got.Encode() != tt.want.Encode() {
				t.Errorf("got  %s\nwant %s", got.Encode(), tt.want.Encode())
			}
		})
	}
}

func TestBuildQueryEnums(t *testing.T) {
	for in, want := range sortValues {
		q, err := buildQuery(SearchParams{Sort: in}, nil, nil)
		if err != nil || q.Get("order_by") != want {
			t.Errorf("sort %q: got %q, %v", in, q.Get("order_by"), err)
		}
	}
	for in, want := range postedWithinValues {
		q, err := buildQuery(SearchParams{PostedWithin: in}, nil, nil)
		if err != nil || q.Get("time_filter") != want {
			t.Errorf("posted_within %q: got %q, %v", in, q.Get("time_filter"), err)
		}
	}
	for in := range conditionValues {
		q, err := buildQuery(SearchParams{Conditions: []string{in}}, nil, nil)
		if err != nil || q.Get("condition") != in {
			t.Errorf("condition %q: got %q, %v", in, q.Get("condition"), err)
		}
	}
}

func TestBuildQueryErrors(t *testing.T) {
	tests := []struct {
		name    string
		p       SearchParams
		origin  *Place
		wantErr string
	}{
		{"bad sort", SearchParams{Sort: "cheapest"}, nil, `invalid sort "cheapest", want one of: closest, newest, price_asc, price_desc, relevance`},
		{"bad posted_within", SearchParams{PostedWithin: "lastWeek"}, nil, `invalid posted_within "lastWeek"`},
		{"bad condition", SearchParams{Conditions: []string{"good", "mint"}}, nil, `invalid condition "mint"`},
		{"negative price", SearchParams{MinPrice: -1}, nil, "must not be negative"},
		{"negative radius", SearchParams{RadiusKm: -1}, testOrigin, "must not be negative"},
		{"min above max", SearchParams{MinPrice: 300, MaxPrice: 100}, nil, "min_price is greater than max_price"},
		{"radius without city", SearchParams{RadiusKm: 10}, nil, "radius_km needs a city"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildQuery(tt.p, tt.origin, nil)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("got %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestSearch(t *testing.T) {
	f := newFake(t)
	a := f.app(t, "")
	res, err := a.Search(context.Background(), SearchParams{Query: "iphone 13", City: "Girona", RadiusKm: 100})
	if err != nil {
		t.Fatal(err)
	}

	q := f.lastQuery(t, "/search")
	if q.Get("latitude") != "41.9793006" || q.Get("longitude") != "2.8199439" || q.Get("distance_in_km") != "100" {
		t.Errorf("location not sent: %s", q.Encode())
	}
	if res.Location == nil || !strings.HasPrefix(res.Location.Name, "Girona") {
		t.Errorf("location = %+v", res.Location)
	}
	if !strings.HasPrefix(res.NextCursor, "eyJ") {
		t.Errorf("next cursor = %q", res.NextCursor)
	}
	if len(res.Listings) != 5 {
		t.Fatalf("got %d listings, want 5", len(res.Listings))
	}

	l := res.Listings[0]
	want := Listing{
		ID: "ejkml3e23yzx", Title: "iPhone 13 Negro", Price: 170, Currency: "EUR", City: "Barcelona",
		PostedAt: "2026-10-10T17:14:15Z", Shippable: true, Promoted: true, CategoryID: 24200, SellerID: "w67dkw9py56x",
		URL: "https://wallapop.com/item/iphone-13-negro-1311621547",
	}
	if l.DistanceKm == nil || *l.DistanceKm != 86.8 {
		t.Errorf("distance = %v, want 86.8", l.DistanceKm)
	}
	l.DistanceKm, l.Description = nil, ""
	if l != want {
		t.Errorf("listing\ngot  %+v\nwant %+v", l, want)
	}
	if !res.Listings[1].Reserved {
		t.Error("listing 1 should be reserved")
	}
	if res.Listings[2].Shippable {
		t.Error("listing 2: seller disallows shipping, so not shippable")
	}
	if l := res.Listings[3]; !l.TopProfile || l.Promoted {
		t.Errorf("listing 3: top profile %v, promoted %v", l.TopProfile, l.Promoted)
	}
	if d := res.Listings[2].Description; !strings.HasSuffix(d, "...") || len([]rune(d)) != listingDescMaxRunes+3 {
		t.Errorf("long description not truncated: %d runes", len([]rune(d)))
	}
}

func TestSearchFlags(t *testing.T) {
	f := newFake(t)
	f.wp["GET /search"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"data":{"section":{"payload":{"items":[
			{"id":"a","bump":{"type":"none"},"has_warranty":{"flag":true}},
			{"id":"b","bump":{"type":"city"}},
			{"id":"c"}]}}}}`))
	}
	res, err := f.app(t, "").Search(context.Background(), SearchParams{})
	if err != nil {
		t.Fatal(err)
	}
	var got []bool
	for _, l := range res.Listings {
		got = append(got, l.Promoted)
	}
	if !slices.Equal(got, []bool{false, true, false}) {
		t.Errorf("promoted = %v", got)
	}
	if !res.Listings[0].Warranty || res.Listings[1].Warranty {
		t.Error("warranty flag not mapped")
	}
}

func TestSearchDefaultCity(t *testing.T) {
	f := newFake(t)
	a := f.app(t, "girona")
	res, err := a.Search(context.Background(), SearchParams{Query: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Location == nil || f.lastQuery(t, "/search").Get("latitude") == "" {
		t.Error("default city not applied")
	}
}

func TestSearchWithoutCity(t *testing.T) {
	f := newFake(t)
	a := f.app(t, "")
	res, err := a.Search(context.Background(), SearchParams{Query: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Location != nil || res.Listings[0].DistanceKm != nil {
		t.Error("expected no location and no distances")
	}
	if q := f.lastQuery(t, "/search"); q.Has("latitude") || q.Has("longitude") {
		t.Errorf("coordinates sent without a city: %s", q.Encode())
	}
	if f.geoRequests() != 0 {
		t.Error("geocoder called without a city")
	}
}

func TestSearchLastPage(t *testing.T) {
	f := newFake(t)
	f.wp["GET /search"] = serveFixture(t, "search_last_page.json")
	a := f.app(t, "")
	res, err := a.Search(context.Background(), SearchParams{Cursor: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if res.NextCursor != "" || len(res.Listings) != 3 {
		t.Errorf("cursor %q, %d listings", res.NextCursor, len(res.Listings))
	}
	if q := f.lastQuery(t, "/search"); q.Get("next_page") != "abc" || q.Get("source") != "search_box" {
		t.Errorf("cursor query = %s", q.Encode())
	}
}

func TestSearchCategory(t *testing.T) {
	f := newFake(t)
	a := f.app(t, "")
	if _, err := a.Search(context.Background(), SearchParams{CategoryID: 9447}); err != nil {
		t.Fatal(err)
	}
	q := f.lastQuery(t, "/search")
	if q.Get("category_id") != "24200" || q.Get("subcategory_ids") != "9447" {
		t.Errorf("query = %s", q.Encode())
	}
	if _, err := a.Search(context.Background(), SearchParams{CategoryID: 24200}); err != nil {
		t.Fatal(err)
	}
	if n := len(f.requests("/categories")); n != 1 {
		t.Errorf("categories fetched %d times, want 1 (cached)", n)
	}
}

func TestSearchRejectsBeforeRequest(t *testing.T) {
	tests := []SearchParams{
		{Sort: "bogus"},
		{CategoryID: 1},
		{City: "Atlantis"},
	}
	for _, p := range tests {
		f := newFake(t)
		a := f.app(t, "")
		if _, err := a.Search(context.Background(), p); err == nil {
			t.Errorf("%+v: expected error", p)
		}
		if n := len(f.requests("/search")); n != 0 {
			t.Errorf("%+v: %d search requests sent", p, n)
		}
	}
}

func TestHaversine(t *testing.T) {
	if d := haversineKm(40.4168, -3.7038, 41.3874, 2.1686); math.Abs(d-505.1) > 0.1 {
		t.Errorf("Madrid-Barcelona = %.1f, want 505.1", d)
	}
	if d := haversineKm(1, 2, 1, 2); d != 0 {
		t.Errorf("same point = %f", d)
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("bateria", 10); got != "bateria" {
		t.Error(got)
	}
	if got := truncateRunes("batería ok", 7); got != "batería..." {
		t.Error(got)
	}
}

func TestTimeFormat(t *testing.T) {
	if got := msToTime(1791652455110); got != "2026-10-10T17:14:15Z" {
		t.Error(got)
	}
	if got := secToTime(1791805960); got != "2026-10-12T11:52:40Z" {
		t.Error(got)
	}
	if msToTime(0) != "" || secToTime(0) != "" {
		t.Error("zero timestamps should be empty")
	}
}
