package tools

import (
	"context"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// Round 15, CHECK findings W1 and F9 (/workspace/logs/1960/check-s1-delta5/check-report.md).
// Oracle: /workspace/logs/1960/spec-s1/spec.md (I-2) and ruling-check-delta.md (S-111 writers).
//
// W1: a rejected reassignment (denied by the delegation gate) and a same-agent
// agent_id resend accompanying a status-only change change NO instruction, so
// they must leave the durable provenance exactly as seeded. The seed is a
// RESOLVED {web} record and the writer a distinct Telegram turn: an early-join
// mutant (persisting the join before the rejection or on the no-op resend)
// would turn {web} into {web, connector:telegram}. The older seedTask fixture
// is unresolved, where an early join is origin-equivalent and cannot be seen.
//
// F9: max_attempts and clear_max_attempts are definition edits (TK-05,
// GroupDefinition), so they join the writing turn's provenance in the same write.

// seedResolvedWebTask stores a task created by agent-a with resolved provenance {web}.
func seedResolvedWebTask(t *testing.T, store *task.Store) *task.Task {
	t.Helper()
	one := 1
	tk := &task.Task{
		Title:            "test task",
		Prompt:           "do something",
		Action:           task.ActionLLM,
		AgentID:          "agent-a",
		CreatedBy:        "agent-a",
		CreatedByAgentID: "agent-a", // the creating AGENT: update_task authorises definition edits (max_attempts) by it alone
		WorkspaceID:      "ws-1",
		Status:           task.StatusNext,
		Provenance:       []string{"web"},
		MaxAttempts:      &one,
	}
	if err := store.Create(tk); err != nil {
		t.Fatalf("seedResolvedWebTask: create: %v", err)
	}
	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("seedResolvedWebTask: get: %v", err)
	}
	if want := []string{"web"}; !reflect.DeepEqual(got.Provenance, want) {
		t.Fatalf("instrument: the seed must record %v, got %v", want, got.Provenance)
	}
	if !got.CreatedByAgent("agent-a") {
		t.Fatalf("instrument: agent-a must be the creating agent, or the creator-only gate refuses definition edits before any join")
	}
	return got
}

func telegramWriterCtx() context.Context {
	origin := NewTurnOrigin()
	origin.AddProvenance(bus.NewProvenance(bus.ConnectorOrigin("telegram")))
	return WithTurnOrigin(WithAgentID(context.Background(), "agent-a"), origin)
}

func TestTaskUpdate_RejectedReassignmentLeavesResolvedProvenanceUntouched(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedResolvedWebTask(t, store)

	tool := NewTaskUpdateTool(store)
	tool.SetDelegationDenyChecker(func(_ context.Context, target string) *DelegationDenial {
		return &DelegationDenial{Reason: "reassignment to untrusted agent denied", Policy: DenyTrustSet, TargetAgentID: target}
	})

	res := tool.Execute(telegramWriterCtx(), map[string]any{"task_id": tk.ID, "agent_id": "evil-agent"})
	if res == nil || !res.IsError {
		t.Fatalf("instrument: the reassignment must be denied, got %+v", res)
	}
	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AgentID != "agent-a" {
		t.Fatalf("instrument: the assignee must be unchanged, got %q", got.AgentID)
	}
	if want := []string{"web"}; !reflect.DeepEqual(got.Provenance, want) {
		t.Fatalf("a rejected reassignment must not join the writer: provenance = %v, want %v", got.Provenance, want)
	}
}

func TestTaskUpdate_SameAgentResendWithStatusOnlyLeavesResolvedProvenanceUntouched(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	tk := seedResolvedWebTask(t, store)

	tool := NewTaskUpdateTool(store)
	tool.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })

	res := tool.Execute(telegramWriterCtx(), map[string]any{"task_id": tk.ID, "status": updStatusFailed, "agent_id": "agent-a"})
	if res == nil || res.IsError {
		t.Fatalf("instrument: the status change with a same-agent resend must succeed, got %+v", res)
	}
	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != task.StatusFailed {
		t.Fatalf("instrument: the status change must be applied, got %q", got.Status)
	}
	if want := []string{"web"}; !reflect.DeepEqual(got.Provenance, want) {
		t.Fatalf("a same-agent resend with a status-only change must not join the writer: provenance = %v, want %v", got.Provenance, want)
	}
}

// F9 at the tool layer: the retry limit is a definition field.
func TestTaskUpdate_RetryLimitOnlyEditJoinsTheTurnsProvenance(t *testing.T) {
	telegram := bus.NewProvenance(bus.ConnectorOrigin("telegram"))
	rows := []struct {
		name   string
		args   map[string]any
		writer bus.Provenance
		want   []string
	}{
		{"max_attempts-only edit from a Telegram turn", map[string]any{"max_attempts": float64(2)}, telegram, []string{"connector:telegram", "web"}},
		{"clear_max_attempts-only edit from a Telegram turn", map[string]any{"clear_max_attempts": true}, telegram, []string{"connector:telegram", "web"}},
		{"max_attempts-only edit from an unresolved turn", map[string]any{"max_attempts": float64(2)}, bus.UnresolvedProvenance(), nil},
		{"clear_max_attempts-only edit from an unresolved turn", map[string]any{"clear_max_attempts": true}, bus.UnresolvedProvenance(), nil},
		{"control: priority-only edit from a Telegram turn joins nothing", map[string]any{"priority": float64(1)}, telegram, []string{"web"}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			store := task.New(t.TempDir())
			tk := seedResolvedWebTask(t, store)
			origin := NewTurnOrigin()
			origin.AddProvenance(row.writer)
			args := map[string]any{"task_id": tk.ID}
			for k, v := range row.args {
				args[k] = v
			}
			update := NewTaskUpdateTool(store)
			update.SetDelegationDenyChecker(func(context.Context, string) *DelegationDenial { return nil })
			res := update.Execute(WithTurnOrigin(WithAgentID(context.Background(), "agent-a"), origin), args)
			if res.IsError {
				t.Fatalf("update_task failed: %s", res.ForLLM)
			}
			got, err := store.Get(tk.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if _, ok := row.args["max_attempts"]; ok && (got.MaxAttempts == nil || *got.MaxAttempts != 2) {
				t.Fatalf("instrument: max_attempts not changed to 2, got %v", got.MaxAttempts)
			}
			if _, ok := row.args["clear_max_attempts"]; ok && got.MaxAttempts != nil {
				t.Fatalf("instrument: max_attempts not cleared, got %v", *got.MaxAttempts)
			}
			if !reflect.DeepEqual(got.Provenance, row.want) {
				t.Fatalf("task provenance after the edit = %v, want %v", got.Provenance, row.want)
			}
		})
	}
}
