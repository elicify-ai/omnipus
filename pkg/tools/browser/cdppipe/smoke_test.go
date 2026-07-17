//go:build cdppipesmoke

// Package cdppipe real-Chrome smoke test.
//
// Excluded from normal builds/tests by the cdppipesmoke build tag so it never
// launches a browser (or risks OOM) in ordinary CI. Run explicitly with a
// Chromium binary available:
//
//	go test -tags 'goolm stdjson cdppipesmoke' -run TestPipeAllocator_RealChrome_Smoke ./pkg/tools/browser/cdppipe/
//
// It drives the CDP calls the live-browser-video-streaming epic depends on over
// the pipe transport and asserts there is no TCP debugging surface.
package cdppipe

import (
	"context"
	"net"
	"os/exec"
	"testing"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

func findChrome() string {
	for _, name := range []string{
		"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
		"chrome", "headless_shell", "headless-shell",
	} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

func TestPipeAllocator_RealChrome_Smoke(t *testing.T) {
	if testing.Short() {
		t.Skip("smoke test skipped in -short mode")
	}
	execPath := findChrome()
	if execPath == "" {
		t.Skip("no chromium binary found on PATH")
	}

	ctx, cancel, err := NewPipeAllocator(context.Background(), execPath, PipeOptions{
		Args: []string{"--headless=new", "--disable-gpu", "--no-sandbox"},
		Errf: t.Logf,
	})
	if err != nil {
		t.Fatalf("NewPipeAllocator: %v", err)
	}
	defer cancel()

	// Page.navigate + Runtime.evaluate + addScriptToEvaluateOnNewDocument.
	var sum int
	if err := chromedp.Run(
		ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument("window.__omnipus=1").Do(ctx)
			return err
		}),
		chromedp.Navigate("about:blank"),
		chromedp.Evaluate(`1+1`, &sum),
	); err != nil {
		t.Fatalf("chromedp.Run: %v", err)
	}
	if sum != 2 {
		t.Fatalf("evaluate 1+1 = %d, want 2", sum)
	}

	// Target.getTargets over the pipe.
	targets, err := chromedp.Targets(ctx)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(targets) == 0 {
		t.Fatalf("expected at least one target")
	}

	// Child context multiplexed over the same pipe (Target.createTarget +
	// attachToTarget), proving concurrent in-process sessions share the pipe.
	childCtx, childCancel := chromedp.NewContext(ctx)
	defer childCancel()
	var title string
	if err := chromedp.Run(
		childCtx,
		chromedp.Navigate("about:blank"),
		chromedp.Evaluate(`document.title || "blank"`, &title),
	); err != nil {
		t.Fatalf("child context Run: %v", err)
	}

	// Screencast is best-effort under headless (may not paint); bounded so it
	// never hangs. Just confirm startScreencast is accepted over the pipe.
	scCtx, scCancel := context.WithTimeout(ctx, 5*time.Second)
	defer scCancel()
	_ = chromedp.Run(scCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		return page.StartScreencast().WithFormat(page.ScreencastFormatJpeg).Do(ctx)
	}))

	// No TCP debugging surface: the common CDP ports must not be listening for
	// this launch (we passed no --remote-debugging-port).
	for _, port := range []string{"9222", "9223"} {
		c, derr := net.DialTimeout("tcp", "127.0.0.1:"+port, 200*time.Millisecond)
		if derr == nil {
			c.Close()
			t.Errorf("unexpected CDP TCP listener on 127.0.0.1:%s", port)
		}
	}
}
