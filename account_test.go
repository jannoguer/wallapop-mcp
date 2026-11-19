package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestMyItems(t *testing.T) {
	f := newFake(t)
	page := fixture(t, "auth/mine_published.json")
	f.wp["GET /items/mine/published"] = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("since") == "" {
			w.Header().Set("X-NextPage", "since=cur123")
		}
		w.Write(page)
	}
	f.wp["GET /sdui/selling/finished"] = serveFixture(t, "auth/selling_finished.json")
	cs := connect(t, f.authApp(t))

	var got MyItems
	call(t, cs, "my_items", nil, &got)
	if len(got.Items) != 1 || got.NextCursor != "cur123" {
		t.Fatalf("got %+v", got)
	}
	it := got.Items[0]
	if it.ID != "p618pm4px2z5" || it.Title != "Taza de ceramica azul" || it.Price != 5 || it.Updated != "2026-11-19" || it.Reserved || it.State != "" {
		t.Errorf("item %+v", it)
	}
	if auth := f.requests("/items/mine/published")[0].Header.Get("Authorization"); !strings.HasPrefix(auth, "Bearer ") {
		t.Errorf("no bearer token: %q", auth)
	}

	var next MyItems
	call(t, cs, "my_items", map[string]any{"cursor": "cur123"}, &next)
	if q := f.lastQuery(t, "/items/mine/published"); q.Get("since") != "cur123" || next.NextCursor != "" {
		t.Errorf("cursor not forwarded: %v, next %q", q, next.NextCursor)
	}

	var sold MyItems
	call(t, cs, "my_items", map[string]any{"status": "sold"}, &sold)
	if len(sold.Sold) != 1 || sold.Items != nil {
		t.Fatalf("sold %+v", sold)
	}
	s := sold.Sold[0]
	if s.Title != "Taza de ceramica azul" || s.Price != "6.00 €" || !strings.Contains(s.Detail, "19 Nov 2026") || !strings.HasPrefix(s.URL, itemURLPrefix) {
		t.Errorf("sold item %+v", s)
	}

	f2 := newFake(t)
	f2.wp["GET /items/mine/published"] = func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) }
	var none MyItems
	call(t, connect(t, f2.authApp(t)), "my_items", nil, &none)
	if !none.Empty || none.Items != nil {
		t.Errorf("no listings: %+v", none)
	}

	if msg := errorText(call(t, cs, "my_items", map[string]any{"status": "gone"}, nil)); !strings.Contains(msg, "status") {
		t.Errorf("bad status error %q", msg)
	}
}

func TestInbox(t *testing.T) {
	f := newFake(t)
	f.wp["GET /bff/messaging/inbox"] = serveFixture(t, "auth/inbox.json")
	cs := connect(t, f.authApp(t))

	var got Inbox
	call(t, cs, "inbox", nil, &got)
	if got.Unread != 2 || len(got.Conversations) != 2 {
		t.Fatalf("got %+v", got)
	}
	c := got.Conversations[0]
	if c.ID != "conv0000001" || c.ItemID != "item0000001" || c.With != "Ana" || c.Unread != 2 || c.Last == nil {
		t.Fatalf("conversation %+v", c)
	}
	if c.Last.From != "them" || len([]rune(c.Last.Text)) != inboxTextMaxRunes+3 || c.Last.Date != "2026-11-19 21:21" {
		t.Errorf("last message %+v", c.Last)
	}
	if q := f.lastQuery(t, "/bff/messaging/inbox"); q.Get("page_size") != "20" || q.Get("max_messages") != "1" {
		t.Errorf("query %v", q)
	}

	call(t, cs, "inbox", map[string]any{"unread_only": true, "limit": 5}, &got)
	if len(got.Conversations) != 1 || f.lastQuery(t, "/bff/messaging/inbox").Get("page_size") != "5" {
		t.Errorf("unread_only: %+v", got.Conversations)
	}
	if msg := errorText(call(t, cs, "inbox", map[string]any{"limit": 99}, nil)); !strings.Contains(msg, "limit") {
		t.Errorf("limit error %q", msg)
	}
}

func TestGetConversation(t *testing.T) {
	f := newFake(t)
	f.wp["GET /bff/messaging/conversation/{id}"] = serveFixture(t, "auth/conversation.json")
	cs := connect(t, f.authApp(t))

	var got Conversation
	call(t, cs, "get_conversation", map[string]any{"conversation_id": "conv0000001"}, &got)
	if got.ID != "conv0000001" || got.With != "Ana" || len(got.Messages) != 3 || got.Last != nil {
		t.Fatalf("got %+v", got)
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

	call(t, cs, "get_conversation", map[string]any{"conversation_id": "conv0000001", "limit": 1}, &got)
	if len(got.Messages) != 1 || got.Messages[0].Text != "Oferta de 4 EUR" {
		t.Errorf("limit 1 kept %+v", got.Messages)
	}
	if msg := errorText(call(t, cs, "get_conversation", map[string]any{}, nil)); !strings.Contains(msg, "conversation_id") {
		t.Errorf("missing id error %q", msg)
	}
}
