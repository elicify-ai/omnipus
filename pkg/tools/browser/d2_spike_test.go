package browser

// D2 spike (ADR-043 / spec TDD test #1) — the decisive architecture gate.
// Proves the two properties the whole shared-Chrome+per-agent-contexts hybrid
// rests on, against a REAL Chrome via chromedp:
//
//  1. ISOLATION: two chromedp.WithNewBrowserContext() contexts get DISTINCT
//     BrowserContextIDs (separate cookie/localStorage partitions — the basis of
//     per-agent isolation).
//  2. O1 PROPERTY (the gating one): a window.open from a tab in context A
//     creates the new target in context A's browser context (not the default,
//     not a sibling's). CDP defaults new targets to the opener's context — this
//     verifies it rather than assumes it.
//
// If this spike FAILS, the hybrid approach must be re-ADRed (spec §Stream B
// fallback) before any further implementation. Skip on short/CI-no-Chrome runs.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

func TestD2Spike_BrowserContextIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("spike needs a real Chrome")
	}
	// Reuse the shared cached managed Chrome (resolveTestBinary) instead of
	// re-downloading ~130MB into a fresh tempdir on every run — the original
	// EnsureChromium(tempdir) call made this test take minutes + OOM-risk on a
	// disk/RAM-constrained devpod for no coverage benefit (same binary).
	binPath := resolveTestBinary(t)

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(binPath),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.NoSandbox,
		chromedp.Headless,
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer allocCancel()

	// Browser root context (the one Chrome process).
	rootCtx, rootCancel := chromedp.NewContext(allocCtx)
	defer rootCancel()
	if err := chromedp.Run(rootCtx); err != nil {
		t.Fatalf("launch root chrome: %v", err)
	}

	// Two distinct browser contexts (agents A and B).
	ctxA, cancelA := chromedp.NewContext(rootCtx, chromedp.WithNewBrowserContext())
	defer cancelA()
	ctxB, cancelB := chromedp.NewContext(rootCtx, chromedp.WithNewBrowserContext())
	defer cancelB()

	// O1 (G6 fix): navigate A to a REAL loopback page, not about:blank —
	// Chrome blocks popups (window.open) on about:blank, which made the
	// original O1 assertion tautological ("inconclusive"). A real http origin
	// allows the popup, so O1 becomes a real pass/fail.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "<html><body><h1>opener</h1></body></html>")
	}))
	defer srv.Close()
	if err := chromedp.Run(ctxA, chromedp.Navigate(srv.URL)); err != nil {
		t.Fatalf("nav A: %v", err)
	}
	if err := chromedp.Run(ctxB, chromedp.Navigate("about:blank")); err != nil {
		t.Fatalf("nav B: %v", err)
	}

	idA := chromedp.FromContext(ctxA).BrowserContextID
	idB := chromedp.FromContext(ctxB).BrowserContextID
	t.Logf("context A id=%q  context B id=%q", idA, idB)

	// Property 1: distinct partitions.
	if idA == "" || idB == "" {
		t.Fatalf("expected non-empty context ids; got A=%q B=%q", idA, idB)
	}
	if idA == idB {
		t.Fatalf("FATAL: two contexts share the same BrowserContextID %q — no isolation", idA)
	}

	// Property 2 (O1): window.open from A's tab lands in A's context.
	// Capture the BEFORE set of page targets so we can identify the NEW one.
	browser := chromedp.FromContext(ctxA).Browser
	beforeInfos, err := target.GetTargets().Do(cdp.WithExecutor(ctxA, browser))
	if err != nil {
		t.Fatalf("GetTargets (before): %v", err)
	}
	beforeSet := make(map[target.ID]struct{}, len(beforeInfos))
	for _, ti := range beforeInfos {
		if ti.Type == "page" {
			beforeSet[ti.TargetID] = struct{}{}
		}
	}

	chromedp.ListenTarget(ctxA, func(ev any) {
		// drain
		_ = ev
	})
	if err := chromedp.Run(ctxA,
		// comma-operator returns undefined so CDP doesn't try to serialize the Window object
		chromedp.Evaluate(fmt.Sprintf(`(window.open(%q, "_blank"), undefined)`, srv.URL), nil),
	); err != nil {
		t.Fatalf("window.open in A: %v", err)
	}

	// Poll getTargets until a NEW page target (not in the before-set) appears,
	// or ~3s timeout. O1 is now a hard pass/fail: a real http origin allows the
	// popup, so "no new target" is a genuine failure, not "inconclusive".
	var newTarget *target.Info
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		infos, gerr := target.GetTargets().Do(cdp.WithExecutor(ctxA, browser))
		if gerr != nil {
			t.Fatalf("GetTargets (poll): %v", gerr)
		}
		for _, ti := range infos {
			if ti.Type != "page" {
				continue
			}
			if _, seen := beforeSet[ti.TargetID]; seen {
				continue
			}
			newTarget = ti
			break
		}
		if newTarget != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if newTarget == nil {
		t.Fatalf("FATAL (O1 FAILED): no new page target appeared within 3s of window.open — " +
			"popup did not fire (expected to fire on a real http origin)")
	}
	t.Logf(
		"new window.open target: id=%q url=%q context=%q",
		newTarget.TargetID,
		newTarget.URL,
		newTarget.BrowserContextID,
	)
	if newTarget.BrowserContextID != idA {
		t.Fatalf("FATAL (O1 FAILED): window.open target landed in context %q, expected A %q",
			newTarget.BrowserContextID, idA)
	}

	// Count targets whose context == A vs == B (diagnostic only now).
	infos, _ := target.GetTargets().Do(cdp.WithExecutor(ctxA, browser))
	var inA, inB int
	for _, ti := range infos {
		if ti.Type != "page" {
			continue
		}
		if ti.BrowserContextID == idA {
			inA++
		} else if ti.BrowserContextID == idB {
			inB++
		}
	}
	t.Logf("page targets in A=%d in B=%d (infos=%d)", inA, inB, len(infos))

	t.Logf(
		"D2 SPIKE PASSED: distinct browser contexts (%q != %q); O1 window.open-in-opener-context VERIFIED (new target in A)",
		idA,
		idB,
	)
}
