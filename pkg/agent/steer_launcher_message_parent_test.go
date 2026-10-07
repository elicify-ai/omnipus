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
