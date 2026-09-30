package tools

// web_search_roles_fixture_test.go — shared fixture for the ADR-096 WS-TOOL
// RED pack. Oracle: docs/internal/specs/web-search-provider-model-spec.md.
// Isolation: the REAL WebSearchTool and REAL provider structs run; only the
// network edge is faked (one httptest server per provider id). Usability is
// evaluated through config.WebToolsConfig.UsableSearchProvider.

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// Env refs. A test needing an UNUSABLE keyed provider repoints that
// provider's APIKeyRef at a name that is never set (envRefMissing*).
const (
	envRefTavily      = "WSFX_TAVILY_KEY"
	envRefBrave       = "WSFX_BRAVE_KEY"
	envRefPerplexity  = "WSFX_PPLX_KEY"
	envRefGLM         = "WSFX_GLM_KEY"
	envRefExa         = "WSFX_EXA_KEY"
	envRefBaidu       = "WSFX_BAIDU_KEY"
	envRefMissingTav  = "WSFX_TAVILY_KEY_MISSING"
	envRefMissingBrav = "WSFX_BRAVE_KEY_MISSING"
)

// swapHandler lets a test replace one provider's response behaviour after
// its httptest server is built.
type swapHandler struct {
	mu sync.Mutex
	h  http.HandlerFunc
}

func (s *swapHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	h := s.h
	s.mu.Unlock()
	if h != nil {
		h(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{}`))
}

func (s *swapHandler) set(h http.HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.h = h
}

// jsonBody is the stock well-formed-200 handler a test installs or overrides.
func jsonBody(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// capturedRequest records the last request a fixture server saw.
type capturedRequest struct {
	method string
	auth   string
	body   []byte
}

// rolesSearchFixture holds one httptest server per provider, keyed by
// retained provider id ("tavily", "ddg", "brave", "perplexity", "glm",
// "exa", "baidu").
type rolesSearchFixture struct {
	servers  map[string]*httptest.Server
	swaps    map[string]*swapHandler
	captures map[string]*capturedRequest
	capMu    sync.Mutex
	hits     map[string]*int32
	cfg      *config.WebToolsConfig
	tool     *WebSearchTool
}

// newRolesSearchFixture builds the per-provider server set, the canonical
// config (Tavily usable default, fallback absent -> auto DuckDuckGo per the
// spec's R3/R6 shape), and the tool with Roles wired. The two mutators adjust
// config and options before the tool is constructed.
func newRolesSearchFixture(t *testing.T,
	cfgMutate func(*config.WebToolsConfig),
	optsMutate func(*WebSearchToolOptions),
) *rolesSearchFixture {
	t.Helper()

	// Every canonical key ref resolves in the environment; the option keys
	// match the env values so usability (via config) and HTTP (via providers)
	// agree with one another.
	t.Setenv(envRefTavily, "wsfx-tavily-key")
	t.Setenv(envRefBrave, "wsfx-brave-key")
	t.Setenv(envRefPerplexity, "wsfx-pplx-key")
	t.Setenv(envRefGLM, "wsfx-glm-key")
	t.Setenv(envRefExa, "wsfx-exa-key")
	t.Setenv(envRefBaidu, "wsfx-baidu-key")

	f := &rolesSearchFixture{
		servers:  map[string]*httptest.Server{},
		swaps:    map[string]*swapHandler{},
		captures: map[string]*capturedRequest{},
		hits:     map[string]*int32{},
	}

	mk := func(name string) {
		sw := &swapHandler{}
		capReq := &capturedRequest{}
		count := new(int32)
		f.swaps[name] = sw
		f.captures[name] = capReq
		f.hits[name] = count
		f.servers[name] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(count, 1)
			body, _ := readIngestBounded(r.Body, 1<<20, "test-capture")
			f.capMu.Lock()
			capReq.body = body
			capReq.method = r.Method
			capReq.auth = r.Header.Get("Authorization")
			f.capMu.Unlock()
			sw.ServeHTTP(w, r)
		}))
	}
	for _, name := range []string{"tavily", "ddg", "brave", "perplexity", "glm", "exa", "baidu"} {
		mk(name)
	}

	// Stock well-formed 200 per provider; individual tests swap these out.
	f.setHandler("tavily", jsonBody(`{"results":[{"title":"Tavily Result","url":"https://example.com/tavily","content":"tavily snippet"}]}`))
	f.setHandler("ddg", jsonBody(`<html><body><a class="result__a" href="https://example.com/ddg1">DDG One</a><a class="result__a" href="https://example.com/ddg2">DDG Two</a></body></html>`))
	f.setHandler("brave", jsonBody(`{"web":{"results":[{"title":"Brave Result","url":"https://example.com/brave","description":"b"}]}}`))
	f.setHandler("perplexity", jsonBody(`{"choices":[{"message":{"content":"prose answer"}}],"citations":["https://cite.example.com/one"]}`))
	f.setHandler("glm", jsonBody(`{"search_result":[{"title":"GLM Result","link":"https://example.com/glm","content":"g"}]}`))
	f.setHandler("exa", jsonBody(`{"results":[{"title":"Exa Result","url":"https://example.com/exa"}]}`))
	f.setHandler("baidu", jsonBody(`{"results":[{"title":"Baidu Result","url":"https://example.com/baidu","abstract":"b"}]}`))

	// Canonical config: Tavily usable default; fallback absent -> auto
	// DuckDuckGo; DuckDuckGo usable. This is the spec's R3/R6 shape.
	cfg := &config.WebToolsConfig{
		DefaultProvider:  config.SearchProviderTavily,
		FallbackProvider: "",
		Tavily: config.TavilyConfig{
			Enabled:   true,
			APIKeyRef: envRefTavily,
		},
		DuckDuckGo: config.DuckDuckGoConfig{Enabled: true},
	}
	if cfgMutate != nil {
		cfgMutate(cfg)
	}

	opts := WebSearchToolOptions{
		IngestBoundBytes:      1 << 20,
		BraveAPIKeys:          []string{"wsfx-brave-key"},
		BraveEnabled:          true,
		BraveBaseURL:          f.servers["brave"].URL,
		TavilyAPIKeys:         []string{"wsfx-tavily-key"},
		TavilyBaseURL:         f.servers["tavily"].URL,
		TavilyEnabled:         true,
		DuckDuckGoEnabled:     true,
		DuckDuckGoBaseURL:     f.servers["ddg"].URL,
		PerplexityAPIKeys:     []string{"wsfx-pplx-key"},
		PerplexityBaseURL:     f.servers["perplexity"].URL,
		PerplexityEnabled:     true,
		PerplexityContextSize: "",
		GLMSearchAPIKey:       "wsfx-glm-key",
		GLMSearchBaseURL:      f.servers["glm"].URL,
		GLMSearchEnabled:      true,
		GLMContentSize:        "medium",
		BaiduSearchAPIKey:     "wsfx-baidu-key",
		BaiduSearchBaseURL:    f.servers["baidu"].URL,
		BaiduSearchEnabled:    true,
		ExaAPIKey:             "wsfx-exa-key",
		ExaBaseURL:            f.servers["exa"].URL,
		ExaEnabled:            true,
		Redact:                nil,
		Roles: func() *config.WebToolsConfig {
			return cfg
		},
	}
	if optsMutate != nil {
		optsMutate(&opts)
	}

	tool, err := NewWebSearchTool(opts)
	if err != nil {
		t.Fatalf("NewWebSearchTool failed: %v", err)
	}
	f.cfg = cfg
	f.tool = tool
	return f
}

// run executes search_web with args on the fixture's tool.
func (f *rolesSearchFixture) run(args map[string]any) *ToolResult {
	return f.tool.Execute(context.Background(), args)
}

// hitsOf returns how many requests the named provider's server received.
func (f *rolesSearchFixture) hitsOf(name string) int {
	return int(atomic.LoadInt32(f.hits[name]))
}

// lastBody returns the last request body the named provider's server saw.
func (f *rolesSearchFixture) lastBody(name string) []byte {
	f.capMu.Lock()
	defer f.capMu.Unlock()
	return f.captures[name].body
}

// lastAuth returns the Authorization header the named provider's server saw.
func (f *rolesSearchFixture) lastAuth(name string) string {
	f.capMu.Lock()
	defer f.capMu.Unlock()
	return f.captures[name].auth
}

// lastMethod returns the HTTP method the named provider's server saw.
func (f *rolesSearchFixture) lastMethod(name string) string {
	f.capMu.Lock()
	defer f.capMu.Unlock()
	return f.captures[name].method
}

// setHandler replaces a provider's response behaviour.
func (f *rolesSearchFixture) setHandler(name string, h http.HandlerFunc) {
	f.swaps[name].set(h)
}

// requestBodyMap unmarshals the last request body of the named provider into
// a generic map for JSON field assertions.
func (f *rolesSearchFixture) requestBodyMap(t *testing.T, name string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(f.lastBody(name), &m); err != nil {
		t.Fatalf("unmarshal %s request body: %v", name, err)
	}
	return m
}

// runCtx executes search_web with an explicit context (cancellation tests).
func (f *rolesSearchFixture) runCtx(ctx context.Context, args map[string]any) *ToolResult {
	return f.tool.Execute(ctx, args)
}

// deadServerURL returns the URL of a listener that accepts every connection
// and closes it immediately without writing a response — a deterministic
// network-class failure for any HTTP client that dials it.
//
// It does NOT use httptest.NewServer-then-Close: that pattern frees the OS
// port back into the machine-wide ephemeral pool while the test keeps using
// the URL. `go test ./...` runs many package test binaries as separate OS
// processes concurrently, and any of those other processes' own listeners
// (an httptest server in a different package, or anything else on the
// machine) can be handed that exact freed port before this test's own
// request lands, turning "network failure" into "200 from a stranger's
// server" (observed in CI: PR #1000 race run 36462420598, job
// 109064538871 — TestR8_NoFallbackInvented_WhenDDGDisabled saw a live tavily
// response instead of the expected network error). Holding our own listener
// open for the test's lifetime (closed only in t.Cleanup) makes that
// rebinding impossible: nothing else can bind this port while we hold it.
//
// Every caller's assertion in this package matches on the "network:" class
// text (pkg/tools/web_search.go's classifySearchFailure wraps any
// client.Do/body-read error — connection refused, reset, or a response with
// no bytes — into "request failed"/"failed to read response", both
// classNetwork), not on a specific underlying error string, so an
// accept-then-close failure satisfies the same oracle a connection-refused
// failure did.
func deadServerURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("deadServerURL: listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
	})
	return "http://" + ln.Addr().String()
}
