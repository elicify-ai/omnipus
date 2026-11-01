package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func capRedDelegateTool(t *testing.T, al *AgentLoop) *tools.DelegateTool {
	t.Helper()
	dt := delegateToolFor(t, al)
	dt.SetSessionLauncher(NewSteerLauncher(al))
	return dt
}

func capRedInbox(t *testing.T, al *AgentLoop, parent, child string) []generated.SessionMessage {
	t.Helper()
	msgs, _, more, err := al.GetMessageInboxStore().Drain(parent, child, "", 100)
	if err != nil {
		t.Fatalf("read real notice inbox: %v", err)
	}
	if more {
		t.Fatal("test unexpectedly exceeds inbox page")
	}
	return msgs
}
func capRedNotice(t *testing.T, msg generated.SessionMessage) generated.SessionMessageProgress {
	t.Helper()
	p, err := msg.AsSessionMessageProgress()
	if err != nil || p.Kind != "progress" {
		t.Fatalf("notice must be an existing progress envelope: %+v / %v", p, err)
	}
	return p
}
func capRedDispatchObservation(t *testing.T, d steer.DispatchResult) time.Time {
	t.Helper()
	field := reflect.ValueOf(d).FieldByName("ObservedAt")
	if !field.IsValid() {
		t.Error("CAP-B1 RED: DispatchResult does not carry its decision ObservedAt")
		return time.Time{}
	}
	return field.Interface().(time.Time)
}
func capRedStoredQueueTime(t *testing.T, g *steerAdmission, sid string) time.Time {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, entry := range g.queue {
		if entry.sessionID != sid {
			continue
		}
		at := reflect.ValueOf(entry).FieldByName("ObservedAt")
		if !at.IsValid() {
			t.Error("CAP-B1 RED: queue entry has no immutable ObservedAt")
			return time.Time{}
		}
		return at.Interface().(time.Time)
	}
	t.Errorf("no queued entry for %s", sid)
	return time.Time{}
}

func capRedQueuedMainMember(t *testing.T, al *AgentLoop) (*task.Task, string, []executionClaim) {
	t.Helper()
	installTestSessionLauncher(t, al)
	wireSteerCompletionDeps(t, al)
	al.GetConfig().Performance.MaxParallelAgents = 8
	held := capRedFillSteer(t, al.steerAdmission(), 8)
	main, err := al.GetSessionStore().GetOrCreateMainSession(testHarnessWorkspaceMembershipID, testDefaultAgentID)
	if err != nil {
		t.Fatal(err)
	}
	te := GetTaskExecutor(al)
	te.SetMainSessionResolver(&u6FakeMainResolver{elig: map[[2]string]string{{testHarnessWorkspaceMembershipID, testDefaultAgentID}: main.ID}})
	ps := plan.New(t.TempDir())
	al.SetPlanStore(ps)
	te.SetPlanStore(ps)
	p := mustCreatePlan(t, ps, &plan.Plan{ID: "notice-plan", Title: "plan", State: plan.StateRunning, OwnerAgentID: testDefaultAgentID, WorkspaceID: testHarnessWorkspaceMembershipID})
	tk := capRedTask(t, al, "notice-member", task.StatusNext, p.ID)
	no := false
	if _, err := GetTaskStore(al).Update(tk.ID, task.Patch{RunIsolated: &no}); err != nil {
		t.Fatal(err)
	}
	return tk, main.ID, held
}

func TestCapRED_Delegate_QueuedSentenceAndClosedJSON(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		configured, held, limit int
		high, ok                bool
		template                string
	}{
		{"S24_configured", 2, 2, 2, false, true, "Queued: all %d delegated turn slots are busy (%d in use, checked just now); this turn has not started. Queue position 1. Use delegate(action=\"stop_all\") with session_id=%q to stop this queued session."},
		{"S25_unset_pressure", 0, 8, 2, true, true, "Queued: this machine is low on memory, so new delegated turns are held at %d at a time (%d in use, checked just now); this turn has not started. Queue position 1. Use delegate(action=\"stop_all\") with session_id=%q to stop this queued session."},
		{"S26_configured_one", 1, 1, 1, true, true, "Queued: all %d delegated turn slots are busy (%d in use, checked just now); this turn has not started. Queue position 1. Use delegate(action=\"stop_all\") with session_id=%q to stop this queued session."},
		{"S27_explicit_pressure", 8, 8, 2, true, true, "Queued: this machine is low on memory, so new delegated turns are held at %d at a time (%d in use, checked just now); this turn has not started. Queue position 1. Use delegate(action=\"stop_all\") with session_id=%q to stop this queued session."},
		{"S28a_unverifiable", 8, 8, 2, false, false, "Queued: this machine cannot report its available memory, so new delegated turns are held at %d at a time (%d in use, checked just now); this turn has not started. Queue position 1. Use delegate(action=\"stop_all\") with session_id=%q to stop this queued session."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			high, ok := false, true
			capRedMemory(t, &high, &ok)
			al, _ := newSteerAL(t)
			installTestSessionLauncher(t, al)
			wireSteerCompletionDeps(t, al)
			_, release := installParkedProvider(t, al)
			defer release()
			al.GetConfig().Performance.MaxParallelAgents = tc.configured
			capRedFillSteer(t, al.steerAdmission(), tc.held)
			high, ok = tc.high, tc.ok
			parent := newTestSteeringSession(t, al, testHarnessWorkspaceMembershipID)
			ctx := tools.WithAgentID(tools.WithWorkspaceID(tools.WithTranscriptSessionID(context.Background(), parent), testHarnessWorkspaceMembershipID), testDefaultAgentID)
			result := capRedDelegateTool(t, al).Execute(ctx, map[string]any{"action": "run", "agent_id": testDefaultAgentID, "task": "hold this work", "label": "capacity-child"})
			if result == nil || result.IsError {
				t.Fatalf("real delegate failed: %+v", result)
			}
			parts := strings.SplitN(result.ForLLM, "\n", 2)
			var obj map[string]any
			if err := json.Unmarshal([]byte(parts[0]), &obj); err != nil {
				t.Fatal(err)
			}
			sid, _ := obj["session_id"].(string)
			if sid == "" {
				t.Fatal("real queued result lacks session id")
			}
			capRedAssert(t, "closed queued delegate JSON", obj, map[string]any{"session_id": sid, "generation": float64(1), "state": "queued", "queue_position": float64(1), "is_3p": false})
			want := fmt.Sprintf(tc.template, tc.limit, tc.held, sid)
			if len(parts) != 2 {
				t.Errorf("missing queued sentence, want %q", want)
			} else {
				capRedAssert(t, "exact M10 cause and numbers", parts[1], want)
			}
			// S37a: this one episode is already conveyed to THIS parent by this call.
			capRedAssert(t, "same-parent launch result is not duplicated", len(capRedInbox(t, al, parent, sid)), 0)
		})
	}
}

func TestCapRED_CapNotice_ProgressDeliveredNotWoken(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	tk, parent, _ := capRedQueuedMainMember(t, al)
	var wakes atomic.Int32
	al.asyncNotifier.registerObserver(func(e AsyncNotifyEvent) {
		if strings.HasPrefix(e.SourceKind, "message_parent:") {
			wakes.Add(1)
		}
	})
	high = true
	if err := GetTaskExecutor(al).ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatal(err)
	}
	stored, err := GetTaskStore(al).Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	messages := capRedInbox(t, al, parent, stored.SessionID)
	if len(messages) != 1 {
		t.Fatalf("S36 exactly one real parent progress notice, got %d", len(messages))
	}
	p := capRedNotice(t, messages[0])
	rec, err := al.GetSessionLifecycleStore().Load(stored.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ExecutionID == nil {
		t.Fatal("SETUP dispatch did not stamp full execution claim")
	}
	wantID := fmt.Sprintf("cap-notice:%s:%s:%d:%s:%d:1:1", parent, rec.SessionID, rec.Generation, rec.ExecutionID.RunID, rec.ExecutionID.BootSeq)
	capRedAssert(t, "full-claim placement/episode identity", p.MessageId, wantID)
	at := capRedStoredQueueTime(t, al.steerAdmission(), stored.SessionID)
	expected := fmt.Sprintf("Cap notice: at %s your plan member %q had a turn waiting to start. The lane allowed 2 delegated turns at a time then (8 in use): the machine was low on memory.", at.UTC().Format(time.RFC3339), tk.Title)
	capRedAssert(t, "exact dated M18", p.Text, expected)
	capRedAssert(t, "notice uses current generation", p.Generation, &rec.Generation)
	capRedAssert(t, "parent only", p.ParentSessionId, &parent)
	capRedAssert(t, "progress cannot wake recipient", wakes.Load(), int32(0))
	capRedAssert(t, "notice starts no turn", al.steerAdmission().activeCount(), 8)
	capRedAssert(t, "task remains in_progress", stored.Status, task.StatusInProgress)
	foreign := newTestSteeringSession(t, al, "different-workspace")
	capRedAssert(t, "unrelated recipient got nothing", len(capRedInbox(t, al, foreign, stored.SessionID)), 0)
}

func TestCapRED_CapNotice_TwoCauseCyclesKeepEpisodeIdentity(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	_, release := installParkedProvider(t, al)
	defer release()
	tk, parent, holders := capRedQueuedMainMember(t, al)
	high = true
	if err := GetTaskExecutor(al).ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatal(err)
	}
	stored, err := GetTaskStore(al).Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	initial := capRedInbox(t, al, parent, stored.SessionID)
	if len(initial) != 1 {
		t.Fatalf("S51 initial episode missing: got %d notices; later cycle assertions cannot be exercised", len(initial))
	}
	first := capRedNotice(t, initial[0])
	seen := map[string]bool{first.MessageId: true}
	for i, step := range []struct {
		high, ok bool
		count    int
		cause    string
	}{
		{true, true, 1, "the machine was low on memory"},
		{false, false, 2, "the machine could not report its available memory"},
		{true, true, 3, "the machine was low on memory"},
		{false, false, 4, "the machine could not report its available memory"},
		{true, true, 5, "the machine was low on memory"},
	} {
		high, ok = step.high, step.ok
		al.drainSteerQueue(holders[i]) // REAL production release consumer, no fake notice.
		msgs := capRedInbox(t, al, parent, stored.SessionID)
		capRedAssert(t, "per-episode count", len(msgs), step.count)
		capRedAssert(t, "pressure never promotes above floor", al.steerAdmission().queueLen(), 1)
		if len(msgs) != step.count {
			continue
		}
		last := capRedNotice(t, msgs[len(msgs)-1])
		if i == 0 {
			capRedAssert(t, "same-cause reconsideration retains id", last.MessageId, first.MessageId)
			continue
		}
		if seen[last.MessageId] {
			t.Errorf("returned cause reused an old episode id: %s", last.MessageId)
		}
		seen[last.MessageId] = true
		// The identity suffix is an episode integer, never pressure->unverifiable.
		capRedAssert(t, "episode suffix", strings.HasSuffix(last.MessageId, fmt.Sprintf(":1:%d", step.count)), true)
		if strings.Contains(last.MessageId, "memory_") {
			t.Errorf("cause transition leaked into id: %s", last.MessageId)
		}
		decisionAt := capRedStoredQueueTime(t, al.steerAdmission(), stored.SessionID)
		expected := fmt.Sprintf("Cap notice: at %s your plan member %q had a turn waiting to start. The lane allowed 2 delegated turns at a time then (%d in use): %s.", decisionAt.UTC().Format(time.RFC3339), tk.Title, 8-i-1, step.cause)
		capRedAssert(t, "each episode describes its own current refusal", last.Text, expected)
	}
}

func TestCapRED_DelegateStatus_DatedAfterRecoveryNoEvent(t *testing.T) {
	var high, readable atomic.Bool
	readable.Store(true)
	var readings atomic.Int32
	restore := config.SetMemoryProviderForTest(func() (bool, bool) { readings.Add(1); return high.Load(), readable.Load() }, func() (uint64, bool) { return 1 << 30, readable.Load() })
	t.Cleanup(restore)
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	al.GetConfig().Performance.MaxParallelAgents = 8
	capRedFillSteer(t, al.steerAdmission(), 8)
	parent := newTestSteeringSession(t, al, testHarnessWorkspaceMembershipID)
	child := capRedLaunchChild(t, al, parent, "historical")
	high.Store(true)
	d, err := NewSteerLauncher(al).Dispatch(context.Background(), child.SessionID, child.Generation)
	if err != nil {
		t.Fatal(err)
	}
	at := capRedDispatchObservation(t, d)
	if at.IsZero() {
		t.Fatal("S59 cannot date an admission with no original observation; interface RED, not a passed historical read")
	}
	high.Store(false)
	al.GetConfig().Performance.MaxParallelAgents = 16
	tool := capRedDelegateTool(t, al)
	tool.SetClock(func() time.Time { return at.Add(120 * time.Second) })
	before := readings.Load()
	text := capRedStatus(t, al, parent, child.SessionID)
	clause := "Admission: this child has a turn waiting to start. At its last check (2 minutes ago) the lane allowed 2 delegated turns at a time (8 in use then): the machine was low on memory. Queue position 1. list_jobs shows current state."
	atClause := strings.Index(text, "Admission:")
	if atClause < 0 {
		t.Errorf("missing historical clause, want %q; result=%q", clause, text)
	} else {
		capRedAssert(t, "complete M24 appended clause", strings.TrimSpace(text[atClause:]), clause)
	}
	capRedAssert(t, "historical read performs no memory measurement", readings.Load(), before)
	capRedAssert(t, "historical read performs no admission", al.steerAdmission().activeCount(), 8)
	for _, present := range []string{"is low on memory", "cannot report its available memory", "all 16 delegated turn slots"} {
		if strings.Contains(text, present) {
			t.Errorf("present-tense diagnosis after recovery: %q", text)
		}
	}
}
