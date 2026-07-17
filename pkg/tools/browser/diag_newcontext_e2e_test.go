//go:build browservideo_e2e

// Diagnostic (ci-omnipus only): isolate WHY the coordinator's per-agent browser
// context creation fails on real headless Chrome. Reproduces the exact chromedp
// sequence launchChrome→Session uses — NewPipeAllocator with the video-capable
// managedExecAllocatorOpts args (--headless=new), then chromedp.NewContext with
// and without WithNewBrowserContext — and prints the exact outcome of each, plus
// the rendered launch args so we can confirm --headless=new is actually present.
package browser

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/cdppipe"
)

func TestDiag_NewBrowserContext_HeadlessNew(t *testing.T) {
	root := os.Getenv("DIAG_INSTALL_ROOT")
	if root == "" {
		root = t.TempDir()
	}
	execPath, err := EnsureChromium(context.Background(), root)
	if err != nil {
		t.Skipf("no chromium: %v", err)
	}

	cfg := BrowserConfig{ProfileDir: t.TempDir()}
	cmdline := managedExecAllocatorOpts(cfg, managedLaunchParams{VideoCapable: true})
	t.Logf("ARGS: has --headless=new = %v", hasArg(cmdline.Args, "--headless=new"))
	t.Logf("ARGS: %s", strings.Join(cmdline.Args, " "))

	rootCtx, cancel, err := cdppipe.NewPipeAllocator(context.Background(), execPath, cdppipe.PipeOptions{
		Args: cmdline.Args, Env: cmdline.Env, UserDataDir: cfg.ProfileDir,
		Errf: func(f string, a ...any) {},
	})
	if err != nil {
		t.Fatalf("LAUNCH FAILED: %v", err)
	}
	defer cancel()
	t.Logf("LAUNCH: ok (pipe allocator up)")

	// A) DEFAULT browser context (like /json/new — known to work).
	{
		ctx, cxl := chromedp.NewContext(rootCtx)
		rctx, rcxl := context.WithTimeout(ctx, 15*time.Second)
		err := chromedp.Run(rctx, chromedp.Navigate("about:blank"))
		t.Logf("A) DEFAULT context Navigate: err=%v", err)
		rcxl()
		cxl()
	}

	// C) Raw Target.createTarget WITHOUT newWindow (what chromedp does today).
	{
		rctx, rcxl := context.WithTimeout(rootCtx, 15*time.Second)
		err := chromedp.Run(rctx, chromedp.ActionFunc(func(ctx context.Context) error {
			_, e := target.CreateTarget("about:blank").Do(ctx)
			return e
		}))
		t.Logf("C) createTarget NO newWindow: err=%v", err)
		rcxl()
	}

	// D) Raw Target.createTarget WITH newWindow=true — the hypothesized fix for
	// new headless (each target needs its own window; new-headless has no
	// persistent one).
	{
		rctx, rcxl := context.WithTimeout(rootCtx, 15*time.Second)
		var tid target.ID
		err := chromedp.Run(rctx, chromedp.ActionFunc(func(ctx context.Context) error {
			var e error
			tid, e = target.CreateTarget("about:blank").WithNewWindow(true).Do(ctx)
			return e
		}))
		t.Logf("D) createTarget WITH newWindow=true: tid=%s err=%v", tid, err)
		if err != nil {
			t.Errorf("newWindow=true did NOT fix it: %v", err)
		}
		rcxl()
	}

	// E) NEW browser context WITH a new window created explicitly — the shape the
	// coordinator's per-agent isolated context needs.
	{
		rctx, rcxl := context.WithTimeout(rootCtx, 15*time.Second)
		var bid cdp.BrowserContextID
		err := chromedp.Run(rctx, chromedp.ActionFunc(func(ctx context.Context) error {
			id, e := target.CreateBrowserContext().Do(ctx)
			if e != nil {
				return e
			}
			bid = id
			_, e = target.CreateTarget("about:blank").WithBrowserContextID(id).WithNewWindow(true).Do(ctx)
			return e
		}))
		t.Logf("E) NEW browser context + createTarget(newWindow=true, bid): bid=%s err=%v", bid, err)
		if err != nil {
			t.Errorf("new-context + newWindow did NOT work: %v", err)
		}
		rcxl()
	}
}

func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
