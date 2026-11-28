package programs

// Revision-8 S07/S08/S90/S114/S115. S116/S131 use the actual edit route
// in pkg/gateway/programs_check_edits_test.go; the requirement is relocated,
// not removed. S90/S114 audit sequences now drive actual person routes and the
// shared audit file in pkg/gateway. S139's real delayed-launch oracle now
// lives in gateway's TestProgramCheck_ActualDelayedLaunch; its old row below
// remains a loud untagged/build-name guard, never a substitute runtime PASS.
import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/require"
)

func TestProgramCheck_Isolation(t *testing.T) {
	t.Run("S07_check_isolation_clean_environment", func(t *testing.T) {
		f := newCheckFixture(t, map[string]checkChildCase{"tok_valid_12345": {Label: "octocat"}})
		t.Setenv("GH_TOKEN", "gateway_ambient_tok")
		t.Setenv("S1_AMBIENT_SECRET", "gateway_ambient_tok")
		personHome := t.TempDir()
		t.Setenv("HOME", personHome)
		_, err := f.svc.Publish("github", "tok_valid_12345", nil, nil)
		require.NoError(t, err)
		conn := f.await(t, "github", "connected")
		require.Equal(t, "octocat", conn.AccountLabel)
		raw, err := os.ReadFile(filepath.Join(f.bin, "tok_valid_12345.observation"))
		require.NoError(t, err)
		var child checkChildObservation
		require.NoError(t, json.Unmarshal(raw, &child))
		require.Equal(t, []string{"api", "user", "--jq", ".login"}, child.Args)
		require.Equal(t, "tok_valid_12345", child.Token)
		require.Empty(t, child.Ambient, "the gateway's ambient secret must not enter the child")
		require.NotEmpty(t, child.Home)
		require.NotEqual(t, personHome, child.Home, "the account check owns a temporary HOME")
		require.NoDirExists(t, child.Home, "temporary check HOME is removed after the child is joined")
	})
	t.Run("S08_program_in_agent_writable_folder_not_run", func(t *testing.T) {
		f := newCheckFixture(t, map[string]checkChildCase{"tok_valid_12345": {Label: "must-not-run"}})
		// The only executable is in the agent's actual writable workspace.
		f.cfg.Agents.Defaults.Home = f.bin
		_, err := f.svc.Publish("github", "tok_valid_12345", nil, nil)
		require.NoError(t, err)
		f.svc.Wait()
		conn := f.svc.Connection("github")
		require.Equal(t, "saved_not_checked", conn.State, "S08: a workspace executable is not trusted for an account check")
		require.Contains(t, conn.Message, "installed", "S08 explains where the program is installed")
		require.NoFileExists(t, filepath.Join(f.bin, "tok_valid_12345.started"), "the untrusted executable never starts")
	})
}

func TestProgramCheck_Ordering(t *testing.T) {
	t.Run("S90_stale_generation_check_never_publishes", func(t *testing.T) {
		f := newCheckFixture(t, map[string]checkChildCase{"tok_valid_12345": {Label: "account-a", Hold: true}, "tok_new_654321": {Label: "account-b"}})
		a, err := f.svc.Publish("github", "tok_valid_12345", nil, nil)
		require.NoError(t, err)
		require.Equal(t, int64(1), a.Generation)
		f.waitStarted(t, "tok_valid_12345")
		require.Empty(t, f.svc.Connection("github").AccountLabel, "held A has not published")
		b, err := f.svc.Publish("github", "tok_new_654321", nil, nil)
		require.NoError(t, err)
		require.Equal(t, int64(2), b.Generation)
		conn := f.await(t, "github", "connected")
		f.release(t, "tok_valid_12345")
		f.svc.Wait()
		require.Equal(t, conn, f.svc.Connection("github"), "late A cannot replace B")
		require.Equal(t, "account-b", conn.AccountLabel)
		require.Equal(t, int64(2), conn.Generation)
		raw, err := os.ReadFile(f.state)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "account-a", "A is never stored")
	})
	t.Run("S114_two_checks_reverse_order_older_stopped", func(t *testing.T) {
		f := newCheckFixture(t, map[string]checkChildCase{"tok_valid_12345": {Label: "octocat"}})
		_, err := f.svc.Publish("github", "tok_valid_12345", nil, nil)
		require.NoError(t, err)
		initial := f.await(t, "github", "connected")
		writeCheckCases(t, f.bin, map[string]checkChildCase{"tok_valid_12345": {Label: "octocat", Hold: true}})
		require.NoError(t, os.Remove(filepath.Join(f.bin, "tok_valid_12345.started")))
		_, err = f.svc.Recheck("github")
		require.NoError(t, err)
		f.waitStarted(t, "tok_valid_12345")
		writeCheckCases(t, f.bin, map[string]checkChildCase{"tok_valid_12345": {Stderr: "HTTP 401: Bad credentials", Exit: 1}})
		_, err = f.svc.Recheck("github")
		require.NoError(t, err)
		conn := f.await(t, "github", "check_failed")
		f.release(t, "tok_valid_12345")
		f.svc.Wait()
		require.Equal(t, initial.Generation, conn.Generation, "rechecks do not create a token generation")
		require.Empty(t, conn.AccountLabel)
		require.Equal(t, `The account check failed. GitHub said: "HTTP 401: Bad credentials".`, conn.Message)
		require.Equal(t, conn, f.svc.Connection("github"), "A cannot publish after B")
	})
	t.Run("S115_restart_while_checking_reruns_a_fresh_check", func(t *testing.T) {
		f := newCheckFixture(t, map[string]checkChildCase{"tok_valid_12345": {Label: "octocat", Hold: true}})
		_, err := f.svc.Publish("github", "tok_valid_12345", nil, nil)
		require.NoError(t, err)
		f.waitStarted(t, "tok_valid_12345")
		before, err := os.ReadFile(f.state)
		require.NoError(t, err)
		require.Contains(t, string(before), `"state": "checking"`, "restart Given is durable")
		// Kill/join the old check without publishing an invented terminal state;
		// restart a fresh real service over that exact persisted checking file.
		f.svc.mu.Lock()
		for id := range f.svc.checks {
			f.svc.supersedeLocked(id)
		}
		f.svc.mu.Unlock()
		f.svc.Wait()
		require.NoError(t, os.Remove(filepath.Join(f.bin, "tok_valid_12345.started")))
		writeCheckCases(t, f.bin, map[string]checkChildCase{"tok_valid_12345": {Label: "octocat"}})
		f.svc = NewService(func() *config.Config { return f.cfg }, func() (*credentials.Store, error) { return f.store, nil }, f.state)
		start := time.Now()
		f.svc.ResumeChecks()
		f.waitStarted(t, "tok_valid_12345")
		conn := f.await(t, "github", "connected")
		require.Equal(t, int64(1), conn.Generation)
		require.Equal(t, "octocat", conn.AccountLabel)
		require.Less(t, time.Since(start), 25*time.Second, "S115: check timeout plus five seconds")
	})
	// S90_S114_superseded_check_audit_binding migrated intact to gateway's
	// TestProgramCheck_ActualSupersededAudit: two actual person-route/child
	// sequences, exact generation/result ordering and the real shared audit
	// sink with a positive reader/outcome instrument. No synthetic product row.
	t.Run("S139_delayed_launch_cannot_stop_the_newer_check", func(t *testing.T) {
		// Full unchanged S139 oracle: gateway's tagged actual A/B routes,
		// kernel process-open observer, terminal B and shared audit campaign.
		// This row checks only mandatory build/name selection; no runtime claim.
		requireS1TaggedLeaf(t, "H42")
	})
}

func writeCheckCases(t *testing.T, bin string, cases map[string]checkChildCase) {
	t.Helper()
	raw, err := json.Marshal(cases)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(bin, "cases.json"), raw, 0o600))
}
