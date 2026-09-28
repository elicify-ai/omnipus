package tools

// web_search_fix_security_test.go — gate round 1, lane T-A (S1–S5).
// Oracle: ADR-096 D8, D9, D10, D16 and spec FR-017, FR-020, FR-026,
// AC-7, AC-9, tests 36, 47 and 51. Expected values come from that text,
// not from the current constructors.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/security"
)

// ddgOnlyRoles is a live resolver whose only usable provider is DuckDuckGo,
// so NewWebSearchTool builds the dynamic map without making Exa or SearXNG
// the legacy winner.
func ddgOnlyRoles() func() *config.WebToolsConfig {
	return func() *config.WebToolsConfig {
		return &config.WebToolsConfig{
			DefaultProvider:  config.SearchProviderDuckDuckGo,
			FallbackProvider: config.SearchProviderNone,
			DuckDuckGo:       config.DuckDuckGoConfig{Enabled: true},
		}
	}
}

func dnsName(labelLens ...int) string {
	parts := make([]string, len(labelLens))
	for i, n := range labelLens {
		parts[i] = strings.Repeat("a", n)
	}
	return strings.Join(parts, ".")
}

// TestFixS1_ExaClientProvenanceIsMakeSearchClient — S1. Exa must take its
// HTTP client from makeSearchClient, the same function every other provider
// uses (ADR-096 D10's client rule, applied to Exa because Exa sends a Bearer
// key to tools.web.exa.base_url).
func TestFixS1_ExaClientProvenanceIsMakeSearchClient(t *testing.T) {
	t.Run("ssrf checker", func(t *testing.T) {
		checker := security.NewSSRFChecker(nil)
		want, err := makeSearchClient(checker, "", searchTimeout)
		if err != nil {
			t.Fatalf("makeSearchClient: %v", err)
		}
		tool, err := NewWebSearchTool(WebSearchToolOptions{
			SSRFChecker:       checker,
			ExaAPIKey:         "exa-test-key",
			ExaBaseURL:        "https://api.exa.ai/search",
			DuckDuckGoEnabled: true,
			Roles:             ddgOnlyRoles(),
		})
		if err != nil {
			t.Fatalf("NewWebSearchTool: %v", err)
		}
		exa, ok := tool.dynamic[config.SearchProviderExa].(*ExaSearchProvider)
		if !ok || exa.client == nil {
			t.Fatalf("exa provider = %T, want *ExaSearchProvider with a client", tool.dynamic[config.SearchProviderExa])
		}
		// makeSearchClient with a checker returns SafeClient: a redirect check
		// and SafeClient's own timeout. A stock &http.Client{Timeout: searchTimeout}
		// has neither.
		if exa.client.CheckRedirect == nil || exa.client.Timeout != want.Timeout {
			t.Fatalf("Exa client is not makeSearchClient's SSRF client (CheckRedirect nil=%v timeout=%s, makeSearchClient timeout=%s)",
				exa.client.CheckRedirect == nil, exa.client.Timeout, want.Timeout)
		}
	})

	t.Run("no checker uses proxy-aware client", func(t *testing.T) {
		const proxy = "http://127.0.0.1:9"
		want, err := makeSearchClient(nil, proxy, searchTimeout)
		if err != nil {
			t.Fatalf("makeSearchClient: %v", err)
		}
		wantTr, ok := want.Transport.(*http.Transport)
		if !ok || wantTr.Proxy == nil {
			t.Fatal("makeSearchClient without a checker did not return a proxy-aware transport")
		}
		tool, err := NewWebSearchTool(WebSearchToolOptions{
			Proxy:             proxy,
			ExaAPIKey:         "exa-test-key",
			ExaBaseURL:        "https://api.exa.ai/search",
			DuckDuckGoEnabled: true,
			Roles:             ddgOnlyRoles(),
		})
		if err != nil {
			t.Fatalf("NewWebSearchTool: %v", err)
		}
		exa, ok := tool.dynamic[config.SearchProviderExa].(*ExaSearchProvider)
		if !ok || exa.client == nil {
			t.Fatalf("exa provider missing")
		}
		gotTr, ok := exa.client.Transport.(*http.Transport)
		if !ok || gotTr == nil || gotTr.Proxy == nil || exa.client.Timeout != want.Timeout {
			t.Fatalf("Exa client is a stock http.Client, not makeSearchClient (transport %T proxy-set=%v timeout=%s)",
				exa.client.Transport, ok && gotTr != nil && gotTr.Proxy != nil, exa.client.Timeout)
		}
	})
}

// TestFixS1_ExaSSRFBlocksPrivateBaseURLAndKey — S1 failure scenario. With the
// SSRF checker on, a base_url of 127.0.0.1 must not be dialed and must not
// receive the Bearer key.
func TestFixS1_ExaSSRFBlocksPrivateBaseURLAndKey(t *testing.T) {
	var hits atomic.Int32
	var sawAuth atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "" {
			sawAuth.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"n","url":"https://example.com/x"}]}`))
	}))
	t.Cleanup(srv.Close)

	tool, err := NewWebSearchTool(WebSearchToolOptions{
		SSRFChecker:       security.NewSSRFChecker(nil),
		ExaAPIKey:         "exa-test-key",
		ExaBaseURL:        srv.URL,
		DuckDuckGoEnabled: true,
		Roles:             ddgOnlyRoles(),
	})
	if err != nil {
		t.Fatalf("NewWebSearchTool: %v", err)
	}
	exa, ok := tool.dynamic[config.SearchProviderExa].(*ExaSearchProvider)
	if !ok || exa == nil {
		t.Fatalf("exa provider = %T, want *ExaSearchProvider", tool.dynamic[config.SearchProviderExa])
	}
	_, err = exa.Search(context.Background(), "golang", 1, "")
	if err == nil || !strings.Contains(err.Error(), "SSRF") {
		t.Fatalf("err = %v, want an SSRF refusal (hits=%d auth-sent=%v)", err, hits.Load(), sawAuth.Load())
	}
	if hits.Load() != 0 || sawAuth.Load() {
		t.Fatalf("private base_url received the Exa request (hits=%d auth-sent=%v)", hits.Load(), sawAuth.Load())
	}
}

// TestFixS2_SearXNGClientProvenanceIsMakeSearchClient — S2 / AC-9 / spec test
// 51. With no SSRF checker, SearXNG must still come from makeSearchClient
// (proxy-aware), not from its own &http.Client{Timeout: 10s}.
func TestFixS2_SearXNGClientProvenanceIsMakeSearchClient(t *testing.T) {
	const proxy = "http://127.0.0.1:9"
	want, err := makeSearchClient(nil, proxy, searchTimeout)
	if err != nil {
		t.Fatalf("makeSearchClient: %v", err)
	}
	wantTr, ok := want.Transport.(*http.Transport)
	if !ok || wantTr.Proxy == nil {
		t.Fatal("makeSearchClient without a checker did not return a proxy-aware transport")
	}
	tool, err := NewWebSearchTool(WebSearchToolOptions{
		Proxy:             proxy,
		SearXNGEnabled:    true,
		SearXNGBaseURL:    "https://searx.example",
		DuckDuckGoEnabled: true,
		Roles:             ddgOnlyRoles(),
	})
	if err != nil {
		t.Fatalf("NewWebSearchTool: %v", err)
	}
	sx, ok := tool.dynamic[config.SearchProviderSearXNG].(*SearXNGSearchProvider)
	if !ok || sx.client == nil {
		t.Fatalf("searxng provider = %T", tool.dynamic[config.SearchProviderSearXNG])
	}
	gotTr, ok := sx.client.Transport.(*http.Transport)
	if !ok || gotTr == nil || gotTr.Proxy == nil || sx.client.Timeout != want.Timeout {
		t.Fatalf("SearXNG client is a stock http.Client, not makeSearchClient (transport %T timeout=%s, want %s)",
			sx.client.Transport, sx.client.Timeout, want.Timeout)
	}
}

// TestFixS2_SearXNGOversizedBodyIsIngestBound — S2 / FR-020 / AC-9 / spec
// test 51. A body longer than the ingest bound is refused by that bound, and
// the refusal does not hop.
func TestFixS2_SearXNGOversizedBodyIsIngestBound(t *testing.T) {
	const bound = 64
	prefix := []byte(`{"results":[]}`)
	if len(prefix) >= bound {
		t.Fatalf("prefix len %d, want < %d", len(prefix), bound)
	}
	body := append(append([]byte{}, prefix...), bytes.Repeat([]byte(" "), bound+1-len(prefix))...)
	if len(body) != bound+1 {
		t.Fatalf("fixture len %d, want %d", len(body), bound+1)
	}

	var sxHits, ddgHits atomic.Int32
	sxSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sxHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(sxSrv.Close)
	ddgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ddgHits.Add(1)
		http.Error(w, "should not be called", http.StatusBadGateway)
	}))
	t.Cleanup(ddgSrv.Close)

	cfg := &config.WebToolsConfig{
		DefaultProvider:  config.SearchProviderSearXNG,
		FallbackProvider: config.SearchProviderDuckDuckGo,
		SearXNG:          config.SearXNGConfig{Enabled: true, BaseURL: sxSrv.URL},
		DuckDuckGo:       config.DuckDuckGoConfig{Enabled: true},
	}
	tool, err := NewWebSearchTool(WebSearchToolOptions{
		IngestBoundBytes:  bound,
		SearXNGEnabled:    true,
		SearXNGBaseURL:    sxSrv.URL,
		DuckDuckGoEnabled: true,
		DuckDuckGoBaseURL: ddgSrv.URL,
		Roles:             func() *config.WebToolsConfig { return cfg },
	})
	if err != nil {
		t.Fatalf("NewWebSearchTool: %v", err)
	}
	res := tool.Execute(context.Background(), map[string]any{"query": "golang"})
	if res == nil || !res.IsError || !strings.Contains(res.ForLLM, "ingest bound") {
		got := ""
		if res != nil {
			got = res.ForLLM
		}
		t.Fatalf("oversized SearXNG body was not refused by the ingest bound:\n%s", got)
	}
	if ddgHits.Load() != 0 {
		t.Fatalf("duckduckgo hits = %d, want 0 (an ingest-bound failure must not hop)", ddgHits.Load())
	}
	if sxHits.Load() == 0 {
		t.Fatal("searxng fixture was never called; the test did not exercise the body read")
	}
}

// TestFixS3_IPLiteralRefusedBeforeRequest — S3 / FR-026. An IP literal is not
// a hostname (validVideoEmbedHosts). include_domains and exclude_domains must
// refuse it before any request.
func TestFixS3_IPLiteralRefusedBeforeRequest(t *testing.T) {
	f := newRolesSearchFixture(t, nil, nil)
	literals := []string{"127.0.0.1", "169.254.169.254", "8.8.8.8"}
	for _, host := range literals {
		for _, key := range []string{"include_domains", "exclude_domains"} {
			before := f.hitsOf("tavily")
			res := f.run(map[string]any{
				"query": "golang",
				key:     []any{host},
			})
			if res == nil || !res.IsError || !strings.Contains(strings.ToLower(res.ForLLM), "ip") {
				got := ""
				if res != nil {
					got = res.ForLLM
				}
				t.Fatalf("%s %q was not refused as an IP literal:\n%s", key, host, got)
			}
			if got := f.hitsOf("tavily"); got != before {
				t.Fatalf("%s %q sent a request (hits %d → %d)", key, host, before, got)
			}
		}
	}
}

// TestFixS3_HostnameLength253Accepted254And10KBRejected — S3 / FR-026 / spec
// test 47. 253 characters is the DNS limit and is accepted. 254 and a 10 KB
// entry are rejected before any request.
func TestFixS3_HostnameLength253Accepted254And10KBRejected(t *testing.T) {
	name253 := dnsName(63, 63, 63, 61)
	name254 := dnsName(63, 63, 63, 62)
	if len(name253) != 253 || len(name254) != 254 {
		t.Fatalf("fixture lengths %d and %d, want 253 and 254", len(name253), len(name254))
	}
	entry10KB := strings.Repeat("b", 10*1024)

	f := newRolesSearchFixture(t, nil, nil)
	res := f.run(map[string]any{
		"query":           "golang",
		"include_domains": []any{name253},
	})
	if res == nil || res.IsError {
		got := ""
		if res != nil {
			got = res.ForLLM
		}
		t.Fatalf("253-character hostname rejected:\n%s", got)
	}
	gotDomains, _ := f.requestBodyMap(t, "tavily")["include_domains"].([]any)
	if len(gotDomains) != 1 || gotDomains[0] != name253 {
		t.Fatalf("include_domains = %v, want the 253-character name on the wire", gotDomains)
	}

	for _, bad := range []string{name254, entry10KB} {
		before := f.hitsOf("tavily")
		res = f.run(map[string]any{
			"query":           "golang",
			"include_domains": []any{bad},
		})
		if res == nil || !res.IsError || !strings.Contains(res.ForLLM, "253") {
			got := ""
			if res != nil {
				got = res.ForLLM
			}
			t.Fatalf("entry of length %d was not refused for the 253-character limit:\n%s", len(bad), got)
		}
		if hits := f.hitsOf("tavily"); hits != before {
			t.Fatalf("length %d sent a request (hits %d → %d)", len(bad), before, hits)
		}
	}
}

// TestFixS4_TavilyLiveBodyIncludeAnswerFalse — S4 / D8 / AC-7 / FR-017 / spec
// test 36. The live Tavily path (searchCaps) must send include_answer false.
// Asserted on the captured raw request body, then on the decoded value.
func TestFixS4_TavilyLiveBodyIncludeAnswerFalse(t *testing.T) {
	f := newRolesSearchFixture(t, nil, nil)
	res := f.run(map[string]any{"query": "golang"})
	if res == nil || res.IsError {
		got := ""
		if res != nil {
			got = res.ForLLM
		}
		t.Fatalf("expected a Tavily success, got:\n%s", got)
	}
	raw := f.lastBody("tavily")
	if !bytes.Contains(raw, []byte(`"include_answer":false`)) {
		t.Fatalf("captured raw Tavily body omits include_answer:false:\n%s", raw)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode captured body: %v\n%s", err, raw)
	}
	v, ok := decoded["include_answer"].(bool)
	if !ok || v {
		t.Fatalf("decoded include_answer = %#v, want false", decoded["include_answer"])
	}
}

// TestFixS5_ConstructorErrorIsNotUsableNotNetworkHop — S5 / honest-tool D16.
// A constructor error is logged with the provider id and the cause, and the
// agent is told "not usable: <reason>". It is not a network hop: the fallback
// is not called.
//
// The bad proxy is what makes makeSearchClient fail. SearXNG is the legacy
// winner so that, on today's code, NewWebSearchTool still returns a tool
// (SearXNG does not use makeSearchClient) and the swallowed Tavily error is
// what the call reports.
func TestFixS5_ConstructorErrorIsNotUsableNotNetworkHop(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "search-construct.log")
	if err := logger.EnableFileLogging(logPath); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(logger.DisableFileLogging)

	var sxHits atomic.Int32
	sxSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sxHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"SX","url":"https://example.com/sx","content":"c"}]}`))
	}))
	t.Cleanup(sxSrv.Close)

	t.Setenv("S5_TAVILY_KEY", "s5-tavily-key")
	cfg := &config.WebToolsConfig{
		DefaultProvider:  config.SearchProviderTavily,
		FallbackProvider: config.SearchProviderSearXNG,
		Tavily:           config.TavilyConfig{Enabled: true, APIKeyRef: "S5_TAVILY_KEY"},
		SearXNG:          config.SearXNGConfig{Enabled: true, BaseURL: sxSrv.URL},
	}
	tool, err := NewWebSearchTool(WebSearchToolOptions{
		Proxy:          "ftp://127.0.0.1:9",
		SearXNGEnabled: true,
		SearXNGBaseURL: sxSrv.URL,
		TavilyEnabled:  true,
		TavilyAPIKeys:  []string{"s5-tavily-key"},
		TavilyBaseURL:  "https://api.tavily.com/search",
		Roles:          func() *config.WebToolsConfig { return cfg },
	})
	if err != nil {
		t.Fatalf("constructor error aborted the tool instead of being reported at call time: %v", err)
	}
	res := tool.Execute(context.Background(), map[string]any{"query": "golang"})
	if res == nil || !res.IsError || !strings.Contains(res.ForLLM, "not usable:") {
		got := ""
		if res != nil {
			got = res.ForLLM
		}
		t.Fatalf("agent text missing \"not usable:\":\n%s", got)
	}
	if !strings.Contains(res.ForLLM, "unsupported proxy scheme") {
		t.Fatalf("agent text missing the constructor cause:\n%s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "provider not constructed") || strings.Contains(res.ForLLM, "network:") {
		t.Fatalf("constructor failure reported as a network hop:\n%s", res.ForLLM)
	}
	if sxHits.Load() != 0 {
		t.Fatalf("searxng hits = %d, want 0 (constructor failure must not hop)", sxHits.Load())
	}

	logger.DisableFileLogging()
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	text := string(logged)
	if !strings.Contains(text, "tavily") || !strings.Contains(text, "unsupported proxy scheme") {
		t.Fatalf("log missing provider id and cause:\n%s", text)
	}
}

// Gate round 2 (security NEW-1): a malformed credentialed proxy URL makes the
// URL parser's own error quote the whole URL. The S5 path surfaces constructor
// errors to the log and to the agent, so the proxy password must be redacted
// in both — never echoed.
func TestFixS5_ProxyCredentialsRedactedInConstructorError(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "search-proxy-creds.log")
	if err := logger.EnableFileLogging(logPath); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(logger.DisableFileLogging)

	const secret = "s3cretProxyPW"
	t.Setenv("S5R_TAVILY_KEY", "s5r-tavily-key")
	cfg := &config.WebToolsConfig{
		DefaultProvider: config.SearchProviderTavily,
		Tavily:          config.TavilyConfig{Enabled: true, APIKeyRef: "S5R_TAVILY_KEY"},
	}
	tool, err := NewWebSearchTool(WebSearchToolOptions{
		Proxy:         "http://proxyuser:" + secret + "@bad host:9",
		TavilyEnabled: true,
		TavilyAPIKeys: []string{"s5r-tavily-key"},
		TavilyBaseURL: "https://api.tavily.com/search",
		Roles:         func() *config.WebToolsConfig { return cfg },
	})
	if err != nil {
		t.Fatalf("constructor error aborted the tool: %v", err)
	}
	res := tool.Execute(context.Background(), map[string]any{"query": "golang"})
	if res == nil || !res.IsError {
		t.Fatalf("expected a not-usable error for the malformed proxy")
	}
	if strings.Contains(res.ForLLM, secret) {
		t.Fatalf("agent text leaks the proxy password:\n%s", res.ForLLM)
	}
	logger.DisableFileLogging()
	raw, rerr := os.ReadFile(logPath)
	if rerr != nil {
		t.Fatalf("read log: %v", rerr)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("log leaks the proxy password:\n%s", raw)
	}
	if !strings.Contains(string(raw), "proxyuser") && !strings.Contains(res.ForLLM, "invalid proxy URL") {
		t.Fatalf("the cause must still be reported (redacted, not dropped):\n%s\n%s", res.ForLLM, raw)
	}
}
