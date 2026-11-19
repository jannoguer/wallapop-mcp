// Package wallapoptest fakes Wallapop (API, web app, PubNub) and Nominatim
// with recorded fixtures, for tests.
package wallapoptest

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// SessionCookie is the web session cookie the fake token endpoint rotates.
const SessionCookie = "__Secure-next-auth.session-token"

// GironaJSON is what the fake geocoder answers for "girona".
const GironaJSON = `[{"lat":"41.9793006","lon":"2.8199439","display_name":"Girona, Gironès, Girona, Catalunya, España"}]`

//go:embed testdata
var fixtures embed.FS

// Fake records every request it receives. Change Routes or GeoRoutes
// before Start; patterns use http.ServeMux syntax.
type Fake struct {
	Routes    map[string]http.HandlerFunc // API, API root, web app and PubNub share one server
	GeoRoutes map[string]http.HandlerFunc

	mu      sync.Mutex
	reqs    []*http.Request
	geoReqs []*http.Request
	mints   atomic.Int32
}

func Fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := fixtures.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ServeFixture(t testing.TB, name string) http.HandlerFunc {
	b := Fixture(t, name)
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	}
}

// New returns a fake serving the anonymous endpoints and a token mint that
// rotates the session cookie on every call.
func New(t testing.TB) *Fake {
	f := &Fake{
		Routes: map[string]http.HandlerFunc{
			"GET /search":             ServeFixture(t, "search.json"),
			"GET /items/{id}":         ServeFixture(t, "item.json"),
			"GET /users/{id}":         ServeFixture(t, "user.json"),
			"GET /users/{id}/stats":   ServeFixture(t, "stats.json"),
			"GET /users/{id}/reviews": ServeFixture(t, "reviews.json"),
			"GET /categories":         ServeFixture(t, "categories.json"),
			"GET /item/{slug}":        ServeFixture(t, "item_page.html"),
			"GET /user/{slug}":        ServeFixture(t, "user_page.html"),
		},
		GeoRoutes: map[string]http.HandlerFunc{
			"GET /search": func(w http.ResponseWriter, r *http.Request) {
				if strings.EqualFold(r.URL.Query().Get("q"), "girona") {
					w.Write([]byte(GironaJSON))
					return
				}
				w.Write([]byte("[]"))
			},
		},
	}
	f.Routes["GET /api/auth/session"] = func(w http.ResponseWriter, _ *http.Request) {
		n := f.mints.Add(1)
		http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: fmt.Sprintf("rotated%d", n)})
		json.NewEncoder(w).Encode(map[string]string{"token": JWT(time.Now().Add(5*time.Minute), "myhash")})
	}
	return f
}

// Start serves the routes and returns the Wallapop and Nominatim base URLs.
func (f *Fake) Start(t testing.TB) (wallapopURL, geoURL string) {
	return f.serve(t, &f.reqs, f.Routes), f.serve(t, &f.geoReqs, f.GeoRoutes)
}

func (f *Fake) serve(t testing.TB, dst *[]*http.Request, routes map[string]http.HandlerFunc) string {
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
	return s.URL
}

// Requests returns the recorded Wallapop requests, all of them when path is
// empty, else those for path.
func (f *Fake) Requests(path string) []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*http.Request
	for _, r := range f.reqs {
		if path == "" || r.URL.Path == path {
			out = append(out, r)
		}
	}
	return out
}

func (f *Fake) GeoRequests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.geoReqs)
}

func (f *Fake) LastQuery(t testing.TB, path string) url.Values {
	t.Helper()
	rs := f.Requests(path)
	if len(rs) == 0 {
		t.Fatalf("no request to %s", path)
	}
	return rs[len(rs)-1].URL.Query()
}

// Mints counts token mints.
func (f *Fake) Mints() int { return len(f.Requests("/api/auth/session")) }

// JWT builds an unsigned token carrying the claims the client reads.
func JWT(exp time.Time, hash string) string {
	enc := base64.RawURLEncoding.EncodeToString
	payload, _ := json.Marshal(map[string]any{"exp": exp.Unix(), "hashed_sub": hash})
	return enc([]byte(`{"alg":"none"}`)) + "." + enc(payload) + ".sig"
}
