package agent

// H55 v2 (#1960 S1), row R9 of the architect's second ruling
// (/workspace/logs/1960/spec-s1/ruling-H55-v2.md, section (3)) and finding F1 of the
// first security review: the bypass that needs no override text. The agent's own
// global git settings switch off git's refusal to move the checked-out branch of
// the evidence repository, and a plain push to a plain remote name then moves it.
// The ruling's answer is structural: the evidence repository pins its own receive
// settings in its local config, which ranks above every global source. The guard
// may admit these commands, but git itself must refuse the push.
//
// Oracles, none of them taken from a run of the code:
//   - ruling-H55-v2 section (3) row R9: the three calls from the mount (remote add,
//     global receive setting, push), the GIT_CONFIG_GLOBAL variant and the global
//     url insteadOf variant; "The guard may admit them, but git refuses the push.
//     Evidence HEAD, refs and history are unchanged (FR-152). RED before the pins";
//   - ruling-H55-v2 section (1): local config beats global, system,
//     GIT_CONFIG_GLOBAL and an insteadOf target; HOME is not pinned for the shell;
//   - FR-152 / ADR-053 D17: the evidence repository stays byte-identical.
//
// The loop, registered bash tool, installed Git, real go-git evidence commits (and
// the pins their Open writes) and operator write mount are real; only the paid
// model and the account check are the established fixture substitutes. Each row
// builds its own fixture. Every row ends with a replay of the push by installed Git
// outside the loop and the guard, in the same state, so that "git refuses" is read
// from git itself and not inferred from the guard's text.
import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Git's refusal of an update to the checked-out branch names that branch's state
// ("refusing to update checked out branch"); R6 reads the same wording.
const programsGitH55V2GitRefusal = "checked out branch"

// programsGitH55V2Fixture is the H55 fixture with a project that is a fast-forward
// of the evidence HEAD (so a push that git lets through really moves it) and the
// HOME of the loop's bash child, where the agent's global git config lives.
type programsGitH55V2Fixture struct {
	programsGitH55Fixture
	childHome string
}

func programsGitH55V2Start(t *testing.T) programsGitH55V2Fixture {
	t.Helper()
	x := programsGitH55Start(t)
	x.forgedDescendant(t)
	home := strings.TrimSpace(x.turn(t, "h55v2-child-home", `printf '%s' "$HOME"`))
	require.True(t, filepath.IsAbs(home), "fixture: the child's HOME is an absolute path, got %q", home)
	rel, err := filepath.Rel(x.f.home, home)
	require.NoError(t, err)
	require.NotEqual(t, "..", rel)
	require.False(t, strings.HasPrefix(rel, ".."+string(filepath.Separator)), "never write outside the isolated fixture home")
	require.NoError(t, os.MkdirAll(home, 0700))
	return programsGitH55V2Fixture{programsGitH55Fixture: x, childHome: home}
}

// globalFile is the child's own global git config, the file "git config --global"
// writes.
func (x programsGitH55V2Fixture) globalFile() string {
	return filepath.Join(x.childHome, ".gitconfig")
}

// callCompleted runs one admitted setup call of the attack and requires that the
// loop really ran it: the marker prints only if the command exited 0.
func (x programsGitH55V2Fixture) callCompleted(t *testing.T, id, script string) {
	t.Helper()
	result := x.attack(t, id, script)
	require.Contains(t, result, programsGitH55Done, "instrument: the setup call %q must have been admitted and completed, or the row cannot show that git (and not the guard) stops the push; tool result=%q", script, result)
}

// globalValue reads one key of a config file with installed Git; "" if absent.
func (x programsGitH55V2Fixture) globalValue(t *testing.T, file, key string) string {
	t.Helper()
	cmd := exec.Command(x.bin, "config", "--file", file, "--get", key)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + x.f.home, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, "git config could not run: %s", out)
	require.Equal(t, 1, exit.ExitCode(), "git config --get exits 1 for a missing key: %s", out)
	return ""
}

// replay runs the attack push with installed Git directly in the mount target, with
// the same HOME and extra environment the loop's child has: no loop, no guard.
func (x programsGitH55V2Fixture) replay(t *testing.T, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(x.bin, args...)
	cmd.Dir = x.env.project
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + x.childHome, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"}, env...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return strings.TrimSpace(string(out)), 0
	}
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, "git could not run: %s", out)
	return strings.TrimSpace(string(out)), exit.ExitCode()
}

// assertGitRefuses is the second half of every R9 row: installed Git, given the
// same global state and the same push, refuses it because the target is the
// checked-out branch, and the evidence repository is still exactly as it was.
func (x programsGitH55V2Fixture) assertGitRefuses(t *testing.T, env []string, args ...string) {
	t.Helper()
	out, code := x.replay(t, env, args...)
	assert.NotZero(t, code, "ruling-H55-v2 R9: git must refuse the push; output=%q", out)
	assert.Contains(t, out, programsGitH55V2GitRefusal, "ruling-H55-v2 R9: git refuses because the target is the checked-out branch, not for an unrelated reason; output=%q", out)
	assert.True(t, x.evidenceUnchanged(t), "FR-152: HEAD, every ref, reachable history and tracked evidence bytes must be unchanged after the replay")
}

// R9, the three calls of finding F1: add a remote that leads to the evidence
// repository, switch the receive-side refusal off in the global config, push.
func TestProgramGitHelper_MountedPushH55V2_GlobalConfigBypassRefusedByGit(t *testing.T) {
	t.Run("R9_three_calls_global_receive_setting", func(t *testing.T) {
		x := programsGitH55V2Start(t)
		push := "git push ev HEAD:" + x.evidenceBranch

		x.callCompleted(t, "h55v2-remote-add", "git remote add ev ../work")
		require.Equal(t, "../work", x.project(t, "remote", "get-url", "ev"), "instrument: call 1 added the remote the ruling names")
		x.control(t, "ev")

		x.callCompleted(t, "h55v2-global-setting", "git config --global receive.denyCurrentBranch ignore")
		require.Equal(t, "ignore", x.globalValue(t, x.globalFile(), "receive.denyCurrentBranch"), "instrument: call 2 switched the refusal off in the child's global config")

		x.assertHolds(t, x.attack(t, "h55v2-push", push))
		x.assertGitRefuses(t, nil, "push", "ev", "HEAD:"+x.evidenceBranch)
	})

	// The ruling's variant: the global file is not the child's own but one named by
	// GIT_CONFIG_GLOBAL, setting the refusal off and core.bare to true.
	t.Run("R9_global_config_file_variant_ignore_and_bare", func(t *testing.T) {
		x := programsGitH55V2Start(t)
		file := filepath.Join(x.env.project, "h55v2-global.cfg")
		require.NoError(t, os.WriteFile(file, []byte("[receive]\n\tdenyCurrentBranch = ignore\n[core]\n\tbare = true\n"), 0600))
		require.Equal(t, "ignore", x.globalValue(t, file, "receive.denyCurrentBranch"), "instrument: the file holds the setting the ruling names")
		require.Equal(t, "true", x.globalValue(t, file, "core.bare"), "instrument: the file holds core.bare=true")

		x.callCompleted(t, "h55v2-remote-add", "git remote add ev ../work")
		x.control(t, "ev")

		x.assertHolds(t, x.attack(t, "h55v2-push", "GIT_CONFIG_GLOBAL="+file+" git push ev HEAD:"+x.evidenceBranch))
		x.assertGitRefuses(t, []string{"GIT_CONFIG_GLOBAL=" + file}, "push", "ev", "HEAD:"+x.evidenceBranch)
	})

	// The ruling's other variant: a global url.<work>.insteadOf makes the plain name
	// zz lead to the evidence repository, so nothing in the pushed command names it.
	// The global config also switches the refusal off, as in the other rows; the
	// guard has no marker and no path to read, so only git's own refusal is left.
	t.Run("R9_global_insteadof_variant_with_the_setting", func(t *testing.T) {
		x := programsGitH55V2Start(t)
		for _, kv := range [][2]string{{"url." + x.env.work + ".insteadOf", "zz"}, {"receive.denyCurrentBranch", "ignore"}} {
			programsGitSetup(t, x.bin, x.f.home, "", "config", "--file", x.globalFile(), kv[0], kv[1])
		}
		require.Equal(t, "zz", x.globalValue(t, x.globalFile(), "url."+x.env.work+".insteadOf"), "instrument: the child's global config maps zz to the work folder")
		x.control(t, "zz")

		x.assertHolds(t, x.attack(t, "h55v2-push", "git push zz HEAD:"+x.evidenceBranch))
		x.assertGitRefuses(t, nil, "push", "zz", "HEAD:"+x.evidenceBranch)
	})
}
