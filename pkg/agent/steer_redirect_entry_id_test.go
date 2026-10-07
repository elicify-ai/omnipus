// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// stoppedHelperForRedirectIDTest builds a helper session that has landed
// stopped (fence cleared), with a parked provider so the revive can dispatch.
func stoppedHelperForRedirectIDTest(t *testing.T) (*AgentLoop, string) {
	t.Helper()
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	steererID := newTestSteeringSession(t, al, adr093Workspace)
	adr093Persist(t, al, adr093Record(steererID, 1, session.LifecycleRunning))
	childID := newTestSteeringSession(t, al, adr093Workspace)
	parentID := steererID
	if err := al.GetSessionStore().SetMeta(childID, session.MetaPatch{ParentSessionID: &parentID}); err != nil {
		t.Fatalf("SetMeta(child).ParentSessionID: %v", err)
	}
	adr093Persist(t, al, &session.LifecycleRecord{
		SessionID:      childID,
		Generation:     1,
		State:          session.LifecycleStopped,
		OwnerScopeKind: session.OwnerScopeHuman,
		SteeredBy:      &session.SteeredBy{SteeringSessionID: steererID, RootSessionID: steererID},
		WorkspaceID:    adr093Workspace,
		AgentID:        testDefaultAgentID,
		Origin:         &session.Origin{Kind: session.OriginKindDelegate, CallID: "call-redirect-id"},
		StopNote:       &session.StopNote{At: time.Now(), By: "human:tester", Seq: 1, Cause: session.StopCauseTimeout},
	})
	installParkedProvider(t, al)
	return al, childID
}

func lastUserEntryID(t *testing.T, al *AgentLoop, sessionID, content string) string {
	t.Helper()
	entries, err := al.GetSessionStore().ReadTranscript(sessionID)
	if err != nil {
		t.Fatalf("ReadTranscript(%s): %v", sessionID, err)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Role == "user" && entries[i].Content == content {
			return entries[i].ID
		}
	}
	t.Fatalf("no user transcript entry with content %q in %s", content, sessionID)
	return ""
}

// The SPA drops the "(interrupted)" marker of a redirected turn only when the
// stored instruction id starts with "redirect-"; a helper /stop-redirect must
// store it that way.
func TestRedirectSessionTurn_StoppedHelperStoresRedirectPrefixedEntry(t *testing.T) {
	al, childID := stoppedHelperForRedirectIDTest(t)
	const instruction = "redirect: summarise instead"
	if err := al.RedirectSessionTurn(context.Background(), childID, instruction, "tester", "web"); err != nil {
		t.Fatalf("RedirectSessionTurn: %v", err)
	}
	id := lastUserEntryID(t, al, childID, instruction)
	if !strings.HasPrefix(id, "redirect-") {
		t.Fatalf("helper redirect instruction entry id = %q, want prefix %q", id, "redirect-")
	}
}

// Every other revival keeps the ordinary "<sid>-instruction-<uuid>" id, so a
// genuine Stop marker followed by a follow-up/answer is never mistaken for a
// redirect.
func TestReviveStoppedSession_FollowUpKeepsInstructionPrefixedEntry(t *testing.T) {
	al, childID := stoppedHelperForRedirectIDTest(t)
	const instruction = "follow-up: carry on"
	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "tester"}
	revived, err := al.ReviveStoppedSession(context.Background(), childID, by, instruction)
	if err != nil || !revived {
		t.Fatalf("ReviveStoppedSession = %v/%v, want revived", revived, err)
	}
	id := lastUserEntryID(t, al, childID, instruction)
	if want := childID + "-instruction-"; !strings.HasPrefix(id, want) {
		t.Fatalf("follow-up instruction entry id = %q, want prefix %q", id, want)
	}
}
