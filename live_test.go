//go:build live

package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Hits the real Wallapop and Nominatim APIs to catch drift: go test -tags live ./...
func TestLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	a := &App{wp: NewClient(defaultMinInterval), geo: NewGeocoder()}

	cats, err := a.cats.all(ctx, a.wp)
	if err != nil {
		t.Fatal(err)
	}
	phones := filterCategories(cats, "smartphones")
	if len(phones) == 0 || phones[0].RootID == phones[0].ID {
		t.Fatalf("smartphones subcategory not found: %+v", phones)
	}

	p := SearchParams{Query: "iphone", City: "Barcelona", RadiusKm: 15, CategoryID: phones[0].ID, MinPrice: 50, Sort: "newest"}
	res, err := a.Search(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Location == nil || len(res.Listings) == 0 || res.NextCursor == "" {
		t.Fatalf("location %v, %d listings, cursor %q", res.Location, len(res.Listings), res.NextCursor)
	}
	for _, l := range res.Listings {
		if l.ID == "" || l.SellerID == "" || l.Price < 50 || !strings.HasPrefix(l.URL, itemURLPrefix) || l.PostedAt == "" {
			t.Errorf("malformed listing: %+v", l)
		}
		// Wallapop approximates locations, so allow some slack over the radius.
		if l.DistanceKm == nil || *l.DistanceKm > 20 {
			t.Errorf("listing %s outside radius: %v km", l.ID, l.DistanceKm)
		}
	}

	page2, err := a.Search(ctx, SearchParams{Cursor: res.NextCursor, City: "Barcelona"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Listings) > 0 && page2.Listings[0].ID == res.Listings[0].ID {
		t.Error("cursor returned the first page again")
	}

	it, err := a.GetItem(ctx, res.Listings[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if it.Title == "" || it.SellerID != res.Listings[0].SellerID || len(it.Categories) == 0 {
		t.Errorf("malformed item: %+v", it)
	}

	s, err := a.GetSeller(ctx, it.SellerID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != it.SellerID || s.Name == "" || s.MemberSince == "" {
		t.Errorf("malformed seller: %+v", s)
	}

	en, err := a.SearchEnriched(ctx, EnrichParams{SearchParams: p, TopN: 3})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range en.Listings {
		if e.Error != "" || e.Seller == nil {
			t.Errorf("enrichment failed for %s: %s", e.ID, e.Error)
		}
	}
}

// liveAccount returns an App logged in with WALLAPOP_SESSION, or skips.
// The rotated cookie is not persisted, so tests never touch the user's config.
func liveAccount(t *testing.T) *App {
	t.Helper()
	seed := os.Getenv("WALLAPOP_SESSION")
	if seed == "" {
		t.Skip("WALLAPOP_SESSION not set")
	}
	a := &App{wp: NewClient(defaultMinInterval), geo: NewGeocoder()}
	s, err := newSession(seed, "")
	if err != nil {
		t.Fatal(err)
	}
	a.wp.sess = s
	return a
}

func TestLiveAccountReads(t *testing.T) {
	a := liveAccount(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := a.MyItems(ctx, MyItemsParams{}); err != nil {
		t.Errorf("active items: %v", err)
	}
	if _, err := a.MyItems(ctx, MyItemsParams{Status: "sold"}); err != nil {
		t.Errorf("sold items: %v", err)
	}
	if _, err := a.Inbox(ctx, InboxParams{}); err != nil {
		t.Errorf("inbox: %v", err)
	}
}
