package agent

// #1537 agent half of orders 1/1a/2/1e/1f/3a/7a. Oracle: approved
// helper-model-notices-spec.md FR-001/002/006/011/013. Real producers,
// response stage, transcript store and audience resolver; only provider edge fake.
import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/require"
)

func hmPayloadChild(t *testing.T, payload any) string {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	for _, key := range []string{"ChildSessionID", "child_session_id"} {
		if s, ok := m[key].(string); ok {
			return s
		}
	}
	return ""
}

func hmAgentTurn(t *testing.T) (*wu22Harness, *agentLoopRunTurn) {
	t.Helper()
	p := &issue1478Provider{script: func(context.Context, int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: "answer"}, nil
	}}
	h := wu22NewHarness(t, p)
	h.ts.routingSessionID = "helper-top-chat"
	return h, h.responseStage().rq.ri.rf.rt
}

func TestHelperNoticeProducerCarriesBothSessionIdentities(t *testing.T) {
	for _, kind := range []string{"rate_limit", "network", "waiting", "responding", "recovered"} {
		t.Run(kind, func(t *testing.T) {
			h, rt := hmAgentTurn(t)
			switch kind {
			case "rate_limit", "network":
				now := time.Now()
				rt.providerRetryObserver()(providers.RetryInfo{Reason: kind, Provider: "openrouter", Model: "m", Attempt: 2, MaxAttempts: 6, SentAt: now, RetryAt: now.Add(10 * time.Second)})
			default:
				rt.emitProviderStatus(ProviderStatusPayload{State: kind, Retries: 1})
			}
			require.Len(t, h.events, 1, "one event; gateway, not the agent, duplicates it")
			require.Equal(t, h.sid, hmPayloadChild(t, h.events[0].Payload), "FR-001/002: event must retain the helper's own chat as well as its top chat")
			require.Equal(t, h.ts.turnID, h.events[0].Meta.TurnID)
			require.Equal(t, h.ts.agent.ID, h.events[0].Meta.AgentID)
		})
	}
}

func TestHelperOuterRetryCarriesBothSessionIdentities(t *testing.T) {
	for _, cause := range []string{"no_response", "empty_reply"} {
		t.Run(cause, func(t *testing.T) {
			p := &issue1478Provider{script: func(_ context.Context, call int) (*providers.LLMResponse, error) {
				if call == 1 {
					if cause == "empty_reply" {
						return nil, common.ErrEmptyStream
					}
					return nil, context.DeadlineExceeded
				}
				return &providers.LLMResponse{Content: "answer", FinishReason: "stop"}, nil
			}}
			h, rr := issue1664R5Response(t, p, false)
			h.ts.routingSessionID = "helper-top-chat"
			rr.callLLMWithRetries()
			require.NoError(t, rr.rq.ri.rf.err, "SETUP: the next model call succeeds")
			retries := issue1664RetryEvents(h)
			require.Len(t, retries, 1, "one announced outer retry")
			require.Equal(t, h.sid, hmPayloadChild(t, retries[0]), "FR-002: #1664 retry must retain its helper identity for the named top copy")
		})
	}
}

func TestHelperSilenceClearPrecedesRetryProducer(t *testing.T) {
	h, rt := hmAgentTurn(t)
	rt.providerSilence = &providerSilenceWatch{rt: rt, waiting: true, lastActivity: time.Now().Add(-31 * time.Second)}
	rt.providerRetryObserver()(providers.RetryInfo{Reason: "rate_limit", Provider: "p", Model: "m", Attempt: 2, MaxAttempts: 6, SentAt: time.Now(), RetryAt: time.Now().Add(20 * time.Second)})
	require.Len(t, h.events, 2)
	require.Equal(t, EventKindProviderStatus, h.events[0].Kind, "FR-013: silence clear is published BEFORE the new retry")
	clearPayload, ok := h.events[0].Payload.(ProviderStatusPayload)
	require.True(t, ok, "silence clear has the documented in-process status payload")
	require.Equal(t, "responding", clearPayload.State)
	require.Equal(t, EventKindProviderRetry, h.events[1].Kind, "the new countdown must be the final event")
}

func TestHelperNoticesAreNotPersisted(t *testing.T) {
	h, rt := hmAgentTurn(t)
	require.NoError(t, h.store.AppendTranscript(h.sid, session.TranscriptEntry{ID: "hm-normal-saved", Role: "assistant", Content: "Normal saved helper answer.", Timestamp: time.Now().UTC()}))
	original, err := h.store.ReadTranscript(h.sid)
	require.NoError(t, err)
	rt.emitProviderStatus(ProviderStatusPayload{State: "waiting", SilentSince: time.Now().Add(-31 * time.Second)})
	rt.providerRetryObserver()(providers.RetryInfo{Reason: "rate_limit", Attempt: 2, MaxAttempts: 6, SentAt: time.Now(), RetryAt: time.Now().Add(10 * time.Second)})
	rt.noteProviderRecovered(1)
	after, err := h.store.ReadTranscript(h.sid)
	require.NoError(t, err)
	require.Equal(t, original, after, "FR-011: no retry, silence or recovery transcript entry")
	// Real fallback queue/write is the persistence positive control, not an empty store assertion.
	chainResult, _ := runFallbackChain(t, []providers.FallbackCandidate{{Provider: "p", Model: fbNotePrimary}, {Provider: "p", Model: fbNoteFinal}})
	h.ts.queueProviderFallbackNote(chainResult.Model, chainResult.Attempts)
	h.ts.writePendingFallbackNotes()
	entries, err := h.store.ReadTranscript(h.sid)
	require.NoError(t, err)
	var notes []session.TranscriptEntry
	for _, e := range entries {
		if e.Type == session.EntryTypeProviderFallback {
			notes = append(notes, e)
		}
	}
	require.Len(t, notes, 1, "positive control: helper fallback note is saved")
	require.Equal(t, "Answered by the Fallback model (final-x2) because primary-y was unavailable.", notes[0].Content)
	var normal []string
	for _, entry := range entries {
		if entry.ID == "hm-normal-saved" {
			normal = append(normal, entry.Content)
		}
	}
	require.Equal(t, []string{"Normal saved helper answer."}, normal, "FR-011: a real ordinary saved message survives beside the persisted fallback note")
}

func TestHelperChannelRetryContainmentWithRootPositiveControl(t *testing.T) {
	al, launcher, recorder, mb := newBoundaryHarness(t, &alternatingTimeoutProvider{})
	runOrdinaryRootTurn(t, al, "hm-channel", "hello")
	rootCount := 0
	for _, m := range drainOutboundText(mb) {
		if m.Content == "Retrying — please wait..." {
			rootCount++
		}
	}
	require.Equal(t, 1, rootCount, "FR-011: root-channel retry positive control is exactly one notice")
	root := newTestSteeringSession(t, al, "")
	launchAndAwaitSteeredChild(t, al, launcher, root, "hm-contained")
	helperCount := 0
	for _, m := range drainOutboundText(mb) {
		if m.Content == "Retrying — please wait..." {
			helperCount++
		}
	}
	recorder.AssertBoundaryInvoked(steer.BoundaryRetryNotice)
	require.Equal(t, 0, helperCount, "FR-011: helper retry must not reach channels (#1851 stays separate)")
}
