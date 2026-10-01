package generated

// redirectframe_contract_test.go — RED wave (qa-lead, test/a-redirect-transport-red):
// contract pins for the landed generated RedirectFrame (ADR-20260928 D9 / D2
// corrected transport).
//
// SPEC SOURCES (expected values derive from these, never from the
// implementation — oracle independence):
//   - contracts/components/schemas/RedirectFrame.yaml: {type: const "redirect",
//     session_id: string 1..128 required, instruction: string minLength 1 /
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
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
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

func TestRedirectFrameSchema_SessionIdBoundaries(t *testing.T) {
	t.Run("128 chars accepted", func(t *testing.T) {
		f := FixtureRedirectFrame_Populated()
		f.SessionId = strings.Repeat("s", 128)
		mustPassAsyncAPI(t, "RedirectFrame", f)
	})
	t.Run("129 chars rejected", func(t *testing.T) {
		f := FixtureRedirectFrame_Populated()
		f.SessionId = strings.Repeat("s", 129)
		mustFailAsyncAPI(t, "RedirectFrame", f, "session_id maxLength 128")
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
// characterization test. A non-breaking-space-only instruction PASSES the
// JSON Schema layer because RE2's `\S` is ASCII-only, while JavaScript's `\s`
// includes U+00A0, so the generated Zod schema REJECTS the same string (pinned
// in src/test/redirectframe-contract.test.ts). The schema layer therefore does
// NOT enforce Unicode-aware nonblank; the server redirect handler must (the
// contract's own "client-side approximation only" wording).
func TestRedirectFrameSchema_NBSPInstruction_PassesSchema_Characterization(t *testing.T) {
	f := FixtureRedirectFrame_Populated()
	f.Instruction = strings.Repeat("\u00a0", 4) // NBSP-only (U+00A0, escaped)
	mustPassAsyncAPI(t, "RedirectFrame", f)
}

// TestRedirectFrameSchema_AstralInstruction_PassesSchema_Characterization —
// characterization test. 8193 astral emoji are 8193 Unicode code points
// (within maxLength 16384 counted as code points) but 16 386 UTF-16 code
// units (over the generated Zod `.max(16384)`, which counts UTF-16 units) and
// 16 386 UTF-8 bytes (over the runtime byte ceiling). The schema passing it
// documents that JSON Schema maxLength counts code points — NOT UTF-16 units
// and NOT bytes. The RedirectFrame.yaml prose "maxLength counts characters
// (UTF-16 code units)" describes Zod/JS semantics, not JSON Schema semantics;
// the wording divergence is reported to the dispatcher (prose is not edited by
// QA).
func TestRedirectFrameSchema_AstralInstruction_PassesSchema_Characterization(t *testing.T) {
	f := FixtureRedirectFrame_Populated()
	f.Instruction = strings.Repeat("\u00e9", 8193) // U+00E9 = 2 UTF-8 bytes
	if got := utf8.RuneCountInString(f.Instruction); got != 8193 {
		t.Fatalf("fixture self-check: code points = %d, want 8193", got)
	}
	if got := len(f.Instruction); got != 16386 {
		t.Fatalf("fixture self-check: UTF-8 bytes = %d, want 16386 (also the UTF-16 unit count: 2 per astral char)", got)
	}
	mustPassAsyncAPI(t, "RedirectFrame", f)
}

// TestRedirectFrameSchema_ByteCeilingNotEnforcedBySchema_Characterization —
// characterization test. An instruction of 16 386 UTF-8 bytes (8193 two-byte
// characters) PASSES the schema although it exceeds the contract's 16 384-byte
// runtime ceiling — direct proof that "schema maxLength is not byte
// enforcement" (the schema's own prose). The server redirect handler MUST
// enforce the UTF-8 byte ceiling at runtime; no such handler exists yet
// (Backend-CMD scope, recorded BLOCKED).
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
