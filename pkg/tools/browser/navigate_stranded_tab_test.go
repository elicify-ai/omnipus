//go:build !lite

// navigate_stranded_tab_test.go — SECURITY regression (2026-08-13): a
// navigation that FAILS TO COMPLETE must not leave the tab parked on the
// target URL.
//
// The gap: browser_navigate / browser_open_tab returned the page-load error
// immediately and only navigated away inside the post-redirect SSRF branch,
// which is reached exclusively when the load SUCCEEDS. A public URL that
// redirects to an internal host which merely responds SLOWLY therefore
// produced a page-load timeout, an error result — and a tab still pointed at
// the internal page, which Chrome was free to finish loading afterwards. The
// next browser_get_text/browser_screenshot on that tab read the internal
// content: an SSRF bypass achieved by timing alone. Found on macOS, where
// link-local addresses hang instead of failing fast, but not macOS-specific:
// any slow internal host opens the same window on any platform.

package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// stallingTestServer serves a page that never finishes loading within the
// tool's page timeout — the portable stand-in for "internal host that
// responds slowly", with no dependence on link-local behaviour.
func stallingTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/stall", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "<html><body>partial")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestNavigate_FailedLoad_DoesNotStrandTheTabOnTheTarget(t *testing.T) {
	skipIfNoBrowser(t)

	srv := stallingTestServer(t)
	cfg := testBrowserCfg(t)
	cfg.PageTimeout = 3 * time.Second // fail fast; the fixture stalls for 30s

	ssrf := security.NewSSRFChecker([]string{"127.0.0.1"})
	registry := tools.NewToolRegistry()
	mgr, err := RegisterTools(registry, cfg, ssrf, false, t.TempDir(), true)
	require.NoError(t, err)
	t.Cleanup(mgr.Shutdown)

	navTool := mustGetTool(t, registry, "browser_navigate")
	result := navTool.Execute(context.Background(), map[string]any{"url": srv.URL + "/stall"})
	require.NotNil(t, result)
	require.True(t, result.IsError, "a stalled load must report an error; got: %s", result.ForLLM)

	// THE security property: whatever the diagnosis, the tab must no longer
	// be sitting on the target where a follow-up tool call could read it.
	tabCtx, err := mgr.Session(defaultSessionID)
	require.NoError(t, err)
	readCtx, cancel := context.WithTimeout(tabCtx, 10*time.Second)
	defer cancel()

	var location string
	require.NoError(t, chromedp.Run(readCtx, chromedp.Location(&location)))
	require.NotContains(t, location, "/stall",
		"tab was left parked on the failed target — a follow-up browser_get_text would read it")
	require.Equal(t, "about:blank", location,
		"a failed navigation must leave the tab on about:blank")
}

func TestOpenTab_FailedLoad_DoesNotStrandTheTabOnTheTarget(t *testing.T) {
	skipIfNoBrowser(t)

	srv := stallingTestServer(t)
	cfg := testBrowserCfg(t)
	cfg.PageTimeout = 3 * time.Second

	ssrf := security.NewSSRFChecker([]string{"127.0.0.1"})
	registry := tools.NewToolRegistry()
	mgr, err := RegisterTools(registry, cfg, ssrf, false, t.TempDir(), true)
	require.NoError(t, err)
	t.Cleanup(mgr.Shutdown)

	openTool := mustGetTool(t, registry, "browser_open_tab")
	result := openTool.Execute(context.Background(), map[string]any{"url": srv.URL + "/stall"})
	require.NotNil(t, result)
	require.True(t, result.IsError, "a stalled load must report an error; got: %s", result.ForLLM)

	tabCtx, err := mgr.Session(defaultSessionID)
	require.NoError(t, err)
	readCtx, cancel := context.WithTimeout(tabCtx, 10*time.Second)
	defer cancel()

	var location string
	require.NoError(t, chromedp.Run(readCtx, chromedp.Location(&location)))
	require.NotContains(t, location, "/stall",
		"the new tab was left parked on the failed target")
}
