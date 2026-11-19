package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	nominatimBaseURL = "https://nominatim.openstreetmap.org"
	// Nominatim's usage policy requires an identifying User-Agent.
	userAgent = "wallapop-mcp/0.1 (+https://github.com/jannoguer/wallapop-mcp)"
)

type Place struct {
	Name string  `json:"name" jsonschema:"resolved place name"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

type Geocoder struct {
	HTTP    *http.Client
	BaseURL string
	gate    *gate

	mu    sync.Mutex
	cache map[string]Place
}

func NewGeocoder() *Geocoder {
	return &Geocoder{
		HTTP:    &http.Client{Timeout: 15 * time.Second},
		BaseURL: nominatimBaseURL,
		gate:    &gate{interval: time.Second}, // Nominatim policy: max 1 req/s
		cache:   map[string]Place{},
	}
}

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
		"countrycodes": {"es,it,pt"}, // the countries Wallapop operates in
		"format":       {"jsonv2"},
		"limit":        {"1"},
	}
	var res []struct {
		Lat         string `json:"lat"`
		Lon         string `json:"lon"`
		DisplayName string `json:"display_name"`
	}
	h := http.Header{"User-Agent": {userAgent}}
	if _, err := send(ctx, g.HTTP, g.gate, http.MethodGet, g.BaseURL+"/search?"+q.Encode(), h, nil, &res); err != nil {
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
