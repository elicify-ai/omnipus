// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Issue #1011 D1: a child's message_parent(kind="artifact") was always
// rejected with `outcome "checkpoint" does not match message kind
// "artifact"`, and message_parent(kind="handback", mode="pause") with
// `outcome "blocker" does not match message kind "handback"` — the tool maps
// those kinds onto OutcomeCheckpoint / OutcomeBlocker, and the real
// SteerUpwardDeliverer's outcome/kind check refused both pairings. The tool
// tests drive the REAL tool into the REAL deliverer (no fake deliverer), so
// the pairing the tool actually produces is what is checked.

package agent

import (
	"context"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// newArtifactTestTool wires the real message_parent tool to the real
// deliverer, and returns a context carrying the child's own session id.
func newArtifactTestTool(lifecycle *session.LifecycleStore, deliverer *SteerUpwardDeliverer, childID string) (*tools.MessageParentTool, context.Context) {
	tool := tools.NewMessageParentTool(deliverer, lifecycle)
	tool.SetSessionMessagingEnabled(func() bool { return true })
	ctx := tools.WithDelegateSessionID(tools.WithTranscriptSessionID(context.Background(), childID), childID)
	return tool, ctx
}

// artifactMessage builds a kind=artifact SessionMessage for direct Deliver calls.
func artifactMessage(t *testing.T, childID, messageID string) generated.SessionMessage {
	t.Helper()
	note := "draft report"
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageArtifact(generated.SessionMessageArtifact{
		MessageId: messageID, SessionId: childID, CreatedAt: time.Now(), Depth: 1,
		SenderIdentity: "worker", Paths: []string{"reports/summary.md"}, Note: &note,
	}); err != nil {
		t.Fatalf("FromSessionMessageArtifact: %v", err)
	}
	return sm
}

// pauseHandbackMessage builds a kind=handback, mode=pause SessionMessage for
// direct Deliver calls and boot-recovery fixtures.
func pauseHandbackMessage(t *testing.T, childID, messageID string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageHandback(generated.SessionMessageHandback{
		MessageId: messageID, SessionId: childID, CreatedAt: time.Now(), Depth: 1,
		SenderIdentity: "worker", Mode: generated.SessionMessageHandbackModePause,
		ResultSoFar: "half done, pausing", Artifacts: []string{}, OpenQuestions: []string{},
	}); err != nil {
		t.Fatalf("FromSessionMessageHandback: %v", err)
	}
	return sm
}

// requireEmptyInbox fails when any entry from childID reached parentID's inbox.
func requireEmptyInbox(t *testing.T, inbox *session.MessageInboxStore, parentID, childID string) {
	t.Helper()
	msgs, _, _, err := inbox.Drain(parentID, childID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("mismatched event reached the inbox: %d entries", len(msgs))
	}
}

// TestMessageParentArtifact_DeliversThroughRealDeliverer: an artifact report
// is accepted, stored once in the parent's inbox with kind "artifact", the
// child's paths and its note, and the parent's side panel gets a
// subagent_message of kind "artifact" whose text names the paths and note.
func TestMessageParentArtifact_DeliversThroughRealDeliverer(t *testing.T) {
	al, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)
	seedUnifiedSession(t, al, parentID)
	tool, ctx := newArtifactTestTool(lifecycle, deliverer, childID)

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
	if art.Note == nil || *art.Note != "draft report" {
		t.Fatalf("stored note = %v, want \"draft report\"", art.Note)
	}

	entries, rerr := al.GetSessionStore().ReadTranscript(parentID)
	if rerr != nil {
		t.Fatalf("ReadTranscript(parent): %v", rerr)
	}
	var frame *generated.SubagentMessageFrame
	for i := range entries {
		if entries[i].SystemSubtype == session.SystemSubtypeSubagentMessage && entries[i].SubagentMessage != nil {
			frame = entries[i].SubagentMessage
			break
		}
	}
	if frame == nil {
		t.Fatalf("expected a subagent_message entry in the parent's transcript, got %d entries", len(entries))
	}
	if frame.Kind != "artifact" {
		t.Fatalf("SubagentMessage.Kind = %q, want \"artifact\"", frame.Kind)
	}
	const wantText = "A delegated session shared artifacts: reports/summary.md, reports/data.csv (draft report)"
	if frame.Text == nil || *frame.Text != wantText {
		t.Fatalf("SubagentMessage.Text = %v, want %q", frame.Text, wantText)
	}
}

// TestDeliver_ArtifactStoredNotWoken: artifact, like checkpoint, is not
// wake-eligible (session.classifyEnvelope) — stored, parent not woken.
func TestDeliver_ArtifactStoredNotWoken(t *testing.T) {
	_, lifecycle, _, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)

	delivery, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{
		ChildSessionID: childID, Outcome: steer.OutcomeCheckpoint, Message: artifactMessage(t, childID, "a-1"),
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if delivery.Outcome != steer.DeliveryStoredNotWoken {
		t.Fatalf("Delivery.Outcome = %q, want stored_not_woken", delivery.Outcome)
	}
}

// TestMessageParentHandbackPause_DeliversThroughRealDeliverer (#1011 D1,
// review round 1): handback mode=pause is mapped onto OutcomeBlocker by
// outcomeForKind (wake-eligible, not terminal) and was refused by the real
// deliverer the same way artifact was. It must be accepted and stored once
// as a handback with mode "pause".
func TestMessageParentHandbackPause_DeliversThroughRealDeliverer(t *testing.T) {
	al, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)
	seedUnifiedSession(t, al, parentID)
	tool, ctx := newArtifactTestTool(lifecycle, deliverer, childID)

	res := tool.Execute(ctx, map[string]any{
		"kind":          "handback",
		"mode":          "pause",
		"result_so_far": "half done, pausing",
		"message_id":    "pause-1",
	})
	if res.IsError {
		t.Fatalf("message_parent(kind=handback, mode=pause) rejected: %s", res.ForLLM)
	}

	msgs, _, _, err := inbox.Drain(parentID, childID, "", 10)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("parent inbox entries = %d, want exactly 1", len(msgs))
	}
	hb, herr := msgs[0].AsSessionMessageHandback()
	if kind, _ := msgs[0].Discriminator(); kind != "handback" || herr != nil {
		t.Fatalf("stored kind = %q (err=%v), want \"handback\"", kind, herr)
	}
	if hb.Mode != generated.SessionMessageHandbackModePause {
		t.Fatalf("stored mode = %q, want \"pause\"", hb.Mode)
	}

	// The side panel labels it a handback (not "blocker", the outcome it
	// rides) and shows the child's own words.
	entries, rerr := al.GetSessionStore().ReadTranscript(parentID)
	if rerr != nil {
		t.Fatalf("ReadTranscript(parent): %v", rerr)
	}
	var frame *generated.SubagentMessageFrame
	for i := range entries {
		if entries[i].SystemSubtype == session.SystemSubtypeSubagentMessage && entries[i].SubagentMessage != nil {
			frame = entries[i].SubagentMessage
			break
		}
	}
	if frame == nil {
		t.Fatalf("expected a subagent_message entry in the parent's transcript, got %d entries", len(entries))
	}
	if frame.Kind != "handback" {
		t.Fatalf("SubagentMessage.Kind = %q, want \"handback\"", frame.Kind)
	}
	const wantText = "A delegated session handed back (pause): half done, pausing"
	if frame.Text == nil || *frame.Text != wantText {
		t.Fatalf("SubagentMessage.Text = %v, want %q", frame.Text, wantText)
	}
}

// TestDeliver_PauseHandbackWakesParent: a pause handback is wake-eligible
// (session.classifyEnvelope) — unlike an artifact, the parent is woken.
func TestDeliver_PauseHandbackWakesParent(t *testing.T) {
	_, lifecycle, _, deliverer := newDeliverTestLoop(t)
	const parentID, childID = "parent-1", "child-1"
	seedParentAndChild(t, lifecycle, parentID, childID)

	delivery, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{
		ChildSessionID: childID, Outcome: steer.OutcomeBlocker, Message: pauseHandbackMessage(t, childID, "pause-wake"),
	})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if delivery.Outcome != steer.DeliveryWoke {
		t.Fatalf("Delivery.Outcome = %q, want woke", delivery.Outcome)
	}
	if delivery.MessageID != "pause-wake" {
		t.Fatalf("Delivery.MessageID = %q, want the caller's id (a pause is not terminal: no :final id)", delivery.MessageID)
	}
}

// TestDeliver_WidenedOutcomesStillRejectOtherPairings guards the widened
// check from over-accepting: each pairing below is neither in the
// outcome's allowed kinds nor the mode it requires, so Deliver must refuse
// it and nothing may reach the inbox.
func TestDeliver_WidenedOutcomesStillRejectOtherPairings(t *testing.T) {
	cases := []struct {
		name    string
		outcome steer.Outcome
		msg     func(t *testing.T, childID string) generated.SessionMessage
	}{
		{"checkpoint carrying handback", steer.OutcomeCheckpoint, func(t *testing.T, c string) generated.SessionMessage {
			return handbackEvent(c, "wrong-kind").Message
		}},
		{"final_answer carrying artifact", steer.OutcomeFinalAnswer, func(t *testing.T, c string) generated.SessionMessage {
			return artifactMessage(t, c, "a-final")
		}},
		{"progress carrying artifact", steer.OutcomeProgress, func(t *testing.T, c string) generated.SessionMessage {
			return artifactMessage(t, c, "a-progress")
		}},
		{"final_answer carrying pause handback", steer.OutcomeFinalAnswer, func(t *testing.T, c string) generated.SessionMessage {
			return pauseHandbackMessage(t, c, "pause-under-final")
		}},
		{"blocker carrying final handback", steer.OutcomeBlocker, func(t *testing.T, c string) generated.SessionMessage {
			return handbackEvent(c, "final-under-blocker").Message
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, lifecycle, inbox, deliverer := newDeliverTestLoop(t)
			const parentID, childID = "parent-1", "child-1"
			seedParentAndChild(t, lifecycle, parentID, childID)

			_, err := deliverer.Deliver(context.Background(), steer.UpwardEvent{
				ChildSessionID: childID, Outcome: tc.outcome, Message: tc.msg(t, childID),
			})
			if err == nil {
				t.Fatalf("Deliver accepted %s", tc.name)
			}
			requireEmptyInbox(t, inbox, parentID, childID)
		})
	}
}
