package tools

// ADR-059 W5 — a precondition refusal must be machine-distinguishable from an
// I/O failure (FR-007).
//
// The defect: write_file's "already exists" refusal and a genuine write
// failure arrived in the same shape — prose, IsError=true. A delegated worker
// told to produce a file that a sibling had already produced could not tell
// "no action needed" from "the write broke", so it either retried a write that
// would never succeed or reported failure for work that was already done. The
// orchestrator inherited that ambiguity.
//
// An earlier attempt put the distinction in a Go struct field (ToolResult.
// Reason). W3 deleted it: the consumer is a language model, and a language
// model reads ForLLM. Nothing else. So the discriminator has to live in the
// bytes the model receives.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeRefusal parses a tool result's ForLLM as the FileExistsRefusal wire
// payload, failing the test if it is not one.
func decodeRefusal(t *testing.T, forLLM string) map[string]any {
	t.Helper()
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(forLLM), &parsed),
		"refusal must be a JSON object the calling agent can parse, got: %q", forLLM)
	return parsed
}

// TestWriteFile_RefusalCarriesDiscriminator_FailureDoesNot is TDD #8: the two
// outcomes must be distinguishable without matching on wording.
func TestWriteFile_RefusalCarriesDiscriminator_FailureDoesNot(t *testing.T) {
	tmpDir := t.TempDir()
	existing := filepath.Join(tmpDir, "already-there.txt")
	require.NoError(t, os.WriteFile(existing, []byte("prior content"), 0o600))

	tool := NewWriteFileTool(tmpDir, false)

	refusal := tool.Execute(t.Context(), map[string]any{
		"path":    existing,
		"content": "new content",
	})
	require.True(t, refusal.IsError, "a refusal is still an error result")

	parsed := decodeRefusal(t, refusal.ForLLM)
	assert.Equal(t, FileExistsRefusalCode, parsed["error"],
		"the refusal must carry the fixed discriminator — this is the whole of FR-007")
	assert.Equal(t, "write_file", parsed["tool"])
	assert.Equal(t, existing, parsed["path"])
	assert.Contains(t, parsed["reason"], "already exists",
		"the original sentence must survive as the reason — the fix adds a tag, it does not "+
			"remove what a human or a model could already read")
	assert.Contains(t, parsed["reason"], "overwrite=true",
		"the reason must still say how to proceed, or the tag has made the result less useful")

	// The file must be untouched: a refusal is a refusal.
	content, err := os.ReadFile(existing)
	require.NoError(t, err)
	assert.Equal(t, "prior content", string(content))

	// The contrast case. A genuine I/O failure must NOT carry the
	// discriminator — otherwise the tag means "write_file failed", which is
	// exactly the conflation this exists to end. Writing to a path whose
	// parent is a regular file cannot succeed and is not a precondition
	// refusal.
	notADir := filepath.Join(tmpDir, "regular.txt")
	require.NoError(t, os.WriteFile(notADir, []byte("x"), 0o600))
	ioFailure := tool.Execute(t.Context(), map[string]any{
		"path":    filepath.Join(notADir, "child.txt"),
		"content": "content",
	})
	require.True(t, ioFailure.IsError, "test premise: this write must actually fail")
	assert.NotContains(t, ioFailure.ForLLM, FileExistsRefusalCode,
		"a genuine I/O failure must not carry the precondition discriminator — if it does, the "+
			"discriminator distinguishes nothing")
}

// TestWriteFile_RefusalDiscriminatorSurvivesTruncation is TDD #9.
//
// Both the persisted transcript and the live error frame cap this string at
// 2000 runes (maxFailClosedOutputChars in pkg/agent, maxLiveErrorChars in
// pkg/gateway). The consumer that matters — the gateway's
// parseStructuredToolFailure — recovers the discriminator with a WHOLE-JSON
// unmarshal, so a payload cut at that boundary does not merely lose its tail:
// it stops parsing entirely, and replay then renders the JSON fragment
// verbatim where the live view showed a sentence.
//
// Prefix-positioning the discriminator does NOT save that. An earlier version
// of this test asserted only prefix position and justified skipping the
// long-payload case with "the OS caps a path below the truncation bound" —
// which is false: the path appears twice in the payload, so ~950 characters
// already overflows, well inside macOS's 1024-byte PATH_MAX and trivially
// inside Linux's 4096. The real fix is at the producer, which bounds its
// fields so the encoded payload can never reach the cap.
//
// This drives the constructor rather than tool.Execute because the failing
// input is a long PATH, and the filesystem cannot hold one long enough on
// macOS — a test driven through Execute would assert nothing here and
// something else on Linux. The production caller is covered by the test above.
func TestWriteFile_RefusalDiscriminatorSurvivesTruncation(t *testing.T) {
	const truncationBound = 2000 // maxFailClosedOutputChars / maxLiveErrorChars

	longPath := "/workspace/" + strings.Repeat("nested-directory/", 200) + "f.txt"
	require.Greater(t, len(longPath), truncationBound,
		"test premise: the raw path must exceed the bound, or nothing is being clamped")

	refusal := FileExistsRefusalResult("write_file", longPath,
		"file: "+longPath+" already exists. Set overwrite=true to replace.")

	// The whole payload must fit, so the downstream cut never fires at all.
	require.LessOrEqual(t, len([]rune(refusal.ForLLM)), truncationBound,
		"the encoded payload exceeds the 2000-rune cap applied downstream; once truncated it no "+
			"longer parses, and a reload shows a JSON fragment instead of a sentence")

	// And it must still be valid, classifiable JSON after all that clamping.
	parsed := decodeRefusal(t, refusal.ForLLM)
	assert.Equal(t, FileExistsRefusalCode, parsed["error"])
	assert.NotEmpty(t, parsed["path"], "path is minLength:1 in the contract")
	assert.NotEmpty(t, parsed["reason"], "reason is minLength:1 in the contract")

	// The basename survives: clamping cuts from the FRONT, because the leading
	// directories are the repetitive part and the filename is the part a
	// reader needs.
	assert.Contains(t, parsed["path"], "f.txt",
		"clamping must keep the tail of the path — the basename is the informative part")

	assert.True(t, strings.HasPrefix(refusal.ForLLM, `{"error":"`+FileExistsRefusalCode+`"`),
		"the discriminator must still be the first field")
}

// TestFileExistsRefusalResult_DefendsEveryRequiredField covers the other three
// contract minLength:1 fields, not just reason. A schema-invalid payload is
// dropped at the SPA edge, which leaves the caller with nothing — worse than
// the prose the tag replaced.
func TestFileExistsRefusalResult_DefendsEveryRequiredField(t *testing.T) {
	parsed := decodeRefusal(t, FileExistsRefusalResult("", "", "").ForLLM)
	for _, field := range []string{"error", "reason", "tool", "path"} {
		assert.NotEmpty(t, parsed[field], "%s must never be empty — the contract requires minLength 1", field)
	}
}

// TestFileExistsRefusalResult_EmptyReasonStillClassifiable defends the
// contract's minLength:1 on reason. A payload with an empty reason is
// schema-invalid and the SPA drops it, which would leave the caller with
// nothing at all — strictly worse than the prose it replaced.
func TestFileExistsRefusalResult_EmptyReasonStillClassifiable(t *testing.T) {
	res := FileExistsRefusalResult("write_file", "/tmp/a.txt", "")
	require.True(t, res.IsError)
	parsed := decodeRefusal(t, res.ForLLM)
	assert.Equal(t, FileExistsRefusalCode, parsed["error"])
	assert.NotEmpty(t, parsed["reason"], "reason must never be empty — the contract requires minLength 1")
}
