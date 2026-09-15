package browser

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/chromedp/chromedp"
)

func TestStartupOldDeathWatchCannotRetireReplacement(t *testing.T) {
	home := t.TempDir()
	cfg := BrowserConfig{Enabled: true, Headless: true, ExecPath: fakeChromeBinary(t), ProfileDir: filepath.Join(home, "profile")}
	c := NewBrowserCoordinator(home, cfg)
	chromeMajorCache.Store(cfg.ExecPath, "152")
	t.Cleanup(func() { chromeMajorCache.Delete(cfg.ExecPath); c.Shutdown(); c.waitStartupDrain() })
	var launches atomic.Int32
	c.pipeLauncher = func(context.Context, string, pipeLaunchConfig) (*pipeLaunchResult, error) {
		launches.Add(1)
		return nil, errors.New("controlled relaunch failure")
	}
	old := &chromedp.Browser{LostConnection: make(chan struct{})}
	current := &chromedp.Browser{LostConnection: make(chan struct{})}
	root, cancels := installStartupWatchRoot(t, c, current)
	close(old.LostConnection)
	c.watchForCrash(old, nil)
	c.mu.Lock()
	kept := c.launched && c.rootCtx == root
	c.mu.Unlock()
	if !kept || root.Err() != nil || cancels.Load() != 0 || launches.Load() != 0 {
		t.Errorf("old watch retired replacement: kept=%t rootErr=%v cancels=%d relaunches=%d", kept, root.Err(), cancels.Load(), launches.Load())
	}
	// Positive control: the installed browser's own death still cleans it up and
	// attempts recovery; identity fencing must not suppress genuine crashes.
	launches.Store(0)
	_, currentCancels := installStartupWatchRoot(t, c, current)
	close(current.LostConnection)
	c.watchForCrash(current, nil)
	if currentCancels.Load() != 1 || launches.Load() != 1 {
		t.Errorf("current crash lost recovery: cancels=%d relaunches=%d", currentCancels.Load(), launches.Load())
	}
}

func installStartupWatchRoot(t *testing.T, c *BrowserCoordinator, b *chromedp.Browser) (context.Context, *atomic.Int32) {
	t.Helper()
	root, cancel := chromedp.NewContext(context.Background())
	chromedp.FromContext(root).Browser = b
	var once sync.Once
	release := func() { once.Do(cancel) }
	t.Cleanup(release)
	count := new(atomic.Int32)
	c.mu.Lock()
	c.rootCtx = root
	c.rootCancel = func() { count.Add(1); release() }
	c.launched = true
	c.mu.Unlock()
	return root, count
}
