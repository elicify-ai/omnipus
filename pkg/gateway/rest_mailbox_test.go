//go:build !cgo

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
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
			Defaults: config.AgentDefaults{Workspace: tmpDir, ModelName: "test-model", MaxTokens: 4096},
			List: []config.AgentConfig{
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCustom, Workspace: tmpDir},
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

func TestSetAgentMailbox_RoutesPasswordToCredentialStore(t *testing.T) {
	api := newMailboxTestAPI(t, nil)

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
	require.NotNil(t, resp.WorkspaceId)
	assert.Equal(t, "ws_my", *resp.WorkspaceId)
	assert.NotContains(t, w.Body.String(), "app-pass-123", "password leaked into response")

	// config.json: nested agent -> workspace -> entry, password_ref set, no inline plaintext.
	raw, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "app-pass-123", "plaintext password leaked into config.json")
	var disk map[string]any
	require.NoError(t, json.Unmarshal(raw, &disk))
	mb := disk["mailboxes"].(map[string]any)["mia"].(map[string]any)["ws_my"].(map[string]any)
	assert.Equal(t, "mailbox_mia_ws_my_password", mb["password_ref"])
	_, hasInline := mb["password"]
	assert.False(t, hasInline, "inline password must not be persisted")

	// Credential store: the secret is retrievable under the per-pair key.
	got, err := api.credStore.Get("mailbox_mia_ws_my_password")
	require.NoError(t, err)
	assert.Equal(t, "app-pass-123", got)
}

func TestSetAgentMailbox_UnknownAgent404(t *testing.T) {
	api := newMailboxTestAPI(t, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/ghost/mailboxes/ws",
		strings.NewReader(`{"enabled":true,"imap_host":"i","smtp_host":"s","username":"u"}`))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "ghost", "ws")
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestSetAgentMailbox_MissingRequiredField(t *testing.T) {
	api := newMailboxTestAPI(t, nil)
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
		"jim": {"ws_shared": {Enabled: true, WorkspaceID: "ws_shared", IMAPHost: "i", SMTPHost: "s", Username: "jim@x.com"}},
	})
	body := `{"enabled":true,"imap_host":"i","smtp_host":"s","username":"mia@x.com","password":"p"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailboxes/ws_shared", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "mia", "ws_shared")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var resp gen.Mailbox
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "mia", resp.AgentId)
	require.NotNil(t, resp.WorkspaceId)
	assert.Equal(t, "ws_shared", *resp.WorkspaceId)
}

func TestSetAgentMailbox_SameAgentTwoWorkspacesBothRetrievable(t *testing.T) {
	// Pair-addressing (2026-07-03): the same agent can hold a DIFFERENT
	// mailbox in each workspace it belongs to. Configure "mia" in two
	// workspaces and confirm both are independently retrievable with distinct
	// usernames and distinct credential-store keys.
	api := newMailboxTestAPI(t, nil)

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
	require.NotNil(t, respA.WorkspaceId)
	assert.Equal(t, "ws_a", *respA.WorkspaceId)

	w = httptest.NewRecorder()
	api.getAgentMailbox(w, "mia", "ws_b")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var respB gen.Mailbox
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &respB))
	require.NotNil(t, respB.Username)
	assert.Equal(t, "mia-b@x.com", *respB.Username)
	require.NotNil(t, respB.WorkspaceId)
	assert.Equal(t, "ws_b", *respB.WorkspaceId)

	// Distinct credential-store keys, both resolvable to their own password.
	gotA, err := api.credStore.Get("mailbox_mia_ws_a_password")
	require.NoError(t, err)
	assert.Equal(t, "pass-a", gotA)
	gotB, err := api.credStore.Get("mailbox_mia_ws_b_password")
	require.NoError(t, err)
	assert.Equal(t, "pass-b", gotB)
	assert.NotEqual(t, gotA, gotB)
}

func TestGetAgentMailbox_NotConfigured404(t *testing.T) {
	api := newMailboxTestAPI(t, nil)
	w := httptest.NewRecorder()
	api.getAgentMailbox(w, "mia", "ws_my")
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetAgentMailbox_WrongWorkspace404(t *testing.T) {
	// The agent has a mailbox in ws_my, but requesting it under a different
	// workspace must 404 — the pair, not just the agent, must match.
	api := newMailboxTestAPI(t, map[string]map[string]config.MailboxConfig{
		"mia": {"ws_my": {
			Enabled: true, WorkspaceID: "ws_my", IMAPHost: "imap.x.com",
			SMTPHost: "smtp.x.com", Username: "me@x.com", PasswordRef: "mailbox_mia_ws_my_password",
		}},
	})
	w := httptest.NewRecorder()
	api.getAgentMailbox(w, "mia", "ws_other")
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetAgentMailbox_ReturnsConfigNoSecret(t *testing.T) {
	api := newMailboxTestAPI(t, map[string]map[string]config.MailboxConfig{
		"mia": {"ws_my": {
			Enabled: true, WorkspaceID: "ws_my", IMAPHost: "imap.x.com",
			SMTPHost: "smtp.x.com", Username: "me@x.com", PasswordRef: "mailbox_mia_ws_my_password",
		}},
	})
	// Store the password so `configured` resolves true.
	require.NoError(t, api.credStore.Set("mailbox_mia_ws_my_password", "secret"))

	w := httptest.NewRecorder()
	api.getAgentMailbox(w, "mia", "ws_my")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var resp gen.Mailbox
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "mia", resp.AgentId)
	assert.True(t, resp.Configured)
	require.NotNil(t, resp.Username)
	assert.Equal(t, "me@x.com", *resp.Username)
	assert.NotContains(t, w.Body.String(), "secret", "password must never be returned")
}

func TestDeleteAgentMailbox(t *testing.T) {
	api := newMailboxTestAPI(t, map[string]map[string]config.MailboxConfig{
		"mia": {"ws_my": {
			Enabled: true, WorkspaceID: "ws_my", IMAPHost: "i", SMTPHost: "s",
			Username: "u", PasswordRef: "mailbox_mia_ws_my_password",
		}},
	})
	require.NoError(t, api.credStore.Set("mailbox_mia_ws_my_password", "secret"))
	// Seed the on-disk config with the mailbox so the delete write has something to remove.
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		m["mailboxes"] = map[string]any{
			"mia": map[string]any{"ws_my": map[string]any{"enabled": true, "workspace_id": "ws_my"}},
		}
		return nil
	}))

	w := httptest.NewRecorder()
	api.deleteAgentMailbox(w, "mia", "ws_my")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	// Credential deleted.
	_, err := api.credStore.Get("mailbox_mia_ws_my_password")
	require.Error(t, err, "mailbox password must be removed from the store")

	// config.json: the agent's outer key is removed entirely (its only pair emptied).
	raw, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	var disk map[string]any
	require.NoError(t, json.Unmarshal(raw, &disk))
	if mbs, ok := disk["mailboxes"].(map[string]any); ok {
		_, exists := mbs["mia"]
		assert.False(t, exists, "agent's mailbox entry must be removed once its last pair is deleted")
	}
}

func TestDeleteAgentMailbox_OnePairLeavesOtherIntact(t *testing.T) {
	// Deleting one (agent, workspace) pair must not disturb a second pair for
	// the SAME agent in a different workspace.
	api := newMailboxTestAPI(t, map[string]map[string]config.MailboxConfig{
		"mia": {
			"ws_a": {Enabled: true, WorkspaceID: "ws_a", IMAPHost: "i", SMTPHost: "s", Username: "a@x.com", PasswordRef: "mailbox_mia_ws_a_password"},
			"ws_b": {Enabled: true, WorkspaceID: "ws_b", IMAPHost: "i", SMTPHost: "s", Username: "b@x.com", PasswordRef: "mailbox_mia_ws_b_password"},
		},
	})
	require.NoError(t, api.credStore.Set("mailbox_mia_ws_a_password", "secret-a"))
	require.NoError(t, api.credStore.Set("mailbox_mia_ws_b_password", "secret-b"))
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		m["mailboxes"] = map[string]any{
			"mia": map[string]any{
				"ws_a": map[string]any{"enabled": true, "workspace_id": "ws_a", "password_ref": "mailbox_mia_ws_a_password"},
				"ws_b": map[string]any{"enabled": true, "workspace_id": "ws_b", "password_ref": "mailbox_mia_ws_b_password"},
			},
		}
		return nil
	}))

	w := httptest.NewRecorder()
	api.deleteAgentMailbox(w, "mia", "ws_a")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	// ws_a's credential is gone, ws_b's survives.
	_, err := api.credStore.Get("mailbox_mia_ws_a_password")
	require.Error(t, err, "ws_a mailbox password must be removed from the store")
	gotB, err := api.credStore.Get("mailbox_mia_ws_b_password")
	require.NoError(t, err, "ws_b mailbox password must survive deleting ws_a")
	assert.Equal(t, "secret-b", gotB)

	// GET confirms: ws_a 404s, ws_b still resolves.
	w = httptest.NewRecorder()
	api.getAgentMailbox(w, "mia", "ws_a")
	require.Equal(t, http.StatusNotFound, w.Code)

	w = httptest.NewRecorder()
	api.getAgentMailbox(w, "mia", "ws_b")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	// config.json: agent entry survives with only ws_b.
	raw, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	var disk map[string]any
	require.NoError(t, json.Unmarshal(raw, &disk))
	agentEntry := disk["mailboxes"].(map[string]any)["mia"].(map[string]any)
	_, hasA := agentEntry["ws_a"]
	assert.False(t, hasA, "ws_a pair must be removed from config.json")
	_, hasB := agentEntry["ws_b"]
	assert.True(t, hasB, "ws_b pair must survive")
}

func TestDeleteAgentMailbox_AlsoRemovesLegacyCredential(t *testing.T) {
	// A pre-pair-addressing install may still have a blob under the OLD
	// per-agent key ("mailbox_<agentID>_password"). Deleting a pair must also
	// attempt to clean that up so migrated installs don't orphan it. A
	// missing legacy entry (the common case) must not be treated as an error.
	api := newMailboxTestAPI(t, map[string]map[string]config.MailboxConfig{
		"mia": {"ws_my": {
			Enabled: true, WorkspaceID: "ws_my", IMAPHost: "i", SMTPHost: "s",
			Username: "u", PasswordRef: "mailbox_mia_password", // legacy ref, still honored as-stored
		}},
	})
	require.NoError(t, api.credStore.Set("mailbox_mia_password", "legacy-secret"))
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		m["mailboxes"] = map[string]any{
			"mia": map[string]any{"ws_my": map[string]any{"enabled": true, "workspace_id": "ws_my", "password_ref": "mailbox_mia_password"}},
		}
		return nil
	}))

	w := httptest.NewRecorder()
	api.deleteAgentMailbox(w, "mia", "ws_my")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	_, err := api.credStore.Get("mailbox_mia_password")
	require.Error(t, err, "legacy mailbox password must be removed from the store")
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
			"ws_a": {Enabled: true, WorkspaceID: "ws_a", IMAPHost: "imap.a.com", SMTPHost: "smtp.a.com", Username: "a@x.com", PasswordRef: "mailbox_mia_ws_a_password"},
			"ws_b": {Enabled: true, WorkspaceID: "ws_b", IMAPHost: "imap.b.com", SMTPHost: "smtp.b.com", Username: "b@x.com", PasswordRef: "mailbox_mia_ws_b_password"},
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
	require.NotNil(t, resp.Mailboxes[0].WorkspaceId)
	assert.Equal(t, "ws_a", *resp.Mailboxes[0].WorkspaceId)
	assert.True(t, resp.Mailboxes[0].Configured)
	assert.Equal(t, "mia", resp.Mailboxes[1].AgentId)
	require.NotNil(t, resp.Mailboxes[1].WorkspaceId)
	assert.Equal(t, "ws_b", *resp.Mailboxes[1].WorkspaceId)
	assert.True(t, resp.Mailboxes[1].Configured)
	assert.NotContains(t, w.Body.String(), "secret", "password must never be returned")

	// Non-GET → 405.
	w = httptest.NewRecorder()
	api.listMailboxes(w, httptest.NewRequest(http.MethodPost, "/api/v1/mailboxes", nil))
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestSetAgentMailbox_GrantsEmailToolAllowsForDenyDefaultAgent(t *testing.T) {
	// Only the Assistant seed carries the five email-tool allows; every other
	// core agent is deny-by-default WITHOUT them, so a mailbox configured for
	// e.g. Jim registered tools that policy silently hid (live-UAT find,
	// 2026-07-03). Enabling a mailbox is the operator's explicit opt-in — the
	// wire contract's `enabled` means "register the email tools" — so the save
	// must fill in MISSING allow entries for deny-default agents. Explicit
	// operator-set entries are intent and must never be overridden.
	api := newMailboxTestAPI(t, nil)

	// Seed the on-disk config with a deny-default agent whose allowlist
	// predates the mailbox: no email entries except an explicit send_email=deny.
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		m["agents"] = map[string]any{
			"list": []any{
				map[string]any{
					"id": "mia",
					"tools": map[string]any{
						"builtin": map[string]any{
							"default_policy": "deny",
							"policies": map[string]any{
								"create_task": "allow",
								"send_email":  "deny", // explicit operator intent — must survive
							},
						},
					},
				},
			},
		}
		return nil
	}))

	body := `{"enabled":true,"imap_host":"i","smtp_host":"s","username":"mia@x.com","password":"p"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailboxes/ws_my", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "mia", "ws_my")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	raw, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(raw, &cfg))
	policies := cfg["agents"].(map[string]any)["list"].([]any)[0].(map[string]any)["tools"].(map[string]any)["builtin"].(map[string]any)["policies"].(map[string]any)

	// Missing email tools were granted…
	for _, name := range []string{"read_inbox", "search_email", "read_message", "reply"} {
		assert.Equal(t, "allow", policies[name], "tool %s must be granted", name)
	}
	// …the explicit deny survived…
	assert.Equal(t, "deny", policies["send_email"], "explicit operator deny must never be overridden")
	// …and unrelated entries are untouched.
	assert.Equal(t, "allow", policies["create_task"])
}
