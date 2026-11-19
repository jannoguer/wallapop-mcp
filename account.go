package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"
)

const (
	defaultInboxSize   = 20
	maxInboxSize       = 50
	defaultThreadSize  = 20
	maxThreadSize      = 100
	inboxTextMaxRunes  = 160
	threadTextMaxRunes = 2000
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

func (a *App) MyItems(ctx context.Context, p MyItemsParams) (MyItems, error) {
	switch p.Status {
	case "", "active":
		return a.activeItems(ctx, p.Cursor)
	case "sold":
		return a.soldItems(ctx)
	}
	return MyItems{}, errors.New(`status must be "active" or "sold"`)
}

func (a *App) activeItems(ctx context.Context, cursor string) (MyItems, error) {
	u := a.wp.BaseURL + "/items/mine/published"
	if cursor != "" {
		u += "?" + url.Values{"since": {cursor}}.Encode()
	}
	var raw rawMine
	h, err := a.wp.authed(ctx, http.MethodGet, u, nil, nil, &raw)
	if err != nil {
		return MyItems{}, err
	}
	out := MyItems{Items: []MyItem{}}
	for _, r := range raw {
		c := r.Content
		it := MyItem{
			ID: r.ID, Title: c.Title, Price: c.SalePrice, Reserved: c.Flags["reserved"],
			Views: c.Views, Favorites: c.Favorites, Conversations: c.Conversations,
			Updated: msToDate(c.ModifiedDate),
		}
		for _, f := range []string{"pending", "onhold", "banned", "expired"} {
			if c.Flags[f] {
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

func (a *App) soldItems(ctx context.Context) (MyItems, error) {
	var raw any
	if _, err := a.wp.authed(ctx, http.MethodGet, a.wp.RootURL+"/sdui/selling/finished", nil, nil, &raw); err != nil {
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
			for _, c := range v {
				if err := walk(c); err != nil {
					return err
				}
			}
		case []any:
			for _, c := range v {
				if err := walk(c); err != nil {
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

type InboxParams struct {
	UnreadOnly bool `json:"unread_only,omitempty"`
	Limit      int  `json:"limit,omitempty" jsonschema:"conversations to return, 1-50 (default 20), most recent first"`
}

type Message struct {
	From string `json:"from" jsonschema:"me or them"`
	Text string `json:"text"`
	Date string `json:"date,omitempty"`
}

type Conversation struct {
	ID       string    `json:"id"`
	ItemID   string    `json:"item_id,omitempty"`
	Item     string    `json:"item,omitempty"`
	With     string    `json:"with,omitempty"`
	Unread   int       `json:"unread,omitempty"`
	Last     *Message  `json:"last,omitempty"`
	Messages []Message `json:"messages,omitempty" jsonschema:"oldest first"`
}

type Inbox struct {
	Unread        int            `json:"unread"`
	Conversations []Conversation `json:"conversations"`
}

type rawMessage struct {
	Text      string `json:"text"`
	Type      string `json:"type"`
	FromSelf  bool   `json:"from_self"`
	Timestamp int64  `json:"timestamp"` // epoch ms
	Payload   struct {
		Text string `json:"text"`
	} `json:"payload"`
}

type rawConversation struct {
	Hash           string `json:"hash"`
	UnreadMessages int    `json:"unread_messages"`
	Channel        string `json:"channel"`
	Item           struct {
		Hash  string `json:"hash"`
		Title string `json:"title"`
	} `json:"item"`
	WithUser struct {
		Hash string `json:"hash"`
		Name string `json:"name"`
	} `json:"with_user"`
	Messages struct {
		Messages []rawMessage `json:"messages"`
	} `json:"messages"`
}

// messages returns the thread oldest first; the API sends newest first.
func (r *rawConversation) messages(maxRunes int) []Message {
	out := make([]Message, 0, len(r.Messages.Messages))
	for _, m := range slices.Backward(r.Messages.Messages) {
		text := m.Text
		if text == "" {
			text = m.Payload.Text
		}
		if text == "" {
			text = "[" + m.Type + "]"
		}
		from := "them"
		if m.FromSelf {
			from = "me"
		}
		out = append(out, Message{From: from, Text: truncateRunes(text, maxRunes), Date: msToMinute(m.Timestamp)})
	}
	return out
}

func (r *rawConversation) summary() Conversation {
	return Conversation{ID: r.Hash, ItemID: r.Item.Hash, Item: r.Item.Title, With: r.WithUser.Name, Unread: r.UnreadMessages}
}

func (a *App) Inbox(ctx context.Context, p InboxParams) (Inbox, error) {
	n := p.Limit
	if n == 0 {
		n = defaultInboxSize
	}
	if n < 1 || n > maxInboxSize {
		return Inbox{}, errors.New("limit must be between 1 and 50")
	}
	q := url.Values{"page_size": {strconv.Itoa(n)}, "max_messages": {"1"}}
	var raw struct {
		UnreadMessages int               `json:"unread_messages"`
		Conversations  []rawConversation `json:"conversations"`
	}
	if _, err := a.wp.authed(ctx, http.MethodGet, a.wp.RootURL+"/bff/messaging/inbox?"+q.Encode(), nil, nil, &raw); err != nil {
		return Inbox{}, err
	}
	out := Inbox{Unread: raw.UnreadMessages, Conversations: []Conversation{}}
	for _, r := range raw.Conversations {
		if p.UnreadOnly && r.UnreadMessages == 0 {
			continue
		}
		c := r.summary()
		if ms := r.messages(inboxTextMaxRunes); len(ms) > 0 {
			c.Last = &ms[len(ms)-1]
		}
		out.Conversations = append(out.Conversations, c)
	}
	return out, nil
}

type ConversationParams struct {
	ConversationID string `json:"conversation_id" jsonschema:"id from inbox"`
	Limit          int    `json:"limit,omitempty" jsonschema:"latest messages to return, 1-100 (default 20)"`
}

func (a *App) thread(ctx context.Context, id string) (rawConversation, error) {
	var raw rawConversation
	_, err := a.wp.authed(ctx, http.MethodGet, a.wp.RootURL+"/bff/messaging/conversation/"+url.PathEscape(id), nil, nil, &raw)
	return raw, err
}

func (a *App) GetConversation(ctx context.Context, p ConversationParams) (Conversation, error) {
	if p.ConversationID == "" {
		return Conversation{}, errors.New("conversation_id is required")
	}
	n := p.Limit
	if n == 0 {
		n = defaultThreadSize
	}
	if n < 1 || n > maxThreadSize {
		return Conversation{}, errors.New("limit must be between 1 and 100")
	}
	raw, err := a.thread(ctx, p.ConversationID)
	if err != nil {
		return Conversation{}, err
	}
	c := raw.summary()
	ms := raw.messages(threadTextMaxRunes)
	c.Messages = ms[max(0, len(ms)-n):]
	return c, nil
}

func msToDate(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.DateOnly)
}

func msToMinute(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04")
}
