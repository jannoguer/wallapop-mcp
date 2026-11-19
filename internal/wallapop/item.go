package wallapop

import (
	"context"
	"errors"
	"fmt"
	"net/url"
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
	// Set by Client.Item only.
	ShippingOptions []ShippingOption `json:"shipping_options,omitempty"`
	DeliveryDays    string           `json:"delivery_days,omitempty" jsonschema:"estimated shipping time, e.g. 3-7"`
	Error           string           `json:"error,omitempty" jsonschema:"why shipping options are missing"`
}

type ShippingOption struct {
	Method  string  `json:"method" jsonschema:"CARRIER_OFFICE: buyer collects at a pickup point; BUYER_ADDRESS: home delivery"`
	Carrier string  `json:"carrier"`
	Cost    float64 `json:"cost" jsonschema:"paid by the buyer in EUR, before Wallapop's buyer protection fee"`
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

// itemByID fetches a listing by id, without shipping options.
func (c *Client) itemByID(ctx context.Context, id string) (ItemDetail, error) {
	r, err := c.fetchItem(ctx, id)
	if err != nil {
		return ItemDetail{}, err
	}
	return toItemDetail(r), nil
}

func (c *Client) fetchItem(ctx context.Context, id string) (rawItem, error) {
	if id == "" {
		return rawItem{}, errors.New("id is required")
	}
	var r rawItem
	err := c.get(ctx, "/items/"+url.PathEscape(id), nil, &r)
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

// Item fetches a listing by id, link or slug. A shippable item gets its
// shipping options from the listing's web page.
func (c *Client) Item(ctx context.Context, ref string) (ItemDetail, error) {
	key := refKey(ref, "item")
	var page itemPage
	if isSlug(key) {
		if err := c.page(ctx, "/item/"+url.PathEscape(key), &page); err != nil {
			return ItemDetail{}, err
		}
		if page.Item.ID == "" {
			return ItemDetail{}, fmt.Errorf("listing %q not found", key)
		}
		key = page.Item.ID
	}
	r, err := c.fetchItem(ctx, key)
	if err != nil {
		return ItemDetail{}, err
	}
	d := toItemDetail(r)
	if !d.Shippable {
		return d, nil
	}
	if page.Item.ID == "" {
		if err := c.page(ctx, "/item/"+url.PathEscape(r.Slug), &page); err != nil {
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
