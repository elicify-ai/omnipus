package agent

// Spec rev 8 S19/S87/S88. Account/version executables are process-edge fakes;
// the loop, store, child environment, helper construction and git fill are real.
import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func programsIsolateGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	for _, name := range []string{"GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS"} {
		t.Setenv(name, "")
		require.NoError(t, os.Unsetenv(name))
	}
}

func programsWriteGit(t *testing.T, dir, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Fatal("BLOCKED: this process-edge git fixture requires a POSIX executable; native Windows git executable acceptance is required by FR014/S87/S88 and is not established by a .cmd surrogate")
	}
	require.NoError(t, os.MkdirAll(dir, 0700))
	path := filepath.Join(dir, "git")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0700))
	return path
}

func TestProgramGitHelper_ActualChild(t *testing.T) {
	t.Run("S19_git_uses_stored_token_ignores_existing_helper", func(t *testing.T) {
		programsIsolateGit(t)
		_, err := exec.LookPath("git")
		require.NoError(t, err, "actual git is a fixture prerequisite")
		f := newProgramsTurnFixture(t)
		work := filepath.Join(f.home, "workspaces", "sales", "work")
		require.NoError(t, os.MkdirAll(work, 0700))
		log := filepath.Join(work, "old-helper-operations")
		helper := filepath.Join(work, "old-helper")
		if runtime.GOOS == "windows" {
			t.Fatal("BLOCKED: S19's current external helper fixture is POSIX; the required native Windows git-helper exercise remains unimplemented in this test harness")
		}
		body := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$1\" >> %q\n[ \"$1\" = get ] || exit 0\nprintf 'username=old-account\\npassword=old_pw_123456\\n'\n", log)
		require.NoError(t, os.WriteFile(helper, []byte(body), 0700))
		// The runtime may supply its own per-turn HOME. Its concrete path is
		// fixture plumbing, not an oracle; install the specified existing
		// global helper where this very child will actually read it.
		f.al.GetConfig().Agents.List[0].Programs = nil
		childHome := programsForeground(t, f, "s19-child-home", "printf '%s' \"$HOME\"")
		rel, err := filepath.Rel(f.home, childHome)
		require.NoError(t, err)
		require.NotEqual(t, "..", rel)
		require.False(t, strings.HasPrefix(rel, ".."+string(filepath.Separator)), "never write outside the isolated fixture home")
		require.True(t, filepath.IsAbs(childHome), "the actual child exposes a concrete isolated home")
		require.NoError(t, os.MkdirAll(childHome, 0700))
		global := filepath.Join(childHome, ".gitconfig")
		require.NoError(t, os.WriteFile(global, []byte(fmt.Sprintf("[credential \"https://github.com\"]\n\thelper = %s\n", helper)), 0600))
		command := "printf 'protocol=https\\nhost=github.com\\n\\n' | git credential fill"
		// With the switch off, this exact helper must answer: otherwise an
		// empty log with programs on could be a vacuous environment mistake.
		control := programsForeground(t, f, "s19-helper-control", command)
		require.Contains(t, control, "username=old-account\npassword=old_pw_123456")
		raw, err := os.ReadFile(log)
		require.NoError(t, err)
		require.Equal(t, "get\n", string(raw), "instrument: the existing helper is really active")
		require.NoError(t, os.WriteFile(log, nil, 0600))
		f.al.GetConfig().Agents.List[0].Programs = []string{"github"}
		result := programsForeground(t, f, "s19-managed-fill", command)
		fields := map[string]string{}
		for _, line := range strings.Split(result, "\n") {
			key, value, ok := strings.Cut(line, "=")
			require.True(t, ok, "actual git fill returns credential fields, not a tool denial or wrapper")
			_, duplicate := fields[key]
			require.False(t, duplicate, "git returned each credential field once")
			fields[key] = value
		}
		require.Equal(t, map[string]string{"protocol": "https", "host": "github.com", "username": "x-access-token", "password": "[FILTERED]"}, fields, "S19: actual git gets the managed account and token; delivered value is masked, field order is not prescribed")
		// FR013's get-only contract also applies to git's store/erase calls.
		operations := "printf 'protocol=https\\nhost=github.com\\nusername=x-access-token\\npassword=unused\\n\\n' | git credential approve; printf 'protocol=https\\nhost=github.com\\n\\n' | git credential reject; printf 'S19_OPERATIONS_RAN'"
		require.Equal(t, "S19_OPERATIONS_RAN", programsForeground(t, f, "s19-no-persist", operations))
		raw, err = os.ReadFile(log)
		require.NoError(t, err)
		require.Empty(t, string(raw), "S19/FR013: the reset chain never invokes the old helper for get, store or erase")
		configText, err := os.ReadFile(global)
		require.NoError(t, err)
		require.NotContains(t, string(configText), "tok_valid_12345", "the managed login did not write its token to the config file")
	})
}

func TestProgramGitVersion_ActualChild(t *testing.T) {
	// S88's unchanged git-push/environment oracle is executable at the real
	// ExecTool/Resolve/probe/Attach/child boundary in
	// pkg/tools::TestProgramGitEnv_RealExecBoundary. The chief authorized that
	// explicit boundary after the full-loop fixture hit the evidence guard;
	// no guard is disabled, and full-loop project admission remains UAT.
	t.Run("S87_explicit_git_path_is_the_one_probed", func(t *testing.T) {
		programsIsolateGit(t)
		f := newProgramsTurnFixture(t)
		work := filepath.Join(f.home, "workspaces", "sales", "work")
		require.NoError(t, os.MkdirAll(work, 0700))
		probeLog := filepath.Join(work, "s87-old-git-calls")
		old := programsWriteGit(t, filepath.Join(work, "old"), fmt.Sprintf(`if [ "$#" -eq 1 ] && [ "$1" = --version ]; then printf 'probe\n' >> %q; printf 'git version 2.30.2\n'; exit 0; fi
[ "$#" -eq 1 ] && [ "$1" = push ] || exit 81
[ "${GIT_CONFIG_COUNT+x}" != x ] || exit 82
printf 'run\n' >> %q
printf 'S87_OLD_GIT_EXECUTED'
`, probeLog, probeLog))
		bin := t.TempDir()
		programsWriteGit(t, bin, "printf 'git version 2.43.0\\n'\n")
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		control, err := exec.Command("git", "--version").Output()
		require.NoError(t, err)
		require.Equal(t, "git version 2.43.0", strings.TrimSpace(string(control)), "instrument: PATH git is not the old explicit executable")
		result := programsForeground(t, f, "s87-explicit-old-git", fmt.Sprintf("%q push", old))
		require.Contains(t, result, "S87_OLD_GIT_EXECUTED", "the ordinary old-git command still runs without managed entries")
		require.Contains(t, result, "Connected git logins were not used: git 2.30.2 is too old. Update git to 2.31 or later.")
		raw, err := os.ReadFile(probeLog)
		require.NoError(t, err)
		require.Equal(t, "probe\nrun\n", string(raw), "S87: the explicit executable, not PATH git, was probed before the actual push")
	})
}
