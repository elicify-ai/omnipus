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

	// B) NEW browser context (WithNewBrowserContext) — exactly what the
	// coordinator's Session()/createAgentContext does, and what fails with
	// "no browser is open -32000".
	{
		ctx, cxl := chromedp.NewContext(rootCtx, chromedp.WithNewBrowserContext())
		rctx, rcxl := context.WithTimeout(ctx, 15*time.Second)
		err := chromedp.Run(rctx, chromedp.Navigate("about:blank"))
		t.Logf("B) NEW-BROWSER-CONTEXT Navigate: err=%v", err)
		if err != nil {
			t.Errorf("NEW-BROWSER-CONTEXT failed (this is the coordinator's failing op): %v", err)
		}
		rcxl()
		cxl()
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
