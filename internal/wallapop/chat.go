package wallapop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
)

const (
	defaultInboxSize   = 20
	maxInboxSize       = 50
	defaultThreadSize  = 20
	maxThreadSize      = 100
	inboxTextMaxRunes  = 160
	threadTextMaxRunes = 2000

	pubnubURL = "https://ps7.pndsn.com"
	// Wallapop's production PubNub keys and SDK id, from its web bundle.
	pubnubPubKey = "pub-c-255dc549-86f5-4abd-8b9e-921d5a02fde7"
	pubnubSubKey = "sub-c-89405e27-d4df-4d87-aca1-d6e9118f0a0d"
	pubnubSDK    = "PubNub-JS-Web/10.2.6"
	webVersion   = "8.3221.0"
)

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

func (c *Client) Inbox(ctx context.Context, p InboxParams) (Inbox, error) {
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
	if _, err := c.authed(ctx, http.MethodGet, c.RootURL+"/bff/messaging/inbox?"+q.Encode(), nil, nil, &raw); err != nil {
		return Inbox{}, err
	}
	out := Inbox{Unread: raw.UnreadMessages, Conversations: []Conversation{}}
	for _, r := range raw.Conversations {
		if p.UnreadOnly && r.UnreadMessages == 0 {
			continue
		}
		conv := r.summary()
		if ms := r.messages(inboxTextMaxRunes); len(ms) > 0 {
			conv.Last = &ms[len(ms)-1]
		}
		out.Conversations = append(out.Conversations, conv)
	}
	return out, nil
}

type ConversationParams struct {
	ConversationID string `json:"conversation_id" jsonschema:"id from inbox"`
	Limit          int    `json:"limit,omitempty" jsonschema:"latest messages to return, 1-100 (default 20)"`
}

func (c *Client) thread(ctx context.Context, id string) (rawConversation, error) {
	var raw rawConversation
	_, err := c.authed(ctx, http.MethodGet, c.RootURL+"/bff/messaging/conversation/"+url.PathEscape(id), nil, nil, &raw)
	return raw, err
}

func (c *Client) Conversation(ctx context.Context, p ConversationParams) (Conversation, error) {
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
	raw, err := c.thread(ctx, p.ConversationID)
	if err != nil {
		return Conversation{}, err
	}
	conv := raw.summary()
	ms := raw.messages(threadTextMaxRunes)
	conv.Messages = ms[max(0, len(ms)-n):]
	return conv, nil
}

type SendParams struct {
	ConversationID string `json:"conversation_id,omitempty" jsonschema:"id from inbox"`
	ItemID         string `json:"item_id,omitempty" jsonschema:"or: a listing to message its seller about"`
	Text           string `json:"text"`
}

type SendResult struct {
	ConversationID string `json:"conversation_id"`
	Status         string `json:"status" jsonschema:"sent, or already_sent when my last message has the same text"`
}

func (c *Client) SendMessage(ctx context.Context, p SendParams) (SendResult, error) {
	if p.Text == "" {
		return SendResult{}, errors.New("text is required")
	}
	if (p.ConversationID == "") == (p.ItemID == "") {
		return SendResult{}, errors.New("give exactly one of conversation_id or item_id")
	}
	cid := p.ConversationID
	if cid == "" {
		var res struct {
			ConversationID string `json:"conversation_id"`
		}
		in := map[string]string{"item_hash_id": p.ItemID}
		if _, err := c.authedJSON(ctx, http.MethodPost, c.BaseURL+"/instant-messaging/conversation", in, &res); err != nil {
			return SendResult{}, err
		}
		if res.ConversationID == "" {
			return SendResult{}, errors.New("opening the conversation returned no id")
		}
		cid = res.ConversationID
	}
	th, err := c.thread(ctx, cid)
	if err != nil {
		return SendResult{}, err
	}
	// Agents retry; resending the same text would spam the other user.
	// Messages come newest first, so the first of mine is my latest.
	for _, m := range th.Messages.Messages {
		if m.FromSelf {
			if m.Text == p.Text {
				return SendResult{ConversationID: cid, Status: "already_sent"}, nil
			}
			break
		}
	}
	if th.Channel == "" || th.WithUser.Hash == "" {
		return SendResult{}, errors.New("conversation has no chat channel or recipient")
	}
	var tok struct {
		Token string `json:"token"`
	}
	if _, err := c.authed(ctx, http.MethodGet, c.BaseURL+"/instant-messaging/token", nil, nil, &tok); err != nil {
		return SendResult{}, err
	}
	if tok.Token == "" {
		return SendResult{}, errors.New("no chat token returned")
	}
	me, err := c.userHash(ctx)
	if err != nil {
		return SendResult{}, err
	}
	if err := c.publish(ctx, th.Channel, cid, me, th.WithUser.Hash, tok.Token, p.Text); err != nil {
		return SendResult{}, err
	}
	return SendResult{ConversationID: cid, Status: "sent"}, nil
}

// publish delivers a chat message the way the web app does: a PubNub REST publish.
func (c *Client) publish(ctx context.Context, channel, cid, from, to, auth, text string) error {
	msgID, err := newUUID()
	if err != nil {
		return err
	}
	reqID, err := newUUID()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"id": msgID, "payload": map[string]string{"text": text}})
	if err != nil {
		return err
	}
	meta, err := json.Marshal(map[string]any{
		"type":              "text",
		"sender":            map[string]any{"platform": map[string]string{"app_version": webVersion, "os_version": "0"}},
		"to_user_hash":      to,
		"from_user_hash":    from,
		"conversation_hash": cid,
	})
	if err != nil {
		return err
	}
	q := url.Values{"meta": {string(meta)}, "uuid": {from}, "requestid": {reqID}, "pnsdk": {pubnubSDK}, "auth": {auth}}
	u := fmt.Sprintf("%s/publish/%s/%s/0/%s/0/%s?%s", c.PubNubURL, pubnubPubKey, pubnubSubKey,
		url.PathEscape(channel), url.PathEscape(string(payload)), q.Encode())
	var ack []any
	if _, err := c.http.Do(ctx, http.MethodGet, u, http.Header{}, nil, &ack); err != nil {
		return fmt.Errorf("chat delivery: %w", err)
	}
	if len(ack) < 2 || ack[0] != float64(1) || ack[1] != "Sent" {
		return fmt.Errorf("chat delivery not acknowledged: %v", ack)
	}
	return nil
}
