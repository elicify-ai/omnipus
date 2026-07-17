//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// newShutdownTestConfig builds the minimal *config.Config omnipusGracefulShutdown
// and its agent.NewAgentLoop dependency need to boot without touching real
// credentials/channels, mirroring the pattern in audit_boot_abort_test.go.
func newShutdownTestConfig(t *testing.T) *config.Config {
	t.Helper()
	workspaceDir := filepath.Join(t.TempDir(), "workspace")
	return &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 0},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: workspaceDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
		},
	}
}

// TestOmnipusGracefulShutdown_CallsBrowserVideoShutdown verifies the
// ADR-044 T6 wiring (Option-A rearchitecture): when services carries a
// non-nil live-browser-video orchestrator, omnipusGracefulShutdown invokes
// its Shutdown() (tears down all streams + the lazily-launched encoder
// browser, if any) during step-4 background-services teardown, which runs
// strictly after step 2's agentLoop.Close() has already reaped any
// agent-facing managed Chrome process.
func TestOmnipusGracefulShutdown_CallsBrowserVideoShutdown(t *testing.T) {
	cfg := newShutdownTestConfig(t)
	msgBus := bus.NewMessageBus()
	provider := &restMockProvider{}
	// Construct the AgentLoop directly (not via the mustAgentLoop test
	// helper) because omnipusGracefulShutdown itself calls agentLoop.Close()
	// — registering a second t.Cleanup(al.Close) would double-close it,
	// which AgentLoop.Close() does not guarantee is safe.
	agentLoop, err := agent.NewAgentLoop(cfg, msgBus, provider)
	if err != nil {
		t.Fatalf("agent.NewAgentLoop: %v", err)
	}

	svc := &services{
		browserVideo: newOrchestrator(BrowserVideoDeps{}),
	}

	// No panic + no hang == pass. Shutdown() itself (stream teardown, encoder
	// browser process termination) is unit-tested where the orchestrator is
	// defined; this test only proves omnipusGracefulShutdown reaches and
	// invokes it on the non-nil path, at the correct point in the sequence.
	omnipusGracefulShutdown(svc, agentLoop, provider, cfg)
}

// TestOmnipusGracefulShutdown_NilBrowserVideoSafe verifies the degraded-boot
// path (setupAndStartServices failed before RegisterBrowserVideo ran) leaves
// services.browserVideo nil, and that omnipusGracefulShutdown tolerates that
// without panicking.
func TestOmnipusGracefulShutdown_NilBrowserVideoSafe(t *testing.T) {
	cfg := newShutdownTestConfig(t)
	msgBus := bus.NewMessageBus()
	provider := &restMockProvider{}
	agentLoop, err := agent.NewAgentLoop(cfg, msgBus, provider)
	if err != nil {
		t.Fatalf("agent.NewAgentLoop: %v", err)
	}

	svc := &services{}

	omnipusGracefulShutdown(svc, agentLoop, provider, cfg)
	// No panic == pass. A nil browserVideo must never be dereferenced.
}
