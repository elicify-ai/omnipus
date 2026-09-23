// websocket_streamer.go: The wsStreamer: start, write, finalize

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/google/uuid"
)

// GetStreamer implements bus.StreamDelegate.
//
// ADR-082 D2/FR-003: returns a streamer for EVERY webchat turn that carries a
// session id — including when ZERO connections are currently bound to that
// session (e.g. the only viewer disconnected mid-turn, or a keeper follow-up
// has no viewer at all). This is what keeps the webchat path on ChatStream
// for every LLM round of a turn instead of silently degrading to a
// non-streaming Chat call under the provider's DefaultRequestTimeout the
// moment the originating connection closes (ADR-082 §2 evidence E4).
// sessionID is provided by the caller (agent loop) so the streamer can record
// to the correct transcript without a map reverse-lookup; chatID is kept only
// as an advisory "origin" label (shadow-stream ownership key, markStreamed) —
// it is NOT used to resolve delivery targets any more (see wsStreamer.Update).
func (h *WSHandler) GetStreamer(_ context.Context, channel, chatID, sessionID string) (bus.Streamer, bool) {
	if channel != "webchat" {
		return nil, false
	}
	sid := sessionID
	if sid == "" {
		// Backward-compat fallback for a caller that hasn't yet threaded a
		// session id through explicitly — resolve from this chatID's own
		// current binding.
		h.mu.Lock()
		sid = h.sessionIDs[chatID]
		h.mu.Unlock()
	}
	if sid == "" {
		return nil, false
	}

	// Resolve the agent store for transcript recording.
	agentStore := h.resolveSessionStore(sid)

	// Resolve the active agent for this session so the transcript entry
	// can be tagged with the correct agent ID (FR-002). Key by sessionID.
	//
	// Prefer the handoff override, then fall back to the session's
	// ActiveAgentID from metadata. Without the fallback, assistant entries
	// for un-handed-off sessions get written with AgentID="", which means
	// HydrateAgentHistoryFromTranscript attributes them to "main" instead
	// of the real owning agent — so the next turn's LLM call has only
	// tool_calls/tool_results in its history, no connecting reasoning text,
	// and the agent re-starts the task from scratch.
	activeAgentID := ""
	if aid, ok := h.agentLoop.GetSessionActiveAgent(sid); ok && aid != "" {
		activeAgentID = aid
	} else if agentStore != nil {
		if meta, err := agentStore.GetMeta(sid); err == nil && meta != nil {
			if meta.ActiveAgentID != "" {
				activeAgentID = meta.ActiveAgentID
			} else if meta.AgentID != "" {
				activeAgentID = meta.AgentID
			}
		}
	}

	streamer := &wsStreamer{
		chatID:     chatID,
		sessionID:  sid,
		agentStore: agentStore,
		agentID:    activeAgentID,
		channel:    h.webchatCh,
		// h (not just channel) so connection resolution works even when a
		// test harness never wired webchatCh — see wsHandler()'s doc
		// comment. Always safe: GetStreamer is a *WSHandler method, so h
		// (the receiver) is never nil here.
		h: h,
	}

	// #823: no per-streamer registry any more — what a catch-up needs of
	// the in-flight round (its text so far) is kept by the session hub's
	// active-turn projection, updated by the very publish that numbers each
	// token (ws_hub_projection.go).
	if pending, ok := h.takePendingMessageStatus(sid); ok {
		sendPendingMessageWorking(h, sid, pending)
	}

	return streamer, true
}

// wsTranscriptWriteFailures counts how many times wsStreamer.Finalize's
// streamed-assistant-message audit write via AppendTranscriptStrict failed
// against a session id that does not resolve to a real, store-backed session
// (ADR-057 FR-001/FR-002/W3, spec BDD-03 row `pkg/gateway/websocket.go:4256`
// "streamed assistant"). Mirrors pkg/agent/turn.go's transcriptWriteFailures
// and pkg/tools/handoff.go's handoffTranscriptWriteFailures — each unit that
// owns a converted call site gets its own package-local counter rather than
// sharing one across package boundaries. The write itself stays best-effort
// by design (a failed streamed-transcript record must never fail the turn
// that already streamed successfully to the client); this counter is the
// only durable, operator-visible signal that it happened. Exposed via
// WSTranscriptWriteFailures() for tests and operator tooling.
var wsTranscriptWriteFailures atomic.Uint64

// WSTranscriptWriteFailures returns the current value of the
// omnipus_ws_transcript_write_failures_total counter (ADR-057 FR-002).
func WSTranscriptWriteFailures() uint64 {
	return wsTranscriptWriteFailures.Load()
}

// wsHandler returns the *WSHandler this streamer resolves live connections
// through (ADR-082 D2), or nil for a bare test fixture with neither wired.
// Prefers s.h — set DIRECTLY by GetStreamer to its own receiver — over
// s.channel.wsHandler. This distinction matters: s.channel (*webchatChannel)
// is wired onto WSHandler.webchatCh only by the gateway's production boot
// sequence (gateway.go, AFTER newWSHandler returns), so a test harness that
// constructs a *WSHandler directly (very common — most of this package's
// tests never call the production boot path) leaves it nil even though the
// handler itself is perfectly real and live. Deriving connection resolution
// SOLELY from s.channel.wsHandler would silently degrade every such
// GetStreamer-driven streaming test to the connection-less bare-fixture path
// — no tokens delivered anywhere, a real regression this field prevents. s.h
// is always set by GetStreamer regardless of whether webchatCh happens to be
// wired; s.channel.wsHandler remains the fallback for a caller that
// constructs a wsStreamer literal directly (as many tests in this package
// do) with only `channel` set.
func (s *wsStreamer) wsHandler() *WSHandler {
	if s.h != nil {
		return s.h
	}
	if s.channel != nil {
		return s.channel.wsHandler
	}
	return nil
}

// streamOwnerClaim is the value stored in WSHandler.streamOwners: which turn
// holds a chatID's live-stream slot, and when it claimed it. claimedAt backs
// claimStreamOwnership's stale-claim force-reclaim safety net.
type streamOwnerClaim struct {
	turnID    string
	claimedAt time.Time
}

// streamOwnershipStaleAfter bounds how long an unreleased live-stream
// ownership claim is honored before a new claimant on the same chatID may
// force-reclaim it. Deliberately generous — every real release path
// (Finalize, Cancel, the abandoned-turn early return) frees the claim
// immediately, well within this window — this exists purely as a backstop
// against a future bug in this family leaving a claim permanently
// unreleased, so such a leak degrades to "briefly wrong attribution" rather
// than "permanently mute chat" (see WSHandler.streamOwners' doc comment).
const streamOwnershipStaleAfter = 10 * time.Minute

// claimStreamOwnership attempts to claim (or re-confirm) sessionID's live
// TokenFrame-delivery slot in owners for turnID. See WSHandler.streamOwners'
// doc comment for the full rationale, including why this is keyed by
// sessionID rather than the originating chatID (ADR-082 review F6). Returns
// true when turnID owns the slot — either because it just claimed an empty
// slot, because it already owned it (a single turn typically opens several
// sequential wsStreamer instances across its own tool-calling iterations —
// see turnState.lastStreamer/finalizeStreamer in pkg/agent/turn.go — and
// each must see itself as "still the owner", not a foreign claimant), or
// because the existing claim is older than streamOwnershipStaleAfter and was
// force-reclaimed. Returns false only when a DIFFERENT, still-fresh turnID
// already owns the slot. The generic `owners *sync.Map` / string-key
// signature (unchanged by the sessionID rename) is also exercised directly
// by unit tests with arbitrary string keys — see
// TestClaimStreamOwnership_StaleClaimIsForceReclaimed and its sibling.
func claimStreamOwnership(owners *sync.Map, sessionID, turnID string) bool {
	now := time.Now()
	newClaim := streamOwnerClaim{turnID: turnID, claimedAt: now}
	actual, loaded := owners.LoadOrStore(sessionID, newClaim)
	for {
		if !loaded {
			return true
		}
		claim, ok := actual.(streamOwnerClaim)
		if !ok || claim.turnID == turnID {
			return true
		}
		if now.Sub(claim.claimedAt) < streamOwnershipStaleAfter {
			return false
		}
		// The existing claim is stale — force-reclaim it. CompareAndSwap only
		// succeeds if the entry is still exactly what we last observed, so a
		// concurrent claimant racing us here safely retries instead of both
		// believing they own the slot.
		if owners.CompareAndSwap(sessionID, actual, newClaim) {
			return true
		}
		actual, loaded = owners.Load(sessionID)
	}
}

// releaseStreamOwnershipClaim releases turnID's live-stream ownership claim
// for sessionID in owners, if it currently holds it. A no-op when turnID
// never held the claim (e.g. a shadow stream, or a turn that never called
// Update()) or when it has already been released or force-reclaimed by a
// stale-claim takeover. Load-then-CompareAndDelete rather than a bare delete
// so a concurrent stale-claim reclaim racing this release can never clobber
// a different, newer claimant's entry.
func releaseStreamOwnershipClaim(owners *sync.Map, sessionID, turnID string) {
	if turnID == "" {
		return
	}
	actual, ok := owners.Load(sessionID)
	if !ok {
		return
	}
	claim, ok := actual.(streamOwnerClaim)
	if !ok || claim.turnID != turnID {
		return
	}
	owners.CompareAndDelete(sessionID, actual)
}

// SetProducedModel stamps the model string that produced this streamed
// response. Called by the agent loop before Finalize so the assistant
// transcript entry carries the per-turn Model field (FR-013). Empty model
// is treated as "not recorded" by the UI; the UI omits the model span
// entirely per FR-014 (no placeholder).
func (s *wsStreamer) SetProducedModel(model string) {
	s.statsMu.Lock()
	s.producedModel = strings.TrimSpace(model)
	s.statsMu.Unlock()
}

// SetProducerAgentID overrides the streamer's attributed agent with the TRUE
// per-turn producer (ts.agent.ID). Called by the agent loop (via the inline
// SetProducerAgentID interface, mirroring the SetProducedModel/
// SuppressTranscriptWrite pattern) immediately after obtaining the streamer
// for an LLM streaming call, before any Update/Finalize can observe it.
//
// FIX 5a: without this, both the live TokenFrame.AgentId (Update) and the
// persisted transcript entry (Finalize) fall back to the "active session
// agent" guess computed at GetStreamer time — correct for an ordinary turn,
// but wrong for a background/delegated sub-turn, where ADR-032 guarantees
// the delegate runs as its own identity, never the parent's. A no-op on an
// empty agentID so a caller that genuinely has no resolved agent (should not
// happen in practice) cannot blank out the GetStreamer-time guess.
func (s *wsStreamer) SetProducerAgentID(agentID string) {
	if agentID == "" {
		return
	}
	s.statsMu.Lock()
	s.agentID = agentID
	s.statsMu.Unlock()
}

// SetTurnID stamps the turn ID that will be attributed to the transcript
// entry Finalize writes. Called by the agent loop (via the inline SetTurnID
// interface, mirroring SetProducerAgentID exactly) immediately after
// obtaining the streamer for an LLM streaming call, before any Update/
// Finalize can observe it.
//
// FIX 5c/1: without this, the assistant entry Finalize writes carries no
// TurnID, so a mid-stream cancel's turn_canceled entry (which DOES carry
// TurnID — see pkg/agent/cancel.go) can never be correlated with the
// assistant message it interrupted on replay, and
// MarkLastEntryTruncated's own turn-scoped matching can never find the
// entry to flag. A no-op on an empty turnID so a caller with no resolved
// turn ID (should not happen in practice) cannot blank out an
// already-stamped value.
func (s *wsStreamer) SetTurnID(turnID string) {
	if turnID == "" {
		return
	}
	s.statsMu.Lock()
	s.turnID = turnID
	s.statsMu.Unlock()
}

// SetMessageID stamps the per-round message id (#823 catch-up redesign)
// that Update/Finalize will attach to their TokenFrame/DoneFrame's
// message_id field. Called by the agent loop via the inline
// `interface{ SetMessageID(messageID string) }` (pkg/agent/turn_stream.go's
// stampStreamerMessageID), mirroring SetTurnID's pattern exactly, using the
// SAME id turn_transcript.go's roundMessageIDOrNew persists onto the
// matching transcript entry. A no-op on an empty messageID, like SetTurnID —
// this should not happen in practice (nextRoundMessageID always mints a
// non-empty id), but a no-op is the safer failure mode than blanking out an
// already-stamped value.
func (s *wsStreamer) SetMessageID(messageID string) {
	if messageID == "" {
		return
	}
	s.statsMu.Lock()
	s.messageID = messageID
	s.statsMu.Unlock()
}

// SetParentSpawnCallID stamps the delegation-nesting correlation that will be
// attributed to the transcript entry Finalize writes. Called by the agent
// loop (via the inline SetParentSpawnCallID interface, mirroring SetTurnID
// exactly) immediately after obtaining the streamer for an LLM streaming
// call, before any Update/Finalize can observe it.
//
// Unlike SetTurnID/SetProducerAgentID, an EMPTY parentSpawnCallID is a valid,
// common value — it means "this is a root turn, not a delegation child" —
// so, unlike those two setters, this one does NOT no-op on empty; it always
// stamps whatever the caller passes (including clearing back to "" for a
// caller that resolves no parent span, which should never blank out a
// previously-stamped value in practice since each wsStreamer instance is
// single-use for one turn, but matches the field's own zero-value semantics
// rather than silently refusing a legitimate "no parent" write).
func (s *wsStreamer) SetParentSpawnCallID(parentSpawnCallID string) {
	s.statsMu.Lock()
	s.parentSpawnCallID = parentSpawnCallID
	s.statsMu.Unlock()
}

// SuppressTranscriptWrite marks this streamer so its Finalize skips the
// transcript-append block. The agent loop calls this (via the inline
// SuppressTranscriptWrite interface) after it has already persisted the round's
// narration through appendIntermediateAssistantTranscript (#416 gate fix).
func (s *wsStreamer) SuppressTranscriptWrite() {
	s.statsMu.Lock()
	s.transcriptPersisted = true
	s.statsMu.Unlock()
}

// StreamedContentLen reports how many bytes of streamed content this streamer
// has already emitted to the client for the current attempt. The agent loop's
// inline-retry guard uses this to avoid re-streaming a full response onto a
// partially-streamed bubble after a mid-stream transport drop (which would
// visibly duplicate text in the SPA, since the dropped attempt sent no `done`
// frame). Guarded by statsMu like every other Update/Finalize field.
func (s *wsStreamer) StreamedContentLen() int {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	return s.accumulated.Len()
}

// SetTurnStats is called by the agent loop's finalizeStreamer just before
// Finalize. Implements the streamerStatsSetter interface from pkg/agent.
func (s *wsStreamer) SetTurnStats(tokens int64, costUSD float64, duration time.Duration) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	s.statsTokens = tokens
	s.statsCostUSD = costUSD
	s.statsDuration = duration
}

// SetTurnIOStats receives the provider's input/output and cache token split
// from the agent loop's finalizeStreamer (streamerIOStatsSetter).
//
// SetTurnStats above carries only a collapsed total, which is why a streamed
// turn used to persist an entry with no split at all — leaving tokens_in at 0
// for every webchat session.
func (s *wsStreamer) SetTurnIOStats(promptTokens, completionTokens, cacheReadTokens, cacheWriteTokens int) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	s.statsPromptTokens = promptTokens
	s.statsCompletionTokens = completionTokens
	s.statsCacheRead = cacheReadTokens
	s.statsCacheWrite = cacheWriteTokens
}

// SetTurnFailed is called by the agent loop's finalizeStreamer when the turn
// ended via the engine's error/limit fallback rather than a real model response.
// Conditions that set the flag: (1) LLM returned empty after retries and the
// engine substituted its defaultResponse sentinel; (2) tool-iteration limit
// reached; (3) generic empty-content exhaustion resolved to the defaultResponse
// sentinel (excludes caller-supplied success strings like the heartbeat path).
// Implements the streamerFailedSetter interface from pkg/agent. The flag is
// emitted in the done frame as DoneStats.TurnFailed so CLI/automation clients
// can exit non-zero.
func (s *wsStreamer) SetTurnFailed(failed bool) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	s.statsTurnFailed = failed
}

// SetTruncation stamps ADR-087 D2's truncation reason onto this streamer so
// Finalize can persist Truncated/TruncationReason on the transcript entry it
// writes — the D4a zero-content case and the D4b "annotate the accumulated
// answer" case both route through here. Implements the
// streamerTruncationSetter interface from pkg/agent, called by
// finalizeStreamer's probe (pkg/agent/turn.go) immediately BEFORE Finalize,
// mirroring SetContinuationContent's calling convention exactly. A no-op on
// an empty reason so a caller with nothing to stamp cannot blank out an
// already-set value.
func (s *wsStreamer) SetTruncation(reason string) {
	if reason == "" {
		return
	}
	s.statsMu.Lock()
	s.truncationReason = reason
	s.statsMu.Unlock()
}

// SetContinuationContent stamps the FULL accumulated answer across an
// ADR-087 D6 auto-continuation (turnState.continuationAccum) onto this
// streamer, so Finalize persists the complete prefix+suffix text instead of
// just the text this particular streamer itself accumulated — which, for a
// continuation's own per-call streamer, is only the suffix (ADR-087 §2.8:
// WSHandler.GetStreamer constructs a NEW wsStreamer on every provider call;
// only the last one is finalized, and it never saw the earlier call's text).
//
// Called by the agent loop (via the inline streamerContinuationSetter
// interface probed by turn.go's finalizeStreamer) immediately before
// Finalize, only when the turn had at least one continuation. A no-op on an
// empty string so a caller with nothing to stamp cannot blank out an
// already-set value; a turn with no continuation simply never calls this,
// leaving hasContinuation false and Finalize's existing
// accumulated/finalContent behavior byte-identical to before this method
// existed.
func (s *wsStreamer) SetContinuationContent(full string) {
	if full == "" {
		return
	}
	s.statsMu.Lock()
	s.continuationContent = full
	s.hasContinuation = true
	s.statsMu.Unlock()
}

func (s *wsStreamer) Update(_ context.Context, content string) error {
	s.statsMu.Lock()
	producerAgentID := s.agentID
	// #823 catch-up redesign: captured under the same lock as
	// producerAgentID for TokenFrame.turn_id/message_id below.
	turnID := s.turnID
	messageID := s.messageID
	// Live-stream ownership gate (see WSHandler.streamOwners' doc comment):
	// resolved once, lazily, on this streamer's first Update() call, then
	// reused. A streamer with no turnID (legacy/best-effort caller) or no
	// channel/wsHandler wired (e.g. a bare unit-test fixture) always streams
	// live — the gate degrades to the pre-fix always-live behavior rather
	// than silently withholding content it has no way to attribute.
	//
	// Finding B (A-I4 round 4): a delegated CHILD sub-turn (s.parentSpawnCallID
	// != "", stamped by stampStreamerParentSpawnCallID before any Update/
	// Finalize call can observe it) must NEVER become the live-stream owner
	// for its shared chatID, full stop — never via claimStreamOwnership's
	// normal empty-slot-wins/stale-reclaim paths either. The pre-fix
	// claimStreamOwnership-only check let a background/async child win a
	// vacated ownership claim and start streaming its own raw, hidden-by-
	// design narration live: turnState.finalizeStreamer's B4 abandoned-turn
	// path (pkg/agent/turn.go) calls ReleaseStreamOwnership() on the PARENT's
	// claim the instant the parent is canceled (e.g. user clicks Stop) —
	// but a background delegate is intentionally allowed to keep running
	// past its parent's cancellation (see the Critical:true / "background
	// delegate's final answer lost when parent finishes first" fix), so the
	// child's NEXT streaming round (a fresh wsStreamer instance, its own
	// lazy shadowResolved=false) then finds the slot empty and legitimately
	// "wins" it under the old rule — even though a child's own token stream
	// is supposed to stay hidden unconditionally, exactly like the
	// already-correct sync/await case.
	//
	// [FIX-5, Defect 5, 2026-08-03] This gate's justification is
	// SELF-CONTAINED and does not rest on replay.go: a delegated child's
	// narration must never reach the user as its own top-level chat bubble,
	// live-streamed or replayed. This function enforces the LIVE half of
	// that invariant (withholding the live TokenFrame for a shadow stream);
	// the DURABLE half no longer needs an analogous read-side filter at all
	// (ADR-057 FR-034/FR-038 gave every delegated child its own store-backed
	// session, so its narration never lands in the parent's transcript for
	// replay.go to have to withhold in the first place — see
	// session.TranscriptEntry.ParentSpawnCallID's doc comment,
	// pkg/session/daypartition.go, for that mechanism's retirement). Do NOT
	// read the historical replay.go comparison as this gate's justification
	// and remove this gate on discovering replay.go no longer filters
	// anything — this Update() gate is a DIFFERENT, still-live mechanism
	// (the live-stream ownership claim, not a transcript read filter) with
	// its own reason to exist, stated above. Live-verified: reproduced the
	// leak (a second, delegate-authored top-level bubble with raw narration)
	// via a background delegation canceled mid-flight, confirmed the fix
	// removes it. A root/non-delegated turn is unaffected — this branch only
	// ever narrows behavior for parentSpawnCallID != "".
	if !s.shadowResolved {
		if s.parentSpawnCallID != "" {
			s.isShadowStream = true
		} else if s.turnID != "" && s.sessionID != "" && s.channel != nil && s.channel.wsHandler != nil {
			// ADR-082 review F6: keyed by sessionID, not s.chatID — delivery
			// itself is resolved purely by session (the session hub, #823),
			// so ownership must be too, or two turns with different ORIGIN
			// chatIDs (e.g. a keeper/background turn's internal chatID and the
			// user's live webchat chatID) that both deliver into the SAME
			// session's bound connections would never contend for the slot and
			// could interleave their live tokens into the same viewers.
			s.isShadowStream = !claimStreamOwnership(&s.channel.wsHandler.streamOwners, s.sessionID, s.turnID)
		}
		s.shadowResolved = true
	}
	shadow := s.isShadowStream
	s.statsMu.Unlock()

	// Accumulate this delta for Finalize's transcript write — a shadow
	// (non-owning) stream's content must still be fully captured even though
	// it withholds live frames (see the shadow gate above).
	s.statsMu.Lock()
	s.accumulated.WriteString(content)
	s.statsMu.Unlock()

	if shadow {
		// A DIFFERENT, still-live turn already owns live TokenFrame delivery
		// to this session. This stream's content is fully captured above and
		// will be persisted correctly by Finalize — withholding the live frame
		// (and never numbering it, BE-DESIGN.md §7) is what prevents two
		// concurrent streams from interleaving their deltas into one message.
		return nil
	}

	frame := generated.TokenFrame{
		Type:      string(generated.WsFrameTypeToken),
		Content:   content,
		SessionId: s.sessionID,
	}
	// FIX 5a: attribute the frame to the turn's TRUE producer (stamped via
	// SetProducerAgentID) rather than leaving the client to guess based on
	// whichever agent it happens to be actively chatting with — wrong for
	// background/delegated sub-turns.
	if producerAgentID != "" {
		frame.AgentId = &producerAgentID
	}
	// #823 catch-up redesign (BE-DESIGN.md §6.3): stamp turn_id/message_id
	// so a client appends this token to the right bubble by id rather than
	// "whatever bubble is currently open" — required for catch-up to resume
	// the correct bubble after a gap. Absent when unset (a bare test
	// fixture, or a caller that never went through the agent loop's stamps).
	if turnID != "" {
		frame.TurnId = &turnID
	}
	if messageID != "" {
		frame.MessageId = &messageID
	}
	data, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("ws: marshal token frame: %w", err)
	}
	// #823 (BE-DESIGN.md §1.1/§1.2): every token is published ONCE through
	// the session's hub — numbered, journaled, added to the active-turn
	// projection, and appended to the ordered queue of every connection
	// bound to the session, whether zero, one or many tabs are attached. No
	// connection's slowness can block this call (the queues never block)
	// and none can lose a token (they never drop; a too-far-behind tab is
	// closed with 4008 and catches up).
	if h := s.wsHandler(); h != nil && s.sessionID != "" {
		h.hubPublishMetaAlsoTo(s.sessionID, string(generated.WsFrameTypeToken), hubFrameMeta{
			kind:      hubKindToken,
			turnID:    turnID,
			messageID: messageID,
			agentID:   producerAgentID,
			content:   content,
		}, data, nil)
	}
	return nil
}

// wsStreamerFinalize carries the shared state of Finalize across its stages.
type wsStreamerFinalize struct {
	s                          *wsStreamer
	finalContent               string
	tokensF                    float64
	costF                      float64
	promptTokensF              int
	completionTokensF          int
	cacheReadF                 int
	cacheWriteF                int
	durF                       float64
	transcriptAlreadyPersisted bool
	producedModel              string
	turnFailed                 bool
	continuationContent        string
	hasContinuation            bool
	truncationReason           string
	producerAgentID            string
	turnID                     string
	messageID                  string
	parentSpawnCallID          string
	shadow                     bool
	// accumulated is the streamer's own text, read once under statsMu.
	accumulated string
}

func (s *wsStreamer) Finalize(_ context.Context, finalContent string) error {
	wsf := &wsStreamerFinalize{s: s, finalContent: finalContent}

	wsf.prepareFinalize()

	// Release this turn's live-stream ownership claim (if held) so a
	// different, still-running turn on the same chatID can become the live
	// owner (see WSHandler.streamOwners' doc comment). Finalize is the
	// normal, once-per-turn release point via turnState's deferred
	// finalizeStreamer (pkg/agent/turn.go) — safe/no-op when this stream was
	// never the owner (a shadow stream) or never claimed at all (e.g. an
	// immediate tool-only round with no narration text, so Update() was
	// never called). See ReleaseStreamOwnership for the other release
	// points (Cancel, and finalizeStreamer's B4 abandoned-turn path, which
	// deliberately skips the rest of this method).
	wsf.s.releaseOwnershipClaim()

	// #823 catch-up redesign, BE-DESIGN.md §4.3 — REQUIRED order: persist
	// THEN publish "done". This gives the invariant "done(T) has seq <= W ⇒
	// T's final transcript entry is in any transcript read taken after the
	// bind", which is what lets the hub's active-turn projection forget the
	// turn at done without leaving a hole in a later snapshot.
	// persistTranscript is best-effort by design (a failed write must never
	// suppress the done frame the client is waiting on — see
	// wsTranscriptWriteFailures' doc comment), so its error is returned
	// AFTER the done still went out, never used to skip it.
	persistErr := wsf.persistTranscript()
	wsf.publishDone()
	return persistErr
}

// publishDone publishes this turn's single done frame through the session
// hub (numbered, journaled, delivered byte-identically to every bound tab —
// BE-DESIGN.md §5: done no longer varies per connection now that nothing is
// ever dropped, so DoneStats.TokensDropped is never set). A shadow stream (a
// delegated child, or a non-owning concurrent turn) publishes nothing: its
// text was never shown live, and a done would close the OWNING turn's bubble.
func (wsf *wsStreamerFinalize) publishDone() {
	if wsf.shadow {
		return
	}
	h := wsf.s.wsHandler()
	if h != nil && wsf.s.sessionID != "" {
		// Review finding 12: "treat a done as implying working" + "clear or
		// expire pending entries at turn end" — flush BEFORE the done itself,
		// so any client-message tick never consumed by a GetStreamer call
		// still reaches "working", and cannot survive to be popped by a
		// LATER, unrelated turn on this session.
		h.flushPendingMessageStatusesAsWorking(wsf.s.sessionID)

		stats := &generated.DoneStats{
			Tokens:     &wsf.tokensF,
			Cost:       &wsf.costF,
			DurationMs: &wsf.durF,
		}
		if wsf.turnFailed {
			tf := wsf.turnFailed
			stats.TurnFailed = &tf
		}
		// ADR-087 D2 (finding #10): mirror the truncation annotation onto the
		// live done frame too, so a turn cut off while the user is watching
		// renders the "(cut off at the output limit)" notice immediately.
		if wsf.truncationReason != "" {
			truncatedCopy := true
			stats.Truncated = &truncatedCopy
			reasonCopy := wsf.truncationReason
			stats.TruncationReason = &reasonCopy
		}
		frame := generated.DoneFrame{
			Type:      string(generated.WsFrameTypeDone),
			SessionId: wsf.s.sessionID,
			Stats:     stats,
		}
		if wsf.turnID != "" {
			turnID := wsf.turnID
			frame.TurnId = &turnID
		}
		if wsf.messageID != "" {
			messageID := wsf.messageID
			frame.MessageId = &messageID
		}
		data, err := json.Marshal(frame)
		if err != nil {
			slog.Error("ws: marshal done frame failed", "session_id", wsf.s.sessionID, "error", err)
		} else {
			h.hubPublishMetaAlsoTo(wsf.s.sessionID, string(generated.WsFrameTypeDone),
				hubFrameMeta{kind: hubKindDone, turnID: wsf.turnID}, data, nil)
		}
	}
	// Only mark as streamed if we actually sent content. If the LLM failed
	// before producing any tokens, let the outbound Send path deliver the
	// error message — otherwise the user sees a stuck "thinking" spinner.
	if wsf.s.channel != nil && wsf.accumulated != "" {
		wsf.s.channel.markStreamed(wsf.s.chatID)
	}
}

// prepareFinalize snapshots turn statistics and resolves whether this streamer is shadowed.
func (wsf *wsStreamerFinalize) prepareFinalize() {
	// Include turn-level token/cost/duration if the agent loop pushed them via
	// SetTurnStats before this call (issue #12). Zero values are still emitted
	// so the client can reset the session counters for turns with no LLM usage.
	wsf.s.statsMu.Lock()
	wsf.tokensF = float64(wsf.s.statsTokens)
	wsf.costF = wsf.s.statsCostUSD
	// Read the split under the same lock as the total, so the entry cannot
	// carry a total from one turn and a split from another.
	wsf.promptTokensF = wsf.s.statsPromptTokens
	wsf.completionTokensF = wsf.s.statsCompletionTokens
	wsf.cacheReadF = wsf.s.statsCacheRead
	wsf.cacheWriteF = wsf.s.statsCacheWrite
	wsf.durF = float64(wsf.s.statsDuration.Milliseconds())
	wsf.transcriptAlreadyPersisted = wsf.s.transcriptPersisted
	wsf.producedModel = wsf.s.producedModel
	wsf.turnFailed = wsf.s.statsTurnFailed
	wsf.continuationContent = wsf.s.continuationContent
	wsf.hasContinuation = wsf.s.hasContinuation
	// ADR-087 D2/D4a/D4b, WP C: read under statsMu, same pattern as
	// continuationContent — SetTruncation (called by the agent loop's
	// finalizeStreamer immediately before Finalize) and Finalize (turn end)
	// may run on different goroutines in principle even though in practice
	// they are sequenced back-to-back by finalizeStreamer itself.
	wsf.truncationReason = wsf.s.truncationReason
	// FIX 5a/5c: read under statsMu — SetProducerAgentID/SetTurnID (called by
	// the agent loop at streaming-call start) may run on a different
	// goroutine than Finalize (called at turn end).
	wsf.producerAgentID = wsf.s.agentID
	wsf.turnID = wsf.s.turnID
	wsf.messageID = wsf.s.messageID
	wsf.parentSpawnCallID = wsf.s.parentSpawnCallID
	// A-I4 round 4 / Finding A: resolve the live-stream shadow gate HERE too,
	// not just in Update(). Every turn — root OR a delegated child sub-turn —
	// runs through the exact same pkg/agent/loop.go runTurn/finalizeStreamer
	// path (spawnSubTurn calls al.runTurn(childCtx, childTS) directly, see
	// subturn.go), so a child's own Finalize fires — sending an UNCONDITIONAL
	// "done" WS frame to the CHATID IT SHARES WITH ITS PARENT — the instant
	// the child's own sub-turn completes, even while the parent's own turn is
	// still actively streaming (the common case for a synchronous/"await"
	// delegate call, which blocks the parent mid-turn while the child runs).
	// DoneFrame carries no turn/parent discriminator, so the client's `done`
	// handler (src/store/chat.ts) has no way to tell "a nested child
	// finished" from "the outer turn finished" — it unconditionally closes
	// whichever bubble is current, so the parent's still-open bubble is
	// finalized prematurely and every following narration segment opens a
	// brand-new bubble. Live-verified via a real 3-round synchronous
	// delegation: a `done` frame lands right after each child's
	// subagent_start (duration_ms matching that child's own subagent_end),
	// well before the parent's real, final `done` — producing N+1 bubbles for
	// N sequential delegate calls instead of one continuous bubble matching
	// the child's own hidden-narration invariant (this same Update() shadow
	// gate already treats a child's own text as never visible — Finalize
	// simply never enforced that for the "done" signal it also sends).
	//
	// [FIX-5, Defect 5, 2026-08-03] The invariant this paragraph enforces —
	// "a delegated child's own narration/signals are never visible as their
	// own top-level chat event" — no longer needs replay.go as a supporting
	// citation: that read-side skip was DELETED (ADR-057 FR-034/FR-038; see
	// session.TranscriptEntry.ParentSpawnCallID's doc comment,
	// pkg/session/daypartition.go, for why — a delegated child now owns its
	// own store-backed session, so there is nothing left in the PARENT's
	// transcript for a read boundary to withhold). This Finalize() gate and
	// Update()'s shadow-stream gate are BOTH still live, LIVE-side
	// mechanisms (withholding the "done" frame / the live TokenFrame,
	// respectively) — each independently justified by the invariant stated
	// above, not by the retired replay.go mechanism.
	//
	// Mirrors Update()'s lazy resolution exactly, including the same "a
	// delegated child NEVER owns the live slot" rule Update() gained for
	// Finding B below — a streamer whose Update() was never called (e.g. an
	// immediate tool-only round with no narration text) would otherwise
	// reach Finalize with shadowResolved still false and default to
	// isShadowStream=false (treated as live), which is wrong for a child
	// sub-turn that happened to stream zero tokens of its own.
	if !wsf.s.shadowResolved {
		if wsf.parentSpawnCallID != "" {
			wsf.s.isShadowStream = true
		} else if wsf.turnID != "" && wsf.s.sessionID != "" && wsf.s.channel != nil && wsf.s.channel.wsHandler != nil {
			// ADR-082 review F6: keyed by sessionID — see Update's identical gate.
			wsf.s.isShadowStream = !claimStreamOwnership(&wsf.s.channel.wsHandler.streamOwners, wsf.s.sessionID, wsf.turnID)
		}
		wsf.s.shadowResolved = true
	}
	wsf.shadow = wsf.s.isShadowStream
	wsf.accumulated = wsf.s.accumulated.String()
	wsf.s.statsMu.Unlock()
}

// persistTranscript records the completed assistant response when the round was not already persisted.
func (wsf *wsStreamerFinalize) persistTranscript() error {
	// Record the full assistant response to the session transcript — unless the
	// agent loop already persisted this round's narration via
	// appendIntermediateAssistantTranscript (#416 gate fix). This happens when
	// the turn exits via max_tool_iterations exhaustion: the last executed round
	// is a tool-call round whose streamer (this one) becomes the lastStreamer.
	// Writing here too would duplicate the assistant bubble on replay. We still
	// sent the done frame, fan-out, and markStreamed above — only the append is
	// suppressed.
	if wsf.s.agentStore != nil && wsf.s.sessionID != "" && !wsf.transcriptAlreadyPersisted {
		content := wsf.accumulated
		if wsf.hasContinuation {
			// ADR-087 D6.1/§2.8: this streamer is per PROVIDER CALL, not per
			// turn — its own `accumulated` buffer (and finalContent, which
			// for a continuation's per-call streamer would also just be the
			// suffix) holds only the LAST call's text. continuationContent
			// carries the full prefix+suffix answer the live bubble showed;
			// persist THAT, not the buffer.
			content = wsf.continuationContent
		} else if content == "" && wsf.finalContent != "" {
			// Fallback: when accumulated is empty (every Update() call silently
			// failed because the client WS was already closed), use the
			// finalContent the agent loop passed in. Without this fallback,
			// disconnected mid-stream turns would leave no assistant entry in
			// transcript.jsonl and the user sees nothing on reconnect/replay.
			content = wsf.finalContent
		}
		// ADR-087 D2/D4a/D4b: a truncated turn must still get an entry even
		// when content is empty — D4a is the deliberate "cut off before any
		// text was produced" outcome, not a fallthrough with nothing to
		// write. Without the `|| truncationReason != ""` arm, a zero-content
		// truncated turn wrote NO entry at all on the streamed path: replay
		// showed no assistant message (the "(cut off at the output limit)"
		// notice never appeared), and finalizeStreamer's old post-hoc
		// MarkLastEntryTruncated call — removed now that this write does the
		// stamping directly — either silently no-op'd (no entry to find) or,
		// worse, walked back and mis-stamped an EARLIER same-turn narration
		// entry as truncated. See truncationReason's own field doc comment.
		if content != "" || wsf.truncationReason != "" {
			// #823 BE-DESIGN.md §6.3: persist the final streamed answer under
			// the SAME id its live token/done frames carried (message_id),
			// exactly as appendIntermediateAssistantTranscript already does for
			// intermediate rounds — so replay_message.id equals the live
			// message_id and a client (or the snapshot filter) can tell the
			// persisted copy and the live copy are one message. A streamer the
			// agent loop never stamped (a bare fixture, the Send fallback)
			// keeps a fresh id.
			entryID := wsf.messageID
			if entryID == "" {
				entryID = uuid.New().String()
			}
			entry := session.TranscriptEntry{
				ID:      entryID,
				Role:    "assistant",
				AgentID: wsf.producerAgentID,
				// TurnID (FIX 5c/1): stamped via SetTurnID so a mid-stream
				// cancel's turn_canceled entry can be correlated with THIS
				// entry on replay.
				TurnID:    wsf.turnID,
				Content:   content,
				Timestamp: time.Now().UTC(),
				Tokens:    int(wsf.tokensF),
				Cost:      wsf.costF,
				Model:     wsf.producedModel,
				// The provider's token split. Without these four fields the
				// session-stats aggregator sees no split and falls back to
				// booking the whole turn total as output, which is how every
				// webchat session came to report tokens_in: 0.
				PromptTokens:     wsf.promptTokensF,
				CompletionTokens: wsf.completionTokensF,
				CacheReadTokens:  wsf.cacheReadF,
				CacheWriteTokens: wsf.cacheWriteF,
				// ParentSpawnCallID: stamped via SetParentSpawnCallID so a
				// delegation child sub-turn's own streamed narration/final
				// response carries the same nesting correlation its
				// non-streaming siblings (appendIntermediateAssistantTranscript
				// / appendAssistantTranscript) already stamp — see
				// session.TranscriptEntry.ParentSpawnCallID's doc comment.
				// Empty (the common case) for a root turn.
				ParentSpawnCallID: wsf.parentSpawnCallID,
			}
			// ADR-087 D2/D4a/D4b, WP C: stamp Truncated/TruncationReason in
			// THIS SAME WRITE — whether content is empty (D4a) or non-empty
			// (D4b, an auto-continue-exhausted/ineligible accumulated
			// answer) — instead of a separate post-hoc
			// MarkLastEntryTruncated call after Finalize returns. See
			// truncationReason's own field doc comment for why the post-hoc
			// call was the bug.
			if wsf.truncationReason != "" {
				entry.Truncated = true
				entry.TruncationReason = wsf.truncationReason
			}
			// ADR-057 FR-001/FR-002 (W3): AppendTranscriptStrict refuses loudly
			// (and creates nothing on disk) when s.sessionID does not resolve to
			// a real, store-backed session, instead of AppendTranscript's old
			// lenient silent-create branch. The error was already checked here
			// before this conversion — only the runtime behavior of a failure
			// changes (loud vs. silently minting an orphan session directory) —
			// so surface it as a counter increment (BDD-03) alongside the
			// pre-existing WARN.
			if err := wsf.s.agentStore.AppendTranscriptStrict(wsf.s.sessionID, entry); err != nil {
				wsTranscriptWriteFailures.Add(1)
				slog.Warn("ws: could not record streamed assistant message", "session_id", wsf.s.sessionID, "error", err)
			}
		}
	}
	return nil
}

func (s *wsStreamer) Cancel(_ context.Context) {
	// Defensive symmetry with Finalize's release: Cancel is not on the
	// agent loop's normal per-turn path (finalizeStreamer always calls
	// Finalize, never Cancel — see turn.go), but release the ownership
	// claim here too so a future caller of Cancel cannot leak the chatID's
	// live-stream slot forever.
	//
	// ADR-082 D2: this streamer no longer pins a single *wsConn, so there is
	// no longer "the" connection to force-close here — a session-bound
	// streamer's turn ending is not a reason to disconnect every (or any)
	// viewer's WebSocket. Cancel has no production call site today (see
	// ReleaseStreamOwnership's doc comment); this stays a no-op release for
	// defensive symmetry only.
	s.ReleaseStreamOwnership()
}

// ReleaseStreamOwnership releases this streamer's live-stream ownership claim
// for its sessionID (if held), allowing a different, still-running turn on
// the same session to become the live owner (see WSHandler.streamOwners' doc
// comment; keyed by sessionID, not chatID — ADR-082 review F6). Safe to call
// multiple times, concurrently, or when the claim was never held —
// releaseStreamOwnershipClaim only deletes an entry that still matches this
// exact turnID.
//
// This is the single implementation shared by Finalize, Cancel, and — via
// the streamOwnershipReleaser optional interface pkg/agent's finalizeStreamer
// type-asserts for — the B4 abandoned-turn early return (pkg/agent/turn.go).
// That early return deliberately skips the rest of Finalize (no done frame,
// no transcript write, so a stuck goroutine cannot send a spurious signal to
// the frontend) but was found, in a 7-reviewer gate, to also skip releasing
// the ownership claim: a background delegate that became the live owner for
// a session and was later MarkAbandoned()'d by cancel.go's PHASE C left that
// session permanently shadowed, since Finalize (the only other release
// point; Cancel has no production call sites) never ran. Exported so
// pkg/agent can reach it through bus.Streamer's optional-interface pattern
// without either package importing the other's concrete type.
func (s *wsStreamer) ReleaseStreamOwnership() {
	s.releaseOwnershipClaim()
	// [ADR-082 review CR4/F2, re-expressed for #823] This exported path is
	// the B4 abandoned-turn early return (and the defensive Cancel): the
	// turn will never send a done and never persist this round's text, so
	// the session hub must forget the text too — otherwise every later
	// snapshot would show a phantom message that never finishes. Finalize
	// does NOT come through here (it releases the claim only, then persists
	// and publishes done, which clears the projection in order).
	s.statsMu.Lock()
	turnID := s.turnID
	s.statsMu.Unlock()
	if h := s.wsHandler(); h != nil && h.hubs != nil && s.sessionID != "" {
		if hub := h.hubs.lookup(s.sessionID); hub != nil {
			hub.forgetTurnText(turnID)
		}
	}
}

// releaseOwnershipClaim releases this streamer's live-stream ownership claim
// for its sessionID, if held (safe to call repeatedly or when never held).
func (s *wsStreamer) releaseOwnershipClaim() {
	s.statsMu.Lock()
	turnID := s.turnID
	s.statsMu.Unlock()
	h := s.wsHandler()
	if h == nil {
		return
	}
	if s.sessionID != "" {
		releaseStreamOwnershipClaim(&h.streamOwners, s.sessionID, turnID)
	}
}
