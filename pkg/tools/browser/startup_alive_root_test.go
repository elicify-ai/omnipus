package browser

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStartupDeadRootCannotBecomeWarm(t *testing.T) {
	home := t.TempDir()
	cfg := BrowserConfig{Enabled: true, Headless: true, ExecPath: fakeChromeBinary(t), ProfileDir: filepath.Join(home, "profile"), PageTimeout: time.Second}
	coordinator := NewBrowserCoordinator(home, cfg)
	t.Cleanup(func() { coordinator.Shutdown(); coordinator.waitStartupDrain() })
	dead, cancel := context.WithCancel(context.Background())
	cancel()
	coordinator.pipeLauncher = func(context.Context, string, pipeLaunchConfig) (*pipeLaunchResult, error) {
		return &pipeLaunchResult{rootCtx: dead, cancel: cancel}, nil
	}
	err := coordinator.WarmUp(context.Background())
	coordinator.mu.Lock()
	launched, root := coordinator.launched, coordinator.rootCtx
	coordinator.mu.Unlock()
	if err == nil || launched || root != nil {
		t.Errorf("dead handshake result became warm: err=%v launched=%t root=%v", err, launched, root)
	}
	// A failed launch must leave the same coordinator able to accept a healthy retry.
	coordinator.pipeLauncher = func(context.Context, string, pipeLaunchConfig) (*pipeLaunchResult, error) {
		root, cancel := context.WithCancel(context.Background())
		return &pipeLaunchResult{rootCtx: root, cancel: cancel}, nil
	}
	next, err := coordinator.Register(context.Background(), "retry", &BrowserManager{})
	if err != nil || next == nil || next.Err() != nil {
		t.Errorf("dead result prevented healthy retry: root=%v err=%v", next, err)
	}
}
