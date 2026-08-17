//go:build !windows

// tool_call_result_error_key_contract_test.go — F13 regression guard.
//
// contracts/asyncapi.yaml's ToolCallResultFrame.result oneOf (round-2
// hardening, ADR-060 finding F1) excludes any object carrying a reserved
// discriminator key — _truncated, _marshal_error, _ref, error — from its
// permissive object catch-all branch, so that only the seven named $refs
// (TruncatedResult, MarshalErrorResult, ToolResultRef, DelegationFailure,
// FileExistsRefusal, PermissionDenied, ToolAssemblyDuplicate) can match a
// reserved-key-bearing object.
//
// Before the F13 fix, that exclusion fired on bare PRESENCE of an `error`
// key, regardless of its JSON type. Two real producers persist a plain
// map[string]any result carrying a BOOLEAN `"error": true` flag that is not
// an attempt at any of the four `error`-keyed $refs (all four pin `error` to
// a string const):
//
//   - pkg/agent/approval_transcript.go's settleAskToolCallTranscript, which
//     records a denied/timed-out/cancelled tool-approval outcome as
//     {"error": true, "text": ..., "reason": ..., "permanent": ...}.
//   - pkg/agent/subturn.go's spawnSubTurn deferred cleanup, which records a
//     failed delegation's persisted result as {"text": ..., "error": true}.
//
// Neither shape matches any of the four string-discriminated $refs (their
// `error` value is a bool, not e.g. "permission_denied"), and under the old
// bare-presence exclusion neither matched the object catch-all either — so
// every replayed denied-approval and failed-delegation frame was
// unconditionally schema-invalid against its own contract. F13 keys the
// exclusion on `error` being a STRING instead, so a boolean-error object
// falls through to the catch-all (a valid "ordinary object" result) while a
// string-error object is still checked against the four typed $refs as
// before.
//
// TestContract_ToolCallResultFrame_BooleanErrorPasses is written to FAIL
// against the pre-F13 schema: reverting the `properties: {error: {type:
// string}}` addition on the object catch-all's `required: [error]` exclusion
// entry in contracts/asyncapi.yaml (i.e. going back to a bare `required:
// [error]`) reproduces the schema-invalid rejection this test guards
// against.
package generated

import "testing"

// approvalDenialResultFixture mirrors settleAskToolCallTranscript's Result
// map verbatim (pkg/agent/approval_transcript.go): a boolean `error` flag
// alongside `text`/`reason`/`permanent`, never a typed $ref.
func approvalDenialResultFixture() map[string]any {
	return map[string]any{
		"error":     true,
		"text":      "Your bash call was denied by the user.",
		"reason":    "user",
		"permanent": true,
	}
}

// subTurnFailureResultFixture mirrors spawnSubTurn's toolCallResult map
// verbatim (pkg/agent/subturn.go): a boolean `error` flag alongside `text`,
// persisted on the parent's spawn tool_call when a delegated sub-turn fails.
func subTurnFailureResultFixture() map[string]any {
	return map[string]any{
		"text":  "sub-turn reported an error",
		"error": true,
	}
}

func TestContract_ToolCallResultFrame_BooleanErrorPasses(t *testing.T) {
	t.Run("settleAskToolCallTranscript shape (denied approval)", func(t *testing.T) {
		mustPassAsyncAPI(t, "ToolCallResultFrame", ToolCallResultFrame{
			Type:      "tool_call_result",
			SessionId: "sess-1",
			Tool:      "bash",
			CallId:    "call-1",
			Status:    "error",
			Result:    approvalDenialResultFixture(),
		})
	})

	t.Run("spawnSubTurn shape (failed delegation)", func(t *testing.T) {
		mustPassAsyncAPI(t, "ToolCallResultFrame", ToolCallResultFrame{
			Type:      "tool_call_result",
			SessionId: "sess-1",
			Tool:      "delegate",
			CallId:    "call-2",
			Status:    "error",
			Result:    subTurnFailureResultFixture(),
		})
	})
}

// TestContract_ToolCallResultFrame_StringErrorStillDiscriminated proves the
// F13 fix did not weaken the union: an object whose `error` value IS a
// string still only matches a named $ref (or is correctly rejected if it's
// malformed) — it must not silently pass via the catch-all just because the
// catch-all's exclusion changed shape.
func TestContract_ToolCallResultFrame_StringErrorStillDiscriminated(t *testing.T) {
	t.Run("well-formed PermissionDenied still validates via its $ref", func(t *testing.T) {
		mustPassAsyncAPI(t, "ToolCallResultFrame", ToolCallResultFrame{
			Type:      "tool_call_result",
			SessionId: "sess-1",
			Tool:      "write_file",
			CallId:    "call-3",
			Status:    "error",
			Result: map[string]any{
				"error":     "permission_denied",
				"message":   "Access to this path is denied by filesystem policy.",
				"tool":      "write_file",
				"reason":    "access denied: outside scope",
				"permanent": true,
			},
		})
	})

	t.Run("malformed PermissionDenied (missing permanent) is rejected, not silently caught-all", func(t *testing.T) {
		mustFailAsyncAPI(t, "ToolCallResultFrame", ToolCallResultFrame{
			Type:      "tool_call_result",
			SessionId: "sess-1",
			Tool:      "write_file",
			CallId:    "call-4",
			Status:    "error",
			Result: map[string]any{
				"error":   "permission_denied",
				"message": "Access to this path is denied by filesystem policy.",
				"tool":    "write_file",
				"reason":  "access denied: outside scope",
				// "permanent" deliberately omitted — must fail its $ref AND
				// must still be excluded from the catch-all (string error),
				// so it should match nothing and be rejected outright.
			},
		}, "error is a string discriminator ('permission_denied') so this must be checked "+
			"against PermissionDenied's $ref, which requires permanent — it must not fall "+
			"through to the object catch-all just because F13 loosened the exclusion")
	})
}
