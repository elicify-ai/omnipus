package agent

// H55 (#1960 S1), rows R6 and R7 of the architect's ruling
// (/workspace/logs/1960/spec-s1/ruling-H55.md). These two rows hold on the code as
// it is and must keep holding after the guard fix (its Rule A and Rule B) lands:
//   - R6 shows what the fix rests on: with no guard in the path at all, installed
//     Git itself refuses a plain forced push into the checked-out branch of the
//     evidence repository;
//   - R7 shows the fix is not a blanket ban on pushing from a mount: a push to a
//     bare remote name held in a variable still runs. The other idioms the ruling
//     lists are already pinned by TestProgramGitHelper_MountedIndirectForms (commit
//     with "why?", push of "$BRANCH") and by the S-20 actual-loop leaf
//     TestProgramGitHelper_ResetChain/S20_no_token_in_config_or_arguments.
//
// Oracles: ruling-H55 section (3) rows R6 and R7, FR-152 (the evidence repository
// stays byte-identical) and S-20 (a push from the mount succeeds and the receiver
// holds exactly the pushed branch).
import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R6. The guard is bypassed on purpose: installed Git runs directly in the mount
// target, with no loop and no evidence guard. The wording of Git's refusal is an
// observation of the installed Git; the oracle is that it refuses and the evidence
// repository is unchanged.
func TestProgramGitHelper_MountedPushH55_PlainPushIsRefusedByGitItself(t *testing.T) {
	x := programsGitH54Start(t, false)
	branch := programsGitSetup(t, x.bin, x.f.home, x.env.work, "symbolic-ref", "--short", "HEAD")
	require.NotEmpty(t, branch, "fixture: the evidence repository has a checked-out branch")
	x.branch(t, "h55-src", x.initProject(t))

	// Instrument: the operand below really leads to the evidence repository, so the
	// refusal cannot be a push that was going nowhere.
	listing := programsGitSetup(t, x.bin, x.f.home, x.env.project, "ls-remote", x.env.work)
	require.Contains(t, listing, x.env.hashes[2]+"\trefs/heads/"+branch)
	require.True(t, x.evidenceUnchanged(t), "instrument: the read-only listing must not change the evidence repository")

	cmd := exec.Command(x.bin, "push", "--force", x.env.work, "HEAD:"+branch)
	cmd.Dir = x.env.project
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + x.f.home, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"}
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if assert.ErrorAs(t, err, &exit, "R6: with no guard in the path, installed Git must refuse the plain push; output=%q", out) {
		assert.NotZero(t, exit.ExitCode(), "R6: the refused push exits non-zero")
	}
	assert.Contains(t, string(out), "checked out branch", "R6: Git refuses because the target is the checked-out branch, not for an unrelated reason")
	assert.True(t, x.evidenceUnchanged(t), "R6 / FR-152: HEAD, every ref, reachable history and tracked evidence bytes must be unchanged")
}

// R7, the form the other allowed-idiom rows do not cover: the remote is a bare
// name held in a variable, so nothing about it is a path.
func TestProgramGitHelper_MountedPushH55_BareRemoteVariableStaysAllowed(t *testing.T) {
	p := programsGitH54RunPush(t, `REMOTE=origin; git push "$REMOTE" main && printf '`+programsGitH54Done+`'`, "main")
	p.assertAdmitted(t)
	assert.Equal(t, "refs/heads/main "+p.commits["main"], p.refs(t),
		"S-20: the receiver holds exactly the pushed branch at the project's commit, and nothing else")
}
