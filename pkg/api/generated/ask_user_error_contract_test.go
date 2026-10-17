// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ask_user_error_contract_test.go — #1343/#1329 fix round 1, architect
// Decision A (evidence/queue-1329-fixround1-20261016/architect-ruling.md):
// the ErrorFrame contract gains ONE optional string property,
// ask_user_card_id (minLength 1, dependentRequired: [session_id]), while
// every existing property, required key and the preserved
// first_message_error → client_message_id dependency stay untouched, and
// no schema-level prohibition of client_message_id / first_message_error
// is introduced (omitting them on card errors is a producer duty, not a
// schema rule).
//
// Oracle independence: every expectation below derives from the ruling and
// from the two current schema copies — never from running an
// implementation.
//
// RED semantics (this pack runs BEFORE the schema change): the positive
// card-error case must FAIL — both copies carry additionalProperties:false,
// which rejects the not-yet-declared property. The kind-pinned negative
// cases must also FAIL, for a second reason: the pre-change rejection is an
// additionalProperties rejection, never the required dependency, minLength
// or type violation. The legacy controls must PASS — the change must not
// disturb them. GREEN requires the schema edit on BOTH copies plus
// regeneration.
//
// Fixtures are raw JSON-shaped maps, not ErrorFrame struct literals: the
// generated Go type has no AskUserCardId field yet and this pack must
// compile against the current types (dispatch constraint). Validation is
// the real schema harness (schema_harness_test.go) over the real contract
// files on disk — nothing is mocked; the dual-copy positive guard reuses
// mustPassBothCopies from askuser_frames_contract_test.go.
package generated

import (
	"errors"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"github.com/stretchr/testify/require"
)

// askUserCardErrorCopy is one canonical ErrorFrame schema copy, labeled for
// failure messages.
type askUserCardErrorCopy struct {
	name     string
	validate func(t *testing.T, schemaName string, v any) error
}

// askUserCardErrorCopies enumerates BOTH canonical ErrorFrame copies — the
// asyncapi.yaml inline components.schemas.ErrorFrame mirror (the codegen
// source) and the contracts/components/schemas/ErrorFrame.yaml file.
// Decision A merges the new property into both, and every case below pins
// its verdict on both, so an edit to one copy alone cannot go green (the
// hand-sync obligation, same guard as the askuser frames pack).
func askUserCardErrorCopies() []askUserCardErrorCopy {
	return []askUserCardErrorCopy{
		{"asyncapi inline copy", validateAgainstAsyncAPISchema},
		{"component schema copy", validateAgainstComponentSchema},
	}
}

// askUserCardErrorFixture returns a valid card-scoped error frame: the
// minimal shape Decision A makes contract-legal — type and message (the
// existing required pair), the session_id the dependency demands, and the
// card id the error is about. The card id mirrors the ask_… hex shape the
// askuser fixtures already use (askuser_frames_contract_test.go); the
// session id matches those fixtures too.
func askUserCardErrorFixture() map[string]any {
	return map[string]any{
		"type":             "error",
		"message":          "Recording your answer failed — the card could not be updated.",
		"session_id":       "session_owner_1",
		"ask_user_card_id": "ask_0123456789abcdef0123456789abcdef",
	}
}

// errorAt reports whether a violation sits on the frame's root object
// (property "") or on exactly the named frame property. jsonschema/v6
// reports object-level keywords (dependentRequired, additionalProperties)
// at the root and property-level keywords (type, minLength) at
// InstanceLocation == [property] (validateVal appends the property token),
// so a minLength on message can never satisfy a minLength required on
// ask_user_card_id.
func errorAt(e *jsonschema.ValidationError, property string) bool {
	if property == "" {
		return len(e.InstanceLocation) == 0
	}
	return len(e.InstanceLocation) == 1 && e.InstanceLocation[0] == property
}

// firstKind walks the validation-error tree (property-level violations nest
// under Causes) and returns the first violation of exactly kind K at the
// requested location. The concrete-kind match is what keeps the negative
// cases honest: on the pre-change schemas the same fixtures are rejected
// too, but by additionalProperties (unknown property) — that rejection must
// never satisfy a dependency, minLength or type requirement.
func firstKind[T jsonschema.ErrorKind](root *jsonschema.ValidationError, atProperty string) (T, bool) {
	var zero T
	stack := []*jsonschema.ValidationError{root}
	for len(stack) > 0 {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if e.ErrorKind == nil {
			continue
		}
		if k, ok := e.ErrorKind.(T); ok && errorAt(e, atProperty) {
			return k, true
		}
		stack = append(stack, e.Causes...)
	}
	return zero, false
}

// rejectionKeywordPaths flattens a rejection into its keyword paths
// ("dependentRequired/ask_user_card_id", "additionalProperties", …) — the
// diagnosis line when a kind pin does not match.
func rejectionKeywordPaths(t *testing.T, err error) []string {
	t.Helper()
	var root *jsonschema.ValidationError
	require.True(t, errors.As(err, &root),
		"expected a *jsonschema.ValidationError, got %T: %v", err, err)
	var paths []string
	stack := []*jsonschema.ValidationError{root}
	for len(stack) > 0 {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if e.ErrorKind != nil {
			paths = append(paths, strings.Join(e.ErrorKind.KeywordPath(), "/"))
		}
		stack = append(stack, e.Causes...)
	}
	return paths
}

// requireRootKind requires the rejection to carry a violation of exactly
// kind K at the frame-object level — where the object-level keywords report
// (dependentRequired, additionalProperties, required).
func requireRootKind[T jsonschema.ErrorKind](t *testing.T, copyName string, err error) T {
	t.Helper()
	require.Error(t, err, "%s: ErrorFrame must reject this frame", copyName)
	var root *jsonschema.ValidationError
	require.True(t, errors.As(err, &root),
		"%s: expected a *jsonschema.ValidationError, got %T: %v", copyName, err, err)
	k, ok := firstKind[T](root, "")
	if !ok {
		t.Fatalf("%s: rejection carried no %T violation at the frame object — keyword paths: %v",
			copyName, k, rejectionKeywordPaths(t, err))
	}
	return k
}

// requirePropertyKind requires the rejection to carry a violation of
// exactly kind K located on exactly the named frame property — where the
// property-level keywords report (type, minLength).
func requirePropertyKind[T jsonschema.ErrorKind](t *testing.T, copyName, prop string, err error) T {
	t.Helper()
	require.Error(t, err, "%s: ErrorFrame must reject this frame", copyName)
	var root *jsonschema.ValidationError
	require.True(t, errors.As(err, &root),
		"%s: expected a *jsonschema.ValidationError, got %T: %v", copyName, err, err)
	k, ok := firstKind[T](root, prop)
	if !ok {
		t.Fatalf("%s: rejection carried no %T violation on property %q — keyword paths: %v",
			copyName, k, prop, rejectionKeywordPaths(t, err))
	}
	return k
}

// askUserCardErrorCaseValidates — Decision A positive: an error frame
// carrying type, message, session_id and ask_user_card_id is contract-legal
// on both canonical copies. RED on the pre-change schemas: rejected there by
// additionalProperties:false because the property is not yet declared.
func askUserCardErrorCaseValidates(t *testing.T) {
	mustPassBothCopies(t, "ErrorFrame", askUserCardErrorFixture())
}

// askUserCardErrorCaseLegacyUntouched — Decision A retains every existing
// property and required key: the populated legacy frame (typed fixture, no
// card id) stays valid on both copies after the change. Its llm_error
// payload satisfies LLMError.yaml (code rate_limited is in the enum, copy
// and detail within their maxLengths), so this control is valid today.
func askUserCardErrorCaseLegacyUntouched(t *testing.T) {
	mustPassBothCopies(t, "ErrorFrame", FixtureErrorFrame_Populated())
}

// askUserCardErrorCaseNoMessageIDProhibition — Decision A makes omitting
// client_message_id / first_message_error on card errors a producer duty,
// NOT a schema rule: the approved YAML introduces no exclusion for those
// properties, so a card error that carries a client_message_id (the
// message-scoped-refusal shape the schema already documents) must stay
// valid. Guards against an over-implemented prohibition the ruling never
// approved.
func askUserCardErrorCaseNoMessageIDProhibition(t *testing.T) {
	withMessageID := askUserCardErrorFixture()
	withMessageID["client_message_id"] = "cmid_02"
	mustPassBothCopies(t, "ErrorFrame", withMessageID)
}

// askUserCardErrorCaseCardRequiresSession — Decision A's dependentRequired:
// ask_user_card_id requires session_id, and the rejection must be the
// dependency itself. Positive control first: the identical frame WITH
// session_id validates, proving ask_user_card_id is a declared property on
// both copies — so the rejection below can only be the dependency, never
// the unknown-property rejection the pre-change schemas also produce.
func askUserCardErrorCaseCardRequiresSession(t *testing.T) {
	mustPassBothCopies(t, "ErrorFrame", askUserCardErrorFixture())

	withoutSession := askUserCardErrorFixture()
	delete(withoutSession, "session_id")
	for _, schemaCopy := range askUserCardErrorCopies() {
		dep := requireRootKind[*kind.DependentRequired](t, schemaCopy.name,
			schemaCopy.validate(t, "ErrorFrame", withoutSession))
		require.Equal(t, "ask_user_card_id", dep.Prop,
			"%s: the violation must be the card id dependency", schemaCopy.name)
		require.Equal(t, []string{"session_id"}, dep.Missing,
			"%s: the ask_user_card_id dependency must require exactly session_id", schemaCopy.name)
	}
}

// askUserCardErrorCaseCardMinLength — Decision A: ask_user_card_id has
// minLength 1; the empty string is rejected ON the card id property
// (session_id present, so no dependency noise).
func askUserCardErrorCaseCardMinLength(t *testing.T) {
	emptyID := askUserCardErrorFixture()
	emptyID["ask_user_card_id"] = ""
	for _, schemaCopy := range askUserCardErrorCopies() {
		ml := requirePropertyKind[*kind.MinLength](t, schemaCopy.name, "ask_user_card_id",
			schemaCopy.validate(t, "ErrorFrame", emptyID))
		require.Equal(t, 0, ml.Got, "%s: the rejected value is the empty string", schemaCopy.name)
		require.Equal(t, 1, ml.Want, "%s: Decision A fixes ask_user_card_id minLength at 1", schemaCopy.name)
	}
}

// askUserCardErrorCaseCardType — Decision A: ask_user_card_id is a string;
// a JSON number is rejected ON the card id property.
func askUserCardErrorCaseCardType(t *testing.T) {
	numericID := askUserCardErrorFixture()
	numericID["ask_user_card_id"] = 42 // marshals to the JSON number 42
	for _, schemaCopy := range askUserCardErrorCopies() {
		typ := requirePropertyKind[*kind.Type](t, schemaCopy.name, "ask_user_card_id",
			schemaCopy.validate(t, "ErrorFrame", numericID))
		require.Equal(t, []string{"string"}, typ.Want,
			"%s: Decision A types ask_user_card_id as string", schemaCopy.name)
	}
}

// askUserCardErrorCaseLegacyDependency — the preserved dependentRequired:
// first_message_error still requires client_message_id on both copies,
// with the schema's own documented positive shapes as controls: the
// first-send outcome (both keys) and the message-scoped refusal
// (client_message_id alone — legal, only the reverse direction is
// dependent).
func askUserCardErrorCaseLegacyDependency(t *testing.T) {
	pair := map[string]any{
		"type":                "error",
		"message":             "The first message of this chat could not be saved.",
		"session_id":          "session_owner_1",
		"client_message_id":   "cmid_01",
		"first_message_error": "not_saved", // enum member, ErrorFrame.yaml
	}
	mustPassBothCopies(t, "ErrorFrame", pair)

	refusal := map[string]any{
		"type":              "error",
		"message":           "This message could not be delivered to @alice.",
		"session_id":        "session_owner_1",
		"client_message_id": "cmid_03",
	}
	mustPassBothCopies(t, "ErrorFrame", refusal)

	unpaired := map[string]any{
		"type":                "error",
		"message":             "The first message of this chat could not be saved.",
		"session_id":          "session_owner_1",
		"first_message_error": "not_saved",
	}
	for _, schemaCopy := range askUserCardErrorCopies() {
		dep := requireRootKind[*kind.DependentRequired](t, schemaCopy.name,
			schemaCopy.validate(t, "ErrorFrame", unpaired))
		require.Equal(t, "first_message_error", dep.Prop,
			"%s: the violation must be the preserved first-send dependency", schemaCopy.name)
		require.Equal(t, []string{"client_message_id"}, dep.Missing,
			"%s: the preserved dependency must require exactly client_message_id", schemaCopy.name)
	}
}

// TestContractAskUserCardError is the single dispatcher for the Decision A
// contract pack. Narrow run:
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestContractAskUserCardError$' -count=1 -v -p 1 ./pkg/api/generated/
func TestContractAskUserCardError(t *testing.T) {
	t.Run("card error frame validates on both canonical copies", askUserCardErrorCaseValidates)
	t.Run("legacy error frame without the new field still validates", askUserCardErrorCaseLegacyUntouched)
	t.Run("card error may carry client_message_id — no schema prohibition", askUserCardErrorCaseNoMessageIDProhibition)
	t.Run("ask_user_card_id without session_id rejected as dependency violation", askUserCardErrorCaseCardRequiresSession)
	t.Run("empty ask_user_card_id rejected by minLength 1", askUserCardErrorCaseCardMinLength)
	t.Run("non-string ask_user_card_id rejected by type string", askUserCardErrorCaseCardType)
	t.Run("first_message_error still requires client_message_id", askUserCardErrorCaseLegacyDependency)
}
