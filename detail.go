package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

const (
	userURLPrefix  = "https://wallapop.com/user/"
	defaultReviews = 5
	maxReviews     = 20
)

type ItemDetail struct {
	ID            string            `json:"id"`
	Title         string            `json:"title"`
	Description   string            `json:"description"`
	Price         float64           `json:"price"`
	Currency      string            `json:"currency"`
	Condition     string            `json:"condition,omitempty" jsonschema:"un_opened, new, as_good_as_new, good, fair or has_given_it_all"`
	Attributes    map[string]string `json:"attributes,omitempty" jsonschema:"category-specific attributes such as brand, model, storage_capacity, size"`
	Categories    []string          `json:"categories"`
	City          string            `json:"city"`
	PostalCode    string            `json:"postal_code"`
	Shippable     bool              `json:"shippable"`
	Views         int               `json:"views"`
	Favorites     int               `json:"favorites"`
	Conversations int               `json:"conversations" jsonschema:"chats buyers opened with the seller about this item"`
	UpdatedAt     string            `json:"updated_at"`
	SellerID      string            `json:"seller_id"`
	URL           string            `json:"url"`
	Images        []string          `json:"images"`
	// Set by LookupItem only.
	ShippingOptions []ShippingOption `json:"shipping_options,omitempty"`
	DeliveryDays    string           `json:"delivery_days,omitempty" jsonschema:"estimated shipping time, e.g. 3-7"`
	Error           string           `json:"error,omitempty" jsonschema:"why shipping options are missing"`
}

type ShippingOption struct {
	Method  string  `json:"method" jsonschema:"CARRIER_OFFICE: buyer collects at a pickup point; BUYER_ADDRESS: home delivery"`
	Carrier string  `json:"carrier"`
	Cost    float64 `json:"cost" jsonschema:"paid by the buyer in EUR, before Wallapop's buyer protection fee"`
}

type Seller struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Type            string   `json:"type,omitempty" jsonschema:"Private or Professional; absent on older accounts"`
	Verified        bool     `json:"verified"`
	MemberSince     string   `json:"member_since"`
	City            string   `json:"city"`
	URL             string   `json:"url"`
	Rating          float64  `json:"rating" jsonschema:"average review rating, 0-5"`
	Reviews         int      `json:"reviews"`
	Sales           int      `json:"sales"`
	Purchases       int      `json:"purchases"`
	ActiveListings  int      `json:"active_listings"`
	ReportsReceived int      `json:"reports_received" jsonschema:"times other users reported this seller"`
	RecentReviews   []Review `json:"recent_reviews,omitempty"`
	// Set by LookupSeller only.
	Shipments *int   `json:"shipments,omitempty" jsonschema:"completed Wallapop shipments"`
	Error     string `json:"error,omitempty" jsonschema:"why shipments is missing"`
}

type Review struct {
	Date  string `json:"date"`
	Stars int    `json:"stars"`
	Item  string `json:"item"`
	Role  string `json:"role" jsonschema:"sell: the reviewed user was the seller; buy: the buyer"`
}

type rawText struct {
	Original string `json:"original"`
}

type rawItem struct {
	ID          string  `json:"id"`
	Slug        string  `json:"slug"`
	Title       rawText `json:"title"`
	Description rawText `json:"description"`
	User        struct {
		ID string `json:"id"`
	} `json:"user"`
	Taxonomy []struct {
		Name string `json:"name"`
	} `json:"taxonomy"`
	ShareURL     string `json:"share_url"`
	ModifiedDate int64  `json:"modified_date"`
	Images       []struct {
		URLs struct {
			Big string `json:"big"`
		} `json:"urls"`
	} `json:"images"`
	Price struct {
		Cash rawPrice `json:"cash"`
	} `json:"price"`
	Location       rawLocation `json:"location"`
	TypeAttributes map[string]struct {
		Value string `json:"value"`
		Text  string `json:"text"`
	} `json:"type_attributes"`
	Shipping rawShipping `json:"shipping"`
	Counters struct {
		Views         int `json:"views"`
		Favorites     int `json:"favorites"`
		Conversations int `json:"conversations"`
	} `json:"counters"`
}

func (a *App) GetItem(ctx context.Context, id string) (ItemDetail, error) {
	r, err := a.fetchItem(ctx, id)
	if err != nil {
		return ItemDetail{}, err
	}
	return toItemDetail(r), nil
}

func (a *App) fetchItem(ctx context.Context, id string) (rawItem, error) {
	if id == "" {
		return rawItem{}, errors.New("id is required")
	}
	var r rawItem
	err := a.wp.get(ctx, "/items/"+url.PathEscape(id), nil, &r)
	return r, err
}

func toItemDetail(r rawItem) ItemDetail {
	d := ItemDetail{
		ID:            r.ID,
		Title:         r.Title.Original,
		Description:   r.Description.Original,
		Price:         r.Price.Cash.Amount,
		Currency:      r.Price.Cash.Currency,
		Categories:    []string{},
		City:          r.Location.City,
		PostalCode:    r.Location.PostalCode,
		Shippable:     r.Shipping.ItemIsShippable && r.Shipping.UserAllowsShipping,
		Views:         r.Counters.Views,
		Favorites:     r.Counters.Favorites,
		Conversations: r.Counters.Conversations,
		UpdatedAt:     secToTime(r.ModifiedDate),
		SellerID:      r.User.ID,
		URL:           r.ShareURL,
		Images:        []string{},
	}
	for _, t := range r.Taxonomy {
		d.Categories = append(d.Categories, t.Name)
	}
	for _, img := range r.Images {
		d.Images = append(d.Images, img.URLs.Big)
	}
	for k, v := range r.TypeAttributes {
		if k == "condition" {
			d.Condition = v.Value
			continue
		}
		if d.Attributes == nil {
			d.Attributes = map[string]string{}
		}
		d.Attributes[k] = v.Text
	}
	return d
}

type itemPage struct {
	Item struct {
		ID string `json:"id"`
	} `json:"item"`
	Delivery struct {
		Options []struct {
			Method  string   `json:"method"`
			Carrier string   `json:"carrier"`
			Cost    rawPrice `json:"cost"`
		} `json:"deliveryOptions"`
		Time *struct {
			From int `json:"from"`
			To   int `json:"to"`
		} `json:"deliveryTime"`
	} `json:"itemDeliveryInfo"`
}

// LookupItem backs get_item: ref may also be a listing link or slug, and a
// shippable item gets its shipping options from the listing's web page.
func (a *App) LookupItem(ctx context.Context, ref string) (ItemDetail, error) {
	key := refKey(ref, "item")
	var page itemPage
	if isSlug(key) {
		if err := a.wp.page(ctx, "/item/"+url.PathEscape(key), &page); err != nil {
			return ItemDetail{}, err
		}
		if page.Item.ID == "" {
			return ItemDetail{}, fmt.Errorf("listing %q not found", key)
		}
		key = page.Item.ID
	}
	r, err := a.fetchItem(ctx, key)
	if err != nil {
		return ItemDetail{}, err
	}
	d := toItemDetail(r)
	if !d.Shippable {
		return d, nil
	}
	if page.Item.ID == "" {
		if err := a.wp.page(ctx, "/item/"+url.PathEscape(r.Slug), &page); err != nil {
			d.Error = "shipping options: " + err.Error()
			return d, nil
		}
	}
	for _, o := range page.Delivery.Options {
		d.ShippingOptions = append(d.ShippingOptions, ShippingOption{Method: o.Method, Carrier: o.Carrier, Cost: o.Cost.Amount})
	}
	if t := page.Delivery.Time; t != nil {
		d.DeliveryDays = fmt.Sprintf("%d-%d", t.From, t.To)
	}
	return d, nil
}

type rawUser struct {
	ID           string `json:"id"`
	MicroName    string `json:"micro_name"`
	WebSlug      string `json:"web_slug"`
	RegisterDate int64  `json:"register_date"`
	Location     struct {
		City string `json:"city"`
	} `json:"location"`
	SellerType struct {
		Type     string `json:"type"`
		Verified bool   `json:"verified"`
	} `json:"seller_type"`
}

type rawStats struct {
	RatingAverage float64 `json:"rating_average"`
	Counters      []struct {
		Type  string `json:"type"`
		Value int    `json:"value"`
	} `json:"counters"`
}

type rawReview struct {
	Item struct {
		Title string `json:"title"`
	} `json:"item"`
	Review struct {
		Date           int64 `json:"date"`
		RatingOverFive int   `json:"rating_over_five"`
	} `json:"review"`
	Type string `json:"type"`
}

func (a *App) GetSeller(ctx context.Context, id string, reviews int) (Seller, error) {
	if id == "" {
		return Seller{}, errors.New("id is required")
	}
	if reviews < 0 || reviews > maxReviews {
		return Seller{}, errors.New("reviews must be between 0 and 20")
	}
	base := "/users/" + url.PathEscape(id)
	var u rawUser
	if err := a.wp.get(ctx, base, nil, &u); err != nil {
		return Seller{}, err
	}
	var st rawStats
	if err := a.wp.get(ctx, base+"/stats", nil, &st); err != nil {
		return Seller{}, err
	}
	s := Seller{
		ID:          u.ID,
		Name:        u.MicroName,
		Type:        u.SellerType.Type,
		Verified:    u.SellerType.Verified,
		MemberSince: msToTime(u.RegisterDate),
		City:        u.Location.City,
		URL:         userURLPrefix + u.WebSlug,
		Rating:      st.RatingAverage,
	}
	for _, c := range st.Counters {
		switch c.Type {
		case "reviews":
			s.Reviews = c.Value
		case "sells":
			s.Sales = c.Value
		case "buys":
			s.Purchases = c.Value
		case "publish":
			s.ActiveListings = c.Value
		case "reports_received":
			s.ReportsReceived = c.Value
		}
	}
	if reviews == 0 {
		return s, nil
	}
	var rr []rawReview
	if err := a.wp.get(ctx, base+"/reviews", nil, &rr); err != nil {
		return Seller{}, err
	}
	slices.SortFunc(rr, func(x, y rawReview) int { return cmp.Compare(y.Review.Date, x.Review.Date) })
	for _, r := range rr[:min(reviews, len(rr))] {
		s.RecentReviews = append(s.RecentReviews, Review{
			Date:  msToTime(r.Review.Date),
			Stars: r.Review.RatingOverFive,
			Item:  r.Item.Title,
			Role:  r.Type,
		})
	}
	return s, nil
}

type userPage struct {
	User struct {
		ID string `json:"id"`
	} `json:"user"`
	ShippingCounter struct {
		SucceededCount int `json:"succeededCount"`
	} `json:"shippingCounter"`
}

// LookupSeller backs get_seller: ref may also be a profile link or slug, and
// the result adds the shipment count from the profile's web page.
func (a *App) LookupSeller(ctx context.Context, ref string, reviews int) (Seller, error) {
	key := refKey(ref, "user")
	var page userPage
	if isSlug(key) {
		if err := a.wp.page(ctx, "/user/"+url.PathEscape(key), &page); err != nil {
			return Seller{}, err
		}
		if page.User.ID == "" {
			return Seller{}, fmt.Errorf("seller %q not found", key)
		}
		key = page.User.ID
	}
	s, err := a.GetSeller(ctx, key, reviews)
	if err != nil {
		return Seller{}, err
	}
	if page.User.ID == "" {
		slug := strings.TrimPrefix(s.URL, userURLPrefix)
		if err := a.wp.page(ctx, "/user/"+url.PathEscape(slug), &page); err != nil {
			s.Error = "shipments: " + err.Error()
			return s, nil
		}
	}
	s.Shipments = &page.ShippingCounter.SucceededCount
	return s, nil
}

// refKey extracts the id or slug from ref, which may be a Wallapop link such
// as https://es.wallapop.com/<kind>/<slug>?x=y.
func refKey(ref, kind string) string {
	ref = strings.TrimSpace(ref)
	if _, after, ok := strings.Cut(ref, "/"+kind+"/"); ok {
		ref = after
	}
	if i := strings.IndexAny(ref, "/?#"); i >= 0 {
		ref = ref[:i]
	}
	return ref
}

// isSlug tells slugs ("iphone-13-1311621547") from ids ("ejkml3e23yzx").
func isSlug(key string) bool { return strings.Contains(key, "-") }
