//go:build browservideo_e2e

// Diagnostic (ci-omnipus only): does chrome-headless-shell support the WebCodecs
// VideoEncoder our encoder page (ADR-044 A2) relies on? Playwright uses
// headless-shell for headless work but encodes with native ffmpeg, not WebCodecs —
// so we must confirm headless-shell can actually run VideoEncoder before adopting
// it as the shared-browser binary for the video path. Also checks the default
// context (headless-shell has no window model, so createBrowserContext + a page in
// it must work here — the thing full-chrome --headless fails at).
//
// DIAG_SHELL_BIN=/path/to/chrome-headless-shell selects the binary.
package browser

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/cdppipe"
)

func TestDiag_HeadlessShell_WebCodecsAndContexts(t *testing.T) {
	bin := os.Getenv("DIAG_SHELL_BIN")
	if bin == "" {
		t.Skip("set DIAG_SHELL_BIN to the chrome-headless-shell binary path")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("binary not found: %v", err)
	}

	cfg := BrowserConfig{ProfileDir: t.TempDir()}
	cmdline := managedExecAllocatorOpts(cfg, managedLaunchParams{}) // non-video → plain --headless companions
	rootCtx, cancel, err := cdppipe.NewPipeAllocator(context.Background(), bin, cdppipe.PipeOptions{
		Args: cmdline.Args, Env: cmdline.Env, UserDataDir: cfg.ProfileDir,
		Errf: func(f string, a ...any) {},
	})
	if err != nil {
		t.Fatalf("LAUNCH FAILED: %v", err)
	}
	defer cancel()

	// 1) Per-agent isolated context (what the coordinator does) MUST work on the shell.
	{
		ctx, cxl := chromedp.NewContext(rootCtx, chromedp.WithNewBrowserContext())
		rctx, rcxl := context.WithTimeout(ctx, 20*time.Second)
		err := chromedp.Run(rctx, chromedp.Navigate("about:blank"))
		t.Logf("CONTEXT: new-browser-context Navigate on headless-shell: err=%v", err)
		if err != nil {
			t.Errorf("headless-shell new-browser-context FAILED: %v", err)
		}
		rcxl()
		cxl()
	}

	// 2) WebCodecs VideoEncoder support (our encoder page needs this).
	{
		ctx, cxl := chromedp.NewContext(rootCtx, chromedp.WithNewBrowserContext())
		rctx, rcxl := context.WithTimeout(ctx, 25*time.Second)
		var typeofEncoder, h264, vp8 string
		js := `(async () => {
			if (typeof VideoEncoder === 'undefined') return 'NO_VideoEncoder';
			try {
				const h = await VideoEncoder.isConfigSupported({codec:'avc1.4D4028', width:1280, height:720});
				const v = await VideoEncoder.isConfigSupported({codec:'vp8', width:1280, height:720});
				return 'VideoEncoder|h264='+(!!h.supported)+'|vp8='+(!!v.supported);
			} catch(e){ return 'ERR:'+e.message; }
		})()`
		err := chromedp.Run(rctx,
			chromedp.Navigate("about:blank"),
			chromedp.Evaluate(js, &typeofEncoder, func(p *chromedp.EvalParams) *chromedp.EvalParams {
				return p.WithAwaitPromise(true)
			}),
		)
		_ = h264
		_ = vp8
		t.Logf("WEBCODECS: result=%q err=%v", typeofEncoder, err)
		if err != nil {
			t.Errorf("WebCodecs eval error: %v", err)
		}
		rcxl()
		cxl()
	}
}
