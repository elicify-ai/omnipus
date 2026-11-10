// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// setMessageParentPolicy replaces the target agent's policy snapshot with an
// explicit global ceiling and per-agent override for message_parent.
func setMessageParentPolicy(t *testing.T, al *AgentLoop, agentID string, global, agent config.ToolPolicy) {
	t.Helper()
	inst, ok := al.GetRegistry().GetAgent(agentID)
	if !ok {
		t.Fatalf("agent %q not registered", agentID)
	}
	pol := &tools.ToolPolicyCfg{GlobalPolicies: map[string]config.ToolPolicy{"message_parent": global}}
	if agent != "" {
		pol.Policies = map[string]config.ToolPolicy{"message_parent": agent}
	}
	inst.StoreToolPolicy(pol)
}

func launchAs(t *testing.T, al *AgentLoop, kind steer.OriginKind) (steer.LaunchResult, error) {
	t.Helper()
	req := steer.LaunchRequest{TargetAgentID: testDefaultAgentID, Task: "do the work"}
	req.Origin = steer.Origin{Kind: kind}
	if kind == steer.OriginKindTask {
		req.Origin.TaskID = "task-948"
	}
	return NewSteerLauncher(al).Launch(context.Background(), req)
}

// #948: a delegate target whose effective message_parent is deny is refused at
// launch, visibly, and no session record is written.
func TestLaunch_DelegateTargetMessageParentDenied_RefusesAndPersistsNoSession(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	setMessageParentPolicy(t, al, testDefaultAgentID, config.ToolPolicyAllow, config.ToolPolicyDeny)

	before, err := al.GetSessionLifecycleStore().List(session.LifecycleFilter{})
	if err != nil {
		t.Fatalf("List(before): %v", err)
	}
	res, err := launchAs(t, al, steer.OriginKindDelegate)
	if !errors.Is(err, tools.ErrDelegateTargetCannotReport) {
		t.Fatalf("Launch error = %v, want errors.Is(..., tools.ErrDelegateTargetCannotReport)", err)
	}
	if res != (steer.LaunchResult{}) {
		t.Fatalf("Launch result = %+v, want the zero value", res)
	}
	after, err := al.GetSessionLifecycleStore().List(session.LifecycleFilter{})
	if err != nil {
		t.Fatalf("List(after): %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("lifecycle records %d -> %d: a refused launch must persist nothing", len(before), len(after))
	}
}

// The ceiling alone (global deny, no per-agent entry) refuses too: strictest
// wins is the same resolution the runtime filter uses.
func TestLaunch_DelegateTargetGlobalMessageParentDenied_Refuses(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	setMessageParentPolicy(t, al, testDefaultAgentID, config.ToolPolicyDeny, "")
	if _, err := launchAs(t, al, steer.OriginKindDelegate); !errors.Is(err, tools.ErrDelegateTargetCannotReport) {
		t.Fatalf("Launch error = %v, want ErrDelegateTargetCannotReport", err)
	}
}

// Controls: allow and ask both leave the worker able to report; a non-delegate
// origin is not subject to this check.
func TestLaunch_MessageParentCheckControls(t *testing.T) {
	for name, tc := range map[string]struct {
		global, agent config.ToolPolicy
		kind          steer.OriginKind
	}{
		"allow, delegate":            {config.ToolPolicyAllow, "", steer.OriginKindDelegate},
		"ask, delegate":              {config.ToolPolicyAllow, config.ToolPolicyAsk, steer.OriginKindDelegate},
		"deny but task origin":       {config.ToolPolicyAllow, config.ToolPolicyDeny, steer.OriginKindTask},
		"deny but human chat origin": {config.ToolPolicyDeny, "", steer.OriginKindHuman},
	} {
		t.Run(name, func(t *testing.T) {
			al, cleanup := newSteerAL(t)
			defer cleanup()
			setMessageParentPolicy(t, al, testDefaultAgentID, tc.global, tc.agent)
			if _, err := launchAs(t, al, tc.kind); err != nil {
				t.Fatalf("Launch must proceed, got %v", err)
			}
		})
	}
}

func storePolicy(t *testing.T, al *AgentLoop, pol *tools.ToolPolicyCfg) {
	t.Helper()
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatalf("agent %q not registered", testDefaultAgentID)
	}
	inst.StoreToolPolicy(pol)
}

// #948 review F1: a WILDCARD deny ("message_*") resolves to deny for
// message_parent through the shared resolver, so it must refuse exactly like
// an exact entry — on either layer.
func TestLaunch_DelegateTargetWildcardMessageParentDenied_Refuses(t *testing.T) {
	for name, pol := range map[string]*tools.ToolPolicyCfg{
		"global wildcard deny": {GlobalPolicies: map[string]config.ToolPolicy{"message_*": config.ToolPolicyDeny}},
		"agent wildcard deny": {
			GlobalPolicies: map[string]config.ToolPolicy{"message_parent": config.ToolPolicyAllow},
			Policies:       map[string]config.ToolPolicy{"message_*": config.ToolPolicyDeny},
		},
	} {
		t.Run(name, func(t *testing.T) {
			al, cleanup := newSteerAL(t)
			defer cleanup()
			storePolicy(t, al, pol)
			if _, err := launchAs(t, al, steer.OriginKindDelegate); !errors.Is(err, tools.ErrDelegateTargetCannotReport) {
				t.Fatalf("Launch error = %v, want ErrDelegateTargetCannotReport", err)
			}
		})
	}
}

// Missing data never refuses. ResolveEffectivePolicy itself fails closed to
// "deny" (and logs an Error) when neither layer covers a tool; this pre-flight
// refuses ONLY on a resolved verdict, leaving uncovered tools to the runtime
// filter. Pinned: nil snapshot, empty snapshot, unrelated entries, and a bare
// "*" (which the resolver does not treat as a wildcard) all launch.
func TestLaunch_DelegateTargetNoMessageParentCoverage_Launches(t *testing.T) {
	for name, pol := range map[string]*tools.ToolPolicyCfg{
		"nil snapshot":     nil,
		"empty snapshot":   {},
		"unrelated entry":  {GlobalPolicies: map[string]config.ToolPolicy{"read_file": config.ToolPolicyDeny}},
		"bare star denies": {GlobalPolicies: map[string]config.ToolPolicy{"*": config.ToolPolicyDeny}},
	} {
		t.Run(name, func(t *testing.T) {
			al, cleanup := newSteerAL(t)
			defer cleanup()
			storePolicy(t, al, pol)
			if _, err := launchAs(t, al, steer.OriginKindDelegate); err != nil {
				t.Fatalf("Launch must proceed without message_parent coverage, got %v", err)
			}
		})
	}
}

// External-CLI targets deliver through the drained CLI stream, not the
// message_parent tool: the exemption is pinned even with message_parent denied.
func TestLaunch_ExternalCLITargetMessageParentDenied_StillLaunches(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	setMessageParentPolicy(t, al, testDefaultAgentID, config.ToolPolicyDeny, config.ToolPolicyDeny)
	inst, _ := al.GetRegistry().GetAgent(testDefaultAgentID)
	inst.Subagents = &config.SubagentsConfig{
		Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "codex"},
	}
	if !al.GetRegistry().IsExternalCLI(testDefaultAgentID) {
		t.Fatal("setup: target must classify as external-CLI")
	}
	if _, err := launchAs(t, al, steer.OriginKindDelegate); err != nil {
		t.Fatalf("external-CLI target must be exempt, got %v", err)
	}
}
