// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — r15 §6.1 registry-folder authority through the REAL
// ResolveTurnFSPolicy constructor, with persisted agent/grant/config records.
//
// ADR-072 D10.3 originally required a special instruction-file gate; r15
// replaces that distinction with whole-folder loadable/editor authority.
// The historical dead-code defect was a coarse home carve-out denying all
// helpers before the narrower gate ran. Preserve the real-policy regression
// and secret-file refusal control rather than testing only a bare policy.
// The supplementary resolver suite does not establish real turn wiring.
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// realTurnPolicy builds an agent-home-rooted turn policy through the real
// fspolicy.EffectiveFSPolicy, against a real $OMNIPUS_HOME on disk, and
// asserts the carve-out list is genuinely populated — a policy that silently
// arrived with no carve-outs would make every assertion in this file
// vacuous, which is the failure mode the file exists to prevent.
func realTurnPolicy(t *testing.T, home, grant string, editor bool) fspolicy.FSPolicy {
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

	// r15 replaces the instruction-only rule with loadable/editor authority.
	// Populate a real persisted agent and derive that authority through the
	// actual turn constructor, never by setting HomeGuard fields in this suite.
	agent := config.AgentConfig{ID: "self", Home: agentHome, Skills: []string{}, Tools: &config.AgentToolsCfg{}}
	agent.Tools.Builtin.Policies = map[string]config.ToolPolicy{"read_file": config.ToolPolicyAllow, "write_file": config.ToolPolicyAllow, "edit_skill": config.ToolPolicyDeny}
	if editor {
		agent.Tools.Builtin.Policies["edit_skill"] = config.ToolPolicyAsk
	}
	if grant != "" {
		generation := "11111111-2222-3333-4444-555555555555"
		require.NoError(t, os.WriteFile(filepath.Join(home, "skills", grant, ".omnipus-identity"), []byte(generation+"\n"), 0o600))
		agent.Skills = []string{grant}
		raw, err := json.Marshal(agent)
		require.NoError(t, err)
		var record map[string]any
		require.NoError(t, json.Unmarshal(raw, &record))
		record["skill_grants"] = map[string]string{grant: generation}
		raw, err = json.Marshal(record)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &agent))
	}
	require.NoError(t, agentstore.New(home).Create(agent.ID, &agent))
	cfg := config.DefaultConfig()
	cfg.Agents.List = []config.AgentConfig{agent}
	require.NoError(t, config.SaveConfig(filepath.Join(home, "config.json"), cfg))
	policy, err = ResolveTurnFSPolicy(WithAgentID(context.Background(), "self"), agentHome, true)
	require.NoError(t, err)
	value := reflect.ValueOf(policy.HomeGuard)
	folders := value.FieldByName("SkillFolders")
	all := value.FieldByName("SkillsReadAll")
	if !folders.IsValid() || !all.IsValid() {
		t.Fatal("BLOCKED: real turn skill authority not implemented — required by r15 §6.1")
	}
	assert.Equal(t, editor, all.Bool())
	if grant != "" {
		assert.Contains(t, folders.Interface(), filepath.Join(home, "skills", grant))
	}
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

	policy := realTurnPolicy(t, home, "foo", false)

	for _, path := range []string{helperPath, refPath} {
		handle, err := ResolvePath(context.Background(), policy, "read_file", "call-1", FSOpRead, path)
		require.NoError(t, err,
			"a registry skill's bundled sibling file must be readable through the REAL carve-out list — r15 §6.1 admits the whole loadable folder")
		data, err := handle.ReadFile()
		require.NoError(t, err)
		assert.Contains(t, string(data), "bundled-")
		require.NoError(t, handle.Close())
	}
}

// TestReadGate_RealPolicy_InstructionFileStillRefused preserves the ungranted
// negative: a non-editor cannot use a file path to read an ungranted skill.
// r15 §6.1 applies the same authority to the entire folder, not just SKILL.md.
func TestReadGate_RealPolicy_InstructionFileStillRefused(t *testing.T) {
	home := realTempDir(t)
	t.Setenv(config.EnvHome, home)

	skillFile := writeGateTestSkillFile(t, filepath.Join(home, "skills"), "foo", "Use when doing foo")

	policy := realTurnPolicy(t, home, "", false)

	_, err := ResolvePath(context.Background(), policy, "read_file", "call-1", FSOpRead, skillFile)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCarveOut)
	assert.Contains(t, err.Error(), "foo", "refusal identifies the ungranted target")
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

	policy := realTurnPolicy(t, home, "", false)

	alias := filepath.Join(policy.WorkDir, "innocuous-notes.md")
	require.NoError(t, os.Symlink(skillFile, alias))

	_, err := ResolvePath(context.Background(), policy, "read_file", "call-1", FSOpRead, alias)
	require.Error(t, err,
		"a symlink in the work dir pointing at a registry skill's instruction file must be refused")
	assert.ErrorIs(t, err, ErrCarveOut)
}

// r15 §10.1: a broad enclosing mount grants no approval marker. Direct
// handle writes must refuse even for an editor; the read/exec authority itself
// is operation-blind (§6.1). Real-loop approved writes live in pkg/agent tests.
func TestSkillsGate_RealPolicy_UncomposedRegistryWritesRefusedUnderBroadMount(t *testing.T) {
	home := realTempDir(t)
	t.Setenv(config.EnvHome, home)
	instruction := writeGateTestSkillFile(t, filepath.Join(home, "skills"), "foo", "Use when doing foo")
	helper := filepath.Join(home, "skills", "foo", "helper.sh")
	require.NoError(t, os.WriteFile(helper, []byte("#!/bin/sh\n"), 0o755))
	policy := realTurnPolicy(t, home, "foo", true)
	policy.AllowedRoots = []string{filepath.Dir(home)}
	for _, path := range []string{instruction, helper} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			handle, err := ResolvePath(WithAgentID(context.Background(), "self"), policy, "write_file", "uncomposed", FSOpWrite, path)
			require.NoError(t, err, "editor resolution is allowed; approval is checked at WriteFile")
			err = handle.WriteFile([]byte("UNAPPROVED"))
			require.ErrorContains(t, err, "SKILL_WRITE_NOT_APPROVED")
			require.NoError(t, handle.Close())
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
	// The same loadable folder remains readable and executable; not a blanket deny.
	for _, op := range []FSOp{FSOpRead, FSOpServe, FSOpExec} {
		path := helper
		if op != FSOpRead {
			path = filepath.Dir(helper)
		}
		handle, err := ResolvePath(context.Background(), policy, "read_file", "authorized", op, path)
		require.NoError(t, err)
		require.NoError(t, handle.Close())
	}
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
	policy := realTurnPolicy(t, home, "", false)

	variant := filepath.Join(home, "SKILLS", "foo", "SKILL.md")
	_, err := ResolvePath(context.Background(), policy, "read_file", "call-1", FSOpRead, variant)
	require.Error(t, err,
		"an upper-case spelling naming the SAME file on this volume must be refused just like the lower-case one")
	assert.ErrorIs(t, err, ErrCarveOut)
}
