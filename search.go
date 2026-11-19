package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

type SearchParams struct {
	Query         string   `json:"query,omitempty" jsonschema:"keywords to search for"`
	City          string   `json:"city,omitempty" jsonschema:"city to search around (Spain, Italy or Portugal); defaults to the server's WALLAPOP_CITY"`
	RadiusKm      float64  `json:"radius_km,omitempty" jsonschema:"max distance from the city in km"`
	CategoryID    int      `json:"category_id,omitempty" jsonschema:"category id at any depth, from list_categories; filters out accessories and unrelated items"`
	MinPrice      float64  `json:"min_price,omitempty" jsonschema:"minimum price in EUR"`
	MaxPrice      float64  `json:"max_price,omitempty" jsonschema:"maximum price in EUR"`
	Conditions    []string `json:"conditions,omitempty" jsonschema:"any of: un_opened, new, as_good_as_new, good, fair, has_given_it_all"`
	PostedWithin  string   `json:"posted_within,omitempty" jsonschema:"one of: today, week, month"`
	Sort          string   `json:"sort,omitempty" jsonschema:"one of: relevance (default), closest, newest, price_asc, price_desc"`
	ShippableOnly bool     `json:"shippable_only,omitempty" jsonschema:"only items the seller ships"`
	Cursor        string   `json:"cursor,omitempty" jsonschema:"next_cursor from a previous call; continues that search, other filters are ignored"`
}

type Listing struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Price       float64  `json:"price"`
	Currency    string   `json:"currency"`
	City        string   `json:"city"`
	DistanceKm  *float64 `json:"distance_km,omitempty"`
	PostedAt    string   `json:"posted_at"`
	Reserved    bool     `json:"reserved"`
	Shippable   bool     `json:"shippable"`
	Refurbished bool     `json:"refurbished"`
	Promoted    bool     `json:"promoted,omitempty" jsonschema:"the seller paid to bump it; Wallapop mixes bumped listings into results"`
	TopProfile  bool     `json:"top_profile,omitempty" jsonschema:"seller has Wallapop's top profile badge"`
	Warranty    bool     `json:"warranty,omitempty"`
	CategoryID  int      `json:"category_id"`
	SellerID    string   `json:"seller_id"`
	URL         string   `json:"url"`
	Description string   `json:"description"`
}

type SearchResult struct {
	Location   *Place    `json:"location,omitempty" jsonschema:"search origin; absent means Wallapop used the server's IP location"`
	Listings   []Listing `json:"listings"`
	NextCursor string    `json:"next_cursor,omitempty" jsonschema:"pass as cursor to get the next page of 40"`
}

var (
	sortValues = map[string]string{
		"relevance":  "most_relevance",
		"closest":    "closest",
		"newest":     "newest",
		"price_asc":  "price_low_to_high",
		"price_desc": "price_high_to_low",
	}
	postedWithinValues = map[string]string{
		"today": "today",
		"week":  "lastWeek",
		"month": "lastMonth",
	}
	conditionValues = map[string]string{
		"un_opened": "un_opened", "new": "new", "as_good_as_new": "as_good_as_new",
		"good": "good", "fair": "fair", "has_given_it_all": "has_given_it_all",
	}
)

const (
	itemURLPrefix       = "https://wallapop.com/item/"
	listingDescMaxRunes = 300
)

func mapEnum(field, v string, values map[string]string) (string, error) {
	if api, ok := values[v]; ok {
		return api, nil
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return "", fmt.Errorf("invalid %s %q, want one of: %s", field, v, strings.Join(keys, ", "))
}

func formatFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// buildQuery validates p and maps it to Wallapop's query params. cat is the
// resolved category for p.CategoryID, or nil.
func buildQuery(p SearchParams, origin *Place, cat *Category) (url.Values, error) {
	// The API answers 400 without source.
	q := url.Values{"source": {"search_box"}}
	if p.Cursor != "" {
		q.Set("next_page", p.Cursor)
		return q, nil
	}
	if p.RadiusKm < 0 || p.MinPrice < 0 || p.MaxPrice < 0 {
		return nil, errors.New("radius_km, min_price and max_price must not be negative")
	}
	if p.MaxPrice > 0 && p.MinPrice > p.MaxPrice {
		return nil, errors.New("min_price is greater than max_price")
	}
	if p.Query != "" {
		q.Set("keywords", p.Query)
	}
	if origin != nil {
		q.Set("latitude", formatFloat(origin.Lat))
		q.Set("longitude", formatFloat(origin.Lon))
	}
	if p.RadiusKm > 0 {
		if origin == nil {
			return nil, errors.New("radius_km needs a city (or WALLAPOP_CITY)")
		}
		// The API ignores the older "distance" param.
		q.Set("distance_in_km", formatFloat(p.RadiusKm))
	}
	if cat != nil {
		// A subcategory id passed as category_id matches nothing.
		q.Set("category_id", strconv.Itoa(cat.RootID))
		if cat.ID != cat.RootID {
			q.Set("subcategory_ids", strconv.Itoa(cat.ID))
		}
	}
	if p.MinPrice > 0 {
		q.Set("min_sale_price", formatFloat(p.MinPrice))
	}
	if p.MaxPrice > 0 {
		q.Set("max_sale_price", formatFloat(p.MaxPrice))
	}
	if len(p.Conditions) > 0 {
		conds := make([]string, len(p.Conditions))
		for i, c := range p.Conditions {
			v, err := mapEnum("condition", c, conditionValues)
			if err != nil {
				return nil, err
			}
			conds[i] = v
		}
		q.Set("condition", strings.Join(conds, ","))
	}
	if p.PostedWithin != "" {
		v, err := mapEnum("posted_within", p.PostedWithin, postedWithinValues)
		if err != nil {
			return nil, err
		}
		q.Set("time_filter", v)
	}
	if p.Sort != "" {
		v, err := mapEnum("sort", p.Sort, sortValues)
		if err != nil {
			return nil, err
		}
		q.Set("order_by", v)
	}
	if p.ShippableOnly {
		q.Set("is_shippable", "true")
	}
	return q, nil
}

type flag struct {
	Flag bool `json:"flag"`
}

type rawLocation struct {
	Latitude   float64 `json:"latitude"`
	Longitude  float64 `json:"longitude"`
	City       string  `json:"city"`
	PostalCode string  `json:"postal_code"`
}

type rawPrice struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

type rawShipping struct {
	ItemIsShippable    bool `json:"item_is_shippable"`
	UserAllowsShipping bool `json:"user_allows_shipping"`
}

type rawListing struct {
	ID            string      `json:"id"`
	UserID        string      `json:"user_id"`
	Title         string      `json:"title"`
	Description   string      `json:"description"`
	CategoryID    int         `json:"category_id"`
	Price         rawPrice    `json:"price"`
	Location      rawLocation `json:"location"`
	Reserved      flag        `json:"reserved"`
	Shipping      rawShipping `json:"shipping"`
	WebSlug       string      `json:"web_slug"`
	CreatedAt     int64       `json:"created_at"`
	IsRefurbished flag        `json:"is_refurbished"`
	IsTopProfile  flag        `json:"is_top_profile"`
	HasWarranty   flag        `json:"has_warranty"`
	Bump          struct {
		Type string `json:"type"`
	} `json:"bump"`
}

type rawSearch struct {
	Data struct {
		Section struct {
			Payload struct {
				Items []rawListing `json:"items"`
			} `json:"payload"`
		} `json:"section"`
	} `json:"data"`
	Meta struct {
		NextPage string `json:"next_page"`
	} `json:"meta"`
}

func (a *App) Search(ctx context.Context, p SearchParams) (SearchResult, error) {
	origin, err := a.origin(ctx, p.City)
	if err != nil {
		return SearchResult{}, err
	}
	var cat *Category
	if p.CategoryID != 0 && p.Cursor == "" {
		list, err := a.cats.all(ctx, a.wp)
		if err != nil {
			return SearchResult{}, err
		}
		c, err := findCategory(list, p.CategoryID)
		if err != nil {
			return SearchResult{}, err
		}
		cat = &c
	}
	q, err := buildQuery(p, origin, cat)
	if err != nil {
		return SearchResult{}, err
	}
	var raw rawSearch
	if err := a.wp.get(ctx, "/search", q, &raw); err != nil {
		return SearchResult{}, err
	}
	res := SearchResult{Location: origin, Listings: []Listing{}, NextCursor: raw.Meta.NextPage}
	for _, r := range raw.Data.Section.Payload.Items {
		res.Listings = append(res.Listings, toListing(r, origin))
	}
	return res, nil
}

func (a *App) origin(ctx context.Context, city string) (*Place, error) {
	if city == "" {
		city = a.defaultCity
	}
	if city == "" {
		return nil, nil
	}
	p, err := a.geo.Locate(ctx, city)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func toListing(r rawListing, origin *Place) Listing {
	l := Listing{
		ID:          r.ID,
		Title:       r.Title,
		Price:       r.Price.Amount,
		Currency:    r.Price.Currency,
		City:        r.Location.City,
		PostedAt:    msToTime(r.CreatedAt),
		Reserved:    r.Reserved.Flag,
		Shippable:   r.Shipping.ItemIsShippable && r.Shipping.UserAllowsShipping,
		Refurbished: r.IsRefurbished.Flag,
		Promoted:    r.Bump.Type != "" && r.Bump.Type != "none",
		TopProfile:  r.IsTopProfile.Flag,
		Warranty:    r.HasWarranty.Flag,
		CategoryID:  r.CategoryID,
		SellerID:    r.UserID,
		URL:         itemURLPrefix + r.WebSlug,
		Description: truncateRunes(r.Description, listingDescMaxRunes),
	}
	if origin != nil {
		d := math.Round(haversineKm(origin.Lat, origin.Lon, r.Location.Latitude, r.Location.Longitude)*10) / 10
		l.DistanceKm = &d
	}
	return l
}

func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat, dLon := rad(lat2-lat1), rad(lon2-lon1)
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(rad(lat1))*math.Cos(rad(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusKm * math.Asin(math.Sqrt(h))
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

func msToTime(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

func secToTime(s int64) string {
	if s == 0 {
		return ""
	}
	return time.Unix(s, 0).UTC().Format(time.RFC3339)
}
