package agent

// H54 (#1960 S1): ordinary git work from an approved write mount is not an
// evidence-repository mutation, and an unresolvable-path mutation that really
// reaches the evidence repository still is. Spec sources, none of them the
// guard's own unit pack:
//   - ruling-H48-H46 S-20 (option B1): a push from the mount succeeds, the
//     evidence guard "stays as strict as today", and the same git push from the
//     workspace folder is still refused by the evidence-repository guard;
//   - FR-152 / ADR-053 D17: <workspace>/work/.git is engine-owned, so a command
//     that would change it is refused with the evidence-repository text and the
//     repository stays byte-identical;
//   - ADR-063 D4 and its 2026-11-24 addendum: the mount grants write, and a bash
//     cwd inside it is admitted.
//
// The loop, registered bash tool, installed Git, real go-git evidence commits and
// operator mount are real; only the paid model and the account check are the
// established fixture substitutes. Every row runs in its own turn.
import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	programsGitH54Done    = "H54_COMMAND_COMPLETED"
	programsGitH54Refusal = "protected evidence repo"
)

func TestProgramGitHelper_MountedIndirectForms(t *testing.T) {
	// ALLOWED: the effect is read back from the project or the receiver.
	for _, row := range []struct{ name, script, subject string }{
		{"H54_allowed_commit_message_question_mark", `git commit -m "why?"`, "why?"},
		{"H54_allowed_commit_message_variable", `MSG='H54 message from a variable'; git commit -m "$MSG"`, "H54 message from a variable"},
	} {
		t.Run(row.name, func(t *testing.T) { programsGitH54AllowedCommit(t, row.script, row.subject) })
	}

	t.Run("H54_allowed_push_branch_variable", func(t *testing.T) {
		p := programsGitH54RunPush(t, `BRANCH=h54-branch; git push origin "$BRANCH" && printf '`+programsGitH54Done+`'`, "h54-branch")
		p.assertAdmitted(t)
		assert.Equal(t, "refs/heads/h54-branch "+p.commits["h54-branch"], p.refs(t),
			"S-20: the receiver holds exactly the pushed branch at the project's commit, and nothing else")
	})

	// A fully qualified one-sided glob is the spelling installed Git really pushes.
	t.Run("H54_allowed_push_glob_refspec_qualified", func(t *testing.T) {
		p := programsGitH54RunPush(t, `git push origin 'refs/heads/feat/*' && printf '`+programsGitH54Done+`'`, "feat/h54-a", "feat/h54-b")
		p.assertAdmitted(t)
		assert.Equal(t, "refs/heads/feat/h54-a "+p.commits["feat/h54-a"]+"\nrefs/heads/feat/h54-b "+p.commits["feat/h54-b"], p.refs(t),
			"S-20: the quoted glob pushes exactly the feat/* branches at the project's commits")
	})

	// The brief's literal form. Installed Git (2.43.0 when this row was written)
	// reads 'feat/*' as matching no ref and pushes nothing, so no effect is
	// asserted here: this row proves only that the guard admits the quoted glob
	// and the real git runs it (marker after ";" prints only if the script ran).
	t.Run("H54_allowed_push_glob_refspec_literal_form", func(t *testing.T) {
		p := programsGitH54RunPush(t, `git push origin 'feat/*'; printf '`+programsGitH54Done+`'`, "feat/h54-a", "feat/h54-b")
		p.assertAdmitted(t)
		t.Logf("observation, not an oracle: git's own result for the literal glob is %q; receiver refs %q", p.result, p.refs(t))
	})

	// REFUSED: every form really reaches the evidence repository if it is let through.
	for _, row := range []struct {
		name, control, attack string
		wantLog               bool
	}{
		{"H54_refused_C_command_substitution_commit",
			`git -C "$(dirname $PWD)/work" log --format=%H`,
			`git -C "$(dirname $PWD)/work" commit --allow-empty -m H54_FORGED`, true},
		{"H54_refused_cd_variable_reset",
			`W=$(dirname $PWD)/work; cd $W && git rev-parse HEAD`,
			`W=$(dirname $PWD)/work; cd $W && git reset --hard`, false},
		{"H54_refused_cd_parent_then_work_reset",
			`cd .. && cd work && git rev-parse HEAD`,
			`cd .. && cd work && git reset --hard`, false},
	} {
		t.Run(row.name, func(t *testing.T) { programsGitH54Refused(t, row.control, row.attack, row.wantLog) })
	}
}

// programsGitH54Fixture is the mounted-evidence fixture plus its evidence baseline.
type programsGitH54Fixture struct {
	bin    string
	f      *wu5Fixture
	env    programsGitMountedEvidence
	before programsGitEvidenceSnapshot
}

// programsGitH54Start builds the actual-loop fixture (three real evidence commits
// registered on the process-wide guard, an operator write mount) and records the
// evidence baseline. dirty leaves an uncommitted tracked evidence file so a hard
// reset would visibly change it.
func programsGitH54Start(t *testing.T, dirty bool) programsGitH54Fixture {
	t.Helper()
	bin, f, env := programsGitMountedEvidenceStart(t)
	if dirty {
		require.NoError(t, os.WriteFile(filepath.Join(env.work, programsGitEvidenceFiles[2]), []byte("H54 uncommitted evidence must survive\n"), 0600))
	}
	tracked := programsGitSetup(t, bin, f.home, env.work, "diff", "--name-only", "HEAD")
	if dirty {
		require.Equal(t, programsGitEvidenceFiles[2], tracked, "fixture: a tracked evidence file carries uncommitted work")
	} else {
		require.Empty(t, tracked, "fixture: the tracked evidence tree is clean")
	}
	before := programsGitEvidenceState(t, bin, f.home, env.work)
	require.Equal(t, env.hashes[2], before.head)
	require.Contains(t, before.refs, env.hashes[2], "the ref reader must observe the real seeded HEAD")
	for _, hash := range env.hashes {
		require.Contains(t, before.history, hash, "the history reader must observe every real boundary commit")
	}
	return programsGitH54Fixture{bin: bin, f: f, env: env, before: before}
}

func (x programsGitH54Fixture) turn(t *testing.T, id, script string) string {
	t.Helper()
	return programsGitMountedTurn(t, x.f, id, x.env.mount, script)
}

func (x programsGitH54Fixture) project(t *testing.T, args ...string) string {
	t.Helper()
	return programsGitSetup(t, x.bin, x.f.home, x.env.project, args...)
}

func (x programsGitH54Fixture) evidenceUnchanged(t *testing.T) bool {
	t.Helper()
	return x.before == programsGitEvidenceState(t, x.bin, x.f.home, x.env.work)
}

// initProject makes the mount target an ordinary repository (not the evidence
// repository) with one base commit under the fixture identity; returns that commit.
func (x programsGitH54Fixture) initProject(t *testing.T) string {
	t.Helper()
	programsGitSetup(t, x.bin, x.f.home, "", "init", x.env.project)
	require.NoError(t, os.WriteFile(filepath.Join(x.env.project, "h54-base.txt"), []byte("H54 base\n"), 0600))
	x.project(t, "add", "h54-base.txt")
	x.project(t, "-c", "user.name="+programsGitF1Name, "-c", "user.email="+programsGitF1Email, "commit", "-m", "H54 base")
	return x.project(t, "rev-parse", "HEAD")
}

// branch creates a local branch off base holding one distinct commit and returns it.
func (x programsGitH54Fixture) branch(t *testing.T, name, base string) string {
	t.Helper()
	x.project(t, "checkout", "-q", "-b", name, base)
	file := strings.ReplaceAll(name, "/", "-") + ".txt"
	require.NoError(t, os.WriteFile(filepath.Join(x.env.project, file), []byte("H54 branch "+name+"\n"), 0600))
	x.project(t, "add", file)
	x.project(t, "-c", "user.name="+programsGitF1Name, "-c", "user.email="+programsGitF1Email, "commit", "-m", "H54 "+name)
	return x.project(t, "rev-parse", "HEAD")
}

// armGuard proves inside this very fixture that the evidence guard is registered
// and refusing (the literal-path form F1 pins); without it an "allowed" result
// could come from a guard that was never armed.
func (x programsGitH54Fixture) armGuard(t *testing.T) {
	t.Helper()
	result := x.turn(t, "h54-armed-guard", "git -C "+x.env.work+" commit --allow-empty -m H54_ARMED && printf '"+programsGitH54Done+"'")
	require.Contains(t, result, programsGitH54Refusal, "instrument: the evidence guard must refuse the literal-path form in this fixture, or an allowed row proves nothing")
	require.NotContains(t, result, programsGitH54Done)
	require.True(t, x.evidenceUnchanged(t), "instrument: the armed-guard control must leave the evidence repository unchanged")
}

func programsGitH54AllowedCommit(t *testing.T, script, subject string) {
	t.Helper()
	x := programsGitH54Start(t, false)
	base := x.initProject(t)
	require.NoError(t, os.WriteFile(filepath.Join(x.env.project, "h54-staged.txt"), []byte("H54 staged change\n"), 0600))
	x.project(t, "add", "h54-staged.txt")
	x.armGuard(t)

	result := x.turn(t, "h54-allowed-commit", script+" && printf '"+programsGitH54Done+"'")
	refused := strings.Contains(result, programsGitH54Refusal)
	assert.False(t, refused, "ruling S-20 / H54: a commit in the mounted project is not an evidence-repository mutation (%q)", result)
	assert.Contains(t, result, programsGitH54Done, "the real git commit must run to completion")
	assert.Equal(t, "2", x.project(t, "rev-list", "--count", "HEAD"), "exactly one new commit on top of the base commit %s", base)
	assert.Equal(t, subject, x.project(t, "log", "-1", "--format=%s"), "the commit carries the message the shell expanded")
	assert.Equal(t, "h54-staged.txt", x.project(t, "show", "--name-only", "--format=", "HEAD"), "the commit holds the staged change")
	assert.Equal(t, programsGitF1Name+" <"+programsGitF1Email+">", x.project(t, "log", "-1", "--format=%an <%ae>"), "the child's identity authored it")
	unchanged := x.evidenceUnchanged(t)
	assert.True(t, unchanged, "FR-152: a project commit must not touch the evidence repository")
	t.Logf("H54 actual registered loop: protected_refusal=%t marker=%t evidence_unchanged=%t", refused, strings.Contains(result, programsGitH54Done), unchanged)
}

// programsGitH54Push is one admitted-push turn with the project's branches and
// the receiver read back independently of the shell.
type programsGitH54Push struct {
	x       programsGitH54Fixture
	result  string
	remote  string
	commits map[string]string
}

func programsGitH54RunPush(t *testing.T, script string, branches ...string) programsGitH54Push {
	t.Helper()
	x := programsGitH54Start(t, false)
	base := x.initProject(t)
	commits := map[string]string{}
	for _, name := range branches {
		commits[name] = x.branch(t, name, base)
	}
	remote := filepath.Join(x.env.project, "h54-remote-bare")
	programsGitSetup(t, x.bin, x.f.home, "", "init", "--bare", remote)
	x.project(t, "remote", "add", "origin", remote)
	x.armGuard(t)
	return programsGitH54Push{x: x, result: x.turn(t, "h54-allowed-push", script), remote: remote, commits: commits}
}

func (p programsGitH54Push) refs(t *testing.T) string {
	t.Helper()
	return programsGitSetup(t, p.x.bin, p.x.f.home, "", "--git-dir", p.remote, "for-each-ref", "--sort=refname", "--format=%(refname) %(objectname)")
}

func (p programsGitH54Push) assertAdmitted(t *testing.T) {
	t.Helper()
	refused := strings.Contains(p.result, programsGitH54Refusal)
	assert.False(t, refused, "ruling S-20 / H54: a push from the approved mount is not an evidence-repository mutation (%q)", p.result)
	assert.Contains(t, p.result, programsGitH54Done, "the script must have been admitted and run")
	unchanged := p.x.evidenceUnchanged(t)
	assert.True(t, unchanged, "FR-152: a mounted push must not touch the evidence repository")
	t.Logf("H54 actual registered loop: protected_refusal=%t marker=%t evidence_unchanged=%t", refused, strings.Contains(p.result, programsGitH54Done), unchanged)
}

// programsGitH54Refused runs one unresolvable-path mutation from the mount. The
// loop's bash child starts in the mount target's physical directory (observed:
// $PWD equals the resolved target, not <workspace>/work/<mount>), so the parent
// of $PWD is that directory's parent and "work" next to it is just a name. A
// "work" entry placed there that leads to the evidence repository makes each form
// GENUINELY reach it when not refused, which puts the cwd beside the work dir the
// way the guard's own unit pack does (<ws>/m next to <ws>/work); without it a
// refusal could not be told from a command that merely fails at a path that was
// never the evidence repository. The control reads through the same route with a
// read-only verb, so the refusal is attributable to the guard.
func programsGitH54Refused(t *testing.T, control, attack string, wantLog bool) {
	t.Helper()
	x := programsGitH54Start(t, true)
	require.NoError(t, os.Symlink(x.env.work, filepath.Join(filepath.Dir(x.env.project), "work")))

	want := x.env.hashes[2]
	if wantLog {
		want = strings.Join([]string{x.env.hashes[2], x.env.hashes[1], x.env.hashes[0]}, "\n")
	}
	read := x.turn(t, "h54-indirect-read", control)
	require.Equal(t, want, strings.TrimSpace(read), "the indirect route from the admitted mount really reads the evidence repository")
	identity := x.turn(t, "h54-admitted-identity", "git config --get user.email")
	require.Equal(t, programsGitF1Email, strings.TrimSpace(identity), "the child Git identity is usable, so an unrefused commit would really succeed")
	require.True(t, x.evidenceUnchanged(t), "harmless main-session turns must not change the evidence repository")

	result := x.turn(t, "h54-indirect-attack", attack+" && printf '"+programsGitH54Done+"'")
	refused := strings.Contains(result, programsGitH54Refusal)
	assert.True(t, refused, "FR-152 / S-20 'as strict as today': an unresolvable path that reaches the evidence repository is refused with the evidence-repository text (%q)", result)
	assert.NotContains(t, result, programsGitH54Done, "the mutating Git command must not run successfully")
	unchanged := x.evidenceUnchanged(t)
	assert.True(t, unchanged, "FR-152: HEAD, every ref, reachable history and tracked evidence bytes must stay unchanged")
	t.Logf("H54 actual registered loop: protected_refusal=%t marker_absent=%t evidence_unchanged=%t", refused, !strings.Contains(result, programsGitH54Done), unchanged)
}
