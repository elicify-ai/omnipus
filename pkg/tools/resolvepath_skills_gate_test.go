// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — ADR-072 (skill activation and loading) D10 (skill read
// gate) and D6.1.1 (write-path audit hook) tests. Traces to
// docs/internal/specs/skill-activation-and-loading-spec.md §"Feature: Gated
// skill reads" and §"Feature: Silent habit and observability" (the write-
// audit scenario), and to ADR-072 §"D10 — Skill content is readable only
// through the tool" / §"D6.1.1 — The write audit belongs at the path
// resolver, not the authoring tool".

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

// writeGateTestSkillFile creates <dir>/<slug>/SKILL.md with minimal valid
// frontmatter (mirrors pkg/skills' own writeSkillFile test helper, which
// this package cannot import — it is unexported in a different package) and
// returns the file's absolute path.
func writeGateTestSkillFile(t *testing.T, dir, slug, description string) string {
	t.Helper()
	skillDir := filepath.Join(dir, slug)
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	content := "---\nname: " + slug + "\ndescription: " + description + "\n---\n\n# " + slug + "\n"
	path := filepath.Join(skillDir, "SKILL.md")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// realTempDir returns t.TempDir(), realpath-resolved — required on macOS,
// where t.TempDir() lives under /var, itself a symlink to /private/var
// (the same fact resolvepath_test.go's confinedPolicy/fr2Policy helpers
// already document and resolve for).
func realTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	return resolved
}

// TestReadGate_RegistrySkillRefusedViaFileTool (test 45, FR-057) — ADR-072
// D10 Part A: a file-tool read of an installed registry skill file
// ($OMNIPUS_HOME/skills/<slug>/SKILL.md) is refused.
func TestReadGate_RegistrySkillRefusedViaFileTool(t *testing.T) {
	home := realTempDir(t)
	t.Setenv(config.EnvHome, home)

	skillFile := writeGateTestSkillFile(t, filepath.Join(home, "skills"), "foo", "Use when doing foo")

	policy := confinedPolicy(t, realTempDir(t))

	handle, err := ResolvePath(context.Background(), policy, "read_file", "call-1", FSOpRead, skillFile)
	require.Error(t, err)
	require.Nil(t, handle)
	assert.ErrorIs(t, err, ErrCarveOut, "a registry skill file must classify as D10 Part A's carve-out-shaped denial")
}

// TestReadGate_SkillToolStillLoads (test 46, FR-061) — ADR-072 D10: "the
// Skill tool needs no bypass" — SkillsLoader.LoadSkill reads with plain
// os.ReadFile, below the ResolvePath boundary, so it keeps working for
// exactly the path the file tool now refuses.
func TestReadGate_SkillToolStillLoads(t *testing.T) {
	home := realTempDir(t)
	t.Setenv(config.EnvHome, home)

	globalSkills := filepath.Join(home, "skills")
	skillFile := filepath.Join(globalSkills, "foo", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(skillFile), 0o755))
	require.NoError(t, os.WriteFile(skillFile,
		[]byte("---\nname: foo\ndescription: Use when doing foo\n---\n\ninstructions-marker\n"), 0o600))

	loader := skills.NewSkillsLoader("", globalSkills, "")
	content, ok := loader.LoadSkill("foo")
	require.True(t, ok, "the Skill tool's loader must still find the skill")
	assert.Contains(t, content, "instructions-marker")

	policy := confinedPolicy(t, realTempDir(t))
	_, err := ResolvePath(context.Background(), policy, "read_file", "call-1", FSOpRead, skillFile)
	require.Error(t, err, "the file tool must still be refused for the identical path the Skill tool just loaded")
}

// TestReadGate_MountOrdinaryFileReadable (test 47, FR-059) — ADR-072 D10
// Part B is location-scoped, not a whole-mount deny: an ordinary file
// alongside a project skill in the same mount stays readable.
func TestReadGate_MountOrdinaryFileReadable(t *testing.T) {
	mountRoot := realTempDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(mountRoot, "main.go"), []byte("package main\n"), 0o600))
	writeGateTestSkillFile(t, filepath.Join(mountRoot, ".claude", "skills"), "deploy", "Use when deploying")

	policy := confinedPolicy(t, realTempDir(t))
	policy.AllowedRoots = []string{mountRoot}

	handle, err := ResolvePath(context.Background(), policy, "read_file", "call-1", FSOpRead, filepath.Join(mountRoot, "main.go"))
	require.NoError(t, err)
	defer handle.Close()

	data, err := handle.ReadFile()
	require.NoError(t, err)
	assert.Equal(t, "package main\n", string(data))
}

// TestReadGate_MountProjectSkillReadable (test 48, FR-058/FR-078, D10.3) —
// D10.3 removed Part B entirely: a project skill's file under a mount's
// recognised skills directory is ordinarily readable via the file tool, even
// its own SKILL.md. D4.1 already makes the mount itself the grant — every
// agent in that workspace may load this skill via the Skill tool regardless
// — so a file-tool-level deny here protects nothing while breaking a skill's
// bundled helper files (the exact defect D10.3 exists to fix). Contrast with
// TestReadGate_RegistrySkillRefusedViaFileTool (45): the registry shelf's
// instruction file stays gated, because there an agent's per-agent grant
// list is the thing a file-tool read could otherwise bypass.
func TestReadGate_MountProjectSkillReadable(t *testing.T) {
	mountRoot := realTempDir(t)
	skillFile := writeGateTestSkillFile(t, filepath.Join(mountRoot, ".claude", "skills"), "deploy", "Use when deploying")

	policy := confinedPolicy(t, realTempDir(t))
	policy.AllowedRoots = []string{mountRoot}

	handle, err := ResolvePath(context.Background(), policy, "read_file", "call-1", FSOpRead, skillFile)
	require.NoError(t, err)
	defer handle.Close()

	data, err := handle.ReadFile()
	require.NoError(t, err)
	assert.Contains(t, string(data), "deploy")
}

// TestReadGate_RegistrySkillBundledFileReadable (test 48a, FR-061a/061b,
// D10.3) — D10.3's narrowed Part A gates a registry skill's INSTRUCTION file
// specifically, not its whole directory: a bundled sibling file (a helper
// script, template, or reference file the skill's own SKILL.md tells the
// agent to read or run) stays ordinarily readable via the file tool, even
// for a skill the acting agent is NOT granted. This is a deliberate,
// accepted cost (an author could hide instructions in a bundled file) traded
// against the alternative, which breaks the ordinary "run the script next to
// me" shape of a real skill.
func TestReadGate_RegistrySkillBundledFileReadable(t *testing.T) {
	home := realTempDir(t)
	t.Setenv(config.EnvHome, home)

	skillDir := filepath.Join(home, "skills", "foo")
	writeGateTestSkillFile(t, filepath.Join(home, "skills"), "foo", "Use when doing foo")
	helperPath := filepath.Join(skillDir, "helper.sh")
	require.NoError(t, os.WriteFile(helperPath, []byte("#!/bin/sh\necho bundled-helper-marker\n"), 0o755))

	policy := confinedPolicy(t, realTempDir(t))

	handle, err := ResolvePath(context.Background(), policy, "read_file", "call-1", FSOpRead, helperPath)
	require.NoError(t, err, "a bundled sibling file must stay readable — only the instruction file is gated")
	defer handle.Close()

	data, err := handle.ReadFile()
	require.NoError(t, err)
	assert.Contains(t, string(data), "bundled-helper-marker")
}

// TestReadGate_RegistrySkillAgentMdAlsoRefused (test 48b, FR-061c, D10.3) —
// the narrowed Part A gates all three recognised instruction filenames
// (SKILL.md, and the legacy AGENT.md/AGENTS.md naming this codebase's
// custom-agent format also accepts), not only SKILL.md.
func TestReadGate_RegistrySkillAgentMdAlsoRefused(t *testing.T) {
	home := realTempDir(t)
	t.Setenv(config.EnvHome, home)

	skillDir := filepath.Join(home, "skills", "foo")
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	agentMdPath := filepath.Join(skillDir, "AGENT.md")
	require.NoError(t, os.WriteFile(agentMdPath, []byte("---\nname: foo\ndescription: legacy format\n---\n"), 0o600))

	policy := confinedPolicy(t, realTempDir(t))

	_, err := ResolvePath(context.Background(), policy, "read_file", "call-1", FSOpRead, agentMdPath)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrCarveOut, "AGENT.md is a recognised instruction filename, same as SKILL.md")
}

// TestReadGate_MountInstructionFileReadable (test 49, FR-060) — ADR-072 D10
// Part B deliberately does not gate a mount's own instruction file: it is
// D7's always-injected context layer, not a skill, and denying the read
// would hide nothing the agent does not already have every turn.
func TestReadGate_MountInstructionFileReadable(t *testing.T) {
	mountRoot := realTempDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(mountRoot, "CLAUDE.md"), []byte("# Project instructions\n"), 0o600))
	writeGateTestSkillFile(t, filepath.Join(mountRoot, ".claude", "skills"), "deploy", "Use when deploying")

	policy := confinedPolicy(t, realTempDir(t))
	policy.AllowedRoots = []string{mountRoot}

	handle, err := ResolvePath(context.Background(), policy, "read_file", "call-1", FSOpRead, filepath.Join(mountRoot, "CLAUDE.md"))
	require.NoError(t, err)
	defer handle.Close()

	data, err := handle.ReadFile()
	require.NoError(t, err)
	assert.Contains(t, string(data), "Project instructions")
}

// newSkillsGateAuditLogger builds an audit.Logger writing into a fresh temp
// dir, installs it as the process-wide skills-write-audit logger
// (SetSkillsWriteAuditLogger), and arranges for both to be torn down and
// cleared at the end of the test — the package-level logger reference must
// never leak into an unrelated test in this package.
func newSkillsGateAuditLogger(t *testing.T) (*audit.Logger, string) {
	t.Helper()
	dir := t.TempDir()
	l, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
	require.NoError(t, err)
	SetSkillsWriteAuditLogger(l)
	t.Cleanup(func() {
		SetSkillsWriteAuditLogger(nil)
		_ = l.Close()
	})
	return l, dir
}

// readSkillsGateAuditEntries flushes auditLogger and parses every JSONL line
// in <dir>/audit.jsonl whose "event" field is SkillWriteAuditEvent.
func readSkillsGateAuditEntries(t *testing.T, auditLogger *audit.Logger, dir string) []map[string]any {
	t.Helper()
	require.NoError(t, auditLogger.Close())

	data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)

	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &parsed), "audit entry must be valid JSON: %s", line)
		if parsed["event"] == SkillWriteAuditEvent {
			out = append(out, parsed)
		}
	}
	return out
}

// TestAudit_ProjectSkillWriteRecorded (test 51a, FR-071) — ADR-072 D6.1.1:
// a write whose resolved path lands under a mount's recognised skills
// directory is audited, carrying the shelf and the resolved path.
func TestAudit_ProjectSkillWriteRecorded(t *testing.T) {
	auditLogger, dir := newSkillsGateAuditLogger(t)

	mountRoot := realTempDir(t)
	skillFile := writeGateTestSkillFile(t, filepath.Join(mountRoot, ".claude", "skills"), "deploy", "Use when deploying")

	policy := confinedPolicy(t, realTempDir(t))
	policy.AllowedRoots = []string{mountRoot}

	ctx := WithAgentID(context.Background(), "jim")
	ctx = WithWorkspaceID(ctx, "ws-1")
	ctx = WithTranscriptSessionID(ctx, "sess-1")

	handle, err := ResolvePath(ctx, policy, "edit_skill", "call-1", FSOpWrite, skillFile)
	require.NoError(t, err)
	require.NoError(t, handle.WriteFile([]byte("---\nname: deploy\ndescription: updated\n---\n")))
	require.NoError(t, handle.Close())

	entries := readSkillsGateAuditEntries(t, auditLogger, dir)
	require.Len(t, entries, 1)
	entry := entries[0]

	assert.Equal(t, "allow", entry["decision"])
	assert.Equal(t, "jim", entry["agent_id"])

	details, ok := entry["details"].(map[string]any)
	require.True(t, ok, "details must be an object: %#v", entry["details"])
	assert.Equal(t, skillShelfProject, details["shelf"])
	assert.Equal(t, "ws-1", details["workspace_id"])
	assert.Equal(t, skillFile, details["path"])
}

// TestProjectSkill_WrittenThenLoadableViaTool (test 51b) — ADR-072 D6.1's
// headline guarantee, restated for D10.3: there is no project skill an agent
// can write but cannot read — via EITHER path. After a write through
// ResolvePath, the SAME content is what project-skill discovery (the
// mechanism the Skill tool reads through, below the ResolvePath boundary)
// finds, and — since D10.3 removed Part B's project-shelf read denial
// entirely — the file tool reads the identical, just-written content too.
func TestProjectSkill_WrittenThenLoadableViaTool(t *testing.T) {
	mountRoot := realTempDir(t)
	skillFile := writeGateTestSkillFile(t, filepath.Join(mountRoot, ".claude", "skills"), "deploy", "Use when deploying")

	policy := confinedPolicy(t, realTempDir(t))
	policy.AllowedRoots = []string{mountRoot}

	newContent := "---\nname: deploy\ndescription: Use when deploying\n---\n\nUPDATED-CONTENT-MARKER\n"
	handle, err := ResolvePath(context.Background(), policy, "edit_skill", "call-1", FSOpWrite, skillFile)
	require.NoError(t, err)
	require.NoError(t, handle.WriteFile([]byte(newContent)))
	require.NoError(t, handle.Close())

	discovered, collisions := skills.DiscoverProjectSkills("acme", mountRoot)
	require.Empty(t, collisions)
	require.Len(t, discovered, 1)
	require.Equal(t, skillFile, discovered[0].Path)

	got, err := os.ReadFile(discovered[0].Path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "UPDATED-CONTENT-MARKER", "the Skill tool's own read path must see what was just written")

	handle2, err := ResolvePath(context.Background(), policy, "read_file", "call-2", FSOpRead, skillFile)
	require.NoError(t, err, "D10.3: the file tool must ALSO see the same just-written content — Part B's project-shelf denial is gone")
	defer handle2.Close()
	got2, err := handle2.ReadFile()
	require.NoError(t, err)
	assert.Contains(t, string(got2), "UPDATED-CONTENT-MARKER")
}

// TestAudit_WriteFileToSkillPathIsAudited (test 51c, CRIT-002/FR-071) —
// ADR-072 D6.1.1's whole reason for moving the hook off the authoring tool:
// a project skill modified via the GENERIC write tool (not an authoring
// verb) is audited too, because the hook lives at the shared path resolver
// every file tool routes through.
func TestAudit_WriteFileToSkillPathIsAudited(t *testing.T) {
	auditLogger, dir := newSkillsGateAuditLogger(t)

	mountRoot := realTempDir(t)
	skillFile := writeGateTestSkillFile(t, filepath.Join(mountRoot, ".claude", "skills"), "deploy", "Use when deploying")

	policy := confinedPolicy(t, realTempDir(t))
	policy.AllowedRoots = []string{mountRoot}

	ctx := WithAgentID(context.Background(), "ray")
	ctx = WithWorkspaceID(ctx, "ws-1")

	// "write_file" — the generic tool's own Name(), never an authoring verb
	// like create_skill/edit_skill/remove_skill.
	handle, err := ResolvePath(ctx, policy, "write_file", "call-1", FSOpWrite, skillFile)
	require.NoError(t, err)
	require.NoError(t, handle.WriteFile([]byte("---\nname: deploy\ndescription: clobbered\n---\n")))
	require.NoError(t, handle.Close())

	entries := readSkillsGateAuditEntries(t, auditLogger, dir)
	require.Len(t, entries, 1, "a write_file call must be audited exactly like an authoring-verb write")
	assert.Equal(t, "write_file", entries[0]["tool"])
}

// TestAudit_RecordNamesPerformingTool (test 51d, FR-071a) — the audit record
// distinguishes the sanctioned authoring route from the generic route by
// naming the ACTUAL tool that performed each write, not a fixed literal.
func TestAudit_RecordNamesPerformingTool(t *testing.T) {
	auditLogger, dir := newSkillsGateAuditLogger(t)

	mountRoot := realTempDir(t)
	skillFile := writeGateTestSkillFile(t, filepath.Join(mountRoot, ".claude", "skills"), "deploy", "Use when deploying")

	policy := confinedPolicy(t, realTempDir(t))
	policy.AllowedRoots = []string{mountRoot}

	ctx := WithAgentID(context.Background(), "ray")

	h1, err := ResolvePath(ctx, policy, "edit_skill", "call-1", FSOpWrite, skillFile)
	require.NoError(t, err)
	require.NoError(t, h1.WriteFile([]byte("---\nname: deploy\ndescription: first\n---\n")))
	require.NoError(t, h1.Close())

	h2, err := ResolvePath(ctx, policy, "write_file", "call-2", FSOpWrite, skillFile)
	require.NoError(t, err)
	require.NoError(t, h2.WriteFile([]byte("---\nname: deploy\ndescription: second\n---\n")))
	require.NoError(t, h2.Close())

	entries := readSkillsGateAuditEntries(t, auditLogger, dir)
	require.Len(t, entries, 2)

	tools := []any{entries[0]["tool"], entries[1]["tool"]}
	assert.Contains(t, tools, "edit_skill")
	assert.Contains(t, tools, "write_file")
}
