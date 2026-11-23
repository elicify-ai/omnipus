package gateway

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SFH-R5-2: exercise the actual hub translator with the real pre-change event
// shape and, once implemented, the explicitly identified internal fields from
// team-lead's follow-up. Missing fields cannot create a false pass: the old
// uncorrelated ErrorFrame must fail the non-terminal wire assertions below.
func TestOpenCodeR5_RefusedInputBridgeNeverSendsTurnEndingErrorToSecondView(t *testing.T) {
	h := makeMinimalHandler()
	first, firstFrames := makeForwarderTestConn(16)
	second, secondFrames := makeForwarderTestConn(16)
	parent, parentFrames := makeForwarderTestConn(16)
	sid := "r5-finishing-helper"
	bindTestConnToSession(h, "r5-finishing-view-a", sid, first)
	bindTestConnToSession(h, "r5-finishing-view-b", sid, second)
	bindTestConnToSession(h, "r5-parent-view", "r5-parent", parent)
	// W14 catalogue oracle is authorized by team-lead's prior ruling. Keep
	// this synthetic event valid so WireLLMError cannot rewrite its sentence.
	const code = "delegation_not_resumable"
	text := strings.ReplaceAll(generated.LLMErrorUserMessages[code], "{cli}", "OpenCode")
	payload := agent.ErrorPayload{SessionID: sid, ChatID: sid, Stage: "human_input",
		Code: code, Message: text}
	// These are internal fields, not handwritten gateway/SPA wire types. Do
	// not substitute an unrelated id for an absent original client id.
	value := reflect.ValueOf(&payload).Elem()
	if marker := value.FieldByName("RefusedInput"); marker.IsValid() {
		require.Equal(t, reflect.Bool, marker.Kind())
		marker.SetBool(true)
	}
	if entry := value.FieldByName("RefusedInputEntryID"); entry.IsValid() {
		require.Equal(t, reflect.String, entry.Kind())
		entry.SetString("r5-actual-refused-human-row")
	}
	h.hubSyncTap(agent.Event{Kind: agent.EventKindError,
		Meta: agent.EventMeta{SessionKey: sid}, Payload: payload})
	require.Len(t, firstFrames, 1, "refusal must reach the sender view")
	require.Len(t, secondFrames, 1, "refusal must reach the other view of the same helper")
	assert.Empty(t, parentFrames, "no parent notice")
	firstRaw, secondRaw := <-firstFrames, <-secondFrames
	assert.Equal(t, firstRaw, secondRaw)
	var envelope wsTypeOnly
	require.NoError(t, json.Unmarshal(secondRaw, &envelope))
	switch envelope.Type {
	case string(generated.WsFrameTypeReplayMessage):
		var notice generated.ReplayMessageFrame
		require.NoError(t, json.Unmarshal(secondRaw, &notice))
		assert.Equal(t, "system", notice.Role)
		assert.Equal(t, sid, notice.SessionId)
		assert.Equal(t, payload.Message, notice.Content)
		require.NotNil(t, notice.Id)
		assert.NotEmpty(t, *notice.Id)
	case string(generated.WsFrameTypeError):
		var frame generated.ErrorFrame
		require.NoError(t, json.Unmarshal(secondRaw, &frame))
		require.Equal(t, &sid, frame.SessionId)
		// No original client id is available here. A turn-ending error with
		// only a session tag is the exact SFH-R5-2 regression, not a notice.
		assert.Nil(t, frame.ClientMessageId, "do not fabricate original client correlation")
		flag := reflect.ValueOf(frame).FieldByName("RefusedInput")
		if !flag.IsValid() {
			t.Fatalf("SFH-R5-2: actual second-view frame is an uncorrelated turn-ending error, not a supported non-terminal carrier: %s", secondRaw)
		}
		if flag.Kind() == reflect.Pointer {
			require.False(t, flag.IsNil())
			flag = flag.Elem()
		}
		require.Equal(t, reflect.Bool, flag.Kind())
		assert.True(t, flag.Bool(), "SFH-R5-2: generated wire discriminator must preserve input-refusal semantics")
	default:
		t.Fatalf("SFH-R5-2: unsupported refused-input carrier %q", envelope.Type)
	}
}
