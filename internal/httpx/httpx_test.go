package httpx

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newClient() *Client { return New(5*time.Second, 0) }

func TestDoBodiesAndStatuses(t *testing.T) {
	var gotMethod, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotMethod, gotBody = r.Method, string(b)
		switch r.URL.Path {
		case "/created":
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"a":2}`))
		case "/empty":
			w.WriteHeader(http.StatusNoContent)
		case "/text":
			w.Write([]byte("photo123\n"))
		case "/gone":
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	ctx, c := context.Background(), newClient()

	var out struct{ A int }
	if _, err := c.Do(ctx, http.MethodPost, srv.URL+"/created", http.Header{}, []byte(`{"x":1}`), &out); err != nil || out.A != 2 {
		t.Errorf("201: %v, %+v", err, out)
	}
	if gotMethod != "POST" || gotBody != `{"x":1}` {
		t.Errorf("sent %s %q", gotMethod, gotBody)
	}
	if _, err := c.Do(ctx, http.MethodPut, srv.URL+"/empty", http.Header{}, nil, &out); err != nil {
		t.Errorf("204 with out: %v", err)
	}
	var s string
	if _, err := c.Do(ctx, http.MethodPost, srv.URL+"/text", http.Header{}, nil, &s); err != nil || s != "photo123" {
		t.Errorf("text body: %v, %q", err, s)
	}
	_, err := c.Do(ctx, http.MethodDelete, srv.URL+"/gone", http.Header{}, nil, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "DELETE ") {
		t.Errorf("error lacks method: %v", err)
	}
}

func TestDoErrors(t *testing.T) {
	tests := []struct {
		status  int
		body    string
		wantErr string
	}{
		{403, "<html>The request could not be satisfied</html>", "HTTP 403: <html>The request could not be satisfied"},
		{400, `{"code":400}`, `HTTP 400: {"code":400}`},
		{500, strings.Repeat("x", 1000), "HTTP 500: " + strings.Repeat("x", errorBodyMax)},
		{200, "not json", "decode"},
	}
	for _, tt := range tests {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tt.status)
			w.Write([]byte(tt.body))
		}))
		var out any
		_, err := newClient().Do(context.Background(), http.MethodGet, srv.URL, http.Header{}, nil, &out)
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("status %d: got %v, want %q", tt.status, err, tt.wantErr)
			continue
		}
		var he *Error
		if tt.status != 200 && (!errors.As(err, &he) || he.Status != tt.status) {
			t.Errorf("status %d: not an *Error: %v", tt.status, err)
		}
		if tt.status == 500 && len(he.Body) != errorBodyMax {
			t.Errorf("error body not capped: %d bytes", len(he.Body))
		}
	}
}

func TestDoTransportError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	var out any
	if _, err := newClient().Do(context.Background(), http.MethodGet, srv.URL, http.Header{}, nil, &out); err == nil {
		t.Error("expected a connection error")
	}
	if _, err := newClient().Do(context.Background(), http.MethodGet, "http://bad host", http.Header{}, nil, &out); err == nil {
		t.Error("expected an invalid URL error")
	}
}

func TestDoRetriesOnce429(t *testing.T) {
	for _, limited := range []int{1, 2} {
		n := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			if string(b) != "body" {
				t.Errorf("request %d body = %q", n, b)
			}
			n++
			if n <= limited {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.Write([]byte("ok"))
		}))
		var out string
		_, err := newClient().Do(context.Background(), http.MethodPost, srv.URL, http.Header{}, []byte("body"), &out)
		srv.Close()
		var he *Error
		switch {
		case n != 2:
			t.Errorf("%d limited: %d requests, want 2", limited, n)
		case limited == 1 && (err != nil || out != "ok"):
			t.Errorf("retry failed: %v %q", err, out)
		case limited == 2 && (!errors.As(err, &he) || he.Status != http.StatusTooManyRequests):
			t.Errorf("second 429 not reported: %v", err)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	tests := map[string]time.Duration{
		"":                              defaultRetryAfter,
		"3":                             3 * time.Second,
		"600":                           maxRetryAfter,
		"-1":                            defaultRetryAfter,
		"Wed, 21 Oct 2026 07:28:00 GMT": defaultRetryAfter,
	}
	for in, want := range tests {
		if got := retryAfter(in); got != want {
			t.Errorf("retryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestGateSpacing(t *testing.T) {
	g := &gate{interval: 40 * time.Millisecond}
	start := time.Now()
	for range 4 {
		if err := g.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d < 120*time.Millisecond {
		t.Errorf("4 calls took %v, want >= 120ms", d)
	}
}

func TestGateCancel(t *testing.T) {
	c := New(time.Second, time.Hour)
	c.gate.wait(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out any
	if _, err := c.Do(ctx, http.MethodGet, "http://127.0.0.1:1", http.Header{}, nil, &out); !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}
