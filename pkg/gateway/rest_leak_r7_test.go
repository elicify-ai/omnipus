// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/health"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// Round 7 (REVIEW-mainfix-r6): the remaining client-visible causes. Same rule as
// rest_leak_r5_test.go - fixed text to the client, the cause to the log; an
// agent id or name is allowed, a session id or a storage file name is not.

// F1: the marker file of a readable knowledge-base folder is unreadable or
// malformed - the HTTP 200 detection diagnostic is fixed text.
func TestR7_F1_KnowledgeMarkerFailureIsFixedText(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Vault")
	require.NoError(t, os.WriteFile(knowledge.MarkerPath(vault), []byte("{not json"), 0o600))

	w := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge?path=vault")

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "marker_unreadable", "the typed diagnostic stays: %s", w.Body.String())
	r5AssertClean(t, w.Body.String(), vault, api.homePath, "vault.json", ".omnipus-vault", "invalid character")
}

// F1: /health serves the reload-degraded reason without a session; it carries
// fixed text, not the reload error.
func TestR7_F1_HealthReloadDegradedReasonIsFixedText(t *testing.T) {
	svc := &services{}
	svc.markReloadDegraded(errors.New("config reload aborted: reading config: open /secret/home/config.json: permission denied"))
	hs := health.NewServer("127.0.0.1", 0)
	hs.SetDegradedFunc(svc.reloadDegradedStatus)
	mux := http.NewServeMux()
	hs.RegisterOnMux(mux)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

	require.Equal(t, http.StatusServiceUnavailable, w.Code, "the degraded state itself stays: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "config reload failed")
	r5AssertClean(t, w.Body.String(), "secret", "reading config")
}

// F1: a failed browser attach is reported as classified fixed text.
func TestR7_F1_BrowserAttachFailureMessageIsFixedText(t *testing.T) {
	for name, cause := range map[string]error{
		"path and OS error": errors.New("browser: profile launch lock /secret/home/browser/profile/.lock: permission denied"),
		"timeout":           errors.New("capture: timed out after 20s waiting for /secret/home/tab"),
	} {
		t.Run(name, func(t *testing.T) {
			msg := browserAttachFailureMessage(cause)
			assert.Contains(t, msg, "browser_attach failed", "the failure is still reported")
			r5AssertClean(t, msg, "secret", ".lock", "profile")
		})
	}
	assert.Contains(t, browserAttachFailureMessage(context.DeadlineExceeded), "did not respond in time",
		"a timeout keeps its class")
}

// F1: a local model endpoint that is down surfaces a fixed warning on the
// provider row; the row and the operator's slugs are kept.
func TestR7_F1_LocalProviderWarningIsFixedText(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	base := down.URL
	down.Close() // the dial now fails with the address in its error
	api := newTestRestAPIWithHome(t)
	api.providerCatalog = freshCatalog(t)
	seedTemplateProviders(t, api, &config.ModelConfig{
		Provider: "ollama", Model: "llama3:latest", APIBase: base + "/v1", Models: []string{"llama3:latest"},
	})

	provs := getProviders(t, api)

	require.Len(t, provs, 1)
	require.NotNil(t, provs[0].Warning)
	assert.Contains(t, *provs[0].Warning, "could not fetch upstream model list")
	assert.Equal(t, []string{"llama3:latest"}, provs[0].Models)
	raw, _ := json.Marshal(provs[0])
	r5AssertClean(t, string(raw), "127.0.0.1", "dial", "connection refused", strings.TrimPrefix(base, "http://"))
}

// F3: a plan member whose parent plan cannot be READ is not a plan-state
// conflict: the client gets a fixed server failure, not the wrapped read error.
func TestR7_F3_UnreadablePlanIsAFixedServerFailure(t *testing.T) {
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"mia"})
	planStore := wirePlanStore(t, api)
	p := makeTestPlan(t, planStore, wsID, plan.StateDraft)
	tsk := assignPlanMemberTask(t, api, "R7UnreadablePlanTask", wsID, p.ID)
	planFile := filepath.Join(api.homePath, "plans", p.ID+".json")
	if _, err := os.Stat(planFile); err != nil {
		matches, _ := filepath.Glob(filepath.Join(api.homePath, "plans", "*"+p.ID+"*"))
		require.NotEmpty(t, matches, "instrument check: the plan's stored file must exist")
		planFile = matches[0]
	}
	r5Block(t, planFile, 0o000)

	w := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)

	assert.Equal(t, http.StatusInternalServerError, w.Code, "a read failure is not a 409: %s", w.Body.String())
	r5AssertClean(t, w.Body.String(), p.ID, "plans", api.homePath)
}

// F3 control: a genuine plan-state conflict stays a 409 with its domain text.
func TestR7_F3_DraftPlanConflictStays409(t *testing.T) {
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"mia"})
	planStore := wirePlanStore(t, api)
	p := makeTestPlan(t, planStore, wsID, plan.StateDraft)
	tsk := assignPlanMemberTask(t, api, "R7DraftPlanTask", wsID, p.ID)

	w := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)

	assert.Equal(t, http.StatusConflict, w.Code, w.Body.String())
}

// F3: the warning for a mount that CONTAINS the server's data directory keeps
// the risk but does not print the resolved server home.
func TestR7_F3_BroadMountWarningDoesNotPrintServerHome(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	id := createWorkspaceViaAPI(t, api, "R7MountWarn", "")
	w := postMount(t, api, id, "broad-mount", filepath.Dir(api.homePath))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var resp gen.WorkspaceMountCreateResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.Warning, "the broad-mount risk warning stays")
	assert.Contains(t, *resp.Warning, "writable by any agent on this workspace")
	resolvedHome, err := filepath.EvalSymlinks(api.homePath)
	require.NoError(t, err)
	assert.NotContains(t, *resp.Warning, api.homePath)
	assert.NotContains(t, *resp.Warning, resolvedHome)
}

// F4: a delete refused because Stop could not account for every session lists
// counts, not session ids.
func TestR7_F4_DeleteRefusalDoesNotPrintSessionIDs(t *testing.T) {
	env := u1NewEnv(t, false)
	id := sf1NewChat(t, env)
	const helper = "session_r7unreachablehelper01"
	env.api.stopSession = func(context.Context, agent.StopRequest) (agent.StopResult, error) {
		return agent.StopResult{Report: steer.CancelReport{
			Unreachable: []steer.UnreachableSession{{ID: helper, Reason: "unreadable"}},
		}}, nil
	}

	code, body := sf1Do(env, http.MethodDelete, "/api/v1/sessions/"+id, "")

	assert.Equal(t, http.StatusInternalServerError, code, body)
	assert.Contains(t, body, "nothing was deleted")
	assert.Contains(t, body, "1 session(s) still running", "the count stays")
	assert.NotContains(t, body, helper)
	assert.NotContains(t, body, id)
	_, err := os.Stat(filepath.Join(env.store(t).BaseDir(), id))
	assert.NoError(t, err, "the delete is still refused: the session folder is still there")
}

// F4: the second incomplete-Stop branch - a turn that is still live after the
// forced Stop - also reports a count. A real stuck turn (the stubborn provider
// of the abandoned-cancel test) keeps the session live.
func TestR7_F4_DeleteWithStuckTurnDoesNotPrintSessionIDs(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	workspaceDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	sp := newStubbornProvider(30 * time.Second)
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 18804, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: workspaceDir, DefaultModel: config.DefaultModel{Model: "stubborn-provider"}, MaxTokens: 4096},
			List:     []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{Mode: config.SandboxModeOff},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, sp)
	ctx, cancelCtx := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() { defer close(runDone); _ = al.Run(ctx) }()
	t.Cleanup(func() {
		sp.Shutdown()
		cancelCtx()
		select {
		case <-runDone:
		case <-time.After(30 * time.Second):
		}
	})
	time.Sleep(20 * time.Millisecond)
	handler := newWSHandler(msgBus, al, "")
	msgBus.SetStreamDelegate(handler)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)
	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)
	data, err := json.Marshal(wsClientFrameTestHelper{Type: "message", Content: "start stubborn turn"})
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))
	sessionID := readFrameOfType(t, conn, "session_started", 5*time.Second).SessionID
	require.NotEmpty(t, sessionID)
	select {
	case <-sp.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("the stubborn provider never entered Chat")
	}

	api := &restAPI{agentLoop: al, homePath: tmpDir}
	// The Stop itself reports success, but the turn ignores it: the
	// forced-stop wait finds the session still live.
	api.stopSession = func(context.Context, agent.StopRequest) (agent.StopResult, error) {
		return agent.StopResult{}, nil
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/"+sessionID, nil)
	r.URL.Path = "/api/v1/sessions/" + sessionID
	api.HandleSessions(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "Stop before delete incomplete")
	assert.Contains(t, w.Body.String(), "1 session(s) still running", "the count stays")
	assert.NotContains(t, w.Body.String(), sessionID)
}
