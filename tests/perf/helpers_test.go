package perf

// startPerfGateway mirrors testutil.StartTestGateway but accepts testing.TB so
// it can be called from both *testing.T (SLO tests) and *testing.B (benchmarks).
// It boots the real gateway via gateway.RunContext (registered in TestMain) with
// DevModeBypass=true and a real OpenRouter provider entry. The test_harness
// override hook was removed 2026-05-10 — perf tests that exercise the LLM hit
// real OpenRouter (OPENROUTER_API_KEY required in env).

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/gateway"
)

// testMasterKey matches the value in pkg/agent/testutil/gateway_harness.go.
const testMasterKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

// perfGateway is a lightweight wrapper around a running gateway for perf tests.
type perfGateway struct {
	URL        string
	HTTPClient *http.Client
	cancel     context.CancelFunc
	done       chan struct{}
	bootErr    atomic.Pointer[error]
}

// close stops the gateway and waits up to 10 s for shutdown.
func (g *perfGateway) close(tb testing.TB) {
	tb.Helper()
	g.cancel()
	select {
	case <-g.done:
	case <-time.After(10 * time.Second):
		tb.Errorf("perfGateway.close: gateway goroutine did not stop within 10 s — goroutine leaked")
	}
	if p := g.bootErr.Load(); p != nil && *p != nil {
		tb.Errorf("perfGateway.close: gateway exited with error: %v", *p)
	}
}

// startPerfGateway boots a real gateway for perf tests. It accepts testing.TB
// so it works from both *testing.T (SLO tests) and *testing.B (benchmarks).
//
// The scenario parameter is unused (kept for signature compatibility with
// existing callers; the test_harness override hook was removed 2026-05-10).
// DevModeBypass is always true (no bearer auth required for WS connections).
//
// LLM traffic targets a mock OpenAI-compatible server started inside this
// function (see mockOpenRouterServer). This removes the dependency on real
// OpenRouter — benchmarks no longer need OPENROUTER_API_KEY, no longer pay
// API quota per CI run, and no longer fail when the upstream is rate-limited
// or slow. The mock exercises the full provider stack (HTTP, JSON decode,
// SSE streaming parse); only the network and the model latency are removed.
func startPerfGateway(tb testing.TB, _ *testutil.ScenarioProvider) *perfGateway {
	tb.Helper()

	tb.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
	tb.Setenv("OMNIPUS_BEARER_TOKEN", "")

	// Start a mock OpenAI-compatible provider before the gateway boots so
	// the config below can point APIBase at it.
	mock := mockOpenRouterServer(tb, "")

	homeDir := tb.TempDir()
	configPath := filepath.Join(homeDir, "config.json")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("startPerfGateway: allocate port: %v", err)
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		tb.Fatalf("startPerfGateway: listener address has unexpected type %T, want *net.TCPAddr", ln.Addr())
	}
	port := tcpAddr.Port
	if err = ln.Close(); err != nil {
		tb.Fatalf("startPerfGateway: close ephemeral listener: %v", err)
	}

	cfg := &config.Config{
		Version: 1,
		Gateway: config.GatewayConfig{
			Host:          "127.0.0.1",
			Port:          port,
			HotReload:     false,
			DevModeBypass: true,
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         homeDir,
				DefaultModel: config.DefaultModel{Provider: "openrouter", Model: "openrouter/z-ai/glm-5-turbo"},
				MaxTokens:    4096,
			},
		},
		Providers: []*config.ModelConfig{
			{
				Name:      "openrouter-glm",
				Model:     "openrouter/z-ai/glm-5-turbo",
				Provider:  "openrouter",
				APIBase:   mock.URL, // mock OpenRouter — see mockOpenRouterServer
				APIKeyRef: "OPENROUTER_API_KEY",
			},
		},
	}
	cfg.Sandbox.Mode = "off"

	rawCfg, err := json.Marshal(cfg)
	if err != nil {
		tb.Fatalf("startPerfGateway: marshal config: %v", err)
	}
	if err = os.WriteFile(configPath, rawCfg, 0o600); err != nil {
		tb.Fatalf("startPerfGateway: write config: %v", err)
	}

	if err = seedPerfCredentials(homeDir); err != nil {
		tb.Fatalf("startPerfGateway: seed credentials: %v", err)
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	gw := &perfGateway{
		URL:        baseURL,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
		cancel:     cancel,
		done:       done,
	}

	go func() {
		defer close(done)
		runErr := gateway.RunContext(ctx, false, homeDir, configPath, true /*allowEmpty*/)
		if runErr != nil {
			gw.bootErr.Store(&runErr)
		}
	}()

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
			var bootErrMsg string
			if p := gw.bootErr.Load(); p != nil {
				bootErrMsg = fmt.Sprintf(": boot error: %v", *p)
			}
			cancel()
			<-done
			tb.Fatalf("startPerfGateway: gateway at %s did not become ready within 5 s%s", baseURL, bootErrMsg)
		}
		time.Sleep(50 * time.Millisecond)
	}

	tb.Cleanup(func() { gw.close(tb) })
	return gw
}

// seedPerfCredentials writes credentials.json into homeDir with an
// OPENROUTER_API_KEY entry encrypted under testMasterKey. Mirrors the seed in
// pkg/agent/testutil/gateway_harness.go::seedTestCredentials.
func seedPerfCredentials(homeDir string) error {
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
