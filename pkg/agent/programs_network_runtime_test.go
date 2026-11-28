package agent

// Oracles: spec rev8 S21/S22/S23/S24/S28/S89, FR-015–018. These are actual
// loop/child/proxy tests, not a mock of the grant or a call-count-only substitute.
import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProgramNetwork_ActualAutoAsk(t *testing.T) {
	t.Run("S21_auto_grants_profile_sites_revokes_after_run", func(t *testing.T) {
		x := newProgramsNetworkFixture(t, false, true, false)
		finish := x.start(t, "gh api user")
		token := x.childToken(t, true)
		require.Empty(t, x.approver.snapshot(), "S21: no approval for a visible connected program")
		x.probe(t, token, "api.github.com", true)
		x.probe(t, "", "api.github.com", false)
		x.probe(t, token, "example.org", false)
		require.Equal(t, "octocat", strings.TrimSpace(finish()), "actual gh child verified its program environment")
		x.probe(t, token, "api.github.com", false)
		require.Empty(t, x.approver.snapshot(), "S21: no delayed/second prompt")
	})
	t.Run("S22_other_hosts_still_asked", func(t *testing.T) {
		x := newProgramsNetworkFixture(t, false, true, false)
		finish := x.start(t, "gh api user && curl https://example.org")
		token := x.childToken(t, true)
		requests := x.approver.snapshot()
		require.Len(t, requests, 1, "S22: exactly one actual approval")
		require.Equal(t, "bash", requests[0].ToolName)
		encodedHosts, err := json.Marshal(requests[0].Args["hosts"])
		require.NoError(t, err)
		var hosts []string
		require.NoError(t, json.Unmarshal(encodedHosts, &hosts))
		require.Equal(t, []string{"example.org"}, hosts, "S22: exact host contents across the real approval carrier's JSON conversion")
		require.Equal(t, "Allows network access to: example.org", requests[0].Args["note"])
		x.probe(t, token, "api.github.com", true)
		x.probe(t, token, "example.org", true)
		require.Equal(t, "octocat\nS22_CURL_RAN", finish(), "both actual external-command fixtures ran")
		x.probe(t, token, "api.github.com", false)
		x.probe(t, token, "example.org", false)
		require.Len(t, x.approver.snapshot(), 1, "no second prompt after approval")
	})
	t.Run("S23_deny_wins", func(t *testing.T) {
		var ordinary string
		for _, connected := range []bool{false, true} {
			x := newProgramsNetworkFixture(t, false, true, true)
			if !connected {
				x.f.al.GetConfig().Agents.List[0].Programs = nil
			}
			result := x.start(t, "gh api user")()
			require.Contains(t, result, "Command blocked by operator rule", "S23 preserves the ordinary existing deny presentation")
			_, err := os.Stat(x.child)
			require.ErrorIs(t, err, os.ErrNotExist, "denied gh never reached its actual child-entry instrument")
			require.Empty(t, x.approver.snapshot(), "deny comes before an approval or grant")
			if !connected {
				ordinary = result
			} else {
				require.Equal(t, ordinary, result, "S23: connecting a program must not replace or bypass the existing deny result")
			}
		}
	})
	t.Run("S24_ask_grants_after_approval", func(t *testing.T) {
		x := newProgramsNetworkFixture(t, false, false, false)
		x.approver.holdFirst = true
		finish := x.start(t, "gh api user")
		select {
		case req := <-x.approver.entered:
			require.Equal(t, "bash", req.ToolName)
			require.Equal(t, "gh api user", req.Args["command"])
		case <-time.After(6 * time.Second):
			t.Fatal("S24: actual Ask-mode command did not reach the person's approval")
		}
		_, err := os.Stat(x.child)
		require.ErrorIs(t, err, os.ErrNotExist, "the real child cannot run while its person approval is held")
		x.approver.decision <- true
		token := x.childToken(t, true)
		x.probe(t, token, "api.github.com", true)
		require.Equal(t, "octocat", strings.TrimSpace(finish()))
		x.probe(t, token, "api.github.com", false)
		require.Len(t, x.approver.snapshot(), 1, "S24: one person approval, never a second program-site prompt")
	})
}

func TestProgramEnv_ActualUnmatchedAndScript(t *testing.T) {
	t.Run("S28_unmatched_program_word_no_grant_token_still_available", func(t *testing.T) {
		var ordinary []map[string]any
		for _, connected := range []bool{false, true} {
			x := newProgramsNetworkFixture(t, false, true, false)
			if !connected {
				x.f.al.GetConfig().Agents.List[0].Programs = nil
				path := filepath.Join(strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))[0], "aws")
				body, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Contains(t, string(body), `[ "$GH_TOKEN" = tok_valid_12345 ]`)
				body = []byte(strings.ReplaceAll(string(body), `[ "$GH_TOKEN" = tok_valid_12345 ]`, `[ -z "${GH_TOKEN:-}" ]`))
				require.NoError(t, os.WriteFile(path, body, 0700))
			}
			finish := x.start(t, "aws s3 ls")
			token := x.childToken(t, false)
			x.probe(t, token, "api.github.com", false)
			require.Equal(t, "octocat", strings.TrimSpace(finish()), "actual unmatched-program child verifies token presence/absence before emitting this instrument")
			requests := x.approver.snapshot()
			require.NotEmpty(t, requests, "S28: ordinary network approval remains reachable")
			args := make([]map[string]any, 0, len(requests))
			for _, req := range requests {
				require.Equal(t, "bash", req.ToolName)
				args = append(args, req.Args)
				require.NotContains(t, req.Args["note"], "github.com")
				encoded, err := json.Marshal(req.Args)
				require.NoError(t, err)
				require.NotContains(t, string(encoded), "github.com", "no GitHub host hidden by a Go slice-type conversion")
			}
			if !connected {
				ordinary = args
			} else {
				require.Equal(t, ordinary, args, "S28: exact ordinary approval arguments/count, not a program-specific prompt")
			}
		}
	})
	t.Run("S89_script_calling_gh_internally", func(t *testing.T) {
		t.Run("God_Mode", func(t *testing.T) {
			x := newProgramsNetworkFixture(t, true, true, false)
			finish := x.start(t, "./deploy.sh")
			// No proxy is required in God Mode. The script still calls the real
			// external-command fixture, which checks its inherited token first.
			require.Eventually(t, func() bool { _, err := os.Stat(x.child); return err == nil }, 6*time.Second, 10*time.Millisecond, "actual script reached gh")
			require.Equal(t, "octocat", strings.TrimSpace(finish()), "S89: actual God Mode script receives the saved token")
			require.Empty(t, x.approver.snapshot())
		})
		t.Run("Auto", func(t *testing.T) {
			x := newProgramsNetworkFixture(t, false, true, false)
			finish := x.start(t, "./deploy.sh")
			token := x.childToken(t, false)
			x.probe(t, token, "api.github.com", false)
			require.Equal(t, "octocat", strings.TrimSpace(finish()), "the actual internal gh still receives GH_TOKEN; site grants depend only on the visible command")
			requests := x.approver.snapshot()
			require.Len(t, requests, 1, "ordinary no-sandbox script approval, not an automatic GitHub site grant")
			require.Equal(t, "no_sandbox_ask", requests[0].Args["adr092_kind"])
			require.NotContains(t, requests[0].Args["note"], "github.com")
		})
	})
}
