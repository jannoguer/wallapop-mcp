package wallapop

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jannoguer/wallapop-mcp/internal/wallapop/wallapoptest"
)

func TestInbox(t *testing.T) {
	f := newFake(t)
	f.Routes["GET /bff/messaging/inbox"] = serveFixture(t, "auth/inbox.json")
	c := authClient(t, f)
	ctx := context.Background()

	got, err := c.Inbox(ctx, InboxParams{})
	if err != nil || got.Unread != 2 || len(got.Conversations) != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	conv := got.Conversations[0]
	if conv.ID != "conv0000001" || conv.ItemID != "item0000001" || conv.With != "Ana" || conv.Unread != 2 || conv.Last == nil {
		t.Fatalf("conversation %+v", conv)
	}
	if conv.Last.From != "them" || len([]rune(conv.Last.Text)) != inboxTextMaxRunes+3 || conv.Last.Date != "2026-11-19 21:21" {
		t.Errorf("last message %+v", conv.Last)
	}
	if q := f.LastQuery(t, "/bff/messaging/inbox"); q.Get("page_size") != "20" || q.Get("max_messages") != "1" {
		t.Errorf("query %v", q)
	}

	got, err = c.Inbox(ctx, InboxParams{UnreadOnly: true, Limit: 5})
	if err != nil || len(got.Conversations) != 1 || f.LastQuery(t, "/bff/messaging/inbox").Get("page_size") != "5" {
		t.Errorf("unread_only: %+v, %v", got.Conversations, err)
	}
	if _, err := c.Inbox(ctx, InboxParams{Limit: 99}); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("limit error %v", err)
	}
}

func TestConversation(t *testing.T) {
	f := newFake(t)
	f.Routes["GET /bff/messaging/conversation/{id}"] = serveFixture(t, "auth/conversation.json")
	c := authClient(t, f)
	ctx := context.Background()

	got, err := c.Conversation(ctx, ConversationParams{ConversationID: "conv0000001"})
	if err != nil || got.ID != "conv0000001" || got.With != "Ana" || len(got.Messages) != 3 || got.Last != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
	want := []Message{
		{From: "them", Text: "Hola!"},
		{From: "me", Text: "Hola, sigue disponible?"},
		{From: "them", Text: "Oferta de 4 EUR"},
	}
	for i, m := range got.Messages {
		if m.From != want[i].From || m.Text != want[i].Text || m.Date == "" {
			t.Errorf("message %d: %+v, want %+v", i, m, want[i])
		}
	}

	got, err = c.Conversation(ctx, ConversationParams{ConversationID: "conv0000001", Limit: 1})
	if err != nil || len(got.Messages) != 1 || got.Messages[0].Text != "Oferta de 4 EUR" {
		t.Errorf("limit 1 kept %+v, %v", got.Messages, err)
	}
	if _, err := c.Conversation(ctx, ConversationParams{}); err == nil || !strings.Contains(err.Error(), "conversation_id") {
		t.Errorf("missing id error %v", err)
	}
}

func chatFake(t *testing.T, ack string) (*wallapoptest.Fake, *recorder, *recorder) {
	f := newFake(t)
	open, pub := &recorder{}, &recorder{}
	f.Routes["GET /bff/messaging/conversation/{id}"] = serveFixture(t, "auth/conversation.json")
	f.Routes["POST /instant-messaging/conversation"] = open.handler(http.StatusOK, `{"conversation_id":"conv0000001"}`)
	f.Routes["GET /instant-messaging/token"] = (&recorder{}).handler(http.StatusCreated, `{"token":"imtok","channels":[]}`)
	f.Routes["GET /publish/{pub}/{sub}/0/{channel}/0/{msg}"] = pub.handler(http.StatusOK, ack)
	return f, open, pub
}

func TestSendMessage(t *testing.T) {
	f, open, pub := chatFake(t, `[1,"Sent","17951232645970000"]`)
	c := authClient(t, f)
	ctx := context.Background()

	got, err := c.SendMessage(ctx, SendParams{ConversationID: "conv0000001", Text: "Puedo pasar a las 6?"})
	if err != nil || got.Status != "sent" || got.ConversationID != "conv0000001" {
		t.Fatalf("got %+v, %v", got, err)
	}
	r := pub.last(t).r
	if r.PathValue("pub") != pubnubPubKey || r.PathValue("sub") != pubnubSubKey || r.PathValue("channel") != "chat.buyer000001.conv0000001.seller000001" {
		t.Errorf("publish path %s", r.URL.Path)
	}
	var msg struct {
		ID      string            `json:"id"`
		Payload map[string]string `json:"payload"`
	}
	if err := json.Unmarshal([]byte(r.PathValue("msg")), &msg); err != nil || msg.Payload["text"] != "Puedo pasar a las 6?" || msg.ID == "" {
		t.Errorf("message %q: %v", r.PathValue("msg"), err)
	}
	q := r.URL.Query()
	var meta map[string]any
	json.Unmarshal([]byte(q.Get("meta")), &meta)
	if q.Get("auth") != "imtok" || q.Get("uuid") != "myhash" || meta["from_user_hash"] != "myhash" ||
		meta["to_user_hash"] != "buyer000001" || meta["conversation_hash"] != "conv0000001" || meta["type"] != "text" {
		t.Errorf("query %v", q)
	}
	if r.Header.Get("Authorization") != "" {
		t.Error("API token leaked to PubNub")
	}

	got, err = c.SendMessage(ctx, SendParams{ItemID: "item0000001", Text: "Otra pregunta"})
	var in map[string]string
	json.Unmarshal(open.last(t).body, &in)
	if err != nil || in["item_hash_id"] != "item0000001" || got.ConversationID != "conv0000001" {
		t.Errorf("open conversation %v -> %+v, %v", in, got, err)
	}
}

func TestSendMessageDedupe(t *testing.T) {
	f, _, pub := chatFake(t, `[1,"Sent","1"]`)
	got, err := authClient(t, f).SendMessage(context.Background(), SendParams{ConversationID: "conv0000001", Text: "Hola, sigue disponible?"})
	if err != nil || got.Status != "already_sent" || len(pub.reqs) != 0 {
		t.Errorf("got %+v, %v after %d publishes", got, err, len(pub.reqs))
	}
}

func TestSendMessageErrors(t *testing.T) {
	f, _, _ := chatFake(t, `[0,"Forbidden","0"]`)
	c := authClient(t, f)
	tests := []struct {
		p       SendParams
		wantErr string
	}{
		{SendParams{ConversationID: "conv0000001", Text: "x"}, "not acknowledged"},
		{SendParams{ConversationID: "c", ItemID: "i", Text: "x"}, "exactly one"},
		{SendParams{ConversationID: "c"}, "text"},
	}
	for _, tt := range tests {
		if _, err := c.SendMessage(context.Background(), tt.p); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("%+v: error %v, want %q", tt.p, err, tt.wantErr)
		}
	}
}
