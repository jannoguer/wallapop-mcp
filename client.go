package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	wallapopBaseURL = "https://api.wallapop.com/api/v3"
	wallapopRootURL = "https://api.wallapop.com"
	wallapopWebURL  = "https://es.wallapop.com"
)

type HTTPError struct {
	Method string
	URL    string
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.URL, e.Status, e.Body)
}

// gate spaces calls at least interval apart, across goroutines.
type gate struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func (g *gate) wait(ctx context.Context) error {
	g.mu.Lock()
	now := time.Now()
	slot := g.next
	if slot.Before(now) {
		slot = now
	}
	g.next = slot.Add(g.interval)
	g.mu.Unlock()

	d := time.Until(slot)
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// send performs one rate-limited request. out may be nil (body discarded),
// a *string (raw body) or anything json.Unmarshal accepts; empty bodies are
// not decoded. It returns the response headers.
func send(ctx context.Context, hc *http.Client, g *gate, method, u string, header http.Header, body []byte, out any) (http.Header, error) {
	if err := g.wait(ctx); err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	req.Header = header
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return resp.Header, &HTTPError{Method: method, URL: u, Status: resp.StatusCode, Body: string(b)}
	}
	if out == nil {
		return resp.Header, nil
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s %s: read: %w", method, u, err)
	}
	if s, ok := out.(*string); ok {
		*s = strings.TrimSpace(string(b))
		return resp.Header, nil
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return resp.Header, nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return nil, fmt.Errorf("%s %s: decode: %w", method, u, err)
	}
	return resp.Header, nil
}

type Client struct {
	HTTP    *http.Client
	BaseURL string // API v3 prefix
	RootURL string // host root, for /bff and /sdui endpoints
	WebURL  string // web app, where the session cookie is exchanged for a token
	// PubNub delivers chat messages.
	PubNubURL string
	gate      *gate
	sess      *session // nil when anonymous
}

func NewClient(minInterval time.Duration) *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: 20 * time.Second},
		BaseURL:   wallapopBaseURL,
		RootURL:   wallapopRootURL,
		WebURL:    wallapopWebURL,
		PubNubURL: pubnubURL,
		gate:      &gate{interval: minInterval},
	}
}

func baseHeader() http.Header {
	// Without X-DeviceOS the CDN answers 403.
	return http.Header{"X-DeviceOS": {"0"}, "Accept": {"application/json"}}
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	_, err := send(ctx, c.HTTP, c.gate, http.MethodGet, u, baseHeader(), nil, out)
	return err
}

// authed sends a request as the logged-in user. u is a full URL; header
// entries override the defaults. A 401 re-mints the token and retries once.
func (c *Client) authed(ctx context.Context, method, u string, header http.Header, body []byte, out any) (http.Header, error) {
	if c.sess == nil {
		return nil, errors.New("no Wallapop session configured")
	}
	for attempt := 0; ; attempt++ {
		tok, err := c.token(ctx, attempt > 0)
		if err != nil {
			return nil, err
		}
		h := baseHeader()
		for k, v := range header {
			h[k] = v
		}
		h.Set("Authorization", "Bearer "+tok)
		rh, err := send(ctx, c.HTTP, c.gate, method, u, h, body, out)
		var he *HTTPError
		if attempt == 0 && errors.As(err, &he) && he.Status == http.StatusUnauthorized {
			continue
		}
		return rh, err
	}
}

// authedJSON marshals in (when non-nil) as the request body.
func (c *Client) authedJSON(ctx context.Context, method, u string, in, out any) (http.Header, error) {
	var body []byte
	var h http.Header
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body, h = b, http.Header{"Content-Type": {"application/json"}}
	}
	return c.authed(ctx, method, u, h, body, out)
}
