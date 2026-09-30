package tools

// web_fetch_fix_redirect_url_test.go — RED pack for issue #1056's carried-
// forward #1025 finding: "Redirect reports the requested URL, not the final
// landing URL — the `url` field echoes what was asked for."
//
// Oracle: the tool's own Description() (pkg/tools/web.go::WebFetchTool.
// Description) promises "Fetch a URL and extract readable content" as a
// single logical operation on whatever page the URL resolves to; an agent
// reasoning about citations or re-fetching needs the page it actually got,
// not the address it started from. This is also how every ordinary HTTP
// client and browser reports the location bar after a redirect — the
// standard the issue is holding this tool to. Go's own net/http.Client
// already tracks this per request: after following redirects,
// resp.Request.URL is the FINAL request's URL, not the first one
// (net/http.Client.Do's documented behaviour) — pkg/tools/web_fetch.go::
// doFetch has this available on the *http.Response it returns and discards
// it; pkg/tools/web_fetch.go::webFetchBuildResult always echoes back the
// original urlStr parameter instead.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFixWebFetch_RedirectReportsFinalLandingURL(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	landing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("landing page content"))
	}))
	defer landing.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, landing.URL+"/final", http.StatusFound) // 302
	}))
	defer redirector.Close()

	tool, err := NewWebFetchTool(50000, format, testFetchLimit)
	if err != nil {
		t.Fatalf("NewWebFetchTool: %v", err)
	}

	result := tool.Execute(context.Background(), map[string]any{"url": redirector.URL})
	if result.IsError {
		t.Fatalf("expected the redirect to be followed and succeed, got IsError=true: %s", result.ForLLM)
	}

	var resultMap map[string]any
	if err := json.Unmarshal([]byte(result.ForLLM), &resultMap); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, result.ForLLM)
	}

	gotURL, _ := resultMap["url"].(string)
	wantURL := landing.URL + "/final"
	if gotURL != wantURL {
		t.Fatalf(
			"result's \"url\" field must be the FINAL landing URL after the 302, not the originally-requested "+
				"one: got %q, want %q (requested %q)", gotURL, wantURL, redirector.URL)
	}
}

// TestFixWebFetch_NoRedirectStillReportsRequestedURL is the negative
// counterpart: without a redirect, the final URL IS the requested one, so a
// naive "always echo resp.Request.URL" fix must not regress the plain case
// either.
func TestFixWebFetch_NoRedirectStillReportsRequestedURL(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("no redirect here"))
	}))
	defer server.Close()

	tool, err := NewWebFetchTool(50000, format, testFetchLimit)
	if err != nil {
		t.Fatalf("NewWebFetchTool: %v", err)
	}

	result := tool.Execute(context.Background(), map[string]any{"url": server.URL})
	if result.IsError {
		t.Fatalf("expected success, got IsError=true: %s", result.ForLLM)
	}

	var resultMap map[string]any
	if err := json.Unmarshal([]byte(result.ForLLM), &resultMap); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, result.ForLLM)
	}

	gotURL, _ := resultMap["url"].(string)
	if gotURL != server.URL {
		t.Fatalf("with no redirect, \"url\" must still be the requested URL: got %q, want %q", gotURL, server.URL)
	}
}
