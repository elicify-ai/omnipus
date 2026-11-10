// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for #1707. Oracle: issue #1707, its dispatcher clarification, and
// contracts/openapi.yaml's agent path operations, not observed handler output.
// Agent PUT/DELETE belong only to the bare agent. Known sub-resources reject
// wrong methods with 405 and their exact contract method set in Allow; unknown
// names and undeclared descendants return 404. Existing malformed mailbox
// paths retain 400. Every refusal must leave the custom agent unchanged.
//
// GET dependencies is explicitly outside this issue; only its GET-only method
// boundary is tested. OPTIONS is separately characterized through the actual
// withAuth/handlePreflight wrapper, which answers before auth and dispatch.
package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	agentsSubPathID      = "test-agent" // buildExecutorTestAPI's persisted custom agent.
	agentsSubPathName    = "1707 must not rename the agent"
	agentsSubPathToken   = "1707-fixture-bearer-token"
	agentsSubPathOrigin  = "http://localhost:3000"
	agentsSubPathMailbox = "mailboxes/ws_1707"
)

type agentsSubPathFixture struct {
	api    *restAPI
	agent  gen.Agent
	stored *agentstore.State
}

// Each row gets a real, independently persisted, unlocked custom agent. The
// valid bare-agent mutation controls prove that neither auth nor revision
// validation can mask a destructive fall-through in the negative rows.
func agentsSubPathNewFixture(t *testing.T) *agentsSubPathFixture {
	t.Helper()
	api := buildExecutorTestAPI(t)
	t.Setenv("OMNIPUS_BEARER_TOKEN", agentsSubPathToken)
	api.allowedOrigin = agentsSubPathOrigin
	// Match the real disk-backed config before taking the baseline. The
	// shared fixture's hand-built defaults otherwise differ after a reload.
	require.NoError(t, api.refreshConfigAndRewireServices(api.configPath()))
	// Delete refreshes the roster through the gateway reload callback. Wire
	// the real refresh, as TestHandleAgentsDelete_OK does, rather than leaving
	// a stale in-memory agent after its entity record has been removed.
	api.agentLoop.SetReloadFunc(func() error {
		defer api.agentLoop.ClearReloadPending()
		return api.refreshConfigAndRewireServices(api.configPath())
	})

	rec := agentsSubPathDo(api, http.MethodGet, "", nil, "")
	require.Equal(t, http.StatusOK, rec.Code, "SETUP: bare custom agent GET: %s", rec.Body.String())
	before := decodeAgentResp(t, rec.Body.Bytes())
	require.Equal(t, agentsSubPathID, before.Id)
	require.Equal(t, "Test Agent", before.Name)
	require.False(t, before.Locked, "a locked agent would hide the wrongful-delete bug")

	stored, err := agentstore.New(api.homePath).ReadState(agentsSubPathID)
	require.NoError(t, err)
	require.Equal(t, config.AgentTypeCustom, stored.Agent.Type)
	require.False(t, stored.Agent.IsSystem(), "system-agent protection must not mask the bug")
	require.False(t, stored.Agent.Locked)
	require.Equal(t, stored.Revision, before.Revision, "GET must describe the seeded persisted revision")
	return &agentsSubPathFixture{api: api, agent: before, stored: stored}
}

// Exercise the registered wrapper composition, not the child handlers. OPTIONS
// deliberately has no Authorization, proving preflight precedes authentication.
func agentsSubPathDo(api *restAPI, method, subPath string, body []byte, revision string) *httptest.ResponseRecorder {
	target := "/api/v1/agents/" + agentsSubPathID
	if subPath != "" {
		target += "/" + subPath
	}
	if method == http.MethodDelete && revision != "" {
		target += "?revision=" + url.QueryEscape(revision)
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.Header.Set("Origin", agentsSubPathOrigin)
	if method == http.MethodOptions {
		req.Header.Set("Access-Control-Request-Method", http.MethodDelete)
		req.Header.Set("Access-Control-Request-Headers", "Content-Type")
	} else {
		req.Header.Set("Authorization", "Bearer "+agentsSubPathToken)
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	api.configSnapshotMiddleware(api.withAuth(api.HandleAgents)).ServeHTTP(rec, req)
	return rec
}

func agentsSubPathMutationBody(t *testing.T, revision string) []byte {
	t.Helper()
	name := agentsSubPathName
	body, err := json.Marshal(gen.AgentUpdateRequest{Revision: revision, Name: &name})
	require.NoError(t, err)
	return body
}

// Non-fatal status assertions in the caller must not short-circuit this read-
// back: the RED proof needs to show the rename/deletion, not only a wrong code.
func agentsSubPathAssertAgentUnchanged(t *testing.T, fixture *agentsSubPathFixture, request string) {
	t.Helper()
	rec := agentsSubPathDo(fixture.api, http.MethodGet, "", nil, "")
	if assert.Equal(t, http.StatusOK, rec.Code,
		"agent must survive %s; bare GET returned %d: %s", request, rec.Code, rec.Body.String()) {
		after := decodeAgentResp(t, rec.Body.Bytes())
		assert.Equal(t, fixture.agent.Name, after.Name, "agent must not be renamed by %s", request)
		assert.Equal(t, fixture.agent, after, "agent GET must be unchanged after %s", request)
	}
	stored, err := agentstore.New(fixture.api.homePath).ReadState(agentsSubPathID)
	if assert.NoError(t, err, "persisted agent must survive %s", request) {
		assert.Equal(t, fixture.stored, stored, "persisted agent state/revision must be unchanged after %s", request)
	}
}

// These are the six sub-resources declared by the contract, including the
// separately scoped dependencies GET. There is no implicit HEAD or OPTIONS in
// the route Allow list; OPTIONS belongs to the preflight wrapper instead.
func agentsSubPathRoutes() []struct {
	path    string
	methods []string
} {
	return []struct {
		path    string
		methods []string
	}{
		{path: "sessions", methods: []string{http.MethodGet}},
		{path: "activity-runs", methods: []string{http.MethodGet}},
		{path: "runner/test", methods: []string{http.MethodPost}},
		{path: "tools", methods: []string{http.MethodGet, http.MethodPut}},
		{path: agentsSubPathMailbox, methods: []string{http.MethodGet, http.MethodPut, http.MethodDelete}},
		{path: "dependencies", methods: []string{http.MethodGet}},
	}
}

func agentsSubPathNonPreflightMethods() []string {
	return []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead}
}

// Segment, case, extension and incomplete-address equivalence classes come
// from the dispatch's unknown-route rule and its clarified descendant rule.
func agentsSubPathUnknownPaths() []string {
	return []string{
		"unknown", "sessionsfoo", "Sessions", "sessions.json", "sessions/extra", "sessions/a/b",
		"activity-runsfoo", "Activity-runs", "activity-runs.json", "activity-runs/extra", "activity-runs/a/b",
		"runner", "runner/testfoo", "Runner/test", "runner/Test", "runner/test.json", "runner/test/extra", "runner/test/a/b",
		"toolsfoo", "Tools", "tools.json", "tools/extra", "tools/a/b",
		"mailboxesfoo/ws_1707", "Mailboxes/ws_1707", "mailboxes.json/ws_1707",
		"dependenciesfoo", "Dependencies", "dependencies.json", "dependencies/extra", "dependencies/a/b",
		"x/sessions", "unknown/tools", "x/mailboxes/ws_1707",
	}
}

func agentsSubPathMalformedMailboxPaths() []string {
	// Dispatcher Q1 preserves the existing mailbox missing-segment,
	// extra-segment and invalid-workspace-ID 400 behaviour, not a blanket 404.
	return []string{"mailboxes", agentsSubPathMailbox + "/extra", "mailboxes/.."}
}

func agentsSubPathRefusal(t *testing.T, method, subPath string, status int, allow []string) {
	t.Helper()
	fixture := agentsSubPathNewFixture(t)
	var body []byte
	if method == http.MethodPut || method == http.MethodPost || method == http.MethodPatch {
		body = agentsSubPathMutationBody(t, fixture.agent.Revision)
	}
	rec := agentsSubPathDo(fixture.api, method, subPath, body, fixture.agent.Revision)
	assert.Equal(t, status, rec.Code, "%s %s: body=%s", method, subPath, rec.Body.String())
	if status == http.StatusMethodNotAllowed {
		// HTTP allows list order/whitespace variations, not extra or duplicate
		// methods. Check every header field value, not substring membership.
		got := strings.Split(strings.Join(rec.Header().Values("Allow"), ","), ",")
		for i := range got {
			got[i] = strings.TrimSpace(got[i])
		}
		assert.ElementsMatch(t, allow, got, "%s %s: Allow must list exactly the contract methods", method, subPath)
	}
	agentsSubPathAssertAgentUnchanged(t, fixture, method+" "+subPath)
}

func TestAgentsSubPathKnownWrongMethodsPreserveAgent(t *testing.T) {
	for _, route := range agentsSubPathRoutes() {
		for _, method := range agentsSubPathNonPreflightMethods() {
			if slices.Contains(route.methods, method) {
				continue // Served operations have separate controls; GET dependencies is expressly out of scope.
			}
			t.Run(method+"/"+route.path, func(t *testing.T) {
				agentsSubPathRefusal(t, method, route.path, http.StatusMethodNotAllowed, route.methods)
			})
		}
	}
}

func TestAgentsSubPathUnknownAndDescendantsPreserveAgent(t *testing.T) {
	for _, subPath := range agentsSubPathUnknownPaths() {
		for _, method := range agentsSubPathNonPreflightMethods() {
			t.Run(method+"/"+subPath, func(t *testing.T) {
				agentsSubPathRefusal(t, method, subPath, http.StatusNotFound, nil)
			})
		}
	}
}

func TestAgentsSubPathMalformedMailboxPreservesAgent(t *testing.T) {
	for _, subPath := range agentsSubPathMalformedMailboxPaths() {
		for _, method := range agentsSubPathNonPreflightMethods() {
			t.Run(method+"/"+subPath, func(t *testing.T) {
				agentsSubPathRefusal(t, method, subPath, http.StatusBadRequest, nil)
			})
		}
	}
}

// Characterization requested by the dispatch: the wrapper answers OPTIONS
// globally, even on unknown and malformed paths; it does not consult Allow.
func TestAgentsSubPathOptionsUsesPreflightAndPreservesAgent(t *testing.T) {
	paths := append(agentsSubPathUnknownPaths(), agentsSubPathMalformedMailboxPaths()...)
	paths = append(paths, "") // The bare-agent route also sits behind this wrapper.
	for _, route := range agentsSubPathRoutes() {
		paths = append(paths, route.path)
	}
	for _, subPath := range paths {
		t.Run("OPTIONS/"+subPath, func(t *testing.T) {
			fixture := agentsSubPathNewFixture(t)
			rec := agentsSubPathDo(fixture.api, http.MethodOptions, subPath, nil, "")
			assert.Equal(t, http.StatusNoContent, rec.Code, "preflight body=%s", rec.Body.String())
			assert.Equal(t, agentsSubPathOrigin, rec.Header().Get("Access-Control-Allow-Origin"))
			assert.Equal(t, "GET, POST, PUT, DELETE, OPTIONS", rec.Header().Get("Access-Control-Allow-Methods"))
			assert.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Credentials"))
			assert.Empty(t, rec.Header().Values("Allow"), "preflight must not advertise a child route's Allow")
			assert.Empty(t, rec.Body.String(), "204 preflight has no response body")
			agentsSubPathAssertAgentUnchanged(t, fixture, "OPTIONS "+subPath)
		})
	}
}

func TestAgentsSubPathBareCRUDControls(t *testing.T) {
	t.Run("GET bare agent returns the addressed custom agent", func(t *testing.T) {
		fixture := agentsSubPathNewFixture(t)
		agentsSubPathAssertAgentUnchanged(t, fixture, "bare GET control")
	})
	t.Run("PUT bare agent really renames the persisted agent", func(t *testing.T) {
		fixture := agentsSubPathNewFixture(t)
		body := agentsSubPathMutationBody(t, fixture.agent.Revision)
		rec := agentsSubPathDo(fixture.api, http.MethodPut, "", body, "")
		require.Equal(t, http.StatusOK, rec.Code, "bare PUT control: %s", rec.Body.String())
		assert.Equal(t, agentsSubPathName, decodeAgentResp(t, rec.Body.Bytes()).Name)
		after := agentsSubPathDo(fixture.api, http.MethodGet, "", nil, "")
		require.Equal(t, http.StatusOK, after.Code, "bare GET after successful PUT: %s", after.Body.String())
		assert.Equal(t, agentsSubPathName, decodeAgentResp(t, after.Body.Bytes()).Name)
		stored, err := agentstore.New(fixture.api.homePath).ReadState(agentsSubPathID)
		require.NoError(t, err)
		assert.Equal(t, agentsSubPathName, stored.Agent.Name, "control must prove the exact name persisted")
		assert.NotEqual(t, fixture.stored.Revision, stored.Revision, "successful rename must advance the revision")
	})
	t.Run("DELETE bare agent really removes the persisted agent", func(t *testing.T) {
		fixture := agentsSubPathNewFixture(t)
		rec := agentsSubPathDo(fixture.api, http.MethodDelete, "", nil, fixture.agent.Revision)
		require.Equal(t, http.StatusOK, rec.Code, "bare DELETE control: %s", rec.Body.String())
		after := agentsSubPathDo(fixture.api, http.MethodGet, "", nil, "")
		assert.Equal(t, http.StatusNotFound, after.Code, "deleted agent GET: %s", after.Body.String())
		_, err := agentstore.New(fixture.api.homePath).ReadState(agentsSubPathID)
		assert.ErrorIs(t, err, entity.ErrNotFound, "control must prove the stored agent was actually removed")
	})
}

func TestAgentsSubPathReadAndRunnerControls(t *testing.T) {
	for _, path := range []string{"sessions", "activity-runs"} {
		t.Run("GET/"+path, func(t *testing.T) {
			fixture := agentsSubPathNewFixture(t)
			rec := agentsSubPathDo(fixture.api, http.MethodGet, path, nil, "")
			require.Equal(t, http.StatusOK, rec.Code, "%s body=%s", path, rec.Body.String())
			assert.JSONEq(t, "[]", rec.Body.String(), "empty fixture must return a resource array, not agent detail")
			agentsSubPathAssertAgentUnchanged(t, fixture, "GET "+path)
		})
	}
	t.Run("POST runner/test reaches the native-runner health check", func(t *testing.T) {
		fixture := agentsSubPathNewFixture(t)
		rec := agentsSubPathDo(fixture.api, http.MethodPost, "runner/test", nil, "")
		require.Equal(t, http.StatusOK, rec.Code, "runner/test body=%s", rec.Body.String())
		var result gen.RunnerTestResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
		assert.False(t, result.Ok)
		assert.Equal(t, gen.RunnerTestResponseReasonNotExternalCli, result.Reason,
			"contract: a native agent has no external CLI runner to test")
		agentsSubPathAssertAgentUnchanged(t, fixture, "POST runner/test")
	})
}

func TestAgentsSubPathToolsGetPutControl(t *testing.T) {
	fixture := agentsSubPathNewFixture(t)
	rec := agentsSubPathDo(fixture.api, http.MethodGet, "tools", nil, "")
	require.Equal(t, http.StatusOK, rec.Code, "tools GET body=%s", rec.Body.String())
	var before gen.AgentToolsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &before))
	require.NotNil(t, before.Config.Builtin, "tools route must return tool configuration, not whole-agent detail")
	assert.Equal(t, fixture.agent.Revision, before.Revision)
	agentsSubPathAssertAgentUnchanged(t, fixture, "GET tools")

	// The tools contract accepts the GET config wrapper as a replacement.
	// Convert via the generated write type to omit read-only response fields.
	readBody, err := json.Marshal(before)
	require.NoError(t, err)
	var replacement gen.AgentToolsUpdateRequest
	require.NoError(t, json.Unmarshal(readBody, &replacement))
	body, err := json.Marshal(replacement)
	require.NoError(t, err)
	put := agentsSubPathDo(fixture.api, http.MethodPut, "tools", body, "")
	require.Equal(t, http.StatusOK, put.Code, "tools PUT body=%s", put.Body.String())
	after := agentsSubPathDo(fixture.api, http.MethodGet, "tools", nil, "")
	require.Equal(t, http.StatusOK, after.Code, "tools GET after PUT body=%s", after.Body.String())
	var got gen.AgentToolsResponse
	require.NoError(t, json.Unmarshal(after.Body.Bytes(), &got))
	assert.Equal(t, before.Config, got.Config, "supported tools PUT must round-trip the supplied configuration")
	assert.Equal(t, before.OverrideNames, got.OverrideNames)
	agentGET := agentsSubPathDo(fixture.api, http.MethodGet, "", nil, "")
	require.Equal(t, http.StatusOK, agentGET.Code, "tools PUT must not delete the agent")
	assert.Equal(t, fixture.agent.Name, decodeAgentResp(t, agentGET.Body.Bytes()).Name)
}

func TestAgentsSubPathMailboxGetPutDeleteControl(t *testing.T) {
	fixture := agentsSubPathNewFixture(t)
	fixture.api.credStore = newUnlockedStore(t, fixture.api.homePath)
	seedWorkspaceFileWithTeam(t, fixture.api.homePath, "ws_1707", []string{agentsSubPathID})
	request := gen.MailboxConfigureRequest{
		Enabled: false, ImapHost: "imap.example.invalid", SmtpHost: "smtp.example.invalid", Username: "1707@example.invalid",
	}
	body, err := json.Marshal(request)
	require.NoError(t, err)
	put := agentsSubPathDo(fixture.api, http.MethodPut, agentsSubPathMailbox, body, "")
	require.Equal(t, http.StatusOK, put.Code, "mailbox PUT body=%s", put.Body.String())
	get := agentsSubPathDo(fixture.api, http.MethodGet, agentsSubPathMailbox, nil, "")
	require.Equal(t, http.StatusOK, get.Code, "mailbox GET body=%s", get.Body.String())
	var mailbox gen.Mailbox
	require.NoError(t, json.Unmarshal(get.Body.Bytes(), &mailbox))
	assert.Equal(t, agentsSubPathID, mailbox.AgentId)
	assert.Equal(t, "ws_1707", mailbox.WorkspaceId)
	assert.False(t, mailbox.Enabled, "disabled fixture must not connect to an external mail server")
	assert.Equal(t, &request.ImapHost, mailbox.ImapHost)
	assert.Equal(t, &request.SmtpHost, mailbox.SmtpHost)
	assert.Equal(t, &request.Username, mailbox.Username)
	agentsSubPathAssertAgentUnchanged(t, fixture, "mailbox PUT and GET")

	deleted := agentsSubPathDo(fixture.api, http.MethodDelete, agentsSubPathMailbox, nil, fixture.agent.Revision)
	require.Equal(t, http.StatusOK, deleted.Code, "mailbox DELETE body=%s", deleted.Body.String())
	after := agentsSubPathDo(fixture.api, http.MethodGet, agentsSubPathMailbox, nil, "")
	assert.Equal(t, http.StatusNotFound, after.Code, "deleted mailbox GET body=%s", after.Body.String())
	agentsSubPathAssertAgentUnchanged(t, fixture, "DELETE mailbox: only the mailbox may be deleted")
}
