package line

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
)

type lineRoundTripFunc func(*http.Request) (*http.Response, error)

func (f lineRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// okResponse builds an empty 200 LINE API response.
func okResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(nil)),
		Header:     make(http.Header),
	}
}

func newTestLINEChannel(secret string) (*LINEChannel, *bus.MessageBus) {
	msgBus := bus.NewMessageBus()
	base := channels.NewBaseChannel("line", nil, msgBus, nil)
	ch := &LINEChannel{
		BaseChannel:   base,
		channelSecret: secret,
		accessToken:   "test-token",
		infoClient:    &http.Client{},
		apiClient:     &http.Client{},
	}
	ch.ctx = context.Background()
	return ch, msgBus
}

func signLINE(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func lineReceive(t *testing.T, msgBus *bus.MessageBus) bus.InboundMessage {
	t.Helper()
	select {
	case msg := <-msgBus.InboundChan():
		return msg
	case <-time.After(time.Second):
		t.Fatal("expected inbound message")
		return bus.InboundMessage{}
	}
}

func lineExpectNone(t *testing.T, msgBus *bus.MessageBus) {
	t.Helper()
	select {
	case msg := <-msgBus.InboundChan():
		t.Fatalf("unexpected inbound message: %+v", msg)
	case <-time.After(50 * time.Millisecond):
	}
}

// --- Signature verification ---

func TestVerifySignature_AcceptsValid(t *testing.T) {
	ch, _ := newTestLINEChannel("s3cr3t")
	body := []byte(`{"events":[]}`)
	if !ch.verifySignature(body, signLINE("s3cr3t", body)) {
		t.Fatal("valid signature rejected")
	}
}

func TestVerifySignature_RejectsTamperedAndMissing(t *testing.T) {
	ch, _ := newTestLINEChannel("s3cr3t")
	body := []byte(`{"events":[]}`)
	good := signLINE("s3cr3t", body)

	if ch.verifySignature(body, "") {
		t.Fatal("empty signature accepted")
	}
	// Tampered body must not match a signature computed over the original body.
	if ch.verifySignature([]byte(`{"events":[1]}`), good) {
		t.Fatal("signature valid for tampered body")
	}
	// Wrong secret must not validate.
	if ch.verifySignature(body, signLINE("wrong", body)) {
		t.Fatal("signature from wrong secret accepted")
	}
}

func TestWebhookHandler_RejectsBadSignature(t *testing.T) {
	ch, _ := newTestLINEChannel("s3cr3t")
	body := []byte(`{"events":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Line-Signature", "deadbeef")
	rec := httptest.NewRecorder()
	ch.webhookHandler(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", rec.Code)
	}
}

func TestWebhookHandler_AcceptsValidSignature(t *testing.T) {
	ch, _ := newTestLINEChannel("s3cr3t")
	body := []byte(`{"events":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Line-Signature", signLINE("s3cr3t", body))
	rec := httptest.NewRecorder()
	ch.webhookHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
}

// --- Inbound event parsing ---

func TestProcessEvent_DirectTextMessage(t *testing.T) {
	ch, msgBus := newTestLINEChannel("s")
	ev := lineEvent{
		Type:       "message",
		ReplyToken: "rt-1",
		Source:     lineSource{Type: "user", UserID: "U123"},
		Message:    json.RawMessage(`{"id":"m1","type":"text","text":"hello"}`),
	}
	ch.processEvent(ev)

	inbound := lineReceive(t, msgBus)
	if inbound.Peer.Kind != bus.PeerDirect || inbound.Peer.ID != "U123" {
		t.Fatalf("peer=%+v", inbound.Peer)
	}
	if inbound.ChatID != "U123" {
		t.Fatalf("chat_id=%q", inbound.ChatID)
	}
	if inbound.Content != "hello" {
		t.Fatalf("content=%q", inbound.Content)
	}
	// Reply token should be cached for outbound reply association.
	if _, ok := ch.replyTokens.Load("U123"); !ok {
		t.Fatal("expected reply token stored")
	}
}

func TestProcessEvent_GroupMessageUsesGroupID(t *testing.T) {
	ch, msgBus := newTestLINEChannel("s")
	ev := lineEvent{
		Type:    "message",
		Source:  lineSource{Type: "group", GroupID: "G9", UserID: "U7"},
		Message: json.RawMessage(`{"id":"m2","type":"text","text":"hi group"}`),
	}
	ch.processEvent(ev)

	inbound := lineReceive(t, msgBus)
	if inbound.Peer.Kind != bus.PeerGroup || inbound.Peer.ID != "G9" {
		t.Fatalf("peer=%+v want group/G9", inbound.Peer)
	}
	if inbound.ChatID != "G9" {
		t.Fatalf("chat_id=%q", inbound.ChatID)
	}
}

func TestProcessEvent_NonMessageEventIgnored(t *testing.T) {
	ch, msgBus := newTestLINEChannel("s")
	ch.processEvent(lineEvent{
		Type:   "follow",
		Source: lineSource{Type: "user", UserID: "U1"},
	})
	lineExpectNone(t, msgBus)
}

func TestProcessEvent_MalformedMessageJSONIgnored(t *testing.T) {
	ch, msgBus := newTestLINEChannel("s")
	ch.processEvent(lineEvent{
		Type:    "message",
		Source:  lineSource{Type: "user", UserID: "U1"},
		Message: json.RawMessage(`{"id":"m1","type":`), // truncated JSON
	})
	lineExpectNone(t, msgBus)
}

func TestProcessEvent_StickerBecomesPlaceholder(t *testing.T) {
	ch, msgBus := newTestLINEChannel("s")
	ch.processEvent(lineEvent{
		Type:    "message",
		Source:  lineSource{Type: "user", UserID: "U1"},
		Message: json.RawMessage(`{"id":"m1","type":"sticker"}`),
	})
	inbound := lineReceive(t, msgBus)
	if inbound.Content != "[sticker]" {
		t.Fatalf("content=%q want [sticker]", inbound.Content)
	}
}

func TestResolveChatID(t *testing.T) {
	ch, _ := newTestLINEChannel("s")
	tests := []struct {
		src  lineSource
		want string
	}{
		{lineSource{Type: "group", GroupID: "G1", UserID: "U1"}, "G1"},
		{lineSource{Type: "room", RoomID: "R1", UserID: "U1"}, "R1"},
		{lineSource{Type: "user", UserID: "U1"}, "U1"},
	}
	for _, tt := range tests {
		if got := ch.resolveChatID(tt.src); got != tt.want {
			t.Errorf("resolveChatID(%+v)=%q want %q", tt.src, got, tt.want)
		}
	}
}

// --- Outbound send encoding ---

func TestSend_FailsWhenNotRunning(t *testing.T) {
	ch, _ := newTestLINEChannel("s")
	err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "U1", Content: "hi"})
	if err != channels.ErrNotRunning {
		t.Fatalf("err=%v want ErrNotRunning", err)
	}
}

func TestSend_PushAPIRequestShape(t *testing.T) {
	ch, _ := newTestLINEChannel("s")
	ch.SetRunning(true)
	t.Cleanup(func() { ch.SetRunning(false) })

	var gotURL, gotAuth string
	var gotBody []byte
	ch.apiClient.Transport = lineRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		return okResponse(), nil
	})

	// No reply token cached → falls back to Push API.
	if err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "U42", Content: "pushed"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotURL != linePushEndpoint {
		t.Fatalf("url=%q want %q", gotURL, linePushEndpoint)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("auth=%q", gotAuth)
	}
	var payload struct {
		To       string              `json:"to"`
		Messages []map[string]string `json:"messages"`
	}
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("payload unmarshal: %v body=%s", err, gotBody)
	}
	if payload.To != "U42" {
		t.Fatalf("to=%q", payload.To)
	}
	if len(payload.Messages) != 1 || payload.Messages[0]["type"] != "text" || payload.Messages[0]["text"] != "pushed" {
		t.Fatalf("messages=%+v", payload.Messages)
	}
}

func TestSend_PrefersReplyAPIWhenTokenFresh(t *testing.T) {
	ch, _ := newTestLINEChannel("s")
	ch.SetRunning(true)
	t.Cleanup(func() { ch.SetRunning(false) })

	ch.replyTokens.Store("U1", replyTokenEntry{token: "reply-tok", timestamp: time.Now()})

	var gotURL string
	ch.apiClient.Transport = lineRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotURL = r.URL.String()
		return okResponse(), nil
	})

	if err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "U1", Content: "reply"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotURL != lineReplyEndpoint {
		t.Fatalf("url=%q want reply endpoint %q", gotURL, lineReplyEndpoint)
	}
}

func TestSend_PropagatesTransportError(t *testing.T) {
	ch, _ := newTestLINEChannel("s")
	ch.SetRunning(true)
	t.Cleanup(func() { ch.SetRunning(false) })

	// Non-2xx from the Push API must surface as an error.
	ch.apiClient.Transport = lineRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"message":"bad"}`))),
			Header:     make(http.Header),
		}, nil
	})

	if err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "U1", Content: "x"}); err == nil {
		t.Fatal("expected error on 400 from LINE API")
	}
}
