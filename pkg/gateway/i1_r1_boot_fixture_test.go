package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type i1R1BootFixture struct {
	api      *restAPI
	ls       *session.LifecycleStore
	boot     *session.BootEpochStore
	oldEpoch uint64
	notices  []string
}

func newI1R1BootFixture(t *testing.T) *i1R1BootFixture {
	t.Helper()
	api, cleanup := newTestRestAPI(t)
	t.Cleanup(cleanup)
	ls := session.NewLifecycleStore(t.TempDir())
	api.agentLoop.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), ls)
	home := api.agentLoop.GetConfig().Agents.Defaults.Home
	old := session.NewBootEpochStore(home)
	oldEpoch, err := old.Mint()
	require.NoError(t, err)
	api.agentLoop.SetBootEpochStore(old)
	boot := session.NewBootEpochStore(home)
	current, err := boot.Mint()
	require.NoError(t, err)
	require.Greater(t, current, oldEpoch, "instrument: genuine physical-boot counter advances")
	api.agentLoop.SetBootEpochStore(boot)
	return &i1R1BootFixture{api: api, ls: ls, boot: boot, oldEpoch: oldEpoch}
}

func (f *i1R1BootFixture) root(t *testing.T, state session.LifecycleState, epoch uint64) string {
	t.Helper()
	id := createTestSession(t, f.api)
	require.NoError(t, f.ls.Persist(&session.LifecycleRecord{SessionID: id, Generation: 1, State: state, Origin: &session.Origin{Kind: session.OriginKindChat}, AgentID: "mia", WorkspaceID: "ws", OwnerScopeKind: session.OwnerScopeHuman, ExecutionID: &session.ExecutionIdentity{RunID: "i1-saved-" + id, BootSeq: epoch}}))
	return id
}

func (f *i1R1BootFixture) recovery() *agent.SteerBootRecovery {
	return &agent.SteerBootRecovery{Lifecycle: f.ls, Sessions: f.api.agentLoop.GetSessionStore(), Inbox: f.api.agentLoop.GetMessageInboxStore(), Classifier: agent.NewSteerRecordClassifier(f.ls, f.api.agentLoop.GetSessionStore()), BootEpoch: f.boot, OperatorNotice: func(message string) { f.notices = append(f.notices, message) }}
}

func (f *i1R1BootFixture) journal(t *testing.T, id string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.ls.Dir(), id+".jsonl"))
	require.NoError(t, err)
	return data
}

func (f *i1R1BootFixture) display(t *testing.T, id string, want generated.SessionLifecycleState) {
	t.Helper()
	w := httptest.NewRecorder()
	f.api.HandleSessions(w, httptest.NewRequest(http.MethodGet, "/api/v1/sessions?flat=true", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var page generated.SessionPage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	found := false
	for _, s := range page.Sessions {
		if s.Id != id {
			continue
		}
		found = true
		require.NotNil(t, s.LifecycleState)
		assert.Equal(t, want, *s.LifecycleState, "a readable previous-boot dead root cannot keep showing Working")
		assert.True(t, s.LifecycleState.Valid(), "use only founder-approved generated enum values")
	}
	require.True(t, found, "original chat must remain reachable")
	w = httptest.NewRecorder()
	f.api.HandleSessions(w, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+id, nil))
	require.Equal(t, http.StatusOK, w.Code)
	var detail generated.SessionDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	require.NotNil(t, detail.Session.LifecycleState)
	assert.Equal(t, string(want), string(*detail.Session.LifecycleState), "chat detail and sidebar must agree")
	w = httptest.NewRecorder()
	f.api.HandleAgents(w, httptest.NewRequest(http.MethodGet, "/api/v1/agents/mia/sessions", nil))
	require.Equal(t, http.StatusOK, w.Code, "agent-session producer must load the same saved chat")
	var agentSessions []generated.Session
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &agentSessions))
	found = false
	for _, s := range agentSessions {
		if s.Id != id {
			continue
		}
		found = true
		require.NotNil(t, s.LifecycleState)
		assert.Equal(t, want, *s.LifecycleState, "agent-session list must pass the current boot epoch too")
	}
	require.True(t, found, "agent-session list must include this actual saved row")
}

// The unit remains real; this adapter changes only the external filesystem
// AFTER the real classifier has read it, forcing recoverOrdinaryRoot's Load arm.
type i1R1FilesystemClassify struct {
	base  steer.RecordClassifier
	after func(string)
}

func (c i1R1FilesystemClassify) Classify(ctx context.Context, id string) (steer.Class, error) {
	class, err := c.base.Classify(ctx, id)
	if err == nil && class == steer.ClassOrdinaryRoot {
		c.after(id)
	}
	return class, err
}
