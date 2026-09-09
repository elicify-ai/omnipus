// Omnipus — tests for per-turn workspace instructions injection (v0.1.0)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// ─── injectWorkspaceInstructions unit tests ─────────────────────────────────

// TestInjectWorkspaceInstructions_InsertsAtIndex1 proves the note lands at
// index 1 with role "system", and history messages are shifted right.
func TestInjectWorkspaceInstructions_InsertsAtIndex1(t *testing.T) {
	msgs := []providers.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	result := injectWorkspaceInstructions(msgs, "# Workspace Instructions\n\nDo great things.")

	require.Len(t, result, 4, "injectWorkspaceInstructions must add exactly 1 message")
	assert.Equal(t, "system", result[0].Role, "index 0 must remain the system prompt")
	assert.Equal(t, "system prompt", result[0].Content)
	assert.Equal(t, "system", result[1].Role, "index 1 must be the injected workspace instructions (role=system)")
	assert.Contains(
		t,
		result[1].Content,
		"Workspace Instructions",
		"index 1 must contain the workspace instructions header",
	)
	assert.Equal(t, "user", result[2].Role, "index 2 must be the original user message")
	assert.Equal(t, "assistant", result[3].Role, "index 3 must be the original assistant message")
}

// TestInjectWorkspaceInstructions_EmptyNoteNoOp proves no injection when note is "".
func TestInjectWorkspaceInstructions_EmptyNoteNoOp(t *testing.T) {
	msgs := []providers.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "hello"},
	}
	result := injectWorkspaceInstructions(msgs, "")
	assert.Equal(t, msgs, result, "empty note must return msgs unchanged")
}

// TestInjectWorkspaceInstructions_EmptyMsgsNoOp proves no injection when msgs is empty.
func TestInjectWorkspaceInstructions_EmptyMsgsNoOp(t *testing.T) {
	result := injectWorkspaceInstructions([]providers.Message{}, "# Workspace Instructions\n\nHello.")
	assert.Empty(t, result, "empty msgs must return empty slice unchanged")
}

// TestInjectWorkspaceInstructions_SingleMsg proves injection works with 1 message.
func TestInjectWorkspaceInstructions_SingleMsg(t *testing.T) {
	msgs := []providers.Message{
		{Role: "system", Content: "system prompt"},
	}
	result := injectWorkspaceInstructions(msgs, "# Workspace Instructions\n\nHello.")
	require.Len(t, result, 2)
	assert.Equal(t, "system", result[0].Role)
	assert.Equal(t, "system", result[1].Role)
	assert.Contains(t, result[1].Content, "Workspace Instructions")
}

// TestInjectWorkspaceInstructions_OrderRelativeToManifestNote proves the
// call-order contract: calling injectWorkspaceInstructions (inserts at [1])
// BEFORE injectManifestNote (also inserts at [1]) yields the final ordering:
//
//	[0] system prompt · [1] manifest note · [2] workspace instructions · [3+] history
func TestInjectWorkspaceInstructions_OrderRelativeToManifestNote(t *testing.T) {
	msgs := []providers.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "hello"},
	}
	wsNote := "# Workspace Instructions\n\nDo great things."
	manifestNote := "## More tools\n  - create_agent"

	// Mirror the loop.go call order.
	out := injectWorkspaceInstructions(msgs, wsNote)
	out = injectManifestNote(out, manifestNote)

	require.Len(t, out, 4, "both injections must each add 1 message; want 4 total")
	assert.Equal(t, "system prompt", out[0].Content, "[0] must be the system prompt")
	assert.Contains(t, out[1].Content, "More tools", "[1] must be the manifest note")
	assert.Contains(t, out[2].Content, "Workspace Instructions", "[2] must be the workspace instructions")
	assert.Equal(t, "user", out[3].Role, "[3] must be the original user message")
}

// TestInjectWorkspaceInstructions_NoManifestNote proves that when the manifest
// note is absent (injectManifestNote is a no-op), the workspace instructions
// land at [1] directly after the system prompt.
func TestInjectWorkspaceInstructions_NoManifestNote(t *testing.T) {
	msgs := []providers.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "hello"},
	}
	wsNote := "# Workspace Instructions\n\nDo great things."

	out := injectWorkspaceInstructions(msgs, wsNote)
	// No injectManifestNote call (absent/empty manifest note).
	out = injectManifestNote(out, "") // explicit no-op

	require.Len(t, out, 3)
	assert.Equal(t, "system prompt", out[0].Content, "[0] must be the system prompt")
	assert.Contains(
		t,
		out[1].Content,
		"Workspace Instructions",
		"[1] must be workspace instructions when no manifest note",
	)
	assert.Equal(t, "user", out[2].Role)
}

// ─── buildWorkspaceInstructionsNote unit tests ──────────────────────────────

// seedWorkspace creates a minimal workspace setup in tmpDir for testing:
//   - Writes the workspace's AGENT.md via workspace.WriteInstructions.
//   - Writes a workspaces/<id>.json with is_default=true so ResolveDefaultID works.
//
// Returns the workspace ID.
func seedWorkspace(t *testing.T, tmpDir, content string) string {
	t.Helper()
	id := "ws-test-01"

	// Write AGENT.md content if provided.
	if content != "" {
		err := workspace.WriteInstructions(tmpDir, id, content)
		require.NoError(t, err, "WriteInstructions must succeed")
	}

	// Write the workspace JSON record so ResolveDefaultID can find the default.
	wsDir := filepath.Join(tmpDir, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	record := `{"id":"` + id + `","is_default":true}`
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, id+".json"), []byte(record), 0o644))

	return id
}

// TestBuildWorkspaceInstructionsNote_ExplicitID_Present proves that when the
// workspace ID is given explicitly and AGENT.md exists, the note contains the
// "# Workspace Instructions" header and the file content.
func TestBuildWorkspaceInstructionsNote_ExplicitID_Present(t *testing.T) {
	tmpDir := t.TempDir()
	id := seedWorkspace(t, tmpDir, "Be concise and helpful.")
	t.Setenv("OMNIPUS_HOME", tmpDir)

	note := buildWorkspaceInstructionsNote(id)

	require.NotEmpty(t, note)
	assert.Equal(t, "# Workspace Instructions\n\nBe concise and helpful.", note)
}

// TestBuildWorkspaceInstructionsNote_ExplicitID_Absent proves that when the
// AGENT.md file does not exist for an explicit workspace ID, the function
// returns "" (absent file is a valid empty state, not an error).
func TestBuildWorkspaceInstructionsNote_ExplicitID_Absent(t *testing.T) {
	tmpDir := t.TempDir()
	// Only seed the workspace JSON, not the AGENT.md.
	id := seedWorkspace(t, tmpDir, "")
	t.Setenv("OMNIPUS_HOME", tmpDir)

	note := buildWorkspaceInstructionsNote(id)

	assert.Empty(t, note, "absent AGENT.md must produce an empty note")
}

// TestBuildWorkspaceInstructionsNote_DefaultWorkspace proves that when the
// workspace ID is "" (no explicit ID), ResolveDefaultID is used and the
// default workspace's AGENT.md is read.
func TestBuildWorkspaceInstructionsNote_DefaultWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	seedWorkspace(t, tmpDir, "Focus on security.")
	t.Setenv("OMNIPUS_HOME", tmpDir)

	// Pass "" to trigger the ResolveDefaultID path.
	note := buildWorkspaceInstructionsNote("")

	require.NotEmpty(t, note)
	assert.Equal(t, "# Workspace Instructions\n\nFocus on security.", note)
}

// TestBuildWorkspaceInstructionsNote_NoDefaultWorkspace proves that when the
// workspace ID is "" and no default workspace exists, the function returns ""
// without error (ErrNoDefault is a normal, non-error state for fresh installs).
func TestBuildWorkspaceInstructionsNote_NoDefaultWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)

	note := buildWorkspaceInstructionsNote("")

	assert.Empty(t, note, "no default workspace must produce an empty note (not an error)")
}

// TestBuildWorkspaceInstructionsNote_WhitespaceOnlyContent proves that AGENT.md
// content that is whitespace-only after TrimSpace produces an empty note.
func TestBuildWorkspaceInstructionsNote_WhitespaceOnlyContent(t *testing.T) {
	tmpDir := t.TempDir()
	id := "ws-ws-only"
	// Write whitespace-only content directly (WriteInstructions treats it as clear
	// and removes the file, so we write the file directly).
	wsDir := filepath.Join(tmpDir, "workspaces", id)
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	agentMD := filepath.Join(wsDir, "AGENT.md")
	require.NoError(t, os.WriteFile(agentMD, []byte("   \n\t\n   "), 0o644))
	// Seed the workspace JSON record.
	wsMetaDir := filepath.Join(tmpDir, "workspaces")
	record := `{"id":"` + id + `","is_default":true}`
	require.NoError(t, os.WriteFile(filepath.Join(wsMetaDir, id+".json"), []byte(record), 0o644))
	t.Setenv("OMNIPUS_HOME", tmpDir)

	note := buildWorkspaceInstructionsNote(id)
	assert.Empty(t, note, "whitespace-only AGENT.md must produce an empty note")
}

// TestBuildWorkspaceInstructionsNote_ContentIsTrimmed proves that leading/trailing
// whitespace in the AGENT.md content is trimmed in the returned note.
func TestBuildWorkspaceInstructionsNote_ContentIsTrimmed(t *testing.T) {
	tmpDir := t.TempDir()
	id := "ws-trim"
	wsDir := filepath.Join(tmpDir, "workspaces", id)
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	agentMD := filepath.Join(wsDir, "AGENT.md")
	require.NoError(t, os.WriteFile(agentMD, []byte("  \n  Keep it short.\n  \n"), 0o644))
	wsMetaDir := filepath.Join(tmpDir, "workspaces")
	record := `{"id":"` + id + `","is_default":true}`
	require.NoError(t, os.WriteFile(filepath.Join(wsMetaDir, id+".json"), []byte(record), 0o644))
	t.Setenv("OMNIPUS_HOME", tmpDir)

	note := buildWorkspaceInstructionsNote(id)
	assert.Equal(t, "# Workspace Instructions\n\nKeep it short.", note)
}

// ─── ADR-072 R2 fix: mounted-project CLAUDE.md/AGENTS.md reaching the note ──
//
// Before this fix, pkg/skills/project_instructions.go's
// SelectProjectInstructionFile/ComposeProjectInstructions (D7) had zero
// production callers anywhere outside their own file and tests — confirmed
// live in docs/internal/qa/uat-report-skill-activation-batch2-groupD-2026-09-02.md
// (S21: zero trace of a mounted CLAUDE.md's content in the assembled system
// prompt across every turn). These tests exercise buildWorkspaceInstructionsNote
// itself (the exact function pkg/agent/loop.go's injectWorkspaceInstructions
// call site, and pkg/agent/midturn_budget.go's identical call, both use) with
// a REAL mount record + REAL CLAUDE.md file on disk, rather than only the
// underlying pure functions those already have unit tests for.

// seedMountForInstructions writes a mount record for wsID under home naming
// one mount (mountName -> mountRoot), mirroring
// pkg/workspace/mountstore.go's unexported on-disk JSON shape exactly the way
// pkg/agent/project_shelf_wiring_test.go's seedProjectShelfWorkspace already
// does for the R1 tests.
func seedMountForInstructions(t *testing.T, home, wsID, mountName, mountRoot string) {
	t.Helper()
	mountsDir := filepath.Join(home, "entities", "mounts")
	require.NoError(t, os.MkdirAll(mountsDir, 0o700))
	rec := struct {
		WorkspaceID string `json:"workspace_id"`
		Mounts      []struct {
			Name     string `json:"name"`
			HostPath string `json:"host_path"`
		} `json:"mounts"`
	}{
		WorkspaceID: wsID,
	}
	rec.Mounts = append(rec.Mounts, struct {
		Name     string `json:"name"`
		HostPath string `json:"host_path"`
	}{Name: mountName, HostPath: mountRoot})
	data, err := json.Marshal(rec)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(mountsDir, wsID+".json"), data, 0o600))
}

// TestBuildWorkspaceInstructionsNote_MountContributesProjectInstructions is
// the R2 regression test: a mounted repository's own root CLAUDE.md must
// reach the per-turn note, labelled with its mount name, AFTER the
// workspace's own AGENT.md (D7: "the operator's intent outranks the
// repository's").
func TestBuildWorkspaceInstructionsNote_MountContributesProjectInstructions(t *testing.T) {
	tmpDir := t.TempDir()
	id := seedWorkspace(t, tmpDir, "Be concise and helpful.")
	t.Setenv("OMNIPUS_HOME", tmpDir)

	mountRoot := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(mountRoot, "CLAUDE.md"),
		[]byte("# Acme Service\n\nAlways run `make test` before committing.\nUse `make deploy` — never call kubectl directly.\n"),
		0o644,
	))
	seedMountForInstructions(t, tmpDir, id, "acme", mountRoot)

	note := buildWorkspaceInstructionsNote(id)

	require.NotEmpty(t, note)
	assert.Contains(t, note, "# Workspace Instructions")
	assert.Contains(t, note, "Be concise and helpful.", "the workspace's own AGENT.md content must still be present")
	assert.Contains(t, note, "Acme Service", "the mounted CLAUDE.md's content must be present")
	assert.Contains(t, note, "kubectl", "the mounted CLAUDE.md's content must be present in full")
	assert.Contains(t, note, "acme", "the mount block must be labelled with its mount name (D7)")

	// D7's stated ordering: the workspace's own instructions come first.
	ownIdx := strings.Index(note, "Be concise and helpful.")
	mountIdx := strings.Index(note, "Acme Service")
	require.NotEqual(t, -1, ownIdx)
	require.NotEqual(t, -1, mountIdx)
	assert.Less(t, ownIdx, mountIdx, "the workspace's own instructions must precede the mounted project's")
}

// TestBuildWorkspaceInstructionsNote_MountOnly_NoWorkspaceInstructions proves
// a mount's CLAUDE.md reaches the note even when the workspace itself has no
// AGENT.md at all (the common case for a workspace that exists purely to
// hold a mounted repository).
func TestBuildWorkspaceInstructionsNote_MountOnly_NoWorkspaceInstructions(t *testing.T) {
	tmpDir := t.TempDir()
	id := seedWorkspace(t, tmpDir, "") // no AGENT.md
	t.Setenv("OMNIPUS_HOME", tmpDir)

	mountRoot := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(mountRoot, "AGENTS.md"),
		[]byte("Run the linter before every commit."),
		0o644,
	))
	seedMountForInstructions(t, tmpDir, id, "widget-repo", mountRoot)

	note := buildWorkspaceInstructionsNote(id)

	require.NotEmpty(t, note)
	assert.Contains(t, note, "# Workspace Instructions")
	assert.Contains(t, note, "Run the linter before every commit.")
	assert.Contains(t, note, "widget-repo")
}

// TestBuildWorkspaceInstructionsNote_MountWithNoInstructionFile_NoOp proves
// D6's "silent when there is nothing to find": a mount with neither
// CLAUDE.md nor AGENTS.md at its root contributes nothing, and the note is
// exactly the workspace-only note (no regression, no stray separator).
func TestBuildWorkspaceInstructionsNote_MountWithNoInstructionFile_NoOp(t *testing.T) {
	tmpDir := t.TempDir()
	id := seedWorkspace(t, tmpDir, "Be concise and helpful.")
	t.Setenv("OMNIPUS_HOME", tmpDir)

	mountRoot := t.TempDir() // empty — no CLAUDE.md/AGENTS.md
	seedMountForInstructions(t, tmpDir, id, "empty-repo", mountRoot)

	note := buildWorkspaceInstructionsNote(id)
	assert.Equal(t, "# Workspace Instructions\n\nBe concise and helpful.", note,
		"a mount with no root instruction file must contribute nothing")
}
