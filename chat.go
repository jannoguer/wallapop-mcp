package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
)

const (
	pubnubURL = "https://ps7.pndsn.com"
	// Wallapop's production PubNub keys and SDK id, from its web bundle.
	pubnubPubKey = "pub-c-255dc549-86f5-4abd-8b9e-921d5a02fde7"
	pubnubSubKey = "sub-c-89405e27-d4df-4d87-aca1-d6e9118f0a0d"
	pubnubSDK    = "PubNub-JS-Web/10.2.6"
	webVersion   = "8.3221.0"
)

type SendParams struct {
	ConversationID string `json:"conversation_id,omitempty" jsonschema:"id from inbox"`
	ItemID         string `json:"item_id,omitempty" jsonschema:"or: a listing to message its seller about"`
	Text           string `json:"text"`
}

type SendResult struct {
	ConversationID string `json:"conversation_id"`
	Status         string `json:"status" jsonschema:"sent, or already_sent when my last message has the same text"`
}

func (a *App) SendMessage(ctx context.Context, p SendParams) (SendResult, error) {
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
		if _, err := a.wp.authedJSON(ctx, http.MethodPost, a.wp.BaseURL+"/instant-messaging/conversation", in, &res); err != nil {
			return SendResult{}, err
		}
		if res.ConversationID == "" {
			return SendResult{}, errors.New("opening the conversation returned no id")
		}
		cid = res.ConversationID
	}
	th, err := a.thread(ctx, cid)
	if err != nil {
		return SendResult{}, err
	}
	// Agents retry; resending the same text would spam the other user.
	for _, m := range slices.Backward(th.Messages.Messages) {
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
	if _, err := a.wp.authed(ctx, http.MethodGet, a.wp.BaseURL+"/instant-messaging/token", nil, nil, &tok); err != nil {
		return SendResult{}, err
	}
	if tok.Token == "" {
		return SendResult{}, errors.New("no chat token returned")
	}
	me, err := a.wp.userHash(ctx)
	if err != nil {
		return SendResult{}, err
	}
	if err := a.publish(ctx, th.Channel, cid, me, th.WithUser.Hash, tok.Token, p.Text); err != nil {
		return SendResult{}, err
	}
	return SendResult{ConversationID: cid, Status: "sent"}, nil
}

// publish delivers a chat message the way the web app does: a PubNub REST publish.
func (a *App) publish(ctx context.Context, channel, cid, from, to, auth, text string) error {
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
	u := fmt.Sprintf("%s/publish/%s/%s/0/%s/0/%s?%s", a.wp.PubNubURL, pubnubPubKey, pubnubSubKey,
		url.PathEscape(channel), url.PathEscape(string(payload)), q.Encode())
	var ack []any
	if _, err := send(ctx, a.wp.HTTP, a.wp.gate, http.MethodGet, u, http.Header{}, nil, &ack); err != nil {
		return fmt.Errorf("chat delivery: %w", err)
	}
	if len(ack) < 2 || ack[0] != float64(1) || ack[1] != "Sent" {
		return fmt.Errorf("chat delivery not acknowledged: %v", ack)
	}
	return nil
}
