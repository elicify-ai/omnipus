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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
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

	// Poll until /health returns 200 or the deadline expires.
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, httpErr := gw.HTTPClient.Get(baseURL + "/health")
		if httpErr == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			// Surface any boot error for diagnostics.
			var bootErrMsg string
			if p := gw.bootErr.Load(); p != nil {
				bootErrMsg = fmt.Sprintf(": boot error: %v", *p)
			}
			cancel()
			<-done
			t.Fatalf("testutil.StartTestGateway: gateway at %s did not become ready within 5s%s", baseURL, bootErrMsg)
		}
		time.Sleep(50 * time.Millisecond)
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

	// Drain hold-off — RunContext returning does NOT guarantee every
	// background goroutine that touches the session store, cost tracker, or
	// audit logger has finished its final write/rename. On macOS APFS and
	// some Linux runners, t.TempDir's RemoveAll fires immediately after this
	// returns and races those writers ("directory not empty" / cost.json
	// rename misses). Wait for the sessions/ subtree to stop changing for a
	// short window before yielding to the caller — drain is best-effort and
	// capped, never blocks the test on its own. A proper fix wires the
	// session-store backend's Close into omnipusGracefulShutdown (tracked
	// as v0.2).
	waitForHomeDirStability(g.homeDir, 200*time.Millisecond, 3*time.Second)

	// Surface any boot error that occurred after the gateway became ready.
	if p := g.bootErr.Load(); p != nil && *p != nil {
		if g.t != nil {
			g.t.Errorf("testutil.TestGateway.Close: gateway exited with error: %v", *p)
		}
	}
}

// waitForHomeDirStability polls homeDir/sessions until two consecutive scans
// produce the same (file-count, total-size) snapshot, or the budget elapses.
// Caller-side hold-off for the documented gateway-shutdown drain race; see
// TestGateway.Close. Pure read-only — never errors, never blocks beyond budget.
func waitForHomeDirStability(homeDir string, settleWindow, budget time.Duration) {
	if homeDir == "" {
		return
	}
	sessionsDir := filepath.Join(homeDir, "sessions")
	deadline := time.Now().Add(budget)
	var prevCount, prevSize int64 = -1, -1
	stableSince := time.Time{}
	for time.Now().Before(deadline) {
		var count, size int64
		_ = filepath.WalkDir(sessionsDir, func(_ string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			info, infoErr := d.Info()
			if infoErr != nil {
				return infoErr
			}
			count++
			size += info.Size()
			return nil
		})
		if count == prevCount && size == prevSize {
			if stableSince.IsZero() {
				stableSince = time.Now()
			}
			if time.Since(stableSince) >= settleWindow {
				return
			}
		} else {
			stableSince = time.Time{}
			prevCount, prevSize = count, size
		}
		time.Sleep(50 * time.Millisecond)
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
	// Retry on 500 "reload already in progress" — this is a transient race when
	// onboarding's own reload (rest_onboarding.go::awaitReload) is still in
	// flight. The reloading flag is cleared in executeReload's defer, so
	// re-issuing /reload after a short backoff succeeds.
	reloadDeadline := time.Now().Add(2 * time.Second)
	var lastStatus int
	for {
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
		lastStatus = reloadResp.StatusCode
		if reloadResp.StatusCode == http.StatusOK {
			break
		}
		if reloadResp.StatusCode != http.StatusInternalServerError || time.Now().After(reloadDeadline) {
			return fmt.Errorf("SeedUser: POST /reload returned %d", reloadResp.StatusCode)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf(
				"SeedUser: context canceled during reload retry (last status %d): %w",
				lastStatus, ctx.Err(),
			)
		case <-time.After(50 * time.Millisecond):
		}
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
				Workspace: homeDir,
				ModelName: "openrouter-glm",
				MaxTokens: 4096,
			},
		},
		Providers: []*config.ModelConfig{
			{
				ModelName: "openrouter-glm",
				Model:     "openrouter/z-ai/glm-5v-turbo",
				Provider:  "openrouter",
				APIBase:   "https://openrouter.ai/api/v1",
				APIKeyRef: "OPENROUTER_API_KEY",
			},
		},
	}

	// Optional APIBase override (perf tests redirect LLM traffic to a local
	// mock server — see tests/perf/mock_openrouter_test.go).
	if hc.apiBase != "" {
		cfg.Providers[0].APIBase = hc.apiBase
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
