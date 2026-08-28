package tools

// web_ingest_test.go — ADR-066 D10 (FR-038, B-46, DS-7 #4–#5): every search
// provider (Brave, DuckDuckGo, Perplexity, GLM, Baidu, Tavily) is bounded
// at ingest_bound_bytes — a response of bound+1 bytes is a tool failure
// naming the bound; 2 MiB and exactly the bound are accepted with nothing
// truncated (the two former 1 MiB LimitReader sites — GLM, Baidu — are
// raised to the bound). The fetch_url fallback limit is 8,000,000 bytes.
//
// Test 24 in the spec's test table.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const (
	ingestTestBound    = 8_000_000
	ingestTestTwoMiB   = 2 * 1024 * 1024
	ingestTestOverflow = ingestTestBound + 1
	ingestTestMarker   = "END-OF-PAYLOAD"
)

// rewriteTransport sends every request to the fake server regardless of the
// provider's hard-coded upstream host.
type rewriteTransport struct{ target *url.URL }

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = r.target.Scheme
	req.URL.Host = r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

// exactBody builds a response body of exactly want bytes from the given
// prefix/suffix by padding the middle with 'a'.
func exactBody(t *testing.T, prefix, suffix string, want int) string {
	t.Helper()
	pad := want - len(prefix) - len(suffix)
	if pad < 0 {
		t.Fatalf("exactBody: want %d smaller than envelope %d", want, len(prefix)+len(suffix))
	}
	return prefix + strings.Repeat("a", pad) + suffix
}

// providerCase describes how one provider is built against a fake server
// and what a valid body of a given size looks like for it.
type providerCase struct {
	name  string
	build func(client *http.Client, base string) SearchProvider
	body  func(t *testing.T, want int) string
}

func TestIngestBound_SearchProvidersAll5(t *testing.T) {
	providers := []providerCase{
		{
			name: "Brave",
			build: func(c *http.Client, _ string) SearchProvider {
				return &BraveSearchProvider{keyPool: NewAPIKeyPool([]string{"k"}), client: c, ingestBound: ingestTestBound}
			},
			body: func(t *testing.T, want int) string {
				return exactBody(t, `{"web":{"results":[{"title":"t","url":"https://x","description":"`,
					ingestTestMarker+`"}]}}`, want)
			},
		},
		{
			name: "DuckDuckGo",
			build: func(c *http.Client, _ string) SearchProvider {
				return &DuckDuckGoSearchProvider{client: c, ingestBound: ingestTestBound}
			},
			body: func(t *testing.T, want int) string {
				return exactBody(t,
					`<html><body><a class="result__a" href="https://x">t</a><a class="result__snippet">`,
					ingestTestMarker+`</a></body></html>`, want)
			},
		},
		{
			name: "Perplexity",
			build: func(c *http.Client, _ string) SearchProvider {
				return &PerplexitySearchProvider{keyPool: NewAPIKeyPool([]string{"k"}), client: c, ingestBound: ingestTestBound}
			},
			body: func(t *testing.T, want int) string {
				return exactBody(t, `{"choices":[{"message":{"content":"`, ingestTestMarker+`"}}]}`, want)
			},
		},
		{
			name: "GLM",
			build: func(c *http.Client, base string) SearchProvider {
				return &GLMSearchProvider{apiKey: "k", baseURL: base, client: c, ingestBound: ingestTestBound}
			},
			body: func(t *testing.T, want int) string {
				return exactBody(t, `{"search_result":[{"title":"t","link":"https://x","content":"`,
					ingestTestMarker+`"}]}`, want)
			},
		},
		{
			name: "Baidu",
			build: func(c *http.Client, base string) SearchProvider {
				return &BaiduSearchProvider{apiKey: "k", baseURL: base, client: c, ingestBound: ingestTestBound}
			},
			body: func(t *testing.T, want int) string {
				return exactBody(t, `{"references":[{"title":"t","url":"https://x","content":"`,
					ingestTestMarker+`"}]}`, want)
			},
		},
		{
			// Tavily is not named in FR-038's list, so it kept a bare
			// io.ReadAll: a compromised endpoint (or an operator-set custom
			// base URL) could buffer an arbitrarily large body into the
			// gateway process before any parsing or capping. D4 protects the
			// window; it cannot protect the process — which is what D10
			// exists for.
			name: "Tavily",
			build: func(c *http.Client, base string) SearchProvider {
				return &TavilySearchProvider{
					keyPool: NewAPIKeyPool([]string{"k"}), baseURL: base,
					client: c, ingestBound: ingestTestBound,
				}
			},
			body: func(t *testing.T, want int) string {
				return exactBody(t, `{"results":[{"title":"t","url":"https://x","content":"`,
					ingestTestMarker+`"}]}`, want)
			},
		},
	}

	sizes := []struct {
		name   string
		bytes  int
		accept bool
	}{
		{"2MiB accepted", ingestTestTwoMiB, true},
		{"exactly-bound accepted", ingestTestBound, true},
		{"DS-7#4 bound+1 failure", ingestTestOverflow, false},
	}

	for _, pc := range providers {
		t.Run(pc.name, func(t *testing.T) {
			for _, sz := range sizes {
				t.Run(sz.name, func(t *testing.T) {
					body := pc.body(t, sz.bytes)
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.Header().Set("Content-Length", fmt.Sprint(len(body)))
						_, _ = w.Write([]byte(body))
					}))
					t.Cleanup(srv.Close)
					target, _ := url.Parse(srv.URL)
					client := &http.Client{Transport: rewriteTransport{target: target}, Timeout: 60 * time.Second}

					out, err := pc.build(client, srv.URL).Search(context.Background(), "q", 5, "")
					if sz.accept {
						if err != nil {
							t.Fatalf("expected %d-byte response accepted, got: %v", sz.bytes, err)
						}
						// DuckDuckGo's extractor is a regex over the body; the
						// other four surface the padded field verbatim. Either
						// way the payload marker must survive — nothing is
						// truncated at ingest.
						if pc.name != "DuckDuckGo" && !strings.Contains(out, ingestTestMarker) {
							t.Fatalf("payload truncated: marker missing from output (len=%d)", len(out))
						}
						return
					}
					if err == nil {
						t.Fatalf("expected a tool failure for %d bytes, got output len=%d", sz.bytes, len(out))
					}
					var ibe *IngestBoundError
					if !errors.As(err, &ibe) || ibe.Bound != ingestTestBound {
						t.Fatalf("expected *IngestBoundError{Bound:%d}, got %T: %v", ingestTestBound, err, err)
					}
					if !strings.Contains(err.Error(), fmt.Sprint(ingestTestBound)) ||
						!strings.Contains(err.Error(), "ingest_bound_bytes") {
						t.Fatalf("error must name the bound and the setting; got: %v", err)
					}
				})
			}
		})
	}

	t.Run("DS-7#5 fetch_url fallback is 8,000,000", func(t *testing.T) {
		withPrivateWebFetchHostsAllowed(t)
		tool, err := NewWebFetchTool(50000, "text", 0) // 0 → fallback
		if err != nil {
			t.Fatalf("NewWebFetchTool: %v", err)
		}
		if tool.fetchLimitBytes != ingestTestBound {
			t.Fatalf("fetch_url fallback = %d, want %d", tool.fetchLimitBytes, ingestTestBound)
		}
		body := strings.Repeat("a", ingestTestOverflow)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)
		res := tool.Execute(context.Background(), map[string]any{"url": srv.URL})
		if !res.IsError {
			t.Fatalf("expected failure for %d bytes, got success (len=%d)", ingestTestOverflow, len(res.ForLLM))
		}
		if !strings.Contains(res.ForLLM, fmt.Sprint(ingestTestBound)) {
			t.Fatalf("failure must name the limit; got: %s", res.ForLLM)
		}
	})

	t.Run("WebSearchToolOptions.IngestBoundBytes reaches the provider", func(t *testing.T) {
		ws, err := NewWebSearchTool(WebSearchToolOptions{DuckDuckGoEnabled: true, IngestBoundBytes: 123})
		if err != nil {
			t.Fatalf("NewWebSearchTool: %v", err)
		}
		ddg, ok := ws.provider.(*DuckDuckGoSearchProvider)
		if !ok {
			t.Fatalf("provider = %T, want *DuckDuckGoSearchProvider", ws.provider)
		}
		if ddg.ingestBound != 123 {
			t.Fatalf("ingestBound = %d, want 123", ddg.ingestBound)
		}
	})
}
