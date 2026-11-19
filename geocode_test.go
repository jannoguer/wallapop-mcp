package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestLocate(t *testing.T) {
	f := newFake(t)
	var ua, countries string
	girona := f.geo["GET /search"]
	f.geo["GET /search"] = func(w http.ResponseWriter, r *http.Request) {
		ua, countries = r.Header.Get("User-Agent"), r.URL.Query().Get("countrycodes")
		girona(w, r)
	}
	g := f.app(t, "").geo
	p, err := g.Locate(context.Background(), "Girona")
	if err != nil {
		t.Fatal(err)
	}
	if p.Lat != 41.9793006 || p.Lon != 2.8199439 || !strings.HasPrefix(p.Name, "Girona, ") {
		t.Errorf("place = %+v", p)
	}
	if ua != userAgent || countries != "es,it,pt" {
		t.Errorf("user agent %q, countries %q", ua, countries)
	}
	if _, err := g.Locate(context.Background(), "  GIRONA "); err != nil {
		t.Fatal(err)
	}
	if n := f.geoRequests(); n != 1 {
		t.Errorf("geocoder hit %d times, want 1 (cached)", n)
	}
}

func TestLocateErrors(t *testing.T) {
	f := newFake(t)
	g := f.app(t, "").geo
	if _, err := g.Locate(context.Background(), "Atlantis"); err == nil || !strings.Contains(err.Error(), `"Atlantis" not found`) {
		t.Errorf("unknown city: %v", err)
	}

	f = newFake(t)
	f.geo["GET /search"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`[{"lat":"north","lon":"2.8"}]`))
	}
	g = f.app(t, "").geo
	if _, err := g.Locate(context.Background(), "Girona"); err == nil || !strings.Contains(err.Error(), "bad coordinates") {
		t.Errorf("bad coordinates: %v", err)
	}

	f = newFake(t)
	f.geo["GET /search"] = func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "slow down", 429) }
	g = f.app(t, "").geo
	if _, err := g.Locate(context.Background(), "Girona"); err == nil || !strings.Contains(err.Error(), "HTTP 429") {
		t.Errorf("http error: %v", err)
	}
}
