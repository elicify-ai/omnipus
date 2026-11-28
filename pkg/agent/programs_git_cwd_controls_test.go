package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

// ADR-063 D4: there is no read-only Mount mode. Reads are already open; the
// negative control is a readable project with NO operator write grant. Chief
// confirmed this interpretation of the S20 brief on 2026-11-24. Do not persist
// a pretend read-only Mount or hand-stamp a permissive filesystem policy.
func programsGitNoWriteGrant(t *testing.T, f *wu5Fixture, git, childHome, source string, receiver *programsGitReceiver) {
	t.Helper()
	project := filepath.Join(t.TempDir(), "project")
	programsGitSetup(t, git, childHome, "", "clone", "--no-hardlinks", source, project)
	programsGitSetup(t, git, childHome, project, "remote", "set-url", "origin", "https://github.com/s20.git")
	work, err := workspace.SafeWorkDir(f.home, "sales")
	require.NoError(t, err)
	const name = "s20-no-write-grant"
	require.NoError(t, os.Symlink(project, filepath.Join(work, name)))
	for _, root := range workspace.AllowedMountRoots(f.home, "sales") {
		rel, relErr := filepath.Rel(root, project)
		require.NoError(t, relErr)
		require.True(t, rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)), "Given: no operator write grant covers the control project")
	}

	// Read through the actual registered read_file tool and loop, not os.ReadFile
	// in the test process. A broken symlink or denied/unregistered tool cannot
	// satisfy this independent prepared-content positive control.
	sid := f.mainSession("sales", "dev")
	const callID = "s20-no-write-grant-read"
	f.provider.setScript(sid, []providers.LLMResponse{{ToolCalls: []providers.ToolCall{{
		ID: callID, Type: "function", Name: "read_file",
		Arguments: map[string]any{"path": filepath.Join(name, "s20.txt")},
	}}}, wu5Stop("Read-only access probe complete")})
	programsWebTurn(t, f, sid, "read the prepared project without requesting a write grant")
	f.waitForCalls(sid, 2, "S20 actual-loop no-write-grant read")
	require.Contains(t, f.provider.resultForCall(sid, callID), "S20 actual Git transfer\n", "ADR-063 D4: the no-write-grant project is genuinely readable")

	beforeUsers, beforePacks, beforeRejected := receiver.state.snapshot()
	beforeRef := programsGitSetup(t, git, childHome, "", "--git-dir", receiver.repo, "for-each-ref", "--format=%(objectname)", "refs/heads/s20")
	blocked := programsGitMountedTurn(t, f, "s20-no-write-grant-push", name, "git push origin HEAD:refs/heads/s20 && printf 'S20_PUSH_DONE'")
	require.Contains(t, blocked, "path escapes workspace", "B1 admits only operator-approved WRITE mounts, not a readable ungranted symlink")
	require.NotContains(t, blocked, "S20_PUSH_DONE")
	users, packs, rejected := receiver.state.snapshot()
	require.Equal(t, beforeUsers, users, "ungranted cwd refusal must not authenticate")
	require.Equal(t, beforePacks, packs, "ungranted cwd refusal must not transfer a pack")
	require.Equal(t, beforeRejected, rejected, "ungranted cwd refusal must happen before any HTTP request")
	require.Equal(t, beforeRef, programsGitSetup(t, git, childHome, "", "--git-dir", receiver.repo, "for-each-ref", "--format=%(objectname)", "refs/heads/s20"), "ungranted cwd refusal must not update a remote ref")
	t.Log("S20 no-write-grant control: actual-loop read_file sees prepared content; bash cwd/push refused; HTTP/account/pack/ref unchanged")
}
