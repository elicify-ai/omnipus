// task_verdict_store_binding_test.go — regression cover for the store
// mismatch that made GET /api/v1/tasks/{id}/verdicts answer [] for every
// launcher-started task (CI "E2E — llm-verifier-eval").
//
// Since ADR-091 (feat(task): route task starts through session launcher,
// 7cbe3df99) a task started via StartTaskNow mints its session through
// SteerLauncher.Launch -> launchOrdinaryRoot, which calls
// AgentLoop.GetSessionStore().NewSession — the SHARED store at
// $OMNIPUS_HOME/sessions. writeJudgeVerdictTranscript still resolved its
// store by AGENT (GetAgentStore, the legacy per-agent store under the
// agent's own workspace), so AppendTranscriptStrict's existence check
// ("session %q does not exist") refused the write and the Judge's verdict
// was never persisted anywhere.
//
// The store a task session's verdict is written to must be the store that
// session actually lives in — whichever of the two that is.

package agent

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// judgeVerdictEntriesIn returns sessionID's persisted judge_verdict entries.
func judgeVerdictEntriesIn(t *testing.T, store *session.UnifiedStore, sessionID string) []session.TranscriptEntry {
	t.Helper()
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		t.Fatalf("ReadTranscript(%q): %v", sessionID, err)
	}
	var out []session.TranscriptEntry
	for _, e := range entries {
		if e.Type == session.EntryTypeJudgeVerdict {
			out = append(out, e)
		}
	}
	return out
}

// TestWriteJudgeVerdictTranscript_SharedStoreSession_PersistsVerdict pins the
// ADR-091 launcher shape: the task session lives in the SHARED store, and the
// verdict must land in it. Before the fix this failed with zero persisted
// judge_verdict entries — AppendTranscriptStrict refused the write against the
// per-agent store, which has no such session.
func TestWriteJudgeVerdictTranscript_SharedStoreSession_PersistsVerdict(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	shared := al.GetSessionStore()
	if shared == nil {
		t.Fatal("GetSessionStore() returned nil — the shared store is where the ADR-091 launcher mints task sessions")
	}
	// Exactly what SteerLauncher.launchOrdinaryRoot does for a task-origin
	// LaunchRequest: sessions.NewSession on the SHARED store.
	sessionID := u26FreshTaskSession(t, shared, "native-agent")

	tk := &task.Task{
		Title: "launcher-minted task session", Prompt: "x", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default",
		Status: task.StatusInProgress, SessionID: sessionID,
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	verdict := &task.JudgeVerdict{
		ID: "verdict-shared-1", Scope: task.VerdictScopeTask, TaskID: tk.ID,
		Round: 1, Met: false, JudgeAgentID: "judge",
		PerCriterion: []task.CriterionVerdict{
			{CriterionID: "c1", Met: false, Reason: "the claim is not supported by any tool call in this run"},
		},
	}
	al.taskExecutor.writeJudgeVerdictTranscript(tk, sessionID, verdict)

	got := judgeVerdictEntriesIn(t, shared, sessionID)
	if len(got) != 1 {
		t.Fatalf("persisted judge_verdict entries in the session's own store = %d, want exactly 1 — "+
			"the verdict the Judge produced must be readable back from the session it was judged for", len(got))
	}
	var round task.JudgeVerdict
	if err := json.Unmarshal([]byte(got[0].Content), &round); err != nil {
		t.Fatalf("persisted verdict content is not a task.JudgeVerdict: %v", err)
	}
	if round.ID != verdict.ID || round.Round != 1 || round.Met {
		t.Errorf("persisted verdict = id %q round %d met %v; want id %q round 1 met false",
			round.ID, round.Round, round.Met, verdict.ID)
	}
	if len(round.PerCriterion) != 1 || round.PerCriterion[0].Met {
		t.Errorf("persisted per_criterion = %+v; want exactly one entry with met=false", round.PerCriterion)
	}
}

// TestWriteJudgeVerdictTranscript_LegacyPerAgentSession_StillPersists keeps the
// pre-ADR-091 path covered: ExecuteTask's createTaskSessionSync still mints
// into the per-agent store, so a verdict for such a session must keep landing
// there. This is the half a naive "always use the shared store" fix would break.
func TestWriteJudgeVerdictTranscript_LegacyPerAgentSession_StillPersists(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	perAgent := al.GetAgentStore("native-agent")
	if perAgent == nil {
		t.Fatal("GetAgentStore(native-agent) returned nil")
	}
	sessionID := u26FreshTaskSession(t, perAgent, "native-agent")

	tk := &task.Task{
		Title: "legacy per-agent task session", Prompt: "x", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default",
		Status: task.StatusInProgress, SessionID: sessionID,
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	verdict := &task.JudgeVerdict{
		ID: "verdict-legacy-1", Scope: task.VerdictScopeTask, TaskID: tk.ID,
		Round: 1, Met: true, JudgeAgentID: "judge",
		PerCriterion: []task.CriterionVerdict{{CriterionID: "c1", Met: true, Reason: "evidenced"}},
	}
	al.taskExecutor.writeJudgeVerdictTranscript(tk, sessionID, verdict)

	if got := judgeVerdictEntriesIn(t, perAgent, sessionID); len(got) != 1 {
		t.Fatalf("persisted judge_verdict entries in the legacy per-agent store = %d, want exactly 1", len(got))
	}
}
