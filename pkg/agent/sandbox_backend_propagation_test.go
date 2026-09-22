// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// These tests pin the two halves of AgentLoop.SetSandboxBackend
// (pkg/agent/loop_accessors.go): the accessor returns the new backend, and
// wireEnvProviders re-runs so every registered agent's ContextBuilder holds a
// fresh envcontext.DefaultProvider built from it. A DefaultProvider is built
// once per wireEnvProviders call, so the re-wire is what keeps the rendered
// system preamble in sync with a boot-time degrade.

package agent

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// stubKernelBackend simulates a Landlock-capable backend (LinuxBackend) for
// tests that need a "kernel-capable, ABI reported" starting state without a
// real Linux kernel: it implements sandbox.SandboxBackend plus the narrow
// abiReporter/policyApplyReporter interfaces sandbox.DescribeBackend type-
// asserts against.
type stubKernelBackend struct {
	name string
	abi  int
}

func (s *stubKernelBackend) Name() string    { return s.name }
func (s *stubKernelBackend) Available() bool { return true }
func (s *stubKernelBackend) Apply(sandbox.SandboxPolicy) error {
	return nil
}
func (s *stubKernelBackend) ApplyToCmd(*exec.Cmd, sandbox.SandboxPolicy) error {
	return nil
}
func (s *stubKernelBackend) ABIVersion() int     { return s.abi }
func (s *stubKernelBackend) PolicyApplied() bool { return true }

// TestSetSandboxBackend_UpdatesAccessor proves the accessor half of the
// contract: SandboxBackend() must return whatever was last passed to
// SetSandboxBackend, not the construction-time selection. This is what
// pkg/gateway/rest_security_wave5.go::HandleSandboxStatus reads.
func TestSetSandboxBackend_UpdatesAccessor(t *testing.T) {
	cfg := minimalTestConfig(t)
	cfg.Agents.Defaults.DefaultModel.Model = "test-model"
	cfg.Agents.Defaults.MaxTokens = 4096
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })

	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	t.Cleanup(func() { al.Close() })

	// Simulate the boot-time selection (whatever this test host resolved to)
	// being kernel-capable, then simulate the degrade the gateway's
	// degradeAfterLandlockFailure performs.
	kernelCapable := &stubKernelBackend{name: "landlock-v1", abi: 1}
	al.SetSandboxBackend(kernelCapable)
	if al.SandboxBackend() != sandbox.SandboxBackend(kernelCapable) {
		t.Fatalf("SandboxBackend() did not return the kernel-capable stub right after SetSandboxBackend")
	}

	fallback := sandbox.NewFallbackBackend()
	al.SetSandboxBackend(fallback)
	if al.SandboxBackend() != sandbox.SandboxBackend(fallback) {
		t.Errorf("SandboxBackend() must return the degraded FallbackBackend instance after SetSandboxBackend, "+
			"got %T (name=%s) — a caller reading this after a degrade would still see the stale kernel-capable backend",
			al.SandboxBackend(), al.SandboxBackend().Name())
	}
}

// TestSetSandboxBackend_RewiresAgentPreamble proves the preamble half of the
// contract: envcontext.DefaultProvider instances are constructed once, at
// wireEnvProviders time, and hold a copy of the backend value rather than a
// live pointer to AgentLoop. SetSandboxBackend re-runs wireEnvProviders so
// every registered agent's ContextBuilder gets a fresh DefaultProvider built
// from the new backend.
func TestSetSandboxBackend_RewiresAgentPreamble(t *testing.T) {
	cfg := minimalTestConfig(t)
	cfg.Agents.Defaults.DefaultModel.Model = "test-model"
	cfg.Agents.Defaults.MaxTokens = 4096
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })

	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	t.Cleanup(func() { al.Close() })

	registry := al.GetRegistry()
	agentIDs := registry.ListAgentIDs()
	if len(agentIDs) == 0 {
		t.Fatal("test setup: no agents registered")
	}
	inst, ok := registry.GetAgent(agentIDs[0])
	if !ok || inst == nil || inst.ContextBuilder == nil {
		t.Fatalf("test setup: agent %q has no ContextBuilder", agentIDs[0])
	}

	// Baseline: the provider NewAgentLoop wired at construction time, from
	// whatever real backend this test host's SelectBackend() resolved to.
	// Captured before touching SetSandboxBackend at all so the two identity
	// checks below cannot be satisfied by coincidence.
	providerAtConstruction := inst.ContextBuilder.EnvironmentProvider()
	if providerAtConstruction == nil {
		t.Fatal("test setup: EnvironmentProvider() is nil right after NewAgentLoop")
	}

	// Step 1: install a kernel-capable stub as if boot had selected a real
	// Landlock backend. wireEnvProviders constructs a FRESH
	// envcontext.DefaultProvider on every call (loop_env.go), so a correct
	// SetSandboxBackend must swap in a NEW provider instance — identity
	// (pointer) comparison, not string content, so this assertion cannot
	// pass by coincidence regardless of what backend this test host's
	// SelectBackend() happened to resolve to at construction time.
	kernelCapable := &stubKernelBackend{name: "landlock-v1", abi: 1}
	al.SetSandboxBackend(kernelCapable)

	providerAfterStep1 := inst.ContextBuilder.EnvironmentProvider()
	if providerAfterStep1 == nil {
		t.Fatal("EnvironmentProvider() is nil after SetSandboxBackend — wireEnvProviders did not run")
	}
	if providerAfterStep1 == providerAtConstruction {
		t.Fatal("EnvironmentProvider() identity did not change after SetSandboxBackend(kernelCapable) — " +
			"wireEnvProviders was not re-run, so the ContextBuilder still holds the construction-time provider")
	}
	// Content check (secondary — KernelLevel=true must suppress the
	// fallback-degradation warning envcontext.DefaultProvider.ActiveWarnings
	// emits).
	for _, w := range providerAfterStep1.ActiveWarnings() {
		if strings.Contains(w, "fallback") {
			t.Errorf("preamble claims fallback mode right after wiring a kernel-capable, policy-applied stub backend (warnings=%v)",
				providerAfterStep1.ActiveWarnings())
			break
		}
	}

	// Step 2: simulate the gateway's degradeAfterLandlockFailure swap.
	fallback := sandbox.NewFallbackBackend()
	al.SetSandboxBackend(fallback)

	providerAfterStep2 := inst.ContextBuilder.EnvironmentProvider()
	if providerAfterStep2 == nil {
		t.Fatal("EnvironmentProvider() is nil after the degrade — wireEnvProviders did not re-run")
	}
	if providerAfterStep2 == providerAfterStep1 {
		t.Fatal("EnvironmentProvider() identity did not change after SetSandboxBackend(fallback) — " +
			"the degrade did not re-wire the preamble; the agent would keep reading the stale kernel-capable provider")
	}
	mode, err := providerAfterStep2.SandboxMode()
	if err != nil {
		t.Fatalf("SandboxMode() returned an error: %v", err)
	}
	if mode != "fallback" {
		t.Errorf("SandboxMode() = %q after degrading to FallbackBackend, want %q — "+
			"the agent's own system preamble still claims kernel-level enforcement after a degrade",
			mode, "fallback")
	}
}
