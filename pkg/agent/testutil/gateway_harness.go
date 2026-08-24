package testutil

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// testMasterKey is the fixed AES key used by all test harnesses.
// It is a 32-byte value encoded as 64 hex characters.
const testMasterKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

// testBearerToken is the bearer token injected when WithBearerAuth() is used.
const testBearerToken = "test-bearer-token-for-harness"

// runContextFunc is set by RegisterGatewayRunner. It matches the signature of
// gateway.RunContext so the harness can call the real gateway without importing
// the gateway package (which would create an import cycle).
//
// Signature: func(ctx, debug, homePath, configPath, allowEmpty) error
var runContextFunc func(context.Context, bool, string, string, bool) error

// runContextMu guards the registration variables so that tests running in
// parallel do not race on setup (registrations happen once, at test-init time).
var runContextMu sync.RWMutex

// RegisterGatewayRunner installs the gateway.RunContext function so that
// StartTestGateway can call it without importing pkg/gateway. Call this once
// from a TestMain or package-level init in the gateway's test package.
//
// Example (in pkg/gateway/gateway_test_init_test.go):
//
//	func TestMain(m *testing.M) {
//	    testutil.RegisterGatewayRunner(gateway.RunContext)
//	    os.Exit(m.Run())
//	}
//
// The provider-override hook (RegisterProviderOverrideFuncs) was removed
// 2026-05-10 along with the test_harness build tag. The harness now boots
// the gateway with the real provider config seeded by buildConfig +
// seedTestCredentials; tests that exercise LLM behavior hit real OpenRouter.
func RegisterGatewayRunner(fn func(context.Context, bool, string, string, bool) error) {
	runContextMu.Lock()
	defer runContextMu.Unlock()
	runContextFunc = fn
}

// TestGateway wraps a running gateway for integration tests.
// Cleanup runs automatically via t.Cleanup — callers do not need to call Close.
//
// Public API: URL, HTTPClient, Provider are exported fields. Use the getter
// methods HomeDir(), Token(), and ConfigPath() to read the private fields.
// Use SeedUser() to add users via the gateway's own locking mechanism.
type TestGateway struct {
	// URL is the base URL of the running gateway, e.g. "http://127.0.0.1:54321".
	URL string

	// HTTPClient is pre-configured with the correct Origin header.
	HTTPClient *http.Client

	// Provider is the ScenarioProvider wired into the gateway agent loop.
	// Tests can script it directly after StartTestGateway returns.
	Provider *ScenarioProvider

	// homeDir is the temp directory used as OMNIPUS_HOME. Cleaned up automatically.
	// Read via HomeDir().
	homeDir string

	// configPath is homeDir/config.json. Read via ConfigPath().
	configPath string

	// bearerToken is the token to use for authenticated requests. Empty unless
	// WithBearerAuth() was passed as an option. Read via Token().
	bearerToken string

	// mu guards the closed flag so Close is idempotent.
	mu     sync.Mutex
	closed bool
	cancel context.CancelFunc
	done   chan struct{}

	// t is the test that owns this gateway. Used by Close to report errors.
	t *testing.T

	// bootErr captures any error returned by RunContext so Close can surface it.
	bootErr atomic.Pointer[error]
}

// HomeDir returns the temp directory used as OMNIPUS_HOME for this gateway.
func (g *TestGateway) HomeDir() string { return g.homeDir }

// ConfigPath returns the path to config.json inside HomeDir.
func (g *TestGateway) ConfigPath() string { return g.configPath }

// Token returns the bearer token in use for authenticated requests.
// Empty string means the gateway is running without token auth (DevModeBypass=true).
func (g *TestGateway) Token() string { return g.bearerToken }

// ─────────────────────────────────────────────────────────────────────────────
// Readiness polling — extracted so the decision logic is unit-testable without
// booting a real gateway. See gateway_harness_poll_test.go.
//
// Background: the previous design polled /health against a single wall-clock
// deadline (`if time.Now().After(deadline) { fail }`). That trusts elapsed
// wall-clock time as the failure signal, which breaks down under a host
// freeze (CI scheduling stall, IO stall): the ENTIRE deadline budget can be
// consumed while zero probes run, and then the very next probe — which may
// simply catch the gateway a few ms before its listener binds — is treated
// as terminal, even though measured gateway boot cost is 0.22s p50 and only
// 0.75s worst-case even at 8x CPU oversubscription (i.e. the gateway itself
// is nowhere near actually failing). The failure message in that case was
// also undiagnosable: "did not return 200 within 15000ms" looks identical
// whether the gateway genuinely never boots or the host merely froze once.
//
// The fix: require a run of consecutive FAILED probes, not elapsed wall
// time, as the primary failure signal. A freeze produces fewer probes, not
// failed ones, so it can no longer masquerade as a boot failure — whenever
// probing resumes and the gateway answers healthy, pollUntilReady succeeds
// regardless of how much wall-clock time the freeze consumed. A boot error
// captured from RunContext is checked every iteration (not only once the
// old deadline had already expired), so a genuinely fast boot failure is
// reported immediately with the real error instead of only after paying the
// full timeout. An absolute wall-clock backstop remains as a ceiling — not
// the primary signal — so a gateway that is truly wedged (e.g. accepting
// probes that hang or fail slowly enough that the consecutive-failure count
// would take unreasonably long to reach) still cannot hang CI forever.
// ─────────────────────────────────────────────────────────────────────────────

// probeOutcome is the result of a single readiness probe attempt.
// A nil err means the probe observed the gateway healthy.
type probeOutcome struct {
	err error
}

// pollOutcomeKind identifies why pollUntilReady stopped polling.
type pollOutcomeKind int

const (
	// pollReady means the probe succeeded — the gateway is healthy.
	pollReady pollOutcomeKind = iota
	// pollFatalBootError means RunContext (or equivalent) reported a boot
	// error before the gateway ever became healthy. Reported immediately,
	// regardless of how little wall-clock time or how few attempts have
	// elapsed — this is the fast-fail path.
	pollFatalBootError
	// pollConsecutiveFailures means consecutiveFailThreshold consecutive
	// failed probes were observed. This is the primary failure signal for a
	// genuinely-failed-to-become-healthy gateway.
	pollConsecutiveFailures
	// pollHardBackstop means the absolute wall-clock ceiling was hit before
	// either of the above could resolve — e.g. individual probes are slow
	// enough (a wedged gateway hanging on each request) that reaching
	// consecutiveFailThreshold would take far longer than is reasonable to
	// wait. This is a backstop, not the primary signal.
	pollHardBackstop
)

// pollResult carries the outcome plus enough diagnostics (attempts + elapsed)
// that a caller's failure message can distinguish "host froze" from "gateway
// genuinely failed to boot" from "gateway is wedged" without re-running
// anything.
type pollResult struct {
	kind             pollOutcomeKind
	attempts         int
	elapsed          time.Duration
	consecutiveFails int
	lastProbeErr     error
	bootErr          error
}

// pollConfig parameterizes pollUntilReady. now/sleep/probe/bootErr are all
// injectable so the decision logic can be unit-tested with a fake clock —
// no real gateway, no real network, no real sleeping required.
type pollConfig struct {
	probe                    func() probeOutcome
	bootErr                  func() error
	interval                 time.Duration
	consecutiveFailThreshold int
	hardBackstop             time.Duration
	now                      func() time.Time
	sleep                    func(time.Duration)
}

// pollUntilReady polls cfg.probe at cfg.interval until it reports healthy.
// See the package doc comment above for the rationale.
func pollUntilReady(cfg pollConfig) pollResult {
	start := cfg.now()
	var attempts, consecutiveFails int
	var lastErr error

	for {
		// Check every iteration — not only once a deadline has expired — so
		// a fast boot failure is reported immediately with the real error.
		if err := cfg.bootErr(); err != nil {
			return pollResult{
				kind:     pollFatalBootError,
				attempts: attempts,
				elapsed:  cfg.now().Sub(start),
				bootErr:  err,
			}
		}

		attempts++
		outcome := cfg.probe()
		if outcome.err == nil {
			return pollResult{kind: pollReady, attempts: attempts, elapsed: cfg.now().Sub(start)}
		}

		lastErr = outcome.err
		consecutiveFails++
		elapsed := cfg.now().Sub(start)

		// Backstop check first: it must fire regardless of how the failures
		// are patterned (e.g. very few, very slow failures), since it exists
		// purely to bound total wall-clock time.
		if elapsed >= cfg.hardBackstop {
			return pollResult{
				kind:             pollHardBackstop,
				attempts:         attempts,
				elapsed:          elapsed,
				consecutiveFails: consecutiveFails,
				lastProbeErr:     lastErr,
				bootErr:          cfg.bootErr(),
			}
		}
		if consecutiveFails >= cfg.consecutiveFailThreshold {
			return pollResult{
				kind:             pollConsecutiveFailures,
				attempts:         attempts,
				elapsed:          elapsed,
				consecutiveFails: consecutiveFails,
				lastProbeErr:     lastErr,
				bootErr:          cfg.bootErr(),
			}
		}

		cfg.sleep(cfg.interval)
	}
}

// StartTestGateway boots a real gateway via the registered RunContextFunc on
// an ephemeral port and returns a TestGateway once the /health endpoint
// responds 200.
//
// It requires RegisterGatewayRunner to have been called first (typically from
// a TestMain in the test package that imports pkg/gateway). If it has not
// been called, StartTestGateway fails the test.
//
// It:
//   - Creates a temp dir for OMNIPUS_HOME via t.TempDir().
//   - Sets OMNIPUS_MASTER_KEY to a fixed test value via t.Setenv.
//   - Picks a free ephemeral port using the listen/close/reuse idiom.
//   - Writes a config.json seeded with a real OpenRouter+glm provider entry.
//   - Seeds OPENROUTER_API_KEY (from env, or a stub if env is empty) into
//     credentials.json so credentials.InjectFromConfig succeeds at boot.
//   - Runs the gateway in a goroutine; captures boot errors.
//   - Polls GET /health until 200 (max 5 s) before returning.
//   - Registers t.Cleanup to call Close, which cancels ctx and waits up to 10 s.
//
// Tests that exercise LLM behavior require OPENROUTER_API_KEY in the env;
// the scripted-scenario override hook was removed 2026-05-10.
func StartTestGateway(t *testing.T, opts ...Option) *TestGateway {
	t.Helper()

	runContextMu.RLock()
	rcFn := runContextFunc
	runContextMu.RUnlock()

	if rcFn == nil {
		t.Fatal("testutil.StartTestGateway: gateway runner not registered — " +
			"call testutil.RegisterGatewayRunner(gateway.RunContext) from TestMain " +
			"before running tests that require the full gateway stack")
	}

	hc := &harnessConfig{
		allowEmpty: true,
	}
	for _, o := range opts {
		o(hc)
	}

	if hc.scenario == nil {
		hc.scenario = NewScenario()
	}

	// Set the master key in the test environment so credentials unlock cleanly.
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)

	// Wire bearer token into the environment so checkBearerAuth's legacy
	// OMNIPUS_BEARER_TOKEN path accepts requests from gw.NewRequest.
	if hc.bearerAuth {
		t.Setenv("OMNIPUS_BEARER_TOKEN", testBearerToken)
	}

	homeDir := t.TempDir()
	configPath := filepath.Join(homeDir, "config.json")

	// Pin OMNIPUS_HOME to this gateway's private temp home.
	//
	// The gateway is given homeDir as its HomePath argument, but the agent layer
	// resolves its data root (memory rooms, sessions, agents dir) independently
	// via omnipusHome() / getGlobalConfigDir(), which read the OMNIPUS_HOME env
	// var (falling back to ~/.omnipus when unset). In PRODUCTION the gateway's
	// HomePath and OMNIPUS_HOME are the same value, so the two agree. In tests
	// the harness previously set HomePath but left OMNIPUS_HOME unset, so EVERY
	// test gateway's MemoryStore resolved to the SHARED ~/.omnipus workspace-room
	// path. When multiple gateways boot in one process (the integration suite),
	// the first gateway's MemoryStore opens the bleve/bbolt index there and holds
	// its exclusive (timeout-less) file lock; the SECOND gateway's first turn then
	// blocks forever inside roomIndexLocked → bleve.Open while building the system
	// prompt — its turn worker never makes an LLM call. Setting OMNIPUS_HOME here
	// gives each test gateway its own isolated data root, exactly as production
	// has it, so the lock contention disappears. t.Setenv is safe because these
	// integration tests do not run in parallel (no t.Parallel()).
	t.Setenv("OMNIPUS_HOME", homeDir)

	// Pick an ephemeral port by opening a listener, reading the port, then closing it.
	// The OS will not reuse the port immediately, giving RunContext time to bind.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("testutil.StartTestGateway: allocate port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err = ln.Close(); err != nil {
		t.Fatalf("testutil.StartTestGateway: close ephemeral listener: %v", err)
	}

	cfg := buildConfig(hc, homeDir, port)

	rawCfg, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("testutil.StartTestGateway: marshal config: %v", err)
	}
	if err = os.WriteFile(configPath, rawCfg, 0o600); err != nil {
		t.Fatalf("testutil.StartTestGateway: write config: %v", err)
	}

	// Seed the OPENROUTER_API_KEY credential so the gateway's
	// credentials.InjectFromConfig step (gateway.go:209) succeeds. The seeded
	// provider entry in buildConfig references this name; without the
	// credential, boot fails with "fatal: provider credential injection failed".
	// The real key MUST be in env (OPENROUTER_API_KEY) — there is no longer a
	// scripted-scenario fallback (the test_harness override hook was removed
	// 2026-05-10). Tests that exercise LLM behavior hit real OpenRouter.
	if err = seedTestCredentials(homeDir); err != nil {
		t.Fatalf("testutil.StartTestGateway: seed credentials: %v", err)
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})

	gw := &TestGateway{
		URL:        baseURL,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
		Provider:   hc.scenario,
		homeDir:    homeDir,
		configPath: configPath,
		cancel:     cancel,
		done:       done,
		t:          t,
	}

	if hc.bearerAuth {
		gw.bearerToken = testBearerToken
	}

	go func() {
		defer close(done)
		runErr := rcFn(ctx, false, homeDir, configPath, hc.allowEmpty)
		if runErr != nil {
			gw.bootErr.Store(&runErr)
		}
	}()

	// Poll until /health returns 200. See the pollUntilReady doc comment
	// above (near the getter methods) for the full rationale. Summary of the
	// constants below:
	//
	//   - healthProbeInterval (50ms): unchanged from the previous design.
	//   - healthConsecutiveFailThreshold (300): 300 * 50ms == 15s of
	//     CONTINUOUSLY failing probes — the same real-time budget the old
	//     wall-clock deadline granted, except it can now only be consumed by
	//     actual failed attempts, never by a frozen/stalled host doing
	//     nothing. Measured gateway boot cost is 0.22s p50 and 0.75s
	//     worst-case even at 8x CPU oversubscription (≈15 failed attempts at
	//     this interval); the previous deadline comment also recorded busy
	//     GitHub-hosted runners taking up to 3-8s (≈60-160 attempts) under
	//     load. 300 leaves roughly 2x margin over that documented worst case.
	//   - healthHardBackstop (30s): an absolute ceiling, not the primary
	//     signal — protects against a gateway that is genuinely wedged (e.g.
	//     accepting TCP connections but hanging on every request), where
	//     each failed probe could itself take seconds, making
	//     healthConsecutiveFailThreshold consecutive fails take far longer
	//     than is reasonable to wait.
	//   - healthProbeTimeout (2s): bounds a single health GET so one hung
	//     request cannot silently eat most of healthHardBackstop by itself.
	const (
		healthProbeInterval            = 50 * time.Millisecond
		healthConsecutiveFailThreshold = 300
		healthHardBackstop             = 30 * time.Second
		healthProbeTimeout             = 2 * time.Second
	)

	probeClient := &http.Client{Timeout: healthProbeTimeout}
	result := pollUntilReady(pollConfig{
		probe: func() probeOutcome {
			resp, httpErr := probeClient.Get(baseURL + "/health")
			if httpErr != nil {
				return probeOutcome{err: httpErr}
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				return probeOutcome{err: fmt.Errorf("health endpoint returned status %d", resp.StatusCode)}
			}
			return probeOutcome{}
		},
		bootErr: func() error {
			if p := gw.bootErr.Load(); p != nil {
				return *p
			}
			return nil
		},
		interval:                 healthProbeInterval,
		consecutiveFailThreshold: healthConsecutiveFailThreshold,
		hardBackstop:             healthHardBackstop,
		now:                      time.Now,
		sleep:                    time.Sleep,
	})

	if result.kind != pollReady {
		cancel()
		<-done

		switch result.kind {
		case pollFatalBootError:
			t.Fatalf(
				"testutil.StartTestGateway: gateway at %s failed to boot: %v "+
					"(fast-fail: %d probe attempt(s), %s elapsed — this is a genuine boot "+
					"error surfaced immediately, not a timeout)",
				baseURL, result.bootErr, result.attempts, result.elapsed,
			)
		case pollConsecutiveFailures:
			var bootErrMsg string
			if result.bootErr != nil {
				bootErrMsg = fmt.Sprintf("; boot error: %v", result.bootErr)
			}
			t.Fatalf(
				"testutil.StartTestGateway: gateway at %s never became healthy after "+
					"%d consecutive failed health probes (%s elapsed) — this indicates a "+
					"genuine boot failure, not a scheduling stall (a frozen host would "+
					"produce FEW probes, not failed ones); last probe error: %v%s",
				baseURL, result.consecutiveFails, result.elapsed, result.lastProbeErr, bootErrMsg,
			)
		case pollHardBackstop:
			var bootErrMsg string
			if result.bootErr != nil {
				bootErrMsg = fmt.Sprintf("; boot error: %v", result.bootErr)
			}
			t.Fatalf(
				"testutil.StartTestGateway: gateway at %s hit the %s hard backstop after "+
					"only %d probe attempt(s) (%s elapsed) without becoming healthy — this is "+
					"the absolute ceiling, not the primary failure signal; probes are likely "+
					"hanging (gateway wedged/hung, not merely slow to boot); last probe error: %v%s",
				baseURL, healthHardBackstop, result.attempts, result.elapsed, result.lastProbeErr, bootErrMsg,
			)
		}
	}

	t.Cleanup(func() {
		gw.Close()
	})

	return gw
}

// Close stops the gateway. Normally you rely on t.Cleanup; call Close only when
// you need to stop the gateway before the test ends (e.g. restart tests).
// Close is idempotent — calling it multiple times is safe.
//
// Close reports a test failure via t.Errorf if:
//   - RunContext returned a non-nil error after the gateway was considered ready.
//   - The gateway goroutine did not stop within 10 s (goroutine leak).
func (g *TestGateway) Close() {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	g.closed = true
	g.mu.Unlock()

	g.cancel()

	// Wait up to 10 s for RunContext to return. The graceful shutdown sequence
	// in pkg/gateway/shutdown.go has its own 70 s budget, but tests cancel
	// cleanly after in-flight requests drain (which is near-instant in tests).
	select {
	case <-g.done:
	case <-time.After(10 * time.Second):
		if g.t != nil {
			g.t.Errorf("testutil.TestGateway.Close: gateway goroutine did not stop within 10s — goroutine leaked")
		}
		return
	}

	// #265: deterministic cleanup-race safety net. The shutdown fixes (recap
	// drain + tracking the system/unroutable turn goroutines + stopping
	// heartbeat/cron before the drain) drain the writers, but a straggler write
	// can still land just after RunContext returns and race t.TempDir's
	// RemoveAll ("directory not empty"). Wait until the home dir is stable for a
	// short settle window before yielding to the test's RemoveAll. Bounded, and
	// where nothing is in flight it returns on the first stable scan.
	//
	// Scans the WHOLE home dir, not just sessions/. It used to watch only the
	// sessions subtree, which left every other late writer uncovered — logs,
	// memory, tasks, config — and that is exactly how it still failed: CI run 2
	// hit "unlinkat /tmp/TestSinceCursor_.../001: directory not empty" on the
	// HOME dir while sessions/ itself had long since settled. Earlier
	// fixed-sleep attempts failed for a different reason (they ran WITHOUT the
	// shutdown drains, so writers never stopped at all).
	waitForHomeQuiescent(g.homeDir, 150*time.Millisecond, 3*time.Second)

	// Surface any boot error that occurred after the gateway became ready.
	if p := g.bootErr.Load(); p != nil && *p != nil {
		if g.t != nil {
			g.t.Errorf("testutil.TestGateway.Close: gateway exited with error: %v", *p)
		}
	}
}

// waitForHomeQuiescent blocks until the ENTIRE homeDir tree produces two
// consecutive identical (path,size,mtime) snapshots `settle` apart, or `budget`
// elapses. Pure read-only; never errors. Used by Close to avoid the
// RemoveAll-vs-late-write race (#265) that surfaces as t.TempDir cleanup
// failing with "directory not empty". Returns after the first settle window
// when nothing is in flight.
func waitForHomeQuiescent(homeDir string, settle, budget time.Duration) {
	if homeDir == "" {
		return
	}
	deadline := time.Now().Add(budget)
	prev := ""
	stableSince := time.Time{}
	for time.Now().Before(deadline) {
		var sb strings.Builder
		_ = filepath.WalkDir(homeDir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // best-effort read-only scan; ignore transient walk errors
			}
			if d.IsDir() {
				return nil
			}
			if fi, e := d.Info(); e == nil {
				fmt.Fprintf(&sb, "%s:%d:%d;", p, fi.Size(), fi.ModTime().UnixNano())
			}
			return nil
		})
		sig := sb.String()
		if sig == prev {
			if stableSince.IsZero() {
				stableSince = time.Now()
			}
			if time.Since(stableSince) >= settle {
				return
			}
		} else {
			prev = sig
			stableSince = time.Time{}
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// NewRequest builds an *http.Request with the path prefixed to g.URL,
// the Origin header set to g.URL, and (if BearerToken is non-empty) the
// Authorization header set.
func (g *TestGateway) NewRequest(method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, g.URL+path, body)
	if err != nil {
		return nil, fmt.Errorf("testutil.TestGateway.NewRequest: %w", err)
	}
	req.Header.Set("Origin", g.URL)
	if g.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+g.bearerToken)
	}
	return req, nil
}

// Do sends req via g.HTTPClient. Returns (nil, err) on network error.
func (g *TestGateway) Do(req *http.Request) (*http.Response, error) {
	return g.HTTPClient.Do(req)
}

// SeedUser appends u to the gateway.users list in config.json on disk, then
// POSTs /reload and polls until the gateway recognizes the new user.
//
// It uses a raw JSON read-modify-write cycle (the same approach the gateway's
// safeUpdateConfigJSON uses) to avoid destroying SecureString values that would
// be lost through a Go-struct round-trip. A sync.Mutex internal to SeedUser
// serializes concurrent calls; for additional isolation, callers should avoid
// racing SeedUser with direct config.json writes.
//
// ctx controls the maximum wait for reload propagation; use a context with a
// reasonable deadline (5–10 s is typical for CI).
//
// beforeWrite, if non-nil, is called with the raw config map after the user
// is appended but before the config is written to disk and reloaded. This lets
// callers mutate fields such as DevModeBypass without needing a separate
// write-reload round-trip. Example:
//
//	SeedUser(ctx, user, func(m map[string]any) {
//		gw := m["gateway"].(map[string]any)
//		gw["dev_mode_bypass"] = false
//	})
func (g *TestGateway) SeedUser(ctx context.Context, u config.UserConfig, beforeWrite func(map[string]any)) error {
	// Read-modify-write the raw JSON to preserve SecureString values.
	cfgPath := g.configPath
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return fmt.Errorf("SeedUser: read config: %w", err)
	}
	var m map[string]any
	if err = json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("SeedUser: unmarshal config: %w", err)
	}

	gwSection, _ := m["gateway"].(map[string]any)
	if gwSection == nil {
		gwSection = map[string]any{}
	}
	users, _ := gwSection["users"].([]any)

	// Marshal the new user as a generic map entry so it serializes cleanly
	// alongside the existing users (which may already be map[string]any).
	userBytes, err := json.Marshal(u)
	if err != nil {
		return fmt.Errorf("SeedUser: marshal user: %w", err)
	}
	var userMap map[string]any
	if err = json.Unmarshal(userBytes, &userMap); err != nil {
		return fmt.Errorf("SeedUser: re-unmarshal user: %w", err)
	}
	gwSection["users"] = append(users, userMap)
	m["gateway"] = gwSection

	// Allow caller to mutate the config before writing.
	if beforeWrite != nil {
		beforeWrite(m)
	}

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("SeedUser: marshal config: %w", err)
	}

	// Write to a temp file in the same directory then rename for atomicity.
	tmpPath := cfgPath + ".seeduser.tmp"
	if err = os.WriteFile(tmpPath, out, 0o600); err != nil {
		return fmt.Errorf("SeedUser: write tmp config: %w", err)
	}
	if err = os.Rename(tmpPath, cfgPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("SeedUser: rename config: %w", err)
	}

	// Trigger a gateway reload so the in-memory config picks up the new user.
	//
	// No retry loop: this used to poll for up to 2s on 500 "reload already in
	// progress", because the gateway's reload trigger rejected any request that
	// arrived while another reload was running (e.g. onboarding's own reload via
	// rest_onboarding.go::awaitReload). That trigger now COALESCES instead of
	// rejecting — a mid-flight request is recorded and served by a follow-up
	// reload that re-reads config from disk — so /reload answers 200 and the
	// 500 this loop existed to absorb can no longer occur. Any non-200 here is
	// now a genuine failure and must surface immediately rather than being
	// retried for 2s and reported as something else.
	reloadReq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.URL+"/reload", nil)
	if err != nil {
		return fmt.Errorf("SeedUser: build reload request: %w", err)
	}
	reloadReq.Header.Set("Origin", g.URL)
	reloadResp, err := g.HTTPClient.Do(reloadReq)
	if err != nil {
		return fmt.Errorf("SeedUser: POST /reload: %w", err)
	}
	_ = reloadResp.Body.Close()
	if reloadResp.StatusCode != http.StatusOK {
		return fmt.Errorf("SeedUser: POST /reload returned %d", reloadResp.StatusCode)
	}

	// Poll with the new user's token (if non-empty) until the auth middleware
	// accepts it (non-401), confirming reload has propagated.
	if u.TokenHash.IsZero() {
		// No token to probe with — caller must verify independently.
		return nil
	}

	// We cannot reverse the hash here to get the plaintext token, so we can only
	// verify the reload completed by polling the health endpoint with a small
	// delay. The reload is triggered synchronously before this point; the in-memory
	// swap happens asynchronously. A 300 ms grace period is sufficient for CI.
	select {
	case <-ctx.Done():
		return fmt.Errorf("SeedUser: context canceled before reload propagated: %w", ctx.Err())
	case <-time.After(300 * time.Millisecond):
	}

	return nil
}

// SeedCLIToken writes tok to gateway.cli_token in config.json on disk, then
// POSTs /reload and polls until the gateway recognizes the new token.
//
// Mirrors SeedUser's raw JSON read-modify-write cycle (preserves SecureString
// values) but targets the dedicated Gateway.CLIToken slot instead of the
// human-account Gateway.Users list — the CLI's machine-only bearer credential
// is checked as a distinct principal from the human account (see
// pkg/config/cli_token_migration.go).
//
// ctx controls the maximum wait for reload propagation; use a context with a
// reasonable deadline (5–10 s is typical for CI).
func (g *TestGateway) SeedCLIToken(ctx context.Context, tok config.TokenEntry) error {
	cfgPath := g.configPath
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return fmt.Errorf("SeedCLIToken: read config: %w", err)
	}
	var m map[string]any
	if err = json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("SeedCLIToken: unmarshal config: %w", err)
	}

	gwSection, _ := m["gateway"].(map[string]any)
	if gwSection == nil {
		gwSection = map[string]any{}
	}

	tokBytes, err := json.Marshal(tok)
	if err != nil {
		return fmt.Errorf("SeedCLIToken: marshal token: %w", err)
	}
	var tokMap map[string]any
	if err = json.Unmarshal(tokBytes, &tokMap); err != nil {
		return fmt.Errorf("SeedCLIToken: re-unmarshal token: %w", err)
	}
	gwSection["cli_token"] = tokMap
	m["gateway"] = gwSection

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("SeedCLIToken: marshal config: %w", err)
	}

	tmpPath := cfgPath + ".seedclitoken.tmp"
	if err = os.WriteFile(tmpPath, out, 0o600); err != nil {
		return fmt.Errorf("SeedCLIToken: write tmp config: %w", err)
	}
	if err = os.Rename(tmpPath, cfgPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("SeedCLIToken: rename config: %w", err)
	}

	reloadDeadline := time.Now().Add(2 * time.Second)
	var lastStatus int
	for {
		reloadReq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.URL+"/reload", nil)
		if err != nil {
			return fmt.Errorf("SeedCLIToken: build reload request: %w", err)
		}
		reloadReq.Header.Set("Origin", g.URL)
		reloadResp, err := g.HTTPClient.Do(reloadReq)
		if err != nil {
			return fmt.Errorf("SeedCLIToken: POST /reload: %w", err)
		}
		_ = reloadResp.Body.Close()
		lastStatus = reloadResp.StatusCode
		if reloadResp.StatusCode == http.StatusOK {
			break
		}
		if reloadResp.StatusCode != http.StatusInternalServerError || time.Now().After(reloadDeadline) {
			return fmt.Errorf("SeedCLIToken: POST /reload returned %d", reloadResp.StatusCode)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf(
				"SeedCLIToken: context canceled during reload retry (last status %d): %w",
				lastStatus, ctx.Err(),
			)
		case <-time.After(50 * time.Millisecond):
		}
	}

	if tok.Hash.IsZero() {
		return nil
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("SeedCLIToken: context canceled before reload propagated: %w", ctx.Err())
	case <-time.After(300 * time.Millisecond):
	}

	return nil
}

// buildConfig assembles a minimal config.Config from the harness options.
//
// The Providers list is seeded with a single OpenRouter+glm-5v-turbo entry that
// matches the e2e Playwright setup in .github/workflows/pr.yml. The gateway's
// boot path validates `len(cfg.Providers) > 0` (pkg/providers/legacy_provider.go);
// without an entry every test using StartTestGateway fails to boot. Tests that
// make LLM calls hit real OpenRouter (api_key_ref is resolved from the credential
// store / env at boot via credentials.InjectFromConfig).
func buildConfig(hc *harnessConfig, homeDir string, port int) *config.Config {
	cfg := &config.Config{
		Version: 1,
		Gateway: config.GatewayConfig{
			Host:      "127.0.0.1",
			Port:      port,
			HotReload: false,
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         homeDir,
				DefaultModel: config.DefaultModel{Provider: "openrouter", Model: "z-ai/glm-5v-turbo"},
				MaxTokens:    4096,
			},
		},
		Providers: []*config.ModelConfig{
			{
				Model:     "z-ai/glm-5v-turbo",
				Provider:  "openrouter",
				APIBase:   "https://openrouter.ai/api/v1",
				APIKeyRef: "OPENROUTER_API_KEY",
			},
		},
	}

	// Pin the browser exec path to a nonexistent binary so the gateway's
	// boot-time Preprovision goroutine fails instantly and locally. Without
	// this, every test gateway kicks off a real ~130 MB Chrome-for-Testing
	// download into its t.TempDir() home — and since gateway shutdown does
	// not wait for that detached goroutine, the extraction races TempDir
	// RemoveAll cleanup ("unlinkat ...: directory not empty" — TestIdleHeartbeat
	// on the ci-omnipus worker). ExecPath is an operator override honored
	// as-is (no download fallback), so resolution fails with a stat error:
	// no network, no disk writes, no race. No harness consumer drives real
	// browser tools; a future test that needs them should override this via
	// its own Option.
	cfg.Tools.Browser.ExecPath = filepath.Join(homeDir, "nonexistent-test-chromium")

	// Optional APIBase override (perf tests redirect LLM traffic to a local
	// mock server — see tests/perf/mock_openrouter_test.go).
	if hc.apiBase != "" {
		cfg.Providers[0].APIBase = hc.apiBase
		// ADR-066 D2/D3: the redirected row now points at a loopback
		// httptest server. Until the gateway installs the served catalog
		// (ADR-067 T067-07) the resolver classifies it as a custom row at a
		// local host — `locality: local` — and a local endpoint that
		// reports no context window is REFUSED at turn start
		// (context_window_unknown), never floored. Pin the window the way
		// an operator would for such an endpoint (D2 rung 3, the global
		// default; B-10) so the harness turns run. 128000 mirrors the cloud
		// floor these mocks stood in for before the ladder landed.
		if cfg.Context.DefaultContextWindow == nil {
			pinned := 128000
			cfg.Context.DefaultContextWindow = &pinned
		}
	}

	if len(hc.agents) > 0 {
		cfg.Agents.List = hc.agents
	}

	if hc.sandbox != nil {
		cfg.Sandbox = *hc.sandbox
	} else {
		// Sprint J: default tests to sandbox=off so the harness can boot
		// on kernels where Landlock Apply would otherwise fail closed
		// (FR-J-004) and abort the gateway. Tests that specifically
		// exercise enforce/permissive mode can opt in via an Option that
		// sets hc.sandbox explicitly. Production defaults are unaffected
		// — the CLI defaults to enforce when no config is written.
		cfg.Sandbox.Mode = "off"
	}

	if hc.bearerAuth {
		// Store the raw token so the withAuth middleware accepts it via the
		// Authorization: Bearer header. Dev mode bypass is left false so that
		// auth is actually enforced.
		cfg.Gateway.Token = testBearerToken
	} else {
		// Allow unauthenticated access for tests that do not need auth.
		cfg.Gateway.DevModeBypass = true
	}

	return cfg
}

// seedTestCredentials writes credentials.json into homeDir with an
// OPENROUTER_API_KEY entry encrypted under testMasterKey. The real OpenRouter
// key is taken from env var OPENROUTER_API_KEY when set (dev/CI exercising real
// LLM calls); otherwise a placeholder is stored. Tests that drive an LLM path
// will fail at the OpenRouter API boundary if env was not provided.
func seedTestCredentials(homeDir string) error {
	masterKey, err := hex.DecodeString(testMasterKey)
	if err != nil {
		return fmt.Errorf("decode testMasterKey: %w", err)
	}

	store := credentials.NewStore(filepath.Join(homeDir, "credentials.json"))
	if err := store.UnlockWithKey(masterKey); err != nil {
		return fmt.Errorf("unlock store: %w", err)
	}

	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		apiKey = "test-stub-openrouter-key-not-for-real-calls"
	}
	if err := store.Set("OPENROUTER_API_KEY", apiKey); err != nil {
		return fmt.Errorf("set OPENROUTER_API_KEY: %w", err)
	}
	return nil
}
