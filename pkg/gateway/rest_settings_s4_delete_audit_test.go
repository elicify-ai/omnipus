// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_settings_s4_delete_audit_test.go — Settings redesign #1519, part 8, RED.
//
// Security finding S4 proof (sec report 2026-11-20, "successful credential
// deletion has no operation audit record"). The report's instrument gap: the
// earlier test "does not exercise actual route bypass exclusion, CSRF or
// deletion auditing". This test inspects the ACTUAL audit record a successful
// deletion leaves in a real audit logger's JSONL.
//
// Oracle (spec + report, never the fix code):
//   - FR-056: DELETE /credentials/{key} "keeps … the audit entry"; C-5:
//     "still audited".
//   - Report S4: "emit a value-free credential-deletion event containing the
//     authenticated actor, credential reference and durable outcome. Record a
//     committed deletion even when later reload fails; do not include the
//     secret. QA should inspect the actual audit event, not only the HTTP
//     response or absence of the vault entry."
//
// The audit EVENT NAME is deliberately not pinned — the fix chooses it. What
// is pinned: a NEW record appears (vs. before the delete), it names the
// authenticated actor and the credential reference, and it never contains the
// secret value. Durability is proven by the reload-failure subtest.
//
// RED today (landing): deleteCredential's whole successful path (requireReAuth
// consent, Store.Delete, reload, response) emits no audit event and neither
// does Store.Delete — so no new record of any kind appears.
//
// Expected against work/settings-1519-be (fix 3daee36252): both subtests pass.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
)

const (
	s4DoomedKey    = "S4_DOOMED_KEY"
	s4DoomedSecret = "s4-vault-secret-value"
	s4PersonUser   = "s4-person"
)

// s4AuditProofAPI is the real-route harness for the deletion-audit proof: a
// real logged-in person, the real registered routes behind the production
// middleware chain, an unlocked vault, and a REAL audit logger attached.
// Returns the auditor's directory (the loop's own writer directory is added
// by the caller from the live config, so both real sinks are covered).
func s4AuditProofAPI(t *testing.T) (*restAPI, http.Handler, []*http.Cookie, string) {
	t.Helper()
	withEdition(t, config.EditionCore)
	t.Setenv("OMNIPUS_MASTER_KEY", testMasterKey)
	api, home := newTestRestAPIWithUser(t, s4PersonUser, "S4-Audit-Proof-Passphrase-1")
	credStore := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, credentials.Unlock(credStore), "unlock the proof vault")
	api.credStore = credStore
	require.NoError(t, credStore.Set(s4DoomedKey, s4DoomedSecret), "seed the doomed vault entry")

	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	handler := buildProductionMiddlewareChain(api, mux)

	login := httptest.NewRecorder()
	loginBody := `{"username":"` + s4PersonUser + `","password":"S4-Audit-Proof-Passphrase-1"}`
	api.HandleLogin(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(loginBody)))
	require.Equal(t, http.StatusOK, login.Code, "real person login: %s", login.Body.String())

	auditDir := attachTestAuditor(t, api) // a REAL audit logger on api.auditor
	return api, handler, login.Result().Cookies(), auditDir
}

// s4AuditLines returns every JSONL line in the given audit directories,
// skipping directories whose file does not exist yet.
func s4AuditLines(t *testing.T, dirs ...string) []string {
	t.Helper()
	var lines []string
	for _, dir := range dirs {
		data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		require.NoError(t, err, "read audit sink %s", dir)
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) != "" {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

// s4DeleteCredential sends the signed-in DELETE through the real route. On
// landing the server still requires the re-auth consent token, so a token
// minted for the signed-in person is attached on both branches (harmless once
// the gate is removed — C-5 B7).
func s4DeleteCredential(t *testing.T, api *restAPI, handler http.Handler, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/credentials/"+s4DoomedKey, nil)
	var csrf *http.Cookie
	for _, c := range cookies {
		switch {
		case c.Name == middleware.CSRFCookieName || c.Name == middleware.CSRFCookieNameHTTP:
			csrf = c
		case c.Name == middleware.SessionCookieName:
			req.AddCookie(c)
		}
	}
	require.NotNil(t, csrf, "fixture: the login response must seed a CSRF cookie")
	req.AddCookie(csrf)
	req.Header.Set(middleware.CSRFHeaderName, csrf.Value)
	token, err := api.reauthStoreOrInit().mint(s4PersonUser)
	require.NoError(t, err, "mint the consent token the landing server still requires")
	req.Header.Set(reAuthHeader, token)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

// s4LoopAuditDir is the live audit writer's directory, derived the way the
// writer derives it (Dir(agents home) + "/system" — the spec's #1742 row).
func s4LoopAuditDir(t *testing.T, api *restAPI) string {
	t.Helper()
	return filepath.Join(filepath.Dir(api.agentLoop.GetConfig().AgentHomeBasePath()), "system")
}

func TestDeleteCredentialAudit_RecordsCommittedDeletion(t *testing.T) {
	t.Run("a successful delete writes a value-free deletion record naming actor and reference", func(t *testing.T) {
		api, handler, cookies, auditorDir := s4AuditProofAPI(t)
		loopDir := s4LoopAuditDir(t, api)
		before := map[string]bool{}
		for _, line := range s4AuditLines(t, auditorDir, loopDir) {
			before[line] = true
		}

		w := s4DeleteCredential(t, api, handler, cookies)

		require.Equal(t, http.StatusOK, w.Code,
			"precondition: the signed-in delete must succeed; body=%s", w.Body.String())
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Equal(t, "removed", resp["status"], "the delete must commit; body=%s", w.Body.String())

		after := s4AuditLines(t, auditorDir, loopDir)
		var newDeletionRecords []string
		for _, line := range after {
			if before[line] {
				continue // pre-existing records (e.g. the logger's startup row)
			}
			if strings.Contains(line, s4DoomedKey) && strings.Contains(line, s4PersonUser) {
				newDeletionRecords = append(newDeletionRecords, line)
			}
		}
		require.NotEmpty(t, newDeletionRecords,
			"RED (S4, FR-056 'keeps … the audit entry', report S4): a successful credential deletion wrote NO "+
				"audit record naming the actor (%s) and the credential reference (%s) in either real audit sink — "+
				"the deletion cannot be attributed after an outage caused by a signed-in user or a stolen session",
			s4PersonUser, s4DoomedKey)
		for _, line := range newDeletionRecords {
			require.NotContains(t, line, s4DoomedSecret,
				"the deletion record must be value-free — the secret itself never enters the audit trail: %s", line)
		}
	})

	t.Run("a committed deletion is still audited when the reload fails afterwards", func(t *testing.T) {
		api, handler, cookies, auditorDir := s4AuditProofAPI(t)
		loopDir := s4LoopAuditDir(t, api)
		// The post-delete live reload fails (the seam is the reload pipeline
		// callback production wires at gateway boot).
		api.agentLoop.SetReloadFunc(func() error {
			return errors.New("s4 proof: the live reload failed")
		})
		before := map[string]bool{}
		for _, line := range s4AuditLines(t, auditorDir, loopDir) {
			before[line] = true
		}

		w := s4DeleteCredential(t, api, handler, cookies)

		require.Equal(t, http.StatusOK, w.Code,
			"the deletion commits even when the reload fails — 200 with a reload warning; body=%s", w.Body.String())
		require.Contains(t, w.Body.String(), "reload_warning",
			"fixture check: the failed reload must be reported, proving the failure path was taken; body=%s",
			w.Body.String())

		after := s4AuditLines(t, auditorDir, loopDir)
		var newDeletionRecords []string
		for _, line := range after {
			if before[line] {
				continue
			}
			if strings.Contains(line, s4DoomedKey) && strings.Contains(line, s4PersonUser) {
				newDeletionRecords = append(newDeletionRecords, line)
			}
		}
		require.NotEmpty(t, newDeletionRecords,
			"RED (S4 durability): the deletion committed before the reload failed, so its audit record must exist "+
				"anyway — the durable outcome, not the reload result, is what gets recorded")
		for _, line := range newDeletionRecords {
			require.NotContains(t, line, s4DoomedSecret,
				"the deletion record must stay value-free on the reload-failure path: %s", line)
		}
	})
}
