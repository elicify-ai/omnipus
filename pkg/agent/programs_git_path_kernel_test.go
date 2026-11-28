package agent

// S27/FR014: different gateway and actual workspace-runtime PATH, plus a real
// kernel-denial recording from INSIDE both the ordinary probe and its command.
import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/environmentsetup"
	"github.com/stretchr/testify/require"
)

func TestProgramGitVersion_ActualRuntimePATH(t *testing.T) {
	t.Run("S27_old_runtime_git_probe_is_confined", func(t *testing.T) {
		if programsKernelReexec(t) {
			return
		}
		programsIsolateGit(t)
		observe, err := os.MkdirTemp("/dev/shm", "s1-git-path-")
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, os.RemoveAll(observe)) })
		x := newProgramsNetworkFixture(t, false, true, false)
		f := x.f
		work := filepath.Join(f.home, "workspaces", "sales", "work")
		require.NoError(t, os.MkdirAll(work, 0700))
		gatewayLog := filepath.Join(observe, "gateway-calls")
		gatewayBin := t.TempDir()
		programsWriteGit(t, gatewayBin, fmt.Sprintf("[ \"$#:$1\" = 1:--version ] || exit 81\nprintf 'gateway-version\\n' >> %q\nprintf 'git version 2.43.0\\n'\n", gatewayLog))
		t.Setenv("PATH", gatewayBin+string(os.PathListSeparator)+os.Getenv("PATH"))
		host, err := exec.Command("git", "--version").Output()
		require.NoError(t, err)
		require.Equal(t, "git version 2.43.0\n", string(host), "Given: the actual gateway PATH resolves the newer, distinct executable")

		prefix, err := environmentsetup.WorkspaceEnvRoot(work)
		require.NoError(t, err)
		calls := filepath.Join(observe, "runtime-calls")
		probeDenial, runDenial := filepath.Join(observe, "probe-denial"), filepath.Join(observe, "command-denial")
		canary := programsEnforceKernel(t, f)
		old := programsWriteGit(t, filepath.Join(prefix, "bin"), fmt.Sprintf(`if [ "$#:$1" = 1:--version ]; then
%sprintf 'probe\n' >> %q
printf 'git version 2.30.2\n'
exit 0
fi
[ "$#" -eq 2 ] && [ "$1" = ls-remote ] && [ "$2" = https://github.com/o/r ] || exit 81
[ "${GIT_CONFIG_COUNT+x}" != x ] || exit 82
[ "$GH_TOKEN" = tok_valid_12345 ] || exit 83
%sprintf 'command\n' >> %q
printf 'S27_RUNTIME_COMMAND\n'
`, programsKernelReadAttempt(canary, probeDenial), calls, programsKernelReadAttempt(canary, runDenial), calls))
		view, err := environmentsetup.RuntimeEnvPaths(f.home, work)
		require.NoError(t, err)
		require.NotEmpty(t, view.BinDirs, "Given: actual runtime discovery, not a test-authored child PATH")
		require.Equal(t, filepath.Dir(old), view.BinDirs[0])
		result := programsForeground(t, f, "s27-runtime-path", "git ls-remote https://github.com/o/r")
		const notice = "Connected git logins were not used: git 2.30.2 is too old. Update git to 2.31 or later."
		// The spec fixes the exact notice and the fixture fixes command output,
		// not their separating whitespace. Compare ALL words, not a substring.
		require.Equal(t, strings.Fields("S27_RUNTIME_COMMAND "+notice), strings.Fields(result), "S27: actual old runtime command executes without managed config and reports the complete exact notice")
		raw, err := os.ReadFile(calls)
		require.NoError(t, err)
		require.Equal(t, "probe\ncommand\n", string(raw), "actual runtime executable was probed before the actual command")
		programsAssertKernelDenied(t, canary, probeDenial)
		programsAssertKernelDenied(t, canary, runDenial)
		raw, err = os.ReadFile(gatewayLog)
		require.NoError(t, err)
		require.Equal(t, "gateway-version\n", string(raw), "the control ran host git once; the real turn did NOT probe host git")
		t.Log("S27 runtime_PATH_version=2.30.2 gateway_PATH_version=2.43.0 config_count_absent=true actual_probe_kernel_denied=true actual_command_kernel_denied=true")
	})
}
