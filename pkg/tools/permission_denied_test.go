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

	"github.com/elicify-ai/omnipus/pkg/api/generated"
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
	// This case cannot actually exercise the budget: even with ALL bounding
	// (clamp + shrink) removed it encodes to well under 2000 runes (~991) —
	// it exercises the %q/escaping regression at a size an ordinary
	// deeply-nested workspace path (a fifth of Linux's 4096-byte PATH_MAX)
	// would realistically hit, not budget enforcement. See
	// downstreamCapLiteralIsExercised below for the case that actually
	// forces the shrink loop to run.
	{"over_long_830_chars_escaping_not_budget", "/workspace/" + strings.Repeat("nested-directory/", 49) + "f.txt"},   // > 830 chars
	{"over_long_4000_chars", "/workspace/" + strings.Repeat("nested-directory/", 240) + "f.txt"}, // > 4000 chars
}

// downstreamCapRunes mirrors the 2000-rune hard cap applied downstream —
// pkg/agent.maxFailClosedOutputChars on the persisted transcript and
// pkg/gateway.maxLiveErrorChars on the live frame — as a LITERAL, not as
// maxRefusalPayloadRunes (the production constant this package's own budget
// logic is built around). Asserting against maxRefusalPayloadRunes would be
// self-referential: bump that constant and the assertion below moves with
// it and never catches the regression it exists to catch. pkg/tools cannot
// import pkg/agent or pkg/gateway (import direction), so the literal is
// pinned here with a comment instead of a shared symbol.
const downstreamCapRunes = 2000

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
			assert.LessOrEqual(t, runeLen, downstreamCapRunes,
				"encoded payload is %d runes for adversarial input %q — exceeds the %d-rune downstream "+
					"truncation cap (pkg/agent.maxFailClosedOutputChars / pkg/gateway.maxLiveErrorChars)",
				runeLen, tc.name, downstreamCapRunes)
		})
	}
}

// TestPermissionDeniedPayload_ShrinkLoopHandlesEscapeHeavyOverflow is the
// case adversarialInputs cannot construct: exactly maxRefusalReasonRunes
// (900) characters, ALL of them JSON-HTML-escape-expanding ('<', which
// encoding/json expands to the 6-rune <). The pre-shrink-loop, clamped-
// only encoded size for reason ALONE is ~5400 runes — nearly 3x the whole
// 1900-rune payload budget — so this input cannot pass without the shrink
// loop genuinely running past the input-level clamp; a test built only from
// moderate-length adversarial strings (like the table above) cannot tell a
// working shrink loop from a deleted one, because the input clamp alone
// already gets those under budget.
func TestPermissionDeniedPayload_ShrinkLoopHandlesEscapeHeavyOverflow(t *testing.T) {
	reason := strings.Repeat("<", maxRefusalReasonRunes)
	encoded, err := PermissionDeniedPayload("write_file", "Access to this path is denied by filesystem policy.", reason, true)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(encoded, &parsed), "payload must remain valid JSON: %s", encoded)
	assert.Equal(t, PermissionDeniedCode, parsed["error"])
	assert.NotEmpty(t, parsed["reason"])

	runeLen := len([]rune(string(encoded)))
	assert.LessOrEqualf(t, runeLen, downstreamCapRunes,
		"encoded payload is %d runes — exceeds the %d-rune downstream cap; the shrink loop did not "+
			"actually run past the input-level clamp", runeLen, downstreamCapRunes)
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
			assert.LessOrEqual(t, runeLen, downstreamCapRunes,
				"encoded payload is %d runes for adversarial input %q — exceeds the %d-rune downstream cap",
				runeLen, tc.name, downstreamCapRunes)
		})
	}
}

// TestPermissionDeniedPayload_SurvivesAdversarialToolName drives every
// adversarialInputs entry through the `tool` parameter instead of `reason`
// (H3): tool is MCP-declared and not statically enumerable, so it is just
// as attacker/environment-influenced as reason, but before this test no
// case exercised it. clampRefusalField(tool, maxRefusalToolRunes) at the
// PermissionDeniedPayload call site is tool's ONLY input-level bound, and
// marshalWithinBudget's shrink loop is its second, independent bound (added
// alongside this test) — this covers both, and the table already contains
// a >=4000-char entry and several control-byte entries, satisfying the
// ">=4000 chars, plus control bytes" adversarial requirement.
func TestPermissionDeniedPayload_SurvivesAdversarialToolName(t *testing.T) {
	for _, tc := range adversarialInputs {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := PermissionDeniedPayload(tc.s, "Access to this action is denied.", "policy_denied", true)
			require.NoError(t, err, "PermissionDeniedPayload must not error for adversarial tool name %q", tc.name)

			var parsed map[string]any
			uerr := json.Unmarshal(encoded, &parsed)
			require.NoError(t, uerr,
				"payload must remain valid JSON for adversarial tool name class %q; got: %s", tc.name, encoded)

			assert.Equal(t, PermissionDeniedCode, parsed["error"])
			assert.NotEmpty(t, parsed["tool"], "tool must stay non-empty (contract minLength:1)")

			runeLen := len([]rune(string(encoded)))
			assert.LessOrEqual(t, runeLen, downstreamCapRunes,
				"encoded payload is %d runes for adversarial tool name %q — exceeds the %d-rune downstream cap",
				runeLen, tc.name, downstreamCapRunes)
		})
	}
}

// TestPermissionDeniedPayload_JSONInjectionCannotForgeFields is M2's
// stronger check on the "json_injection_attempt" adversarial row
// (`x","admin":true,"y":"z`). Discriminator equality, non-empty reason, and
// a size bound (the checks TestPermissionDeniedPayload_SurvivesAdversarialInputs
// already runs) all pass even for a HYPOTHETICAL concatenating producer that
// naively spliced this string into `..."reason":"` + s + `","tool":...` —
// that would parse, carry the right discriminator, a non-empty reason, and
// stay small. What it would NOT do is round-trip reason back to the exact
// input string, or keep the key set to exactly the five contract fields
// (a naive splice adds "admin":true as a SIXTH key). Both are asserted here.
func TestPermissionDeniedPayload_JSONInjectionCannotForgeFields(t *testing.T) {
	const injection = `x","admin":true,"y":"z`
	encoded, err := PermissionDeniedPayload("write_file", "Access to this path is denied by filesystem policy.", injection, true)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(encoded, &parsed))

	// Exact key set: a successful injection would add "admin" (and/or "y")
	// as extra top-level keys.
	wantKeys := []string{"error", "message", "tool", "reason", "permanent"}
	gotKeys := make([]string, 0, len(parsed))
	for k := range parsed {
		gotKeys = append(gotKeys, k)
	}
	assert.ElementsMatch(t, wantKeys, gotKeys,
		"payload must have EXACTLY the five contract fields — an extra key means the injection attempt "+
			"escaped the reason string and forged a sibling field")

	// reason must round-trip to the exact input — proper JSON-encoding the
	// value is what keeps the embedded quotes/colons INSIDE the string
	// rather than becoming structure.
	assert.Equal(t, injection, parsed["reason"],
		"reason must round-trip byte-for-byte; a value that decodes to something else means the quotes "+
			"in the input were interpreted as JSON structure rather than escaped string content")

	// Belt and suspenders: unmarshal into the generated type with unknown
	// fields disallowed — any forged sibling field fails this decode too.
	dec := json.NewDecoder(strings.NewReader(string(encoded)))
	dec.DisallowUnknownFields()
	var typed generated.PermissionDenied
	require.NoError(t, dec.Decode(&typed),
		"payload must decode into generated.PermissionDenied with no unknown fields")
	assert.Equal(t, injection, typed.Reason)
}

// TestPermissionDeniedPayload_ShrinkExhaustionStaysSchemaValid is the
// exhaustion path: message, tool, AND reason are all simultaneously large
// enough that the shrink loop runs every shrinkable field down toward its
// 1-rune floor. The concern is whether the EMITTED payload is still valid
// JSON and still schema-valid at the end of that — in particular whether
// any of the three minLength:1 string fields ever goes empty. It must not:
// clampRefusalField's floor is 1 rune, never 0, so exhaustion degrades
// fields to near-uselessness but never to schema-invalidity.
func TestPermissionDeniedPayload_ShrinkExhaustionStaysSchemaValid(t *testing.T) {
	huge := strings.Repeat(`<"&\`, 5000) // 20,000 runes, heavy JSON/HTML escaping
	encoded, err := PermissionDeniedPayload(huge, huge, huge, true)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(encoded, &parsed), "payload must remain valid JSON under exhaustion: %s", encoded)

	assert.Equal(t, PermissionDeniedCode, parsed["error"])
	for _, field := range []string{"message", "tool", "reason"} {
		s, ok := parsed[field].(string)
		require.True(t, ok, "%s must still be a JSON string", field)
		assert.NotEmpty(t, s, "%s must never go empty under shrink exhaustion — the contract requires minLength 1", field)
	}
	assert.Equal(t, true, parsed["permanent"])

	runeLen := len([]rune(string(encoded)))
	assert.LessOrEqualf(t, runeLen, downstreamCapRunes,
		"encoded payload is %d runes under exhaustion — exceeds the %d-rune downstream cap even after "+
			"the shrink loop ran every field to its floor", runeLen, downstreamCapRunes)
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
