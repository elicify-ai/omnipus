package generated

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// U7 — FR-024 wire/contract slice (spec docs/internal/specs/session-core-spec.md
// §"Contract Changes" C-INPUT):
//
//	"contracts/components/schemas/MessageFrame.yaml,
//	 contracts/components/schemas/MessageStatusFrame.yaml; Message/replay in
//	 contracts/asyncapi.yaml ... MessageStatusFrame.state adds `discarded`,
//	 reason=`stopped_before_delivery`. Message/REST/replay retain read-only
//	 input_disposition: original message_id:string, optional
//	 client_message_id, state=`discarded`, reason=`stopped_before_delivery`."
//
// These read the CONTRACT SOURCE, not the generated artifact: the generator
// reads the inline asyncapi.yaml copy for frames, so a test against the
// generated Go type (a bare `string` for State, with no enum) would silently
// pass on a stale/missing contract. componentSchemaYAML resolves the component
// file (all four schemas below are file-hosted).

type u7DispositionProp struct {
	Type     string   `yaml:"type"`
	ReadOnly bool     `yaml:"readOnly"`
	Const    string   `yaml:"const"`
	Enum     []string `yaml:"enum"`
	Required []string `yaml:"required"`
	Props    map[string]struct {
		Type     string `yaml:"type"`
		ReadOnly bool   `yaml:"readOnly"`
	} `yaml:"properties"`
}

type u7SchemaDoc struct {
	Properties map[string]u7DispositionProp `yaml:"properties"`
}

func u7ParseSchema(t *testing.T, name string) u7SchemaDoc {
	t.Helper()
	var doc u7SchemaDoc
	require.NoError(t, yaml.Unmarshal(componentSchemaYAML(t, name), &doc), "parse component schema %s", name)
	return doc
}

// TestSessionCoreU7_MessageStatusFrame_DiscardedStateDeclared is the C-INPUT
// contract half for the frame state. RED on the pre-change contract:
// MessageStatusFrame.state enum is exactly [received, working, failed] and the
// schema declares no `reason` property at all.
func TestSessionCoreU7_MessageStatusFrame_DiscardedStateDeclared(t *testing.T) {
	doc := u7ParseSchema(t, "MessageStatusFrame")

	state, ok := doc.Properties["state"]
	require.True(t, ok, "MessageStatusFrame must declare a `state` property")
	assert.Contains(t, state.Enum, "discarded",
		"C-INPUT: MessageStatusFrame.state must add the `discarded` value (FR-024)")

	reason, ok := doc.Properties["reason"]
	require.True(t, ok,
		"C-INPUT: MessageStatusFrame must declare a `reason` property carrying reason=stopped_before_delivery (FR-024)")
	gotReason := reason.Const
	if gotReason == "" && len(reason.Enum) > 0 {
		gotReason = reason.Enum[0]
	}
	assert.Equal(t, "stopped_before_delivery", gotReason,
		"C-INPUT: the discarded delivery reason must be stopped_before_delivery (FR-024)")
}

// TestSessionCoreU7_Message_InputDispositionDeclared is the C-INPUT contract
// half for the REST/replay read-only projection. RED on the pre-change
// contract: Message.yaml declares no input_disposition property.
func TestSessionCoreU7_Message_InputDispositionDeclared(t *testing.T) {
	doc := u7ParseSchema(t, "Message")

	disp, ok := doc.Properties["input_disposition"]
	require.True(t, ok,
		"C-INPUT: Message must retain a read-only input_disposition projection (FR-024)")
	assert.True(t, disp.ReadOnly,
		"C-INPUT: input_disposition is read-only (no client release/discard action, FR-024)")
	assert.Contains(t, disp.Required, "message_id",
		"input_disposition.message_id is the original input id (FR-024)")
	assert.Contains(t, disp.Required, "state", "input_disposition.state is required (FR-024)")
	assert.Contains(t, disp.Required, "reason", "input_disposition.reason is required (FR-024)")
	_, hasClientID := disp.Props["client_message_id"]
	assert.True(t, hasClientID,
		"input_disposition must declare the OPTIONAL client_message_id (FR-024)")
}

// TestSessionCoreU7_ReplayMessageFrame_InputDispositionDeclared covers the
// "Message/replay" half of C-INPUT. RED on the pre-change contract:
// ReplayMessageFrame.yaml declares no input_disposition property.
func TestSessionCoreU7_ReplayMessageFrame_InputDispositionDeclared(t *testing.T) {
	doc := u7ParseSchema(t, "ReplayMessageFrame")
	_, ok := doc.Properties["input_disposition"]
	require.True(t, ok,
		"C-INPUT: the replay projection must retain a read-only input_disposition (FR-024)")
}

// TestSessionCoreU7_MessageStatusFrame_DiscardedWireAccepted validates an ACTUAL
// discarded frame against the asyncapi schema the generator reads. RED on the
// pre-change contract: the state enum rejects `discarded` and
// additionalProperties:false rejects `reason`.
func TestSessionCoreU7_MessageStatusFrame_DiscardedWireAccepted(t *testing.T) {
	raw := []byte(`{"type":"message_status","session_id":"sess-u7","client_message_id":"c-u7","state":"discarded","reason":"stopped_before_delivery"}`)
	require.NoError(t, validateAgainstAsyncAPISchemaRawJSON(t, "MessageStatusFrame", raw),
		"C-INPUT: a discarded MessageStatusFrame (reason=stopped_before_delivery) must validate against the asyncapi schema")
}

// TestSessionCoreU7_MessageStatusFrame_BaselineAccepted is the instrument
// control for the validator above: a frame the CURRENT contract accepts must
// still be accepted, proving the validator is not failing everything (so the
// discarded-frame failure is a real contract gap, not a broken instrument).
// GREEN by design on the pre-change contract.
func TestSessionCoreU7_MessageStatusFrame_BaselineAccepted(t *testing.T) {
	raw := []byte(`{"type":"message_status","session_id":"sess-u7","client_message_id":"c-u7","state":"received"}`)
	require.NoError(t, validateAgainstAsyncAPISchemaRawJSON(t, "MessageStatusFrame", raw),
		"instrument control: a baseline `received` frame must validate")
}

// TestSessionCoreU7_MessageStatusFrame_UnknownStateRejected is the negative
// control: the validator must REJECT an unknown state, proving it can actually
// see the enum membership (so its acceptance of `discarded` later is
// meaningful). GREEN by design on the pre-change contract.
func TestSessionCoreU7_MessageStatusFrame_UnknownStateRejected(t *testing.T) {
	raw := []byte(`{"type":"message_status","session_id":"sess-u7","client_message_id":"c-u7","state":"definitely_not_a_state"}`)
	require.Error(t, validateAgainstAsyncAPISchemaRawJSON(t, "MessageStatusFrame", raw),
		"instrument control: an unknown state must be rejected by the enum")
}
