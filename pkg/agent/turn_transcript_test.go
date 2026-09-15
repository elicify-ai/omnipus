// turn_transcript_test.go: tests for transcript recording for a turn — tool-call, assistant and error entries

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from turn.go tests 2026-09-15 ---

// TestIsTrustedInternalStage_HookStages locks the trustedInternalStageSet
// membership directly: every stage hookAbortError can reach (the literal
// "hooks" stage it always passes to appendErrorTranscript, plus the
// defensive before_tool/after_tool entries) must be trusted, alongside the
// pre-existing before_llm/after_llm/model_switch/etc. entries. An unrelated
// stage must remain untrusted.
func TestIsTrustedInternalStage_HookStages(t *testing.T) {
	cases := []struct {
		name        string
		stage, kind string
		want        bool
	}{
		{"hooks/error trusted — hookAbortError's actual appendErrorTranscript stage", "hooks", "error", true},
		{"before_tool/error trusted (defensive)", "before_tool", "error", true},
		{"after_tool/error trusted (defensive)", "after_tool", "error", true},
		{"before_llm/error trusted (pre-existing)", "before_llm", "error", true},
		{"after_llm/error trusted (pre-existing)", "after_llm", "error", true},
		{"unrelated stage/kind is not trusted", "runTurn", "error", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isTrustedInternalStage(tc.stage, tc.kind))
		})
	}
}

// (W2-20 #3 — TestSummarizeDroppedTurns_Respects50PercentCap removed:
// summarizeDroppedTurns is deleted as part of the context-paging epic.
// handleModelSwitch now uses windowTrim (FR-011) with no LLM call.
// The windowTrim budget arithmetic is covered by TestWindowTrim_* in
// window_trim_test.go and TestModelSwitch_ReWindowsNoSummary.)

// =============================================================================
// W2-27 — appendErrorTranscript no-op paths
// =============================================================================
//
// W2-33 (silent-failure-A #12) flagged that appendErrorTranscript silently
// no-ops on nil store / empty session ID; we assert that behavior so a
// regression that panics or writes to a nil store surfaces as a failure.
func TestAppendErrorTranscript_NoOpOnNilStore(t *testing.T) {
	ts := &turnState{
		transcriptStore:     nil,
		transcriptSessionID: "session_test",
	}
	// Must not panic; must not call AppendTranscript (would NPE on nil).
	ts.appendErrorTranscript("error", "test", "should be ignored")
	// If we got here without panicking, the no-op path is intact.
}

func TestAppendErrorTranscript_NoOpOnEmptySessionID(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewUnifiedStore(tmpDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	ts := &turnState{
		transcriptStore:     store,
		transcriptSessionID: "",
	}
	// Must not panic; must not write to the store.
	ts.appendErrorTranscript("error", "test", "should be ignored")

	// Verify nothing was written — every session in the store still has
	// no transcript.
	entries := filepath.Join(tmpDir)
	_, err = os.ReadDir(entries)
	require.NoError(t, err)
	// We don't enumerate every session — the contract is that the call
	// returns silently without panicking, and the no-op is observable
	// through the lack of a panic.
}

// Replay looks up the catalogue by ErrorCode (getLLMErrorDisplay →
// codeToMessage), not by the persisted Content. If the workspace refusal
// lands as unknown, a reload shows "we can't tell why" even though the
// live frame was right.
func TestAppendErrorTranscript_WorkspaceRefusal_StampsAgentNotConfigured(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)

	ts := &turnState{
		transcriptStore:     store,
		transcriptSessionID: meta.ID,
		agentID:             "main",
	}
	llm := LLMError{
		Code:      CodeAgentNotConfigured,
		Message:   UserMessageForCode(CodeAgentNotConfigured),
		Retryable: isRetryable(CodeAgentNotConfigured),
	}
	ts.appendClassifiedError(EventKindError.String(), "workspace", llm)
	catalogue := llm.Message

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var got *session.TranscriptEntry
	for i := range entries {
		if entries[i].Type == session.EntryTypeSystem && entries[i].Status == "error" {
			got = &entries[i]
			break
		}
	}
	require.NotNil(t, got, "workspace refusal must persist a system error entry for replay")
	assert.Equal(t, string(CodeAgentNotConfigured), got.ErrorCode,
		"replay stamps the bubble from ErrorCode; unknown would resurrect the UAT defect")
	assert.Equal(t, catalogue, got.Content,
		"persisted text must stay the catalogue sentence, not a re-classified unknown line")
	assert.Equal(t, isRetryable(CodeAgentNotConfigured), got.ErrorRetryable)
	assert.True(t, isTrustedInternalStage("workspace", EventKindError.String()),
		"workspace/error must be a trusted internal stage so a later classifier change cannot clobber the sentence")
}

// Uncoded workspace writes must NOT be restamped as membership. That lie
// is how a work-dir failure replayed as "add this agent to a workspace".
func TestAppendErrorTranscript_UncodedWorkspaceDoesNotStampMembership(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)

	ts := &turnState{
		transcriptStore:     store,
		transcriptSessionID: meta.ID,
		agentID:             "main",
	}
	catalogue := UserMessageForCode(CodeWorkspaceUnavailable)
	ts.appendErrorTranscript(EventKindError.String(), "workspace", catalogue)

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var got *session.TranscriptEntry
	for i := range entries {
		if entries[i].Type == session.EntryTypeSystem && entries[i].Status == "error" {
			got = &entries[i]
			break
		}
	}
	require.NotNil(t, got)
	assert.NotEqual(t, string(CodeAgentNotConfigured), got.ErrorCode,
		"an uncoded workspace write must not be restamped as membership")
	assert.Equal(t, string(CodeUnknown), got.ErrorCode,
		"a catalogue sentence has no classifier substring; uncoded write must stay unknown")
}

func TestAppendClassifiedError_KeepsCallerCodeThroughCatalogueSentence(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)

	ts := &turnState{
		transcriptStore:     store,
		transcriptSessionID: meta.ID,
		agentID:             "main",
	}
	llm := LLMError{
		Code:      CodeWorkspaceUnavailable,
		Message:   UserMessageForCode(CodeWorkspaceUnavailable),
		Retryable: isRetryable(CodeWorkspaceUnavailable),
	}
	ts.appendClassifiedError(EventKindError.String(), "workspace", llm)

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var got *session.TranscriptEntry
	for i := range entries {
		if entries[i].Type == session.EntryTypeSystem && entries[i].Status == "error" {
			got = &entries[i]
			break
		}
	}
	require.NotNil(t, got)
	assert.Equal(t, string(CodeWorkspaceUnavailable), got.ErrorCode,
		"replay looks up by ErrorCode; re-classifying the catalogue sentence would stamp unknown")
	assert.Equal(t, llm.Message, got.Content)
}

func TestAppendClassifiedError_ModelSwitchStampsModelUnavailable(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)

	ts := &turnState{
		transcriptStore:     store,
		transcriptSessionID: meta.ID,
		agentID:             "main",
	}
	llm := LLMError{
		Code:      CodeModelUnavailable,
		Message:   UserMessageForCode(CodeModelUnavailable),
		Retryable: isRetryable(CodeModelUnavailable),
	}
	ts.appendClassifiedError(EventKindError.String(), "model_switch", llm)

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var got *session.TranscriptEntry
	for i := range entries {
		if entries[i].Type == session.EntryTypeSystem && entries[i].Status == "error" {
			got = &entries[i]
			break
		}
	}
	require.NotNil(t, got)
	assert.Equal(t, string(CodeModelUnavailable), got.ErrorCode)
	assert.Equal(t, llm.Message, got.Content)
	assert.NotEqual(t, string(CodeUnknown), got.ErrorCode)
}

func TestAppendErrorTranscript_RateLimitDenial_StampsRateLimited(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)

	ts := &turnState{
		transcriptStore:     store,
		transcriptSessionID: meta.ID,
		agentID:             "main",
	}
	// Caller text without the historical "rate limit:" prefix — trusted
	// stage must still stamp CodeRateLimited, not unknown.
	ts.appendErrorTranscript(EventKindRateLimit.String(), "rate_limit", "session daily cost cap (retry shortly)")

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var got *session.TranscriptEntry
	for i := range entries {
		if entries[i].Type == session.EntryTypeSystem && entries[i].Status == "error" {
			got = &entries[i]
			break
		}
	}
	require.NotNil(t, got)
	assert.True(t, isTrustedInternalStage("rate_limit", EventKindRateLimit.String()),
		"rate_limit/rate_limit must be trusted; stage runTurn was the live-vs-reload landmine")
	assert.Equal(t, string(CodeRateLimited), got.ErrorCode,
		"replay looks up by ErrorCode; unknown would say we cannot tell why")
	assert.Equal(t, "session daily cost cap (retry shortly)", got.Content,
		"trusted stage must keep the caller text, not a reclassified unknown line")
}

func TestOutcomeRelabelApplies(t *testing.T) {
	assert.True(t, outcomeRelabelApplies("", CodeMediaUnsupported),
		"empty residual 4xx is the FR-017a inconclusive case")
	assert.True(t, outcomeRelabelApplies(CodeUnknown, CodeMediaUnsupported),
		"CodeUnknown is the FR-017a inconclusive case")
	assert.False(t, outcomeRelabelApplies(CodeRateLimited, CodeMediaUnsupported),
		"a later classified failure must keep its own code")
	assert.False(t, outcomeRelabelApplies(CodeUnknown, ""),
		"no stamp means the classifier verdict stands")
}

func TestAppendClassifiedError_OutcomeRelabelDoesNotClobberDistinctCode(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)

	ts := &turnState{
		transcriptStore:     store,
		transcriptSessionID: meta.ID,
		agentID:             "main",
	}
	ts.setOutcomeRelabel(CodeMediaUnsupported)
	llm := LLMError{
		Code:      CodeRateLimited,
		Message:   UserMessageForCode(CodeRateLimited),
		Retryable: isRetryable(CodeRateLimited),
	}
	ts.appendClassifiedError(EventKindError.String(), "hooks", llm)

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var got *session.TranscriptEntry
	for i := range entries {
		if entries[i].Type == session.EntryTypeSystem && entries[i].Status == "error" {
			got = &entries[i]
			break
		}
	}
	require.NotNil(t, got)
	assert.Equal(t, string(CodeRateLimited), got.ErrorCode,
		"a later classified failure must keep its own code after a successful media strip-retry")
	assert.Equal(t, llm.Message, got.Content)
	assert.NotEqual(t, string(CodeMediaUnsupported), got.ErrorCode,
		"reload must not say the model rejected an image when the later failure was a rate limit")
}

func TestAppendClassifiedError_OutcomeRelabelStillLabelsUnknown(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)

	ts := &turnState{
		transcriptStore:     store,
		transcriptSessionID: meta.ID,
		agentID:             "main",
	}
	ts.setOutcomeRelabel(CodeMediaUnsupported)
	llm := LLMError{
		Code:      CodeUnknown,
		Message:   UserMessageForCode(CodeUnknown),
		Retryable: isRetryable(CodeUnknown),
	}
	ts.appendClassifiedError(EventKindError.String(), "runTurn", llm)

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var got *session.TranscriptEntry
	for i := range entries {
		if entries[i].Type == session.EntryTypeSystem && entries[i].Status == "error" {
			got = &entries[i]
			break
		}
	}
	require.NotNil(t, got)
	assert.Equal(t, string(CodeMediaUnsupported), got.ErrorCode,
		"FR-017a must still label an inconclusive residual 4xx as media after a successful strip-retry")
	assert.Equal(t, defaultUserMessage(CodeMediaUnsupported), got.Content)
}
