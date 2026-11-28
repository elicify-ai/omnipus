package agent

// H55 (#1960 S1), rows R1-R5 of the architect's ruling
// (/workspace/logs/1960/spec-s1/ruling-H55.md): a push from an approved write
// mount must not write into the engine-owned evidence repository
// <workspace>/work/.git.
//
// EXPECTED RED until the guard fix of that ruling (its Rule A, the receive-side
// override, and its Rule B, the push target) lands. These rows were written
// before the fix exists. Two leaves here are RED and fail only on what the
// product does today (the evidence guard admits the command and the evidence
// repository changes), never on a compile or setup error:
//   - TestProgramGitHelper_MountedPushH55_ReceiveSideOverrideRefused (R1-R3);
//   - TestProgramGitHelper_MountedPushH55_PathIntoWorkFolderRefused (R4 and the
//     file:// form of R5).
//
// The third leaf, TestProgramGitHelper_MountedPushH55_LiteralWorkPathPushStaysRefused,
// is NOT red: the absolute and the relative work path are already refused today by
// the guard's literal-path check, so those two R5 rows are regression pins and sit
// in a leaf of their own. The round-8 entry of the test disputes names every row
// with its saved run log.
//
// Oracles, none of them taken from a run of the code:
//   - FR-152 / ADR-053 D17: <workspace>/work/.git is engine-owned; a command that
//     would change it is refused with the evidence-repository text and HEAD, every
//     ref, reachable history and tracked evidence bytes stay unchanged;
//   - ruling-H55 section (3): which commands are refused (R1-R5) and that every
//     row asserts that state unchanged.
//
// The loop, registered bash tool, installed Git, real go-git evidence commits and
// operator write mount are real; only the paid model and the account check are
// the established fixture substitutes (same as the H54 rows). The rows that must
// keep working are in programs_git_mount_h55_controls_test.go.
import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	programsGitH55Done = "H55_COMMAND_COMPLETED"
	// The evidence guard wraps every refusal, whatever rule made it, in this
	// standard text (sandbox.ErrGitEvidenceProtected). The armed-guard control in
	// programsGitH55Start shows it in a real refusal of this fixture, so a missing
	// text cannot be mistaken for a refusal.
	programsGitH55GuardRefusal = "protected git-evidence repository"
	// The receiving-side program named by the ruling's rows R1-R3.
	programsGitH55Program = "git -c receive.denyCurrentBranch=ignore receive-pack"
	// The path the ruling's rows compute from the mount: the parent of the mount
	// target plus "work" (a symlink to the evidence work folder in the fixture).
	programsGitH55Computed = `"$(dirname $PWD)/work"`
)

type programsGitH55Fixture struct {
	programsGitH54Fixture
	// evidenceBranch is the evidence repository's checked-out branch, read from git.
	evidenceBranch string
}

// programsGitH55Start builds the H54 mounted-evidence fixture (three real
// evidence commits on the process-wide guard, an operator write mount), makes
// "<parent of the mount target>/work" lead to the evidence repository so a
// computed path from the mount really reaches it, and shows in this very fixture
// that the guard is armed and what its refusal looks like.
func programsGitH55Start(t *testing.T) programsGitH55Fixture {
	t.Helper()
	x := programsGitH54Start(t, false)
	require.NoError(t, os.Symlink(x.env.work, filepath.Join(filepath.Dir(x.env.project), "work")))
	branch := programsGitSetup(t, x.bin, x.f.home, x.env.work, "symbolic-ref", "--short", "HEAD")
	require.NotEmpty(t, branch, "fixture: the evidence repository has a checked-out branch")
	h := programsGitH55Fixture{programsGitH54Fixture: x, evidenceBranch: branch}

	armed := x.turn(t, "h55-armed-guard", "git -C "+x.env.work+" commit --allow-empty -m H55_ARMED && printf '"+programsGitH55Done+"'")
	require.Contains(t, armed, programsGitH54Refusal, "instrument: the evidence guard must refuse the literal-path form in this fixture")
	require.Contains(t, armed, programsGitH55GuardRefusal, "instrument: the guard's standard refusal text must be what the model sees, or no refusal below can be recognised")
	require.NotContains(t, armed, programsGitH55Done)
	require.True(t, x.evidenceUnchanged(t), "instrument: the armed-guard control must leave the evidence repository unchanged")
	return h
}

// unrelated gives the mount target a repository whose HEAD shares no history with
// the evidence repository.
func (x programsGitH55Fixture) unrelated(t *testing.T) {
	t.Helper()
	x.branch(t, "h55-src", x.initProject(t))
}

// forgedDescendant makes the mount target a clone of the evidence repository with
// one empty commit on top of its real HEAD, so a push of it is a fast-forward.
func (x programsGitH55Fixture) forgedDescendant(t *testing.T) {
	t.Helper()
	programsGitSetup(t, x.bin, x.f.home, "", "clone", "-q", x.env.work, x.env.project)
	require.Equal(t, x.evidenceBranch, x.project(t, "symbolic-ref", "--short", "HEAD"), "fixture: the clone sits on the evidence branch")
	x.project(t, "-c", "user.name="+programsGitF1Name, "-c", "user.email="+programsGitF1Email, "commit", "--allow-empty", "-m", "H55 forged descendant")
	require.Equal(t, x.env.hashes[2], x.project(t, "rev-parse", "HEAD~1"), "fixture: the extra commit sits directly on the real history")
}

// addEvidenceRemote gives the mount target a remote named "ev" that is the
// evidence repository, so a row can reach it through a plain remote name.
func (x programsGitH55Fixture) addEvidenceRemote(t *testing.T) {
	t.Helper()
	x.project(t, "remote", "add", "ev", x.env.work)
}

// control reads the evidence repository through the same remote operand a row
// pushes to, with a read-only command: the operand really leads to the evidence
// repository, so a refusal cannot be a push that was going nowhere.
func (x programsGitH55Fixture) control(t *testing.T, operand string) {
	t.Helper()
	read := x.turn(t, "h55-route-control", "git ls-remote "+operand)
	require.Contains(t, read, x.env.hashes[2]+"\trefs/heads/"+x.evidenceBranch, "instrument: the operand %s leads to the evidence repository", operand)
	require.NotContains(t, read, programsGitH55GuardRefusal, "instrument: a read through the route is not refused")
	require.True(t, x.evidenceUnchanged(t), "instrument: the read-only route control must not change the evidence repository")
}

// attack runs one script from the mount; the marker prints only if it completed.
func (x programsGitH55Fixture) attack(t *testing.T, id, script string) string {
	t.Helper()
	return x.turn(t, id, script+" && printf '"+programsGitH55Done+"'")
}

// assertHolds is the evidence half of every H55 row (FR-152): the command did not
// complete and HEAD, every ref, reachable history and tracked evidence bytes are
// exactly what they were.
func (x programsGitH55Fixture) assertHolds(t *testing.T, result string) {
	t.Helper()
	after := programsGitEvidenceState(t, x.bin, x.f.home, x.env.work)
	completed := strings.Contains(result, programsGitH55Done)
	t.Logf("H55 observation, not an oracle: completed=%t guard_text=%t head %s -> %s refs_changed=%t history_changed=%t",
		completed, strings.Contains(result, programsGitH55GuardRefusal), x.before.head, after.head, x.before.refs != after.refs, x.before.history != after.history)
	assert.False(t, completed, "ruling-H55: the command must not run to completion")
	assert.Equal(t, x.before.head, after.head, "FR-152: HEAD of the evidence repository must not move")
	assert.Equal(t, x.before.refs, after.refs, "FR-152: no ref of the evidence repository may be added, moved or removed")
	assert.Equal(t, x.before.history, after.history, "FR-152: reachable history must be byte-identical")
	assert.Equal(t, x.before.contents, after.contents, "FR-152: tracked evidence bytes must be unchanged")
}

// assertRefused adds the guard half: the evidence guard itself refused, with its
// standard text, before git ran (ruling-H55 rows R1-R5).
func (x programsGitH55Fixture) assertRefused(t *testing.T, result, rule string) {
	t.Helper()
	assert.Contains(t, result, programsGitH55GuardRefusal,
		"ruling-H55 %s: the evidence guard must refuse this command (the file header names the leaves that are expected RED until the ruling's fix lands); tool result=%q", rule, result)
	x.assertHolds(t, result)
}

// projectConfigHas reports whether the mount target's own git config holds key.
func (x programsGitH55Fixture) projectConfigHas(t *testing.T, key string) bool {
	t.Helper()
	cmd := exec.Command(x.bin, "config", "--local", "--get", key)
	cmd.Dir = x.env.project
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + x.f.home, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true
	}
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, "git config could not run: %s", out)
	require.Equal(t, 1, exit.ExitCode(), "git config --get exits 1 for a missing key: %s", out)
	return false
}

// R1, R2 and R3 of the ruling: a push that names its own receiving-side program
// is refused (Rule A), whichever way the program is named, and nothing changes.
func TestProgramGitHelper_MountedPushH55_ReceiveSideOverrideRefused(t *testing.T) {
	prog := programsGitH55Program
	rows := []struct {
		name    string
		setup   func(x programsGitH55Fixture, t *testing.T)
		operand string // the remote operand the push addresses (also read back as the control)
		script  func(branch string) string
	}{
		{"R1_force_push_of_an_unrelated_commit", programsGitH55Fixture.unrelated, programsGitH55Computed, func(b string) string {
			return "git push --force " + programsGitH55Computed + " HEAD:" + b + " --receive-pack='" + prog + "'"
		}},
		{"R2_push_of_a_forged_descendant", programsGitH55Fixture.forgedDescendant, programsGitH55Computed, func(b string) string {
			return "git push " + programsGitH55Computed + " HEAD:" + b + " --receive-pack='" + prog + "'"
		}},
		{"R3_receive_pack_glued", programsGitH55Fixture.unrelated, programsGitH55Computed, func(b string) string {
			return "git push --force --receive-pack='" + prog + "' " + programsGitH55Computed + " HEAD:" + b
		}},
		{"R3_receive_pack_spaced", programsGitH55Fixture.unrelated, programsGitH55Computed, func(b string) string {
			return "git push --force --receive-pack '" + prog + "' " + programsGitH55Computed + " HEAD:" + b
		}},
		{"R3_exec_glued", programsGitH55Fixture.unrelated, programsGitH55Computed, func(b string) string {
			return "git push --force --exec='" + prog + "' " + programsGitH55Computed + " HEAD:" + b
		}},
		{"R3_remote_receivepack_on_the_command_line", func(x programsGitH55Fixture, t *testing.T) {
			x.unrelated(t)
			x.addEvidenceRemote(t)
		}, "ev", func(b string) string {
			return "git -c remote.ev.receivepack='" + prog + "' push --force ev HEAD:" + b
		}},
		{"R3_send_pack_receive_pack", programsGitH55Fixture.unrelated, programsGitH55Computed, func(b string) string {
			return "git send-pack --force --receive-pack='" + prog + "' " + programsGitH55Computed + " HEAD:" + b
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			x := programsGitH55Start(t)
			row.setup(x, t)
			x.control(t, row.operand)
			x.assertRefused(t, x.attack(t, "h55-override-attack", row.script(x.evidenceBranch)), "Rule A")
		})
	}

	// R3, the setting made by its own call: the config call is refused, so the
	// later plain push has no receiving-side program to run and git refuses it.
	t.Run("R3_remote_receivepack_set_by_its_own_config_call", func(t *testing.T) {
		x := programsGitH55Start(t)
		x.unrelated(t)
		x.addEvidenceRemote(t)
		x.control(t, "ev")
		x.assertRefused(t, x.attack(t, "h55-config-call", "git config remote.ev.receivepack '"+programsGitH55Program+"'"), "Rule A")
		assert.False(t, x.projectConfigHas(t, "remote.ev.receivepack"), "ruling-H55 Rule A: the refused config call must not have been applied")
		x.assertHolds(t, x.attack(t, "h55-push-after-config", "git push --force ev HEAD:"+x.evidenceBranch))
	})
}

// programsGitH55PathRow is the body of one R4/R5 row: an unrelated commit of the
// mount target is pushed to a new branch of the evidence repository, which the push
// remote reaches through the operand. The route control runs first, and the row ends
// with the evidence-state half of the ruling (FR-152).
func programsGitH55PathRow(t *testing.T, operand func(x programsGitH55Fixture) string) {
	t.Helper()
	x := programsGitH55Start(t)
	x.unrelated(t)
	remote := operand(x)
	x.control(t, remote)
	x.assertRefused(t, x.attack(t, "h55-path-attack", "git push "+remote+" HEAD:refs/heads/x"), "Rule B")
}

type programsGitH55PathCase struct {
	name    string
	operand func(x programsGitH55Fixture) string
}

// R4 and the file:// form of R5 of the ruling: a push whose remote is a path built
// at run time, or a file:// URL of the work folder, is refused (Rule B), and no ref
// or object is added. RED today: the guard does not look at a push remote beyond a
// literal or relative path.
func TestProgramGitHelper_MountedPushH55_PathIntoWorkFolderRefused(t *testing.T) {
	rows := []programsGitH55PathCase{
		{"R4_computed_path", func(programsGitH55Fixture) string { return programsGitH55Computed }},
		{"R5_file_url", func(x programsGitH55Fixture) string { return "file://" + x.env.work }},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) { programsGitH55PathRow(t, row.operand) })
	}
}

// R5, the absolute and the relative path of the work folder. NOT red: the guard's
// literal-path check already refuses both today, so these two rows are regression
// pins that must still hold once Rule B lands (the ruling lists them under Rule B;
// a fix that replaced the literal-path check and missed them would turn this leaf
// red).
func TestProgramGitHelper_MountedPushH55_LiteralWorkPathPushStaysRefused(t *testing.T) {
	rows := []programsGitH55PathCase{
		{"R5_absolute_work_path", func(x programsGitH55Fixture) string { return x.env.work }},
		{"R5_relative_path", func(programsGitH55Fixture) string { return "../work" }},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) { programsGitH55PathRow(t, row.operand) })
	}
}
