// rest_task_verdicts_store_test.go — regression cover for the read half of
// the store mismatch behind CI "E2E — llm-verifier-eval".
//
// handleTaskVerdicts resolved its transcript store by AGENT
// (agentLoop.GetAgentStore(t.AgentID) — the legacy per-agent store under the
// agent's own workspace) rather than by SESSION. Since ADR-091 routes task
// starts through SteerLauncher.Launch, a started task's session is minted in
// the SHARED store, so the per-agent lookup found no transcript.jsonl at all
// and the endpoint answered 200 [] — indistinguishable from "the Judge never
// ran", which is exactly what the eval reported ("BLOCKED: no JudgeVerdict
// was recorded").
//
// Every other session-reading REST boundary already resolves by session id
// via restAPI.resolveSessionStore (rest_sessions.go); this one did not.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// seedTaskWithVerdict creates a task bound to sessionID and writes one
// judge_verdict transcript entry into store — the exact entry shape
// TaskExecutor.writeJudgeVerdictTranscript persists.
func seedTaskWithVerdict(
	t *testing.T, api *restAPI, store *session.UnifiedStore, sessionID, agentID string,
) string {
	t.Helper()
	tk := &task.Task{
		Title: "verdict store binding", Prompt: "x", Action: task.ActionLLM,
		AgentID: agentID, Priority: 3, WorkspaceID: "default",
		Status: task.StatusFailed, SessionID: sessionID,
	}
	require.NoError(t, api.taskStore.Create(tk))

	verdict := task.JudgeVerdict{
		ID: "verdict-1", Scope: task.VerdictScopeTask, TaskID: tk.ID,
		Round: 1, Met: false, JudgeAgentID: "judge", Model: "test-judge-model",
		JudgedAt:     "2026-09-24T00:00:00Z",
		PerCriterion: []task.CriterionVerdict{{CriterionID: "c1", Met: false, Reason: "no tool call supports this claim"}},
	}
	payload, err := json.Marshal(verdict)
	require.NoError(t, err)
	require.NoError(t, store.AppendTranscriptStrict(sessionID, session.TranscriptEntry{
		ID:      tk.ID + "-judge-1",
		Type:    session.EntryTypeJudgeVerdict,
		Role:    "system",
		Content: string(payload),
		AgentID: "judge",
	}))
	return tk.ID
}

// getTaskVerdicts calls the handler behind GET /api/v1/tasks/{id}/verdicts.
func getTaskVerdicts(t *testing.T, api *restAPI, taskID string) []gen.JudgeVerdict {
	t.Helper()
	rec := httptest.NewRecorder()
	api.handleTaskVerdicts(rec, taskID)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var out []gen.JudgeVerdict
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

// TestHandleTaskVerdicts_SharedStoreSession_ReturnsVerdict is the failing CI
// shape: an ADR-091 launcher-minted task session lives in the SHARED store.
// The recorded verdict must come back, not an empty array.
func TestHandleTaskVerdicts_SharedStoreSession_ReturnsVerdict(t *testing.T) {
	api := newTestRestAPIWithAgent(t)
	shared := api.agentLoop.GetSessionStore()
	require.NotNil(t, shared, "the shared store is where the ADR-091 launcher mints task sessions")

	meta, err := shared.NewSession(session.SessionTypeTask, "", "01JXTESTAGENTSTARTTEST001")
	require.NoError(t, err)
	taskID := seedTaskWithVerdict(t, api, shared, meta.ID, "01JXTESTAGENTSTARTTEST001")

	out := getTaskVerdicts(t, api, taskID)
	require.Len(t, out, 1,
		"GET /tasks/{id}/verdicts must return the verdict recorded for the task's own session; "+
			"an empty array here is indistinguishable from 'the Judge never ran'")
	require.Equal(t, "verdict-1", out[0].Id)
	require.False(t, out[0].Met)
	require.Len(t, out[0].PerCriterion, 1)
}

// TestHandleTaskVerdicts_LegacyPerAgentSession_ReturnsVerdict keeps the
// pre-ADR-091 path covered: ExecuteTask's createTaskSessionSync still mints
// into the per-agent store, and those verdicts must keep coming back too.
func TestHandleTaskVerdicts_LegacyPerAgentSession_ReturnsVerdict(t *testing.T) {
	api := newTestRestAPIWithAgent(t)
	perAgent := api.agentLoop.GetAgentStore("01JXTESTAGENTSTARTTEST001")
	require.NotNil(t, perAgent)

	meta, err := perAgent.NewSession(session.SessionTypeTask, "", "01JXTESTAGENTSTARTTEST001")
	require.NoError(t, err)
	taskID := seedTaskWithVerdict(t, api, perAgent, meta.ID, "01JXTESTAGENTSTARTTEST001")

	out := getTaskVerdicts(t, api, taskID)
	require.Len(t, out, 1)
	require.Equal(t, "verdict-1", out[0].Id)
}
