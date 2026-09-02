// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// browser_cache_trim_schedule_test.go — tools.browser.cache_trim_interval is
// documented as taking effect when you save settings, with no restart. It did
// not.
//
// The value reached the POOL on a reload (pkg/agent/loop.go copies it into the
// BrowserConfig and BrowserPool.ApplyRuntimeConfig stores it), and stopped
// there. The gateway's sweep ran on a time.Ticker built ONCE at boot from
// pool.CacheTrimInterval(), so an operator lowering the interval because their
// disk was filling saw the setting accepted, saw the new value read back, and
// got the old hourly sweep. docs/configuration.md said otherwise in writing.
// That is the ADR-037 "reports success and changes nothing" shape this project
// treats as a release blocker.
//
// THE CHAIN THESE TESTS DRIVE, end to end, with only the sweep counter faked:
//
//	config.json on disk
//	  -> config.LoadConfig                              (production loader)
//	  -> BrowserToolConfig.EffectiveCacheTrimInterval() (the exact expression
//	     pkg/agent/loop.go assigns, on both its boot and its reload pass)
//	  -> browser.NewBrowserPool / BrowserPool.ApplyRuntimeConfig
//	     (the production reload call, and the owner of the "<= 0 means the
//	     built-in default" substitution)
//	  -> BrowserPool.CacheTrimInterval()
//	  -> runBrowserCacheTrimSchedule
//	  -> OBSERVED SWEEPS.
//
// Reading pool.CacheTrimInterval() back and asserting on it would prove
// nothing: that field was already correct on every reload while the sweep
// ignored it, which is precisely how the defect stayed invisible.
//
// No Chrome anywhere: the pool is never asked to acquire a browser, and
// TrimAllEligible against an empty profile root is a directory read that finds
// nothing.

// countingTrimPool wraps a REAL *browser.BrowserPool and counts sweeps. The
// interval always comes from the wrapped pool, so nothing about the value under
// test is simulated — only the tally is.
type countingTrimPool struct {
	inner  *browser.BrowserPool
	mu     sync.Mutex
	sweeps int
}

func (c *countingTrimPool) CacheTrimInterval() time.Duration { return c.inner.CacheTrimInterval() }

func (c *countingTrimPool) TrimAllEligible() []browser.TrimResult {
	c.mu.Lock()
	c.sweeps++
	c.mu.Unlock()
	return c.inner.TrimAllEligible()
}

func (c *countingTrimPool) sweepCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sweeps
}

// operatorTrimInterval writes browserJSON as the tools.browser block of a real
// config.json, loads it with the production loader, and returns the value the
// SAME expression pkg/agent/loop.go uses produces. Nothing here is
// re-implemented by the test.
func operatorTrimInterval(t *testing.T, browserJSON string) time.Duration {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	body := fmt.Sprintf(`{"version": %d, "tools": {"browser": %s}}`, config.CurrentVersion, browserJSON)
	require.NoError(t, os.WriteFile(cfgPath, []byte(body), 0o600))

	loaded, err := config.LoadConfig(cfgPath)
	require.NoError(t, err, "an operator's config.json carrying cache_trim_interval must load")

	// Verbatim from pkg/agent/loop.go:
	//   browserCfg.CacheTrimInterval = cfg.Tools.Browser.EffectiveCacheTrimInterval()
	return loaded.Tools.Browser.EffectiveCacheTrimInterval()
}

// newCountingTrimPool builds a real pool the way the gateway builds one, with
// its trim interval sourced from an operator's config.json.
func newCountingTrimPool(t *testing.T, browserJSON string) *countingTrimPool {
	t.Helper()
	home := t.TempDir()
	pool := browser.NewBrowserPool(home, browser.BrowserConfig{
		Enabled:           true,
		Headless:          true,
		ProfileDir:        filepath.Join(home, "browser", "profiles", "default"),
		CacheTrimInterval: operatorTrimInterval(t, browserJSON),
	})
	t.Cleanup(pool.Shutdown)
	return &countingTrimPool{inner: pool}
}

// applyOperatorSave is what a Settings save does to a live pool: pkg/agent/loop.go
// re-reads the config and calls BrowserPool.ApplyRuntimeConfig with the new
// value. Same production call, same source of the number.
func applyOperatorSave(t *testing.T, p *countingTrimPool, browserJSON string) {
	t.Helper()
	p.inner.ApplyRuntimeConfig(browser.BrowserConfig{
		Enabled:           true,
		Headless:          true,
		CacheTrimInterval: operatorTrimInterval(t, browserJSON),
	})
}

// fastReconcile shrinks the reconcile bound so a test observes the loop's
// re-read without waiting a real quarter-minute for it. It changes ONLY how
// often the loop wakes to re-read the interval — never whether it re-reads,
// which is the property under test.
func fastReconcile(t *testing.T, d time.Duration) {
	t.Helper()
	prev := browserCacheTrimReconcileEvery
	browserCacheTrimReconcileEvery = d
	t.Cleanup(func() { browserCacheTrimReconcileEvery = prev })
}

// TestBrowserCacheTrimSchedule_LoweringTheIntervalTakesEffectWithoutARestart is
// the defect, start to finish.
//
// The operator boots with cache_trim_interval unset — the documented hour — then
// lowers it to one second and saves. On a schedule that captured its period at
// boot, nothing sweeps for the next hour and this fails.
//
// The discriminating detail is that the schedule is ALREADY armed on the long
// interval when the change lands. A loop that re-read the interval only after
// its current wait expired would satisfy a weaker version of this test and still
// leave the operator waiting out the old hour.
func TestBrowserCacheTrimSchedule_LoweringTheIntervalTakesEffectWithoutARestart(t *testing.T) {
	fastReconcile(t, 10*time.Millisecond)

	pool := newCountingTrimPool(t, `{}`)
	require.Equal(t, time.Hour, pool.CacheTrimInterval(),
		"an unset cache_trim_interval must reach the schedule as the hour docs/configuration.md "+
			"documents, or this test is not starting from the state an operator starts from")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go runBrowserCacheTrimSchedule(ctx, pool)

	// The boot sweep (FR-072 trigger 2) is unconditional and immediate.
	require.Eventually(t, func() bool { return pool.sweepCount() >= 1 }, 2*time.Second, 5*time.Millisecond,
		"the boot sweep must run whatever the interval is")

	// Armed on an hour. Nothing more may happen on its own.
	time.Sleep(80 * time.Millisecond)
	require.Equal(t, 1, pool.sweepCount(),
		"on an hourly interval only the boot sweep may have run")

	// The Settings save.
	applyOperatorSave(t, pool, `{"cache_trim_interval": 1}`)
	require.Equal(t, time.Second, pool.CacheTrimInterval(),
		"the reload must reach the pool — if it does not, the rest of this test is measuring the "+
			"wrong hop")

	assert.Eventually(t, func() bool { return pool.sweepCount() >= 2 }, 4*time.Second, 10*time.Millisecond,
		"the operator lowered tools.browser.cache_trim_interval and saved. The sweep schedule is "+
			"still running the value it captured at boot, so the setting was accepted, read back "+
			"correctly by the pool, and changed nothing until a restart — which is exactly what "+
			"the documentation promises it does not need")
}

// TestBrowserCacheTrimSchedule_HonoursTheConfiguredPeriod is the other
// direction, and without it the test above is satisfied by a loop that sweeps as
// fast as it can reconcile and ignores the interval altogether.
//
// The operator asks for two seconds; the schedule must NOT have swept again a
// second in, and MUST have swept by the time three have passed.
func TestBrowserCacheTrimSchedule_HonoursTheConfiguredPeriod(t *testing.T) {
	fastReconcile(t, 10*time.Millisecond)

	pool := newCountingTrimPool(t, `{"cache_trim_interval": 2}`)
	require.Equal(t, 2*time.Second, pool.CacheTrimInterval(),
		"the operator's 2 must arrive as two seconds, not as the built-in hour")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go runBrowserCacheTrimSchedule(ctx, pool)

	require.Eventually(t, func() bool { return pool.sweepCount() >= 1 }, 2*time.Second, 5*time.Millisecond,
		"the boot sweep must run")

	time.Sleep(time.Second)
	assert.Equal(t, 1, pool.sweepCount(),
		"one second into a two-second interval, only the boot sweep may have run — a schedule that "+
			"swept every time it woke to reconcile would walk every profile directory on disk many "+
			"times a minute")

	assert.Eventually(t, func() bool { return pool.sweepCount() >= 2 }, 3*time.Second, 20*time.Millisecond,
		"two seconds of a two-second interval have passed and nothing swept")
}

// TestBrowserCacheTrimSchedule_StopsWithTheGatewayContext keeps the loop from
// outliving the gateway. It is also what makes the tests above safe to run: a
// schedule that ignored ctx would leave a goroutine sweeping for the rest of the
// test binary's life.
func TestBrowserCacheTrimSchedule_StopsWithTheGatewayContext(t *testing.T) {
	fastReconcile(t, 5*time.Millisecond)

	pool := newCountingTrimPool(t, `{"cache_trim_interval": 1}`)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		runBrowserCacheTrimSchedule(ctx, pool)
		close(done)
	}()

	require.Eventually(t, func() bool { return pool.sweepCount() >= 2 }, 4*time.Second, 10*time.Millisecond,
		"the schedule must actually be running before cancelling proves anything")

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the cache-trim schedule did not return when the gateway's context was cancelled")
	}

	settled := pool.sweepCount()
	time.Sleep(1500 * time.Millisecond) // longer than the 1s interval it was sweeping on
	assert.Equal(t, settled, pool.sweepCount(),
		"the schedule swept again after returning — it is still walking profile directories on a "+
			"gateway that has shut down")
}
