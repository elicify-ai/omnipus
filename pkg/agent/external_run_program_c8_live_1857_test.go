package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
)

// #1857 X2 (gate finding sfh F1; spec 13.8.5 row C8). The curated program-path
// refusal (a missing or failing Settings program path) is the text the person
// sees, live and after a reload alike. The live frame must never say "we can
// not tell why" (the generic unknown sentence) for this refusal.
//
// The test drives the real emitter, reportProgramRefusal, with the curated
// refusal text from thirdpartycli, takes the ErrorPayload it emits on the event
// bus, runs it through the real wire translation (WireLLMError, the call the
// gateway hub makes), and compares the live frame with what the same call
// saved to the transcript.
func TestC8ProgramRefusal_LiveFrameKeepsCuratedSentence_1857(t *testing.T) {
	const cli = "codex"
	curated := thirdpartycli.NoPathText(cli)
	require.NotEmpty(t, curated, "SETUP: the curated refusal text must exist")

	al, ts := newTestAgentLoopForRateLimit(t)
	store, sessionID := newRateLimitTranscript(t, ts)
	sub := al.SubscribeEvents(16)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })

	ed := &runExternalCLISubTurnState{al: al, childTS: ts, agent: ts.agent}
	refusal := ed.reportProgramRefusal(cli, curated)
	require.Error(t, refusal, "SETUP: the program refusal is returned as the turn error")

	// The payload the emitter put on the bus.
	var payload *ErrorPayload
	deadline := time.After(5 * time.Second)
	for payload == nil {
		select {
		case ev := <-sub.C:
			if p, ok := ev.Payload.(ErrorPayload); ok && ev.Kind == EventKindError && p.Stage == "external_cli" {
				cp := p
				payload = &cp
			}
		case <-deadline:
			t.Fatal("SETUP: reportProgramRefusal emitted no external_cli error event")
		}
	}

	live := WireLLMError(*payload)

	generic := defaultUserMessage(CodeUnknown)
	filled := strings.ReplaceAll(generated.LLMErrorUserMessages["external_cli_not_found"], "{cli}", "Codex")

	assert.Equal(t, "external_cli_not_found", live.Code,
		"C8: the refusal travels on its own typed code")
	assert.NotEqual(t, generic, live.Message,
		"C8 / F1: the live frame must never be demoted to the generic unknown sentence")
	// The sentence the person sees is a C8 sentence: the curated refusal text
	// from the input, or the catalogue C8 sentence with its {cli} slot filled.
	assert.Contains(t, []string{curated, filled}, live.Message,
		"C8 / F1: the live frame must carry the curated refusal or the filled C8 catalogue sentence")

	// Live / reload parity: the saved record for the same refusal.
	var saved []session.TranscriptEntry
	for _, e := range readTranscriptEntries(t, store, sessionID) {
		if e.Type == session.EntryTypeSystem {
			saved = append(saved, e)
		}
	}
	require.Len(t, saved, 1, "the refusal must be saved once")
	assert.Equal(t, "external_cli_not_found", saved[0].ErrorCode, "the saved record keeps the typed code")
	assert.NotEqual(t, generic, saved[0].Content, "the saved record keeps a C8 sentence, never the generic one")
	assert.Equal(t, saved[0].Content, live.Message,
		"C8 live/reload parity: the live frame text must equal the saved record text")

	// No raw text: no literal slot and no internal wrapper text in the frame.
	assert.False(t, messageHasSlots(live.Message), "C8: no raw {slot} in the live frame: %q", live.Message)
	assert.NotContains(t, live.Message, "external-cli run failed", "C8: no internal wrapper text in the live frame")
}
