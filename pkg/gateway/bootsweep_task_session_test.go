package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracle: session-core FR-017, OpenAPI listSessions/Session, and sub-agent
// control plane D8: a restart-cut steered child stays active, displays Stopped,
// and retains its restart cause. Its original identity survives SIGKILL.
func TestBootsweepTaskSession_RestartVisibility(t *testing.T) {
	if receiptPath := os.Getenv(bootsweepTaskReceiptEnv); receiptPath != "" {
		bootsweepWriteTaskReceipt(t, receiptPath)
		return
	}
	m := bootsweepKillTask(t)
	t.Setenv("OMNIPUS_HOME", m.Boot.Config.Agents.Defaults.Home)
	before, err := session.NewLifecycleStore(m.Boot.LifecycleDir).Load(m.Boot.Root)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleRunning, before.State,
		"instrument: SIGKILL leaves this actual task execution in flight on disk")
	require.NotNil(t, before.ExecutionID)

	fresh, stg, provider, _ := i1ReopenAndBoot(t, m.Boot)
	store := agent.GetTaskStore(fresh)
	require.NotNil(t, store)
	api := &restAPI{agentLoop: fresh, homePath: m.Boot.Config.Agents.Defaults.Home, taskStore: store}
	api.reconcileStuckTasks()
	bound, err := store.Get(m.TaskID)
	require.NoError(t, err)
	require.Equal(t, m.Boot.Root, bound.SessionID, "restart must not replace the task's session id")
	require.Equal(t, task.StatusFailed, bound.Status)
	require.Equal(t, "interrupted: gateway restarted while task was running", bound.Result)
	mainID, err := session.MainSessionID(bound.WorkspaceID, bound.AgentID)
	require.NoError(t, err)

	page := bootsweepSessionPage(t, api, "/api/v1/sessions?flat=true")
	t.Logf("pre-kill task session=%s; post-restart listed ids=%v", m.Boot.Root, bootsweepSessionIDs(page))
	var child *gen.Session
	for i := range page.Sessions {
		if page.Sessions[i].Id == m.Boot.Root {
			child = &page.Sessions[i]
		}
	}
	require.NotNil(t, child, "pre-kill task session must still be present after restart")
	assert.Equal(t, gen.SessionStatusActive, child.Status)
	require.NotNil(t, child.LifecycleState)
	assert.Equal(t, gen.SessionLifecycleStateStopped, *child.LifecycleState)
	require.NotNil(t, child.StopNote)
	assert.Equal(t, gen.SessionStopNoteCauseRestart, child.StopNote.Cause)
	assert.Equal(t, "restart", child.StopNote.By)
	assert.Nil(t, child.Execution, "the old process owns no live execution after restart")
	require.NotNil(t, child.ParentSessionId)
	assert.Equal(t, mainID, *child.ParentSessionId)
	require.NotNil(t, child.TaskId)
	assert.Equal(t, m.TaskID, *child.TaskId)
	assert.Equal(t, bound.AgentID, child.AgentId)
	require.NotNil(t, child.WorkspaceId)
	assert.Equal(t, bound.WorkspaceID, *child.WorkspaceId)
	t.Logf("recovered child=%s status=%s lifecycle=%s stop=%s execution=%v",
		child.Id, child.Status, *child.LifecycleState, child.StopNote.Cause, child.Execution)

	roots := bootsweepSessionPage(t, api, "/api/v1/sessions")
	require.Contains(t, bootsweepSessionIDs(roots), mainID, "positive control: the main is a visible root")
	assert.NotContains(t, bootsweepSessionIDs(roots), m.Boot.Root, "OpenAPI: default list excludes children")
	children := bootsweepSessionPage(t, api, "/api/v1/sessions?parent_session_id="+url.QueryEscape(mainID))
	assert.Equal(t, []string{m.Boot.Root}, bootsweepSessionIDs(children), "the original task is its main's direct child")

	w := httptest.NewRecorder()
	api.HandleSessions(w, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+m.Boot.Root, nil))
	require.Equal(t, http.StatusOK, w.Code, "exact-id detail must remain readable: %s", w.Body.String())
	var detail gen.SessionDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	assert.Equal(t, child.Id, detail.Session.Id)
	require.NotNil(t, detail.Session.LifecycleState)
	assert.Equal(t, string(*child.LifecycleState), string(*detail.Session.LifecycleState))
	require.NotNil(t, detail.Session.StopNote)
	assert.Equal(t, child.StopNote.At, detail.Session.StopNote.At)
	assert.Equal(t, child.StopNote.By, detail.Session.StopNote.By)
	assert.Equal(t, child.StopNote.Seq, detail.Session.StopNote.Seq)
	assert.Equal(t, string(child.StopNote.Cause), string(detail.Session.StopNote.Cause))
	assert.Nil(t, detail.Session.Execution)
	after, err := stg.lifecycleStore.Load(m.Boot.Root)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleStopped, after.State)
	assert.False(t, after.Terminal(), "a restart-cut child stays resumable")
	assert.Equal(t, before.Generation, after.Generation)
	assert.Equal(t, before.ExecutionID, after.ExecutionID, "recovery records the old execution, never invents one")
	assert.Zero(t, provider.calls.Load(), "boot must not replay the interrupted provider call")
}

func bootsweepSessionPage(t *testing.T, api *restAPI, target string) gen.SessionPage {
	t.Helper()
	w := httptest.NewRecorder()
	api.HandleSessions(w, httptest.NewRequest(http.MethodGet, target, nil))
	require.Equal(t, http.StatusOK, w.Code, "GET %s: %s", target, w.Body.String())
	var page gen.SessionPage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	require.Nil(t, page.PartialErrors, "the proof must not silently accept degraded session reads")
	return page
}

func bootsweepSessionIDs(page gen.SessionPage) []string {
	ids := make([]string, 0, len(page.Sessions))
	for _, row := range page.Sessions {
		ids = append(ids, row.Id)
	}
	return ids
}
