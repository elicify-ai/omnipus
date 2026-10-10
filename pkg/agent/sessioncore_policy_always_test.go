// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Founder ruling 2026-10-10 (session-core spec FR-014/015/016): the delegation
// policy always applies. A launch carries an identified caller or is refused,
// and no operator setting exists that turns the gate off.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// TestLaunch_EmptyCaller_RefusedAndNoConfigValueTurnsGateOff drives the real
// Launch path with a steering session that has NO owning agent. Each config
// document is applied through the same JSON decode a config.json load uses, so
// the legacy key (now removed) is exercised exactly as an old install would
// carry it: it must have no effect.
func TestLaunch_EmptyCaller_RefusedAndNoConfigValueTurnsGateOff(t *testing.T) {
	origins := []struct {
		name   string
		origin steer.Origin
	}{
		{"delegate origin", steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-empty-caller"}},
		// A task-origin launch to another agent has a lenient missing-edge
		// fall-back, so only the caller-identity check refuses it: this arm
		// is what proves the identity check itself cannot be switched off.
		{"task origin", steer.Origin{Kind: steer.OriginKindTask, TaskID: "task-1", CallID: "call-empty-caller-task"}},
	}
	for _, oc := range origins {
		for _, tc := range []struct{ name, cfgJSON string }{
			{"no config", `{}`},
			{"legacy key true", `{"tools":{"delegate":{"require_parent_agent_id":true}}}`},
			{"legacy key false", `{"tools":{"delegate":{"require_parent_agent_id":false}}}`},
		} {
			t.Run(oc.name+"/"+tc.name, func(t *testing.T) {
				al, cleanup := newSteerAL(t)
				defer cleanup()
				if err := json.Unmarshal([]byte(tc.cfgJSON), al.GetConfig()); err != nil {
					t.Fatalf("apply config: %v", err)
				}
				steerer, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "")
				if err != nil {
					t.Fatalf("NewSession(steerer): %v", err)
				}
				if meta, mErr := al.GetSessionStore().GetMeta(steerer.ID); mErr != nil || meta.AgentID != "" {
					t.Fatalf("fixture must be an identity-less steerer, got agent=%q err=%v", meta.AgentID, mErr)
				}

				result, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
					SteeringSessionID: steerer.ID,
					TargetAgentID:     testDefaultAgentID,
					Task:              "do something",
					Origin:            oc.origin,
				})
				if !errors.Is(err, steer.ErrInvalidEdge) || result.SessionID != "" {
					t.Fatalf("Launch() = %+v, %v; want no child and ErrInvalidEdge", result, err)
				}
				if n := childrenOf(t, al, steerer.ID); n != 0 {
					t.Fatalf("refused launch persisted %d child record(s), want 0", n)
				}
			})
		}
	}
}

// TestStartingRemainingDepth_EmptyCaller_StillGraphGated pins that the graph
// gate no longer depends on caller identification: an identity-less steering
// record with an unreadable delegation graph is refused (it used to be
// exempted and granted the global budget).
func TestStartingRemainingDepth_EmptyCaller_StillGraphGated(t *testing.T) {
	home := seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge(u5aCallerAgentID, testDefaultAgentID, nil, nil),
	})
	corruptDelegationStore(t, home, testWS)

	al, cleanup := newSteerAL(t)
	defer cleanup()
	al.GetConfig().Performance.MaxDelegationDepth = 10

	got, err := NewSteerLauncher(al).startingRemainingDepth(ctxWS(testWS, 0),
		&session.LifecycleRecord{}, testDefaultAgentID, 0, steer.OriginKindDelegate)
	if !errors.Is(err, steer.ErrInvalidEdge) {
		t.Fatalf("identity-less caller, unreadable graph: got (%d, %v), want ErrInvalidEdge", got, err)
	}
}
