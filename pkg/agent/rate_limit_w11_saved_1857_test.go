package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// #1857 X1 (diagnosis fix A; spec 13.8.5 B3, wording W11). After the LLM-call
// limit refuses a turn, the saved chat record holds the W11 sentence from the
// generated catalogue (code agent_request_limit) with {minutes} filled from the
// retry time, rounded up to whole minutes. Never the internal "rate limit: ..."
// text and never the generic sentence. One entry, translated once.
func TestRecordRateLimitDenial_LLMCall_SavesFilledW11Sentence_1857(t *testing.T) {
	al, ts := newTestAgentLoopForRateLimit(t)
	store, sessionID := newRateLimitTranscript(t, ts)

	// 3591.75184 s is 59.9 minutes: W11 says 60 (the wait rounded up).
	al.recordRateLimitDenial(ts, "llm_call", RateLimitPayload{
		Scope:             "agent",
		Resource:          "llm_call",
		PolicyRule:        "agent_max_llm_per_hour",
		RetryAfterSeconds: 3591.75184,
		AgentID:           ts.agentID,
	}, nil)

	var systemEntries []session.TranscriptEntry
	for _, e := range readTranscriptEntries(t, store, sessionID) {
		if e.Type == session.EntryTypeSystem {
			systemEntries = append(systemEntries, e)
		}
	}
	require.Len(t, systemEntries, 1, "one denial must save exactly one system entry; got %+v", systemEntries)
	entry := systemEntries[0]

	template := generated.LLMErrorUserMessages["agent_request_limit"]
	require.Contains(t, template, "{minutes}", "SETUP: W11 in the generated catalogue carries the {minutes} slot")
	want := strings.ReplaceAll(template, "{minutes}", "60")

	assert.Equal(t, string(CodeAgentRequestLimit), entry.ErrorCode, "the saved entry keeps code agent_request_limit")
	assert.Equal(t, want, entry.Content,
		"the saved record must hold the W11 sentence with {minutes} filled from the retry time")
	assert.NotEqual(t, generated.LLMErrorUserMessages[string(CodeUnknown)], entry.Content,
		"the saved record must not fall back to the generic sentence")
	lowered := strings.ToLower(entry.Content)
	assert.NotContains(t, lowered, "rate limit:", "no internal rate-limit text in the saved record")
	assert.NotContains(t, lowered, "retry after", "no internal retry-after text in the saved record")
	assert.False(t, messageHasSlots(entry.Content), "no raw {slot} in the saved record: %q", entry.Content)
}
