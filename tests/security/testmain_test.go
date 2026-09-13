// Package security_test — PR-D security test stack.
//
// TestMain registers the real gateway.RunContext into pkg/agent/testutil so
// that StartTestGateway can boot the full gateway without creating an import
// cycle.
//
// This file carries no build tag: it compiles under both CGO_ENABLED=0 and
// CGO_ENABLED=1, so the package is race-testable. Build with the canonical
// -tags goolm,stdjson.
//
// All tests in this package share this TestMain.
package security_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway"
)

func TestMain(m *testing.M) {
	testutil.RegisterGatewayRunner(runSecurityGateway)
	os.Exit(m.Run())
}

func runSecurityGateway(ctx context.Context, debug bool, homePath, configPath string, allowEmpty bool) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read security fixture config: %w", err)
	}
	var cfg config.Config
	if decodeErr := json.Unmarshal(data, &cfg); decodeErr != nil {
		return fmt.Errorf("decode security fixture config: %w", decodeErr)
	}
	// These tests exercise real gateway security routes, not a hosted LLM.
	// A custom cloud endpoint without a credential skips live model-limit
	// discovery, whose detached cache writer can outlive gateway shutdown and
	// race TempDir cleanup. A context-window override alone still queries live
	// limits. The reserved .invalid endpoint also prevents accidental LLM use.
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Provider: "security-fixture", Model: "security-model"}
	cfg.Providers = []*config.ModelConfig{{
		Provider: "security-fixture",
		Model:    "security-model",
		Custom:   true,
		Protocol: "openai-compatible",
		APIBase:  "https://security-provider.invalid/v1",
	}}
	data, err = json.MarshalIndent(&cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode security fixture config: %w", err)
	}
	if writeErr := os.WriteFile(configPath, data, 0o600); writeErr != nil {
		return fmt.Errorf("write security fixture config: %w", writeErr)
	}
	return gateway.RunContext(ctx, debug, homePath, configPath, allowEmpty)
}
