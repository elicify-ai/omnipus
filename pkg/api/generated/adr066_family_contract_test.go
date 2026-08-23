package generated

import (
	"strings"
	"testing"
)

// ── ToolArgumentRefusal / ToolResultRecallMark — ADR-066 (spec test 44) ───────
// Traces to:
//   contracts/asyncapi.yaml #/components/schemas/ToolArgumentRefusal
//   contracts/asyncapi.yaml #/components/schemas/ToolResultRecallMark
//
// Both are ADR-060-family members (inline asyncapi schema, `error` const
// discriminator, additionalProperties:false) added by T066-01. The producers
// land in T066-04 (pkg/tools/result.go via marshalWithinBudget); this file
// pins the CONTRACT those producers must satisfy — FR-016 (refusal names
// tool, size and cap), FR-018 (mark carries tool name and id ≤ 64 chars,
// archive line, size in chars, turn number, recall hint), B-19, B-21, B-26.

func TestContract_ToolArgumentRefusal_Populated(t *testing.T) {
	mustPassAsyncAPI(t, "ToolArgumentRefusal", ToolArgumentRefusal{
		Error:     "tool_arguments_too_large",
		Reason:    "write_file: serialised arguments are 64,001 chars; the limit is 64,000. Retry with smaller arguments.",
		Tool:      "write_file",
		SizeChars: 64001,
		CapChars:  64000,
	})
}

func TestContract_ToolArgumentRefusal_ZeroValue(t *testing.T) {
	// The zero value violates the const, both minLength:1 strings and both
	// minimum:1 integers at once — the shape an unchecked producer yields.
	mustFailAsyncAPI(t, "ToolArgumentRefusal", ToolArgumentRefusal{},
		"every field is required; zero value fails const, minLength and minimum")
}

func TestContract_ToolArgumentRefusal_WrongDiscriminatorRejected(t *testing.T) {
	mustFailAsyncAPI(t, "ToolArgumentRefusal", ToolArgumentRefusal{
		Error:     "permission_denied",
		Reason:    "too large",
		Tool:      "write_file",
		SizeChars: 64001,
		CapChars:  64000,
	}, "error is a const: tool_arguments_too_large")
}

func TestContract_ToolArgumentRefusal_CapBelowOneRejected(t *testing.T) {
	// cap_chars minimum is 1: a zero cap would describe a refusal that can
	// never be retried under, which is not a real configuration (FR-036
	// rejects cap < 1 at the settings boundary).
	mustFailAsyncAPI(t, "ToolArgumentRefusal", ToolArgumentRefusal{
		Error:     "tool_arguments_too_large",
		Reason:    "too large",
		Tool:      "write_file",
		SizeChars: 64001,
		CapChars:  0,
	}, "cap_chars minimum 1")
}

func TestContract_ToolArgumentRefusal_UnknownPropertyRejected(t *testing.T) {
	mustFailAsyncAPI(t, "ToolArgumentRefusal", map[string]any{
		"error":      "tool_arguments_too_large",
		"reason":     "too large",
		"tool":       "write_file",
		"size_chars": 64001,
		"cap_chars":  64000,
		"extra":      "no",
	}, "additionalProperties is false")
}

func recallMarkFixture(state string) ToolResultRecallMark {
	return ToolResultRecallMark{
		Error:        "tool_result_recall_mark",
		Tool:         "search_email",
		ToolCallId:   "call_978a85",
		ArchiveLine:  41,
		SizeChars:    1178522,
		Turn:         6,
		ContentState: state,
		Hint:         "recall_conversation(tool_call_id=\"call_978a85\", archive_line=41) returns it in pages",
	}
}

func TestContract_ToolResultRecallMark_Populated(t *testing.T) {
	t.Run("emptied (D5)", func(t *testing.T) {
		mustPassAsyncAPI(t, "ToolResultRecallMark", recallMarkFixture("emptied"))
	})
	t.Run("capped (D4 shares the mark)", func(t *testing.T) {
		mustPassAsyncAPI(t, "ToolResultRecallMark", recallMarkFixture("capped"))
	})
}

func TestContract_ToolResultRecallMark_ZeroValue(t *testing.T) {
	mustFailAsyncAPI(t, "ToolResultRecallMark", ToolResultRecallMark{},
		"every field is required; zero value fails const, minLength, minimum and enum")
}

func TestContract_ToolResultRecallMark_WrongDiscriminatorRejected(t *testing.T) {
	f := recallMarkFixture("emptied")
	f.Error = "tool_result_emptied"
	mustFailAsyncAPI(t, "ToolResultRecallMark", f, "error is a const: tool_result_recall_mark")
}

func TestContract_ToolResultRecallMark_FullStateRejected(t *testing.T) {
	// "full" is ToolCall.content_state's default and is never a mark — a
	// mark exists only because the content is NOT full.
	mustFailAsyncAPI(t, "ToolResultRecallMark", recallMarkFixture("full"),
		"content_state enum is capped | emptied")
}

func TestContract_ToolResultRecallMark_NameOver64Rejected(t *testing.T) {
	// FR-018: the producer sanitises tool name and id to ≤ 64 printable
	// chars; the schema must refuse what the producer promises never to emit.
	t.Run("tool", func(t *testing.T) {
		f := recallMarkFixture("emptied")
		f.Tool = strings.Repeat("a", 65)
		mustFailAsyncAPI(t, "ToolResultRecallMark", f, "tool maxLength 64")
	})
	t.Run("tool_call_id", func(t *testing.T) {
		f := recallMarkFixture("emptied")
		f.ToolCallId = strings.Repeat("b", 65)
		mustFailAsyncAPI(t, "ToolResultRecallMark", f, "tool_call_id maxLength 64")
	})
	t.Run("exactly 64 passes", func(t *testing.T) {
		f := recallMarkFixture("emptied")
		f.Tool = strings.Repeat("a", 64)
		f.ToolCallId = strings.Repeat("b", 64)
		mustPassAsyncAPI(t, "ToolResultRecallMark", f)
	})
}

func TestContract_ToolResultRecallMark_TurnBelowOneRejected(t *testing.T) {
	// turn = 1 + preceding role:user archive lines, so it is never 0.
	f := recallMarkFixture("emptied")
	f.Turn = 0
	mustFailAsyncAPI(t, "ToolResultRecallMark", f, "turn minimum 1")
}

func TestContract_ToolResultRecallMark_ArchiveLineZeroAllowed(t *testing.T) {
	// archive_line is a zero-based index (mirrors ToolResultProjectionFrame).
	f := recallMarkFixture("emptied")
	f.ArchiveLine = 0
	mustPassAsyncAPI(t, "ToolResultRecallMark", f)
}

// The refusal is a toolResult-channel member (ADR-060 D2): it is returned as
// the tool's result and flows through ToolCallResultFrame.result. The mark is
// enrolled in the same oneOf as defensive over-provisioning (ADR-060 §7 item
// 3, the ToolAssemblyDuplicate precedent). Both must match exactly one
// branch of the union — their own $ref — and a malformed copy must be
// rejected rather than caught by the ordinary-object catch-all.
func TestContract_ToolCallResultFrame_ADR066FamilyMembersDiscriminated(t *testing.T) {
	frame := func(result any) ToolCallResultFrame {
		return ToolCallResultFrame{
			Type:      "tool_call_result",
			SessionId: "sess-1",
			Tool:      "write_file",
			CallId:    "call-5",
			Status:    "error",
			Result:    result,
		}
	}
	t.Run("well-formed ToolArgumentRefusal validates via its $ref", func(t *testing.T) {
		mustPassAsyncAPI(t, "ToolCallResultFrame", frame(map[string]any{
			"error":      "tool_arguments_too_large",
			"reason":     "write_file: serialised arguments are 64,001 chars; the limit is 64,000.",
			"tool":       "write_file",
			"size_chars": 64001,
			"cap_chars":  64000,
		}))
	})
	t.Run("malformed ToolArgumentRefusal (missing cap_chars) is rejected", func(t *testing.T) {
		mustFailAsyncAPI(t, "ToolCallResultFrame", frame(map[string]any{
			"error":      "tool_arguments_too_large",
			"reason":     "too large",
			"tool":       "write_file",
			"size_chars": 64001,
		}), "string-error object must satisfy exactly one $ref; catch-all excludes it")
	})
	t.Run("well-formed ToolResultRecallMark validates via its $ref", func(t *testing.T) {
		mustPassAsyncAPI(t, "ToolCallResultFrame", frame(recallMarkFixture("emptied")))
	})
	t.Run("malformed ToolResultRecallMark (bad content_state) is rejected", func(t *testing.T) {
		mustFailAsyncAPI(t, "ToolCallResultFrame", frame(recallMarkFixture("full")),
			"string-error object must satisfy exactly one $ref; catch-all excludes it")
	})
}
