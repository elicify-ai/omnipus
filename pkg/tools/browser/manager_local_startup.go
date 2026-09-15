package browser

import (
	"context"
	"fmt"
	"os"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// ensureLocalStartedLocked is called and returns with manager.mu held. Every
// wait and launch runs outside that mutex; the marker remains until any rejected
// child has been disposed, preventing a new launch against the same profile.
func (m *BrowserManager) ensureLocalStartedLocked(ctx context.Context) error {
	for {
		if err := sessionStartupError(ctx); err != nil {
			return err
		}
		if m.started {
			return nil
		}
		flight := m.localStartup
		launch := flight == nil
		if launch {
			flight = newStartupCohort()
		}
		leave, joined := flight.join(ctx)
		if !joined {
			if launch {
				flight.cancel()
				return sessionStartupError(ctx)
			}
			m.mu.Unlock()
			select {
			case <-ctx.Done():
			case <-flight.done:
			}
			m.mu.Lock()
			continue
		}
		cfg, launcher := m.cfg, m.pipeLauncherFn
		if launch {
			m.localStartup = flight
		}
		m.mu.Unlock()
		if launch {
			go m.runLocalStartup(flight, cfg, launcher)
		}
		err := flight.wait(ctx)
		leave()
		m.mu.Lock()
		if callerErr := sessionStartupError(ctx); callerErr != nil {
			return callerErr
		}
		if err != nil {
			return err
		}
	}
}

func (m *BrowserManager) runLocalStartup(flight *startupCohort, cfg BrowserConfig, launcher func(context.Context, string, pipeLaunchConfig) (*pipeLaunchResult, error)) {
	var result *pipeLaunchResult
	err := invokeStartup(func() error {
		if !flight.live() {
			return context.Canceled
		}
		if err := os.MkdirAll(cfg.ProfileDir, 0o700); err != nil {
			return fmt.Errorf("browser: cannot create profile directory %s: %w", cfg.ProfileDir, err)
		}
		cleanStaleSingletons(cfg.ProfileDir)
		execPath, err := m.execPath.resolve(flight.ctx, cfg)
		if err != nil {
			return fmt.Errorf("browser: cannot locate chromium: %w", err)
		}
		cmdline := managedExecAllocatorOpts(cfg, chromeMajorVersion(flight.ctx, execPath))
		if canceledErr := flight.ctx.Err(); canceledErr != nil {
			return canceledErr
		}
		if launcher == nil {
			launcher = launchManagedPipe
		}
		result, err = launcher(flight.ctx, execPath, pipeLaunchConfig{args: cmdline.Args, env: cmdline.Env, userDataDir: cfg.ProfileDir})
		if err != nil {
			return fmt.Errorf("browser: failed to launch managed Chrome over the CDP pipe: %w", err)
		}
		return nil
	})
	m.mu.Lock()
	if err == nil {
		switch {
		case m.localStartup != flight || !flight.live():
			err = context.Canceled
		case result == nil || result.rootCtx == nil || result.rootCtx.Err() != nil:
			err = fmt.Errorf("browser: managed Chrome exited before startup completed")
		default:
			m.allocCtx = result.rootCtx
			m.allocCancel = result.cancel
			m.started = true
		}
	}
	m.mu.Unlock()
	if err != nil && result != nil && result.cancel != nil {
		result.cancel()
	}
	m.mu.Lock()
	if m.localStartup == flight {
		m.localStartup = nil
	}
	m.mu.Unlock()
	flight.finish(err)
	if err == nil {
		logger.InfoCF("browser", "Browser allocator ready (managed mode)", map[string]any{"headless": cfg.Headless, "profile_dir": cfg.ProfileDir})
	}
}
