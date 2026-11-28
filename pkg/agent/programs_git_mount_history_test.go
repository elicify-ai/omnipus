package agent

// H48-F1 / ADR-053 D17 (FR-152): admitting a bash cwd inside an operator write
// mount (ADR-063 D4, H48 B1) must not admit mutations of the engine-owned
// evidence repository <workspace>/work/.git. The loop, registered bash tool,
// installed Git, operator mount and evidence history are real; only the paid
// model and account-check edges are the established fixture substitutes.
//
// Source of the five attack forms: security-s1-2/h48-cwd-review.md F1. The
// backend's own guard-unit rows (relative, glued and quoted spellings) are a
// separate pack; this file is the actual-loop regression the chief requested.
import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/gitevidence"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	programsGitF1Name  = "H48 F1 Fixture"
	programsGitF1Email = "f1-fixture@example.invalid"
	programsGitF1Done  = "H48_F1_COMMAND_COMPLETED"
)

// Three distinct files, one per engine boundary commit, so reset visibly
// changes the working tree and rebase --onto rewrites without conflicts.
var programsGitEvidenceFiles = [...]string{"f1-evidence-1.txt", "f1-evidence-2.txt", "f1-evidence-3.txt"}

func TestProgramGitHelper_MountedEvidenceHistory(t *testing.T) {
	rows := []struct {
		name, script string
		dirty        bool
	}{
		{"F1_git_C_commit_refused", "git -C $WORK commit --allow-empty -m H48_F1_FORGED", false},
		{"F1_git_C_reset_refused", "git -C $WORK reset --hard $FIRST", true},
		{"F1_git_C_update_ref_refused", "git -C $WORK update-ref refs/heads/h48-f1-forged $SECOND", false},
		{"F1_git_C_rebase_refused", "git -C $WORK rebase --onto $FIRST $SECOND", false},
		{"F1_cd_reset_refused", "cd $WORK && git reset --hard $FIRST", true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			git, f, env := programsGitMountedEvidenceStart(t)
			if row.dirty {
				require.NoError(t, os.WriteFile(filepath.Join(env.work, programsGitEvidenceFiles[2]), []byte("H48-F1 uncommitted evidence must survive\n"), 0600))
			}
			tracked := programsGitSetup(t, git, f.home, env.work, "diff", "--name-only", "HEAD")
			if row.dirty {
				require.Equal(t, programsGitEvidenceFiles[2], tracked, "fixture: a tracked evidence file carries uncommitted work")
			} else {
				require.Empty(t, tracked, "fixture: the tracked evidence tree is clean, so rebase/commit are executable")
			}
			before := programsGitEvidenceState(t, git, f.home, env.work)
			require.Equal(t, env.hashes[2], before.head)
			require.Contains(t, before.refs, env.hashes[2], "the ref reader must observe the real seeded HEAD")
			for _, hash := range env.hashes {
				require.Contains(t, before.history, hash, "the history reader must observe every real boundary commit")
			}

			// A refused cwd, broken executable, missing identity or empty evidence
			// reader cannot satisfy the negative cases: the registered tool first
			// reads the non-empty history and a working child identity from this
			// admitted mount, without mutation. Two separate turns: the shell
			// guard treats `git config` as a mutating verb in any script that
			// names the work dir, so the identity read must not name it.
			read := programsGitMountedTurn(t, f, "f1-admitted-read", env.mount, fmt.Sprintf("pwd && git -C %s log --format=%%H", env.work))
			require.Equal(t, strings.Join([]string{env.project, env.hashes[2], env.hashes[1], env.hashes[0]}, "\n"),
				strings.TrimSpace(read), "real mounted cwd and the exact three-commit evidence read")
			identity := programsGitMountedTurn(t, f, "f1-admitted-identity", env.mount, "git config --get user.email")
			require.Equal(t, programsGitF1Email, strings.TrimSpace(identity), "the child Git identity is usable, so an unrefused commit/rebase would really succeed")
			require.Equal(t, before, programsGitEvidenceState(t, git, f.home, env.work), "harmless main-session turns must not create an engine boundary commit")

			command := strings.NewReplacer("$WORK", env.work, "$FIRST", env.hashes[0], "$SECOND", env.hashes[1]).Replace(row.script)
			result := programsGitMountedTurn(t, f, "f1-history-attack", env.mount, command+" && printf '"+programsGitF1Done+"'")
			assert.Contains(t, result, "protected evidence repo", "H48-F1/FR-152: a mounted cwd must not bypass the evidence-repository refusal")
			assert.NotContains(t, result, programsGitF1Done, "the mutating Git command must not run successfully")
			after := programsGitEvidenceState(t, git, f.home, env.work)
			assert.Equal(t, before, after, "H48-F1/FR-152: HEAD, every ref, reachable history and tracked evidence bytes must stay unchanged")
			t.Logf("H48-F1 actual registered loop: protected_refusal=%t marker_absent=%t evidence_unchanged=%t",
				strings.Contains(result, "protected evidence repo"), !strings.Contains(result, programsGitF1Done), before == after)
		})
	}

	// The fix must not become a blanket mount deny: a normal push from the same
	// approved mount still transfers its own project commit, and the evidence
	// repository is untouched. (The receiver-backed token/config/argv proof of
	// a mounted push is the separate S20 actual-loop leaf.)
	t.Run("F1_push_from_mount_still_works", func(t *testing.T) {
		git, f, env := programsGitMountedEvidenceStart(t)
		programsGitSetup(t, git, f.home, "", "init", env.project)
		require.NoError(t, os.WriteFile(filepath.Join(env.project, "f1-push.txt"), []byte("H48-F1 mounted push\n"), 0600))
		programsGitSetup(t, git, f.home, env.project, "add", "f1-push.txt")
		programsGitSetup(t, git, f.home, env.project, "-c", "user.name="+programsGitF1Name, "-c", "user.email="+programsGitF1Email, "commit", "-m", "F1 mounted push source")
		source := programsGitSetup(t, git, f.home, env.project, "rev-parse", "HEAD")
		remote := filepath.Join(env.project, "f1-remote-bare")
		programsGitSetup(t, git, f.home, "", "init", "--bare", remote)
		programsGitSetup(t, git, f.home, env.project, "remote", "add", "origin", remote)
		before := programsGitEvidenceState(t, git, f.home, env.work)

		result := programsGitMountedTurn(t, f, "f1-mounted-push", env.mount, "git push origin HEAD:refs/heads/f1-ok && printf 'F1_PUSH_DONE'")
		require.Contains(t, result, "F1_PUSH_DONE", "a real registered push from the approved mount must still run")
		require.Equal(t, source, programsGitSetup(t, git, f.home, "", "--git-dir", remote, "rev-parse", "refs/heads/f1-ok"), "the push transferred the prepared project commit")
		require.Equal(t, before, programsGitEvidenceState(t, git, f.home, env.work), "a mounted push must not touch the evidence repository")
	})
}

type programsGitMountedEvidence struct {
	work, project, mount string
	hashes               [3]string
}

// Every leaf builds its own isolated fixture: the connected-program turn
// fixture, a child Git identity, three real engine boundary commits and an
// operator write mount outside the evidence repository.
func programsGitMountedEvidenceStart(t *testing.T) (string, *wu5Fixture, programsGitMountedEvidence) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Fatal("BLOCKED: native Windows mounted-cwd evidence-history fixture is not implemented; this POSIX actual-loop regression does not certify H48-F1/FR-152 on Windows")
	}
	programsIsolateGit(t)
	git, err := exec.LookPath("git")
	require.NoError(t, err, "installed Git is required, not a guard/result substitute")
	f := newProgramsTurnFixture(t)
	programsGitChildIdentity(t, f, git)
	work, hashes := programsGitEvidenceSeed(t, f, git)
	require.NotContains(t, work, " ", "fixture invariant: the unquoted absolute work path is one shell word")
	project, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	mount, warning, err := workspace.CreateMount(f.home, "sales", "f1-project", project)
	require.NoError(t, err, "a real operator write grant must admit this cwd")
	require.Empty(t, warning)
	require.Equal(t, "f1-project", mount.Name)
	return git, f, programsGitMountedEvidence{work: work, project: project, mount: mount.Name, hashes: hashes}
}

// The actual child's HOME (the runtime supplies its own) receives a fixture
// identity so a commit or rebase that is NOT refused really succeeds; without
// it a missing identity would mask the attack as an unrelated failure.
func programsGitChildIdentity(t *testing.T, f *wu5Fixture, git string) {
	t.Helper()
	childHome := programsForeground(t, f, "f1-home", "printf '%s' \"$HOME\"")
	rel, err := filepath.Rel(f.home, childHome)
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(childHome))
	require.NotEqual(t, "..", rel)
	require.False(t, strings.HasPrefix(rel, ".."+string(filepath.Separator)), "never write outside the isolated fixture home")
	require.NoError(t, os.MkdirAll(childHome, 0700))
	global := filepath.Join(childHome, ".gitconfig")
	programsGitSetup(t, git, childHome, "", "config", "--file", global, "user.name", programsGitF1Name)
	programsGitSetup(t, git, childHome, "", "config", "--file", global, "user.email", programsGitF1Email)
}

// Seed through the actual engine-owned API and production secret scanner, not
// a foreign .git or a fabricated guard registration (gitevidence.Open registers
// the protected workdir). Plain main-session turns below do not dispatch
// task/attempt commits, so no legitimate engine commit races the attack.
func programsGitEvidenceSeed(t *testing.T, f *wu5Fixture, git string) (string, [3]string) {
	t.Helper()
	work, err := workspace.EnsureWorkDir(f.home, "sales")
	require.NoError(t, err)
	repo, err := gitevidence.Open(work, gitevidence.WithSecretScanner(f.al.verifierEvidenceScanner()))
	require.NoError(t, err)
	var hashes [3]string
	for i, name := range programsGitEvidenceFiles {
		require.NoError(t, os.WriteFile(filepath.Join(work, name), []byte(fmt.Sprintf("H48-F1 evidence boundary %d\n", i+1)), 0600))
		commit, commitErr := repo.Commit(gitevidence.BoundaryTask, gitevidence.CommitMeta{TaskID: "h48-f1-seed", AgentID: "dev"}, []string{name})
		require.NoError(t, commitErr)
		require.False(t, commit.Skipped, "actual evidence boundary must create a commit")
		require.NotEmpty(t, commit.Hash)
		hashes[i] = commit.Hash
	}
	require.NotEqual(t, hashes[0], hashes[1])
	require.NotEqual(t, hashes[1], hashes[2])
	require.Equal(t, strings.Join([]string{hashes[2], hashes[1], hashes[0]}, "\n"),
		programsGitSetup(t, git, f.home, work, "rev-list", "HEAD"),
		"installed Git must independently read all three real go-git boundary commits")
	return work, hashes
}

type programsGitEvidenceSnapshot struct {
	head, refs, history, contents string
}

func programsGitEvidenceState(t *testing.T, git, home, work string) programsGitEvidenceSnapshot {
	t.Helper()
	var contents strings.Builder
	for _, name := range programsGitEvidenceFiles {
		body, err := os.ReadFile(filepath.Join(work, name))
		switch {
		case err == nil:
			fmt.Fprintf(&contents, "%s=%q\n", name, body)
		case errors.Is(err, os.ErrNotExist):
			fmt.Fprintf(&contents, "%s=<absent>\n", name)
		default:
			require.NoError(t, err)
		}
	}
	return programsGitEvidenceSnapshot{
		head:     programsGitSetup(t, git, home, work, "rev-parse", "HEAD"),
		refs:     programsGitSetup(t, git, home, work, "for-each-ref", "--sort=refname", "--format=%(refname) %(objectname)"),
		history:  programsGitSetup(t, git, home, work, "log", "--all", "--topo-order", "--format=%H:%P:%T:%s"),
		contents: contents.String(),
	}
}
