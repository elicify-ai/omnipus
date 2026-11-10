package providers

// Security proof candidate only. qa-lead owns any test that lands.
// Oracle: D20/dispatch log privacy plus the pre-swap bounded error behavior.
// Real factory/connector/SDK; only the HTTP provider is replaced by httptest.
// Not executed by security-lead. See proof-plan.md for scope and known gaps.

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

func TestSecuritySDKErrorBoundary_DoesNotExposePromptTail(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("ANTHROPIC_API_KEY", "ambient-key-fixture-not-a-secret")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "ambient-token-fixture-not-a-secret")
	withFixtureCatalog(t)

	const marker = "PRIVATE-PROMPT-TAIL-MUST-NOT-ENTER-APPLICATION-ERROR-LOG"
	// Beyond both the former 512-byte log preview and 8 KiB body cap.
	body := `{"type":"error","padding":"` + strings.Repeat("P", 9*1024) + `","error":{"type":"invalid_request_error","message":"request rejected"},"request_echo":"` + marker + `"}`
	var reject atomic.Bool
	type observedRequest struct{ path, key, authorization string }
	requests := make(chan observedRequest, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- observedRequest{r.URL.Path, r.Header.Get("X-Api-Key"), r.Header.Get("Authorization")}
		w.Header().Set("Content-Type", "application/json")
		if reject.Load() {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, body)
			return
		}
		_, _ = io.WriteString(w, `{"id":"msg_security","type":"message","role":"assistant","model":"MiniMax-M2.7","stop_reason":"end_turn","stop_sequence":null,"content":[{"type":"text","text":"OK"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	t.Cleanup(srv.Close)
	provider, model, err := CreateProviderFromConfig(&config.ModelConfig{
		Provider: "minimax", Model: "MiniMax-M2.7", APIBase: srv.URL + "/v1",
		APIKeyRef: keyRef(t, "SECURITY_GATE_CONFIGURED_KEY"),
	})
	if err != nil {
		t.Fatalf("factory positive control: %v", err)
	}
	response, err := provider.Chat(t.Context(), []Message{{Role: "user", Content: "Return OK"}}, nil, model, map[string]any{"max_tokens": 32})
	if err != nil || response == nil || response.Content != "OK" {
		t.Fatalf("healthy connector positive control failed: response=%v error=%v", response, err)
	}
	first := <-requests
	if first.path != "/v1/messages" || first.key != "test-key" || strings.Contains(first.authorization, "ambient-") {
		t.Fatalf("configured-endpoint/credential positive control failed: path=%q configured-key-used=%v ambient-auth-present=%v", first.path, first.key == "test-key", strings.Contains(first.authorization, "ambient-"))
	}

	// Characterization control: the former connector delegates error handling
	// to this unchanged boundary. It does not retain or render the tail marker.
	old := common.HandleErrorResponse(&http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, srv.URL)
	if old == nil || strings.Contains(old.Error(), marker) {
		t.Fatal("baseline bounded-error characterization control did not hold")
	}

	reject.Store(true)
	_, err = provider.Chat(t.Context(), []Message{{Role: "user", Content: "Return OK"}}, nil, model, map[string]any{"max_tokens": 32})
	if err == nil {
		t.Fatal("HTTP 400 was converted into success")
	}
	var sdkError *anthropic.Error
	if !errors.As(err, &sdkError) || sdkError.StatusCode != http.StatusBadRequest {
		t.Fatalf("instrument failed to reach SDK HTTP 400 boundary: type=%T error=%v", err, err)
	}
	// handleProviderResponse logs precisely err.Error(); credential replacement
	// cannot remove arbitrary prompt text. No llm_usage formatter is involved.
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("SDK swap exposes a private prompt tail beyond the old error-body cap: rendered_error_bytes=%d", len(err.Error()))
	}
}
