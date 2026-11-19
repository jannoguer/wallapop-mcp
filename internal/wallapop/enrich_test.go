package wallapop

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestSearchEnriched(t *testing.T) {
	f := newFake(t)
	a := testClient(t, f, "Girona")
	res, err := a.SearchEnriched(context.Background(), EnrichParams{SearchParams: SearchParams{Query: "iphone 13"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Listings) != 5 || res.Location == nil || res.NextCursor == "" {
		t.Fatalf("%d listings, location %v, cursor %q", len(res.Listings), res.Location, res.NextCursor)
	}
	e := res.Listings[0]
	if e.Error != "" {
		t.Fatalf("unexpected error: %s", e.Error)
	}
	if e.Condition != "fair" || e.Attributes["brand"] != "Apple" || e.Views != 540 || e.Favorites != 26 || e.Conversations != 16 {
		t.Errorf("item fields not merged: %+v", e)
	}
	if e.Seller == nil || e.Seller.Rating != 5 || e.Seller.ReportsReceived != 1 || e.Seller.RecentReviews != nil {
		t.Errorf("seller = %+v", e.Seller)
	}
	if e.DistanceKm == nil || e.ID != "ejkml3e23yzx" {
		t.Errorf("listing fields lost: %+v", e.Listing)
	}
	if strings.HasSuffix(res.Listings[2].Description, "...") {
		t.Error("enriched description should be the full text")
	}

	// Listings 0 and 4 share a seller in the fixture.
	sellerReqs := 0
	for _, r := range f.Requests("") {
		if strings.HasPrefix(r.URL.Path, "/users/") && !strings.Contains(r.URL.Path, "/stats") {
			sellerReqs++
		}
	}
	if sellerReqs != 4 {
		t.Errorf("seller fetched %d times, want 4 (deduped)", sellerReqs)
	}
	if res.Listings[4].Seller != res.Listings[0].Seller {
		t.Error("shared seller not reused")
	}
}

func TestSearchEnrichedTopN(t *testing.T) {
	tests := []struct {
		topN, want int
		wantErr    bool
	}{
		{0, 5, false}, // default 10, fixture has 5
		{2, 2, false},
		{20, 5, false},
		{21, 0, true},
		{-1, 0, true},
	}
	for _, tt := range tests {
		f := newFake(t)
		a := testClient(t, f, "")
		res, err := a.SearchEnriched(context.Background(), EnrichParams{TopN: tt.topN})
		if (err != nil) != tt.wantErr {
			t.Errorf("top_n %d: err %v", tt.topN, err)
			continue
		}
		if !tt.wantErr && len(res.Listings) != tt.want {
			t.Errorf("top_n %d: got %d listings", tt.topN, len(res.Listings))
		}
		if tt.topN == 2 && len(f.Requests("/items/ejkml3e23yzx"))+len(f.Requests("/items/qjwroklv0e6o")) != 2 {
			t.Errorf("top_n 2 fetched other items: %d requests", len(f.Requests("")))
		}
		if tt.wantErr && len(f.Requests("")) != 0 {
			t.Errorf("top_n %d: requests sent despite invalid input", tt.topN)
		}
	}
}

func TestSearchEnrichedPartialFailure(t *testing.T) {
	f := newFake(t)
	item := f.Routes["GET /items/{id}"]
	f.Routes["GET /items/{id}"] = func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "qjwroklv0e6o" {
			http.NotFound(w, r)
			return
		}
		item(w, r)
	}
	user := f.Routes["GET /users/{id}"]
	f.Routes["GET /users/{id}"] = func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "36ed2wnknqjd" {
			http.Error(w, "gone", http.StatusGone)
			return
		}
		user(w, r)
	}
	a := testClient(t, f, "")
	res, err := a.SearchEnriched(context.Background(), EnrichParams{})
	if err != nil {
		t.Fatal(err)
	}
	if e := res.Listings[1]; !strings.HasPrefix(e.Error, "item: ") || !strings.Contains(e.Error, "HTTP 404") || e.Seller == nil || e.Views != 0 {
		t.Errorf("item failure: %+v", e)
	}
	if e := res.Listings[2]; !strings.HasPrefix(e.Error, "seller: ") || e.Seller != nil || e.Condition != "fair" {
		t.Errorf("seller failure: %+v", e)
	}
	for _, i := range []int{0, 3, 4} {
		if e := res.Listings[i]; e.Error != "" || e.Seller == nil {
			t.Errorf("listing %d affected by others' failures: %+v", i, e)
		}
	}
}

func TestSearchEnrichedBothFail(t *testing.T) {
	f := newFake(t)
	f.Routes["GET /items/{id}"] = http.NotFound
	f.Routes["GET /users/{id}"] = http.NotFound
	a := testClient(t, f, "")
	res, err := a.SearchEnriched(context.Background(), EnrichParams{TopN: 1})
	if err != nil {
		t.Fatal(err)
	}
	if e := res.Listings[0].Error; !strings.HasPrefix(e, "item: ") || !strings.Contains(e, "; seller: ") {
		t.Errorf("error = %q", e)
	}
}

func TestSearchEnrichedSearchFails(t *testing.T) {
	f := newFake(t)
	f.Routes["GET /search"] = func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "nope", 403) }
	a := testClient(t, f, "")
	if _, err := a.SearchEnriched(context.Background(), EnrichParams{}); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("got %v", err)
	}
}
