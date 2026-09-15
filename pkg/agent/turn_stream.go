// turn_stream.go: Streamer lifecycle for a turn — start, finalize, flush

package agent

import (
	"context"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

func (ts *turnState) setLastStreamer(s bus.Streamer) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.lastStreamer = s
}

// markLastStreamerProducedModel stamps the model that produced the response
// on the active streamer. The streamer's Finalize writes the assistant
// transcript entry directly (bypassing appendAssistantTranscript); we push
// the model there so the entry carries the per-turn Model field (FR-013).
//
// Uses a type-assertion to an inline interface so bus.Streamer needs no new
// method — non-streaming-transcript streamers (telegram, wecom, sse, manager)
// are untouched; only wsStreamer implements SetProducedModel.
func (ts *turnState) markLastStreamerProducedModel(model string) {
	ts.mu.RLock()
	s := ts.lastStreamer
	ts.mu.RUnlock()
	if pm, ok := s.(interface{ SetProducedModel(model string) }); ok && s != nil {
		pm.SetProducedModel(model)
	}
}

// markLastStreamerTranscriptPersisted tells the active streamer that the agent
// loop has already written this round's narration to the transcript (via
// appendIntermediateAssistantTranscript), so the streamer's own Finalize must
// not write it again. Only the streamer that ends up finalized (the last one)
// matters; marking superseded streamers is harmless (they are never finalized).
//
// Uses a type-assertion to an inline interface so bus.Streamer needs no new
// method — non-streaming-transcript impls (telegram, wecom, sse, manager) are
// untouched; only wsStreamer implements SuppressTranscriptWrite.
func (ts *turnState) markLastStreamerTranscriptPersisted() {
	ts.mu.RLock()
	s := ts.lastStreamer
	ts.mu.RUnlock()
	if sup, ok := s.(interface{ SuppressTranscriptWrite() }); ok {
		sup.SuppressTranscriptWrite()
	}
}

// stampStreamerProducerAgentID stamps the TRUE per-turn producer (ts.agent.ID)
// onto a freshly-obtained streamer, before any token can flow through it.
//
// FIX 5a: without this, a streaming-capable streamer's own "active session
// agent" guess (computed by the channel Manager/WSHandler at GetStreamer
// time, from session metadata) leaks into both the live TokenFrame.AgentId
// and the streamer's own Finalize transcript entry. That guess is correct
// for an ordinary turn, but wrong for a background/delegated sub-turn: per
// ADR-032 (no inheritance from the parent), the delegate runs as its own
// identity, never the parent's, so the session's "active" (parent) agent and
// this specific turn's real producer (ts.agent.ID) can legitimately differ.
//
// Uses a type-assertion to an inline interface so bus.Streamer needs no new
// method — non-webchat streamers (telegram, wecom, sse) are untouched; only
// wsStreamer implements SetProducerAgentID.
func (ts *turnState) stampStreamerProducerAgentID(streamer bus.Streamer) {
	if pas, ok := streamer.(interface{ SetProducerAgentID(agentID string) }); ok && ts.agent != nil {
		pas.SetProducerAgentID(ts.agent.ID)
	}
}

// stampStreamerTurnID stamps this turn's own ID onto a freshly-obtained
// streamer, before any token can flow through it. Mirrors
// stampStreamerProducerAgentID exactly.
//
// FIX 5c/1: without this, the assistant transcript entry Finalize writes
// carries no TurnID at all — confirmed via live verification to break BOTH
// the frontend's turn_canceled -> assistant-message replay correlation
// (chatTurnCanceledNoMatch fires on every reload after a mid-stream cancel)
// and MarkLastEntryTruncated's own turn-scoped backward-walk matching
// (pkg/session/unified.go), silently disabling the Truncated flag for every
// real cancel.
//
// Uses a type-assertion to an inline interface so bus.Streamer needs no new
// method — non-webchat streamers (telegram, wecom, sse) are untouched; only
// wsStreamer implements SetTurnID.
func (ts *turnState) stampStreamerTurnID(streamer bus.Streamer) {
	if tid, ok := streamer.(interface{ SetTurnID(turnID string) }); ok {
		tid.SetTurnID(ts.turnID)
	}
}

// stampStreamerParentSpawnCallID stamps this turn's parentSpawnCallID (empty
// for a root/non-delegated turn) onto a freshly-obtained streamer, before any
// token can flow through it. Mirrors stampStreamerTurnID exactly.
//
// A delegated child sub-turn streams through the SAME wsStreamer/wsConn
// machinery as any other turn (it shares its parent's chatID —
// spawnSubTurn's opts.ChatID: parentTS.chatID), so the assistant-text entry
// wsStreamer.Finalize persists must carry the same ParentSpawnCallID
// correlation that appendIntermediateAssistantTranscript/
// appendAssistantTranscript already stamp for the child's non-streaming
// writes — otherwise a delegate's OWN final streamed response (the common
// case: multi-step delegations stream their last round) would round-trip
// through Finalize with no way for pkg/gateway/replay.go to tell it apart
// from a genuine top-level parent message. See
// session.TranscriptEntry.ParentSpawnCallID's doc comment for the full
// root-cause writeup.
//
// Uses a type-assertion to an inline interface so bus.Streamer needs no new
// method — non-webchat streamers (telegram, wecom, sse) are untouched; only
// wsStreamer implements SetParentSpawnCallID.
func (ts *turnState) stampStreamerParentSpawnCallID(streamer bus.Streamer) {
	if pid, ok := streamer.(interface {
		SetParentSpawnCallID(parentSpawnCallID string)
	}); ok {
		pid.SetParentSpawnCallID(ts.parentSpawnCallID)
	}
}

// streamerStatsSetter is an optional interface a Streamer may implement to
// receive turn-end stats (tokens, cost, duration) before Finalize is called.
// The ws streamer uses this to populate the "done" frame so the chat UI shows
// real token counts and cost instead of zeros (issue #12).
type streamerStatsSetter interface {
	SetTurnStats(tokens int64, costUSD float64, duration time.Duration)
}

// streamerIOStatsSetter is an optional interface a Streamer may implement to
// receive the provider's input/output token split, and the cache split,
// before Finalize is called.
//
// It exists because streamerStatsSetter carries only a COLLAPSED total, and
// the streamer builds its own TranscriptEntry. Without this, a streamed turn —
// which is every ordinary webchat turn — wrote an entry with no split, so
// session stats fell back to booking the whole total as output and tokens_in
// stayed 0. That is the exact defect the split was added to fix; wiring it
// only into the non-streaming path fixed headless runs and left the flagship
// chat surface reporting the same wrong numbers.
type streamerIOStatsSetter interface {
	SetTurnIOStats(promptTokens, completionTokens, cacheReadTokens, cacheWriteTokens int)
}

// streamerFailedSetter is an optional interface a Streamer may implement to
// receive the turn-failed flag before Finalize is called. When implemented,
// finalizeStreamer calls SetTurnFailed(true) whenever the turn ended via the
// engine's error/limit fallback — (1) empty response after retries (engine
// defaultResponse sentinel), (2) tool-iteration limit, (3) generic
// empty-content exhaustion that resolved to the defaultResponse sentinel
// (excluding caller-supplied success DefaultResponse strings such as the
// heartbeat path's "Background task completed."), or (4) any return path that
// left turnStatus == TurnEndStatusError, including the LLM-error early returns.
// The done frame carries DoneStats.TurnFailed=true. CLI/automation clients
// read this field to exit non-zero on a failed turn.
type streamerFailedSetter interface {
	SetTurnFailed(failed bool)
}

// streamerContinuationSetter is an optional interface a Streamer may
// implement to receive the full answer accumulated across this turn's
// ADR-087 D6 truncation-continuation rounds (D6.1, §9 fixed E→C
// interface), overriding whatever the streamer accumulated from its own
// per-call token buffer. Only the LAST streamer is ever finalized (see
// lastStreamer's own doc comment on the turn-scoped-vs-per-call mismatch,
// §2.8), and a per-call streamer's buffer holds only the FINAL
// continuation round's text — without this, a continued answer would
// persist and render only its last segment, silently dropping every
// earlier round even though the live bubble showed the full text as it
// streamed.
type streamerContinuationSetter interface {
	SetContinuationContent(full string)
}

// streamerTruncationSetter is an optional interface a Streamer may implement
// to receive ADR-087 D2's truncation reason before Finalize is called,
// mirroring streamerContinuationSetter's calling convention exactly.
//
// This is the WP C fix for the streamed (webchat) path's D4a/D4b gap: a
// streamer's own Finalize call is the choke point that actually persists the
// assistant transcript entry, so it must stamp Truncated/TruncationReason
// itself, on the SAME write, rather than have finalizeStreamer call
// MarkLastEntryTruncated AFTER Finalize returns. That post-hoc call was
// confirmed live-broken two ways: for a D4a zero-content turn, Finalize's own
// `content != ""` gate wrote no entry at all, so the backward-walk found
// nothing to flag (silent no-op — replay showed no assistant entry, and the
// "(cut off at the output limit)" notice never appeared on reconnect); and
// when an EARLIER same-turn assistant entry existed (this turn's own TurnID,
// written by an earlier tool-calling round via
// appendIntermediateAssistantTranscript), the backward-walk matched and
// mis-stamped THAT completed narration as truncated instead.
type streamerTruncationSetter interface {
	SetTruncation(reason string)
}

// streamOwnershipReleaser is an optional interface a Streamer may implement
// to release a live-stream ownership claim (see gateway's
// WSHandler.streamOwners doc comment) without performing the rest of
// Finalize's work — sending the done frame, persisting the transcript
// entry. finalizeStreamer's B4 abandoned-turn early return deliberately
// skips the full Finalize call so a stuck goroutine cannot send a spurious
// done signal to the frontend, but it must still relinquish any live-stream
// ownership claim the streamer holds: without this, a background delegate
// that became the live owner for a chatID and was later MarkAbandoned()'d
// by cancel.go's PHASE C left that chatID permanently shadowed — no other
// release path runs for an abandoned turn (Finalize never fires; there's no
// TTL or sweep on the gateway side either). Confirmed as a critical,
// unanimous finding across a 7-reviewer gate.
type streamOwnershipReleaser interface {
	ReleaseStreamOwnership()
}

func (ts *turnState) finalizeStreamer(ctx context.Context) {
	// B4: if the turn has been abandoned, suppress the final "done" frame so
	// a stuck goroutine cannot send a spurious done signal to the frontend.
	if ts.abandoned.Load() {
		abandonedWritesSuppressed.Add(1)
		ts.mu.Lock()
		s := ts.lastStreamer
		ts.lastStreamer = nil
		ts.mu.Unlock()
		// Even though the full Finalize (done frame + transcript write) is
		// skipped above, this streamer may already hold a live-stream
		// ownership claim for its chatID — claimed on its first Update()
		// call, before abandonment occurred. Release it here so a later,
		// unrelated turn on the same chatID is not shadowed forever.
		if s != nil {
			if releaser, ok := s.(streamOwnershipReleaser); ok {
				releaser.ReleaseStreamOwnership()
			}
		}
		return
	}
	ts.mu.Lock()
	s := ts.lastStreamer
	tokens := ts.turnTokens
	cost := ts.turnCostUSD
	duration := time.Since(ts.startedAt)
	finalContent := ts.finalContent
	failed := ts.turnFailed
	promptTokens := ts.turnPromptTokens
	completionTokens := ts.turnCompletionTokens
	cacheRead := ts.turnCacheRead
	cacheWrite := ts.turnCacheWrite
	truncReason := ts.truncationReason
	ts.lastStreamer = nil
	ts.mu.Unlock()
	if s != nil {
		if setter, ok := s.(streamerStatsSetter); ok {
			setter.SetTurnStats(tokens, cost, duration)
		}
		if iosetter, ok := s.(streamerIOStatsSetter); ok {
			iosetter.SetTurnIOStats(promptTokens, completionTokens, cacheRead, cacheWrite)
		}
		if fsetter, ok := s.(streamerFailedSetter); ok {
			fsetter.SetTurnFailed(failed)
		}
		// ADR-087 D6.1 (§9 fixed E→C interface): a continued answer's
		// per-call streamer buffer holds only the LAST round's text — hand
		// it the full accumulated answer instead so persistence and the
		// live bubble agree with turnResult.finalContent. Called AFTER
		// ts.mu.Unlock() above — both hadContinuation and
		// continuationAccumulated take ts.mu.RLock() themselves, and
		// sync.RWMutex is not reentrant.
		if cs, ok := s.(streamerContinuationSetter); ok && ts.hadContinuation() {
			cs.SetContinuationContent(ts.continuationAccumulated())
		}
		// ADR-087 D2/D4a/D4b, WP C: stamp the truncation reason on the
		// streamer BEFORE Finalize, mirroring the continuation-content probe
		// immediately above, so Finalize can persist Truncated/
		// TruncationReason on the SAME write that creates (or annotates) the
		// assistant transcript entry — including the D4a zero-content case,
		// which Finalize's own content-gate previously skipped writing an
		// entry for at all. This REPLACES the old post-hoc
		// MarkLastEntryTruncated call that used to run AFTER Finalize
		// returned: on the streamed path that call either found no entry to
		// flag (D4a: Finalize wrote nothing) or, worse, walked back and
		// mis-stamped an EARLIER same-turn narration entry sharing this
		// turn's TurnID (written by an earlier tool-calling round via
		// appendIntermediateAssistantTranscript) as truncated. The
		// non-streaming path (loop.go's own write choke point, gated on
		// !hasActiveStreamer) now has an equivalent single-write fix of its
		// own — appendAssistantTranscriptTruncated — so neither path calls
		// MarkLastEntryTruncated post-hoc anymore; this streamer probe only
		// covers the streamed case.
		if truncReason != "" {
			if tset, ok := s.(streamerTruncationSetter); ok {
				tset.SetTruncation(truncReason)
			}
		}
		// Pass finalContent so the streamer can persist the assistant message
		// even when its own accumulated-from-token buffer is empty — happens
		// when the WS client disconnects mid-stream and every Update() call
		// silently failed against a closed sendCh, leaving the streamer
		// without any tokens to record.
		if err := s.Finalize(ctx, finalContent); err != nil {
			logger.WarnCF("agent", "Turn-end streaming finalize error", map[string]any{"error": err.Error()})
		}
	}
}
