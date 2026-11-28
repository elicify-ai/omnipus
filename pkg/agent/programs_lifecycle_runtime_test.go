package agent

// Spec rev 8 S17/S36/S51/S53: real lifecycle writes followed by real loop-wired
// children. These are not replaced by an Attach unit test or REST state alone.
import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/programs"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func programsTurnService(t *testing.T, f *wu5Fixture) *programs.Service {
	t.Helper()
	svc := programs.NewService(f.al.GetConfig, func() (*credentials.Store, error) {
		return f.al.sysagentDeps.CredStore, nil
	}, filepath.Join(f.home, "programs", "connections.json"))
	t.Cleanup(svc.Wait)
	return svc
}

// programsChildCount observes the specified printenv/wc command through its
// actual workspace file. A denied/missing child cannot satisfy the zero case.
func programsChildCount(t *testing.T, f *wu5Fixture, variable, label string) string {
	t.Helper()
	sid := f.mainSession("sales", "dev")
	work := filepath.Join(f.home, "workspaces", "sales", "work")
	require.NoError(t, os.MkdirAll(work, 0700))
	path := filepath.Join(work, label+"-count")
	command := fmt.Sprintf("printenv %s | wc -c > %q; printf 'S1_CHILD_RAN'", variable, path)
	f.provider.setScript(sid, []providers.LLMResponse{wu5Bash(command), wu5Stop("count completed")})
	programsWebTurn(t, f, sid, "count the program variable without printing its value")
	f.waitForCalls(sid, 2, label+" real child")
	results, err := json.Marshal(f.provider.uniqueToolResultsFor(sid))
	require.NoError(t, err)
	require.Contains(t, string(results), "S1_CHILD_RAN", "the actual child ran")
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.TrimSpace(string(body))
}

func TestProgramRuntime_MissingS1Clauses(t *testing.T) {
	t.Run("S17_on_but_not_connected_next_printenv_is_empty", func(t *testing.T) {
		f := newProgramsTurnFixtureFor(t, func(cfg *config.Config) {
			cfg.Agents.List[0].Programs = []string{"fly"}
		})
		require.Equal(t, "not_connected", programsTurnService(t, f).Connection("fly").State)
		require.Equal(t, []string{"fly"}, f.al.GetConfig().Agents.List[0].Programs, "the switch is on despite no saved Fly token")
		require.Equal(t, "0", programsChildCount(t, f, "FLY_API_TOKEN", "s17"), "S17: no saved token means no child variable")
	})
	t.Run("S36_no_check_next_printenv_uses_saved_token", func(t *testing.T) {
		f := newProgramsTurnFixtureFor(t, func(cfg *config.Config) {
			cfg.Agents.List[0].Programs = []string{"acme"}
			cfg.Programs.Entries = []config.ProgramEntryConfig{{ID: "acme", Kind: "custom", DisplayName: "Acme", ProgramWords: []string{"acme"}, TokenVariables: []string{"ACME_TOKEN"}}}
		})
		svc := programsTurnService(t, f)
		conn, err := svc.Publish("acme", "acme_tok_123456", nil, nil)
		require.NoError(t, err)
		require.Equal(t, "saved_not_checked", conn.State)
		require.Equal(t, "Saved, not checked: this program has no account check.", conn.Message)
		require.Equal(t, "16", programsChildCount(t, f, "ACME_TOKEN", "s36"), "S36's specified next-child count")
	})
	t.Run("S51_replace_next_printenv_uses_new_token", func(t *testing.T) {
		f := newProgramsTurnFixture(t)
		// S51's fake account edge distinguishes the replacement from the old
		// token; neither the producer nor child environment is mocked.
		bin := t.TempDir()
		name, body := "gh", "#!/bin/sh\n[ \"$#\" -eq 4 ] && [ \"$1\" = api ] && [ \"$2\" = user ] && [ \"$3\" = --jq ] && [ \"$4\" = .login ] || exit 8\n[ \"$GH_TOKEN\" = tok_new_654321 ] || exit 9\nprintf 'hubot\\n'\n"
		if runtime.GOOS == "windows" {
			name, body = "gh.cmd", "@echo off\r\nif not \"%~1\"==\"api\" exit /b 8\r\nif not \"%~2\"==\"user\" exit /b 8\r\nif not \"%~3\"==\"--jq\" exit /b 8\r\nif not \"%~4\"==\".login\" exit /b 8\r\nif not \"%GH_TOKEN%\"==\"tok_new_654321\" exit /b 9\r\necho hubot\r\n"
		}
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(body), 0700))
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		svc := programsTurnService(t, f)
		_, err := svc.Publish("github", "tok_new_654321", nil, nil)
		require.NoError(t, err)
		svc.Wait()
		after := svc.Connection("github")
		require.Equal(t, "connected", after.State)
		require.Equal(t, "hubot", after.AccountLabel)
		require.Equal(t, int64(2), after.Generation)
		require.Equal(t, "15", programsChildCount(t, f, "GH_TOKEN", "s51"), "S51: the 14-byte replacement plus printenv newline, not old 16 or withheld 0")
	})
	t.Run("S53_disconnect_next_printenv_is_empty_switch_stays_on", func(t *testing.T) {
		f := newProgramsTurnFixture(t)
		assert.Equal(t, "16", programsChildCount(t, f, "GH_TOKEN", "s53-before"), "positive runtime instrument before disconnect")
		svc := programsTurnService(t, f)
		after, err := svc.Disconnect("github")
		require.NoError(t, err)
		require.Equal(t, "not_connected", after.State)
		require.Equal(t, int64(2), after.Generation)
		require.Equal(t, []string{"github"}, f.al.GetConfig().Agents.List[0].Programs, "disconnect preserves Dev's switch")
		require.Equal(t, "0", programsChildCount(t, f, "GH_TOKEN", "s53-after"), "S53: the next real child has no disconnected token")
	})
}
