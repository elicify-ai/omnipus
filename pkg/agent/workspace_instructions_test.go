// Omnipus — tests for per-turn workspace instructions injection (v0.1.0)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"path/filepath"
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
