package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWriteFileTool_AlreadyExists_ReasonDistinguishesFromIOFailure is the
// RC-2 regression test: it FAILS on the old code (ToolResult had no Reason
// field at all, so this test would not even compile against it) and passes
// once write_file's overwrite=false guard is tagged with ReasonAlreadyExists
// (pkg/tools/filesystem.go, WriteFileTool.Execute).
//
// The point of RC-2 is that a caller must be able to tell "a sibling already
// wrote this file, no action needed" apart from "the write genuinely failed"
// WITHOUT string-matching ForLLM's prose. This test proves exactly that: it
// drives both outcomes through the real tool and asserts they are
// distinguishable purely via the structured Reason field.
func TestWriteFileTool_AlreadyExists_ReasonDistinguishesFromIOFailure(t *testing.T) {
	t.Run("already-exists refusal carries ReasonAlreadyExists", func(t *testing.T) {
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "existing.txt")
		require.NoError(t, os.WriteFile(testFile, []byte("original"), 0o644))

		tool := NewWriteFileTool(tmpDir, false)
		result := tool.Execute(context.Background(), map[string]any{
			"path":    testFile,
			"content": "new content",
		})

		require.True(t, result.IsError, "the requested write did not happen, so this remains an error outcome")
		assert.Equal(t, ReasonAlreadyExists, result.Reason,
			"overwrite refusal must be structurally tagged, not just prose-tagged")

		// Original content must be untouched.
		data, err := os.ReadFile(testFile)
		require.NoError(t, err)
		assert.Equal(t, "original", string(data))
	})

	t.Run("genuine I/O failure carries the zero-value Reason", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX-only permission bits (see #113)")
		}
		if os.Getuid() == 0 {
			t.Skip("skipping permission test: running as root")
		}
		tmpDir := t.TempDir()
		readOnlyDir := filepath.Join(tmpDir, "readonly")
		require.NoError(t, os.Mkdir(readOnlyDir, 0o755))
		require.NoError(t, os.Chmod(readOnlyDir, 0o500)) // read+execute, no write
		defer os.Chmod(readOnlyDir, 0o755)               // ensure TempDir cleanup can remove it

		targetFile := filepath.Join(readOnlyDir, "newfile.txt")

		tool := NewWriteFileTool(tmpDir, false)
		result := tool.Execute(context.Background(), map[string]any{
			"path":    targetFile,
			"content": "should not be written",
		})

		require.True(t, result.IsError, "the write genuinely failed")
		assert.Empty(t, string(result.Reason),
			"a real I/O failure must NOT be tagged ReasonAlreadyExists — this is the exact confusion RC-2 fixes")
		assert.NotContains(t, result.ForLLM, "already exists",
			"sanity check: this failure is not the overwrite-guard path at all")

		// Nothing was written.
		_, statErr := os.Stat(targetFile)
		assert.True(t, os.IsNotExist(statErr))
	})
}

// TestToolResult_Reason_BackwardCompatible pins the backward-compatibility
// contract from ResultReason's doc comment: a ToolResult built the old way
// (no Reason ever set) must behave identically to before this field existed
// — zero value "", omitted from JSON, and every legacy constructor
// unaffected.
func TestToolResult_Reason_BackwardCompatible(t *testing.T) {
	legacy := ErrorResult("Failed to connect to database: connection refused")

	assert.Equal(t, ResultReason(""), legacy.Reason, "legacy constructors must leave Reason at its zero value")
	assert.True(t, legacy.IsError)

	encoded, err := json.Marshal(legacy)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), `"reason"`,
		"omitempty must drop the zero-value Reason from JSON so persisted/legacy shapes are unaffected")

	// A fresh success/silent/user/media/async result also defaults to the
	// zero-value Reason.
	for name, r := range map[string]*ToolResult{
		"NewToolResult": NewToolResult("ok"),
		"SilentResult":  SilentResult("ok"),
		"AsyncResult":   AsyncResult("ok"),
		"UserResult":    UserResult("ok"),
		"MediaResult":   MediaResult("ok", []string{"media://x"}),
	} {
		assert.Equal(t, ResultReason(""), r.Reason, "%s must default Reason to zero value", name)
	}

	// WithReason is opt-in and chainable, mirroring WithError.
	tagged := ErrorResult("file: /tmp/x already exists. Set overwrite=true to replace.").
		WithReason(ReasonAlreadyExists)
	assert.Equal(t, ReasonAlreadyExists, tagged.Reason)
	assert.True(t, tagged.IsError)
}
