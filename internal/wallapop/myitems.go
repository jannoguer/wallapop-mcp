package wallapop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
)

type MyItemsParams struct {
	Status string `json:"status,omitempty" jsonschema:"active (default, includes reserved) or sold"`
	Cursor string `json:"cursor,omitempty" jsonschema:"next_cursor from a previous call"`
}

type MyItem struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Price         float64 `json:"price"`
	Reserved      bool    `json:"reserved,omitempty"`
	State         string  `json:"state,omitempty" jsonschema:"set when not live: pending, onhold, banned or expired"`
	Views         int     `json:"views,omitempty"`
	Favorites     int     `json:"favorites,omitempty"`
	Conversations int     `json:"conversations,omitempty"`
	Updated       string  `json:"updated,omitempty"`
}

type SoldItem struct {
	Title  string `json:"title"`
	Price  string `json:"price,omitempty"`
	Detail string `json:"detail,omitempty" jsonschema:"transaction status and date"`
	URL    string `json:"url,omitempty"`
}

// MyItems fills Items for status active and Sold for status sold.
type MyItems struct {
	Items      []MyItem   `json:"items,omitempty"`
	Sold       []SoldItem `json:"sold,omitempty"`
	NextCursor string     `json:"next_cursor,omitempty"`
	Empty      bool       `json:"empty,omitempty" jsonschema:"true when there is nothing to list"`
}

type rawMine []struct {
	ID      string `json:"id"`
	Content struct {
		Title         string          `json:"title"`
		SalePrice     float64         `json:"sale_price"`
		Flags         map[string]bool `json:"flags"`
		ModifiedDate  int64           `json:"modified_date"`
		Views         int             `json:"views"`
		Favorites     int             `json:"favorites"`
		Conversations int             `json:"conversations"`
	} `json:"content"`
}

func (c *Client) MyItems(ctx context.Context, p MyItemsParams) (MyItems, error) {
	switch p.Status {
	case "", "active":
		return c.activeItems(ctx, p.Cursor)
	case "sold":
		return c.soldItems(ctx)
	}
	return MyItems{}, errors.New(`status must be "active" or "sold"`)
}

func (c *Client) activeItems(ctx context.Context, cursor string) (MyItems, error) {
	u := c.BaseURL + "/items/mine/published"
	if cursor != "" {
		u += "?" + url.Values{"since": {cursor}}.Encode()
	}
	var raw rawMine
	h, err := c.authed(ctx, http.MethodGet, u, nil, nil, &raw)
	if err != nil {
		return MyItems{}, err
	}
	out := MyItems{Items: []MyItem{}}
	for _, r := range raw {
		rc := r.Content
		it := MyItem{
			ID: r.ID, Title: rc.Title, Price: rc.SalePrice, Reserved: rc.Flags["reserved"],
			Views: rc.Views, Favorites: rc.Favorites, Conversations: rc.Conversations,
			Updated: msToDate(rc.ModifiedDate),
		}
		for _, f := range []string{"pending", "onhold", "banned", "expired"} {
			if rc.Flags[f] {
				it.State = f
			}
		}
		out.Items = append(out.Items, it)
	}
	out.Empty = len(out.Items) == 0
	// The web reads the next page's cursor from the X-NextPage header ("since=...").
	if q, err := url.ParseQuery(h.Get("X-NextPage")); err == nil {
		out.NextCursor = q.Get("since")
	}
	return out, nil
}

// sduiNode is the subset of Wallapop's server-driven UI tree we read.
type sduiNode struct {
	Type string `json:"type"`
	Data struct {
		Content     json.RawMessage `json:"content"`
		Main        *sduiNode       `json:"main"`
		Detail      *sduiNode       `json:"detail"`
		DetailPrice *sduiNode       `json:"detail_price"`
	} `json:"data"`
	Events struct {
		OnClick struct {
			Data struct {
				URL string `json:"url"`
			} `json:"data"`
		} `json:"on_click"`
	} `json:"events"`
}

func (n *sduiNode) text() string {
	if n == nil {
		return ""
	}
	var s string
	if json.Unmarshal(n.Data.Content, &s) != nil {
		return ""
	}
	return s
}

func (c *Client) soldItems(ctx context.Context) (MyItems, error) {
	var raw any
	if _, err := c.authed(ctx, http.MethodGet, c.RootURL+"/sdui/selling/finished", nil, nil, &raw); err != nil {
		return MyItems{}, err
	}
	out := MyItems{Sold: []SoldItem{}}
	var walk func(v any) error
	walk = func(v any) error {
		switch v := v.(type) {
		case map[string]any:
			if v["type"] == "list_item_transaction" {
				b, err := json.Marshal(v)
				if err != nil {
					return err
				}
				var n sduiNode
				if err := json.Unmarshal(b, &n); err != nil {
					return err
				}
				out.Sold = append(out.Sold, SoldItem{
					Title: n.Data.Main.text(), Price: n.Data.DetailPrice.text(),
					Detail: n.Data.Detail.text(), URL: n.Events.OnClick.Data.URL,
				})
				return nil
			}
			for _, child := range v {
				if err := walk(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range v {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(raw); err != nil {
		return MyItems{}, err
	}
	out.Empty = len(out.Sold) == 0
	return out, nil
}
