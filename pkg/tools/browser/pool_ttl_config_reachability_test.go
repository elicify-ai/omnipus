package browser

// pool_ttl_config_reachability_test.go — the test tools.browser.idle_close_ttl
// and tools.browser.cache_trim_interval did not have.
//
// Both keys shipped documented (CHANGELOG.md, docs/configuration.md) and both
// were UNREACHABLE: config.BrowserToolConfig had no field for either, so an
// operator's `"idle_close_ttl": 120` in config.json was parsed into nothing and
// discarded, and pkg/agent/loop.go never copied a value into the BrowserConfig
// the pool is built from. The pool's own `<= 0 means default` fallback then
// made the failure invisible — every install silently ran the 15m/1h
// constants, and an operator who changed the number saw exactly the same
// behaviour as one who had not. That is the ADR-037 anti-pattern (a setting
// that confirms and changes nothing), and this project treats it as a release
// blocker.
//
// Written on the shape TestActionabilityGate_ConfigKeyIsActuallyRead
// established, but pushed one step further where the package boundary allows
// it. Reading a struct field back after setting it proves nothing about
// reachability, so these tests START at a real config.json on disk, load it
// through the production loader, and end at OBSERVED BEHAVIOUR: a browser that
// actually closes at the operator's number and not at the built-in one.
//
// The one hop that cannot be executed from here is the assignment inside
// pkg/agent/loop.go, because pkg/agent imports this package and not the other
// way round. TestBrowserTTLConfigKeys_HaveAWriter covers that hop at the
// source level — the same compromise, for the same reason, as the precedent.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// ttlConfigFixture is a pool built the way the gateway builds one, except that
// its two TTLs come from an operator's config.json rather than from a literal
// in the test.
type ttlConfigFixture struct {
	pool *BrowserPool
	now  *time.Time
}

func (f *ttlConfigFixture) advance(d time.Duration) { *f.now = f.now.Add(d) }

// newPoolFromOperatorConfig writes browserJSON as the tools.browser block of a
// real config.json, loads it with config.LoadConfig (the production loader,
// env parsing and removed-key validation included), applies the SAME two
// expressions pkg/agent/loop.go applies, and builds a pool from the result.
//
// Chrome is never launched: the pipe launcher and the memory reader are
// replaced by the package's ordinary test seams.
func newPoolFromOperatorConfig(t *testing.T, browserJSON string) *ttlConfigFixture {
	t.Helper()
	home := t.TempDir()

	cfgPath := filepath.Join(home, "config.json")
	body := fmt.Sprintf(`{"version": %d, "tools": {"browser": %s}}`, config.CurrentVersion, browserJSON)
	require.NoError(t, os.WriteFile(cfgPath, []byte(body), 0o600))

	loaded, err := config.LoadConfig(cfgPath)
	require.NoError(t, err, "an operator's config.json carrying these keys must load")

	cfg := BrowserConfig{
		Enabled:     true,
		Headless:    true,
		PageTimeout: 30 * time.Second,
		ProfileDir:  filepath.Join(home, "browser", "profiles", "default"),
		ExecPath:    fakeChromeBinary(t),
		IdleTTL:     DefaultIdleTTL,
	}
	// ---- the production wiring, verbatim (see TestBrowserTTLConfigKeys_HaveAWriter) ----
	cfg.IdleCloseTTL = loaded.Tools.Browser.EffectiveIdleCloseTTL()
	cfg.CacheTrimInterval = loaded.Tools.Browser.EffectiveCacheTrimInterval()
	// -----------------------------------------------------------------------------------

	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	p := NewBrowserPool(home, cfg)
	p.now = func() time.Time { return now }
	p.availableMemory = func() (uint64, bool) { return uint64(64) << 30, true }
	p.newCoordinator = func(homeDir string, c BrowserConfig, key BrowsingKey) *BrowserCoordinator {
		coord := newKeyedCoordinator(homeDir, c, key)
		coord.pipeLauncher = func(_ context.Context, _ string, _ pipeLaunchConfig) (*pipeLaunchResult, error) {
			ctx, cancel := context.WithCancel(context.Background())
			return &pipeLaunchResult{rootCtx: ctx, cancel: cancel}, nil
		}
		return coord
	}
	t.Cleanup(p.Shutdown)
	return &ttlConfigFixture{pool: p, now: &now}
}

// TestBrowserIdleCloseTTL_ConfigKeyIsActuallyRead drives tools.browser.idle_close_ttl
// from a file on disk to an observed close.
//
// The discriminating number is what makes it a reachability test: the operator
// asks for two minutes, and the test asserts a browser is STILL LIVE at 1m59s
// and GONE at 2m01s. On the unreachable build the pool ran the 15-minute
// constant, so nothing closed at 2m01s and this test fails — which is exactly
// the difference an operator could not see.
func TestBrowserIdleCloseTTL_ConfigKeyIsActuallyRead(t *testing.T) {
	f := newPoolFromOperatorConfig(t, `{"idle_close_ttl": 120}`)

	_, err := f.pool.Acquire(context.Background(), browserTestKey("alpha"))
	require.NoError(t, err)
	require.Equal(t, []string{"ws:alpha"}, f.pool.LiveKeys())

	f.advance(119 * time.Second)
	assert.Empty(t, f.pool.CloseIdle(*f.now),
		"one second short of the operator's idle_close_ttl, the browser must still be there")
	require.Equal(t, []string{"ws:alpha"}, f.pool.LiveKeys())

	f.advance(2 * time.Second) // 2m01s total
	assert.Equal(t, []string{"ws:alpha"}, f.pool.CloseIdle(*f.now),
		"the operator set tools.browser.idle_close_ttl to 120 seconds and the browser sat idle "+
			"past it. Nothing closed, so the key reaches nothing and the 15-minute built-in is "+
			"still what is running")
	assert.Empty(t, f.pool.LiveKeys(), "a closed browser must actually be gone from the pool")
}

// TestBrowserCacheTrimInterval_ConfigKeyIsActuallyRead does the same for the
// trim schedule. The observable end of the chain is BrowserPool.CacheTrimInterval,
// because that is the single value the gateway's trim goroutine ticks on —
// asserting it is asserting the sweep's actual period, not a struct field.
func TestBrowserCacheTrimInterval_ConfigKeyIsActuallyRead(t *testing.T) {
	f := newPoolFromOperatorConfig(t, `{"cache_trim_interval": 90}`)

	assert.Equal(t, 90*time.Second, f.pool.CacheTrimInterval(),
		"the operator set tools.browser.cache_trim_interval to 90 seconds; the pool is still "+
			"handing the scheduler the 1-hour built-in, so the key changes nothing")

	// And the scheduler really does take its period from there — otherwise the
	// value above would be a number nobody ticks on.
	gw := readSourceForTest(t, "../../gateway/gateway.go")
	assert.Contains(t, gw, "time.NewTicker(pool.CacheTrimInterval())",
		"the scheduled trim must tick on the pool's configured interval; if this call moved, "+
			"the assertion above stopped describing the sweep's real period")
}

// TestBrowserIdleCloseTTL_ZeroOrNegativeMeansDefaultNotDisabled is FR-061 seen
// from the config side: idle close is one of only two things bounding this
// pool's memory, so no operator value may switch it off. Unset (0) and a
// nonsense negative both mean "use the built-in", never "never close".
func TestBrowserIdleCloseTTL_ZeroOrNegativeMeansDefaultNotDisabled(t *testing.T) {
	for _, body := range []string{`{}`, `{"idle_close_ttl": 0}`, `{"idle_close_ttl": -30}`} {
		t.Run(body, func(t *testing.T) {
			f := newPoolFromOperatorConfig(t, body)

			_, err := f.pool.Acquire(context.Background(), browserTestKey("alpha"))
			require.NoError(t, err)

			f.advance(defaultIdleCloseTTL + time.Minute)
			assert.Equal(t, []string{"ws:alpha"}, f.pool.CloseIdle(*f.now),
				"with tools.browser.%s the built-in idle close must still fire — a config value "+
					"that disables one of the two memory controls is what FR-061 forbids", body)
		})
	}
}

// TestBrowserTTLConfigKeys_HaveAWriter covers the one hop the tests above
// cannot execute: pkg/agent/loop.go is where the loaded config meets the
// BrowserConfig the pool is built from, and pkg/agent imports this package, so
// nothing here can call it.
//
// It asserts on the source for the same reason the actionability-gate
// precedent does. Delete either assignment and every other test in this file
// still passes while the operator's number stops arriving — which is precisely
// the state this whole file exists to make impossible.
func TestBrowserTTLConfigKeys_HaveAWriter(t *testing.T) {
	loopSrc := readSourceForTest(t, "../../agent/loop.go")

	for _, want := range []string{
		"browserCfg.IdleCloseTTL = cfg.Tools.Browser.EffectiveIdleCloseTTL()",
		"browserCfg.CacheTrimInterval = cfg.Tools.Browser.EffectiveCacheTrimInterval()",
	} {
		assert.Contains(t, loopSrc, want,
			"%q is missing from pkg/agent/loop.go — the key would be documented, parsed and then "+
				"dropped on the floor, which is the failure mode this project has shipped before", want)
	}
}

// TestBrowserTTLDocs_StateTheUnit closes the loop the other way. These keys are
// SECONDS as an integer, matching idle_ttl / page_timeout / lease_wait beside
// them; an operator who copies a `15m` out of the documentation gets a config
// file that will not load at all. Documented defaults must also be the real
// ones, so the numbers are checked against the constants rather than trusted.
func TestBrowserTTLDocs_StateTheUnit(t *testing.T) {
	doc := readRepoDoc(t, "docs/configuration.md")

	require.Contains(t, doc, "tools.browser.idle_close_ttl")
	require.Contains(t, doc, "tools.browser.cache_trim_interval")

	for key, def := range map[string]time.Duration{
		"tools.browser.idle_close_ttl":      defaultIdleCloseTTL,
		"tools.browser.cache_trim_interval": defaultCacheTrimInterval,
	} {
		want := fmt.Sprintf("%d", int(def.Seconds()))
		assert.Contains(t, doc, want,
			"the documented default for %s must be the real one (%s = %s seconds) and must be "+
				"written in the unit the config file actually takes", key, def, want)
	}
	assert.True(t,
		strings.Contains(strings.ToLower(doc), "seconds"),
		"the browser table must say these values are SECONDS — a documented `15m` is a config "+
			"file that fails to parse")
}
