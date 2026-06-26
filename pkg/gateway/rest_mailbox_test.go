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
// endpoints.
func newMailboxTestAPI(t *testing.T, mailboxes map[string]config.MailboxConfig) *restAPI {
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

	body := `{"enabled":true,"workspace_id":"ws_my","imap_host":"imap.x.com","smtp_host":"smtp.x.com","username":"me@x.com","password":"app-pass-123"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailbox", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "mia")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	// Response: configured=true, password never echoed.
	var resp gen.Mailbox
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Configured)
	assert.True(t, resp.Enabled)
	assert.Equal(t, "mia", resp.AgentId)
	assert.NotContains(t, w.Body.String(), "app-pass-123", "password leaked into response")

	// config.json: password_ref set, no inline plaintext.
	raw, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "app-pass-123", "plaintext password leaked into config.json")
	var disk map[string]any
	require.NoError(t, json.Unmarshal(raw, &disk))
	mb := disk["mailboxes"].(map[string]any)["mia"].(map[string]any)
	assert.Equal(t, "mailbox_mia_password", mb["password_ref"])
	_, hasInline := mb["password"]
	assert.False(t, hasInline, "inline password must not be persisted")

	// Credential store: the secret is retrievable.
	got, err := api.credStore.Get("mailbox_mia_password")
	require.NoError(t, err)
	assert.Equal(t, "app-pass-123", got)
}

func TestSetAgentMailbox_UnknownAgent404(t *testing.T) {
	api := newMailboxTestAPI(t, nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/ghost/mailbox",
		strings.NewReader(`{"enabled":true,"workspace_id":"ws","imap_host":"i","smtp_host":"s","username":"u"}`))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "ghost")
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestSetAgentMailbox_MissingRequiredField(t *testing.T) {
	api := newMailboxTestAPI(t, nil)
	// Missing imap_host.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailbox",
		strings.NewReader(`{"enabled":true,"workspace_id":"ws","smtp_host":"s","username":"u"}`))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "mia")
	require.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
}

func TestSetAgentMailbox_Cap1Rejected(t *testing.T) {
	// Pre-existing mailbox for "jim" in ws_shared (in live cfg).
	api := newMailboxTestAPI(t, map[string]config.MailboxConfig{
		"jim": {Enabled: true, WorkspaceID: "ws_shared", IMAPHost: "i", SMTPHost: "s", Username: "jim@x.com"},
	})
	body := `{"enabled":true,"workspace_id":"ws_shared","imap_host":"i","smtp_host":"s","username":"mia@x.com","password":"p"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailbox", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "mia")
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "cap-1")
}

func TestGetAgentMailbox_NotConfigured404(t *testing.T) {
	api := newMailboxTestAPI(t, nil)
	w := httptest.NewRecorder()
	api.getAgentMailbox(w, "mia")
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetAgentMailbox_ReturnsConfigNoSecret(t *testing.T) {
	api := newMailboxTestAPI(t, map[string]config.MailboxConfig{
		"mia": {
			Enabled: true, WorkspaceID: "ws_my", IMAPHost: "imap.x.com",
			SMTPHost: "smtp.x.com", Username: "me@x.com", PasswordRef: "mailbox_mia_password",
		},
	})
	// Store the password so `configured` resolves true.
	require.NoError(t, api.credStore.Set("mailbox_mia_password", "secret"))

	w := httptest.NewRecorder()
	api.getAgentMailbox(w, "mia")
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
	api := newMailboxTestAPI(t, map[string]config.MailboxConfig{
		"mia": {
			Enabled: true, WorkspaceID: "ws_my", IMAPHost: "i", SMTPHost: "s",
			Username: "u", PasswordRef: "mailbox_mia_password",
		},
	})
	require.NoError(t, api.credStore.Set("mailbox_mia_password", "secret"))
	// Seed the on-disk config with the mailbox so the delete write has something to remove.
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		m["mailboxes"] = map[string]any{"mia": map[string]any{"enabled": true, "workspace_id": "ws_my"}}
		return nil
	}))

	w := httptest.NewRecorder()
	api.deleteAgentMailbox(w, "mia")
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	// Credential deleted.
	_, err := api.credStore.Get("mailbox_mia_password")
	require.Error(t, err, "mailbox password must be removed from the store")

	// config.json no longer has the mailbox entry.
	raw, err := os.ReadFile(filepath.Join(api.homePath, "config.json"))
	require.NoError(t, err)
	var disk map[string]any
	require.NoError(t, json.Unmarshal(raw, &disk))
	if mbs, ok := disk["mailboxes"].(map[string]any); ok {
		_, exists := mbs["mia"]
		assert.False(t, exists, "mailbox entry must be removed from config.json")
	}
}
