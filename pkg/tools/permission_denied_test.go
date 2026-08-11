package tools

// issue #618 — PermissionDeniedPayload / ToolAssemblyDuplicatePayload.
//
// Both producers used to be built with fmt.Sprintf's %q verb: Go-string
// quoting, not JSON quoting. That is empirically broken for two input
// classes reachable in production — invalid UTF-8 (a *os.PathError's
// Error() concatenates the raw OS path byte-for-byte with no validation)
// and any C0/C1 control byte outside \n\t\r (%q emits \xNN, which is not a
// legal JSON escape; json.Unmarshal then fails with "invalid character 'x'
// in string escape code"). Neither producer had a length budget at all
// before this fix, so an ordinary long path silently produced a payload the
// downstream 2000-rune truncation would sever into invalid JSON.
//
// This table drives every adversarial input class the fix is supposed to
// survive through both producers and asserts (1) json.Unmarshal succeeds
// and (2) the encoded payload stays within maxRefusalPayloadRunes.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// adversarialInputs is the shared table of hostile strings both producers
// must survive. Named so each subtest's name documents exactly what class
// of input it exercises.
var adversarialInputs = []struct {
	name string
	s    string
}{
	{"invalid_utf8_lone_0xff", "path/\xff/file.txt"},
	{"invalid_utf8_truncated_sequence", "path/\xe2\x28/file.txt"}, // truncated 3-byte sequence
	{"control_nul", "path/\x00/file.txt"},
	{"control_0x01", "path/\x01/file.txt"},
	{"control_0x7f_del", "path/\x7f/file.txt"},
	{"embedded_double_quote", `path/"quoted"/file.txt`},
	{"embedded_backslash", `path\to\file.txt`},
	{"embedded_newline", "path/\nfile.txt"},
	{"json_injection_attempt", `x","admin":true,"y":"z`},
	{"non_ascii", "路径/文件/パス/файл.txt"},
	{"over_long_830_chars", "/workspace/" + strings.Repeat("nested-directory/", 49) + "f.txt"},   // > 830 chars
	{"over_long_4000_chars", "/workspace/" + strings.Repeat("nested-directory/", 240) + "f.txt"}, // > 4000 chars
}

func TestPermissionDeniedPayload_SurvivesAdversarialInputs(t *testing.T) {
	require.GreaterOrEqual(t, len(adversarialInputs[len(adversarialInputs)-2].s), 830,
		"test premise: the 830-char case must actually exceed 830 chars")
	require.GreaterOrEqual(t, len(adversarialInputs[len(adversarialInputs)-1].s), 4000,
		"test premise: the 4000-char case must actually exceed 4000 chars")

	for _, tc := range adversarialInputs {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := PermissionDeniedPayload("write_file", "Access to this path is denied by filesystem policy.", tc.s, true)
			require.NoError(t, err, "PermissionDeniedPayload must not error for adversarial input %q", tc.name)

			var parsed map[string]any
			uerr := json.Unmarshal(encoded, &parsed)
			require.NoError(t, uerr,
				"payload must remain valid JSON for adversarial input class %q; got: %s", tc.name, encoded)

			assert.Equal(t, PermissionDeniedCode, parsed["error"])
			assert.NotEmpty(t, parsed["reason"], "reason must stay non-empty (contract minLength:1)")
			assert.Equal(t, true, parsed["permanent"])

			runeLen := len([]rune(string(encoded)))
			assert.LessOrEqual(t, runeLen, maxRefusalPayloadRunes,
				"encoded payload is %d runes for adversarial input %q — exceeds the %d-rune budget",
				runeLen, tc.name, maxRefusalPayloadRunes)
		})
	}
}

func TestPermissionDeniedPayload_PermanentFalseSurvivesRoundTrip(t *testing.T) {
	// "saturated" is the one approval-flow reason classified Permanent:false
	// (ADR-058) — must round-trip as a real JSON boolean, not stringified.
	encoded, err := PermissionDeniedPayload("bash",
		"Too many approval requests were already pending. This one may be retried later.",
		"saturated", false)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(encoded, &parsed))
	assert.Equal(t, false, parsed["permanent"])
}

func TestPermissionDeniedPayload_DefendsEveryRequiredField(t *testing.T) {
	encoded, err := PermissionDeniedPayload("", "", "", true)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(encoded, &parsed))
	for _, field := range []string{"error", "message", "tool", "reason"} {
		assert.NotEmpty(t, parsed[field], "%s must never be empty — the contract requires minLength 1", field)
	}
}

func TestToolAssemblyDuplicatePayload_SurvivesAdversarialInputs(t *testing.T) {
	for _, tc := range adversarialInputs {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := ToolAssemblyDuplicatePayload("duplicate tool assembly: " + tc.s)
			require.NoError(t, err, "ToolAssemblyDuplicatePayload must not error for adversarial input %q", tc.name)

			var parsed map[string]any
			uerr := json.Unmarshal(encoded, &parsed)
			require.NoError(t, uerr,
				"payload must remain valid JSON for adversarial input class %q; got: %s", tc.name, encoded)

			assert.Equal(t, ToolAssemblyDuplicateCode, parsed["error"])
			assert.NotEmpty(t, parsed["message"], "message must stay non-empty (contract minLength:1)")

			runeLen := len([]rune(string(encoded)))
			assert.LessOrEqual(t, runeLen, maxRefusalPayloadRunes,
				"encoded payload is %d runes for adversarial input %q — exceeds the %d-rune budget",
				runeLen, tc.name, maxRefusalPayloadRunes)
		})
	}
}

func TestToolAssemblyDuplicatePayload_DefendsRequiredField(t *testing.T) {
	encoded, err := ToolAssemblyDuplicatePayload("")
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(encoded, &parsed))
	assert.NotEmpty(t, parsed["message"], "message must never be empty — the contract requires minLength 1")
	assert.Equal(t, ToolAssemblyDuplicateCode, parsed["error"])
}

// TestPermissionDeniedResult_UsesSharedProducer covers fserrors.go's
// PermissionDeniedResult end to end, mirroring
// TestWriteFile_RefusalDiscriminatorSurvivesTruncation's long-path drive:
// the filesystem cannot hold a path long enough to reproduce this on disk,
// so this drives the constructor directly with a long classErr message.
func TestPermissionDeniedResult_UsesSharedProducer(t *testing.T) {
	longPath := "/workspace/" + strings.Repeat("nested-directory/", 200) + "f.txt"
	require.Greater(t, len(longPath), 2000, "test premise: the raw path must exceed the downstream truncation bound")

	classErr := ErrOutsideScope
	res := PermissionDeniedResult("write_file", classErr, "access denied: path is outside the effective filesystem scope: "+longPath)
	require.True(t, res.IsError)

	runeLen := len([]rune(res.ForLLM))
	assert.LessOrEqual(t, runeLen, 2000,
		"the encoded payload exceeds the 2000-rune cap applied downstream (pkg/agent.maxFailClosedOutputChars "+
			"/ pkg/gateway.maxLiveErrorChars); once truncated it no longer parses")

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &parsed), "result must still be valid JSON after clamping")
	assert.Equal(t, PermissionDeniedCode, parsed["error"])
	assert.Equal(t, "write_file", parsed["tool"])
	assert.Equal(t, true, parsed["permanent"])
	assert.NotEmpty(t, parsed["reason"])
}

// TestPermissionDeniedResult_ControlCharacterPathIsValidJSON is the direct
// regression for the %q defect: before this fix, a path containing a raw
// control byte produced a payload that failed json.Unmarshal downstream.
func TestPermissionDeniedResult_ControlCharacterPathIsValidJSON(t *testing.T) {
	res := PermissionDeniedResult("edit_file", ErrOutsideScope, "access denied: path is outside the effective filesystem scope: /w/\x01bad\x7f.txt")
	require.True(t, res.IsError)

	var parsed map[string]any
	err := json.Unmarshal([]byte(res.ForLLM), &parsed)
	require.NoError(t, err, "a control-character path must still produce parseable JSON; got: %s", res.ForLLM)
	assert.Equal(t, PermissionDeniedCode, parsed["error"])
}
