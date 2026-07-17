//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// fakeDisplaySidecar is a minimal browser.DisplaySidecar test double that
// records whether Stop was called, so ADR-044 T6 shutdown wiring can be
// verified without a real Xvfb process.
type fakeDisplaySidecar struct {
	mu      sync.Mutex
	stopped bool
}

func (f *fakeDisplaySidecar) Start(_ context.Context) error { return nil }
func (f *fakeDisplaySidecar) Display() string               { return ":99" }
func (f *fakeDisplaySidecar) Healthy() bool                 { return true }
func (f *fakeDisplaySidecar) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = true
}

func (f *fakeDisplaySidecar) wasStopped() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopped
}

// fakeAudioSidecar is the AudioSidecar counterpart of fakeDisplaySidecar.
type fakeAudioSidecar struct {
	mu      sync.Mutex
	stopped bool
}

func (f *fakeAudioSidecar) Start(_ context.Context) error { return nil }
func (f *fakeAudioSidecar) PulseServer() string           { return "unix:/tmp/fake-pulse.sock" }
func (f *fakeAudioSidecar) Healthy() bool                 { return true }
func (f *fakeAudioSidecar) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = true
}

func (f *fakeAudioSidecar) wasStopped() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopped
}

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

// TestOmnipusGracefulShutdown_StopsSidecarsAfterAgentLoopClose verifies the
// ADR-044 T6 wiring: when services carries non-nil display/audio sidecars
// (the live-video-capable boot path), omnipusGracefulShutdown stops both
// during its step-4 background-services teardown, which runs strictly after
// step 2's agentLoop.Close() has already reaped any managed Chrome process.
func TestOmnipusGracefulShutdown_StopsSidecarsAfterAgentLoopClose(t *testing.T) {
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

	display := &fakeDisplaySidecar{}
	audio := &fakeAudioSidecar{}
	svc := &services{
		displaySidecar: display,
		audioSidecar:   audio,
	}

	omnipusGracefulShutdown(svc, agentLoop, provider, cfg)

	if !display.wasStopped() {
		t.Error("expected displaySidecar.Stop() to be called during graceful shutdown")
	}
	if !audio.wasStopped() {
		t.Error("expected audioSidecar.Stop() to be called during graceful shutdown")
	}
}

// TestOmnipusGracefulShutdown_NilSidecarsSafe verifies the dormant/degraded
// boot path (non-Linux, no Xvfb on PATH, or the FR-020 kill-switch off)
// leaves services.displaySidecar/audioSidecar nil, and that
// omnipusGracefulShutdown tolerates that without panicking.
func TestOmnipusGracefulShutdown_NilSidecarsSafe(t *testing.T) {
	cfg := newShutdownTestConfig(t)
	msgBus := bus.NewMessageBus()
	provider := &restMockProvider{}
	agentLoop, err := agent.NewAgentLoop(cfg, msgBus, provider)
	if err != nil {
		t.Fatalf("agent.NewAgentLoop: %v", err)
	}

	svc := &services{}

	omnipusGracefulShutdown(svc, agentLoop, provider, cfg)
	// No panic == pass. Nil sidecars must never be dereferenced.
}
