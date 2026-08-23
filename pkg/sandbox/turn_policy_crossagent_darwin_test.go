//go:build darwin

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// This file proves ADR-063 FR-3.3/FR-3.5 against REAL children run under
// /usr/bin/sandbox-exec, through the production composition:
//
//	fspolicy.FSPolicy -> KernelPolicyForTurn -> Limits.KernelPolicy -> Run
//	  -> applyPlatformHardening -> SeatbeltBackend.ApplyToCmd
//
// Only the first hop is stood in for: production reaches the authored FSPolicy
// via tools.ResolveTurnFSPolicy, which pkg/sandbox cannot import (it would be an
// import cycle). Everything downstream is the real code path.
//
// # What was actually broken, and why assertions on profile text would not have
// caught it
//
// Two independent defects, and the second hid behind the first:
//
//  1. Nothing in production ever SET Limits.KernelPolicy, so every child ran
//     under the boot profile.
//  2. Even with it set, DeriveKernelPolicy's deny set came from
//     fspolicy.DeniedPathsFor, which re-admits the whole `agents` root for an
//     agent-home-rooted turn. So the kernel granted every OTHER agent's home
//     while the app layer denied it by path.
//
// Fixing (1) alone would have left (2) — a child confined to a policy that was
// itself too wide. Both are only visible by running a process and watching what
// the kernel does to it, which is why every assertion here is an exit code from
// a real /bin/bash.

// crossAgentHome builds a realistic $OMNIPUS_HOME layout and returns its
// realpath (macOS /var -> /private/var, and every path comparison downstream
// works on resolved paths).
func crossAgentHome(t *testing.T) string {
	t.Helper()
	raw := t.TempDir()
	home, err := filepath.EvalSymlinks(raw)
	require.NoError(t, err)

	for _, dir := range []string{
		filepath.Join(home, "agents", "self"),
		filepath.Join(home, "agents", "victim"),
		filepath.Join(home, "workspaces", "w1", "work"),
		filepath.Join(home, "workspaces", "w2", "work"),
	} {
		require.NoError(t, os.MkdirAll(dir, 0o750))
	}
	write := func(rel, content string) {
		require.NoError(t, os.WriteFile(filepath.Join(home, rel), []byte(content), 0o600))
	}
	write("master.key", "SECRET-MASTER-KEY")
	write(filepath.Join("agents", "self", "OWN.md"), "own soul")
	write(filepath.Join("agents", "victim", "SOUL.md"), "victim soul")
	write(filepath.Join("workspaces", "w1", "mounts.json"), `{"mounts":[]}`)
	write(filepath.Join("workspaces", "w2", "record.json"), `{"id":"w2"}`)
	return home
}

// turnLimits is the production hop under test: derive the per-turn kernel
// policy from the turn's authored FSPolicy and hand it to the spawn.
func turnLimits(t *testing.T, home, workDir string) Limits {
	t.Helper()
	authored := fspolicy.FSPolicy{
		WorkDir:   workDir,
		Scope:     fspolicy.FSScopeConfined,
		CarveOuts: fspolicy.SecretPaths(home),
	}
	kp, err := KernelPolicyForTurn(authored)
	require.NoError(t, err)
	require.NotNil(t, kp, "a registered turn-policy base must yield a per-turn policy; "+
		"a nil here means the spawn would silently fall back to the WIDER boot profile")
	return Limits{WorkspaceDir: workDir, KernelPolicy: kp}
}

// installCrossAgentTurn registers the boot half of the per-turn policy and
// installs a DELIBERATELY WIDE boot Seatbelt profile: RWX on the whole of
// $OMNIPUS_HOME, which is what production's boot policy actually grants.
//
// The width is the point. If the per-turn seam were broken and a child fell
// back to the boot profile, every "cannot reach another agent" assertion below
// would FAIL rather than pass by accident — the boot profile permits exactly
// the access these tests forbid.
func installCrossAgentTurn(t *testing.T, home string) {
	t.Helper()
	installBootBackend(t, SandboxPolicy{
		FilesystemRules: []PathRule{{Path: home, Access: AccessRead | AccessWrite | AccessExecute}},
		ReadsOpen:       true,
		ExecOpen:        true,
	})
	RegisterTurnPolicyBase(&TurnPolicyInput{HomePath: home, Model: FilesystemModelOpen})
	t.Cleanup(func() { RegisterTurnPolicyBase(nil) })
}

func readScript(path string) []string { return []string{"/bin/bash", "-c", "cat " + path} }
func writeAppend(path string) []string {
	return []string{"/bin/bash", "-c", "echo INJECTED >> " + path}
}

// runChild runs argv under lim and returns the exit code. A spawn error is
// fatal: this suite is about what the KERNEL did to a running process, so a
// child that never started would make every assertion vacuous.
func runChild(t *testing.T, argv []string, lim Limits) (int, string) {
	t.Helper()
	res, err := Run(context.Background(), argv, nil, lim)
	require.NoError(t, err, "the spawn itself must succeed even when the child's access is denied")
	return res.ExitCode, string(res.Stderr)
}

func fileContains(t *testing.T, path, want string) bool {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b) == want
}

// TestPerTurnPolicy_AgentTurn_CannotReachAnotherAgentsHome is the exact hole a
// reviewer demonstrated, asserted closed.
//
// The reported result, run against a profile matching what production rendered:
//
//	cat   $OMNIPUS_HOME/master.key                    -> Operation not permitted
//	echo INJECTED > $OMNIPUS_HOME/workspaces/w1.json  -> SUCCEEDED
//	cat   $OMNIPUS_HOME/agents/victim/SOUL.md         -> SUCCEEDED
//	echo PWNED >  $OMNIPUS_HOME/agents/victim/SOUL.md -> SUCCEEDED
//
// The three SUCCEEDED lines are what this test forbids.
func TestPerTurnPolicy_AgentTurn_CannotReachAnotherAgentsHome(t *testing.T) {
	home := crossAgentHome(t)
	installCrossAgentTurn(t, home)
	lim := turnLimits(t, home, filepath.Join(home, "agents", "self"))

	victimSoul := filepath.Join(home, "agents", "victim", "SOUL.md")

	code, stderr := runChild(t, readScript(victimSoul), lim)
	assert.NotEqual(t, 0, code,
		"reading ANOTHER agent's home must be denied at the kernel layer; the app layer "+
			"(fspolicy.IsCarveOut) already denies it, and a kernel that does not is the "+
			"divergence ADR-063 FR-3.3 exists to remove. stderr=%s", stderr)

	code, stderr = runChild(t, writeAppend(victimSoul), lim)
	assert.NotEqual(t, 0, code, "writing another agent's home must be denied. stderr=%s", stderr)
	assert.True(t, fileContains(t, victimSoul, "victim soul"),
		"the victim's file must be byte-identical afterwards — a non-zero exit with a "+
			"modified file would mean the write landed and something else failed")

	// The secret set, which was already closed, must stay closed.
	code, _ = runChild(t, readScript(filepath.Join(home, "master.key")), lim)
	assert.NotEqual(t, 0, code, "master.key must remain unreadable")
}

// TestPerTurnPolicy_AgentTurn_CanStillUseItsOwnHome is the positive control.
//
// Without it every assertion in the test above would pass against a totally
// broken profile that denies the child everything — including the directory it
// is supposed to be working in. A confinement that also breaks the product is
// not a confinement that shipped.
func TestPerTurnPolicy_AgentTurn_CanStillUseItsOwnHome(t *testing.T) {
	home := crossAgentHome(t)
	installCrossAgentTurn(t, home)
	self := filepath.Join(home, "agents", "self")
	lim := turnLimits(t, home, self)

	code, stderr := runChild(t, readScript(filepath.Join(self, "OWN.md")), lim)
	assert.Equal(t, 0, code, "an agent must be able to read its OWN home. stderr=%s", stderr)

	created := filepath.Join(self, "created-by-child.txt")
	code, stderr = runChild(t, writeAppend(created), lim)
	assert.Equal(t, 0, code, "an agent must be able to write its OWN home. stderr=%s", stderr)
	assert.FileExists(t, created)
}

// TestPerTurnPolicy_WorkspaceTurn_CannotReachItsOwnAgentHome pins FR-3.3's
// "carried across EXACTLY" in the direction that is easy to get backwards.
//
// During a re-rooted workspace turn the app layer denies agents/<self> just as
// it denies every other agent's home — the own-tree exception is anchored on the
// WORK DIR, and during a workspace turn the work dir is not inside agents/. A
// kernel that re-admitted the agent's own home "because it is theirs" would be
// MORE permissive than the app layer, which is the same class of divergence as
// the hole above, only pointing the other way.
func TestPerTurnPolicy_WorkspaceTurn_CannotReachItsOwnAgentHome(t *testing.T) {
	home := crossAgentHome(t)
	installCrossAgentTurn(t, home)
	work := filepath.Join(home, "workspaces", "w1", "work")
	lim := turnLimits(t, home, work)

	ownSoul := filepath.Join(home, "agents", "self", "OWN.md")

	code, stderr := runChild(t, readScript(ownSoul), lim)
	assert.NotEqual(t, 0, code,
		"during a workspace turn agents/<self> is as unreachable as anyone else's home. stderr=%s", stderr)

	code, stderr = runChild(t, writeAppend(ownSoul), lim)
	assert.NotEqual(t, 0, code, "…and unwritable too. stderr=%s", stderr)
	assert.True(t, fileContains(t, ownSoul, "own soul"), "the file must be unchanged")

	// Positive control in the same shape: the work dir itself must work, or the
	// assertions above prove only that the child can do nothing at all.
	created := filepath.Join(work, "built.txt")
	code, stderr = runChild(t, writeAppend(created), lim)
	assert.Equal(t, 0, code, "the workspace work dir must be writable. stderr=%s", stderr)
	assert.FileExists(t, created)
}

// TestPerTurnPolicy_WorkspaceTurn_CannotWriteAWorkspaceRecordItDoesNotOwn
// covers both halves of "a workspace record it does not own": another
// workspace's entire tree, and its OWN workspace's metadata sitting beside the
// work dir. The app layer denies both — the own-tree exception is the work dir,
// not the workspace directory that contains it.
func TestPerTurnPolicy_WorkspaceTurn_CannotWriteAWorkspaceRecordItDoesNotOwn(t *testing.T) {
	home := crossAgentHome(t)
	installCrossAgentTurn(t, home)
	lim := turnLimits(t, home, filepath.Join(home, "workspaces", "w1", "work"))

	foreign := filepath.Join(home, "workspaces", "w2", "record.json")
	code, stderr := runChild(t, writeAppend(foreign), lim)
	assert.NotEqual(t, 0, code, "another workspace's record must be unwritable. stderr=%s", stderr)
	assert.True(t, fileContains(t, foreign, `{"id":"w2"}`), "the foreign record must be unchanged")

	code, _ = runChild(t, readScript(foreign), lim)
	assert.NotEqual(t, 0, code, "another workspace's record must be unreadable")

	ownRecord := filepath.Join(home, "workspaces", "w1", "mounts.json")
	code, stderr = runChild(t, writeAppend(ownRecord), lim)
	assert.NotEqual(t, 0, code,
		"a turn's OWN workspace record sits beside the work dir, not inside it, so the app "+
			"layer denies it — rewriting mounts.json would let a turn grant itself new write "+
			"roots for the next turn. stderr=%s", stderr)
	assert.True(t, fileContains(t, ownRecord, `{"mounts":[]}`), "the record must be unchanged")
}

// TestPerTurnPolicy_TwoAgentTurns_AreGenuinelyDifferentChildren rules out a
// whole class of "the seam works once" bug: a cache or a backend field that
// accepts the first per-turn policy and then ignores every later one would pass
// every single-turn test above.
func TestPerTurnPolicy_TwoAgentTurns_AreGenuinelyDifferentChildren(t *testing.T) {
	home := crossAgentHome(t)
	installCrossAgentTurn(t, home)

	selfLim := turnLimits(t, home, filepath.Join(home, "agents", "self"))
	victimLim := turnLimits(t, home, filepath.Join(home, "agents", "victim"))

	selfFile := filepath.Join(home, "agents", "self", "OWN.md")
	victimFile := filepath.Join(home, "agents", "victim", "SOUL.md")

	// Each turn reads its own file and is refused the other's, in both
	// directions — so neither policy is simply "whatever was derived first".
	code, stderr := runChild(t, readScript(selfFile), selfLim)
	assert.Equal(t, 0, code, "self turn reads its own file. stderr=%s", stderr)
	code, _ = runChild(t, readScript(victimFile), selfLim)
	assert.NotEqual(t, 0, code, "self turn is refused victim's file")

	code, stderr = runChild(t, readScript(victimFile), victimLim)
	assert.Equal(t, 0, code, "victim turn reads its own file. stderr=%s", stderr)
	code, _ = runChild(t, readScript(selfFile), victimLim)
	assert.NotEqual(t, 0, code, "victim turn is refused self's file")
}

// TestPerTurnPolicy_NoRegisteredBase_FallsBackRatherThanErroring pins the
// documented contract that nil is not an error. A spawn path with no turn —
// and a gateway that degraded to application-level enforcement — must keep
// working exactly as before.
func TestPerTurnPolicy_NoRegisteredBase_FallsBackRatherThanErroring(t *testing.T) {
	RegisterTurnPolicyBase(nil)
	kp, err := KernelPolicyForTurn(fspolicy.FSPolicy{WorkDir: t.TempDir()})
	require.NoError(t, err, "no registered base is a normal condition, not a failure")
	assert.Nil(t, kp, "nil policy means 'use the boot profile', which is the pre-existing behaviour")
}

// TestPerTurnPolicy_EmptyWorkDirIsRefused: a turn with no work dir cannot
// produce a meaningful confinement, and silently returning nil would hand the
// child the WIDER boot profile. Fail closed instead.
func TestPerTurnPolicy_EmptyWorkDirIsRefused(t *testing.T) {
	home := crossAgentHome(t)
	RegisterTurnPolicyBase(&TurnPolicyInput{HomePath: home, Model: FilesystemModelOpen})
	t.Cleanup(func() { RegisterTurnPolicyBase(nil) })

	kp, err := KernelPolicyForTurn(fspolicy.FSPolicy{})
	require.Error(t, err, "an unusable authored policy must abort the spawn, not downgrade it")
	assert.Nil(t, kp)
}

// --- The directory-node rename bypass (finding: "a wall computed correctly and
// never connected") ---
//
// fspolicy.KernelDeniedNodesFor was written to stop this and had ZERO callers:
// DeriveKernelPolicy consumed KernelDeniedPathsFor (subtrees) and never the
// node list. Everything above this line passed while the attack below worked.
//
// Reproduced against real children before the wiring existed:
//
//	agent-home turn:
//	  cat  <home>/agents/victim/SOUL.md      -> Operation not permitted
//	  mv   <home>/agents <home>/agents-old   -> SUCCEEDED
//	  cat  <home>/agents-old/victim/SOUL.md  -> VICTIM-SOUL-SECRET
//
//	workspace turn:
//	  mv   <home>/workspaces <home>/ws-old   -> SUCCEEDED
//	  read AND write of every workspace record, own and foreign, then rename
//	  back so the gateway reads the TAMPERED record as if nothing happened.
//
// The workspace half is the worse one: workspaces/<id>.json carries core_team,
// which drives per-turn filesystem re-rooting, and — until issue #636 moved it
// to $OMNIPUS_HOME/entities/delegation/<id>.json — it also carried the
// delegation trust_set that pkg/workspace.ReadDelegation feeds to the live
// gate. The delegation half is closed by the store move (an edge planted in
// the record now authorizes nothing); the node-rename bypass this file tests
// is what stops the rest.

// renameNode returns the argv that attempts to rename a directory node.
func renameNode(from, to string) []string {
	return []string{"/bin/bash", "-c", "mv " + from + " " + to}
}

// TestPerTurnPolicy_AgentTurn_CannotRenameTheAgentsRootNode is the attack, end
// to end: rename the node, then read through the relocated path.
func TestPerTurnPolicy_AgentTurn_CannotRenameTheAgentsRootNode(t *testing.T) {
	home := crossAgentHome(t)
	installCrossAgentTurn(t, home)
	self := filepath.Join(home, "agents", "self")
	lim := turnLimits(t, home, self)

	agents := filepath.Join(home, "agents")
	relocated := filepath.Join(home, "agents-old")

	code, stderr := runChild(t, renameNode(agents, relocated), lim)
	assert.NotEqual(t, 0, code,
		"renaming the agents/ node must be denied: it relocates EVERY agent's home to a path "+
			"no deny in the policy covers, which defeats the entire per-sibling deny list in "+
			"one syscall. stderr=%s", stderr)
	assert.NoDirExists(t, relocated,
		"the node must still be where it was — a non-zero exit with the directory moved would "+
			"mean the rename landed and something else failed")
	assert.DirExists(t, agents)

	// The whole attack chain in one child, so a partial fix (rename blocked in
	// isolation but reachable via some other spelling) still fails here.
	code, stderr = runChild(t, []string{"/bin/bash", "-c",
		"mv " + agents + " " + relocated + " && cat " + filepath.Join(relocated, "victim", "SOUL.md")}, lim)
	assert.NotEqual(t, 0, code, "the rename-then-read chain must not succeed. stderr=%s", stderr)
	assert.True(t, fileContains(t, filepath.Join(agents, "victim", "SOUL.md"), "victim soul"),
		"the victim's file must still be at its original path and unchanged")
}

// TestPerTurnPolicy_AgentTurn_OwnTreeSurvivesTheNodeDeny is the control that
// makes the test above meaningful. A node deny rendered as a SUBPATH rather
// than a LITERAL would block the rename and also lock the agent out of its own
// home — which passes every assertion above and ships a broken product.
func TestPerTurnPolicy_AgentTurn_OwnTreeSurvivesTheNodeDeny(t *testing.T) {
	home := crossAgentHome(t)
	installCrossAgentTurn(t, home)
	self := filepath.Join(home, "agents", "self")
	lim := turnLimits(t, home, self)

	code, stderr := runChild(t, readScript(filepath.Join(self, "OWN.md")), lim)
	assert.Equal(t, 0, code, "own home must stay readable under the node deny. stderr=%s", stderr)

	code, stderr = runChild(t, writeAppend(filepath.Join(self, "OWN.md")), lim)
	assert.Equal(t, 0, code, "own home must stay writable under the node deny. stderr=%s", stderr)

	created := filepath.Join(self, "sub", "deep.txt")
	code, stderr = runChild(t, []string{"/bin/bash", "-c",
		"mkdir -p " + filepath.Dir(created) + " && echo hi > " + created}, lim)
	assert.Equal(t, 0, code, "creating directories inside the own tree must stay possible. stderr=%s", stderr)
	assert.FileExists(t, created)
}

// TestPerTurnPolicy_WorkspaceTurn_CannotRenameEitherNodeOnTheChain covers the
// two-node shape: <home>/workspaces AND <home>/workspaces/w1 are both on the
// chain to the work dir, and either rename relocates the workspace records.
func TestPerTurnPolicy_WorkspaceTurn_CannotRenameEitherNodeOnTheChain(t *testing.T) {
	home := crossAgentHome(t)
	installCrossAgentTurn(t, home)
	work := filepath.Join(home, "workspaces", "w1", "work")
	lim := turnLimits(t, home, work)

	for _, node := range []string{
		filepath.Join(home, "workspaces"),
		filepath.Join(home, "workspaces", "w1"),
	} {
		relocated := node + "-old"
		code, stderr := runChild(t, renameNode(node, relocated), lim)
		assert.NotEqual(t, 0, code,
			"renaming %s must be denied; it relocates the workspace records (delegation trust_set, "+
				"core_team) to a path no deny covers. stderr=%s", node, stderr)
		assert.NoDirExists(t, relocated)
		assert.DirExists(t, node)
	}

	// Control: the work dir underneath both nodes stays fully usable.
	created := filepath.Join(work, "built.txt")
	code, stderr := runChild(t, writeAppend(created), lim)
	assert.Equal(t, 0, code, "the work dir must stay writable. stderr=%s", stderr)
	assert.FileExists(t, created)

	// And the record the rename was after is still denied by path, as before.
	assert.True(t, fileContains(t, filepath.Join(home, "workspaces", "w1", "mounts.json"), `{"mounts":[]}`))
}

// TestPerTurnPolicy_RenamedBackIsNotAnEscape closes the variant that makes the
// workspace attack silent: move the node aside, tamper, move it back. If the
// rename is denied both ways there is no window in which the gateway reads a
// record the child rewrote.
func TestPerTurnPolicy_RenamedBackIsNotAnEscape(t *testing.T) {
	home := crossAgentHome(t)
	installCrossAgentTurn(t, home)
	work := filepath.Join(home, "workspaces", "w1", "work")
	lim := turnLimits(t, home, work)

	ws := filepath.Join(home, "workspaces")
	record := filepath.Join(home, "workspaces", "w1", "mounts.json")

	code, stderr := runChild(t, []string{"/bin/bash", "-c",
		"mv " + ws + " " + ws + "-old && " +
			"echo '{\"mounts\":[\"/\"]}' > " + ws + "-old/w1/mounts.json && " +
			"mv " + ws + "-old " + ws}, lim)
	assert.NotEqual(t, 0, code, "the move-tamper-move-back chain must fail at the first step. stderr=%s", stderr)
	assert.True(t, fileContains(t, record, `{"mounts":[]}`),
		"the workspace record must be byte-identical: this is the file the delegation trust gate "+
			"and the per-turn re-rooting both read")
}

// --- Backups written AFTER the child starts ---
//
// The secret set discovers `config.json.bak-<ts>` by LISTING $OMNIPUS_HOME when
// the policy is built. A Seatbelt profile is fixed at exec, so a backup the
// GATEWAY writes mid-turn — which is exactly what a config migration does — is
// covered by no entry in that list. A real install's config backup carries the
// gateway CLI token and the user account list.
//
// Measured before SandboxPolicy.DeniedPathPrefixes existed, with the whole
// enumerated deny list in place:
//
//	cat <home>/config.json           -> Operation not permitted
//	cat <home>/config.json.bak-<ts>  -> TOKEN-IN-BACKUP

// TestPerTurnPolicy_BackupWrittenAfterSpawnIsStillDenied runs the child AFTER
// creating the backup, but derives the policy BEFORE — which is the real
// ordering, since the policy is derived once per spawn and the gateway keeps
// writing for the rest of the turn.
func TestPerTurnPolicy_BackupWrittenAfterSpawnIsStillDenied(t *testing.T) {
	home := crossAgentHome(t)
	installCrossAgentTurn(t, home)
	lim := turnLimits(t, home, filepath.Join(home, "agents", "self"))

	// Only now does the gateway write the backup — after the policy above was
	// derived and after any listing it performed.
	for _, name := range []string{
		"config.json.bak-1755000000",
		"credentials.json.old",
		"master.key.2",
	} {
		p := filepath.Join(home, name)
		require.NoError(t, os.WriteFile(p, []byte("LIVE-TOKEN-AND-ACCOUNTS"), 0o600))

		code, stderr := runChild(t, readScript(p), lim)
		assert.NotEqual(t, 0, code,
			"%s was created after the policy was derived; an enumerated deny list cannot cover it, "+
				"which is why the profile carries an anchored regex prefix deny. stderr=%s", name, stderr)

		code, stderr = runChild(t, writeAppend(p), lim)
		assert.NotEqual(t, 0, code, "%s must be unwritable too. stderr=%s", name, stderr)
		assert.True(t, fileContains(t, p, "LIVE-TOKEN-AND-ACCOUNTS"), "%s must be unchanged", name)
	}

	// A path that merely LOOKS similar must stay reachable — an over-broad
	// regex is a different defect, not a fix.
	ordinary := filepath.Join(home, "config.jsonl")
	require.NoError(t, os.WriteFile(ordinary, []byte("not a secret"), 0o600))
	code, stderr := runChild(t, readScript(ordinary), lim)
	assert.Equal(t, 0, code,
		"config.jsonl is not a backup of config.json; the prefix includes the dot for exactly "+
			"this reason. stderr=%s", stderr)
}
