package agent

// S20/FR013, founder Q1 A / architect H48 B1: Given GitHub connected and on
// for Dev, with an operator-approved WRITE mount holding a Git project whose
// origin is the local HTTPS receiver accepting only the spec account/token.
// When Dev pushes through bash with that mount as cwd, Then the actual transfer
// succeeds as x-access-token without the token in config, remote URL or argv.
// And the same push from the workspace remains refused by the evidence guard.
// ADR-063 D4's no-write-grant control is separate from a real write mount.
import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

func TestProgramGitHelper_ResetChain(t *testing.T) {
	t.Run("S20_no_token_in_config_or_arguments", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Fatal("BLOCKED: native Windows HTTPS Git/argv-observer fixture is not implemented; this POSIX wrapper does not certify S20/FR013 on Windows")
		}
		programsIsolateGit(t)
		git, err := exec.LookPath("git")
		require.NoError(t, err, "real installed Git is required, not a fake push executable")
		f := newProgramsTurnFixtureFor(t, func(cfg *config.Config) {
			cfg.Sandbox.ToolPolicies["read_file"] = "allow"
		})
		childHome := programsForeground(t, f, "s20-home", "printf '%s' \"$HOME\"")
		rel, err := filepath.Rel(f.home, childHome)
		require.NoError(t, err)
		require.True(t, filepath.IsAbs(childHome))
		require.NotEqual(t, "..", rel)
		require.False(t, strings.HasPrefix(rel, ".."+string(filepath.Separator)))
		require.NoError(t, os.MkdirAll(childHome, 0700))
		xdg := filepath.Join(childHome, "s20-xdg")
		require.NoError(t, os.MkdirAll(filepath.Join(xdg, "git"), 0700))
		t.Setenv("XDG_CONFIG_HOME", xdg)
		receiver := newProgramsGitReceiver(t, git, childHome)
		proxy := programsGitLocalProxy(t, receiver.server.Listener.Addr().String())
		global := filepath.Join(childHome, ".gitconfig")
		for key, value := range map[string]string{
			"s20.observer": "global-control", "http.sslCAInfo": receiver.cert, "http.proxy": proxy,
		} {
			programsGitSetup(t, git, childHome, "", "config", "--file", global, key, value)
		}
		programsGitSetup(t, git, childHome, "", "config", "--file", filepath.Join(xdg, "git", "config"), "s20.xdg", "xdg-control")

		// Exact401 controls prove neither anonymous nor wrong-credential HTTP
		// traffic can exercise the receive-pack backend.
		for _, wrong := range []bool{false, true} {
			req, requestErr := http.NewRequest(http.MethodGet, receiver.server.URL+"/s20.git/info/refs?service=git-receive-pack", nil)
			require.NoError(t, requestErr)
			if wrong {
				req.SetBasicAuth("x-access-token", "s20-wrong-synthetic-password")
			}
			response, requestErr := receiver.server.Client().Do(req)
			require.NoError(t, requestErr)
			require.NoError(t, response.Body.Close())
			require.Equal(t, http.StatusUnauthorized, response.StatusCode)
		}
		users, packs, _ := receiver.state.snapshot()
		require.Empty(t, users)
		require.Zero(t, packs)

		project := t.TempDir()
		programsGitSetup(t, git, childHome, "", "init", project)
		require.NoError(t, os.WriteFile(filepath.Join(project, "s20.txt"), []byte("S20 actual Git transfer\n"), 0600))
		programsGitSetup(t, git, childHome, project, "add", "s20.txt")
		programsGitSetup(t, git, childHome, project, "-c", "user.name=Daniel Piatkowski", "-c", "user.email=10800669+daniel-piatkowski-ai@users.noreply.github.com", "commit", "-m", "S20 fixture source")
		source := programsGitSetup(t, git, childHome, project, "rev-parse", "HEAD")
		programsGitSetup(t, git, childHome, project, "remote", "add", "origin", "https://github.com/s20.git")
		mount, warning, err := workspace.CreateMount(f.home, "sales", "s20-project", project)
		require.NoError(t, err, "real operator mount is required; do not bypass cwd admission")
		require.Empty(t, warning)
		require.Equal(t, "s20-project", mount.Name)
		observed := programsGitObserver(t, git, project)

		// No guard is disabled. The actual loop must still deny mutating the
		// engine-owned evidence repository, with no successful push marker.
		blocked := programsForeground(t, f, "s20-evidence-denial", "git push origin HEAD:refs/heads/s20 && printf 'S20_PUSH_DONE'")
		require.Contains(t, blocked, "protected evidence repo")
		require.NotContains(t, blocked, "S20_PUSH_DONE")
		users, packs, rejected := receiver.state.snapshot()
		require.Empty(t, users, "workspace refusal must not authenticate")
		require.Zero(t, packs, "workspace refusal must not transfer a pack")
		require.Equal(t, 2, rejected, "workspace refusal must not reach HTTP beyond the two direct401 controls")
		require.Empty(t, programsGitSetup(t, git, childHome, "", "--git-dir", receiver.repo, "for-each-ref", "--format=%(objectname)", "refs/heads/s20"))
		t.Log("S20 workspace/evidence control: actual-loop push refused; no HTTP/account/pack/ref effect")
		programsGitNoWriteGrant(t, f, git, childHome, project, receiver)
		push := "git push origin HEAD:refs/heads/s20 && printf 'S20_PUSH_DONE'"
		f.al.GetConfig().Agents.List[0].Programs = nil
		off := programsGitMountedTurn(t, f, "s20-programs-off", mount.Name, push)
		require.NotContains(t, off, "path escapes workspace", "S20/H48 B1: an operator-approved write mount must be admitted as bash cwd; the unchanged evidence/path guards must still refuse the two negative controls. Actual refusal: %s", off)
		require.NotContains(t, off, "S20_PUSH_DONE")
		users, packs, rejected = receiver.state.snapshot()
		require.Empty(t, users, "off control must not authenticate using an ambient saved login")
		require.Zero(t, packs)
		require.Greater(t, rejected, 2, "instrument: actual off child reached the receiver after two direct401 controls")
		require.Empty(t, programsGitSetup(t, git, childHome, "", "--git-dir", receiver.repo, "for-each-ref", "--format=%(objectname)", "refs/heads/s20"))

		f.al.GetConfig().Agents.List[0].Programs = []string{"github"}
		result := programsGitMountedTurn(t, f, "s20-real-push", mount.Name, push)
		require.Contains(t, result, "S20_PUSH_DONE", "real registered Dev bash push must succeed, not just credential fill or a fake marker")
		require.Equal(t, source, programsGitSetup(t, git, childHome, "", "--git-dir", receiver.repo, "rev-parse", "refs/heads/s20"), "actual receive-pack transferred the prepared commit/ref")
		require.Equal(t, "S20 actual Git transfer", programsGitSetup(t, git, childHome, "", "--git-dir", receiver.repo, "show", "refs/heads/s20:s20.txt"))
		users, packs, _ = receiver.state.snapshot()
		require.NotEmpty(t, users, "receiver authenticated the real Git client")
		for _, user := range users {
			require.Equal(t, "x-access-token", user, "S20 exact receiver account")
		}
		require.Equal(t, 1, packs, "one actual authenticated receive-pack POST, not a credential-fill surrogate")
		programsGitObserveArguments(t, observed)
		programsGitObserveConfig(t, observed, f.home, project, filepath.Dir(receiver.repo))
		t.Log("actual Dev loop: protected workspace push denied; no-write-grant project read succeeds but cwd/push refused; approved-write-mount off control401; managed helper authenticated x-access-token; HTTPS receive-pack updated exact source ref/content; positive config/URL/argv controls contain no token")
	})
}
