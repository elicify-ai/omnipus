// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// TestSubagentSpanID_GenerationOneStaysBare pins the bare id. Suffixing
// generation 1 would orphan every span already persisted as span_<callID>.
func TestSubagentSpanID_GenerationOneStaysBare(t *testing.T) {
	for _, gen := range []int{0, 1} {
		if got := SubagentSpanID("call-1", gen); got != "span_call-1" {
			t.Errorf("SubagentSpanID(call-1, %d) = %q, want span_call-1", gen, got)
		}
	}
	if got := SubagentSpanID("call-1", 2); got != "span_call-1_g2" {
		t.Errorf("SubagentSpanID(call-1, 2) = %q, want span_call-1_g2", got)
	}
	if got := SubagentSpanID("call-1", 10); got != "span_call-1_g10" {
		t.Errorf("SubagentSpanID(call-1, 10) = %q, want span_call-1_g10", got)
	}
}

// TestFollowUpGeneration_OwnSpanDoesNotOverwriteOriginal is D9: a second
// generation must get its own subagent_start and subagent_end, keyed
// span_<callID>_g2, and generation 1's terminal end must still be in the
// parent transcript under the bare span id.
func TestFollowUpGeneration_OwnSpanDoesNotOverwriteOriginal(t *testing.T) {
	al, lifecycle, _, _ := newDeliverTestLoop(t)
	const parentID, childID, callID = "parent-fu", "child-fu", "call-1"
	seedParentAndChild(t, lifecycle, parentID, childID)
	seedUnifiedSession(t, al, parentID)

	rec, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("load child: %v", err)
	}
	rec.Title = "write the report"
	al.deliverSubagentStart(parentID, rec, "write the report")
	al.deliverSubagentEnd(parentID, rec, steer.OutcomeFinalAnswer)

	rec.Generation = 2
	rec.State = session.LifecycleQueued
	rec.ResumedFrom = childID
	if err := lifecycle.Persist(rec); err != nil {
		t.Fatalf("persist generation 2: %v", err)
	}
	rec2, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("reload generation 2: %v", err)
	}
	al.deliverSubagentState(parentID, rec2, string(session.LifecycleRunning), nil)
	al.deliverSubagentEnd(parentID, rec2, steer.OutcomeInterrupted)

	entries, err := al.GetSessionStore().ReadTranscript(parentID)
	if err != nil {
		t.Fatalf("read parent transcript: %v", err)
	}

	var gen1Ends, gen2Starts, gen2Ends int
	for i := range entries {
		e := &entries[i]
		if e.SubagentStart != nil && e.SubagentStart.SpanId == "span_call-1_g2" {
			gen2Starts++
			if e.ID != callID+":g2:start" {
				t.Errorf("generation-2 start entry id = %q, want %q", e.ID, callID+":g2:start")
			}
			if e.SubagentStart.ParentCallId != callID {
				t.Errorf("generation-2 start parent_call_id = %q, want the original run call id %q", e.SubagentStart.ParentCallId, callID)
			}
		}
		if e.SubagentEnd == nil {
			continue
		}
		switch e.SubagentEnd.SpanId {
		case "span_call-1":
			gen1Ends++
			if e.SubagentEnd.Status != "success" {
				t.Errorf("generation-1 end status = %q, want success (the original terminal frame must survive)", e.SubagentEnd.Status)
			}
		case "span_call-1_g2":
			gen2Ends++
			if e.SubagentEnd.Status != "interrupted" {
				t.Errorf("generation-2 end status = %q, want interrupted", e.SubagentEnd.Status)
			}
			if e.SubagentEnd.ParentCallId == nil || *e.SubagentEnd.ParentCallId != callID {
				t.Errorf("generation-2 end parent_call_id = %v, want %q", e.SubagentEnd.ParentCallId, callID)
			}
		}
	}
	if gen2Starts != 1 {
		t.Errorf("generation-2 subagent_start frames with span_call-1_g2 = %d, want 1", gen2Starts)
	}
	if gen1Ends != 1 {
		t.Errorf("generation-1 subagent_end frames still on span_call-1 = %d, want 1 (a follow-up must not reuse that span)", gen1Ends)
	}
	if gen2Ends != 1 {
		t.Errorf("generation-2 subagent_end frames with span_call-1_g2 = %d, want 1", gen2Ends)
	}
}

// TestSteeringReceipt_FollowUpGenerationUsesOwnSpan checks the receipt a
// steer applies during a follow-up generation. It must name that
// generation's span, not the original run's bare span.
func TestSteeringReceipt_FollowUpGenerationUsesOwnSpan(t *testing.T) {
	al, lifecycle, _, _ := newDeliverTestLoop(t)
	const parentID, childID = "parent-rcpt", "child-rcpt"
	seedParentAndChild(t, lifecycle, parentID, childID)
	seedUnifiedSession(t, al, parentID)

	rec, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("load child: %v", err)
	}
	rec.Generation = 2
	rec.State = session.LifecycleRunning
	if err := lifecycle.Persist(rec); err != nil {
		t.Fatalf("persist generation 2: %v", err)
	}
	al.deliverSteeringReceiptsForInjection(childID, []string{"corr-follow-up"})

	entries, err := al.GetSessionStore().ReadTranscript(parentID)
	if err != nil {
		t.Fatalf("read parent transcript: %v", err)
	}
	var got string
	for i := range entries {
		if entries[i].SubagentState != nil && entries[i].SubagentState.SteeringReceipt != nil {
			got = entries[i].SubagentState.SpanId
			break
		}
	}
	if got != "span_call-1_g2" {
		t.Fatalf("steering receipt span_id = %q, want span_call-1_g2", got)
	}
}
