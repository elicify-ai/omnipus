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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
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
	mgr, err := registerToolsForTest(t, registry, cfg, ssrf, false, t.TempDir(), true)
	require.NoError(t, err)
	t.Cleanup(mgr.Shutdown)

	navTool := mustGetTool(t, registry, "browser_navigate")
	result := navTool.Execute(context.Background(), map[string]any{"url": srv.URL + "/stall"})
	require.NotNil(t, result)
	require.True(t, result.IsError, "a stalled load must report an error; got: %s", result.ForLLM)

	// THE security property: whatever the diagnosis, the tab must no longer
	// be sitting on the target where a follow-up tool call could read it.
	tabCtx, err := mgr.Session(testSessionID)
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
	mgr, err := registerToolsForTest(t, registry, cfg, ssrf, false, t.TempDir(), true)
	require.NoError(t, err)
	t.Cleanup(mgr.Shutdown)

	openTool := mustGetTool(t, registry, "browser_open_tab")
	result := openTool.Execute(context.Background(), map[string]any{"url": srv.URL + "/stall"})
	require.NotNil(t, result)
	require.True(t, result.IsError, "a stalled load must report an error; got: %s", result.ForLLM)

	tabCtx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	readCtx, cancel := context.WithTimeout(tabCtx, 10*time.Second)
	defer cancel()

	var location string
	require.NoError(t, chromedp.Run(readCtx, chromedp.Location(&location)))
	require.NotContains(t, location, "/stall",
		"the new tab was left parked on the failed target")
}

// --- Independent recovery budgets (review finding F8, 2026-08-13) ---
//
// The two tests above pass even with a BROKEN abandon path, because their
// stalling fixture commits a partial document — so the diagnostic
// chromedp.Location read answers instantly and leaves the shared 5s budget
// almost untouched. The dangerous case is the opposite one: a renderer wedged
// hard enough that the location read answers NOTHING and burns the entire
// budget. With both recovery steps on ONE context (as originally written) the
// about:blank navigation then inherited an already-expired deadline and never
// reached Chrome — so the tab stayed parked on the internal target in exactly
// the scenario the helper was written for.
//
// These drive that case through BrowserManager.abandonCDPFn rather than a real
// wedged renderer: deterministic, no Chrome, and it can observe the one thing
// that matters — what context the SECOND step was handed.

// abandonStep records one recovery CDP round trip as the helper issued it.
type abandonStep struct {
	ctxErrAtEntry error
	hasDeadline   bool
}

// recordAbandonSteps installs a stand-in for the recovery CDP calls in which
// the FIRST call (the diagnostic location read — abandonTabAfterFailedLoad
// makes exactly two round trips, read then navigate, in that order) blocks
// until its context is done, simulating a renderer that answers nothing.
func recordAbandonSteps(m *BrowserManager, steps *[]abandonStep, mu *sync.Mutex) {
	m.abandonCDPFn = func(ctx context.Context, _ ...chromedp.Action) error {
		mu.Lock()
		idx := len(*steps)
		_, hasDeadline := ctx.Deadline()
		*steps = append(*steps, abandonStep{ctxErrAtEntry: ctx.Err(), hasDeadline: hasDeadline})
		mu.Unlock()
		if idx == 0 {
			<-ctx.Done() // wedged renderer: never answers, burns the whole budget
			return ctx.Err()
		}
		return nil
	}
}

func TestAbandonTabAfterFailedLoad_AbandonNavigationGetsItsOwnBudget(t *testing.T) {
	m := &BrowserManager{}
	var mu sync.Mutex
	var steps []abandonStep
	recordAbandonSteps(m, &steps, &mu)

	start := time.Now()
	msg := abandonTabAfterFailedLoad(
		context.Background(), m, context.Background(),
		"browser_navigate", "http://internal.example/secret", nil,
		context.DeadlineExceeded,
	)
	elapsed := time.Since(start)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, steps, 2, "the helper must make exactly two recovery round trips: the location read, then the about:blank navigation")
	require.GreaterOrEqual(t, elapsed, tabAbandonTimeout,
		"the fixture must really have burned a full budget on the location read, or this test proves nothing")

	// THE assertion. With one shared context this is context.DeadlineExceeded:
	// the security-critical navigation is handed a dead deadline and never
	// reaches Chrome, leaving the tab parked on the target.
	require.NoError(t, steps[1].ctxErrAtEntry,
		"the about:blank navigation must get a LIVE context of its own — a location read that "+
			"burns its budget (a wedged renderer, the exact case this helper exists for) must not "+
			"be able to consume the abandon navigation's budget too")
	require.True(t, steps[1].hasDeadline,
		"the abandon navigation must still be bounded — its own budget, not an unbounded context")
	require.NotContains(t, msg, "could NOT be steered away",
		"the abandon landed, so the message must not warn that it did not")
}

// TestAbandonTabAfterFailedLoad_FailedAbandonIsReportedToTheCaller — when the
// abandon navigation itself fails there is nothing left that can move the tab,
// so the one remaining mitigation is telling the caller not to trust the tab.
// Silence here would leave an agent free to run browser_get_text on a tab
// still holding the internal page.
func TestAbandonTabAfterFailedLoad_FailedAbandonIsReportedToTheCaller(t *testing.T) {
	m := &BrowserManager{}
	m.abandonCDPFn = func(context.Context, ...chromedp.Action) error {
		return errors.New("target closed")
	}

	msg := abandonTabAfterFailedLoad(
		context.Background(), m, context.Background(),
		"browser_navigate", "http://internal.example/secret", nil,
		context.DeadlineExceeded,
	)

	require.Contains(t, msg, "could NOT be steered away",
		"a failed abandon must be visible in the tool result, not only in the operator's log")
	require.Contains(t, msg, "target closed", "the real reason must survive into the message")
}
