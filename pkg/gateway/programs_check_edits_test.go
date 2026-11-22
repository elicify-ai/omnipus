package gateway

// Revision-8 S116/S131: relocated from the marker-only programs package
// leaves so the REAL person-session edit route, not a manual cfg mutation,
// drives the check. No expected result is taken from handler behavior.
import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type pgHeldEditCheck struct{ hold, release, starts string }

func pgInstallHeldEditCheck(t *testing.T, f *pgGateFixture) pgHeldEditCheck {
	t.Helper()
	dir := t.TempDir()
	h := pgHeldEditCheck{filepath.Join(dir, "hold"), filepath.Join(dir, "release"), filepath.Join(dir, "starts")}
	sh := fmt.Sprintf("#!/bin/sh\nprintf '%%s|%%s|%%s\\n' \"$*\" \"$ACME_TOKEN\" \"$ACME_NEW_TOKEN\" >> %q\nprintf 'acme-user\\n'\nwhile [ -f %q ] && [ ! -f %q ]; do /bin/sleep 0.01; done\n", h.starts, h.hold, h.release)
	bat := fmt.Sprintf("@echo off\r\necho %%*^|%%ACME_TOKEN%%^|%%ACME_NEW_TOKEN%%>>\"%s\"\r\necho acme-user\r\n:hold\r\nif not exist \"%s\" exit /b 0\r\nif exist \"%s\" exit /b 0\r\nping -n 2 127.0.0.1 >nul\r\ngoto hold\r\n", h.starts, h.hold, h.release)
	pgInstallFakeGh(t, f, sh, bat)
	t.Cleanup(func() { _ = os.WriteFile(h.release, []byte("release"), 0o600); f.api.programService().Wait() })
	return h
}

func (h pgHeldEditCheck) count() int {
	raw, _ := os.ReadFile(h.starts)
	return strings.Count(string(raw), "\n")
}

func (h pgHeldEditCheck) wait(t *testing.T, count int) {
	t.Helper()
	require.Eventually(t, func() bool { return h.count() == count }, 5*time.Second, 10*time.Millisecond, "exactly %d real checks must launch", count)
}

func pgEditCheckEntry(t *testing.T, f *pgGateFixture, id string, patch map[string]any) map[string]any {
	t.Helper()
	before := pgProgramEntryByID(t, f, id)
	patch["revision"] = before["revision"]
	body, err := json.Marshal(patch)
	require.NoError(t, err)
	w := pgPutProgramEntry(t, f, id, string(body))
	require.Equal(t, http.StatusOK, w.Code, "real person edit is accepted: %s", w.Body.String())
	return pgProgramEntryByID(t, f, id)
}

func pgAcmeCheckEntry(t *testing.T, f *pgGateFixture) string {
	t.Helper()
	entry := pgCreateProgramEntry(t, f, `{"display_name":"Acme","program_words":["gh"],"token_variables":["ACME_TOKEN"],"network_sites":["api.acme.example"],"identity_check":{"program_word":"gh","args":["whoami","old"],"timeout_seconds":20}}`)
	id, ok := entry["id"].(string)
	require.True(t, ok)
	return id
}

func TestProgramCheck_RealEdits(t *testing.T) {
	t.Run("S116_check_relevant_edit_starts_new_check", func(t *testing.T) {
		f := pgPersonAPI(t)
		h := pgInstallHeldEditCheck(t, f)
		id := pgAcmeCheckEntry(t, f)
		save := pgProgramsSessionCall(t, f, http.MethodPut, "/api/v1/programs/"+id+"/credential", `{"value":"acme_tok_123456"}`)
		require.Equal(t, http.StatusOK, save.Code, save.Body.String())
		pgPollEntryState(t, f, id, "connected", 5*time.Second)
		require.NoError(t, os.WriteFile(h.hold, []byte("hold"), 0o600))
		recheck := pgProgramsSessionCall(t, f, http.MethodPost, "/api/v1/programs/"+id+"/check", `{}`)
		require.Equal(t, http.StatusAccepted, recheck.Code, recheck.Body.String())
		h.wait(t, 2)
		oldRevision := f.api.programService().Connection(id).CheckRevision
		pgEditCheckEntry(t, f, id, map[string]any{"identity_check": map[string]any{"program_word": "gh", "args": []string{"whoami", "new"}, "timeout_seconds": 20}})
		require.Equal(t, oldRevision+1, f.api.programService().Connection(id).CheckRevision, "a check-relevant edit advances exactly once")
		h.wait(t, 3)
		require.NoError(t, os.WriteFile(h.release, []byte("release"), 0o600))
		final := pgPollEntryState(t, f, id, "connected", 5*time.Second)
		f.api.programService().Wait()
		require.Equal(t, "acme-user", pgConnection(t, final)["account_label"])
		raw, err := os.ReadFile(h.starts)
		require.NoError(t, err)
		require.Equal(t, []string{"whoami old|acme_tok_123456|", "whoami old|acme_tok_123456|", "whoami new|acme_tok_123456|"}, strings.Split(strings.TrimSpace(string(raw)), "\n"), "the new args must run automatically, not by a test recheck")
	})
	t.Run("S131_unrelated_edit_does_not_strand_the_row", func(t *testing.T) {
		f := pgPersonAPI(t)
		h := pgInstallHeldEditCheck(t, f)
		id := pgAcmeCheckEntry(t, f)
		require.NoError(t, os.WriteFile(h.hold, []byte("hold"), 0o600))
		save := pgProgramsSessionCall(t, f, http.MethodPut, "/api/v1/programs/"+id+"/credential", `{"value":"acme_tok_123456"}`)
		require.Equal(t, http.StatusOK, save.Code, save.Body.String())
		h.wait(t, 1)
		beforeRevision := f.api.programService().Connection(id).CheckRevision
		pgEditCheckEntry(t, f, id, map[string]any{"display_name": "Acme renamed"})
		require.Equal(t, beforeRevision, f.api.programService().Connection(id).CheckRevision)
		pgEditCheckEntry(t, f, id, map[string]any{"network_sites": []string{"other.acme.example"}})
		require.Equal(t, beforeRevision, f.api.programService().Connection(id).CheckRevision)
		require.NoError(t, os.WriteFile(h.release, []byte("release"), 0o600))
		final := pgPollEntryState(t, f, id, "connected", 5*time.Second)
		f.api.programService().Wait()
		require.Equal(t, "acme-user", pgConnection(t, final)["account_label"])
		require.Equal(t, 1, h.count(), "unrelated edits do not start another check")
		require.NoError(t, os.Remove(h.release))
		recheck := pgProgramsSessionCall(t, f, http.MethodPost, "/api/v1/programs/"+id+"/check", `{}`)
		require.Equal(t, http.StatusAccepted, recheck.Code, recheck.Body.String())
		h.wait(t, 2)
		pgEditCheckEntry(t, f, id, map[string]any{"token_variables": []string{"ACME_NEW_TOKEN"}})
		h.wait(t, 3)
		require.NoError(t, os.WriteFile(h.release, []byte("release"), 0o600))
		final = pgPollEntryState(t, f, id, "connected", 5*time.Second)
		f.api.programService().Wait()
		require.Equal(t, "acme-user", pgConnection(t, final)["account_label"])
		require.Equal(t, beforeRevision+1, f.api.programService().Connection(id).CheckRevision, "token-variable edits are check-relevant")
		require.Equal(t, 3, h.count(), "token-variable edit starts exactly one new check")
	})
}
