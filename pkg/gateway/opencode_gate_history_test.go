package gateway

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Independent process input: exact OpenCode 1.18.18 UAT capture, also preserved
// in runner/opencode_captured_error_test.go. Never synthesize it from the parser.
const opencodeGateCapturedUnknown = `{"type":"error","timestamp":1795147660495,"sessionID":"ses_e08dc8729ffexGiTHbDIAcf2Ue","error":{"name":"UnknownError","data":{"message":"Unexpected server error. Check server logs for details.","ref":"err_0be266a4"}}}`

// Founder-approved #1857 W4 contract sentences, referenced through the
// generated catalogue per team-lead's 2026-11-23 ruling, never renderer calls.
var (
	opencodeGateUnknownChat   = strings.ReplaceAll(generated.LLMErrorUserMessages["external_cli_failed"], "{cli}", "OpenCode")
	opencodeGateUnknownParent = strings.ReplaceAll(generated.LLMErrorNeutralMessages["external_cli_failed"], "{cli}", "OpenCode")
)

func opencodeGateReopen(t *testing.T, store *session.UnifiedStore, sid string) []session.TranscriptEntry {
	t.Helper()
	reopened, err := session.NewUnifiedStore(store.BaseDir())
	require.NoError(t, err)
	defer func() { require.NoError(t, reopened.Close()) }()
	entries, err := reopened.ReadTranscript(sid)
	require.NoError(t, err, "reopening must read durable history, not the running loop's cached transcript")
	return entries
}

// ARCHITECT A5 / ADR-091 D3: a failure first announced by live finalization
// must remain in the helper's own saved history after the viewer reopens it.
func TestOpenCodeGateA5_RefusedContinuationErrorSurvivesReopening(t *testing.T) {
	al, child, viewers, process := opencodeFix1895Launch(t, opencodeFix1895Program(t), "resume_refusal")
	_, _, err := al.EnqueueSteeringMessageWithStatus(child.SessionID, opencodeFix1895Worker,
		providers.Message{Role: "user", Content: "resume the conversation that never announced its id"}, "gate-a5")
	require.NoError(t, err)
	opencodeFix1895Finish(t, al, child, process)
	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleFailed, rec.State, "instrument: exercise a genuine refused continuation")
	entries := opencodeGateReopen(t, al.ResolveSessionStore(child.SessionID), child.SessionID)
	var saved []session.TranscriptEntry
	for _, entry := range entries {
		if entry.Status == "error" && entry.ErrorCode != "" {
			saved = append(saved, entry)
		}
		assert.NotContains(t, entry.Content, "no native conversation", "raw process/refusal diagnostics are not saved chat notices")
	}
	for _, viewer := range viewers {
		frames := opencodeFix1895Collect(t, viewer, child.SessionID)
		require.Equal(t, 2, frames.tools, "positive control: actual process tool frames reached both subscribers")
		require.Len(t, frames.errors, 1, "the finalizer really announced the failure live")
		live := frames.errors[0]
		require.NotNil(t, live.Payload)
		require.NotEmpty(t, live.Payload.LlmError.Code)
		var matching []session.TranscriptEntry
		for _, entry := range saved {
			if entry.ErrorCode == live.Payload.LlmError.Code && entry.Content == live.Message {
				matching = append(matching, entry)
			}
		}
		assert.Len(t, matching, 1, "A5: reopening must retain this exact typed live failure, not only an unrelated queued-input abandonment notice")
	}
}

// ARCHITECT A2 / DOCS-AUDIT-3 §1 / #1857 §13.8.4: the real 1.x capture must
// traverse the process, parser, classifier and actual hub. The parent receives
// its neutral catalogue voice, never raw diagnostics or the helper's I voice.
func TestOpenCodeGateA2_CapturedUnknownErrorUsesCatalogueForHelperAndParent(t *testing.T) {
	al, child, viewers, process := opencodeFix1895Launch(t, opencodeFix1895Program(t), "captured_unknown_1x")
	opencodeFix1895Finish(t, al, child, process)
	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleFailed, rec.State)
	entries := opencodeGateReopen(t, al.ResolveSessionStore(child.SessionID), child.SessionID)
	var saved []session.TranscriptEntry
	for _, entry := range entries {
		if entry.Status == "error" && entry.ErrorCode != "" {
			saved = append(saved, entry)
		}
		opencodeGateNoRawDiagnostic(t, entry.Content)
	}
	require.Len(t, saved, 1)
	assert.Equal(t, "external_cli_failed", saved[0].ErrorCode)
	assert.Equal(t, opencodeGateUnknownChat, saved[0].Content, "W4 helper sentence survives reload exactly")
	for _, viewer := range viewers {
		frames := opencodeFix1895Collect(t, viewer, child.SessionID)
		require.Equal(t, 2, frames.tools)
		require.Len(t, frames.errors, 1)
		live := frames.errors[0]
		require.NotNil(t, live.Payload)
		assert.Equal(t, "external_cli_failed", live.Payload.LlmError.Code)
		assert.Equal(t, opencodeGateUnknownChat, live.Message, "W4 exact sentence, not stale generic-unknown documentation")
		opencodeGateNoRawDiagnostic(t, live.Message)
		if live.Payload.LlmError.Detail != nil {
			opencodeGateNoRawDiagnostic(t, *live.Payload.LlmError.Detail)
		}
	}
	messages, _, more, err := al.GetMessageInboxStore().Drain(rec.SteeringSessionID(), child.SessionID, "", 10)
	require.NoError(t, err)
	require.False(t, more)
	require.Len(t, messages, 1, "one real completion delivered to the parent's inbox")
	kind, err := messages[0].Discriminator()
	require.NoError(t, err)
	require.Equal(t, "error", kind, "failed completion is the contract's error variant, not a successful handback")
	failure, err := messages[0].AsSessionMessageError()
	require.NoError(t, err)
	assert.True(t, failure.Fatal, "parent must be told that this helper run failed")
	assert.Contains(t, failure.Text, "external_cli_failed", "parent gets the typed cause")
	assert.Contains(t, failure.Text, opencodeGateUnknownParent, "parent gets the complete exact neutral W4 sentence")
	assert.NotContains(t, failure.Text, opencodeGateUnknownChat, "parent must not impersonate the helper's I voice")
	assert.NotContains(t, strings.ToLower(failure.Text), "external-cli run failed:", "do not wrap the safe sentence in an internal diagnostic")
	opencodeGateNoRawDiagnostic(t, failure.Text)
}

func opencodeGateNoRawDiagnostic(t *testing.T, text string) {
	t.Helper()
	for _, raw := range []string{"UnknownError", "Unexpected server error", "err_0be266a4", "ses_e08dc8729ffexGiTHbDIAcf2Ue", "Check server logs"} {
		assert.NotContains(t, text, raw, "#1857: raw external diagnostic stays in operator logs, not public chat/parent surfaces")
	}
}
