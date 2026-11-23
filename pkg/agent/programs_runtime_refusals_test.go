package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgramError_PassThrough(t *testing.T) {
	t.Run("S32_program_error_passed_through", func(t *testing.T) {
		f := newProgramsTurnFixture(t)
		const message = "HTTP 403: Resource not accessible by integration"
		bin := t.TempDir()
		// The scenario's command has two arguments: api and repos/o/r.
		name, body := "gh", "#!/bin/sh\n[ \"$#\" -eq 2 ] && [ \"$1\" = api ] && [ \"$2\" = repos/o/r ] || exit 8\nprintf '"+message+"\\n' >&2\nexit 1\n"
		if runtime.GOOS == "windows" {
			name, body = "gh.cmd", "@echo off\r\nif not \"%~1\"==\"api\" exit /b 8\r\nif not \"%~2\"==\"repos/o/r\" exit /b 8\r\nif not \"%~3\"==\"\" exit /b 8\r\necho "+message+" 1>&2\r\nexit /b 1\r\n"
		}
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(body), 0700))
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		sid := f.mainSession("sales", "dev")
		f.provider.setScript(sid, []providers.LLMResponse{wu5BashCall("s32-error", "gh api repos/o/r"), wu5Stop("error received")})
		programsWebTurn(t, f, sid, "read the repository response")
		f.waitForCalls(sid, 2, "S32 real child error")
		result := strings.TrimSpace(f.provider.resultForCall(sid, "s32-error"))
		// S32 forbids a program-specific rewrite; it does not delete bash's
		// existing stderr wrapper. Compare the WHOLE result to a real ordinary
		// child emitting the same exact stderr/exit pair. This is a metamorphic
		// equality, not a copied expected presentation from an observed run.
		control := programsForeground(t, f, "s32-control", "printf '"+message+"\\n' >&2; exit 1")
		require.Equal(t, 1, strings.Count(control, message), "the process-edge control emitted exactly the spec's error line")
		require.Contains(t, control, "[Command exited with code 1]", "the actual control's exit is 1, not a swallowed failure")
		require.Equal(t, control, result, "S32: gh error and exit pass through exactly as for an ordinary child, with no program-specific rewrite")
	})
}

func TestProgramRunTimeConflicts(t *testing.T) {
	t.Run("S30_runtime_variable_conflict_fails_closed", func(t *testing.T) {
		f := newProgramsTurnFixture(t)
		store := f.al.sysagentDeps.CredStore
		_, err := store.CommitCard("s30-private-conflict", "s30-private-session", []credentials.CardValue{{
			BaseName: "S30_PRIVATE", Recipient: credentials.CardRecipient{Kind: credentials.KindAgentPrivate, WorkspaceID: "sales", AgentID: "dev", Field: "GH_TOKEN"}, Value: "private_conflict_value",
		}}, time.Now())
		require.NoError(t, err, "hand-edited Given deliberately bypasses the private-variable request gate")
		selected, err := store.PrivateEnvironment("sales", "dev", false)
		require.NoError(t, err)
		require.Equal(t, []credentials.PrivateValue{{Name: "GH_TOKEN", Value: "private_conflict_value"}}, selected, "instrument: the second source is actually selected for Dev")
		assert.Equal(t, "0", programsChildCount(t, f, "GH_TOKEN", "s30"), "S30: neither conflicting source may reach the actual child")
		sid := f.mainSession("sales", "dev")
		assert.Contains(t, strings.Join(f.provider.uniqueToolResultsFor(sid), "\n"), "GH_TOKEN was not set for this command: two sources set it. A person can fix this in Settings → Programs.", "S30: the exact repair notice is delivered")
		after, err := store.PrivateEnvironment("sales", "dev", false)
		require.NoError(t, err)
		require.Equal(t, selected, after, "runtime refusal must not delete a person's private selection")
	})
	t.Run("S86_runtime_git_host_conflict_no_helper", func(t *testing.T) {
		_, err := exec.LookPath("git")
		require.NoError(t, err, "the real git executable is a fixture prerequisite, not the behavior under test")
		// Keep system/global git helpers out of the process-edge fixture.
		t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
		t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "absent-global-config"))
		t.Setenv("GIT_TERMINAL_PROMPT", "0")
		t.Setenv("GIT_CONFIG_COUNT", "0")
		t.Setenv("GIT_CONFIG_PARAMETERS", "")
		for _, order := range [][]string{{"github", "mirror"}, {"mirror", "github"}} {
			f := newProgramsTurnFixtureFor(t, func(cfg *config.Config) {
				cfg.Agents.List[0].Programs = append([]string(nil), order...)
				cfg.Programs.Entries = []config.ProgramEntryConfig{{ID: "mirror", Kind: "custom", DisplayName: "GitLab Mirror", ProgramWords: []string{"git"}, TokenVariables: []string{"MIRROR_TOKEN"}, Git: &config.ProgramGitConfig{Host: "github.com", Username: "mirror-user"}}}
			})
			conn, err := programsTurnService(t, f).Publish("mirror", "mirror_fixture_token_123", nil, nil)
			require.NoError(t, err)
			require.Equal(t, "saved_not_checked", conn.State)
			sid := f.mainSession("sales", "dev")
			work := filepath.Join(f.home, "workspaces", "sales", "work")
			require.NoError(t, os.MkdirAll(work, 0700))
			answer := filepath.Join(work, "s86-git-answer")
			command := fmt.Sprintf("printf 'protocol=https\\nhost=github.com\\n\\n' | git credential fill > %q; printf 'S86_CHILD_RAN'", answer)
			f.provider.setScript(sid, []providers.LLMResponse{wu5BashCall("s86-conflict", command), wu5Stop("git inspected")})
			programsWebTurn(t, f, sid, "read the git credential for github.com")
			f.waitForCalls(sid, 2, "S86 real git child")
			result := f.provider.resultForCall(sid, "s86-conflict")
			require.Contains(t, result, "S86_CHILD_RAN", "a denied/missing bash tool cannot satisfy the empty-answer oracle")
			raw, err := os.ReadFile(answer)
			require.NoError(t, err)
			assert.Empty(t, string(raw), "S86: neither managed login answers under order %v", order)
			assert.Contains(t, result, "The connected git login for github.com was not used: two programs supply it. A person can fix this in Settings → Programs.", "S86: identical notice under order %v", order)
		}
	})
}
