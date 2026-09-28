// Tests for the agent-picker-freshness fix (#1009)'s agent_created WS frame:
// the createAgent REST handler must broadcast a frame after a new agent is
// durably persisted, so every OTHER connected tab's Agent Picker drops its
// stale ['agents'] listing instead of waiting out the query's 30s staleTime.
// Two halves are pinned here, mirroring rest_library_change_frame_test.go's
// D-107 coverage:
//
//  1. WSHandler.broadcastAgentCreated fans the frame out to every connected
//     client (same construction + drop-counter shape as broadcastLibraryChange).
//  2. createAgent actually emits it — once per landed create, with the right
//     agent id, and NOT for a request that never reached persistence (a 422
//     validation failure created nothing).

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

func TestBroadcastAgentCreated_FanOutAndDropCounter(t *testing.T) {
	wcOK := &wsConn{sendCh: make(chan []byte, 4)}
	wcFull := &wsConn{sendCh: make(chan []byte)} // unbuffered, nobody reading → queued (#823: never dropped)
	h := &WSHandler{sessions: map[string]*wsConn{"ok": wcOK, "full": wcFull}}

	h.broadcastAgentCreated(gen.AgentCreatedFrame{AgentId: "agent-1"})

	select {
	case raw := <-wcOK.sendCh:
		var frame map[string]any
		require.NoError(t, json.Unmarshal(raw, &frame))
		assert.Equal(t, "agent_created", frame["type"])
		assert.Equal(t, "agent-1", frame["agent_id"])
	default:
		t.Fatal("connected client with buffer room never received the frame")
	}
	queued, _ := wcFull.queuedFrames()
	assert.Equal(t, 1, queued,
		"#823: a full-window client keeps the frame in its ordered queue instead of losing it")
}

// agentCreatedRecorder captures every frame the handlers emit, in order.
func agentCreatedRecorder(api *restAPI) func() []gen.AgentCreatedFrame {
	var mu sync.Mutex
	frames := make([]gen.AgentCreatedFrame, 0, 4)
	fn := func(f gen.AgentCreatedFrame) {
		mu.Lock()
		defer mu.Unlock()
		frames = append(frames, f)
	}
	api.agentCreatedBroadcast.Store(&fn)
	return func() []gen.AgentCreatedFrame {
		mu.Lock()
		defer mu.Unlock()
		out := make([]gen.AgentCreatedFrame, len(frames))
		copy(out, frames)
		return out
	}
}

func postCreateAgent(api *restAPI, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	return w
}

func TestCreateAgent_EmitsAgentCreatedFrame(t *testing.T) {
	api := buildExecutorTestAPI(t)
	frames := agentCreatedRecorder(api)

	w := postCreateAgent(api, `{"name":"Freshness Agent","type":"Main","soul":"You are a test agent."}`)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())

	created := decodeAgentResp(t, w.Body.Bytes())
	require.NotEmpty(t, created.Id)

	got := frames()
	require.Len(t, got, 1, "exactly one agent_created frame per landed create; got %+v", got)
	assert.Equal(t, created.Id, got[0].AgentId)
}

// A refused create (missing required field) must not emit — nothing was
// persisted, so there is nothing for another tab to pick up.
func TestCreateAgent_ValidationFailure_DoesNotEmitAgentCreatedFrame(t *testing.T) {
	api := buildExecutorTestAPI(t)
	frames := agentCreatedRecorder(api)

	w := postCreateAgent(api, `{"name":"","type":"Main","soul":"missing name must 422, never persist"}`)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body: %s", w.Body.String())
	assert.Empty(t, frames(), "a request that never persisted an agent must not emit agent_created")
}

// TestCreateAgent_RosterWideCoverageGapRejected_DoesNotEmitAgentCreatedFrame
// proves emitAgentCreated does not fire when createAgent is rejected by
// persistAgent's LATER config.ValidateToolPolicyCoverage guard
// (withToolPolicyCoverageGuard) rather than by an earlier gate.
// TestCreateAgent_ValidationFailure_DoesNotEmitAgentCreatedFrame above only
// proves the emit is gated on SOME rejection — the earliest possible one, a
// missing required field — so it would still pass against a mutation that
// hoisted the emit call to fire unconditionally anywhere between prepareAgent
// and persistAgent, since that earliest rejection never reaches persistAgent
// at all. This test closes that gap: the request here has a valid name/soul/
// type and carries no tools_cfg at all, so buildToolConfig's caller-side
// check never runs and cra.ac.Tools ends up fully enumerated (coreagent.
// NewCustomAgentToolsCfg()'s complete deny-seeded baseline) — the NEW agent's
// own map is never the problem. Deliberately NOT calling seedGlobalCeiling
// (see its own doc comment: "the bare fixture agent already has 88
// roster-wide coverage gaps") leaves the PRE-EXISTING fixture agent
// (01JXTESTAGENTSTARTTEST001, seeded by newTestRestAPIWithAgent with
// Tools: nil) with a roster-wide gap for every known builtin tool and no
// ceiling to fall back on — so persistAgent's withToolPolicyCoverageGuard
// rejects the create for a reason that has nothing to do with the new
// agent's own (complete) map. A hoisted-earlier emit would fire here; the
// correct, current position does not.
func TestCreateAgent_RosterWideCoverageGapRejected_DoesNotEmitAgentCreatedFrame(t *testing.T) {
	api := newTestRestAPIWithAgent(t)
	frames := agentCreatedRecorder(api)

	w := postAgent(t, api, `{"type":"Main","name":"RosterGapAgent","soul":"passes every earlier gate"}`)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"a create must be rejected when the roster-wide tool-policy coverage guard finds a gap "+
			"(from the PRE-EXISTING fixture agent, not the new one); body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "tool policy coverage incomplete",
		"the rejection must come from persistAgent's withToolPolicyCoverageGuard (the roster-wide "+
			"guard), confirming this test exercises that later gate and not an earlier one")
	assert.Empty(t, frames(),
		"a create rejected by the roster-wide coverage guard never reached persistence — "+
			"emitAgentCreated must not fire (a hoisted-emit mutation before persistAgent would fire here)")
}

// The broadcaster is nil until the gateway wires the WS handler in at boot;
// createAgent must survive that (test APIs, and any boot ordering hiccup)
// rather than nil-deref.
func TestCreateAgent_NilBroadcasterIsSafe(t *testing.T) {
	api := buildExecutorTestAPI(t) // no broadcaster installed
	w := postCreateAgent(api, `{"name":"Nil Broadcaster Agent","type":"Main","soul":"proves no panic"}`)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
}
