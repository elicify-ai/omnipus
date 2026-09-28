// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Issue #1011 D1: a child's message_parent(kind="artifact") was always
// rejected with `outcome "checkpoint" does not match message kind
// "artifact"` — the tool maps artifact onto OutcomeCheckpoint, and the real
// SteerUpwardDeliverer's outcome/kind check refused that pairing. These
// tests drive the REAL tool into the REAL deliverer (no fake deliverer), so
// the pairing the tool actually produces is what is checked.

package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestMessageParentArtifact_DeliversThroughRealDeliverer: an artifact report
// is accepted, stored once in the parent's inbox with kind "artifact" and the
// child's paths, and does not wake the parent (artifact, like checkpoint, is
// not wake-eligible — session.classifyEnvelope).
func TestMessageParentArtifact_DeliversThroughRealDeliverer(t *testing.T) {
	_, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)

	tool := tools.NewMessageParentTool(deliverer, lifecycle)
	tool.SetSessionMessagingEnabled(func() bool { return true })
	ctx := tools.WithDelegateSessionID(tools.WithTranscriptSessionID(context.Background(), childID), childID)

	res := tool.Execute(ctx, map[string]any{
		"kind":       "artifact",
		"paths":      []any{"reports/summary.md", "reports/data.csv"},
		"note":       "draft report",
		"message_id": "artifact-1",
	})
	if res.IsError {
		t.Fatalf("message_parent(kind=artifact) rejected: %s", res.ForLLM)
	}

	msgs, _, _, err := inbox.Drain(parentID, childID, "", 10)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("parent inbox entries = %d, want exactly 1", len(msgs))
	}
	kind, derr := msgs[0].Discriminator()
	if derr != nil || kind != "artifact" {
		t.Fatalf("stored kind = %q (err=%v), want \"artifact\"", kind, derr)
	}
	art, aerr := msgs[0].AsSessionMessageArtifact()
	if aerr != nil {
		t.Fatalf("AsSessionMessageArtifact: %v", aerr)
	}
	want := []string{"reports/summary.md", "reports/data.csv"}
	if len(art.Paths) != len(want) || art.Paths[0] != want[0] || art.Paths[1] != want[1] {
		t.Fatalf("stored paths = %v, want %v", art.Paths, want)
	}
}

// TestDeliver_CheckpointOutcomeStillRejectsOtherKinds guards the widened
// check from over-accepting: OutcomeCheckpoint carrying a handback envelope
// (neither checkpoint nor artifact) must still be refused and never stored.
func TestDeliver_CheckpointOutcomeStillRejectsOtherKinds(t *testing.T) {
	_, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)

	_, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{
		ChildSessionID: childID,
		Outcome:        steer.OutcomeCheckpoint,
		Message:        handbackEvent(childID, "wrong-kind").Message,
	})
	if err == nil {
		t.Fatal("Deliver accepted an OutcomeCheckpoint carrying a handback envelope")
	}
	msgs, _, _, drainErr := inbox.Drain(parentID, childID, "", 10)
	if drainErr != nil {
		t.Fatal(drainErr)
	}
	if len(msgs) != 0 {
		t.Fatalf("mismatched event reached the inbox: %d entries", len(msgs))
	}
}
