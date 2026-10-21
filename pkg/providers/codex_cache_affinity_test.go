package providers

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	anthropicprovider "github.com/elicify-ai/omnipus/pkg/providers/anthropic"
	azureprovider "github.com/elicify-ai/omnipus/pkg/providers/azure"
	"github.com/elicify-ai/omnipus/pkg/providers/openai_compat"
)

const affinityFixtureCredential = "affinity-fixture-credential"

// The oracle is OpenAI Codex commit bc5957eac9e89e66f990ed490d11e625a4a3b02c:
// ChatGPT's session-id header uses the effective conversation cache key.
func TestChatGPTCacheAffinity(t *testing.T) {
	t.Setenv("OPENAI_CUSTOM_HEADERS", "")
	t.Run("subscription_session_is_stable_private_and_request_scoped", testSubscriptionSessionAffinity)
	t.Run("subscription_overlapping_sessions", testOverlappingSubscriptionSessionAffinity)
	t.Run("other_provider_requests_are_byte_identical_to_release", testOtherProviderAffinityRequests)
}

func testSubscriptionSessionAffinity(t *testing.T) {
	const (
		sessionA     = "session_01M5EZC6E0YK7XZNRJJR50QEZ0"
		sessionB     = "session_01M5EZC6E0YK7XZNRJJR50QEZ1"
		instructions = "private-instruction-canary"
		userText     = "private-user-content-canary"
	)
	completed := "event: response.completed\ndata: " +
		`{"type":"response.completed","response":{"id":"resp_fixture","status":"completed",` +
		`"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}}` + "\n\n"
	baseURL, captured := newAffinityCaptureServer(t, completed, true)
	// A single provider serves interleaved conversations. Affinity must not be
	// installed on its shared client or retained when a later call has no key.
	provider := newCodexProviderWithBaseURL(affinityFixtureCredential, "fixture-account", baseURL)
	cases := []struct {
		name    string
		options map[string]any
		want    string
	}{
		{"session_a_first", map[string]any{"prompt_cache_key": sessionA}, sessionA},
		{"session_a_next", map[string]any{"prompt_cache_key": sessionA}, sessionA},
		{"session_b", map[string]any{"prompt_cache_key": sessionB}, sessionB},
		{"session_a_after_b", map[string]any{"prompt_cache_key": sessionA}, sessionA},
		{"nil_options", nil, ""},
		{"missing_key", map[string]any{}, ""},
		{"empty_key", map[string]any{"prompt_cache_key": ""}, ""},
		{"null_key", map[string]any{"prompt_cache_key": nil}, ""},
		{"wrong_type", map[string]any{"prompt_cache_key": 42}, ""},
		{"session_a_after_omissions", map[string]any{"prompt_cache_key": sessionA}, sessionA},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			messages := []Message{
				{Role: "system", Content: instructions},
				{Role: "user", Content: userText + strconv.Itoa(i)},
			}
			response, err := provider.Chat(t.Context(), messages, nil, "gpt-6.1-sol", tc.options)
			if err != nil {
				t.Fatalf("local subscription call: %v", err)
			}
			if response.Content != "ok" {
				t.Errorf("response content = %q, want ok", response.Content)
			}
			got := <-captured
			wantHeaderCount := "1"
			if tc.want == "" {
				wantHeaderCount = "0"
			}
			if got["session-id"] != tc.want || got["session-id-count"] != wantHeaderCount {
				t.Errorf("session-id = %q (count %s), want %q (count %s)",
					got["session-id"], got["session-id-count"], tc.want, wantHeaderCount)
			}
			if got["method"] != http.MethodPost || got["path"] != "/responses" {
				t.Errorf("request target = %s %s, want POST /responses", got["method"], got["path"])
			}
			var body map[string]any
			if err := json.Unmarshal([]byte(got["body"]), &body); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			key, exists := body["prompt_cache_key"]
			if tc.want == "" && exists {
				t.Errorf("body prompt_cache_key = %v, want absent", key)
			}
			if tc.want != "" && key != tc.want {
				t.Errorf("body prompt_cache_key = %v, want %q", key, tc.want)
			}
			for _, private := range []string{affinityFixtureCredential, instructions, userText} {
				if strings.Contains(got["session-id"], private) {
					t.Error("session affinity contains a credential or private message canary")
				}
			}
		})
	}
}

func testOtherProviderAffinityRequests(t *testing.T) {
	// Characterization only: the exact body bytes and application headers were
	// captured on untouched release 03dc3a97a before this subscription-only fix.
	// These snapshots guard noninterference, not each provider's protocol design.
	data, err := os.ReadFile("testdata/chatgpt-cache-affinity-other-providers.json")
	if err != nil {
		t.Fatal(err)
	}
	var baseline map[string]map[string]string
	if err := json.Unmarshal(data, &baseline); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"openai", "openrouter", "xai", "azure", "anthropic"} {
		t.Run(id, func(t *testing.T) {
			baseURL, captured := newAffinityCaptureServer(t, affinityOtherProviderReply(id), false)
			provider := affinityOtherProvider(t, id, baseURL)
			messages := []Message{
				{Role: "system", Content: "affinity-fixture-instructions"},
				{Role: "user", Content: "affinity-fixture-user"},
			}
			response, err := provider.Chat(t.Context(), messages, nil, "affinity-fixture-model",
				map[string]any{"prompt_cache_key": "affinity-fixture-session"})
			if err != nil {
				t.Fatalf("local %s call: %v", id, err)
			}
			if response.Content != "ok" {
				t.Errorf("response content = %q, want ok", response.Content)
			}
			got := <-captured
			if got["session-id-count"] != "0" {
				t.Error("non-subscription provider received session-id")
			}
			delete(got, "session-id-count") // Captured separately from release's byte snapshots.
			want, exists := baseline[id]
			if !exists {
				t.Fatalf("release baseline missing %s", id)
			}
			if got["body"] != want["body"] {
				t.Errorf("%s request bytes changed:\ngot  %s\nwant %s", id, got["body"], want["body"])
			}
			if !maps.Equal(got, want) {
				t.Errorf("%s request or application headers differ from release: got %#v, want %#v", id, got, want)
			}
		})
	}
}

func newAffinityCaptureServer(t *testing.T, reply string, stream bool) (string, <-chan map[string]string) {
	t.Helper()
	captured := make(chan map[string]string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read local request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		captured <- map[string]string{
			"method": r.Method, "path": r.URL.Path, "body": string(body),
			"session-id":         r.Header.Get("Session-Id"),
			"session-id-count":   strconv.Itoa(len(r.Header.Values("Session-Id"))),
			"x-grok-conv-id":     r.Header.Get("X-Grok-Conv-Id"),
			"x-session-affinity": r.Header.Get("X-Session-Affinity"),
			"content-type":       r.Header.Get("Content-Type"),
		}
		contentType := "application/json"
		if stream {
			contentType = "text/event-stream"
		}
		w.Header().Set("Content-Type", contentType)
		if _, err := io.WriteString(w, reply); err != nil {
			t.Errorf("write local fixture reply: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL, captured
}

func affinityOtherProvider(t *testing.T, id, baseURL string) LLMProvider {
	t.Helper()
	var (
		provider LLMProvider
		err      error
	)
	switch id {
	case "azure":
		provider, err = azureprovider.NewProvider(affinityFixtureCredential, baseURL, "")
	case "anthropic":
		provider = anthropicprovider.NewProviderWithBaseURL(affinityFixtureCredential, baseURL).WithProviderID(id)
	default:
		provider, err = NewHTTPProviderWithTimeouts(affinityFixtureCredential, baseURL+"/v1", "", "", 0, 0, nil,
			openai_compat.WithProviderID(id))
	}
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func affinityOtherProviderReply(id string) string {
	switch id {
	case "azure":
		return `{"id":"resp_fixture","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	case "anthropic":
		return `{"id":"msg_fixture","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"affinity-fixture-model","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
	default:
		return `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	}
}
