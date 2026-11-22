package gateway

// FR-051/S140/S141 backend half: the real tool, person submit/Settings routes,
// process-edge held gh, typed card snapshot and pinned committed generation.
// Rendered card text is asserted separately, not inferred from these fields.
import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

func pgHeldProgramCard(t *testing.T, f *pgGateFixture, fail bool) string {
	t.Helper()
	dir := t.TempDir()
	status := 0
	if fail {
		status = 1
	}
	sh := fmt.Sprintf("#!/bin/sh\nprintf started > %q/\"$GH_TOKEN\".started\nwhile [ ! -f %q/release ]; do /bin/sleep 0.01; done\nif [ \"$GH_TOKEN\" = tok_new_654321 ]; then echo newer-user; else echo octocat; fi\nprintf 'HTTP 401: Bad credentials' >&2\nexit %d\n", dir, dir, status)
	bat := fmt.Sprintf("@echo off\r\necho started>\"%s\\%%GH_TOKEN%%.started\"\r\n:hold\r\nif exist \"%s\\release\" goto ready\r\nping -n 2 127.0.0.1 >nul\r\ngoto hold\r\n:ready\r\nif \"%%GH_TOKEN%%\"==\"tok_new_654321\" (echo newer-user) else (echo octocat)\r\necho HTTP 401: Bad credentials 1>&2\r\nexit /b %d\r\n", dir, dir, status)
	pgInstallFakeGh(t, f, sh, bat)
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(dir, "release"), []byte("release"), 0600)
		f.api.programService().Wait()
	})
	return dir
}

func pgWaitProgramCardCheck(t *testing.T, dir, token string) {
	t.Helper()
	require.Eventually(t, func() bool { _, err := os.Stat(filepath.Join(dir, token+".started")); return err == nil }, 5*time.Second, 5*time.Millisecond, "the SAME card's check actually launches")
}

func pgReloadProgramCard(t *testing.T, f *pgGateFixture, sid, id string) gen.SessionStateSecretRequest {
	t.Helper()
	cards := f.api.agentLoop.SecretRequests().Snapshot("pg-person", sid)
	for _, card := range cards {
		if card.RequestId == id {
			return card
		}
	}
	t.Fatalf("submitted card %s missing from actual reload snapshot", id)
	return gen.SessionStateSecretRequest{}
}

func TestProgramCard_Contract(t *testing.T) {
	t.Run("S140_card_while_check_slow_fails_or_page_reloads", func(t *testing.T) {
		f := pgPersonAPI(t)
		dir := pgHeldProgramCard(t, f, true)
		id, sid, err := pgRaiseProgramCard(t, f, "dev", map[string]any{"program": "github"})
		require.NoError(t, err)
		w, _ := pgSubmitCard(t, f, id, pgCardFieldKey(t, f, "pg-person", sid, id), "tok_valid_12345", "", true, true)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		pgWaitProgramCardCheck(t, dir, "tok_valid_12345")
		card := pgReloadProgramCard(t, f, sid, id)
		require.Equal(t, "submitted", card.State)
		require.Equal(t, "program", card.Recipient.Kind)
		require.NotNil(t, card.Recipient.ProgramId)
		require.Equal(t, "github", *card.Recipient.ProgramId)
		require.NotNil(t, card.ProgramGeneration)
		require.Equal(t, int64(1), *card.ProgramGeneration)
		conn := pgConnection(t, pgProgramEntryByID(t, f, "github"))
		require.Equal(t, "checking", conn["state"], "submission completes BEFORE this held check")
		require.Equal(t, float64(*card.ProgramGeneration), conn["generation"])
		require.NoError(t, os.WriteFile(filepath.Join(dir, "release"), []byte("release"), 0600))
		entry := pgPollEntryState(t, f, "github", "check_failed", 5*time.Second)
		f.api.programService().Wait()
		require.Equal(t, `The account check failed. GitHub said: "HTTP 401: Bad credentials".`, pgConnection(t, entry)["message"])
		require.Equal(t, card, pgReloadProgramCard(t, f, sid, id), "same card survives settlement; UI derives the result from this generation")
	})
	t.Run("S141_replaced_before_the_cards_check_finished", func(t *testing.T) {
		f := pgPersonAPI(t)
		dir := pgHeldProgramCard(t, f, false)
		id, sid, err := pgRaiseProgramCard(t, f, "dev", map[string]any{"program": "github"})
		require.NoError(t, err)
		w, _ := pgSubmitCard(t, f, id, pgCardFieldKey(t, f, "pg-person", sid, id), "tok_valid_12345", "", true, true)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		pgWaitProgramCardCheck(t, dir, "tok_valid_12345")
		card := pgReloadProgramCard(t, f, sid, id)
		require.NotNil(t, card.ProgramGeneration)
		require.Equal(t, int64(1), *card.ProgramGeneration)
		replacement := pgProgramsSessionCall(t, f, http.MethodPut, pgSaveRoute, `{"value":"tok_new_654321"}`)
		require.Equal(t, http.StatusOK, replacement.Code, replacement.Body.String())
		pgWaitProgramCardCheck(t, dir, "tok_new_654321")
		require.NoError(t, os.WriteFile(filepath.Join(dir, "release"), []byte("release"), 0600))
		entry := pgPollEntryState(t, f, "github", "connected", 5*time.Second)
		f.api.programService().Wait()
		require.Equal(t, float64(2), pgConnection(t, entry)["generation"])
		require.Equal(t, "newer-user", pgConnection(t, entry)["account_label"])
		require.Equal(t, card, pgReloadProgramCard(t, f, sid, id), "card is pinned to N, not Settings N+1")
		receipt, found, err := f.store.CardReceipt(id)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, int64(1), receipt.ProgramGeneration)
	})
}
