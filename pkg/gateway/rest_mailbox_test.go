package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMailboxTestAPI builds a restAPI with one agent ("mia"), an unlocked
// credential store, and an on-disk config.json — the harness for the M11 mailbox
// endpoints. mailboxes is agent ID → workspace ID → mailbox (pair-addressed).
func newMailboxTestAPI(t *testing.T, mailboxes map[string]map[string]config.MailboxConfig) *restAPI {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
			List: []config.AgentConfig{
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCustom, Home: tmpDir},
			},
		},
		Mailboxes: mailboxes,
	}
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"),
		[]byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{}}`), 0o600))

	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	store := newUnlockedStore(t, tmpDir)
	return &restAPI{
		agentLoop:     al,
		homePath:      tmpDir,
		allowedOrigin: "http://localhost:3000",
		credStore:     store,
	}
}

// seedWorkspaceFile writes a minimal workspace JSON file directly to
// homePath/workspaces/{id}.json — the on-disk shape setAgentMailbox's
// workspace-existence gate (a.loadWorkspace, backed by readWorkspaceFile)
// reads. Mailbox tests seed workspaces this way rather than via the full
// HandleWorkspaces POST path, which needs machinery (taskStore,
// onboardingMgr, …) the mailbox test harness does not construct.
func seedWorkspaceFile(t *testing.T, homePath, id string) {
	t.Helper()
	// ADR-033: the owning agent must be a core_team member — seed the harness
	// agent ("mia") so existing save fixtures pass the membership gate.
	seedWorkspaceFileWithTeam(t, homePath, id, []string{"mia"})
}

func seedWorkspaceFileWithTeam(t *testing.T, homePath, id string, coreTeam []string) {
	t.Helper()
	dir := filepath.Join(homePath, "workspaces")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	team, err := json.Marshal(coreTeam)
	require.NoError(t, err)
	data := fmt.Sprintf(
		`{"id":%q,"name":"Test Workspace","status":"active","core_team":%s,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`,
		id,
		team,
	)
	require.NoError(t, os.WriteFile(filepath.Join(dir, id+".json"), []byte(data), 0o600))
}

func TestSetAgentMailbox_RoutesPasswordToCredentialStore(t *testing.T) {
	api := newMailboxTestAPI(t, nil)
	seedWorkspaceFile(t, api.homePath, "ws_my")

	body := `{"enabled":true,"imap_host":"imap.x.com","smtp_host":"smtp.x.com","username":"me@x.com","password":"app-pass-123"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailboxes/ws_my", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "mia", "ws_my")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	// Response: configured=true, password never echoed.
	var resp gen.Mailbox
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Configured)
	assert.True(t, resp.Enabled)
	assert.Equal(t, "mia", resp.AgentId)
	assert.Equal(t, "ws_my", resp.WorkspaceId)
	assert.NotContains(t, w.Body.String(), "app-pass-123", "password leaked into response")

	// config.json: nested agent -> workspace -> entry, password_ref set, no inline plaintext.
	raw, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "app-pass-123", "plaintext password leaked into config.json")
	var disk map[string]any
	require.NoError(t, json.Unmarshal(raw, &disk))
	mailboxesMap, mailboxesMapOk := disk["mailboxes"].(map[string]any)
	require.True(t, mailboxesMapOk, "config.mailboxes must be an object")
	miaMap, miaMapOk := mailboxesMap["mia"].(map[string]any)
	require.True(t, miaMapOk, "config.mailboxes.mia must be an object")
	mb, mbOk := miaMap["ws_my"].(map[string]any)
	require.True(t, mbOk, "config.mailboxes.mia.ws_my must be an object")
	assert.Equal(t, "mailbox_mia_ws_my_password", mb["password_ref"])
	_, hasInline := mb["password"]
	assert.False(t, hasInline, "inline password must not be persisted")

	// Credential store: the secret is retrievable under the per-pair key.
	got, err := api.credStore.Get("mailbox_mia_ws_my_password")
	require.NoError(t, err)
	assert.Equal(t, "app-pass-123", got)
}

// TestSetAgentMailbox_UnknownAgent404 verifies the agent-existence guard at
// the top of setAgentMailbox (rest_mailbox.go). setAgentMailbox has TWO
// distinct 404 causes in sequence: (1) !a.agentExists(agentID), and (2) an
// unknown workspace via a.loadWorkspace. The original version of this test
// used workspace ID "ws" WITHOUT ever seeding a "ws" workspace file, so BOTH
// preconditions were violated simultaneously — a regression that deleted the
// agent-existence check entirely would fall through to the workspace check
// and still 404, and this test would keep passing without ever exercising
// the guard it's named for. Fixed by seeding "ws" as a real workspace (so the
// ONLY remaining failure is the unknown agent) and asserting the body names
// the agent, not the workspace.
func TestSetAgentMailbox_UnknownAgent404(t *testing.T) {
	api := newMailboxTestAPI(t, nil)
	seedWorkspaceFile(t, api.homePath, "ws")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/ghost/mailboxes/ws",
		strings.NewReader(`{"enabled":true,"imap_host":"i","smtp_host":"s","username":"u"}`))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "ghost", "ws")
	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "ghost",
		"the 404 must name the unknown agent, not an unrelated workspace-not-found rejection")
	assert.NotContains(t, w.Body.String(), "workspace",
		"with the workspace seeded, the ONLY possible 404 cause is the unknown agent")
}

func TestSetAgentMailbox_MissingRequiredField(t *testing.T) {
	api := newMailboxTestAPI(t, nil)
	seedWorkspaceFile(t, api.homePath, "ws")
	// Missing imap_host.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailboxes/ws",
		strings.NewReader(`{"enabled":true,"smtp_host":"s","username":"u"}`))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "mia", "ws")
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
}

func TestSetAgentMailbox_SecondMailboxInSameWorkspaceAllowed(t *testing.T) {
	// The 0.1.0 cap-1-per-workspace rule was removed 2026-07-03 (operator-
	// approved): a second agent configuring a mailbox in the SAME workspace
	// must succeed. Each mailbox's unhandled mail becomes Board tasks assigned
	// to its own owning agent, so multiple inboxes per workspace stay
	// unambiguous. Pre-existing mailbox for "jim" in ws_shared (in live cfg).
	api := newMailboxTestAPI(t, map[string]map[string]config.MailboxConfig{
		"jim": {
			"ws_shared": {Enabled: true, WorkspaceID: "ws_shared", IMAPHost: "i", SMTPHost: "s", Username: "jim@x.com"},
		},
	})
	seedWorkspaceFile(t, api.homePath, "ws_shared")
	body := `{"enabled":true,"imap_host":"i","smtp_host":"s","username":"mia@x.com","password":"p"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailboxes/ws_shared", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "mia", "ws_shared")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var resp gen.Mailbox
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "mia", resp.AgentId)
	assert.Equal(t, "ws_shared", resp.WorkspaceId)
}

func TestSetAgentMailbox_SameAgentTwoWorkspacesBothRetrievable(t *testing.T) {
	// Pair-addressing (2026-07-03): the same agent can hold a DIFFERENT
	// mailbox in each workspace it belongs to. Configure "mia" in two
	// workspaces and confirm both are independently retrievable with distinct
	// usernames and distinct credential-store keys.
	api := newMailboxTestAPI(t, nil)
	seedWorkspaceFile(t, api.homePath, "ws_a")
	seedWorkspaceFile(t, api.homePath, "ws_b")

	bodyA := `{"enabled":true,"imap_host":"imap.a.com","smtp_host":"smtp.a.com","username":"mia-a@x.com","password":"pass-a"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailboxes/ws_a", strings.NewReader(bodyA))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "mia", "ws_a")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	bodyB := `{"enabled":true,"imap_host":"imap.b.com","smtp_host":"smtp.b.com","username":"mia-b@x.com","password":"pass-b"}`
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailboxes/ws_b", strings.NewReader(bodyB))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "mia", "ws_b")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	// GET each pair back independently.
	w = httptest.NewRecorder()
	api.getAgentMailbox(w, "mia", "ws_a")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var respA gen.Mailbox
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &respA))
	require.NotNil(t, respA.Username)
	assert.Equal(t, "mia-a@x.com", *respA.Username)
	assert.Equal(t, "ws_a", respA.WorkspaceId)

	w = httptest.NewRecorder()
	api.getAgentMailbox(w, "mia", "ws_b")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var respB gen.Mailbox
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &respB))
	require.NotNil(t, respB.Username)
	assert.Equal(t, "mia-b@x.com", *respB.Username)
	assert.Equal(t, "ws_b", respB.WorkspaceId)

	// Distinct credential-store keys, both resolvable to their own password.
	gotA, err := api.credStore.Get("mailbox_mia_ws_a_password")
	require.NoError(t, err)
	assert.Equal(t, "pass-a", gotA)
	gotB, err := api.credStore.Get("mailbox_mia_ws_b_password")
	require.NoError(t, err)
	assert.Equal(t, "pass-b", gotB)
	assert.NotEqual(t, gotA, gotB)
}

func TestListMailboxes_EmptyAndConfigured(t *testing.T) {
	// Empty: no mailboxes configured → 200 with an empty (non-null) list —
	// this endpoint must NEVER 404, so the SPA can render mailbox status
	// without per-agent probe requests (each probe 404 lands in the browser
	// console and trips the e2e zero-console-errors gate).
	api := newMailboxTestAPI(t, nil)
	w := httptest.NewRecorder()
	api.listMailboxes(w, httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes", nil))
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var empty gen.MailboxListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &empty))
	require.NotNil(t, empty.Mailboxes)
	assert.Empty(t, empty.Mailboxes)
	assert.Contains(t, w.Body.String(), `"mailboxes":[]`, "empty list must serialize as [], not null")

	// Configured: one agent with TWO (agent, workspace) pairs → two entries,
	// both configured=true, and the secret never appears in the body.
	api = newMailboxTestAPI(t, map[string]map[string]config.MailboxConfig{
		"mia": {
			"ws_a": {
				Enabled:     true,
				WorkspaceID: "ws_a",
				IMAPHost:    "imap.a.com",
				SMTPHost:    "smtp.a.com",
				Username:    "a@x.com",
				PasswordRef: "mailbox_mia_ws_a_password",
			},
			"ws_b": {
				Enabled:     true,
				WorkspaceID: "ws_b",
				IMAPHost:    "imap.b.com",
				SMTPHost:    "smtp.b.com",
				Username:    "b@x.com",
				PasswordRef: "mailbox_mia_ws_b_password",
			},
		},
	})
	require.NoError(t, api.credStore.Set("mailbox_mia_ws_a_password", "secret-a"))
	require.NoError(t, api.credStore.Set("mailbox_mia_ws_b_password", "secret-b"))

	w = httptest.NewRecorder()
	api.listMailboxes(w, httptest.NewRequest(http.MethodGet, "/api/v1/mailboxes", nil))
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var resp gen.MailboxListResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Mailboxes, 2)
	// Deterministic order: sorted by workspace ID within the agent.
	assert.Equal(t, "mia", resp.Mailboxes[0].AgentId)
	assert.Equal(t, "ws_a", resp.Mailboxes[0].WorkspaceId)
	assert.True(t, resp.Mailboxes[0].Configured)
	assert.Equal(t, "mia", resp.Mailboxes[1].AgentId)
	assert.Equal(t, "ws_b", resp.Mailboxes[1].WorkspaceId)
	assert.True(t, resp.Mailboxes[1].Configured)
	assert.NotContains(t, w.Body.String(), "secret", "password must never be returned")

	// Non-GET → 405.
	w = httptest.NewRecorder()
	api.listMailboxes(w, httptest.NewRequest(http.MethodPost, "/api/v1/mailboxes", nil))
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestSetAgentMailbox_GrantsEmailToolAllowsForDenyDefaultAgent(t *testing.T) {
	// D19 / MC-26: mailbox configure fills email-tool policy only where the
	// key is absent. NewCustomAgentToolsCfg's denyAllThenOverride entries are
	// explicit denies, so enabling a mailbox must leave them deny. An explicit
	// ask (operator intent) still survives. The pre-D19 flip of seed deny to
	// allow is the regression this test now forbids.
	api := newMailboxTestAPI(t, nil)
	seedWorkspaceFile(t, api.homePath, "ws_my")

	// ADR-054: agents are per-entity records under entities/agents/<id>.json,
	// not config.json's agents.list — seed a real "mia" entity record via the
	// agent store with a real, fully-enumerated deny-by-default custom-agent
	// tools_cfg from the ACTUAL seed constructor — not a hand-fabricated
	// default_policy shape (that field was removed; a raw fixture using it
	// silently stopped exercising this code path with no compile or runtime
	// error, which is exactly how the original regression here went
	// undetected).
	seedPolicies := coreagent.NewCustomAgentToolsCfg().Builtin.Policies
	policies := make(map[string]config.ToolPolicy, len(seedPolicies)+2)
	for k, v := range seedPolicies {
		policies[k] = v
	}
	policies["create_task"] = config.ToolPolicyAllow
	policies["send_email"] = config.ToolPolicyAsk // explicit operator intent — must survive

	store := agentstore.New(api.homePath)
	require.NoError(t, store.Create("mia", &config.AgentConfig{
		ID:    "mia",
		Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{Policies: policies}},
	}))

	body := `{"enabled":true,"imap_host":"i","smtp_host":"s","username":"mia@x.com","password":"p"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailboxes/ws_my", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "mia", "ws_my")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	updated, err := store.Get("mia")
	require.NoError(t, err)
	updatedPolicies := updated.Tools.Builtin.Policies

	// Explicit seed denies stay denies (MC-26 / D19). They are not absent keys.
	for _, name := range []string{"read_inbox", "search_email", "read_message", "reply"} {
		assert.Equal(t, config.ToolPolicyDeny, updatedPolicies[name], "MC-26: explicit deny on %s must survive mailbox configure", name)
	}
	// …the explicit operator override survived…
	assert.Equal(t, config.ToolPolicyAsk, updatedPolicies["send_email"], "explicit operator ask must never be overridden")
	// …and unrelated entries are untouched.
	assert.Equal(t, config.ToolPolicyAllow, updatedPolicies["create_task"])
}

func TestSetAgentMailbox_DisabledSaveDoesNotGrantToolAllows(t *testing.T) {
	// grantEmailToolAllows is only called when req.Enabled is true (the wire
	// contract's "enabled" means "register the email tools" — an opt-IN). A
	// save with enabled:false must leave a deny-default agent's policies
	// completely untouched: no email-tool allows fabricated for a mailbox
	// that isn't even active.
	api := newMailboxTestAPI(t, nil)
	seedWorkspaceFile(t, api.homePath, "ws_my")

	// ADR-054: seed a real "mia" entity record via the agent store.
	store := agentstore.New(api.homePath)
	require.NoError(t, store.Create("mia", &config.AgentConfig{
		ID: "mia",
		Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{
			Policies: map[string]config.ToolPolicy{"create_task": config.ToolPolicyAllow},
		}},
	}))

	body := `{"enabled":false,"imap_host":"i","smtp_host":"s","username":"mia@x.com"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailboxes/ws_my", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "mia", "ws_my")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	updated, err := store.Get("mia")
	require.NoError(t, err)
	policies := updated.Tools.Builtin.Policies

	for _, name := range emailToolNames {
		_, exists := policies[name]
		assert.False(t, exists, "tool %s must NOT be granted when the mailbox save disables the mailbox", name)
	}
	assert.Equal(t, config.ToolPolicyAllow, policies["create_task"], "unrelated entries must be untouched")
	assert.Len(t, policies, 1, "only the pre-existing entry should remain")
}

// TestSetAgentMailbox_AlreadyEnabledEditDoesNotReGrantExplicitDeny is the
// regression test for the privilege-widening bug found alongside the
// grantEmailToolAllows dead-mailbox fix: grantEmailToolAllows used to run on
// EVERY save where req.Enabled==true, not just the disabled→enabled
// transition. Since a seed "deny" and an operator's later, deliberate "deny"
// (set via the Tool Policies UI/API to lock an email tool back down AFTER
// the mailbox was first enabled) are the same literal string in the data
// model, ANY subsequent edit to an already-enabled mailbox (e.g. rotating
// the password, changing the IMAP host) with enabled:true would silently
// re-grant "allow" to a tool the operator had deliberately locked down — the
// mirror image of the dead-mailbox bug the original fix closed.
func TestSetAgentMailbox_AlreadyEnabledEditDoesNotReGrantExplicitDeny(t *testing.T) {
	api := newMailboxTestAPI(t, nil)
	seedWorkspaceFile(t, api.homePath, "ws_my")

	// 1. ADR-054: seed a real "mia" entity record via the agent store with a
	//    real, fully-enumerated deny-by-default tools_cfg (the actual seed
	//    constructor, not a hand-fabricated shape) and enable the mailbox for
	//    the FIRST time — the disabled→enabled transition. D19/MC-26: that
	//    transition must not rewrite an explicit seed deny into allow.
	seedPolicies := coreagent.NewCustomAgentToolsCfg().Builtin.Policies
	policies := make(map[string]config.ToolPolicy, len(seedPolicies))
	for k, v := range seedPolicies {
		policies[k] = v
	}
	store := agentstore.New(api.homePath)
	require.NoError(t, store.Create("mia", &config.AgentConfig{
		ID:    "mia",
		Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{Policies: policies}},
	}))

	firstBody := `{"enabled":true,"imap_host":"i","smtp_host":"s","username":"mia@x.com","password":"p"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailboxes/ws_my", strings.NewReader(firstBody))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "mia", "ws_my")
	require.Equal(t, http.StatusOK, w.Code, "first enable body=%s", w.Body.String())

	updated, err := store.Get("mia")
	require.NoError(t, err)
	require.Equal(t, config.ToolPolicyDeny, updated.Tools.Builtin.Policies["send_email"],
		"MC-26: the disabled→enabled transition must not rewrite an explicit seed deny")

	// 2. Operator deliberately locks send_email back down to "deny" (e.g. via
	//    the Tool Policies UI/API) — simulated as a direct entity-store write,
	//    since it is indistinguishable in the data model from the seed's
	//    original "deny".
	_, err = store.Update("mia", func(a *config.AgentConfig) error {
		a.Tools.Builtin.Policies["send_email"] = config.ToolPolicyDeny
		return nil
	})
	require.NoError(t, err)

	// 3. A SECOND save where the mailbox was ALREADY enabled — only the IMAP
	//    host changes, enabled stays true throughout — must NOT re-grant
	//    send_email.
	secondBody := `{"enabled":true,"imap_host":"i2","smtp_host":"s","username":"mia@x.com"}`
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailboxes/ws_my", strings.NewReader(secondBody))
	r2.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w2, r2, "mia", "ws_my")
	require.Equal(t, http.StatusOK, w2.Code, "second (already-enabled) edit body=%s", w2.Body.String())

	updated2, err := store.Get("mia")
	require.NoError(t, err)
	policies2 := updated2.Tools.Builtin.Policies
	assert.Equal(
		t,
		config.ToolPolicyDeny,
		policies2["send_email"],
		"an edit to an ALREADY-enabled mailbox must NOT re-grant an operator's explicit deny (privilege-widening regression)",
	)
	// The other explicit seed denies stay denies too (MC-26 / D19).
	for _, name := range []string{"read_inbox", "search_email", "read_message", "reply"} {
		assert.Equal(t, config.ToolPolicyDeny, policies2[name], "MC-26: explicit deny on %s must survive a later mailbox edit", name)
	}
}

// --- Workspace-existence gate (fix 2) ---

func TestSetAgentMailbox_NonexistentWorkspace404(t *testing.T) {
	api := newMailboxTestAPI(t, nil)
	// Deliberately do NOT seed a workspace file for "ws_ghost".
	body := `{"enabled":true,"imap_host":"i","smtp_host":"s","username":"u"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailboxes/ws_ghost", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "mia", "ws_ghost")
	require.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())

	// Nothing persisted for the nonexistent workspace.
	raw, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "ws_ghost", "a mailbox must never be saved against a nonexistent workspace")
}

func TestSetAgentMailbox_ExistingWorkspace200(t *testing.T) {
	api := newMailboxTestAPI(t, nil)
	seedWorkspaceFile(t, api.homePath, "ws_real")

	body := `{"enabled":true,"imap_host":"i","smtp_host":"s","username":"u"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailboxes/ws_real", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "mia", "ws_real")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var resp gen.Mailbox
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "ws_real", resp.WorkspaceId)
}

// --- Malformed legacy/nested mailbox entry (fix 5) ---

func TestSetAgentMailbox_MalformedMixedEntry500(t *testing.T) {
	api := newMailboxTestAPI(t, nil)
	seedWorkspaceFile(t, api.homePath, "ws_my")

	// Seed a malformed agent entry directly on disk: one key holds an object
	// (nested shape), another holds a scalar (legacy shape) — ambiguous,
	// must be rejected rather than silently misclassified/dropped. Written
	// directly with os.WriteFile rather than via safeUpdateConfigJSON:
	// safeUpdateConfigJSON's OWN post-write refresh reloads config.json
	// through config.LoadConfig, which applies the exact same strict shape
	// rule (config.MailboxesConfig.UnmarshalJSON) and would itself fail on
	// this seed step — a bare os.WriteFile reproduces the on-disk corruption
	// (hand-edited config.json, or an older non-strict writer) without going
	// through that typed round-trip.
	before := `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"channels":{},` +
		`"mailboxes":{"mia":{"ws_other":{"enabled":true,"workspace_id":"ws_other"},"enabled":true}}}`
	require.NoError(t, os.WriteFile(filepath.Join(api.homePath, "config.json"), []byte(before), 0o600))

	body := `{"enabled":true,"imap_host":"i","smtp_host":"s","username":"u"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailboxes/ws_my", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "mia", "ws_my")
	require.Equal(t, http.StatusInternalServerError, w.Code, "body=%s", w.Body.String())
	var errResp gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Contains(t, errResp.Error, `mailboxes entry for agent "mia" is malformed`,
		"error must name the offending agent")

	// Nothing persisted — config.json is byte-for-byte unchanged.
	after, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	assert.Equal(t, before, string(after), "malformed entry must abort the write with nothing persisted")
}

func TestSetAgentMailbox_RejectsNonCoreTeamAgent(t *testing.T) {
	// ADR-033 (operator-decided): mailbox ownership requires core_team
	// membership in the target workspace, aligning with ADR-029 FR-006.
	api := newMailboxTestAPI(t, nil)
	seedWorkspaceFileWithTeam(t, api.homePath, "ws_team", []string{"someone-else"})

	body := `{"enabled":true,"imap_host":"i","smtp_host":"s","username":"me@x.com","password":"p"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailboxes/ws_team", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "mia", "ws_team")
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "not a member")

	// Zero side effects: no config entry, no credential.
	cfg := api.agentLoop.GetConfig()
	_, exists := cfg.Mailboxes["mia"]["ws_team"]
	assert.False(t, exists, "rejected save must not persist a mailbox")
	_, err := api.credStore.Get("mailbox_mia_ws_team_password")
	assert.Error(t, err, "rejected save must not store a credential")
}

func TestSetAgentMailbox_MemberAgentAccepted(t *testing.T) {
	api := newMailboxTestAPI(t, nil)
	seedWorkspaceFileWithTeam(t, api.homePath, "ws_team", []string{"other", "mia"})

	body := `{"enabled":true,"imap_host":"i","smtp_host":"s","username":"me@x.com","password":"p"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailboxes/ws_team", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "mia", "ws_team")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
}
