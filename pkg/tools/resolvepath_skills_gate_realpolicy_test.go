// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — ADR-072 D10.3 read-gate tests driven through the REAL
// policy constructor, fspolicy.EffectiveFSPolicy.
//
// # Why this file exists separately from resolvepath_skills_gate_test.go
//
// Every test in that file builds its policy with confinedPolicy, the
// direct-construction helper that leaves FSPolicy.CarveOuts nil. That is a
// shape no production caller ever produces: EffectiveFSPolicy — the sole
// constructor tools.ResolveTurnFSPolicy reaches ResolvePath through — always
// populates CarveOuts from buildCarveOuts($OMNIPUS_HOME). Because
// ResolvePath consults fspolicy.IsCarveOut BEFORE it reaches D10.3's
// narrower classifier, a gate test over a carve-out-free policy cannot see
// the interaction between the two, and D10.3's whole narrowing was dead code
// in production while its own tests passed: `skills` was in
// fspolicy.SecretEntriesAlwaysPathOnly, SecretEntriesRelative folded that
// into SecretPaths, buildCarveOuts returned SecretPaths, and so IsCarveOut
// refused every file under $OMNIPUS_HOME/skills — bundled helper files
// included — several lines before isSkillInstructionFileLeaf ran.
//
// So: these tests construct the policy the way production does. A test using
// the bare-literal helper does not prove this behaviour, which is exactly how
// the dead-code defect survived review.
package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// realTurnPolicy builds an agent-home-rooted turn policy through the real
// fspolicy.EffectiveFSPolicy, against a real $OMNIPUS_HOME on disk, and
// asserts the carve-out list is genuinely populated — a policy that silently
// arrived with no carve-outs would make every assertion in this file
// vacuous, which is the failure mode the file exists to prevent.
func realTurnPolicy(t *testing.T, home string) fspolicy.FSPolicy {
	t.Helper()
	agentHome := filepath.Join(home, "agents", "self")
	require.NoError(t, os.MkdirAll(agentHome, 0o755))

	policy, err := fspolicy.EffectiveFSPolicy(
		context.Background(), agentHome, "", true /* restrict */, home, "self", "")
	require.NoError(t, err)
	require.NotEmpty(t, policy.CarveOuts,
		"EffectiveFSPolicy must populate CarveOuts — an empty list would make this file's assertions vacuous")

	// The secret set must still be enforced through this policy, or a test
	// below that observes "readable" would be observing a broken policy
	// rather than the narrowed skills gate.
	require.NoError(t, os.WriteFile(filepath.Join(home, "cli.token"), []byte("LIVE-TOKEN"), 0o600))
	_, err = ResolvePath(context.Background(), policy, "read_file", "guard", FSOpRead,
		filepath.Join(home, "cli.token"))
	require.ErrorIs(t, err, ErrCarveOut,
		"control: the real policy must still refuse cli.token — otherwise this policy proves nothing")

	return policy
}

// caseInsensitiveVolume reports whether dir sits on a volume that resolves
// two different case spellings to the same file. Mirrors
// pkg/fspolicy/carveout_case_identity_test.go's probeVolume: the property is
// measured on the actual volume rather than guessed from runtime.GOOS,
// because case sensitivity is a property of the MOUNT (APFS is
// case-insensitive by default; ext4 is not; SMB/exfat mounts on Linux are).
func caseInsensitiveVolume(t *testing.T, dir string) bool {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "caseprobe.tmp"), []byte("x"), 0o600))
	_, err := os.Stat(filepath.Join(dir, "CASEPROBE.TMP"))
	return err == nil
}

// TestReadGate_RealPolicy_RegistryBundledFileReadable is the regression for
// the dead-code defect described in this file's header (ADR-072 D10.3(b)):
// through the REAL carve-out list, a registry skill's bundled sibling file —
// the helper script or reference file its SKILL.md tells the agent to open —
// must be readable by the file tools.
//
// Before the fix this failed with ErrCarveOut, because $OMNIPUS_HOME/skills
// was a whole-directory app-layer carve-out that ran ahead of D10.3's
// instruction-file classifier.
func TestReadGate_RealPolicy_RegistryBundledFileReadable(t *testing.T) {
	home := realTempDir(t)
	t.Setenv(config.EnvHome, home)

	skillDir := filepath.Join(home, "skills", "foo")
	writeGateTestSkillFile(t, filepath.Join(home, "skills"), "foo", "Use when doing foo")
	helperPath := filepath.Join(skillDir, "helper.sh")
	require.NoError(t, os.WriteFile(helperPath, []byte("#!/bin/sh\necho bundled-helper-marker\n"), 0o755))
	refPath := filepath.Join(skillDir, "reference.md")
	require.NoError(t, os.WriteFile(refPath, []byte("bundled-reference-marker\n"), 0o600))

	policy := realTurnPolicy(t, home)

	for _, path := range []string{helperPath, refPath} {
		handle, err := ResolvePath(context.Background(), policy, "read_file", "call-1", FSOpRead, path)
		require.NoError(t, err,
			"a registry skill's bundled sibling file must be readable through the REAL carve-out list — only the instruction file is gated (D10.3)")
		data, err := handle.ReadFile()
		require.NoError(t, err)
		assert.Contains(t, string(data), "bundled-")
		require.NoError(t, handle.Close())
	}
}

// TestReadGate_RealPolicy_InstructionFileStillRefused is the other half: the
// narrowing must not have opened the gate itself. Through the same real
// policy, the instruction file is still refused — and the refusal must come
// from D10.3's own classifier (whose message names the Skill tool), not from
// a blanket directory carve-out, so this test fails loudly if the coarse
// carve-out is ever restored underneath the gate and hides its removal.
func TestReadGate_RealPolicy_InstructionFileStillRefused(t *testing.T) {
	home := realTempDir(t)
	t.Setenv(config.EnvHome, home)

	skillFile := writeGateTestSkillFile(t, filepath.Join(home, "skills"), "foo", "Use when doing foo")

	policy := realTurnPolicy(t, home)

	_, err := ResolvePath(context.Background(), policy, "read_file", "call-1", FSOpRead, skillFile)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCarveOut)
	assert.Contains(t, err.Error(), "instruction file",
		"the refusal must come from D10.3's instruction-file gate, not from a blanket skills-directory carve-out")
}

// TestReadGate_RealPolicy_InstructionFileRefusedViaSymlinkAlias closes the
// aliasing direction the removed directory-wide carve-out used to cover
// incidentally.
//
// A symlink planted in the agent's own work dir, pointing at a registry
// skill's SKILL.md, has an innocuous basename and an ancestor chain that is
// nowhere near the skills root — so classifying only the path as WRITTEN
// (which D10.3 must keep doing, per FR-078) would let it straight through.
// The gate therefore judges BOTH spellings: the path as written and its
// fully-resolved realpath.
func TestReadGate_RealPolicy_InstructionFileRefusedViaSymlinkAlias(t *testing.T) {
	home := realTempDir(t)
	t.Setenv(config.EnvHome, home)

	skillFile := writeGateTestSkillFile(t, filepath.Join(home, "skills"), "foo", "Use when doing foo")

	policy := realTurnPolicy(t, home)

	alias := filepath.Join(policy.WorkDir, "innocuous-notes.md")
	require.NoError(t, os.Symlink(skillFile, alias))

	_, err := ResolvePath(context.Background(), policy, "read_file", "call-1", FSOpRead, alias)
	require.Error(t, err,
		"a symlink in the work dir pointing at a registry skill's instruction file must be refused")
	assert.ErrorIs(t, err, ErrCarveOut)
}

// TestSkillsGate_RealPolicy_RegistryWritesStillRefusedUnderABroadMount pins
// the half of the app-layer posture D10.3 does NOT change.
//
// Removing $OMNIPUS_HOME/skills from the app carve-out list opens reads of
// bundled files, which is the point — but it must not open WRITES as a side
// effect. It could: pkg/workspace/mount.go's CheckMountTarget refuses only a
// mount target INSIDE $OMNIPUS_HOME and deliberately allows (with a warning)
// one that CONTAINS it, on the stated reasoning that the secret set protects
// the contents. With `skills` out of that set, a write under such a mount
// would be granted and an agent could clobber another skill's instructions
// through write_file.
//
// The registry is therefore refused for every non-read op, exactly as the
// carve-out used to refuse it. This costs the sanctioned authoring path
// nothing — create_skill/edit_skill/install_skill write through pkg/skills'
// own raw I/O, never through ResolvePath.
func TestSkillsGate_RealPolicy_RegistryWritesStillRefusedUnderABroadMount(t *testing.T) {
	home := realTempDir(t)
	t.Setenv(config.EnvHome, home)

	skillFile := writeGateTestSkillFile(t, filepath.Join(home, "skills"), "foo", "Use when doing foo")
	bundled := filepath.Join(home, "skills", "foo", "helper.sh")
	require.NoError(t, os.WriteFile(bundled, []byte("#!/bin/sh\n"), 0o755))

	policy := realTurnPolicy(t, home)
	// A mount that CONTAINS $OMNIPUS_HOME — the shape CheckMountTarget warns
	// about but permits.
	policy.AllowedRoots = []string{filepath.Dir(home)}

	for _, tc := range []struct {
		name string
		op   FSOp
		path string
	}{
		{"write the instruction file", FSOpWrite, skillFile},
		{"write a bundled file", FSOpWrite, bundled},
		{"serve the skill directory", FSOpServe, filepath.Join(home, "skills", "foo")},
		{"use the skill directory as a bash cwd", FSOpExec, filepath.Join(home, "skills", "foo")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolvePath(context.Background(), policy, "write_file", "call-1", tc.op, tc.path)
			require.Error(t, err, "the installed registry must stay closed to every non-read op")
			assert.ErrorIs(t, err, ErrCarveOut)
		})
	}

	// The read side is still open for bundled files even under this mount —
	// otherwise the deny above would be indistinguishable from a blanket one.
	handle, err := ResolvePath(context.Background(), policy, "read_file", "call-2", FSOpRead, bundled)
	require.NoError(t, err)
	require.NoError(t, handle.Close())
}

// TestReadGate_RealPolicy_InstructionFileRefusedViaCaseVariant is Finding 2's
// regression: the skills gate must judge containment by filesystem IDENTITY
// (fspolicy.CoversForDeny), not by comparing path bytes.
//
// On a case-insensitive volume $OMNIPUS_HOME/SKILLS/foo/SKILL.md and
// $OMNIPUS_HOME/skills/foo/SKILL.md are ONE file to the kernel and two
// different strings to filepath.Rel — the exact defect pkg/fspolicy/
// pathidentity.go's header records as having leaked the live gateway bearer
// token and the master key on a real APFS volume. filepath.EvalSymlinks does
// not canonicalise case, so the resolved ancestor chain keeps whatever
// spelling the caller supplied and a byte comparison against the skills root
// misses.
//
// Skipped where the volume genuinely treats the two spellings as different
// files, mirroring pkg/fspolicy/carveout_case_identity_test.go's own
// probe-and-skip pattern.
func TestReadGate_RealPolicy_InstructionFileRefusedViaCaseVariant(t *testing.T) {
	home := realTempDir(t)
	t.Setenv(config.EnvHome, home)

	if !caseInsensitiveVolume(t, home) {
		t.Skip("volume is case-sensitive: SKILLS/ and skills/ are genuinely different directories here")
	}

	writeGateTestSkillFile(t, filepath.Join(home, "skills"), "foo", "Use when doing foo")
	policy := realTurnPolicy(t, home)

	variant := filepath.Join(home, "SKILLS", "foo", "SKILL.md")
	_, err := ResolvePath(context.Background(), policy, "read_file", "call-1", FSOpRead, variant)
	require.Error(t, err,
		"an upper-case spelling naming the SAME file on this volume must be refused just like the lower-case one")
	assert.ErrorIs(t, err, ErrCarveOut)
}
