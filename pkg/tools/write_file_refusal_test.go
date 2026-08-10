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

// TestWriteFile_RefusalDiscriminatorSurvivesTruncation is TDD #9 (A1-5's
// prefix-positioning rule).
//
// Both the persisted transcript and the live error frame cap this string at
// 2000 runes (maxFailClosedOutputChars in pkg/agent, maxLiveErrorChars in
// pkg/gateway). A payload long enough to blow that budget must not be able to
// push the discriminator past the cut — a severed payload is unparseable, so
// FR-007 would fail silently on exactly the deeply-nested paths a delegated
// worker in a shared workspace is most likely to produce.
//
// This drives the constructor rather than tool.Execute deliberately, and the
// reason is a real constraint rather than convenience: the operating system
// caps a full path well below the truncation bound (1024 bytes on macOS), so
// no filesystem state can produce a 2000-rune payload here at all. Driving it
// through Execute would silently assert nothing on this platform and something
// else on Linux. The property under test belongs to the payload's field order,
// not to the filesystem — and the test above already proves the production
// caller emits this exact payload.
//
// The guarantee comes from field order: encoding/json emits struct fields in
// declaration order and Error is declared first, so the discriminator sits
// within the first ~30 bytes no matter how long the path is.
func TestWriteFile_RefusalDiscriminatorSurvivesTruncation(t *testing.T) {
	const truncationBound = 2000 // maxFailClosedOutputChars / maxLiveErrorChars

	longPath := "/workspace/" + strings.Repeat("nested-directory/", 200) + "f.txt"
	refusal := FileExistsRefusalResult("write_file", longPath,
		"file: "+longPath+" already exists. Set overwrite=true to replace.")

	require.Greater(t, len([]rune(refusal.ForLLM)), truncationBound,
		"test premise: the payload must actually exceed the truncation bound")

	truncated := string([]rune(refusal.ForLLM)[:truncationBound])
	assert.Contains(t, truncated, FileExistsRefusalCode,
		"the discriminator was cut off by truncation — it must be prefix-positioned so no path "+
			"length can push it past the cap")
	assert.True(t, strings.HasPrefix(refusal.ForLLM, `{"error":"`+FileExistsRefusalCode+`"`),
		"the discriminator must be the FIRST field; anything else makes survival a matter of luck")
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
