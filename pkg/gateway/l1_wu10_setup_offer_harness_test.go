package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// No invented public API or production hook: the chief's final gateway
// producer seam is checked dynamically so its absence is loud RED, not a
// compilation failure on the pinned pre-change production tree.
type l1WU10SetupProducer interface {
	postWorkspaceSetupOffer(workspaceID, user string)
}

type l1WU10SetupHarness struct {
	api       *restAPI
	loop      *agent.AgentLoop
	bus       *bus.MessageBus
	store     *session.UnifiedStore
	provider  *testutil.ScenarioProvider
	server    *httptest.Server
	completed chan struct{}
	defaultID string
	mainID    string
	miaMainID string
}

func newL1WU10SetupHarness(t *testing.T) *l1WU10SetupHarness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = home
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	cfg.Gateway.DevModeBypass = true
	coreagent.SeedConfig(cfg)
	require.Empty(t, repairAndValidateToolPolicyCoverage(cfg))
	seedAgentEntities(t, home, cfg.Agents.List)
	data, encodeErr := json.Marshal(cfg)
	require.NoError(t, encodeErr)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), data, 0o600))
	require.NoError(t, ensureDefaultWorkspace(home, "kickoff-test-user", cfg))
	defaultID, defaultErr := workspace.ResolveDefaultID(home)
	require.NoError(t, defaultErr)
	msgBus := bus.NewMessageBus()
	provider := testutil.NewScenario()
	loop, loopErr := agent.NewAgentLoop(cfg, msgBus, provider)
	require.NoError(t, loopErr)
	t.Cleanup(loop.Close)
	store := loop.GetSessionStore()
	require.NotNil(t, store)
	main, mainErr := store.GetOrCreateMainSession(defaultID, "omnipus")
	require.NoError(t, mainErr)
	mia, miaErr := store.GetOrCreateMainSession(defaultID, "mia")
	require.NoError(t, miaErr)
	for _, pair := range []struct{ id, agentID string }{{main.ID, "omnipus"}, {mia.ID, "mia"}} {
		require.NoError(t, store.AppendTranscript(pair.id, session.TranscriptEntry{
			ID: "prior-" + pair.agentID, Role: "user", Type: session.EntryTypeMessage,
			AgentID: pair.agentID, Content: "existing conversation before workspace creation", Timestamp: time.Now().UTC(),
		}))
	}
	api := &restAPI{agentLoop: loop, homePath: home}
	h := &l1WU10SetupHarness{api: api, loop: loop, bus: msgBus, store: store, provider: provider,
		completed: make(chan struct{}, 32), defaultID: defaultID, mainID: main.ID, miaMainID: mia.ID}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.HandleWorkspaces(w, withAdminRole(r))
		h.completed <- struct{}{}
	}))
	t.Cleanup(h.server.Close)
	// The actual bus and already-written transcript are positive instruments,
	// not mocked producer output. The loop is not running during bus capture.
	require.NoError(t, msgBus.PublishInbound(context.Background(), bus.InboundMessage{Content: "wu10-bus-instrument"}))
	control := awaitInboundMessage(t, msgBus, "WU-10 actual bus instrument")
	require.Equal(t, "wu10-bus-instrument", control.Content)
	prior, priorErr := store.ReadTranscript(main.ID)
	require.NoError(t, priorErr)
	require.Len(t, prior, 1)
	require.Equal(t, "prior-omnipus", prior[0].ID)
	return h
}

func (h *l1WU10SetupHarness) requireProducer(t *testing.T) l1WU10SetupProducer {
	t.Helper()
	producer, present := any(h.api).(l1WU10SetupProducer)
	if !present {
		t.Fatal("BLOCKED: server postWorkspaceSetupOffer producer not implemented — required by FR-047 / OSA-9 W8 and the chief's final WU-10 seam")
	}
	return producer
}

func (h *l1WU10SetupHarness) post(t *testing.T, req gen.WorkspaceCreateRequest) gen.Workspace {
	t.Helper()
	data, encodeErr := json.Marshal(req)
	require.NoError(t, encodeErr)
	response, callErr := h.server.Client().Post(h.server.URL+"/api/v1/workspaces", "application/json", bytes.NewReader(data))
	require.NoError(t, callErr)
	defer response.Body.Close()
	require.Equal(t, http.StatusCreated, response.StatusCode, "actual REST create must return 201")
	var created gen.Workspace
	require.NoError(t, json.NewDecoder(response.Body).Decode(&created))
	require.NotEmpty(t, created.Id)
	assert.Equal(t, req.Name, created.Name)
	return created
}

func (h *l1WU10SetupHarness) awaitHandler(t *testing.T) {
	t.Helper()
	select {
	case <-h.completed:
	case <-time.After(busDeliveryTimeout):
		t.Fatal("REST handler did not complete; this is an execution/fixture failure, not kickoff RED")
	}
}

func (h *l1WU10SetupHarness) get(t *testing.T, id string) gen.Workspace {
	t.Helper()
	response, callErr := h.server.Client().Get(h.server.URL + "/api/v1/workspaces/" + id)
	require.NoError(t, callErr)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var got gen.Workspace
	require.NoError(t, json.NewDecoder(response.Body).Decode(&got))
	h.awaitHandler(t)
	return got
}

func (h *l1WU10SetupHarness) awaitOffer(t *testing.T, targetID string) bus.InboundMessage {
	t.Helper()
	h.requireProducer(t)
	select {
	case msg := <-h.bus.InboundChan():
		assert.Equal(t, h.mainID, msg.SessionID)
		assert.Equal(t, "1", msg.Metadata["workspace_setup_offer"])
		assert.Equal(t, targetID, msg.Metadata["setup_target_workspace_id"])
		return msg
	case <-time.After(busDeliveryTimeout):
		t.Fatal("FR-047: the real server producer delivered no setup offer to the existing Omnipus main")
		return bus.InboundMessage{}
	}
}

func (h *l1WU10SetupHarness) assertNoFurtherWake(t *testing.T) {
	t.Helper()
	select {
	case msg := <-h.bus.InboundChan():
		t.Errorf("no extra setup/ordinary wake may remain after the completed producer: %+v", msg)
	default:
	}
}

func (h *l1WU10SetupHarness) assertMainAnchor(t *testing.T) {
	t.Helper()
	meta, metaErr := h.store.GetMeta(h.mainID)
	require.NoError(t, metaErr)
	assert.Equal(t, "omnipus", meta.AgentID)
	assert.Equal(t, h.defaultID, meta.WorkspaceID, "OSA-9.1: configuring a foreign workspace never moves the Omnipus main")
	assert.Equal(t, session.SessionTypeMain, meta.Type)
	computed, computeErr := session.MainSessionID(h.defaultID, "omnipus")
	require.NoError(t, computeErr)
	assert.Equal(t, computed, h.mainID)
}

func l1WU10Pending(w gen.Workspace) bool { return w.SetupPending != nil && *w.SetupPending }
