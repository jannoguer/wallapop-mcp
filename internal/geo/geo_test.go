package geo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeNominatim serves h and counts the requests it receives.
func fakeNominatim(t *testing.T, h http.HandlerFunc) (*Geocoder, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	g := New()
	g.BaseURL = srv.URL
	return g, &n
}

func TestLocate(t *testing.T) {
	girona, err := os.ReadFile("testdata/nominatim.json")
	if err != nil {
		t.Fatal(err)
	}
	var ua, countries, q string
	g, n := fakeNominatim(t, func(w http.ResponseWriter, r *http.Request) {
		ua, countries, q = r.Header.Get("User-Agent"), r.URL.Query().Get("countrycodes"), r.URL.Query().Get("q")
		w.Write(girona)
	})
	p, err := g.Locate(context.Background(), "Girona")
	if err != nil {
		t.Fatal(err)
	}
	if p.Lat != 41.9793006 || p.Lon != 2.8199439 || !strings.HasPrefix(p.Name, "Girona, ") {
		t.Errorf("place = %+v", p)
	}
	if ua != userAgent || countries != "es,it,pt" || q != "Girona" {
		t.Errorf("user agent %q, countries %q, q %q", ua, countries, q)
	}
	if _, err := g.Locate(context.Background(), "  GIRONA "); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 1 {
		t.Errorf("geocoder hit %d times, want 1 (cached)", n.Load())
	}
}

func TestLocateErrors(t *testing.T) {
	tests := []struct {
		body, wantErr string
		status        int
	}{
		{`[]`, `"Atlantis" not found`, 200},
		{`[{"lat":"north","lon":"2.8"}]`, "bad coordinates", 200},
		{"slow down", "HTTP 429", 429},
	}
	for _, tt := range tests {
		g, _ := fakeNominatim(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(tt.status)
			w.Write([]byte(tt.body))
		})
		if _, err := g.Locate(context.Background(), "Atlantis"); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("got %v, want %q", err, tt.wantErr)
		}
	}
}
