package wallapop

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestFlattenCategories(t *testing.T) {
	roots := []categoryNode{{ID: 1, Name: "Tech", Subcategories: []categoryNode{
		{ID: 2, Name: "Phones", Subcategories: []categoryNode{{ID: 3, Name: "Smartphones"}}},
	}}, {ID: 9, Name: "Cars"}}
	got := flattenCategories(roots)
	want := []Category{
		{ID: 1, Name: "Tech", Path: "Tech", RootID: 1},
		{ID: 2, Name: "Phones", Path: "Tech > Phones", RootID: 1},
		{ID: 3, Name: "Smartphones", Path: "Tech > Phones > Smartphones", RootID: 1},
		{ID: 9, Name: "Cars", Path: "Cars", RootID: 9},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d categories", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestCategoriesFromFixture(t *testing.T) {
	f := newFake(t)
	a := testClient(t, f, "")
	list, err := a.categories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 147 {
		t.Errorf("got %d categories, want 147", len(list))
	}

	got := filterCategories(list, "telefonia")
	if len(got) != 1 || got[0].ID != 24201 || got[0].RootID != 24200 || got[0].Path != "Tecnología y electrónica > Telefonía: móviles y smartwatches" {
		t.Errorf("telefonia = %+v", got)
	}
	if got := filterCategories(list, "SMARTPHONES"); len(got) != 1 || got[0].ID != 9447 {
		t.Errorf("SMARTPHONES = %+v", got)
	}
	if got := filterCategories(list, ""); len(got) != len(list) {
		t.Errorf("empty query returned %d of %d", len(got), len(list))
	}
	if got := filterCategories(list, "zzz"); got == nil || len(got) != 0 {
		t.Errorf("no match should be an empty, non-nil list: %#v", got)
	}

	if c, err := findCategory(list, 9447); err != nil || c.RootID != 24200 {
		t.Errorf("find 9447 = %+v, %v", c, err)
	}
	if _, err := findCategory(list, 1); err == nil {
		t.Error("unknown id found")
	}

	a.categories(context.Background())
	if n := len(f.Requests("/categories")); n != 1 {
		t.Errorf("categories fetched %d times", n)
	}
}

func TestFold(t *testing.T) {
	if got := fold("  Música, Niños y Bebés "); got != "musica, ninos y bebes" {
		t.Error(got)
	}
}

func TestCategoriesUnavailable(t *testing.T) {
	f := newFake(t)
	f.Routes["GET /categories"] = http.NotFound
	a := testClient(t, f, "")
	if _, err := a.categories(context.Background()); err == nil {
		t.Error("expected error")
	}
	if _, err := a.Search(context.Background(), SearchParams{CategoryID: 9447}); err == nil {
		t.Error("search with a category should fail when categories are unavailable")
	}
	if len(f.Requests("/search")) != 0 {
		t.Error("search sent without a resolved category")
	}
	if _, err := a.Categories(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("Categories error = %v", err)
	}
}
