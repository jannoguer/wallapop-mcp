package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fake serves testdata fixtures in place of Wallapop and Nominatim and
// records every request it receives.
type fake struct {
	mu      sync.Mutex
	wpReqs  []*http.Request
	geoReqs []*http.Request
	wp      map[string]http.HandlerFunc
	geo     map[string]http.HandlerFunc
	mints   int
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func serveFixture(t *testing.T, name string) http.HandlerFunc {
	b := fixture(t, name)
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	}
}

func newFake(t *testing.T) *fake {
	girona := serveFixture(t, "nominatim.json")
	return &fake{
		wp: map[string]http.HandlerFunc{
			"GET /search":             serveFixture(t, "search.json"),
			"GET /items/{id}":         serveFixture(t, "item.json"),
			"GET /users/{id}":         serveFixture(t, "user.json"),
			"GET /users/{id}/stats":   serveFixture(t, "stats.json"),
			"GET /users/{id}/reviews": serveFixture(t, "reviews.json"),
			"GET /categories":         serveFixture(t, "categories.json"),
		},
		geo: map[string]http.HandlerFunc{
			"GET /search": func(w http.ResponseWriter, r *http.Request) {
				if strings.EqualFold(r.URL.Query().Get("q"), "girona") {
					girona(w, r)
					return
				}
				w.Write([]byte("[]"))
			},
		},
	}
}

func (f *fake) server(t *testing.T, dst *[]*http.Request, routes map[string]http.HandlerFunc) *httptest.Server {
	mux := http.NewServeMux()
	for pattern, h := range routes {
		mux.HandleFunc(pattern, h)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		*dst = append(*dst, r)
		f.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

// app starts the servers; override routes in f.wp/f.geo before calling it.
func (f *fake) app(t *testing.T, defaultCity string) *App {
	wp := f.server(t, &f.wpReqs, f.wp)
	geo := f.server(t, &f.geoReqs, f.geo)

	c := NewClient(0)
	c.BaseURL = wp.URL
	g := NewGeocoder()
	g.BaseURL = geo.URL
	g.gate.interval = 0
	return &App{wp: c, geo: g, defaultCity: defaultCity}
}

// requests returns the recorded Wallapop requests whose path matches.
func (f *fake) requests(path string) []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*http.Request
	for _, r := range f.wpReqs {
		if r.URL.Path == path {
			out = append(out, r)
		}
	}
	return out
}

func (f *fake) geoRequests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.geoReqs)
}

func (f *fake) lastQuery(t *testing.T, path string) url.Values {
	t.Helper()
	rs := f.requests(path)
	if len(rs) == 0 {
		t.Fatalf("no request to %s", path)
	}
	return rs[len(rs)-1].URL.Query()
}

// fakeJWT builds an unsigned token carrying the claims the server reads.
func fakeJWT(exp time.Time, hash string) string {
	enc := base64.RawURLEncoding.EncodeToString
	payload, _ := json.Marshal(map[string]any{"exp": exp.Unix(), "hashed_sub": hash})
	return enc([]byte(`{"alg":"none"}`)) + "." + enc(payload) + ".sig"
}

// authApp is f.app with a logged-in session: one fake server stands in for
// the API, its root, the web app (token mint) and PubNub. Each mint rotates
// the session cookie.
func (f *fake) authApp(t *testing.T) *App {
	t.Helper()
	if _, ok := f.wp["GET /api/auth/session"]; !ok {
		f.wp["GET /api/auth/session"] = func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			f.mints++
			n := f.mints
			f.mu.Unlock()
			http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: fmt.Sprintf("rotated%d", n)})
			json.NewEncoder(w).Encode(map[string]string{"token": fakeJWT(time.Now().Add(5*time.Minute), "myhash")})
		}
	}
	a := f.app(t, "")
	a.wp.RootURL, a.wp.WebURL, a.wp.PubNubURL = a.wp.BaseURL, a.wp.BaseURL, a.wp.BaseURL
	s, err := newSession("seed", "")
	if err != nil {
		t.Fatal(err)
	}
	a.wp.sess = s
	return a
}

func (f *fake) mintCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mints
}
