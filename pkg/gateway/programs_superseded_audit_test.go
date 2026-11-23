package gateway

// Spec rev8 S90/S114: actual person -> service -> held check -> superseding
// check -> shared audit file. No synthetic reservation/state/audit product row.
import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProgramCheck_ActualSupersededAudit(t *testing.T) {
	t.Run("S90_stale_generation_check_audited_superseded", func(t *testing.T) {
		f := pgPersonAPI(t)
		pgSupersededAuditInstrument(t, f)
		h := pgInstallSupersededBarrier(t, f, "account-a")
		require.NoError(t, os.WriteFile(h.hold, []byte("hold"), 0o600))
		a := pgProgramsSessionCall(t, f, http.MethodPut, "/api/v1/programs/github/credential", `{"value":"tok_valid_12345"}`)
		require.Equal(t, http.StatusOK, a.Code, "real person save A: %s", a.Body.String())
		h.waitAfterPrint(t)
		held := pgConnection(t, pgProgramEntryByID(t, f, "github"))
		require.Equal(t, "checking", held["state"])
		require.Equal(t, float64(1), held["generation"])
		require.Empty(t, held["account_label"], "S90: printed A has not published while held")
		b := pgProgramsSessionCall(t, f, http.MethodPut, "/api/v1/programs/github/credential", `{"value":"tok_new_654321","expected_generation":1}`)
		require.Equal(t, http.StatusOK, b.Code, "real person save B: %s", b.Body.String())
		final := pgConnection(t, pgPollEntryState(t, f, "github", "connected", 5*time.Second))
		require.Equal(t, float64(2), final["generation"])
		require.Equal(t, "account-b", final["account_label"], "S90: B finishes before A's barrier release")
		require.NoError(t, os.WriteFile(h.release, []byte("release"), 0o600))
		f.api.programService().Wait()
		require.Equal(t, 2, f.ghStarted(), "instrument: both actual checks launched exactly once")
		require.Equal(t, final, pgConnection(t, pgProgramEntryByID(t, f, "github")), "S90: late A never replaces B")
		persisted, err := os.ReadFile(filepath.Join(f.api.homePath, "programs", "connections.json"))
		require.NoError(t, err)
		require.NotContains(t, string(persisted), "account-a", "S90: A is never stored")
		pgAssertOneSupersededAudit(t, f)
	})

	t.Run("S114_same_generation_reverse_checks_audited_superseded", func(t *testing.T) {
		f := pgPersonAPI(t)
		pgSupersededAuditInstrument(t, f)
		h := pgInstallSupersededBarrier(t, f, "octocat")
		first := pgProgramsSessionCall(t, f, http.MethodPut, "/api/v1/programs/github/credential", `{"value":"tok_valid_12345"}`)
		require.Equal(t, http.StatusOK, first.Code)
		initial := pgConnection(t, pgPollEntryState(t, f, "github", "connected", 5*time.Second))
		require.Equal(t, float64(1), initial["generation"])
		require.Equal(t, "octocat", initial["account_label"])
		require.NoError(t, os.WriteFile(h.hold, []byte("hold"), 0o600))
		a := pgProgramsSessionCall(t, f, http.MethodPost, "/api/v1/programs/github/check", `{}`)
		require.Equal(t, http.StatusAccepted, a.Code)
		h.waitAfterPrint(t)
		require.Equal(t, "checking", pgConnection(t, pgProgramEntryByID(t, f, "github"))["state"])
		require.NoError(t, os.WriteFile(h.fail, []byte("fail newer check"), 0o600))
		b := pgProgramsSessionCall(t, f, http.MethodPost, "/api/v1/programs/github/check", `{}`)
		require.Equal(t, http.StatusAccepted, b.Code)
		final := pgConnection(t, pgPollEntryState(t, f, "github", "check_failed", 5*time.Second))
		require.Equal(t, float64(1), final["generation"], "S114: rechecks never create a new credential generation")
		require.Empty(t, final["account_label"], "S114: only newer failure publishes")
		require.Equal(t, `The account check failed. GitHub said: "HTTP 401: Bad credentials".`, final["message"])
		f.api.programService().Wait()
		// With A's barrier still closed, successful join cannot be an A success.
		// Unix additionally proves the actual whole group no longer exists.
		pgAssertCheckProcessGone(t, h.pid)
		require.NoFileExists(t, h.release, "instrument: A's barrier has not been released by this fixture")
		require.NoError(t, os.WriteFile(h.release, []byte("release"), 0o600))
		require.Equal(t, 3, f.ghStarted(), "instrument: initial, A and B are actual separate checks")
		require.Equal(t, final, pgConnection(t, pgProgramEntryByID(t, f, "github")), "S114: stopped A cannot publish over B")
		pgAssertOneSupersededAudit(t, f)
	})
}
