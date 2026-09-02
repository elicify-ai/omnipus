package browser

// target_e2e_test.go — the ARIA role + accessible-name branch against a REAL
// accessibility tree.
//
// Everything here needs a live renderer by construction: the computed role and
// the computed accessible name are Chrome's answers, not ours, and the whole
// value of this locator is that it agrees with what a person (or a screen
// reader) would call the element.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// axFixtureHTML is deliberately written the way a modern build tool emits a
// page: the class names are content hashes and change on every deploy, so a
// CSS locator against them is worthless by the following week. The accessible
// names do not change.
const axFixtureHTML = `<!doctype html><html><body>
  <button class="_1f3a9x_btn _9zq">Submit</button>
  <div class="_c81b2f_row">
    <button class="_1f3a9x_btn">Delete</button>
    <button class="_1f3a9x_btn">Delete</button>
    <button class="_1f3a9x_btn">Delete</button>
  </div>
  <button aria-hidden="true" class="_hidden">Ghost</button>
  <a href="#" class="_2ab">Learn more</a>
  <input aria-label="Search" class="_3cd" />
  <iframe title="child" srcdoc="&lt;button&gt;Framed&lt;/button&gt;"></iframe>
</body></html>`

func axFixtureTab(t *testing.T) (context.Context, *BrowserManager) {
	t.Helper()
	srv := htmlFixtureServer(t, axFixtureHTML)
	registry, mgr := newPermissiveRegistry(t, testBrowserCfg(t))

	nav := mustGetTool(t, registry, "browser_navigate")
	res := nav.Execute(context.Background(), map[string]any{"url": srv.URL})
	require.False(t, res.IsError, "navigate must succeed; got: %s", res.ForLLM)

	tabCtx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	return tabCtx, mgr
}

// TestResolveTarget_RoleName_ResolvesOnHashedClasses is the headline: an
// element whose only CSS handle is a build hash resolves by what it IS and
// what it SAYS.
func TestResolveTarget_RoleName_ResolvesOnHashedClasses(t *testing.T) {
	skipIfNoBrowser(t)
	tabCtx, _ := axFixtureTab(t)

	target, cleanup, err := resolveTarget(tabCtx, "browser_click",
		Locator{Role: "button", Name: "Submit"}, 5*time.Second)
	defer cleanup()
	require.NoError(t, err, "role=button name=Submit must resolve")

	// It resolves to the SAME marker-selector shape the visible-text branch
	// produces, which is what keeps every downstream chromedp action unchanged.
	if !strings.HasPrefix(target, "["+textMarkerAttr+`="`) {
		t.Fatalf("role+name must return the shared marker selector; got %q", target)
	}
}

// TestResolveTarget_MultiMatch_ErrorsWithCount — three identical Delete
// buttons. Silently picking the first would be a click the agent did not ask
// for, on a destructive verb.
func TestResolveTarget_MultiMatch_ErrorsWithCount(t *testing.T) {
	skipIfNoBrowser(t)
	tabCtx, _ := axFixtureTab(t)

	_, cleanup, err := resolveTarget(tabCtx, "browser_click",
		Locator{Role: "button", Name: "Delete"}, 3*time.Second)
	defer cleanup()
	if err == nil {
		t.Fatal("three matching elements must be an ambiguity error, never a silent first-match")
	}
	msg := err.Error()
	if !strings.Contains(msg, "3") {
		t.Errorf("the error must name HOW MANY matched, so the agent knows an index is available; got %q", msg)
	}
	if !strings.Contains(msg, "index") {
		t.Errorf("the error must name the way out; got %q", msg)
	}
}

// TestResolveTarget_IndexSelectsDocumentOrder — the ordering guarantee is
// asserted DIRECTLY. Inferring it from a passing click would pass just as
// happily on a random order that happened to put the right node first.
func TestResolveTarget_IndexSelectsDocumentOrder(t *testing.T) {
	skipIfNoBrowser(t)
	srv := htmlFixtureServer(t, `<!doctype html><html><body>
	  <button id="first">Go</button>
	  <button id="second">Go</button>
	  <button id="third">Go</button>
	</body></html>`)
	registry, mgr := newPermissiveRegistry(t, testBrowserCfg(t))
	nav := mustGetTool(t, registry, "browser_navigate")
	require.False(t, nav.Execute(context.Background(), map[string]any{"url": srv.URL}).IsError)
	tabCtx, err := mgr.Session(testSessionID)
	require.NoError(t, err)

	wantIDs := []string{"first", "second", "third"}
	for i, wantID := range wantIDs {
		idx := i
		target, cleanup, rerr := resolveTarget(tabCtx, "browser_click",
			Locator{Role: "button", Name: "Go", Index: &idx}, 3*time.Second)
		require.NoErrorf(t, rerr, "index %d must resolve", idx)

		gotID := idOfMarkedElement(t, tabCtx, target)
		cleanup()
		if gotID != wantID {
			t.Fatalf("index %d resolved #%s, want #%s — the ordering must be DOCUMENT order, "+
				"not whatever order the accessibility tree happened to return", idx, gotID, wantID)
		}
	}

	// Out of range names the count rather than silently clamping.
	tooBig := 7
	_, cleanup, rerr := resolveTarget(tabCtx, "browser_click",
		Locator{Role: "button", Name: "Go", Index: &tooBig}, 2*time.Second)
	cleanup()
	if rerr == nil || !strings.Contains(rerr.Error(), "3") {
		t.Errorf("an out-of-range index must name how many DID match; got %v", rerr)
	}
}

// TestResolveTarget_ExcludesIgnoredNodes / _AllIgnoredNamesTheCount — Chrome
// returns matches "including nodes that are ignored for accessibility". A
// hidden node must never win. But an agent that genuinely needs to reach one
// would otherwise get a "not found" indistinguishable from a genuinely absent
// element, so the count of ignored matches is in the error — that one number
// is what lets it fall back to a CSS locator instead of retrying forever.
func TestResolveTarget_AllIgnoredNamesTheCount(t *testing.T) {
	skipIfNoBrowser(t)
	tabCtx, _ := axFixtureTab(t)

	_, cleanup, err := resolveTarget(tabCtx, "browser_click",
		Locator{Role: "button", Name: "Ghost"}, 1500*time.Millisecond)
	defer cleanup()
	if err == nil {
		t.Fatal("an aria-hidden element must not resolve")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ignored for accessibility") {
		t.Errorf("the error must say the match was IGNORED, not merely absent — the two need different "+
			"next moves; got %q", msg)
	}
}

// TestResolveTarget_ChildFrameMatchErrors — the AX tree crosses frames, the
// marker stamp and the downstream chromedp query do not. A match in a child
// frame resolves an attribute the query will never find, so it is refused by
// name rather than becoming a mystery timeout.
func TestResolveTarget_ChildFrameMatchErrors(t *testing.T) {
	skipIfNoBrowser(t)
	tabCtx, _ := axFixtureTab(t)

	_, cleanup, err := resolveTarget(tabCtx, "browser_click",
		Locator{Role: "button", Name: "Framed"}, 1500*time.Millisecond)
	defer cleanup()
	if err == nil {
		t.Fatal("a match owned by a child frame must not resolve")
	}
	if !strings.Contains(err.Error(), "frame") {
		t.Errorf("the refusal must say it is a FRAME problem and point at the CSS fallback; got %q", err)
	}
}

// TestResolveTarget_EmptyAXTreeErrors — a tab with no committed document is a
// named error, never a nil deref and never a silent zero-match.
func TestResolveTarget_EmptyAXTreeErrors(t *testing.T) {
	skipIfNoBrowser(t)
	registry, mgr := newPermissiveRegistry(t, testBrowserCfg(t))
	nav := mustGetTool(t, registry, "browser_navigate")
	_ = nav.Execute(context.Background(), map[string]any{"url": "about:blank"})

	tabCtx, err := mgr.Session(testSessionID)
	require.NoError(t, err)

	_, cleanup, rerr := resolveTarget(tabCtx, "browser_click",
		Locator{Role: "button", Name: "Anything"}, 1200*time.Millisecond)
	defer cleanup()
	if rerr == nil {
		t.Fatal("resolving against a blank page must fail, not hang or return an empty target")
	}
	if strings.Contains(rerr.Error(), "nil pointer") {
		t.Errorf("must be a NAMED error, not a panic surfaced as text; got %q", rerr)
	}
}

// TestResolveTarget_AllActionToolsShareSeam — the seam is the point. Four
// shipped tools resolve the SAME element from the SAME locator, so an agent
// that learned one locator syntax has learned all of them.
func TestResolveTarget_AllActionToolsShareSeam(t *testing.T) {
	skipIfNoBrowser(t)
	srv := htmlFixtureServer(t, `<!doctype html><html><body>
	  <button id="target" class="_hash1">Continue</button>
	</body></html>`)
	registry, _ := newPermissiveRegistry(t, testBrowserCfg(t))
	nav := mustGetTool(t, registry, "browser_navigate")
	require.False(t, nav.Execute(context.Background(), map[string]any{"url": srv.URL}).IsError)

	loc := map[string]any{"role": "button", "name": "Continue"}
	for _, name := range []string{"browser_wait", "browser_get_text", "browser_click"} {
		tool := mustGetTool(t, registry, name)
		res := tool.Execute(context.Background(), loc)
		if res.IsError {
			t.Errorf("%s must accept the same role+name locator as every other tool; got: %s", name, res.ForLLM)
		}
	}
}

// htmlFixtureServer serves one fixed HTML document.
func htmlFixtureServer(t *testing.T, html string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// idOfMarkedElement reads back the id of whichever element carries the marker
// selector, which is how the ordering assertion identifies the winner without
// trusting the resolver's own account of it.
func idOfMarkedElement(t *testing.T, tabCtx context.Context, marker string) string {
	t.Helper()
	selJSON, err := json.Marshal(marker)
	require.NoError(t, err)
	var id string
	script := "(function(){var e=document.querySelector(" + string(selJSON) + "); return e ? e.id : '';})()"
	require.NoError(t, chromedp.Run(tabCtx, chromedp.Evaluate(script, &id)))
	return id
}
