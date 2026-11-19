package wallapop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientGet(t *testing.T) {
	var gotHeader, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader, gotQuery = r.Header.Get("X-DeviceOS"), r.URL.RawQuery
		w.Write([]byte(`{"a":1}`))
	}))
	defer srv.Close()
	c := mustNew(t, Config{})
	c.BaseURL = srv.URL
	var out struct{ A int }
	if err := c.get(context.Background(), "/x", map[string][]string{"k": {"v w"}}, &out); err != nil {
		t.Fatal(err)
	}
	if out.A != 1 || gotHeader != "0" || gotQuery != "k=v+w" {
		t.Errorf("out=%v header=%q query=%q", out, gotHeader, gotQuery)
	}
}
