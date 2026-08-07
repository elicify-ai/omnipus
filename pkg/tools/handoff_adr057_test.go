// handoff_adr057_test.go — ADR-057 U22 (W3c): handoff.go's two AppendTranscript
// writers (HandoffTool.Execute:205, ReturnToDefaultTool.Execute:386) convert
// to the strict AppendTranscriptStrict entry point (FR-001/FR-002), and the
// HandoffSessionStore interface declaration (:70) is renamed to match.
//
// FR-002's conversion-boundary table requires every AppendTranscript
// invocation site to surface the now-possible strict-write error as a
// counter increment plus a WARN naming the session id — this file covers
// that NEW behavior for handoff.go's two sites. Both writes remain
// best-effort by design (Execute step 7's doc comment: a failed audit-trail
// entry must never fail the handoff/return-to-default operation itself),
// so the counter is the only durable, operator-visible signal a write
// failed.
//
// The pre-existing handoff_test.go's stubSessionStore.AppendTranscript was
// mechanically renamed to AppendTranscriptStrict (same behavior, always
// succeeds) so it keeps satisfying the updated HandoffSessionStore
// interface — its own tests are unaffected since none of them force a
// non-nil append error. This file adds the one new package-level test
// double the strict-failure path needs (u22-prefixed per binding rule 6).

package tools

import (
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// u22FailingAppendStore is a HandoffSessionStore whose AppendTranscriptStrict
// always fails, letting the tests below exercise FR-002's strict-write
// failure path without needing a real UnifiedStore against an unminted
// session id (that scenario is already covered end to end by
// pkg/session's own AppendTranscriptStrict tests; this file's concern is
// purely "does handoff.go surface the error correctly").
type u22FailingAppendStore struct {
	switchErr  error
	transcript []session.TranscriptEntry
	appendErr  error
}

func (s *u22FailingAppendStore) SwitchAgent(sessionID, newAgentID string) error {
	return s.switchErr
}

func (s *u22FailingAppendStore) ReadTranscript(sessionID string) ([]session.TranscriptEntry, error) {
	return s.transcript, nil
}

func (s *u22FailingAppendStore) AppendTranscriptStrict(sessionID string, entry session.TranscriptEntry) error {
	return s.appendErr
}

// TestHandoffTool_AuditWriteFailure_CountedNotFatal covers FR-002 for
// handoff.go:205: a failed AppendTranscriptStrict on the audit-trail write
// must be surfaced as a counter increment (HandoffTranscriptWriteFailures)
// and must NOT fail the handoff itself.
func TestHandoffTool_AuditWriteFailure_CountedNotFatal(t *testing.T) {
	before := HandoffTranscriptWriteFailures()

	store := &u22FailingAppendStore{appendErr: errors.New("session unminted: no meta.json")}
	reg := &stubRegistry{agents: map[string]string{"ray": "Ray"}}
	tool := NewHandoffTool(
		func() AgentRegistryReader { return reg },
		store,
		func(string) int { return 8192 },
		nil,
	)
	ctx := makeCtx("session_abc", "chat_1", "mia")
	result := tool.Execute(ctx, map[string]any{
		"agent_id": "ray",
		"context":  "user needs billing help",
	})

	if result.IsError {
		t.Fatalf("a failed audit-trail write must not fail the handoff (best-effort by design); got error: %s", result.ForLLM)
	}

	after := HandoffTranscriptWriteFailures()
	if after != before+1 {
		t.Fatalf("HandoffTranscriptWriteFailures() = %d, want %d (exactly one failure counted)", after, before+1)
	}
}

// TestHandoffTool_AuditWriteSuccess_DoesNotIncrementFailureCounter is the
// positive lower bound (binding rule 4): a SUCCESSFUL write must leave the
// counter unchanged, so the +1 delta asserted above is a meaningful signal
// rather than an artifact of the counter free-running.
func TestHandoffTool_AuditWriteSuccess_DoesNotIncrementFailureCounter(t *testing.T) {
	before := HandoffTranscriptWriteFailures()

	store := &stubSessionStore{}
	reg := &stubRegistry{agents: map[string]string{"ray": "Ray"}}
	tool := NewHandoffTool(
		func() AgentRegistryReader { return reg },
		store,
		func(string) int { return 8192 },
		nil,
	)
	ctx := makeCtx("session_abc", "chat_1", "mia")
	result := tool.Execute(ctx, map[string]any{
		"agent_id": "ray",
		"context":  "test",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}

	after := HandoffTranscriptWriteFailures()
	if after != before {
		t.Fatalf("HandoffTranscriptWriteFailures() = %d, want unchanged at %d after a successful write", after, before)
	}
}

// TestReturnToDefaultTool_AuditWriteFailure_CountedNotFatal mirrors the
// above for handoff.go:386 (ReturnToDefaultTool.Execute's writer).
func TestReturnToDefaultTool_AuditWriteFailure_CountedNotFatal(t *testing.T) {
	before := HandoffTranscriptWriteFailures()

	store := &u22FailingAppendStore{appendErr: errors.New("session unminted: no meta.json")}
	tool := NewReturnToDefaultTool(store, func() string { return "mia" }, nil)
	ctx := makeCtx("session_abc", "chat_1", "ray")
	result := tool.Execute(ctx, map[string]any{})

	if result.IsError {
		t.Fatalf("a failed audit-trail write must not fail return_to_default (best-effort by design); got error: %s", result.ForLLM)
	}

	after := HandoffTranscriptWriteFailures()
	if after != before+1 {
		t.Fatalf("HandoffTranscriptWriteFailures() = %d, want %d (exactly one failure counted)", after, before+1)
	}
}
