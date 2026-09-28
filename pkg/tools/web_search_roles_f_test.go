package tools

// web_search_roles_f_test.go — the failover ladder ("Which failures hop",
// spec lines 292–317) and the text rules ("What the tool returns",
// lines 361–393). Expected values derive from the class table and the
// exact result-text blocks.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// auth hops: HTTP 401 -> fallback answers, the auth line stays in the note.
func TestHop_Auth401_FallbackAnswers(t *testing.T) {
	f := newRolesSearchFixture(t, nil, nil)
	f.setHandler("tavily", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	res := f.run(map[string]any{"query": "golang"})
	if res.IsError {
		t.Fatalf("expected fallback success, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Search provider: duckduckgo (fallback)") {
		t.Fatalf("expected duckduckgo (fallback), got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "tavily: auth:") {
		t.Fatalf("expected auth class in note, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 1 {
		t.Fatalf("duckduckgo hits = %d, want 1", h)
	}
}

// rate_limit hops exactly once: HTTP 429 -> one fallback try, no third call.
func TestHop_RateLimit429_HopsOnce(t *testing.T) {
	f := newRolesSearchFixture(t, nil, nil)
	f.setHandler("tavily", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	res := f.run(map[string]any{"query": "golang"})
	if res.IsError {
		t.Fatalf("expected fallback success, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "tavily: rate_limit:") {
		t.Fatalf("expected rate_limit class, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 1 {
		t.Fatalf("duckduckgo hits = %d, want exactly 1", h)
	}
}

// upstream hops: HTTP 500 -> fallback answers.
func TestHop_Upstream500_FallbackAnswers(t *testing.T) {
	f := newRolesSearchFixture(t, nil, nil)
	f.setHandler("tavily", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	res := f.run(map[string]any{"query": "golang"})
	if res.IsError {
		t.Fatalf("expected fallback success, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Search provider: duckduckgo (fallback)") {
		t.Fatalf("expected duckduckgo (fallback), got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "tavily: upstream:") {
		t.Fatalf("expected upstream class, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 1 {
		t.Fatalf("duckduckgo hits = %d, want 1", h)
	}
}

// bad_response hops: HTTP 200 with a body that is not the documented JSON.
func TestHop_BadResponse200_UnparseableBody(t *testing.T) {
	f := newRolesSearchFixture(t, nil, nil)
	f.setHandler("tavily", jsonBody("not-json-at-all"))
	res := f.run(map[string]any{"query": "golang"})
	if res.IsError {
		t.Fatalf("expected fallback success, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "tavily: bad_response:") {
		t.Fatalf("expected bad_response class, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 1 {
		t.Fatalf("duckduckgo hits = %d, want 1", h)
	}
}

// DuckDuckGo non-200 is bad_response now (the status was ignored before) and
// hops when a fallback exists and provider was omitted.
func TestHop_DDGNon200_DefaultDDG_BraveFallback(t *testing.T) {
	f := newRolesSearchFixture(t, func(c *config.WebToolsConfig) {
		c.DefaultProvider = config.SearchProviderDuckDuckGo
		c.FallbackProvider = config.SearchProviderBrave
		c.Brave = config.BraveConfig{Enabled: true, APIKeyRef: envRefBrave}
	}, nil)
	f.setHandler("ddg", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	res := f.run(map[string]any{"query": "golang"})
	if res.IsError {
		t.Fatalf("expected fallback success, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Search provider: brave (fallback)") {
		t.Fatalf("expected brave (fallback), got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "duckduckgo: bad_response:") {
		t.Fatalf("expected ddg bad_response, got:\n%s", res.ForLLM)
	}
}

// rejected is final: HTTP 400 -> one error, no fallback request.
func TestFinal_Rejected400_NoHop(t *testing.T) {
	f := newRolesSearchFixture(t, nil, nil)
	f.setHandler("tavily", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	res := f.run(map[string]any{"query": "golang"})
	if !res.IsError {
		t.Fatalf("expected error, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "- tavily (default): rejected:") {
		t.Fatalf("expected rejected class, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 0 {
		t.Fatalf("duckduckgo hits = %d, want 0", h)
	}
}

// The ingest bound is final: an oversized body refuses that provider and no
// hop happens.
func TestFinal_IngestBound_NoHop(t *testing.T) {
	f := newRolesSearchFixture(t, nil, nil)
	big := strings.Repeat("x", 2<<20)
	f.setHandler("tavily", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(big))
	}))
	res := f.run(map[string]any{"query": "golang"})
	if !res.IsError {
		t.Fatalf("expected error, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "ingest bound") {
		t.Fatalf("expected ingest-bound failure naming the bound, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 0 {
		t.Fatalf("duckduckgo hits = %d, want 0", h)
	}
}

// Cancelled is final: a cancelled turn does not start the fallback.
func TestFinal_Cancelled_NoHop(t *testing.T) {
	f := newRolesSearchFixture(t, nil, nil)
	f.setHandler("tavily", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(500)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := f.runCtx(ctx, map[string]any{"query": "golang"})
	if !res.IsError {
		t.Fatalf("expected error, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "tavily (default): cancelled") {
		t.Fatalf("expected cancelled class, got:\n%s", res.ForLLM)
	}
	if h := f.hitsOf("ddg"); h != 0 {
		t.Fatalf("duckduckgo hits = %d, want 0", h)
	}
}

// D17a budget: an exhausted budget skips the fallback with the exact reason
// "no time budget remaining".
func TestFinal_BudgetExhausted_SkipsFallback(t *testing.T) {
	f := newRolesSearchFixture(t, nil, func(o *WebSearchToolOptions) {
		o.CallBudget = time.Nanosecond
	})
	res := f.run(map[string]any{"query": "golang"})
	if !res.IsError {
		t.Fatalf("expected error, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "no time budget remaining") {
		t.Fatalf("expected budget skip reason, got:\n%s", res.ForLLM)
	}
}

// FR-036: every provider message is truncated to 300 characters. A 429 whose
// body text is 500 characters produces a report line whose message part is
// capped; the whole line stays near 300+prefix.
func TestText_TruncationTo300(t *testing.T) {
	f := newRolesSearchFixture(t, nil, nil)
	long := strings.Repeat("a", 500)
	f.setHandler("tavily", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(429)
		_, _ = w.Write([]byte(long))
	}))
	f.setHandler("ddg", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(502)
	}))
	res := f.run(map[string]any{"query": "golang"})
	if !res.IsError {
		t.Fatalf("expected error, got: %s", res.ForLLM)
	}
	for _, line := range strings.Split(res.ForLLM, "\n") {
		if strings.HasPrefix(line, "- tavily (default): rate_limit: ") {
			msg := strings.TrimPrefix(line, "- tavily (default): rate_limit: ")
			if len(msg) > 300 {
				t.Fatalf("provider message not truncated: %d chars: %s...", len(msg), msg[:60])
			}
		}
	}
}

// FR-036: every provider message passes through the registered sensitive
// values (the Redact hook) before it reaches the result text.
func TestText_RedactHookApplied(t *testing.T) {
	f := newRolesSearchFixture(t, nil, func(o *WebSearchToolOptions) {
		o.Redact = func(s string) string {
			return strings.ReplaceAll(s, "leak-me-please", "[REDACTED]")
		}
	})
	f.setHandler("tavily", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte("upstream says leak-me-please"))
	}))
	f.setHandler("ddg", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(502)
	}))
	res := f.run(map[string]any{"query": "golang"})
	if !res.IsError {
		t.Fatalf("expected error, got: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "leak-me-please") {
		t.Fatalf("secret leaked into result text:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "[REDACTED]") {
		t.Fatalf("expected [REDACTED] in text, got:\n%s", res.ForLLM)
	}
}

// "A single line that names only the last provider is not a legal error when
// more than one role was involved": both failing -> both named.
func TestText_BothFail_BothNamed(t *testing.T) {
	f := newRolesSearchFixture(t, nil, nil)
	for _, n := range []string{"tavily", "ddg"} {
		name := n
		f.setHandler(name, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(502)
		}))
	}
	res := f.run(map[string]any{"query": "golang"})
	if !res.IsError {
		t.Fatalf("expected error, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "- tavily (default): upstream:") {
		t.Fatalf("expected tavily line, got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "- duckduckgo (fallback): bad_response:") {
		t.Fatalf("expected duckduckgo line, got:\n%s", res.ForLLM)
	}
}
