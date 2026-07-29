// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListDirTool_DoesNotHideLibraryDotDirectory is the explicit
// dot-directory-visibility guard the operator called out directly:
// "make sure dot-directories are not silently filtered out, or the agent
// will be blind to exactly the files we just fixed." This drives the
// GENERIC list_directory tool (not library_list) over a workspace root that
// contains .library/ alongside ordinary files, proving the plain file-tool
// path an agent would also use to explore the workspace root sees .library
// as a real DIR entry — not hidden by any dotfile convention.
func TestListDirTool_DoesNotHideLibraryDotDirectory(t *testing.T) {
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, libraryDirName), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "todo.txt"), []byte("x"), 0o644))

	tool := NewListDirTool(ws, true)
	result := tool.Execute(context.Background(), map[string]any{"path": "."})
	require.False(t, result.IsError, result.ForLLM)
	assert.Contains(t, result.ForLLM, "DIR:  "+libraryDirName,
		"the workspace-root listing must show .library/ as a real directory entry, not hide it")
	assert.Contains(t, result.ForLLM, "FILE: todo.txt")
}

// TestLibraryListTool_ListsUploadedFile proves library_list surfaces a file
// staged at .library/<name> — the D-1 dual-write destination — by name.
func TestLibraryListTool_ListsUploadedFile(t *testing.T) {
	ws := t.TempDir()
	libDir := filepath.Join(ws, libraryDirName)
	require.NoError(t, os.MkdirAll(libDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(libDir, "report.pptx"), []byte("PPTX"), 0o644))

	tool := NewLibraryListTool(ws, true)
	result := tool.Execute(context.Background(), map[string]any{})
	require.False(t, result.IsError, result.ForLLM)
	assert.Contains(t, result.ForLLM, "FILE: report.pptx")
}

// TestLibraryListTool_DoesNotHideNestedDotEntries verifies a dot-prefixed
// entry INSIDE .library/ itself is also not filtered — the operator's
// "many listing helpers skip dotfiles by default" trap, checked at every
// level this tool can reach, not just the top one.
func TestLibraryListTool_DoesNotHideNestedDotEntries(t *testing.T) {
	ws := t.TempDir()
	libDir := filepath.Join(ws, libraryDirName)
	require.NoError(t, os.MkdirAll(filepath.Join(libDir, ".nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(libDir, ".hidden-name.txt"), []byte("x"), 0o644))

	tool := NewLibraryListTool(ws, true)
	result := tool.Execute(context.Background(), map[string]any{})
	require.False(t, result.IsError, result.ForLLM)
	assert.Contains(t, result.ForLLM, "DIR:  .nested")
	assert.Contains(t, result.ForLLM, "FILE: .hidden-name.txt")
}

// TestLibraryListTool_Subdirectory verifies the optional "path" parameter
// narrows to a subdirectory within the library.
func TestLibraryListTool_Subdirectory(t *testing.T) {
	ws := t.TempDir()
	sub := filepath.Join(ws, libraryDirName, "nested")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "inner.txt"), []byte("x"), 0o644))

	tool := NewLibraryListTool(ws, true)
	result := tool.Execute(context.Background(), map[string]any{"path": "nested"})
	require.False(t, result.IsError, result.ForLLM)
	assert.Contains(t, result.ForLLM, "FILE: inner.txt")
}

// TestLibraryListTool_EscapeRejected verifies a ".." attempt to leave
// .library/ is refused with a clear error, not silently reinterpreted as
// "somewhere else in work/".
func TestLibraryListTool_EscapeRejected(t *testing.T) {
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, libraryDirName), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "secret.txt"), []byte("x"), 0o644))

	tool := NewLibraryListTool(ws, true)
	result := tool.Execute(context.Background(), map[string]any{"path": ".."})
	assert.True(t, result.IsError, "an escaping path must be refused")
	assert.Contains(t, result.ForLLM, "escapes it")
}

// TestLibraryListTool_MissingDirectoryIsAnError verifies a workspace with no
// uploads yet (no .library/ directory) surfaces a real error rather than a
// fabricated empty success — honest signal over a fake "nothing here".
func TestLibraryListTool_MissingDirectoryIsAnError(t *testing.T) {
	ws := t.TempDir() // no .library/ created

	tool := NewLibraryListTool(ws, true)
	result := tool.Execute(context.Background(), map[string]any{})
	assert.True(t, result.IsError)
}

// TestLibraryReadTool_ReadsUploadedFile is the round-trip proof required by
// the task: a file staged the way the D-1 dual-write stages it must be
// readable through the agent's normal rooted file tool (here: library_read,
// restrict=true — the SAME os.Root-confined path a real agent turn uses).
func TestLibraryReadTool_ReadsUploadedFile(t *testing.T) {
	ws := t.TempDir()
	libDir := filepath.Join(ws, libraryDirName)
	require.NoError(t, os.MkdirAll(libDir, 0o755))
	content := "Copy of elicify_company_profile contents"
	uploadedPath := filepath.Join(libDir, "Copy of elicify_company_profile.txt")
	require.NoError(t, os.WriteFile(uploadedPath, []byte(content), 0o644))

	tool := NewLibraryReadTool(ws, true, MaxReadFileSize)
	result := tool.Execute(context.Background(), map[string]any{"path": "Copy of elicify_company_profile.txt"})
	require.False(t, result.IsError, result.ForLLM)
	assert.Contains(t, result.ForLLM, content)
}

// TestLibraryReadTool_MissingPathIsError mirrors ReadFileTool's own
// "path is required" contract.
func TestLibraryReadTool_MissingPathIsError(t *testing.T) {
	ws := t.TempDir()
	tool := NewLibraryReadTool(ws, true, MaxReadFileSize)
	result := tool.Execute(context.Background(), map[string]any{})
	assert.True(t, result.IsError)
	assert.Contains(t, strings.ToLower(result.ForLLM), "path is required")
}

// TestLibraryReadTool_NotFoundIsError verifies reading a name that was never
// uploaded (the exact UAT failure mode — guessing a filename) surfaces a
// real "not found" style error rather than fabricated content.
func TestLibraryReadTool_NotFoundIsError(t *testing.T) {
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, libraryDirName), 0o755))

	tool := NewLibraryReadTool(ws, true, MaxReadFileSize)
	result := tool.Execute(context.Background(), map[string]any{"path": "Elicify.pptx"})
	assert.True(t, result.IsError, "a guessed, never-uploaded filename must fail, not fabricate content")
}

// TestLibraryReadTool_EscapeRejected mirrors the list-side escape guard.
func TestLibraryReadTool_EscapeRejected(t *testing.T) {
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, libraryDirName), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "secret.txt"), []byte("do not read"), 0o644))

	tool := NewLibraryReadTool(ws, true, MaxReadFileSize)
	result := tool.Execute(context.Background(), map[string]any{"path": "../secret.txt"})
	assert.True(t, result.IsError)
	assert.Contains(t, result.ForLLM, "escapes it")
}

func TestJoinLibraryPath(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"empty defaults to root", "", libraryDirName, false},
		{"dot defaults to root", ".", libraryDirName, false},
		{"simple file", "report.pptx", libraryDirName + "/report.pptx", false},
		{"subdirectory", "nested/inner.txt", filepath.Join(libraryDirName, "nested", "inner.txt"), false},
		{"escape rejected", "..", "", true},
		{"escape via nested rejected", "../secret.txt", "", true},
		{"absolute rejected", "/etc/passwd", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := joinLibraryPath(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
