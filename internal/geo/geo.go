// Package geo resolves city names to coordinates with OpenStreetMap Nominatim.
package geo

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jannoguer/wallapop-mcp/internal/httpx"
)

const (
	nominatimURL = "https://nominatim.openstreetmap.org"
	// Nominatim's usage policy requires an identifying User-Agent.
	userAgent = "wallapop-mcp/0.1 (+https://github.com/jannoguer/wallapop-mcp)"
)

type Place struct {
	Name string  `json:"name" jsonschema:"resolved place name"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

// Geocoder caches results per process.
type Geocoder struct {
	BaseURL string
	http    *httpx.Client

	mu    sync.Mutex
	cache map[string]Place
}

func New() *Geocoder {
	return &Geocoder{
		BaseURL: nominatimURL,
		http:    httpx.New(15*time.Second, time.Second), // Nominatim policy: max 1 req/s
		cache:   map[string]Place{},
	}
}

// Locate finds city in the countries Wallapop operates in.
func (g *Geocoder) Locate(ctx context.Context, city string) (Place, error) {
	key := strings.ToLower(strings.TrimSpace(city))
	g.mu.Lock()
	p, ok := g.cache[key]
	g.mu.Unlock()
	if ok {
		return p, nil
	}

	q := url.Values{
		"q":            {city},
		"countrycodes": {"es,it,pt"},
		"format":       {"jsonv2"},
		"limit":        {"1"},
	}
	var res []struct {
		Lat         string `json:"lat"`
		Lon         string `json:"lon"`
		DisplayName string `json:"display_name"`
	}
	h := http.Header{"User-Agent": {userAgent}}
	if _, err := g.http.Do(ctx, http.MethodGet, g.BaseURL+"/search?"+q.Encode(), h, nil, &res); err != nil {
		return Place{}, fmt.Errorf("geocode %q: %w", city, err)
	}
	if len(res) == 0 {
		return Place{}, fmt.Errorf("city %q not found in Spain, Italy or Portugal", city)
	}
	lat, err1 := strconv.ParseFloat(res[0].Lat, 64)
	lon, err2 := strconv.ParseFloat(res[0].Lon, 64)
	if err1 != nil || err2 != nil {
		return Place{}, fmt.Errorf("geocode %q: bad coordinates %q,%q", city, res[0].Lat, res[0].Lon)
	}
	p = Place{Name: res[0].DisplayName, Lat: lat, Lon: lon}

	g.mu.Lock()
	g.cache[key] = p
	g.mu.Unlock()
	return p, nil
}
