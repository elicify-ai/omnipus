package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestReconstructSteeredTurn_UsesChildTranscriptAndAddress(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	launcher := NewSteerLauncher(al)
	parentID := newTestSteeringSession(t, al, "ws-1")

	launched, err := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID,
		TargetAgentID:     testDefaultAgentID,
		Task:              "inspect the child transcript",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-reconstruct"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	rec, err := al.GetSessionLifecycleStore().Load(launched.SessionID)
	if err != nil {
		t.Fatalf("Load child lifecycle: %v", err)
	}
	// Make the upward address visibly different from the child's own meta.
	rec.SteeredBy.ReportingTarget.Channel = "telegram"
	rec.SteeredBy.ReportingTarget.ChatID = "parent-chat"

	ts, err := al.reconstructSteeredTurn(rec, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}
	if ts.transcriptSessionID != launched.SessionID {
		t.Fatalf("transcript session = %q, want child %q", ts.transcriptSessionID, launched.SessionID)
	}
	if ts.transcriptStore != al.GetSessionStore() {
		t.Fatal("transcript store is not the shared unified session store")
	}
	if ts.channel == "telegram" || ts.chatID == "parent-chat" {
		t.Fatalf("child execution address reused upward reporting target: channel=%q chat=%q", ts.channel, ts.chatID)
	}
}
