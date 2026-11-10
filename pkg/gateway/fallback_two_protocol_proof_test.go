package gateway

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// Hermetic fallback proof. design.md §5.1 and §7 R4.
//
// Two local HTTP stubs stand in for the providers. Stub A speaks the failure
// half of an Anthropic messages call (the status codes below). Stub B speaks
// an OpenAI chat completion and is the only place the token exists. Since
// effort-levels S-88/S-95 every fallback candidate runs the same attempt body
// as a single candidate, so it streams when the channel has a streamer
// (pkg/agent/loop_run_turn_attempt.go header); stub B therefore answers in
// the shape the request asks for: SSE when "stream" is true, a plain
// completion otherwise. The gateway, the provider clients and the transcript
// are real. This is not a mock of the agent.

const (
	proofPrimaryModel  = "MiniMax-M3.1-Flash-Preview"
	proofFallbackModel = "deepseek/deepseek-v4.1-flash"
	proofNote          = "Answered by the Fallback model (deepseek/deepseek-v4.1-flash) because MiniMax-M3.1-Flash-Preview was unavailable."
)

type proofHit struct {
	Model string
}

type proofStub struct {
	srv  *httptest.Server
	mu   sync.Mutex
	hits []proofHit
}

func (s *proofStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.hits)
}

func (s *proofStub) models() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.hits))
	for i, h := range s.hits {
		out[i] = h.Model
	}
	return out
}

func newCountingStub(t *testing.T, handler http.HandlerFunc) *proofStub {
	t.Helper()
	s := &proofStub{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		model, _ := body["model"].(string)
		s.mu.Lock()
		s.hits = append(s.hits, proofHit{Model: model})
		s.mu.Unlock()
		r.Body = io.NopCloser(strings.NewReader(string(raw)))
		handler(w, r)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func anthropicError(status int, retryAfter, errType, message string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		raw, _ := json.Marshal(map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    errType,
				"message": message,
			},
		})
		_, _ = w.Write(raw)
	}
}

// openAIChatCompletion answers both OpenAI chat shapes with the same token:
// plain Chat reads choices[0].message.content (pkg/providers/common/common.go)
// and ChatStream reads choices[0].delta.content from SSE data lines
// (pkg/providers/openai_compat/provider.go::parseStreamResponse). The
// request's own "stream" flag picks the shape, so whichever path the agent
// takes, the answer carries the token only this stub returns.
func openAIChatCompletion(token string) http.HandlerFunc {
	usage := map[string]any{
		"prompt_tokens":     1,
		"completion_tokens": 1,
		"total_tokens":      2,
	}
	body, err := json.Marshal(map[string]any{
		"id":     "x",
		"object": "chat.completion",
		"choices": []any{
			map[string]any{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": token,
				},
				"finish_reason": "stop",
			},
		},
		"usage": usage,
	})
	if err != nil {
		panic(err)
	}
	var sse strings.Builder
	for _, chunk := range []map[string]any{
		{
			"id": "x", "object": "chat.completion.chunk",
			"choices": []any{map[string]any{
				"index":         0,
				"delta":         map[string]any{"role": "assistant", "content": token},
				"finish_reason": "stop",
			}},
		},
		{"id": "x", "object": "chat.completion.chunk", "choices": []any{}, "usage": usage},
	} {
		raw, err := json.Marshal(chunk)
		if err != nil {
			panic(err)
		}
		sse.WriteString("data: " + string(raw) + "\n\n")
	}
	sse.WriteString("data: [DONE]\n\n")
	streamBody := []byte(sse.String())
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream bool `json:"stream"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &req)
		if !req.Stream {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(streamBody)
	}
}

// proofDataRoot is where the harness writes sessions. AgentHomeBasePath is
// the gateway home, and sessionsHomeFor returns its parent
// (pkg/agent/shared_session_store.go). Walking the gateway home itself
// never sees the transcript.
func proofDataRoot(gatewayHome string) string {
	return filepath.Dir(gatewayHome)
}

func countProofNotes(gatewayHome string) int {
	n := 0
	_ = filepath.Walk(proofDataRoot(gatewayHome), func(path string, info os.FileInfo, walkErr error) error {
		// Unreadable entries are skipped, never fatal: only readable
		// transcript.jsonl files are counted.
		if walkErr == nil && info != nil && !info.IsDir() && strings.HasSuffix(path, "transcript.jsonl") {
			if raw, readErr := os.ReadFile(path); readErr == nil {
				n += strings.Count(string(raw), proofNote)
			}
		}
		return nil
	})
	return n
}

func proofTextHas(frames []map[string]any, gatewayHome, token string) bool {
	for _, f := range frames {
		raw, _ := json.Marshal(f)
		if strings.Contains(string(raw), token) {
			return true
		}
	}
	found := false
	_ = filepath.Walk(proofDataRoot(gatewayHome), func(path string, info os.FileInfo, walkErr error) error {
		// Unreadable entries are skipped, never fatal (same rule as
		// countProofNotes).
		if walkErr == nil && info != nil && !info.IsDir() {
			if raw, readErr := os.ReadFile(path); readErr == nil && strings.Contains(string(raw), token) {
				found = true
			}
		}
		return nil
	})
	return found
}

func driveProofTurn(t *testing.T, gw *testutil.TestGateway, home string, timeout time.Duration) []map[string]any {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(gw.URL, "http") + "/api/v1/chat/ws"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if resp != nil {
		resp.Body.Close()
	}
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	send := func(frame wsClientFrameTestHelper) {
		t.Helper()
		raw, err := json.Marshal(frame)
		require.NoError(t, err)
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, raw))
	}
	send(wsClientFrameTestHelper{Type: "auth", Token: "dev-token"})
	send(wsClientFrameTestHelper{Type: "message", Content: "Say the token.", AgentID: "proof-agent"})

	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	var frames []map[string]any
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var frame map[string]any
		if json.Unmarshal(raw, &frame) != nil {
			continue
		}
		frames = append(frames, frame)
		if frame["type"] == "error" {
			break
		}
		// provider_fallback is emitted when the chain succeeds, before the
		// assistant answer is sent (pkg/agent/fallback_note.go
		// queueProviderFallbackNote). "done" is published after that answer
		// is persisted (pkg/gateway/websocket_streamer.go Finalize). The
		// transcript note is written after done. Stop on done, or when the
		// read deadline set above expires, not on the fallback frame.
		if frame["type"] != "done" {
			continue
		}
		noteDeadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(noteDeadline) && countProofNotes(home) < 1 {
			time.Sleep(50 * time.Millisecond)
		}
		break
	}
	return frames
}

func fallbackFrame(frames []map[string]any) map[string]any {
	for _, f := range frames {
		if f["type"] == "provider_fallback" {
			return f
		}
	}
	return nil
}

func errorFrameSeen(frames []map[string]any) bool {
	for _, f := range frames {
		if f["type"] == "error" {
			return true
		}
	}
	return false
}

// TestFallbackTwoProtocolProof is the §5.1 table. Each row boots a real
// gateway pointed at stub A (Anthropic-protocol primary) and stub B
// (OpenAI-protocol fallback). The assistant answer must contain a token that
// only stub B returns, and the transcript must contain the one fallback note,
// except the plain-400 row, which must not fall back.
func TestFallbackTwoProtocolProof(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		retryAfter   string
		errType      string
		message      string
		wantPrimary  int
		refused      bool
		wantFallback bool
		timeout      time.Duration
	}{
		{name: "429_retry_after_3600", status: 429, retryAfter: "3600", errType: "rate_limit_error", message: "rate limit", wantPrimary: 1, wantFallback: true, timeout: 20 * time.Second},
		{name: "429_no_retry_after", status: 429, errType: "rate_limit_error", message: "rate limit", wantPrimary: 3, wantFallback: true, timeout: 45 * time.Second},
		{name: "503", status: 503, errType: "api_error", message: "unavailable", wantPrimary: 1, wantFallback: true, timeout: 20 * time.Second},
		{name: "529_overloaded", status: 529, errType: "overloaded_error", message: "overloaded", wantPrimary: 1, wantFallback: true, timeout: 20 * time.Second},
		{name: "connection_refused", refused: true, wantFallback: true, timeout: 20 * time.Second},
		{name: "401_authentication_error", status: 401, errType: "authentication_error", message: "invalid", wantPrimary: 1, wantFallback: true, timeout: 20 * time.Second},
		{name: "400_does_not_fall_back", status: 400, errType: "invalid_request_error", message: "neutral text", wantPrimary: 1, wantFallback: false, timeout: 20 * time.Second},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Do not let the harness seed a real key from the environment.
			t.Setenv("OPENROUTER_API_KEY", "")
			t.Setenv("MINIMAX_API_KEY", "")
			t.Setenv("MINIMAX_API_KEY_CI", "")

			token := "fallback-only-" + tc.name
			var primary *proofStub
			var primaryURL string
			if tc.refused {
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)
				primaryURL = "http://" + ln.Addr().String()
				require.NoError(t, ln.Close())
			} else {
				primary = newCountingStub(t, anthropicError(tc.status, tc.retryAfter, tc.errType, tc.message))
				primaryURL = primary.srv.URL
			}
			fallback := newCountingStub(t, openAIChatCompletion(token))

			gw := testutil.StartTestGateway(t,
				testutil.WithProviderRows([]*config.ModelConfig{
					{
						Provider:  "minimax-coding-plan",
						Model:     proofPrimaryModel,
						APIBase:   primaryURL,
						APIKeyRef: "MINIMAX_API_KEY",
					},
					{
						Provider:  "openrouter",
						Model:     proofFallbackModel,
						APIBase:   fallback.srv.URL,
						APIKeyRef: "OPENROUTER_API_KEY",
					},
				}),
				testutil.WithCredentials(map[string]string{
					"MINIMAX_API_KEY":    "test-key-aaaa",
					"OPENROUTER_API_KEY": "test-key-bbbb",
				}),
				testutil.WithAgents([]config.AgentConfig{{
					ID:   "proof-agent",
					Name: "Proof",
					Type: config.AgentTypeCustom,
					Model: &config.AgentModelConfig{
						Provider: "minimax-coding-plan",
						Primary:  proofPrimaryModel,
					},
					FallbackModels: config.FallbackModelSlice{{
						Provider: "openrouter",
						Model:    proofFallbackModel,
					}},
				}}),
			)

			frames := driveProofTurn(t, gw, gw.HomeDir(), tc.timeout)
			notes := countProofNotes(gw.HomeDir())

			if tc.wantFallback {
				if primary != nil {
					require.Equal(t, tc.wantPrimary, primary.count(), "stub A call count; models=%v", primary.models())
					for _, m := range primary.models() {
						require.Equal(t, proofPrimaryModel, m, "stub A must be asked for the primary model")
					}
				}
				require.Equal(t, 1, fallback.count(), "stub B call count")
				require.Equal(t, []string{proofFallbackModel}, fallback.models())
				require.True(t, proofTextHas(frames, gw.HomeDir(), token), "the answer must contain the token only stub B returns")
				require.Equal(t, 1, notes, "the transcript must contain the fallback note exactly once")
				frame := fallbackFrame(frames)
				require.NotNil(t, frame, "a provider_fallback frame must be sent on the websocket")
				require.Equal(t, proofFallbackModel, frame["answered_model"])
				require.Equal(t, proofPrimaryModel, frame["unavailable_model"])
				return
			}

			require.Equal(t, tc.wantPrimary, primary.count(), "stub A call count on the negative row")
			require.Zero(t, fallback.count(), "plain 400 must not call the fallback")
			require.Zero(t, notes, "plain 400 must not write the fallback note")
			require.False(t, proofTextHas(frames, gw.HomeDir(), token), "the fallback token must not appear")
			require.Nil(t, fallbackFrame(frames), "plain 400 must not emit provider_fallback")
			require.True(t, errorFrameSeen(frames), "the error must be surfaced on the websocket")
		})
	}
}
