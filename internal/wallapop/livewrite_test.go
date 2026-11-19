//go:build livewrite

package wallapop

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// Publishes, edits, reserves, sells and deletes a throwaway listing on the
// account in WALLAPOP_SESSION. Burner accounts only:
// go test -tags "live livewrite" -run LiveWrite ./...
func TestLiveWriteLifecycle(t *testing.T) {
	a := liveAccount(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	title := "Taza de ceramica " + time.Now().Format("150405")
	r, err := a.CreateListing(ctx, CreateParams{
		Title: title, Description: "Taza de ceramica, sin uso. Recogida en mano.", CategoryID: 12485,
		Price: 5, Condition: "as_good_as_new", Photos: []string{writeJPEG(t, "a.jpg"), writeJPEG(t, "b.jpg")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Note != "" {
		t.Errorf("create note: %s", r.Note)
	}
	id := r.ID
	deleted := false
	t.Cleanup(func() {
		if !deleted {
			if _, err := a.SetItemStatus(context.Background(), StatusParams{ID: id, Action: "delete"}); err != nil {
				t.Errorf("cleanup: listing %s left behind: %v", id, err)
			}
		}
	})

	// A new listing takes a few seconds to reach every service.
	retry := func(name string, f func() error) {
		t.Helper()
		var err error
		for range 10 {
			if err = f(); err == nil {
				return
			}
			time.Sleep(3 * time.Second)
		}
		t.Fatalf("%s: %v", name, err)
	}
	mine := func() (MyItem, error) {
		res, err := a.MyItems(ctx, MyItemsParams{})
		if err != nil {
			return MyItem{}, err
		}
		i := slices.IndexFunc(res.Items, func(it MyItem) bool { return it.ID == id })
		if i < 0 {
			return MyItem{}, errNotListed
		}
		return res.Items[i], nil
	}
	retry("listed", func() error { _, err := mine(); return err })

	price := 6.5
	photo := writeJPEG(t, "c.jpg")
	retry("edit", func() error {
		_, err := a.EditItem(ctx, EditParams{ID: id, Price: &price, AddPhotos: []string{photo}})
		return err
	})
	it, err := a.itemByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if it.Price != 6.5 || it.Title != title || len(it.Images) != 3 || it.Condition != "as_good_as_new" {
		t.Errorf("after edit: price %v title %q images %d condition %q", it.Price, it.Title, len(it.Images), it.Condition)
	}

	retry("reserve", func() error { _, err := a.SetItemStatus(ctx, StatusParams{ID: id, Action: "reserve"}); return err })
	if m, err := mine(); err != nil || !m.Reserved {
		t.Errorf("after reserve: %+v, %v", m, err)
	}
	if _, err := a.SetItemStatus(ctx, StatusParams{ID: id, Action: "unreserve"}); err != nil {
		t.Fatal(err)
	}
	if m, err := mine(); err != nil || m.Reserved {
		t.Errorf("after unreserve: %+v, %v", m, err)
	}

	if _, err := a.SetItemStatus(ctx, StatusParams{ID: id, Action: "sold"}); err != nil {
		t.Fatal(err)
	}
	retry("in sold list", func() error {
		res, err := a.MyItems(ctx, MyItemsParams{Status: "sold"})
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(res.Sold, func(s SoldItem) bool { return strings.Contains(s.Title, title) }) {
			return errNotListed
		}
		return nil
	})

	if _, err := a.SetItemStatus(ctx, StatusParams{ID: id, Action: "delete"}); err != nil {
		t.Fatal(err)
	}
	deleted = true
	if _, err := a.itemByID(ctx, id); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("listing still readable after delete: %v", err)
	}
}

var errNotListed = errors.New("listing not in the list yet")
