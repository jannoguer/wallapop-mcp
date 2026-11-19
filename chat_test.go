package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func chatFake(t *testing.T, ack string) (*fake, *recorder, *recorder) {
	f := newFake(t)
	open, pub := &recorder{}, &recorder{}
	f.wp["GET /bff/messaging/conversation/{id}"] = serveFixture(t, "auth/conversation.json")
	f.wp["POST /instant-messaging/conversation"] = open.handler(http.StatusOK, `{"conversation_id":"conv0000001"}`)
	f.wp["GET /instant-messaging/token"] = (&recorder{}).handler(http.StatusCreated, `{"token":"imtok","channels":[]}`)
	f.wp["GET /publish/{pub}/{sub}/0/{channel}/0/{msg}"] = pub.handler(http.StatusOK, ack)
	return f, open, pub
}

func TestSendMessage(t *testing.T) {
	f, open, pub := chatFake(t, `[1,"Sent","17951232645970000"]`)
	cs := connect(t, f.authApp(t))

	var got SendResult
	call(t, cs, "send_message", map[string]any{"conversation_id": "conv0000001", "text": "Puedo pasar a las 6?"}, &got)
	if got.Status != "sent" || got.ConversationID != "conv0000001" {
		t.Fatalf("got %+v", got)
	}
	c := pub.last(t)
	if c.r.PathValue("pub") != pubnubPubKey || c.r.PathValue("sub") != pubnubSubKey || c.r.PathValue("channel") != "chat.buyer000001.conv0000001.seller000001" {
		t.Errorf("publish path %s", c.r.URL.Path)
	}
	var msg struct {
		ID      string            `json:"id"`
		Payload map[string]string `json:"payload"`
	}
	if err := json.Unmarshal([]byte(c.r.PathValue("msg")), &msg); err != nil || msg.Payload["text"] != "Puedo pasar a las 6?" || msg.ID == "" {
		t.Errorf("message %q: %v", c.r.PathValue("msg"), err)
	}
	q := c.r.URL.Query()
	var meta map[string]any
	json.Unmarshal([]byte(q.Get("meta")), &meta)
	if q.Get("auth") != "imtok" || q.Get("uuid") != "myhash" || meta["from_user_hash"] != "myhash" ||
		meta["to_user_hash"] != "buyer000001" || meta["conversation_hash"] != "conv0000001" || meta["type"] != "text" {
		t.Errorf("query %v", q)
	}
	if c.r.Header.Get("Authorization") != "" {
		t.Error("API token leaked to PubNub")
	}

	call(t, cs, "send_message", map[string]any{"item_id": "item0000001", "text": "Otra pregunta"}, &got)
	var in map[string]string
	json.Unmarshal(open.last(t).body, &in)
	if in["item_hash_id"] != "item0000001" || got.ConversationID != "conv0000001" {
		t.Errorf("open conversation %v -> %+v", in, got)
	}
}

func TestSendMessageDedupe(t *testing.T) {
	f, _, pub := chatFake(t, `[1,"Sent","1"]`)
	cs := connect(t, f.authApp(t))
	var got SendResult
	call(t, cs, "send_message", map[string]any{"conversation_id": "conv0000001", "text": "Hola, sigue disponible?"}, &got)
	if got.Status != "already_sent" || len(pub.reqs) != 0 {
		t.Errorf("got %+v after %d publishes", got, len(pub.reqs))
	}
}

func TestSendMessageErrors(t *testing.T) {
	f, _, _ := chatFake(t, `[0,"Forbidden","0"]`)
	cs := connect(t, f.authApp(t))
	tests := []struct {
		args    map[string]any
		wantErr string
	}{
		{map[string]any{"conversation_id": "conv0000001", "text": "x"}, "not acknowledged"},
		{map[string]any{"conversation_id": "c", "item_id": "i", "text": "x"}, "exactly one"},
		{map[string]any{"conversation_id": "c"}, "text"},
	}
	for _, tt := range tests {
		if msg := errorText(call(t, cs, "send_message", tt.args, nil)); !strings.Contains(msg, tt.wantErr) {
			t.Errorf("%v: error %q, want %q", tt.args, msg, tt.wantErr)
		}
	}
}
