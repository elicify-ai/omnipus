package browser

// pool_ttl_config_reachability_test.go — the test tools.browser.idle_close_ttl
// and tools.browser.cache_trim_interval did not have.
//
// Both keys shipped documented (CHANGELOG.md, the configuration reference)
// and both were UNREACHABLE: config.BrowserToolConfig had no field for either,
// so an operator's `"idle_close_ttl": 120` in config.json was parsed into
// nothing and discarded, and pkg/agent never copied a value into the
// BrowserConfig the pool is built from. The pool's own `<= 0 means default`
// fallback then
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
// pkg/agent, because pkg/agent imports this package and not the other way
// round. TestBrowserTTLConfigKeys_HaveAWriter covers that hop at the source
// level — the same compromise, for the same reason, as the precedent.

import (
	"context"
	"fmt"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
// expressions pkg/agent applies, and builds a pool from the result.
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
	// ---- the production wiring (see TestBrowserTTLConfigKeys_HaveAWriter) ----
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
// trim schedule, as far as this package's boundary allows: the observable end
// of the chain from here is BrowserPool.CacheTrimInterval, because that is the
// single value the gateway's sweep reads.
//
// It is only HALF the reachability question, and the source assertion below is
// what says so honestly. This value being right was ALREADY true while the
// sweep ignored it: the gateway armed a time.Ticker once, at boot, from
// pool.CacheTrimInterval(), so a Settings save updated the pool and changed
// nothing an operator could observe until a restart. The behavioural half —
// lower the interval on a running schedule and watch the sweep follow — needs
// the gateway package and lives in
// pkg/gateway/browser_cache_trim_schedule_test.go.
func TestBrowserCacheTrimInterval_ConfigKeyIsActuallyRead(t *testing.T) {
	f := newPoolFromOperatorConfig(t, `{"cache_trim_interval": 90}`)

	assert.Equal(t, 90*time.Second, f.pool.CacheTrimInterval(),
		"the operator set tools.browser.cache_trim_interval to 90 seconds; the pool is still "+
			"handing the scheduler the 1-hour built-in, so the key changes nothing")

	// And the scheduler really does read its period from there, on EVERY round
	// rather than once — otherwise the value above would be a number nobody
	// ticks on, or one that only the next gateway restart would notice.
	gw := readSourceForTest(t, "../../gateway/gateway.go")
	assert.Contains(t, gw, "interval := pool.CacheTrimInterval()",
		"the scheduled trim must read the pool's configured interval inside its loop; if this "+
			"moved back out of the loop, the assertion above stopped describing anything an "+
			"operator can observe without restarting")
	assert.NotContains(t, gw, "time.NewTicker(pool.CacheTrimInterval())",
		"a ticker armed once from the interval is the restart-gated schedule this key was "+
			"documented not to have")
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

// codeOnlySource blanks every comment in src to spaces, using the real lexer
// (never a regex — `//` inside a string literal is not a comment, and this
// codebase is full of URLs). A comment is whitespace-equivalent in Go's
// grammar, so this cannot change what the code says; what it removes is
// commented-out CODE satisfying a source assertion. The twin of this helper
// in pkg/gateway/browser_workspace_drift_test.go carries the fuller story;
// the two are one approach, duplicated only because Go test helpers do not
// cross package boundaries.
func codeOnlySource(t *testing.T, path string, src []byte) []byte {
	t.Helper()
	fset := token.NewFileSet()
	file := fset.AddFile(path, fset.Base(), len(src))
	var s scanner.Scanner
	var scanErr error
	s.Init(file, src, func(pos token.Position, msg string) {
		if scanErr == nil {
			scanErr = fmt.Errorf("%s: %s", pos, msg)
		}
	}, scanner.ScanComments)
	out := make([]byte, len(src))
	copy(out, src)
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.COMMENT {
			start := file.Offset(pos)
			for i := start; i < start+len(lit) && i < len(out); i++ {
				out[i] = ' '
			}
		}
	}
	require.NoError(t, scanErr, "%s must scan cleanly — it compiles, so a scan error means the "+
		"tripwire is misreading what it guards", path)
	return out
}

// agentPackageSource concatenates every non-test .go file under pkg/agent,
// skipping testdata (Go never compiles those into a package), with comments
// blanked (see codeOnlySource). The tripwire below asserts against the
// concatenation rather than one hardcoded file because loop.go has already
// been split into siblings once — the TTL assignments live in loop_wire.go
// today — and a tripwire pinned to a filename breaks on the next split while
// the wiring it guards is intact, which trains the next reader to assume
// "stale" and weaken it. Same shape as pkg/gateway's agentPackageSource, for
// the same reason.
func agentPackageSource(t *testing.T) string {
	t.Helper()
	var src strings.Builder
	files := 0
	err := filepath.WalkDir(filepath.Join("..", "..", "agent"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		src.Write(codeOnlySource(t, path, b))
		src.WriteByte('\n')
		files++
		return nil
	})
	require.NoError(t, err,
		"pkg/agent must be walkable — a tripwire that cannot read what it guards proves nothing")
	require.NotZero(t, files,
		"the walk found no Go sources under pkg/agent — the path is wrong and every assertion "+
			"below would pass vacuously")
	require.Contains(t, src.String(), "package agent",
		"the concatenation must contain pkg/agent's own sources, not only its subpackages")
	return src.String()
}

// The two assignment matchers. Either side may hide behind an arbitrarily
// deep receiver chain — the loop.go split rewrote the plain `cfg` of the
// original lines into `bw.rw.cfg` — so the receivers are matched as
// identifier chains and only the load-bearing names are pinned: the
// destination FIELD on the BrowserConfig being assigned, and the loaded
// config's full `.Tools.Browser.Effective…()` chain. Those names are what
// keep this from being a "symbol name appears somewhere" check: the field
// assignment and the effective-value source must both be present, as one
// expression, for the operator's number to arrive at the pool.
var (
	browserIdleCloseTTLWriteRe = regexp.MustCompile(
		`[\w.]*\.IdleCloseTTL\s*=\s*[\w.]+\.Tools\.Browser\.EffectiveIdleCloseTTL\(\)`)
	browserCacheTrimWriteRe = regexp.MustCompile(
		`[\w.]*\.CacheTrimInterval\s*=\s*[\w.]+\.Tools\.Browser\.EffectiveCacheTrimInterval\(\)`)
)

// TestBrowserTTLConfigKeys_HaveAWriter covers the one hop the tests above
// cannot execute: pkg/agent is where the loaded config meets the
// BrowserConfig the pool is built from, and pkg/agent imports this package, so
// nothing here can call it.
//
// It asserts on the source for the same reason the actionability-gate
// precedent does. Delete either assignment and every other test in this file
// still passes while the operator's number stops arriving — which is precisely
// the state this whole file exists to make impossible. The scan covers the
// whole package rather than one file, so the assertion survives the code being
// split across siblings again.
func TestBrowserTTLConfigKeys_HaveAWriter(t *testing.T) {
	agentSrc := agentPackageSource(t)

	for _, tc := range []struct {
		re   *regexp.Regexp
		want string
	}{
		{browserIdleCloseTTLWriteRe,
			"browserCfg.IdleCloseTTL = cfg.Tools.Browser.EffectiveIdleCloseTTL()"},
		{browserCacheTrimWriteRe,
			"browserCfg.CacheTrimInterval = cfg.Tools.Browser.EffectiveCacheTrimInterval()"},
	} {
		assert.Regexp(t, tc.re, agentSrc,
			"%q is missing from pkg/agent — the key would be documented, parsed and then "+
				"dropped on the floor, which is the failure mode this project has shipped before", tc.want)
	}
}

// TestBrowserTTLDocs_StateTheUnit closes the loop the other way. These keys are
// SECONDS as an integer, matching idle_ttl / page_timeout / lease_wait beside
// them; an operator who copies a `15m` out of the documentation gets a config
// file that will not load at all. Documented defaults must also be the real
// ones, so the numbers are checked against the constants rather than trusted.
//
// The page is FOUND, not pinned: the handbook has been reorganised once
// already and carried these keys from docs/configuration.md into
// docs/operations/tools-configuration.md, so the test asks for whichever
// operator-facing page documents the key — the same shape
// readHandbookDocContaining's other callers use. A reorganisation that drops
// the keys from every page is a documentation regression this reports loudly,
// not a stale path to repoint.
func TestBrowserTTLDocs_StateTheUnit(t *testing.T) {
	_, doc := readHandbookDocContaining(t, "tools.browser.idle_close_ttl")

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
		// The page-level Contains above is not enough on its own: a number
		// or the word "seconds" anywhere on the page satisfied it while the
		// key's own row carried a different (or no) default or unit. Two
		// such collisions were found by mutation — a `900` used as the unit
		// EXAMPLE in the warning prose, and "fifteen seconds" in a sentence
		// that happened to name the key — so both row assertions are
		// anchored to the key's actual table row: a line starting with the
		// backticked key as its first cell.
		rowPrefix := "(?m)^\\| `" + regexp.QuoteMeta(key) + "`"
		assert.Regexp(t, regexp.MustCompile(rowPrefix+`[^\n]*`+want), doc,
			"the default for %s must be documented on the key's own row — a number that appears "+
				"elsewhere on the page is not this key's default", key)
		assert.Regexp(t, regexp.MustCompile(rowPrefix+`[^\n]*seconds`), doc,
			"%s's own row must state the unit — an operator who copies a `15m` out of the "+
				"documentation gets a config file that will not load at all", key)
	}
	assert.True(t,
		strings.Contains(strings.ToLower(doc), "seconds"),
		"the browser table must say these values are SECONDS — a documented `15m` is a config "+
			"file that fails to parse")
}
