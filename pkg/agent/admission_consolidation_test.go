// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// This file covers the concurrency-gate consolidation (2026-08-04): making
// config.PerformanceConfig.EffectiveMaxParallelAgents() the single authority
// for agent concurrency. Unlike admission_test.go (which unit-tests
// AdmissionController in isolation), these tests exercise the REAL
// production wiring (NewAgentLoop -> al.admission), proving the session
// admission gate documented as "gate #1" in the consolidation report can no
// longer impose an independent, smaller cap than the central
// Performance.max_parallel_agents value — the exact defect measured live in
// docs/internal/uat/parallelism-cost-browser-bash-2026-08-04.md finding #1
// (a hardcoded runtime.NumCPU()*4 silently rejected sessions at 8 while
// max_parallel_agents advertised 16).
package agent

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestNewAgentLoop_AdmissionCapMatchesPerformanceConfig verifies that a
// freshly constructed AgentLoop's session-admission gate reports EXACTLY the
// central Performance.EffectiveMaxParallelAgents() value — not a
// hardware-derived guess (the retired runtime.NumCPU()*4 default).
func TestNewAgentLoop_AdmissionCapMatchesPerformanceConfig(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
		Performance: config.PerformanceConfig{MaxParallelAgents: 3},
	}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	defer al.Close()

	want, _ := cfg.Performance.EffectiveMaxParallelAgents()
	if want != 3 {
		t.Fatalf("test setup invariant broken: EffectiveMaxParallelAgents() = %d, want 3", want)
	}
	if got := al.admission.SoftCap(); got != want {
		t.Fatalf("al.admission.SoftCap() = %d, want %d (the central Performance.EffectiveMaxParallelAgents() value, not an independent default)", got, want)
	}
}

// TestNewAgentLoop_AdmissionCapTracksConfigChangeLive is the "re-read later"
// regression test: after construction, changing Performance.MaxParallelAgents
// via the SAME live-config-swap mechanism the gateway's config-write path
// uses (AgentLoop.SwapConfig) must be reflected by al.admission.SoftCap()
// immediately — no reconstruction, no explicit resize call. This is what
// makes the auto-detected default's boot-time-available-memory caveat
// self-correcting rather than a permanent misconfiguration until restart.
func TestNewAgentLoop_AdmissionCapTracksConfigChangeLive(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
		Performance: config.PerformanceConfig{MaxParallelAgents: 3},
	}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	defer al.Close()

	if got := al.admission.SoftCap(); got != 3 {
		t.Fatalf("al.admission.SoftCap() before config change = %d, want 3", got)
	}

	newCfg, err := cfg.Clone()
	if err != nil {
		t.Fatalf("cfg.Clone(): %v", err)
	}
	newCfg.Performance.MaxParallelAgents = 9
	al.SwapConfig(newCfg)

	if got := al.admission.SoftCap(); got != 9 {
		t.Fatalf("al.admission.SoftCap() after SwapConfig = %d, want 9 (must track the central config live, no restart/resize needed)", got)
	}
}

// TestOnlyOneGate_SessionConcurrencyBoundByCentralValueAlone is the required
// "only one gate enforces" regression test: with the REAL production
// AdmissionController (no manual al.admission override, unlike
// TestSessionWorker_AdmissionRejection which deliberately injects a fixed
// cap for a narrow unit test), session concurrency must be bound by
// Performance.max_parallel_agents ALONE — not silently by the retired
// runtime.NumCPU()*4 soft cap (which would have rejected around
// runtime.NumCPU()*4 sessions regardless of what max_parallel_agents said).
//
// BDD:
//
//	Given max_parallel_agents=2 and no manual admission override,
//	When 3 concurrent chat sessions are dispatched,
//	Then exactly 2 are admitted (matching the CONFIGURED value) and the 3rd
//	     receives a real, visible capacity-rejection reply — proving both
//	     that the central value is what bound the concurrency, and that a
//	     refusal is never silent (the "removing gate #1 must not reintroduce
//	     a silent rejection" requirement).
func TestOnlyOneGate_SessionConcurrencyBoundByCentralValueAlone(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
		Performance: config.PerformanceConfig{MaxParallelAgents: 2},
	}
	msgBus := bus.NewMessageBus()

	release := make(chan struct{})
	blockingProvider := &blockingMockProvider{releaseAll: release}
	al := mustNewAgentLoop(t, cfg, msgBus, blockingProvider)
	defer al.Close()

	// Sanity: the production wiring must have picked up the configured
	// value, not runtime.NumCPU()*4 (which could coincidentally also be a
	// small number on a constrained CI box, so this assertion is what makes
	// the rest of the test meaningful rather than a false negative).
	if got := al.admission.SoftCap(); got != 2 {
		t.Fatalf("al.admission.SoftCap() = %d, want 2 (from Performance.MaxParallelAgents, the ONLY configured cap)", got)
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	runErrCh := make(chan error, 1)
	go func() { runErrCh <- al.Run(runCtx) }()
	t.Cleanup(func() {
		close(release)
		cancelRun()
		select {
		case <-runErrCh:
		case <-time.After(3 * time.Second):
			t.Error("Run() did not stop within 3 s")
		}
	})

	pubCtx, pubCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pubCancel()

	for i := 1; i <= 2; i++ {
		sid := fmt.Sprintf("central-sess-%d", i)
		msg := makeSessionMsg(sid, fmt.Sprintf("hold slot %d", i))
		if err := msgBus.PublishInbound(pubCtx, msg); err != nil {
			t.Fatalf("PublishInbound[%d]: %v", i, err)
		}
	}
	time.Sleep(150 * time.Millisecond)

	msg3 := makeSessionMsg("central-sess-3", "over the central cap")
	if err := msgBus.PublishInbound(pubCtx, msg3); err != nil {
		t.Fatalf("PublishInbound[3]: %v", err)
	}

	rejectCtx, rejectCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer rejectCancel()

	const wantRejection = "I'm at capacity right now — please try again in a few seconds."
	found := false
	for !found {
		select {
		case out := <-msgBus.OutboundChan():
			if out.Content == wantRejection {
				found = true
			}
		case <-rejectCtx.Done():
			t.Fatal("timeout waiting for a VISIBLE capacity-rejection reply — a refusal must never be silent")
		}
	}
}

// TestRemovedGate1_HardcodedNumCPUCapNoLongerBinds is the required
// "every removed gate: a test that its cap no longer binds" regression test
// for gate #1. Before the consolidation, newAdmissionController(0) defaulted
// to runtime.NumCPU()*4 regardless of any configured
// performance.max_parallel_agents — on a small box (e.g. 2 cores => cap 8)
// this silently rejected sessions far below an operator's advertised, much
// larger max_parallel_agents (measured live at 8-vs-16 in
// docs/internal/uat/parallelism-cost-browser-bash-2026-08-04.md finding #1).
//
// This test sets max_parallel_agents comfortably above whatever
// runtime.NumCPU()*4 evaluates to on the machine running this test, and
// proves every one of those sessions is admitted — the retired hardcoded
// ceiling no longer binds at all.
func TestRemovedGate1_HardcodedNumCPUCapNoLongerBinds(t *testing.T) {
	oldHardcodedCap := runtime.NumCPU() * 4
	configuredCap := oldHardcodedCap + 25 // comfortably above the retired ceiling

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
		Performance: config.PerformanceConfig{MaxParallelAgents: configuredCap},
	}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	defer al.Close()

	if got := al.admission.SoftCap(); got != configuredCap {
		t.Fatalf("al.admission.SoftCap() = %d, want %d (configured value, NOT the retired runtime.NumCPU()*4=%d ceiling)",
			got, configuredCap, oldHardcodedCap)
	}

	// Admit strictly MORE scopes than the old hardcoded runtime.NumCPU()*4
	// ceiling would ever have allowed — every one must succeed.
	var releases []func()
	for i := 0; i < oldHardcodedCap+10; i++ {
		scope := fmt.Sprintf("no-longer-capped-scope-%d", i)
		ok, release := al.admission.TryAdmit(scope)
		if !ok {
			t.Fatalf("TryAdmit refused scope #%d (only %d admitted so far) — the retired runtime.NumCPU()*4=%d cap appears to still be binding independently",
				i, i, oldHardcodedCap)
		}
		releases = append(releases, release)
	}
	for _, release := range releases {
		release()
	}
}
