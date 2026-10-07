// delegate_status_quiet_test.go: #614 — status of a running native child with no observable activity must say so explicitly.

package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

type fixedProgressReader struct {
	snap ToolCallProgressSnapshot
	ok   bool
}

func (f fixedProgressReader) ProgressForSession(string) (ToolCallProgressSnapshot, bool) {
	return f.snap, f.ok
}

const quietNoteFragment = "no live tool-call activity recorded"

func statusOf(t *testing.T, reader DelegateProgressReader, is3P bool) string {
	t.Helper()
	now := time.Date(2026, time.October, 7, 12, 0, 0, 0, time.UTC)
	tool, lifecycle, _, _ := newADR053TestTool(t)
	tool.SetClock(func() time.Time { return now })
	if reader != nil {
		tool.SetProgressReader(reader)
	}
	ctx := WithTranscriptSessionID(context.Background(), "parent-quiet")
	if err := lifecycle.Persist(&session.LifecycleRecord{
		SessionID: "child-quiet", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: "parent-quiet",
		SteeredBy:   &session.SteeredBy{SteeringSessionID: "parent-quiet", RootSessionID: "parent-quiet"},
		WorkspaceID: "ws-quiet", AgentID: "worker", ParentAgentID: "orchestrator",
		CreatedAt: now.Add(-30 * time.Second), Is3P: is3P,
	}); err != nil {
		t.Fatalf("persist: %v", err)
	}
	got := tool.Execute(ctx, map[string]any{"action": "status", "session_id": "child-quiet"})
	if got.IsError {
		t.Fatalf("status failed: %s", got.ForLLM)
	}
	return got.ForLLM
}

// A running native child with no progress snapshot and no transcript activity
// is indistinguishable from a hung one unless status says it cannot tell.
func TestDelegateStatus_RunningNoActivitySaysCannotReport(t *testing.T) {
	t.Parallel()
	for name, reader := range map[string]DelegateProgressReader{
		"no reader wired":      nil,
		"reader reports false": fixedProgressReader{},
	} {
		got := statusOf(t, reader, false)
		if !strings.Contains(got, quietNoteFragment) || !strings.Contains(got, "does not mean the child is stalled") {
			t.Errorf("%s: status must carry the explicit quiet-vs-cannot-report note, got %q", name, got)
		}
	}
}

// Control: a live snapshot is real progress; the "unavailable" note must not
// accompany it (it would contradict the progress line).
func TestDelegateStatus_RunningWithProgressHasNoQuietNote(t *testing.T) {
	t.Parallel()
	got := statusOf(t, fixedProgressReader{
		snap: ToolCallProgressSnapshot{Name: "write_file", ArgsBytes: 10, TotalArgsBytes: 10, Age: time.Second, LastActivity: time.Now()},
		ok:   true,
	}, false)
	if strings.Contains(got, quietNoteFragment) {
		t.Errorf("progress available, must not say none recorded: %q", got)
	}
	if !strings.Contains(got, "progress: generating tool call") {
		t.Errorf("expected the live progress line, got %q", got)
	}
}

// External-CLI children keep their own fixed note, not the native one.
func TestDelegateStatus_Running3PKeepsOwnNote(t *testing.T) {
	t.Parallel()
	got := statusOf(t, nil, true)
	if !strings.Contains(got, delegate3PStatusNote) || strings.Contains(got, quietNoteFragment) {
		t.Errorf("3P status = %q", got)
	}
}
