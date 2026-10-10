package generated

// redirectframe_contract_test.go — RED wave (qa-lead, test/a-redirect-transport-red):
// contract pins for the landed generated RedirectFrame (ADR-20260928 D9 / D2
// corrected transport).
//
// SPEC SOURCES (expected values derive from these, never from the
// implementation — oracle independence):
//   - contracts/components/schemas/RedirectFrame.yaml: {type: const "redirect",
//     session_id: string 1..255 required, instruction: string minLength 1 /
//     maxLength 16384 / pattern \S}, additionalProperties: false, NO scope
//     field (D9 row 2 fixes scope: that helper only — a scope enum would
//     re-open a decided behavior).
//   - The schema's own prose + the dispatch brief: the 16 384 cap is a UTF-8
//     BYTE ceiling enforced at RUNTIME by the server redirect handler; JSON
//     Schema maxLength is character-based and does NOT enforce it. The three
//     CHARACTERIZATION tests below pin that gap at the schema layer (they pass
//     today on purpose; they are evidence, not verification).
//   - Architect seam ruling §3.1 (a-control-architect-recovery-20261002):
//     transport = client interception mirroring CancelFrame; no busy sentinel.
//
// GREEN PINS vs CHARACTERIZATION: every test here is a GREEN pin (the schema
// and generated types already exist) except the three marked
// "characterization test" — those pin current divergent behaviour as evidence
// for the report; they verify nothing about runtime enforcement, which is
// Backend-CMD scope and recorded as a BLOCKED/missing-seam scenario.
//
// FILE PLACEMENT: new file on purpose — contract_test.go and fixtures.go are
// grandfathered over the 3 000-line budget and may only shrink; fixtures are
// local to this file for the same reason.

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// ─── Fixtures (local — fixtures.go is grandfathered and may only shrink) ────

func FixtureRedirectFrame_Populated() RedirectFrame {
	return RedirectFrame{
		Type:        "redirect",
		SessionId:   "helper-session-9",
		Instruction: "focus on the failing tests",
	}
}

func FixtureRedirectFrame_ZeroValue() RedirectFrame {
	return RedirectFrame{}
}

// redirectFrameMapWithScope builds a raw map fixture carrying a forbidden
// "scope" key — the schema's additionalProperties:false must reject it (the
// typed struct cannot express the extra key, so this one fixture is a map).
func redirectFrameMapWithScope() map[string]any {
	return map[string]any{
		"type":        "redirect",
		"session_id":  "helper-session-9",
		"instruction": "focus on the failing tests",
		"scope":       "helper",
	}
}

// ─── Generated Go type shape ─────────────────────────────────────────────────

// TestRedirectFrameGoShape_MarshalsExactWireShape: the generated struct
// marshals to exactly the three contract keys — no scope field may ever ride
// the wire (D9 row 2; §3.1 "No scope field").
func TestRedirectFrameGoShape_MarshalsExactWireShape(t *testing.T) {
	raw, err := json.Marshal(FixtureRedirectFrame_Populated())
	if err != nil {
		t.Fatalf("marshal RedirectFrame: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal marshaled frame: %v", err)
	}
	if len(wire) != 3 {
		t.Fatalf("wire keys = %v, want exactly [instruction session_id type]", wire)
	}
	for _, k := range []string{"type", "session_id", "instruction"} {
		if _, ok := wire[k]; !ok {
			t.Errorf("wire shape missing required key %q (got %v)", k, wire)
		}
	}
	if wire["type"] != "redirect" {
		t.Errorf("wire type = %v, want \"redirect\"", wire["type"])
	}
	if _, ok := wire["scope"]; ok {
		t.Error("wire shape carries a \"scope\" key — D9 row 2 fixes scope (that helper only); no scope property may exist")
	}
}

// TestRedirectFrameGoShape_FieldSetMatchesSchema: the generated struct has
// exactly the three schema fields with the snake_case json tags, and the
// WsFrame type constant carries the literal.
func TestRedirectFrameGoShape_FieldSetMatchesSchema(t *testing.T) {
	typ := reflect.TypeOf(RedirectFrame{})
	if typ.NumField() != 3 {
		t.Fatalf("RedirectFrame has %d fields, want exactly 3 (type, session_id, instruction) — additionalProperties:false", typ.NumField())
	}
	want := map[string]string{
		"Instruction": "instruction",
		"SessionId":   "session_id",
		"Type":        "type",
	}
	for fieldName, tag := range want {
		f, ok := typ.FieldByName(fieldName)
		if !ok {
			t.Errorf("RedirectFrame missing field %s (want json %q)", fieldName, tag)
			continue
		}
		if f.Type.Kind() != reflect.String {
			t.Errorf("RedirectFrame.%s is %v, want string", fieldName, f.Type)
		}
		if got := f.Tag.Get("json"); got != tag {
			t.Errorf("RedirectFrame.%s json tag = %q, want %q", fieldName, got, tag)
		}
	}
	if string(WsFrameTypeRedirect) != "redirect" {
		t.Errorf("WsFrameTypeRedirect = %q, want \"redirect\"", string(WsFrameTypeRedirect))
	}
}

// ─── Schema validation: positives and boundaries (GREEN pins) ────────────────

func TestRedirectFrameSchema_PopulatedValid(t *testing.T) {
	mustPassAsyncAPI(t, "RedirectFrame", FixtureRedirectFrame_Populated())
}

func TestRedirectFrameSchema_MultibyteInstructionValid(t *testing.T) {
	f := FixtureRedirectFrame_Populated()
	f.Instruction = "先 export the CSV — 报告"
	mustPassAsyncAPI(t, "RedirectFrame", f)
}

func TestRedirectFrameSchema_ZeroValueRejected(t *testing.T) {
	mustFailAsyncAPI(t, "RedirectFrame", FixtureRedirectFrame_ZeroValue(),
		"type const violation plus minLength 1 on session_id and instruction")
}

func TestRedirectFrameSchema_WrongTypeLiteralRejected(t *testing.T) {
	f := FixtureRedirectFrame_Populated()
	f.Type = "cancel"
	mustFailAsyncAPI(t, "RedirectFrame", f, "type must be the const \"redirect\"")
	f2 := FixtureRedirectFrame_Populated()
	f2.Type = "redirect_x"
	mustFailAsyncAPI(t, "RedirectFrame", f2, "type must be the const \"redirect\" (no variants)")
}

func TestRedirectFrameSchema_ScopePropertyRejected(t *testing.T) {
	mustFailAsyncAPI(t, "RedirectFrame", redirectFrameMapWithScope(),
		"additionalProperties:false — D9 row 2 fixes scope; a scope field would re-open a decided behavior")
}

// redirectFrameSchemaStringBound reads an integer bound (e.g. "maxLength") for a
// string property straight from the RedirectFrame contract schema, so the
// boundary tests take their oracle from the CONTRACT rather than a hardcoded
// literal that silently goes stale when the schema widens (item 11: session_id
// maxLength was widened 128 -> 255 and the old literal 128/129 test stayed).
func redirectFrameSchemaStringBound(t *testing.T, prop, bound string) int {
	t.Helper()
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(componentSchemaYAML(t, "RedirectFrame"), &doc),
		"RedirectFrame schema must parse as YAML")
	props, ok := doc["properties"].(map[string]any)
	require.True(t, ok, "RedirectFrame schema must declare properties")
	p, ok := props[prop].(map[string]any)
	require.True(t, ok, "RedirectFrame schema must declare property %q", prop)
	v, ok := p[bound]
	require.True(t, ok, "RedirectFrame property %q must declare %q", prop, bound)
	n, ok := v.(int)
	require.True(t, ok, "RedirectFrame property %q %q must be an integer, got %T", prop, bound, v)
	return n
}

func TestRedirectFrameSchema_SessionIdBoundaries(t *testing.T) {
	// Derived from the contract schema, never a copied literal: 255 today.
	max := redirectFrameSchemaStringBound(t, "session_id", "maxLength")
	t.Run(fmt.Sprintf("%d chars accepted", max), func(t *testing.T) {
		f := FixtureRedirectFrame_Populated()
		f.SessionId = strings.Repeat("s", max)
		mustPassAsyncAPI(t, "RedirectFrame", f)
	})
	t.Run(fmt.Sprintf("%d chars rejected", max+1), func(t *testing.T) {
		f := FixtureRedirectFrame_Populated()
		f.SessionId = strings.Repeat("s", max+1)
		mustFailAsyncAPI(t, "RedirectFrame", f, fmt.Sprintf("session_id maxLength %d", max))
	})
	t.Run("empty rejected", func(t *testing.T) {
		f := FixtureRedirectFrame_Populated()
		f.SessionId = ""
		mustFailAsyncAPI(t, "RedirectFrame", f, "session_id minLength 1")
	})
}

func TestRedirectFrameSchema_InstructionLengthBoundaries(t *testing.T) {
	t.Run("16384 chars accepted", func(t *testing.T) {
		f := FixtureRedirectFrame_Populated()
		f.Instruction = strings.Repeat("a", 16384)
		mustPassAsyncAPI(t, "RedirectFrame", f)
	})
	t.Run("16385 chars rejected", func(t *testing.T) {
		f := FixtureRedirectFrame_Populated()
		f.Instruction = strings.Repeat("a", 16385)
		mustFailAsyncAPI(t, "RedirectFrame", f, "instruction maxLength 16384")
	})
	t.Run("empty rejected", func(t *testing.T) {
		f := FixtureRedirectFrame_Populated()
		f.Instruction = ""
		mustFailAsyncAPI(t, "RedirectFrame", f, "instruction minLength 1")
	})
}

func TestRedirectFrameSchema_InstructionASCIIWhitespaceOnlyRejected(t *testing.T) {
	f := FixtureRedirectFrame_Populated()
	f.Instruction = "\t \n  "
	mustFailAsyncAPI(t, "RedirectFrame", f, "pattern \\S — whitespace-only instruction carries no instruction")
}

// ─── Divergence evidence (characterization tests — pass today on purpose) ────
//
// The three tests below pin divergent behaviour at the SCHEMA layer as
// evidence for the report. They are labeled `characterization test` per the
// test-writing skill: they document what the schema does, they do not verify
// correctness. The runtime enforcement obligations they imply (Unicode-aware
// nonblank check, UTF-8 byte ceiling) belong to the server redirect handler
// (Backend-CMD) and are recorded as BLOCKED/missing-seam scenarios.

// TestRedirectFrameSchema_NBSPInstruction_PassesSchema_Characterization —
// characterization test. U+00A0 (NBSP) carries the Unicode White_Space
// property — it IS Unicode whitespace. The layers disagree on REGEX ENGINE
// SEMANTICS, not on the property: Go's RE2 `\s` is the ASCII-only Perl class
// [\t\n\f\r ], so RE2 `\S` MATCHES U+00A0 and the schema's unanchored
// `pattern: '\S'` search succeeds — the JSON Schema layer PASSES an
// NBSP-only instruction (the Go validator v6.0.2 applies patterns with RE2
// MatchString — validator.go). JavaScript's `\s` is Unicode-aware (it draws
// on the White_Space set and includes U+00A0), so JS `\S` does not match it
// and the generated Zod `.regex(/\S/)` REJECTS the same string (pinned in
// src/test/redirectframe-contract.test.ts). Outcomes on this input: Go
// schema layer PASSES, Zod REFUSES, and the server redirect handler's
// authoritative Unicode-aware trim must REFUSE (the contract's own
// "client-side approximation only" wording) — two refuse, one passes; saying
// only "nonblank in Unicode terms" without naming each engine's character
// classes is precisely the ambiguity this pair pins.
func TestRedirectFrameSchema_NBSPInstruction_PassesSchema_Characterization(t *testing.T) {
	f := FixtureRedirectFrame_Populated()
	f.Instruction = strings.Repeat("\u00a0", 4) // NBSP-only (U+00A0, escaped)
	mustPassAsyncAPI(t, "RedirectFrame", f)
}

// TestRedirectFrameSchema_AstralInstruction_PassesSchema_Characterization —
// characterization test. 8193 copies of U+1F600 (GRINNING FACE — a
// SUPPLEMENTARY-plane code point, > U+FFFF) are, derived from the code
// point's own Unicode properties (not from the code under test):
//   - 8193 Unicode code points — WITHIN maxLength 16384 as JSON Schema counts
//     it (code points; RFC 8259 "characters"; the Go validator v6.0.2
//     measures utf8.RuneCount — validator.go::strValidate),
//   - 16386 UTF-16 code units — U+1F600 needs a 2-unit surrogate pair, so
//     8193 × 2 EXCEEDS the generated Zod `.max(16384)`, which counts UTF-16
//     units (String.prototype.length semantics): the Zod layer REJECTS the
//     same string (pinned in src/test/redirectframe-contract.test.ts),
//   - 32772 UTF-8 bytes — U+1F600 encodes in the 4-byte form (F0 9F 98 80),
//     so 8193 × 4 EXCEEDS the runtime 16384-byte ceiling: the future server
//     redirect handler must REFUSE it.
//
// The schema PASSING this instruction is therefore the evidence that JSON
// Schema maxLength counts code points — NOT UTF-16 units (16386 would fail)
// and NOT bytes (32772 would fail). On this input the three layers split
// one-pass / two-refuse: the schema layer passes, Zod refuses on units, the
// runtime (not yet implemented — Backend-CMD) must refuse on bytes; the two
// refusals bind at DIFFERENT inputs (the Zod unit ceiling first bites at 8193
// astral chars, the byte ceiling already at 4097 — 4097 × 4 = 16388 > 16384,
// while 4096 × 4 = 16384 is exactly at the boundary). RedirectFrame.yaml's
// prose "maxLength counts characters (UTF-16 code units)" describes the
// Zod/JS side, not JSON Schema semantics; the wording divergence is reported
// to the dispatcher (prose is not edited by QA).
//
// CORRECTION (2026-10-02, a-redirect-red-correction): the first cut of this
// test used strings.Repeat("\u00e9", 8193) — U+00E9 is a BMP code point (1
// UTF-16 unit, 2 UTF-8 bytes), so that fixture could not exercise the
// code-point/UTF-16-unit divergence its comment claimed, and its byte
// self-check mislabelled 16386 as "the UTF-16 unit count: 2 per astral
// char". The two-byte BMP case lives (unchanged) in
// TestRedirectFrameSchema_ByteCeilingNotEnforcedBySchema_Characterization
// below; this test now carries the actual supplementary-plane fixture.
func TestRedirectFrameSchema_AstralInstruction_PassesSchema_Characterization(t *testing.T) {
	const astral = '\U0001F600' // GRINNING FACE — supplementary plane (> U+FFFF)
	const copies = 8193         // minimal astral count whose UTF-16 unit count exceeds the Zod .max(16384)
	f := FixtureRedirectFrame_Populated()
	f.Instruction = strings.Repeat(string(astral), copies)

	// Independent length oracles, each derived from the code point's Unicode
	// properties and cross-checked against the constructed string:
	if got := utf8.RuneCountInString(f.Instruction); got != copies {
		t.Fatalf("fixture self-check: code points = %d, want %d (one per Repeat copy)", got, copies)
	}
	wantBytes := copies * utf8.RuneLen(astral) // 8193 × 4 = 32772 (U+1F600 is in the 4-byte UTF-8 range)
	if wantBytes != 32772 {
		t.Fatalf("oracle self-check: derived UTF-8 bytes = %d, want 32772 (8193 × 4) — the astral byte arithmetic is part of the evidence", wantBytes)
	}
	if got := len(f.Instruction); got != wantBytes {
		t.Fatalf("fixture self-check: UTF-8 bytes = %d, want %d (NOT 16386 — 16386 is the U+00E9 case below; an astral char is 4 UTF-8 bytes, not 2)", got, wantBytes)
	}
	wantUTF16Units := copies * utf16.RuneLen(astral) // 8193 × 2 = 16386 (surrogate pair above U+FFFF)
	if wantUTF16Units != 16386 {
		t.Fatalf("oracle self-check: derived UTF-16 units = %d, want 16386 — must exceed the Zod .max(16384) the TS pin asserts, or this test stops being divergence evidence", wantUTF16Units)
	}
	mustPassAsyncAPI(t, "RedirectFrame", f)
}

// TestRedirectFrameSchema_ByteCeilingNotEnforcedBySchema_Characterization —
// characterization test. An instruction of 16 386 UTF-8 bytes (8193 × U+00E9,
// a TWO-BYTE BMP character) PASSES the schema although it exceeds the
// contract's 16 384-byte runtime ceiling — direct proof that "schema maxLength
// is not byte enforcement" (the schema's own prose). U+00E9 is ONE UTF-16
// code unit, so this input is also only 8193 UTF-16 units: the Zod layer
// PASSES it too (8193 ≤ 16384). That is what makes this the clean BYTES-ONLY
// divergence pin — the byte ceiling is the ONLY refusing layer for this input
// (the astral fixture in the test above is where the unit-counting Zod layer
// refuses as well). The server redirect handler MUST enforce the UTF-8 byte
// ceiling at runtime; no such handler exists yet (Backend-CMD scope, recorded
// BLOCKED). Boundaries for that future runtime, per character class:
// 16384 ASCII bytes accept / 16385 refuse; 8192 U+00E9 = 16384 bytes accept /
// 8193 = 16386 refuse; 4096 U+1F600 = 16384 bytes accept / 4097 = 16388
// refuse.
func TestRedirectFrameSchema_ByteCeilingNotEnforcedBySchema_Characterization(t *testing.T) {
	f := FixtureRedirectFrame_Populated()
	f.Instruction = strings.Repeat("é", 8193) // é = 2 UTF-8 bytes
	if got := len(f.Instruction); got != 16386 {
		t.Fatalf("fixture self-check: UTF-8 bytes = %d, want 16386 (16 384 ceiling exceeded)", got)
	}
	if got := utf8.RuneCountInString(f.Instruction); got != 8193 {
		t.Fatalf("fixture self-check: code points = %d, want 8193 (within character maxLength)", got)
	}
	mustPassAsyncAPI(t, "RedirectFrame", f)
}
