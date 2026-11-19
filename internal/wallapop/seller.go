package wallapop

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
	userURLPrefix = "https://wallapop.com/user/"
	maxReviews    = 20
)

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
	// Set by Client.Seller only.
	Shipments *int   `json:"shipments,omitempty" jsonschema:"completed Wallapop shipments"`
	Error     string `json:"error,omitempty" jsonschema:"why shipments is missing"`
}

type Review struct {
	Date  string `json:"date"`
	Stars int    `json:"stars"`
	Item  string `json:"item"`
	Role  string `json:"role" jsonschema:"sell: the reviewed user was the seller; buy: the buyer"`
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

// sellerByID fetches a profile by id, without the shipment count.
func (c *Client) sellerByID(ctx context.Context, id string, reviews int) (Seller, error) {
	if id == "" {
		return Seller{}, errors.New("id is required")
	}
	if reviews < 0 || reviews > maxReviews {
		return Seller{}, errors.New("reviews must be between 0 and 20")
	}
	base := "/users/" + url.PathEscape(id)
	var u rawUser
	if err := c.get(ctx, base, nil, &u); err != nil {
		return Seller{}, err
	}
	var st rawStats
	if err := c.get(ctx, base+"/stats", nil, &st); err != nil {
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
	for _, n := range st.Counters {
		switch n.Type {
		case "reviews":
			s.Reviews = n.Value
		case "sells":
			s.Sales = n.Value
		case "buys":
			s.Purchases = n.Value
		case "publish":
			s.ActiveListings = n.Value
		case "reports_received":
			s.ReportsReceived = n.Value
		}
	}
	if reviews == 0 {
		return s, nil
	}
	var rr []rawReview
	if err := c.get(ctx, base+"/reviews", nil, &rr); err != nil {
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

// Seller fetches a profile by id, link or slug, with reviews recent reviews
// and the shipment count from the profile's web page.
func (c *Client) Seller(ctx context.Context, ref string, reviews int) (Seller, error) {
	key := refKey(ref, "user")
	var page userPage
	if isSlug(key) {
		if err := c.page(ctx, "/user/"+url.PathEscape(key), &page); err != nil {
			return Seller{}, err
		}
		if page.User.ID == "" {
			return Seller{}, fmt.Errorf("seller %q not found", key)
		}
		key = page.User.ID
	}
	s, err := c.sellerByID(ctx, key, reviews)
	if err != nil {
		return Seller{}, err
	}
	if page.User.ID == "" {
		slug := strings.TrimPrefix(s.URL, userURLPrefix)
		if err := c.page(ctx, "/user/"+url.PathEscape(slug), &page); err != nil {
			s.Error = "shipments: " + err.Error()
			return s, nil
		}
	}
	s.Shipments = &page.ShippingCounter.SucceededCount
	return s, nil
}
