package wallapop

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestMyItems(t *testing.T) {
	f := newFake(t)
	page := fixture(t, "auth/mine_published.json")
	f.Routes["GET /items/mine/published"] = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("since") == "" {
			w.Header().Set("X-NextPage", "since=cur123")
		}
		w.Write(page)
	}
	f.Routes["GET /sdui/selling/finished"] = serveFixture(t, "auth/selling_finished.json")
	c := authClient(t, f)
	ctx := context.Background()

	got, err := c.MyItems(ctx, MyItemsParams{})
	if err != nil || len(got.Items) != 1 || got.NextCursor != "cur123" {
		t.Fatalf("got %+v, %v", got, err)
	}
	it := got.Items[0]
	if it.ID != "p618pm4px2z5" || it.Title != "Taza de ceramica azul" || it.Price != 5 || it.Updated != "2026-11-19" || it.Reserved || it.State != "" {
		t.Errorf("item %+v", it)
	}
	if auth := f.Requests("/items/mine/published")[0].Header.Get("Authorization"); !strings.HasPrefix(auth, "Bearer ") {
		t.Errorf("no bearer token: %q", auth)
	}

	next, err := c.MyItems(ctx, MyItemsParams{Cursor: "cur123"})
	if q := f.LastQuery(t, "/items/mine/published"); err != nil || q.Get("since") != "cur123" || next.NextCursor != "" {
		t.Errorf("cursor not forwarded: %v, next %q, %v", q, next.NextCursor, err)
	}

	sold, err := c.MyItems(ctx, MyItemsParams{Status: "sold"})
	if err != nil || len(sold.Sold) != 1 || sold.Items != nil {
		t.Fatalf("sold %+v, %v", sold, err)
	}
	s := sold.Sold[0]
	if s.Title != "Taza de ceramica azul" || s.Price != "6.00 €" || !strings.Contains(s.Detail, "19 Nov 2026") || !strings.HasPrefix(s.URL, itemURLPrefix) {
		t.Errorf("sold item %+v", s)
	}

	if _, err := c.MyItems(ctx, MyItemsParams{Status: "gone"}); err == nil || !strings.Contains(err.Error(), "status") {
		t.Errorf("bad status error %v", err)
	}
}

func TestMyItemsEmpty(t *testing.T) {
	f := newFake(t)
	f.Routes["GET /items/mine/published"] = func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) }
	none, err := authClient(t, f).MyItems(context.Background(), MyItemsParams{})
	if err != nil || !none.Empty || len(none.Items) != 0 {
		t.Errorf("no listings: %+v, %v", none, err)
	}
}
