// Package httpx sends rate-limited HTTP requests and decodes their responses.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	errorBodyMax      = 300
	defaultRetryAfter = 2 * time.Second
	maxRetryAfter     = 10 * time.Second
)

// Error is a non-2xx response.
type Error struct {
	Method string
	URL    string
	Status int
	Body   string // first bytes of the response body
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.URL, e.Status, e.Body)
}

// Client sends requests at least a minimum interval apart, across goroutines.
type Client struct {
	HTTP *http.Client
	gate gate
}

func New(timeout, minInterval time.Duration) *Client {
	return &Client{HTTP: &http.Client{Timeout: timeout}, gate: gate{interval: minInterval}}
}

// Do performs one request, retried once after a 429. out may be nil (body
// discarded), a *string (raw body, trimmed) or anything json.Unmarshal
// accepts; empty bodies are not decoded. It returns the response headers.
func (c *Client) Do(ctx context.Context, method, url string, header http.Header, body []byte, out any) (http.Header, error) {
	resp, err := c.once(ctx, method, url, header, body)
	if err == nil && resp.StatusCode == http.StatusTooManyRequests {
		resp.Body.Close()
		if err := sleep(ctx, retryAfter(resp.Header.Get("Retry-After"))); err != nil {
			return nil, err
		}
		resp, err = c.once(ctx, method, url, header, body)
	}
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyMax))
		return resp.Header, &Error{Method: method, URL: url, Status: resp.StatusCode, Body: string(b)}
	}
	if out == nil {
		return resp.Header, nil
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s %s: read: %w", method, url, err)
	}
	if s, ok := out.(*string); ok {
		*s = strings.TrimSpace(string(b))
		return resp.Header, nil
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return resp.Header, nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return nil, fmt.Errorf("%s %s: decode: %w", method, url, err)
	}
	return resp.Header, nil
}

func (c *Client) once(ctx context.Context, method, url string, header http.Header, body []byte) (*http.Response, error) {
	if err := c.gate.wait(ctx); err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return nil, err
	}
	req.Header = header
	return c.HTTP.Do(req)
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

	return sleep(ctx, time.Until(slot))
}

func sleep(ctx context.Context, d time.Duration) error {
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

// retryAfter reads a Retry-After header given in seconds, capped so a tool
// call never stalls for long.
func retryAfter(v string) time.Duration {
	s, err := strconv.Atoi(v)
	if err != nil || s < 0 {
		return defaultRetryAfter
	}
	return min(time.Duration(s)*time.Second, maxRetryAfter)
}
