package wallapop

import (
	"bytes"
	"image"
	"image/jpeg"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jannoguer/wallapop-mcp/internal/wallapop/wallapoptest"
)

var (
	newFake      = wallapoptest.New
	fixture      = wallapoptest.Fixture
	serveFixture = wallapoptest.ServeFixture
)

func mustNew(t *testing.T, cfg Config) *Client {
	t.Helper()
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// testClient starts f and returns an anonymous client pointed at it.
func testClient(t *testing.T, f *wallapoptest.Fake, defaultCity string) *Client {
	t.Helper()
	c := mustNew(t, Config{DefaultCity: defaultCity})
	api, geo := f.Start(t)
	c.BaseURL, c.RootURL, c.WebURL, c.PubNubURL, c.Geo.BaseURL = api, api, api, api, geo
	return c
}

// authClient is testClient logged in, without persisting the session.
func authClient(t *testing.T, f *wallapoptest.Fake) *Client {
	t.Helper()
	c := testClient(t, f, "")
	s, err := newSession("seed", "")
	if err != nil {
		t.Fatal(err)
	}
	c.sess = s
	return c
}

// captured is a request and its body, recorded by a fake handler.
type captured struct {
	r    *http.Request
	body []byte
}

type recorder struct {
	mu   sync.Mutex
	reqs []captured
}

func (rec *recorder) handler(status int, resp string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, captured{r, b})
		rec.mu.Unlock()
		w.WriteHeader(status)
		w.Write([]byte(resp))
	}
}

func (rec *recorder) last(t *testing.T) captured {
	t.Helper()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.reqs) == 0 {
		t.Fatal("no request recorded")
	}
	return rec.reqs[len(rec.reqs)-1]
}

func writeJPEG(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// parts parses a multipart request into field name -> content; for the
// image part, its content type.
func parts(t *testing.T, c captured) map[string]string {
	t.Helper()
	_, params, err := mime.ParseMediaType(c.r.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	mr := multipart.NewReader(bytes.NewReader(c.body), params["boundary"])
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p)
		if p.FormName() == "image" {
			out["image"] = p.Header.Get("Content-Type")
			continue
		}
		out[p.FormName()] = string(b)
	}
}
