// Package wallapop is a client for Wallapop's private web API: anonymous
// search and lookups, and account management with a web session cookie.
package wallapop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jannoguer/wallapop-mcp/internal/geo"
	"github.com/jannoguer/wallapop-mcp/internal/httpx"
)

const (
	apiURL  = "https://api.wallapop.com/api/v3"
	rootURL = "https://api.wallapop.com"
	webURL  = "https://es.wallapop.com"

	DefaultMinInterval = 250 * time.Millisecond
	requestTimeout     = 20 * time.Second
)

type Config struct {
	MinInterval time.Duration // minimum gap between Wallapop requests
	DefaultCity string        // search origin when a call names no city
	Session     string        // web session cookie value; empty means anonymous
	SessionFile string        // where the rotated cookie persists; empty disables
}

// Client is safe for concurrent use. The URL fields exist for tests.
type Client struct {
	BaseURL   string // API v3 prefix
	RootURL   string // host root, for /bff and /sdui endpoints
	WebURL    string // web app: pages, and the session cookie to token exchange
	PubNubURL string // chat message delivery
	Geo       *geo.Geocoder

	http        *httpx.Client
	defaultCity string
	sess        *session // nil when anonymous

	catsMu sync.Mutex
	cats   []Category
}

func New(cfg Config) (*Client, error) {
	c := &Client{
		BaseURL:     apiURL,
		RootURL:     rootURL,
		WebURL:      webURL,
		PubNubURL:   pubnubURL,
		Geo:         geo.New(),
		http:        httpx.New(requestTimeout, cfg.MinInterval),
		defaultCity: cfg.DefaultCity,
	}
	if cfg.Session != "" {
		s, err := newSession(cfg.Session, cfg.SessionFile)
		if err != nil {
			return nil, err
		}
		c.sess = s
	}
	return c, nil
}

// HasSession reports whether account methods are available.
func (c *Client) HasSession() bool { return c.sess != nil }

func baseHeader() http.Header {
	// Without X-DeviceOS the CDN answers 403.
	return http.Header{"X-DeviceOS": {"0"}, "Accept": {"application/json"}}
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	_, err := c.http.Do(ctx, http.MethodGet, u, baseHeader(), nil, out)
	return err
}

// page decodes the data a web app page is rendered from (Next.js
// __NEXT_DATA__ props.pageProps) into out. Some data, such as shipping
// costs, is only available there. Pages are addressed by slug; ids get 404.
func (c *Client) page(ctx context.Context, path string, out any) error {
	u := c.WebURL + path
	var html string
	if _, err := c.http.Do(ctx, http.MethodGet, u, http.Header{"Accept": {"text/html"}}, nil, &html); err != nil {
		return err
	}
	_, data, ok := strings.Cut(html, `<script id="__NEXT_DATA__"`)
	if ok {
		_, data, ok = strings.Cut(data, ">")
	}
	if ok {
		data, _, ok = strings.Cut(data, "</script>")
	}
	if !ok {
		return fmt.Errorf("GET %s: no page data, possibly a captcha or block page", u)
	}
	var d struct {
		Props struct {
			PageProps json.RawMessage `json:"pageProps"`
		} `json:"props"`
	}
	if err := json.Unmarshal([]byte(data), &d); err != nil {
		return fmt.Errorf("GET %s: decode page data: %w", u, err)
	}
	if err := json.Unmarshal(d.Props.PageProps, out); err != nil {
		return fmt.Errorf("GET %s: decode page props: %w", u, err)
	}
	return nil
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
		rh, err := c.http.Do(ctx, method, u, h, body, out)
		var he *httpx.Error
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
