package googlechat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

func newSS(s string) config.SecureString { return *config.NewSecureString(s) }

// TestNewGoogleChatChannel_WebhookResolvedFromBundle is the MAJ-1 constructor
// guard: when webhook_url_ref is set, the channel must resolve the real webhook
// URL from the SecretBundle (not from any inline config), mirroring telegram's
// token_ref flow. config.json carries no plaintext secret.
func TestNewGoogleChatChannel_WebhookResolvedFromBundle(t *testing.T) {
	const realURL = "https://chat.googleapis.com/v1/spaces/AAA/messages?key=KKK&token=TTT"
	cfg := config.GoogleChatConfig{
		Enabled:       true,
		Mode:          "webhook",
		WebhookURLRef: "channel_google-chat_webhook_url",
		// No inline WebhookURL — the secret lives only in the bundle.
	}
	secrets := credentials.SecretBundle{
		credentials.SecretRef("channel_google-chat_webhook_url"): realURL,
	}
	ch, err := NewGoogleChatChannel(cfg, secrets, bus.NewMessageBus())
	if err != nil {
		t.Fatalf("NewGoogleChatChannel() = %v", err)
	}
	if got := ch.config.WebhookURL.String(); got != realURL {
		t.Errorf("resolved WebhookURL = %q, want %q", got, realURL)
	}
}

// TestNewGoogleChatChannel_ServiceAccountResolvedFromBundle proves the
// service-account-JSON ref path now lands in a real struct field and resolves —
// the path that was effectively broken before MAJ-1.
func TestNewGoogleChatChannel_ServiceAccountResolvedFromBundle(t *testing.T) {
	const saJSON = `{"client_email":"bot@proj.iam.gserviceaccount.com","private_key":"PK"}`
	cfg := config.GoogleChatConfig{
		Enabled:               true,
		Mode:                  "bot",
		ServiceAccountJSONRef: "channel_google-chat_service_account_json",
	}
	secrets := credentials.SecretBundle{
		credentials.SecretRef("channel_google-chat_service_account_json"): saJSON,
	}
	ch, err := NewGoogleChatChannel(cfg, secrets, bus.NewMessageBus())
	if err != nil {
		t.Fatalf("NewGoogleChatChannel() = %v", err)
	}
	if got := ch.config.ServiceAccountJSON.String(); got != saJSON {
		t.Errorf("resolved ServiceAccountJSON = %q, want %q", got, saJSON)
	}
}

// TestNewGoogleChatChannel_InlineFallbackWhenNoRef confirms env-injected /
// backward-compat configs (inline SecureString, no *Ref) still construct — the
// fallback path that keeps the legacy tests green.
func TestNewGoogleChatChannel_InlineFallbackWhenNoRef(t *testing.T) {
	cfg := config.GoogleChatConfig{
		Enabled:    true,
		Mode:       "webhook",
		WebhookURL: newSS("https://chat.googleapis.com/inline/legacy"),
		// No WebhookURLRef and an empty (nil) bundle.
	}
	ch, err := NewGoogleChatChannel(cfg, nil, bus.NewMessageBus())
	if err != nil {
		t.Fatalf("NewGoogleChatChannel() = %v", err)
	}
	if got := ch.config.WebhookURL.String(); got != "https://chat.googleapis.com/inline/legacy" {
		t.Errorf("inline fallback WebhookURL = %q", got)
	}
}

func TestNewGoogleChatChannel_WebhookMode(t *testing.T) {
	cfg := config.GoogleChatConfig{
		Enabled:    true,
		Mode:       "webhook",
		WebhookURL: newSS("https://chat.googleapis.com/webhook/123"),
	}
	msgBus := bus.NewMessageBus()
	ch, err := NewGoogleChatChannel(cfg, nil, msgBus)
	if err != nil {
		t.Fatalf("NewGoogleChatChannel() = %v", err)
	}
	if ch.Name() != "google-chat" {
		t.Errorf("Name() = %q, want %q", ch.Name(), "google-chat")
	}
	// After SetInstanceID, Name() must reflect the instance key, not the
	// hardcoded literal — this is the invariant: ch.Name() == manager map key.
	ch.SetInstanceID("google-chat.eu")
	if ch.Name() != "google-chat.eu" {
		t.Errorf("Name() after SetInstanceID = %q, want %q", ch.Name(), "google-chat.eu")
	}
	if ch.mode != "webhook" {
		t.Errorf("mode = %q, want %q", ch.mode, "webhook")
	}
}

func TestNewGoogleChatChannel_BotModeWithJSON(t *testing.T) {
	cfg := config.GoogleChatConfig{
		Enabled: true,
		Mode:    "bot",
		ServiceAccountJSON: newSS(`{
			"client_email": "test@example.com",
			"private_key": "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAu1SU1LfVLPHCozMxH2Mo4lgOEePzNm0tRgeLezV6ffAt0gunVvxw\nVYyCAvRA1qVaS2lAFW8J8Z8pwC4sw3q3tqR9tGcLcWaVb3mZMiTJACJL+4WJxKKIlz1\nqL8tPB2Cxn5eGrL8Cnw4PYe0RQYp5q4bjByL2x3tHMF88dTj1gDgtLZM9Y2r0aZKLcS\nZ2fMvwD8W8bIqAYCCg3rGcNHCgL3i3qPVBMD8M8K8E6mBKPO5l9XLTdM4l0qYBN1f7q\n6E8Q1nLqJ0rI3tHMF88dTj1gDgtLZM9Y2r0aZKLcSZ2fMvwD8W8bIqAYCCg3rGcNHCg\nL3i3qPVBMD8M8K8E6mBKPO5l9XLTdM4l0qYBN1f7q6E8Q1nLqJ0rIxQIDAQABAoIBABb\nw2qPb4nLqJ0rI3tHMF88dTj1gDgtLZM9Y2r0aZKLcSZ2fMvwD8W8bIqAYCCg3rGcNHCg\nL3i3qPVBMD8M8K8E6mBKPO5l9XLTdM4l0qYBN1f7q6E8Q1nLqJ0rI3tHMF88dTj1gDgt\nLZM9Y2r0aZKLcSZ2fMvwD8W8bIqAYCCg3rGcNHCgL3i3qPVBMD8M8K8E6mBKPO5l9XLTd\nM4l0qYBN1f7q6E8Q1nLqJ0rI3tHMF88dTj1gDgtLZM9Y2r0aZKLcSZ2fMvwD8W8bIqAY\nCg3rGcNHCgL3i3qPVBMD8M8K8E6mBKPO5l9XLTdM4l0qYBN1f7q6E8Q1nLqJ0rIxQID\nAQABAoIBADhXe7s8vLp1V2xGLBHMx3qPVBMD8M8K8E6mBKPO5l9XLTdM4l0qYBN1f7q6\nE8Q1nLqJ0rI3tHMF88dTj1gDgtLZM9Y2r0aZKLcSZ2fMvwD8W8bIqAYCCg3rGcNHCgL3\ni3qPVBMD8M8K8E6mBKPO5l9XLTdM4l0qYBN1f7q6E8Q1nLqJ0rI3tHMF88dTj1gDgtLZ\nM9Y2r0aZKLcSZ2fMvwD8W8bIqAYCCg3rGcNHCgL3i3qPVBMD8M8K8E6mBKPO5l9XLTdM\nl0qYBN1f7q6E8Q1nLqJ0rI3tHMF88dTj1gDgtLZM9Y2r0aZKLcSZ2fMvwD8W8bIqAYC\nCg3rGcNHCgL3i3qPVBMD8M8K8E6mBKPO5l9XLTdM4l0qYBN1f7q6E8Q1nLqJ0rIxQID\nAQAB\n-----END RSA PRIVATE KEY-----\n",
			"token_uri": "https://oauth2.googleapis.com/token"
		}`),
	}
	msgBus := bus.NewMessageBus()
	ch, err := NewGoogleChatChannel(cfg, nil, msgBus)
	if err != nil {
		t.Fatalf("NewGoogleChatChannel() = %v", err)
	}
	if ch.mode != "bot" {
		t.Errorf("mode = %q, want %q", ch.mode, "bot")
	}
}

func TestNewGoogleChatChannel_NoCredentials(t *testing.T) {
	cfg := config.GoogleChatConfig{
		Enabled: true,
		Mode:    "webhook",
	}
	msgBus := bus.NewMessageBus()
	_, err := NewGoogleChatChannel(cfg, nil, msgBus)
	if err == nil {
		t.Error("expected error for missing credentials")
	}
}

func TestGoogleChatChannel_SendWebhook(t *testing.T) {
	var receivedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		bodyReader := http.MaxBytesReader(w, r.Body, 10<<20)
		receivedBody, _ = io.ReadAll(bodyReader)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := config.GoogleChatConfig{
		Enabled:    true,
		Mode:       "webhook",
		WebhookURL: newSS(server.URL),
	}
	msgBus := bus.NewMessageBus()
	ch, _ := NewGoogleChatChannel(cfg, nil, msgBus)
	ch.client = server.Client()
	ch.ctx = context.Background()
	ch.cancel = func() {}
	ch.SetRunning(true)

	msg := bus.OutboundMessage{
		Channel: "google-chat",
		ChatID:  "spaces/abc",
		Content: "Hello from test",
	}

	err := ch.Send(context.Background(), msg)
	if err != nil {
		t.Errorf("Send() = %v", err)
	}

	var payload map[string]any
	json.Unmarshal(receivedBody, &payload)
	if payload["text"] != "Hello from test" {
		t.Errorf("text = %q, want %q", payload["text"], "Hello from test")
	}
}

func TestGoogleChatChannel_WebhookPath(t *testing.T) {
	ch := &GoogleChatChannel{}
	if path := ch.WebhookPath(); path != "/webhook/google-chat" {
		t.Errorf("WebhookPath() = %q, want %q", path, "/webhook/google-chat")
	}
}

func TestGoogleChatChannel_HealthPath(t *testing.T) {
	ch := &GoogleChatChannel{}
	if path := ch.HealthPath(); path != "/webhook/google-chat/health" {
		t.Errorf("HealthPath() = %q, want %q", path, "/webhook/google-chat/health")
	}
}

func TestGoogleChatChannel_HealthHandlerRunning(t *testing.T) {
	ch := &GoogleChatChannel{}
	ch.healthOK.Store(true)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	ch.HealthHandler(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("HealthHandler() status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestGoogleChatChannel_HealthHandlerNotRunning(t *testing.T) {
	ch := &GoogleChatChannel{}
	ch.healthOK.Store(false)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/health", nil)
	ch.HealthHandler(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("HealthHandler() status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestGoogleChatChannel_GroupTrigger_ORLogic(t *testing.T) {
	tests := []struct {
		name         string
		msg          string
		isMentioned  bool
		mMentionOnly bool
		prefixes     []string
		want         bool
	}{
		{
			name:         "mentioned with mention_only true",
			msg:          "hello",
			isMentioned:  true,
			mMentionOnly: true,
			want:         true,
		},
		{
			name:         "not mentioned with mention_only true",
			msg:          "hello",
			isMentioned:  false,
			mMentionOnly: true,
			want:         false,
		},
		{
			name:        "prefix match",
			msg:         "/ask hello",
			isMentioned: false,
			prefixes:    []string{"/ask"},
			want:        true,
		},
		{
			name:        "no prefix match",
			msg:         "/skip hello",
			isMentioned: false,
			prefixes:    []string{"/ask"},
			want:        false,
		},
		{
			name:        "permissive default (no trigger config)",
			msg:         "hello",
			isMentioned: false,
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.GoogleChatConfig{
				Mode:       "webhook",
				WebhookURL: newSS("https://chat.googleapis.com/webhook/test"),
				GroupTrigger: config.GroupTriggerConfig{
					MentionOnly: tt.mMentionOnly,
					Prefixes:    tt.prefixes,
				},
			}
			msgBus := bus.NewMessageBus()
			ch, err := NewGoogleChatChannel(cfg, nil, msgBus)
			if err != nil {
				t.Fatalf("NewGoogleChatChannel() = %v", err)
			}
			ch.BaseChannel = channels.NewBaseChannel("google-chat", cfg, msgBus, nil,
				channels.WithGroupTrigger(cfg.GroupTrigger),
			)
			ok, _ := ch.ShouldRespondInGroup(tt.isMentioned, tt.msg)
			if ok != tt.want {
				t.Errorf("ShouldRespondInGroup() = %v, want %v", ok, tt.want)
			}
		})
	}
}

func TestBackoff(t *testing.T) {
	for attempt := 0; attempt < 10; attempt++ {
		delay := backoff(attempt)
		if delay <= 0 {
			t.Errorf("backoff(%d) = %v, must be > 0", attempt, delay)
		}
		if delay > 30*time.Second {
			t.Errorf("backoff(%d) = %v, must be <= 30s", attempt, delay)
		}
	}
}

func TestParseRetryAfter_WithHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	client := server.Client()
	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do failed: %v", err)
	}
	defer resp.Body.Close()

	delay := parseRetryAfter(resp)
	if delay != 60*time.Second {
		t.Errorf("parseRetryAfter() = %v, want 60s", delay)
	}
}

func TestParseRetryAfter_WithoutHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	client := server.Client()
	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do failed: %v", err)
	}
	defer resp.Body.Close()

	delay := parseRetryAfter(resp)
	if delay <= 0 {
		t.Errorf("parseRetryAfter() must be > 0, got %v", delay)
	}
}

func TestGoogleChatEvent_Parsing(t *testing.T) {
	raw := `{
  "type": "MESSAGE",
  "space": {
    "name": "spaces/abc123",
    "displayName": "Test Space",
    "type": "ROOM"
  },
  "sender": {
    "name": "users/123",
    "displayName": "Alice",
    "email": "alice@example.com"
  },
  "message": {
    "name": "messages/456",
    "text": "Hello bot!"
  }
}`
	var event googleChatEvent
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatalf("Unmarshal() = %v", err)
	}
	if event.Type != "MESSAGE" {
		t.Errorf("Type = %q, want MESSAGE", event.Type)
	}
	if event.Space.Name != "spaces/abc123" {
		t.Errorf("Space.Name = %q, want spaces/abc123", event.Space.Name)
	}
	if event.Sender.Name != "users/123" {
		t.Errorf("Sender.Name = %q, want users/123", event.Sender.Name)
	}
	var msg googleChatMessage
	json.Unmarshal(event.Message, &msg)
	if msg.Text != "Hello bot!" {
		t.Errorf("Message.Text = %q, want Hello bot!", msg.Text)
	}
}

// TestSpaceResourceName pins the fix for the send_message double-prefix bug:
// processEvent (above) sets an INBOUND message's ChatID to event.Space.Name,
// which is always already "spaces/<id>" per Google's own API — so that is
// this channel's native chat_id format, and it is what an agent sees via
// ToolChatID(ctx) for its current Google Chat conversation. Forwarding that
// same already-prefixed value into send_message's chat_id argument for a
// proactive/out-of-band send (the tool's documented use case) hit
// sendBotMessage/StartTyping's endpoint construction, which used to add
// "spaces/" again unconditionally — producing a malformed, always-404
// ".../spaces/spaces/<id>/..." resource name. spaceResourceName normalizes
// to exactly one prefix regardless of which form the caller supplies,
// matching how send_message already tolerates a raw/unprefixed chat_id for
// every other channel.
func TestSpaceResourceName(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"already-prefixed (the channel's own native format) is not doubled", "spaces/AAAA", "spaces/AAAA"},
		{"bare space id gets the prefix added once", "AAAA", "spaces/AAAA"},
		{"empty input still gets a well-formed (if empty) resource name", "", "spaces/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := spaceResourceName(tc.input); got != tc.want {
				t.Errorf("spaceResourceName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestSpaceResourceName_EndpointNeverDoublePrefixed proves the fix at the
// exact call-site shape sendBotMessage/StartTyping use: building the
// messages endpoint from an already-prefixed chat_id (the value send_message
// would receive when an agent forwards its current turn's chat_id) must
// yield exactly one "spaces/" segment, never "spaces/spaces/".
func TestSpaceResourceName_EndpointNeverDoublePrefixed(t *testing.T) {
	for _, chatID := range []string{"spaces/AAAA", "AAAA"} {
		endpoint := fmt.Sprintf("%s/%s/messages", googleChatAPIBase, spaceResourceName(chatID))
		want := googleChatAPIBase + "/spaces/AAAA/messages"
		if endpoint != want {
			t.Errorf("endpoint for chat_id %q = %q, want %q", chatID, endpoint, want)
		}
		if strings.Contains(endpoint, "spaces/spaces/") {
			t.Errorf("endpoint for chat_id %q is double-prefixed: %q", chatID, endpoint)
		}
	}
}
