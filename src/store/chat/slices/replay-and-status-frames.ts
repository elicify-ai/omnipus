// replay-and-status-frames.ts: replay, media, status, and integration frame reduction



import type { StoreApi } from 'zustand'
import { produce } from 'immer'
import { generateId } from '@/lib/constants'
import { useUiStore } from '@/store/ui'
import { queryClient } from '@/lib/queryClient'
import type {
  WsReplayMessageFrame,
  WsRateLimitFrame,
} from '@/lib/ws'
import type {
  WhatsAppPairingFrame,
  KnowledgeIndexProgressFrame,
  NotificationFrame,
  GoalStatusFrame,
  LoopStatusFrame,
  JudgeVerdictFrame,
  PlanStatusFrame,
  AskUserQuestionFrame,
  SessionStateFrame,
  BrowserHandoverNoticeFrame,
  GoalOutcomeFrame,
} from '@/lib/api/generated/asyncapi-types'
import { useJudgeActivityStore } from '@/store/judgeActivity'
import { useWhatsAppPairingStore } from '@/store/whatsappPairing'
import { useKnowledgeIndexStore } from '@/store/knowledgeIndex'
import { useNotificationsStore } from '@/store/notifications'
import { useToolApprovalStore } from '@/store/toolApproval'
import { reconcilePendingAsks } from '@/store/pendingAskReconcile'
import { logDiagnostic } from '@/lib/telemetry'
import { normalizeTruncationReason } from '@/lib/truncation'
import { buildGoalOutcomeInsertion } from '@/lib/goalOutcome'
import { buildJudgeVerdictInsertion } from '@/lib/judgeVerdictThread'
import {
  getLLMErrorDisplay,
  readEntryIdFromFrame,
  readLLMErrorFromReplayFrame,
} from '@/lib/llm-error'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { buildBrowserHandoverInsertion, buildGoalAckInsertion, evictGoalPillsOverCap, mergeGoalPillFrame } from '../goals'
import { MAX_MESSAGES_PER_SESSION, evictMessageFromBucket, findAssistantMessageIdByTurnId, findLastAssistantMessageId, findOpenAssistantMessageId, getMessages } from '../messages'
import { isTurnFinished, schedulePlanStatusInvalidate } from '../routing'
import { sawReplayMessageThisTurn } from '../runtime-state'
import { applyMessageArray, bakeToolCallsByOwner, stampToolCallOffset } from '../session'
import type { ChatMessage, ChatStore, MediaAttachment, PositionedToolCall, RateLimitEventData, SessionChatState } from '../types'



type Frame = Parameters<ChatStore['handleFrame']>[0]
interface ReplayAndStatusFrameContext {
  frame: Frame
  targetSid: string | null
  get: StoreApi<ChatStore>['getState']
  withBucket: (sid: string | null, updater: (bucket: SessionChatState) => Partial<SessionChatState>) => void
  armRateLimitClear: (sid: string, event: RateLimitEventData) => void
}

export function handleReplayAndStatusFrame({ frame, targetSid, get, withBucket, armRateLimitClear }: ReplayAndStatusFrameContext): boolean {
  switch (frame.type) {
        case 'replay_error': {
          // ADR-051 — historical (replay) error frame. Mirrors the live
          // `case 'error'` translation path but with two replay-specific
          // twists: (1) the typed payload has NO `detail` field (the gateway
          // strips provider internals before persisting to transcript.jsonl),
          // so the "Technical details" disclosure will never mount on replay
          // — `errorCode` is still stamped for code-based styling though;
          // (2) coalesce into the trailing streaming assistant bubble when
          // one exists (a partial assistant reply followed by the error in
          // the same turn is one logical unit), else push a fresh error
          // bubble. The live `case 'error'` did not coalesce because a live
          // streaming bubble is closed by the C8 union sweep above; on
          // replay, bubbles are created finalized (isStreaming:false) so
          // that sweep is a no-op and we need to explicitly coalesce.
          if (!targetSid) break
          const replayEntryId = readEntryIdFromFrame(frame)
          const replayLlmError = readLLMErrorFromReplayFrame(frame)
          const replayVerbose = useChatPreferencesStore.getState().verboseChatEnabled
          const replayDisplay = replayLlmError
            ? getLLMErrorDisplay(replayLlmError, replayVerbose).message
            : (frame.message ?? '')
          withBucket(targetSid, (b) => {
            // Dedup: if a bubble carrying this entry_id is already in the
            // bucket (live path stamped it on the same error, or a
            // reconnect re-replayed the same window), no-op. This is the
            // symmetric half of the live `case 'error'` dedup.
            if (replayEntryId) {
              const alreadyRendered = getMessages(b).some(
                (m) => m.errorEntryId === replayEntryId,
              )
              if (alreadyRendered) return {}
            }
            // Coalesce into the trailing streaming assistant bubble when
            // one exists. The live path closes the bubble via the C8 union
            // sweep (it iterates isStreaming/status==='streaming'); on
            // replay, bubbles are created finalized, so find the trailing
            // assistant message explicitly and stamp it. Same content rule
            // as the live path: keep existing streamed narration, only use
            // the translated copy when the bubble is empty.
            const lastAssistantId = findLastAssistantMessageId(b.messageOrder, b.messagesById)
            if (lastAssistantId) {
              const lastMsg = b.messagesById[lastAssistantId]
              // Only coalesce when the trailing assistant bubble was itself
              // part of THIS error turn — i.e. it's currently marked as
              // 'streaming' (live-replay hybrid: bubble carried over from
              // the live side and not yet closed) OR has empty/blank content
              // (a placeholder that never received tokens before the error
              // fired). Otherwise push a fresh error bubble (the trailing
              // assistant message belongs to a PRIOR, healthy turn).
              const lastContentEmpty = !lastMsg.content || !lastMsg.content.trim()
              if (lastMsg.isStreaming || lastMsg.status === 'streaming' || lastContentEmpty) {
                return produce(b, (draft) => {
                  const m = draft.messagesById[lastAssistantId]
                  if (!m) return
                  if (lastContentEmpty) m.content = replayDisplay
                  m.isStreaming = false
                  m.status = 'error'
                  m.pendingTextBoundary = false
                  if (replayLlmError) {
                    m.errorCode = replayLlmError.code
                    if (replayEntryId) m.errorEntryId = replayEntryId
                  }
                }) as Partial<SessionChatState>
              }
            }
            // No trailing assistant bubble to coalesce into — push a fresh
            // error bubble. Matches the live path's errMsg shape (minus the
            // verbose detail, which the replay payload never carries).
            const replayErrMsg: ChatMessage = {
              id: generateId(),
              role: 'assistant',
              content: replayDisplay,
              timestamp: new Date().toISOString(),
              status: 'error',
              isStreaming: false,
              ...(replayLlmError
                ? {
                    errorCode: replayLlmError.code,
                    ...(replayEntryId ? { errorEntryId: replayEntryId } : {}),
                  }
                : {}),
            }
            const replayMsgs = [...getMessages(b), replayErrMsg]
            return applyMessageArray(replayMsgs, b)
          })
          break
        }

        case 'replay_message': {
          if (!targetSid) break
          sawReplayMessageThisTurn[targetSid] = true
          const replayFrame = frame as WsReplayMessageFrame
          // FR-16 / Fix 5c: turn_canceled entries are metadata-only and must
          // never render as their own chat bubble. ReplayMessageFrame carries
          // no status/truncated field, so — unlike a fresh REST cold-load,
          // where the persisted TranscriptEntry.Status already says
          // "interrupted" — this WS-replay path only learns a turn was
          // cancelled from this separate turn_canceled entry, correlated by
          // TurnId to the specific assistant entry it interrupted (both
          // stamped from TranscriptEntry.TurnID by pkg/gateway/replay.go).
          // Find that message via turnId (captured below whenever an
          // assistant replay_message frame carries one) and mark it
          // interrupted the same way the live-cancel path
          // (markLastMessageInterrupted) does, so reload and live rendering
          // match — that parity is the entire point of this fix.
          if (replayFrame.role === 'turn_canceled') {
            const canceledTurnId = replayFrame.turn_id
            if (!canceledTurnId) {
              // Legacy/undecorated cancellation entry (no turn_id) — nothing
              // to correlate against. Drop gracefully rather than guessing at
              // "last assistant message", which could mis-mark an unrelated
              // turn when async delegation has interleaved other frames.
              console.warn('chat.turn_canceled_missing_turn_id', { sessionId: targetSid })
              logDiagnostic('chatTurnCanceledMissingTurnId', { sessionId: targetSid })
              break
            }
            withBucket(targetSid, (b) => {
              return produce(b, (draft) => {
                const matchId = findAssistantMessageIdByTurnId(draft.messageOrder, draft.messagesById, canceledTurnId)
                if (!matchId) {
                  // No assistant message in this bucket carries this turnId
                  // (e.g. evicted from the ring buffer, or replay delivered the
                  // cancellation before its assistant entry). No-op — never
                  // guess which message to mark.
                  //
                  // Finding D (A-I4 round 4): live-verified this is the EXPECTED,
                  // benign shape for the common case — a turn canceled before it
                  // streamed any narration text at all (e.g. Stop clicked while
                  // still on the first LLM call of a round, right after a
                  // background delegate() dispatch). pkg/gateway/replay.go only
                  // ever emits a replay_message frame — the ONLY frame type that
                  // stamps `.turnId` onto a ChatMessage — when
                  // TranscriptEntry.Content is non-empty; tool_call_start/
                  // subagent_start (which DO still replay correctly, since they
                  // key off entry.ToolCalls independent of Content) carry no
                  // turn_id field on the wire at all. So a turn with empty
                  // narration legitimately has NO bubble anywhere carrying its
                  // turnId for this correlation to find — not a bug, just a gap
                  // in what there is to correlate against. Confirmed this has NO
                  // user-visible effect: the interrupted delegation still renders
                  // correctly (SubagentBlock reads its OWN status field, set
                  // independently via subagent_end/tool_call_result, never via
                  // this turnId match) — reproduced via a real background
                  // delegation canceled mid-dispatch, reloaded twice, span showed
                  // "interrupted" correctly both times despite this no-op firing
                  // both times too. Kept as a dev-visible console.warn (unchanged
                  // behavior) but deliberately NOT escalated to production
                  // error-telemetry (logDiagnostic) — that tier is for
                  // conditions with observable impact, and this one has none.
                  // The ring-buffer-eviction case this branch also covers is not
                  // meaningfully more severe: the underlying data is safely in
                  // transcript.jsonl regardless, only this session's bounded
                  // in-memory window lost the (also-cosmetic) correlation.
                  console.warn('chat.turn_canceled_no_match', { sessionId: targetSid, turnId: canceledTurnId })
                  return
                }
                const m = draft.messagesById[matchId]
                if (m) { m.isStreaming = false; m.status = 'interrupted'; m.pendingTextBoundary = false }
              }) as Partial<SessionChatState>
            })
            break
          }
          // Widened from 'user' | 'assistant' — the 'turn_canceled'
          // branch above always `break`s, so by this point
          // `replayFrame.role` can only be 'user' | 'assistant' | 'system'
          // | undefined (the wire type's fourth member is excluded by
          // control flow). The old, narrower cast silently dropped 'system'
          // even though `Message`/`ChatMessage` (and every downstream
          // consumer keyed off `role`) fully supports it — this is exactly
          // the shape the kickoff's own SYSTEM-role transcript entry
          // replays as.
          const role = (replayFrame.role || 'assistant') as 'user' | 'assistant' | 'system'
          const text = replayFrame.content ?? ''
          const messageId = replayFrame.id
          const messageTimestamp = replayFrame.timestamp
          const replayAgentId = replayFrame.agent_id
          // Per-turn model record on replay. The field is optional on the wire;
          // we read it as `model?: string` so frames without it (legacy or non-
          // model-producing turns) still parse. Trim and treat empty/whitespace
          // as absent — matches the renderer trim guard.
          const replayModelRaw = replayFrame.model
          const replayModel = typeof replayModelRaw === 'string' ? replayModelRaw.trim() : ''
          // Fix 5c: turn-correlation id, stamped by the backend on assistant
          // replay entries so a later turn_canceled entry (handled above) can
          // find this exact message. Captured on the ChatMessage so it survives
          // for the lifetime of the bucket entry (until ring-buffer eviction).
          const replayTurnId = replayFrame.turn_id
          // ADR-087 D2 — WS-replay truncation plumbing, layer 6 of the SPA's
          // six-layer path (§7.2). Same legacy-default rule as the cold-load
          // path (rawToMessage, src/lib/api.ts) via the same helper, so both
          // paths derive the same value from the same wire shape.
          const replayTruncated = replayFrame.truncated === true
          const replayTruncationReason = normalizeTruncationReason(
            replayFrame.truncated,
            replayFrame.truncation_reason,
          )
          withBucket(targetSid, (b) => {
            return produce(b, (draft) => {
              // Cursor advancement is handled centrally before the switch (I1);
              // no per-case advance needed here.
              const msgs = getMessages(b)
              // Reconnection dedup: prefer server-assigned id match when present;
              // fall back to (content + role + timestamp) tuple. Content-only dedup
              // was silently dropping legitimate identical user retries.
              if (messageId) {
                // Also check whether this id was already folded into an
                // existing bubble by the same-turn merge branch below — a
                // reconnect re-replaying frames the SPA already merged must
                // not merge them a second time (merge is an append, not an
                // idempotent overwrite, so a second pass would duplicate
                // content). Checked against the SESSION-level
                // mergedReplayMessageIds set (not just the current tail
                // bubble) — a merge always targets the then-current tail at
                // MERGE time, but by the time a reconnect re-replays that id,
                // a LATER turn may have produced a new tail bubble that never
                // saw this id merged into it. See mergedReplayMessageIds'
                // doc comment on SessionChatState for the exact failure this
                // closes. Also OR in the per-bubble field (belt-and-braces;
                // covers hand-built test fixtures that predate the
                // session-level set).
                const tailAssistantId = findLastAssistantMessageId(draft.messageOrder, draft.messagesById)
                const alreadyMerged =
                  (draft.mergedReplayMessageIds?.[messageId] ?? false) ||
                  (tailAssistantId != null &&
                    (draft.messagesById[tailAssistantId].mergedReplayIds?.includes(messageId) ?? false))
                if (draft.messageOrder.includes(messageId) || alreadyMerged) {
                  console.warn('chat.replay_dedup_skipped', { id: messageId, role, reason: alreadyMerged ? 'merged-id-match' : 'id-match' })
                  logDiagnostic('chatReplayDedupSkipped', { messageId, role, reason: alreadyMerged ? 'merged-id-match' : 'id-match', sessionId: targetSid })
                  return
                }
              } else {
                const tailId = draft.messageOrder[draft.messageOrder.length - 1]
                const tail = tailId ? draft.messagesById[tailId] : null
                const tailTs = tail?.timestamp ?? ''
                const frameTs = messageTimestamp ?? ''
                // Only dedup on content+role if timestamps also match (or both absent).
                if (
                  tail &&
                  tail.role === role &&
                  (tail.content ?? '') === text &&
                  (tailTs === frameTs || (tailTs === '' && frameTs === ''))
                ) {
                  console.warn('chat.replay_dedup_skipped', { role, reason: 'content-tuple-match' })
                  logDiagnostic('chatReplayDedupSkipped', { role, reason: 'content-tuple-match', sessionId: targetSid })
                  return
                }
              }
              // Coalesce assistant text into the trailing empty assistant bubble
              // that tool_call_start frames already created.
              if (role === 'assistant') {
                const lastMsgId = findLastAssistantMessageId(draft.messageOrder, draft.messagesById)
                // ADR-070 §2.2: a candidate is only eligible to receive more
                // replayed content — coalesce OR the same-turn merge below —
                // if it is still the RAW TAIL of messageOrder. Replay
                // bubbles are finalized (isStreaming:false) the instant
                // they're created, so isStreaming can't serve as the "still
                // open" signal the way it does live (§2.1); raw-tail
                // position is the substitute. Without this, a steer's
                // persisted user entry replayed between two same-turn
                // assistant entries would be skipped over by the backward
                // scan and the two entries would wrongly merge into one
                // bubble positioned before the steer message.
                const lastMsgIsRawTail =
                  lastMsgId != null && draft.messageOrder[draft.messageOrder.length - 1] === lastMsgId
                if (lastMsgId && lastMsgIsRawTail && (draft.messagesById[lastMsgId].content ?? '') === '') {
                  // Bake any tool calls that belong to this turn BEFORE taking the early
                  // return. Without this, toolCallOrder accumulates across turns and ends
                  // up baked onto the wrong (later) assistant message.
                  if (draft.toolCallOrder.length > 0) {
                    const existing = (draft.messagesById[lastMsgId].tool_calls ?? []) as PositionedToolCall[]
                    const existingById = new Map(existing.map((tc) => [tc.id, tc]))
                    const baked = draft.toolCallOrder
                      .filter((id) => draft.toolCalls[id])
                      .map((id) => stampToolCallOffset(id, draft.toolCalls[id], draft.textAtToolCallStart, existingById.get(id)?.textOffset))
                    const mergedById = new Map<string, PositionedToolCall>(existing.map((tc) => [tc.id, tc]))
                    for (const tc of baked) mergedById.set(tc.id, tc)
                    draft.messagesById[lastMsgId].tool_calls = Array.from(mergedById.values())
                    draft.toolCalls = {}
                    draft.toolCallOrder = []
                    draft.textAtToolCallStart = {}
                  }
                  const m = draft.messagesById[lastMsgId]
                  m.content = text
                  m.status = 'done'
                  m.isStreaming = false
                  m.pendingTextBoundary = false
                  if (replayAgentId) m.agentId = replayAgentId
                  // Stamp the per-turn model on the coalesced assistant turn (FR-014).
                  // Only set when the frame carried a non-empty model —
                  // legacy frames and non-model-producing turns stay
                  // model-less.
                  if (replayModel) m.model = replayModel
                  // Fix 5c: stamp the turn-correlation id so a later
                  // turn_canceled replay entry can find this exact message.
                  if (replayTurnId) m.turnId = replayTurnId
                  // ADR-087 D2 — this frame is the entry that closes the
                  // bubble (coalesced into the empty placeholder), so it's
                  // the one MarkLastEntryTruncated would have stamped.
                  if (replayTruncated) {
                    m.truncated = true
                    m.truncationReason = replayTruncationReason
                  }
                  // Coalesce path: this empty placeholder was created by the
                  // turn's own tool_call_start frames, so any pending live tool
                  // calls belong to THIS assistant. Bake them in before the early
                  // return — otherwise toolCallOrder leaks into the next turn and
                  // all calls get attributed to the LAST assistant at `done`.
                  // Mirrors the non-coalesce bake path immediately below.
                  if (draft.toolCallOrder.length > 0) {
                    const existing = (m.tool_calls ?? []) as PositionedToolCall[]
                    const existingById = new Map(existing.map((tc) => [tc.id, tc]))
                    const baked = draft.toolCallOrder
                      .filter((id) => draft.toolCalls[id])
                      .map((id) => stampToolCallOffset(id, draft.toolCalls[id], draft.textAtToolCallStart, existingById.get(id)?.textOffset))
                    const mergedById = new Map<string, PositionedToolCall>(existing.map((tc) => [tc.id, tc]))
                    for (const tc of baked) mergedById.set(tc.id, tc)
                    m.tool_calls = Array.from(mergedById.values())
                    draft.toolCalls = {}
                    draft.toolCallOrder = []
                    draft.textAtToolCallStart = {}
                  }
                  return
                }
                // Live/reload parity fix (A-I4): a real interleaved
                // (narration -> tool call -> narration) turn persists as
                // MULTIPLE transcript entries — pkg/agent/turn.go's
                // appendIntermediateAssistantTranscript writes the
                // pre-tool-call segment as its own entry, separate from the
                // post-tool-call segment (Bug #416) — but pkg/gateway/replay.go
                // emits one replay_message frame per entry unconditionally,
                // with no signal distinguishing "a new turn" from "the next
                // segment of the turn still streaming live". Live rendering
                // never splits these: the `token` case's agent_id-boundary
                // check (Fix 5a) keeps ONE bubble open across the whole
                // producer's turn, using pendingTextBoundary to insert a
                // paragraph break around each intervening tool call — the
                // bubble only closes on an actual producer change. Mirror
                // that here using turn_id (every modern entry sharing one
                // turnState's ts.turnID) + agent_id as the equivalent
                // "same producer, still the same turn" signal — replay
                // bubbles are already finalized (isStreaming:false) the
                // instant they're created, so isStreaming can't serve as the
                // open/closed boundary the way it does live; turn_id is the
                // substitute. This is deliberately restricted to entries that
                // both carry a turn_id — legacy/undecorated entries (no
                // turn_id) keep the pre-existing separate-bubble behavior,
                // never merged, since there is no reliable correlation data
                // for them.
                if (lastMsgId) {
                  const candidate = draft.messagesById[lastMsgId]
                  const sameTurn = !!replayTurnId && candidate.turnId === replayTurnId
                  // Mirrors the 'token' case's exact boundary rule: only a
                  // hard mismatch (BOTH sides known and different) blocks the
                  // merge. An unset id on either side stays permissive.
                  const compatibleProducer =
                    !replayAgentId || !candidate.agentId || candidate.agentId === replayAgentId
                  // Session-level check (see mergedReplayMessageIds' doc
                  // comment) — not just this bubble's own field — so this
                  // guard stays correct even if `candidate` is no longer the
                  // bubble the id was originally merged into.
                  const alreadyMerged = messageId != null && (
                    (draft.mergedReplayMessageIds?.[messageId] ?? false) ||
                    (candidate.mergedReplayIds?.includes(messageId) ?? false)
                  )
                  // ADR-070 §2.2: `lastMsgIsRawTail` (computed once, above,
                  // alongside `lastMsgId`) refuses this merge whenever
                  // something — in practice, a steer's persisted user entry
                  // — has been replayed after `candidate` since it was
                  // created, even when turnId/agentId still match.
                  if (sameTurn && compatibleProducer && !alreadyMerged && lastMsgIsRawTail) {
                    // Bake any tool calls that started on this bubble since
                    // the last segment landed, onto the SAME bubble we are
                    // about to extend — this is the bubble live's `done`
                    // handler would have baked onto too, since live never
                    // split this content into separate bubbles in the first
                    // place.
                    if (draft.toolCallOrder.length > 0) {
                      // Offsets are computed from `textAtToolCallStart` BEFORE
                      // `candidate.content += '\n\n' + text` below appends the
                      // next segment — each call's snapshot was captured back
                      // when it started (mid-way through the content
                      // accumulated so far), and since content only ever
                      // grows at the end, that snapshot's `.length` is
                      // already the correct split offset into whatever the
                      // FINAL merged content becomes, including segments
                      // appended after this bake (see stampToolCallOffset's
                      // doc comment; pinned by the "WS-replay same-turn
                      // merge" describe block's exact-offset assertion in
                      // chat.tool-call-offset.test.ts).
                      const existingCalls = (candidate.tool_calls ?? []) as PositionedToolCall[]
                      const existingById = new Map(existingCalls.map((tc) => [tc.id, tc]))
                      const baked = draft.toolCallOrder
                        .filter((id) => draft.toolCalls[id])
                        .map((id) => stampToolCallOffset(id, draft.toolCalls[id], draft.textAtToolCallStart, existingById.get(id)?.textOffset))
                      const mergedById = new Map<string, PositionedToolCall>(existingCalls.map((tc) => [tc.id, tc]))
                      for (const tc of baked) mergedById.set(tc.id, tc)
                      candidate.tool_calls = Array.from(mergedById.values())
                      draft.toolCalls = {}
                      draft.toolCallOrder = []
                      draft.textAtToolCallStart = {}
                    }
                    // Consume the seam marker exactly like the live 'token'
                    // handler: a tool call started on this bubble since the
                    // last segment, so insert a paragraph break rather than
                    // gluing the two segments together with no separator.
                    if (candidate.pendingTextBoundary) {
                      candidate.pendingTextBoundary = false
                    }
                    candidate.content += '\n\n' + text
                    // Keep the FIRST known model rather than the last — live
                    // never shows a per-segment model tag at all (the
                    // 'token' case never sets .model), so a single
                    // once-set-only tag on the merged bubble is the closest
                    // replay equivalent, and avoids the tag flip-flopping
                    // across segments that may report different models.
                    if (!candidate.model && replayModel) candidate.model = replayModel
                    // ADR-087 D2 — only the LAST transcript entry of an
                    // incomplete turn carries truncated/truncation_reason
                    // (MarkLastEntryTruncated stamps the final assistant
                    // entry only), so this only ever fires on the segment
                    // that closes the merged bubble — earlier segments in
                    // the same merge chain arrive with replayTruncated false
                    // and leave candidate.truncated untouched.
                    if (replayTruncated) {
                      candidate.truncated = true
                      candidate.truncationReason = replayTruncationReason
                    }
                    // Stamp agentId when previously unknown (mirrors the
                    // 'token' case). compatibleProducer already guarantees
                    // this never overwrites a genuinely different producer.
                    if (replayAgentId) candidate.agentId = replayAgentId
                    if (messageId) {
                      candidate.mergedReplayIds = [...(candidate.mergedReplayIds ?? []), messageId]
                      // Record at the session level too (see
                      // mergedReplayMessageIds' doc comment) so a later
                      // dedup check finds this id even after `candidate` is
                      // no longer the tail bubble.
                      draft.mergedReplayMessageIds = { ...(draft.mergedReplayMessageIds ?? {}), [messageId]: true }
                    }
                    return
                  }
                }
                // T1.10: Bake any live tool calls from the previous turn.
                if (lastMsgId && draft.toolCallOrder.length > 0) {
                  const lastMsg = draft.messagesById[lastMsgId]
                  const existing = (lastMsg.tool_calls ?? []) as PositionedToolCall[]
                  const existingById = new Map(existing.map((tc) => [tc.id, tc]))
                  const baked = draft.toolCallOrder
                    .filter((id) => draft.toolCalls[id])
                    .map((id) => stampToolCallOffset(id, draft.toolCalls[id], draft.textAtToolCallStart, existingById.get(id)?.textOffset))
                  const mergedById = new Map<string, PositionedToolCall>(existing.map((tc) => [tc.id, tc]))
                  for (const tc of baked) mergedById.set(tc.id, tc)
                  lastMsg.tool_calls = Array.from(mergedById.values())
                  draft.toolCalls = {}
                  draft.toolCallOrder = []
                  draft.textAtToolCallStart = {}
                  draft.toolCallOwnerMessageId = {}
                }
              }
              // FX-E (ADR-082 D9): bake any STILL-pending tool calls before
              // opening a message of a NON-assistant role (user/system) —
              // every branch above only bakes for `role === 'assistant'`
              // (the empty-placeholder coalesce and same-turn-merge
              // sub-paths bake-then-`return`; the T1.10 fallback
              // immediately above bakes-then-falls-through), so a
              // `role === 'assistant'` frame always leaves toolCallOrder
              // empty by the time it reaches here. A user/system
              // replay_message skips that whole `if` block, so without
              // this, a tool call whose owner is a PRIOR assistant bubble —
              // e.g. `set_goal` as the LAST thing in a turn, immediately
              // followed by the transcript's next USER message with no
              // further assistant narration replayed afterward — is left
              // stranded in `toolCallOrder` forever: the only other bake
              // site, the terminal `done` frame, is a no-op replay
              // terminator (`isReplayTerminatorDone`, this file's `done`
              // case) whenever the session has no live turn in flight — the
              // ordinary case of reloading an already-completed session.
              // The call then never reaches `message.tool_calls`, so
              // SetGoalCardBlock (and any other tool-call renderer keyed off
              // `message.tool_calls`) never sees it: the card renders live
              // but silently vanishes on reload (e2e
              // goal-card-position.spec.ts's post-reload assertion).
              // Routed through the owner map (bakeToolCallsByOwner, same as
              // the `done` case) rather than a flat "last assistant
              // message" bake — correct even when more than one assistant
              // bubble is currently open/pending an owner.
              if (role !== 'assistant' && draft.toolCallOrder.length > 0) {
                const fallbackMsgId = findLastAssistantMessageId(draft.messageOrder, draft.messagesById)
                bakeToolCallsByOwner(draft.messagesById, draft.toolCallOrder, draft.toolCalls, draft.toolCallOwnerMessageId ?? {}, fallbackMsgId, draft.textAtToolCallStart)
                draft.toolCalls = {}
                draft.toolCallOrder = []
                draft.textAtToolCallStart = {}
                draft.toolCallOwnerMessageId = {}
              }
              const newMsg: ChatMessage = {
                id: messageId ?? generateId(),
                role,
                content: text,
                timestamp: messageTimestamp ?? new Date().toISOString(),
                status: 'done' as const,
                ...(replayAgentId ? { agentId: replayAgentId } : {}),
                // Per-turn model record. Only on assistant messages (user/system turns
                // don't carry a producer model). Empty model is treated as
                // absent so the renderer doesn't show a phantom footer.
                ...(replayModel && role === 'assistant' ? { model: replayModel } : {}),
                // Fix 5c: turn-correlation id. Only meaningful on assistant
                // messages (the backend stamps turn_id on assistant + turn-
                // cancellation entries only) — lets a later turn_canceled
                // replay entry find this exact message.
                ...(replayTurnId && role === 'assistant' ? { turnId: replayTurnId } : {}),
                // ADR-087 D2 — only meaningful on assistant messages (the
                // backend only ever stamps this on the last assistant
                // transcript entry of an incomplete turn). D4a: an entry
                // with truncated:true and EMPTY content (`text === ''`)
                // still reaches here and still gets stamped — nothing in
                // this reducer conditions message creation on non-empty
                // content, so the bubble renders with no body and just the
                // D1 footer suffix, per spec.
                ...(replayTruncated && role === 'assistant'
                  ? { truncated: true as const, truncationReason: replayTruncationReason }
                  : {}),
              }
              draft.messagesById[newMsg.id] = newMsg
              draft.messageOrder.push(newMsg.id)
              // Ring buffer enforcement during replay — evict oldest entry plus all dependent maps.
              if (draft.messageOrder.length > MAX_MESSAGES_PER_SESSION) {
                const evictId = draft.messageOrder[0]
                evictMessageFromBucket(draft as unknown as SessionChatState, evictId)
                draft.trimmedCount += 1
              }
              void msgs // suppress unused warning — only used for dedup context above
            }) as Partial<SessionChatState>
          })
          break
        }

        case 'media': {
          if (!targetSid) break
          if (!Array.isArray(frame.parts) || frame.parts.length === 0) {
            console.warn('[chat] Received media frame with empty or invalid parts — appending notice')
            logDiagnostic('chatMediaFrameInvalidParts', { sessionId: targetSid })
            withBucket(targetSid, (b) => {
              return produce(b, (draft) => {
                // ADR-070 §2.1/F2 (code review): a bare scan would append
                // this notice to a closed, closedBySteer bubble if this
                // frame is the first to arrive after a mid-turn steer.
                // Mint a fresh bubble when there is no eligible open one —
                // matching the real-attachment branch below — rather than
                // silently dropping a failure notice the user should see.
                const lastMsgId = findOpenAssistantMessageId(draft.messageOrder, draft.messagesById)
                if (lastMsgId) {
                  const msg = draft.messagesById[lastMsgId]
                  msg.content = (msg.content ?? '') + (msg.content ? '\n\n' : '') + '_1 attachment could not be displayed._'
                } else {
                  const newMsg: ChatMessage = {
                    id: generateId(),
                    role: 'assistant',
                    content: '_1 attachment could not be displayed._',
                    timestamp: new Date().toISOString(),
                  }
                  draft.messagesById[newMsg.id] = newMsg
                  draft.messageOrder.push(newMsg.id)
                }
              }) as Partial<SessionChatState>
            })
            break
          }
          const attachments: MediaAttachment[] = frame.parts
            .filter((p) => p.url && p.type)
            .map((p) => ({
              type: p.type,
              url: p.url,
              filename: p.filename,
              contentType: p.content_type,
              caption: p.caption,
            }))
          if (attachments.length === 0) {
            withBucket(targetSid, (b) => {
              return produce(b, (draft) => {
                // ADR-070 §2.1/F2 — same reasoning as the invalid-parts
                // branch above.
                const lastMsgId = findOpenAssistantMessageId(draft.messageOrder, draft.messagesById)
                const notice = `_${frame.parts.length} attachment${frame.parts.length > 1 ? 's' : ''} could not be displayed._`
                if (lastMsgId) {
                  const msg = draft.messagesById[lastMsgId]
                  msg.content = (msg.content ?? '') + (msg.content ? '\n\n' : '') + notice
                } else {
                  const newMsg: ChatMessage = {
                    id: generateId(),
                    role: 'assistant',
                    content: notice,
                    timestamp: new Date().toISOString(),
                  }
                  draft.messagesById[newMsg.id] = newMsg
                  draft.messageOrder.push(newMsg.id)
                }
              }) as Partial<SessionChatState>
            })
            break
          }
          withBucket(targetSid, (b) => {
            return produce(b, (draft) => {
              // ADR-070 §2.1/F2: resolve the candidate through the
              // eligibility helper so a closed, closedBySteer bubble can
              // never be considered — the downstream `canAttach` refinement
              // (isStreaming OR empty-content) is unrelated to steering (it
              // exists for a freshly-created, not-yet-streaming placeholder)
              // and is kept as-is, now only ever evaluated against a
              // candidate that already passed the "still open" gate.
              const lastMsgId = findOpenAssistantMessageId(draft.messageOrder, draft.messagesById)
              const dedupe = (existing: MediaAttachment[] | undefined, incoming: MediaAttachment[]) => {
                const seen = new Set((existing ?? []).map((a) => a.url))
                const fresh = incoming.filter((a) => !seen.has(a.url))
                return [...(existing ?? []), ...fresh]
              }
              if (lastMsgId) {
                const msg = draft.messagesById[lastMsgId]
                const canAttach = msg.isStreaming || (msg.content ?? '') === ''
                if (canAttach) {
                  msg.media = dedupe(msg.media, attachments)
                  return
                }
              }
              const newMsg: ChatMessage = {
                id: generateId(),
                role: 'assistant',
                content: '',
                timestamp: new Date().toISOString(),
                media: attachments,
              }
              draft.messagesById[newMsg.id] = newMsg
              draft.messageOrder.push(newMsg.id)
            }) as Partial<SessionChatState>
          })
          break
        }

        case 'rate_limit': {
          // ADR-091 D7/FR-E-002: rate_limit is session-scoped
          // (SESSION_SCOPED_FRAME_TYPES) — a missing-id instance is already
          // dropped at the top of handleFrame, so targetSid is guaranteed
          // non-null here. No `?? getActiveSid()` fallback (cross-family
          // review finding 18: that fallback is exactly what let an untagged
          // rate_limit get filed under whatever session happened to be
          // active).
          if (!targetSid) break
          const rlFrame = frame as WsRateLimitFrame
          const event: RateLimitEventData = {
            scope: rlFrame.scope,
            resource: rlFrame.resource,
            policyRule: rlFrame.policy_rule,
            retryAfterSeconds: rlFrame.retry_after_seconds,
            agentId: rlFrame.agent_id,
            tool: rlFrame.tool,
          }
          armRateLimitClear(targetSid, event)
          break
        }

        case 'goal_status': {
          // ADR-049 D6/US-12 + ADR-053 FE-1/US-14: session-scoped (in
          // SESSION_SCOPED_FRAME_TYPES above) — targetSid is already
          // resolved/dropped per the routing rules at the top of handleFrame.
          //
          // PER-GOAL-ID pill map (FE-1): the frame carries `goal_id` (optional
          // — the §6 compat shim may omit it for a single-goal session). Key
          // the pill map by goal_id (falling back to '_default' when absent) so
          // a session with 2 goals shows 2 pills. Also maintain the legacy
          // single `goalStatus` (latest frame across all goals) for back-compat
          // with GoalIndicator's loop-only rendering path. The tray/pill
          // components still decide whether/how to RENDER each state — no
          // special-casing of that kind here (GoalPillTray owns the "keep a
          // terminal pill visible briefly, then stop showing it" behaviour).
          //
          // What IS special-cased here, deliberately: `evictGoalPillsOverCap`
          // (regression fix, bc66345f follow-up — see goalPills' doc comment
          // above and the function's own comment for the full reasoning).
          // Before bc66345f every frame shared the `'_default'` key, so this
          // map never grew past 1 entry; bc66345f's stable per-generation
          // `goal_id` (a correct, necessary fix — the multi-goal tray cannot
          // work without it) incidentally made this map's cardinality
          // unbounded, leaking one permanent tombstone per terminated goal
          // (done/failed/cleared) with no way for the user to dismiss it.
          // This is a memory-bound/GC decision (is this entry ever going to
          // change again?), not a render decision (should it be shown right
          // now?) — the render policy the comment above still refers to is
          // untouched.
          //
          // ADR-088 D5 store hygiene: an EMPTY-goal_id frame (the '_default'
          // key) was root cause #2 of the 2026-09-07 UX trace — the deleted
          // `queued` emission carried no `goal_id`, landed on '_default', and
          // was never overwritten once the later keyed `active` frame arrived
          // under a different key, so the stale card rendered forever. The
          // `queued` emission itself is gone (ADR-088 D9), but a keyed frame
          // arriving for this session still evicts any lingering '_default'
          // pill defensively — harmless once no frame is ever emitted with an
          // empty goal_id, cheap insurance against any stale/legacy one.
          //
          // ADR-088 code-review round 1, Finding 1 (HIGH): the pill for
          // `pillKey` is no longer stored verbatim — it goes through
          // `mergeGoalPillFrame` so a routine, criteria-less progress frame
          // cannot clobber a record a prior `set_goal` write already
          // authored. `goalStatus` (the legacy single latest-frame selector,
          // feeding only `GoalIndicator`, which never reads criteria) stays
          // store-verbatim — this fix is scoped to `goalPills` only, the
          // field the record card actually reads. See `mergeGoalPillFrame`'s
          // doc comment for the merge rule.
          if (!targetSid) break
          const goalFrame = frame as GoalStatusFrame
          const pillKey = goalFrame.goal_id && goalFrame.goal_id.length > 0 ? goalFrame.goal_id : '_default'
          withBucket(targetSid, (b) => {
            const storedPill = b.goalPills?.[pillKey]
            const mergedPill = mergeGoalPillFrame(storedPill, goalFrame)
            const merged = { ...(b.goalPills ?? {}), [pillKey]: mergedPill }
            if (pillKey !== '_default') {
              delete merged['_default']
            }
            // Operator-reported UX fix (2026-09-08): the goal-ack line — see
            // buildGoalAckInsertion's doc comment above for the full design
            // (why this frame, why idempotent-by-goal_id, and the durability
            // tradeoff vs. a persisted transcript anchor). Applied in the
            // SAME withBucket pass as the pill-map update above (one set()
            // call) rather than a second withBucket, so a reattach that
            // fires both the pill update and the first-ever ack insertion
            // renders as one atomic state transition, not two.
            const ackInsertion = buildGoalAckInsertion(b, goalFrame)
            return {
              goalStatus: goalFrame,
              goalPills: evictGoalPillsOverCap(merged),
              ...ackInsertion,
            }
          })
          break
        }

        case 'browser_handover_notice': {
          // ADR-085 BROWSER-FR-042 (render half) / FR-044 (SPA idempotency
          // half), wave B8 (C-73 — this arm and buildBrowserHandoverInsertion
          // are this file's ENTIRE B8 region; neither reads nor edits any
          // goal_status symbol). Session-scoped (in SESSION_SCOPED_FRAME_TYPES
          // above) — targetSid is already resolved/dropped per the routing
          // rules at the top of handleFrame. See buildBrowserHandoverInsertion's
          // doc comment for the idempotency/append-at-tail rationale (mirrors
          // buildGoalAckInsertion's pattern — C-73).
          if (!targetSid) break
          const handoverFrame = frame as BrowserHandoverNoticeFrame
          withBucket(targetSid, (b) => buildBrowserHandoverInsertion(b, handoverFrame) ?? {})
          break
        }

        case 'goal_outcome': {
          // Goal outcome line (founder decision 2026-09-14) — how a goal ended,
          // pushed live at the ending or re-sent by replay from the persisted
          // `system_subtype: goal_outcome` entry. Session-scoped (in
          // SESSION_SCOPED_FRAME_TYPES above). buildGoalOutcomeInsertion
          // (src/lib/goalOutcome.ts) drops a frame whose id the bucket already
          // holds, so live + replay + cold load converge on one line.
          if (!targetSid) break
          const outcomeFrame = frame as GoalOutcomeFrame
          withBucket(targetSid, (b) => buildGoalOutcomeInsertion(b, outcomeFrame) ?? {})
          break
        }

        case 'loop_status': {
          // ADR-049 D6/US-12: session-scoped, same store-verbatim pattern as goal_status.
          if (!targetSid) break
          const loopFrame = frame as LoopStatusFrame
          withBucket(targetSid, () => ({ loopStatus: loopFrame }))
          break
        }

        case 'ask_user_question': {
          // askuserquestion-tool-spec v3 §3: session-scoped (card.session_id,
          // resolved into targetSid above). Store the card verbatim — a
          // pending card renders the tabbed question zone and locks the
          // composer; a terminal (answered/cancelled) card renders the
          // collapsed record (§0.6: from THIS record, never a parse of the
          // resume message) and unlocks the composer.
          if (!targetSid) break
          const askFrame = frame as AskUserQuestionFrame
          withBucket(targetSid, () => ({ pendingAsk: askFrame.card }))
          break
        }

        case 'plan_status': {
          // ADR-049 R3/FR-099: GLOBAL frame — PlanStatusFrame carries no
          // session_id (correlated by plan_id, not any chat thread), so it
          // is NOT routed through targetSid/withBucket at all. Invalidate
          // the plan/task query caches so PlansFilterBand/BoardView (which read
          // Plan.state/plan_phase/paused_reason directly off the REST
          // response, not off this frame) refetch and re-render with the
          // new state — including the "paused — owner disabled" surfacing
          // (US-10 AS-7), which is driven entirely by the refetched Plan
          // object's own fields, not by anything stored from this frame.
          //
          // Scoped + debounced (refetch-storm fix) — see
          // schedulePlanStatusInvalidate's doc comment above.
          const planFrame = frame as PlanStatusFrame
          schedulePlanStatusInvalidate(planFrame.plan_id)
          break
        }

        case 'judge_verdict': {
          // ADR-049 D2/D4/US-13: ALWAYS feeds the ActivityPanel's judge row
          // via a dedicated global store (mirrors the #283/#264
          // whatsapp_pairing/notification pattern: accessed via getState()
          // at frame time, never routed through a session bucket) —
          // unconditional, regardless of session_id.
          //
          // Live-thread-card fix (2026-09-14): the frame now OPTIONALLY
          // carries `session_id` for scope=task/scope=goal
          // (JudgeVerdictFrame.yaml). When present, ALSO insert the verdict
          // as a thread message in that session — see
          // buildJudgeVerdictInsertion's own doc comment
          // (src/lib/judgeVerdictThread.ts) for the anchoring/de-dup
          // contract. A frame without session_id (scope=plan, or any legacy
          // emission) keeps exactly today's panel-only behaviour.
          const verdictFrame = frame as JudgeVerdictFrame
          useJudgeActivityStore.getState().apply(verdictFrame)
          if (verdictFrame.session_id) {
            withBucket(verdictFrame.session_id, (b) => buildJudgeVerdictInsertion(b, verdictFrame) ?? {})
          }
          break
        }

        case 'knowledge_index_progress': {
          // ADR-067 FR-080: GLOBAL frame (no session_id — it describes a
          // knowledge base, not a chat). Same shape as the whatsapp_pairing /
          // notification cases: applied through getState() at frame time so
          // chatStore stays decoupled from the knowledge store.
          //
          // Without this case the frame was validated, counted as known, and
          // then dropped on the floor — which is why every knowledge base in
          // the Library reported "no indexing progress received" forever.
          useKnowledgeIndexStore.getState().apply(frame as KnowledgeIndexProgressFrame)
          break
        }

        case 'whatsapp_pairing': {
          // #283: global (not session-tied) — record QR/status for the Channels
          // config panel. Accessed via getState() at frame time (not a hook
          // subscription) so chatStore stays decoupled from the pairing store.
          useWhatsAppPairingStore
            .getState()
            .apply(frame as WhatsAppPairingFrame)
          break
        }

        case 'notification': {
          // #264: global (not session-tied) — push into the dedicated
          // Notifications store backing the header notification center. Mirrors
          // the #283 whatsapp_pairing case: accessed via getState() at frame
          // time so chatStore stays decoupled from the notifications store.
          useNotificationsStore.getState().apply(frame as NotificationFrame)
          break
        }

        case 'tool_approval_required':
          // ADR-091 D7/FR-E-002: session-scoped (SESSION_SCOPED_FRAME_TYPES)
          // — a missing-id instance is already dropped at the top of
          // handleFrame, so frame.session_id (which enqueue reads directly)
          // is guaranteed present here.
          useToolApprovalStore.getState().enqueue(frame)
          break

        case 'tool_approval_resolved':
          // The server closed this approval (a decision from any tab, timeout,
          // Stop, agent deletion, shutdown) — drop it here and keep it dropped.
          useToolApprovalStore.getState().markResolved(frame.approval_id)
          break

        case 'session_state': {
          useToolApprovalStore.getState().reconcileWithSessionState(frame)
          // askuserquestion-tool-spec v3 US-6 S1/FR-9: reconcile pending
          // AskUserQuestion cards on every reconnect snapshot. The
          // hydrate/clear/race semantics live in the dedicated, unit-tested
          // reconcilePendingAsks (mirrors the toolApproval
          // reconcileWithSessionState pattern); this case only applies the
          // computed per-session changes.
          const stateFrame = frame as SessionStateFrame
          const askChanges = reconcilePendingAsks(
            stateFrame.pending_asks ?? [],
            get().sessionsById,
          )
          for (const [sid, card] of Object.entries(askChanges)) {
            withBucket(sid, () => ({ pendingAsk: card }))
          }
          // ADR-082 D4/D5 (FR-008/FR-009), review CR3/S2: a connection that
          // just bound to a session (fresh mount, reconnect, or second tab)
          // learns here whether a turn is already running for it. `targetSid`
          // resolves to `frame.session_id` when the frame carries one (the
          // generated `SessionStateFrame` type carries an optional
          // `session_id` — CR3), falling back to `activeSid` only when it is
          // absent (an older gateway, or any other reason the field is
          // missing) — see the generic `frameSessionId` resolution above.
          // This matters because a client can be attached/foreground on one
          // session while a session_state snapshot for a DIFFERENT session
          // (e.g. a background tab's own reconnect, or a stale broadcast)
          // arrives — routing it to whatever happens to be foreground would
          // wrongly stamp an unrelated session's turn onto the active one.
          //
          // Mirror exactly the state a live turn THIS client had started
          // would already be in — isStreaming:true so the Stop control and
          // composer lock render immediately — without creating the
          // assistant bubble yet: replay history for this attach has not
          // arrived on the wire at this point (case 'replay_message' below
          // pushes messages in arrival order onto messageOrder), so opening
          // the bubble here would insert it BEFORE messages that are
          // chronologically earlier, corrupting order. The bubble opens
          // instead at the replay-terminating `done` (see case 'done'
          // above), which is guaranteed to fire only after every
          // replay_message for this attach has already landed — UNLESS a
          // token for this turn beats that done here (out-of-order gateway,
          // or a fast concurrent turn), in which case the 'token' case's own
          // ADR-082 review fix opens/marks the bubble first and this done
          // becomes a no-op for placeholder purposes.
          if (targetSid) {
            const activeTurn = stateFrame.active_turn
            if (activeTurn) {
              // S2: a stale/racing announcement for a turn this client
              // already finalized (its own done already processed — see
              // markTurnFinished in the 'done' case) must be ignored
              // outright. Re-applying it would set isStreaming:true /
              // activeTurnId again with no second done ever coming to close
              // it a second time — a permanent Stop button and locked
              // composer.
              if (!isTurnFinished(targetSid, activeTurn.turn_id)) {
                withBucket(targetSid, (b) => {
                  // If a bubble for this session is already streaming (e.g.
                  // an older gateway that sends session_state LAST, after
                  // tokens have already started flowing for this very
                  // turn), do not reset the "bubble opened" flag to false —
                  // the 'token' case's own fix already flipped it true the
                  // instant the first token landed, and stomping it back to
                  // false here would make a later replay-terminator-shaped
                  // done wrongly think it still needs to open a placeholder.
                  const lastMsgId = findLastAssistantMessageId(b.messageOrder, b.messagesById)
                  const lastMsg = lastMsgId ? b.messagesById[lastMsgId] : undefined
                  const alreadyStreaming =
                    !!lastMsg && (lastMsg.isStreaming === true || lastMsg.status === 'streaming')
                  return {
                    isStreaming: true,
                    activeTurnId: activeTurn.turn_id,
                    activeTurnAgentId: activeTurn.agent_id,
                    activeTurnBubbleOpened: b.activeTurnBubbleOpened || alreadyStreaming,
                  }
                })
              }
            } else {
              // CR3: this snapshot says NO turn is in flight for this
              // session. If a PRIOR snapshot (or the token case) had
              // announced one and it is still unresolved, clear it so a
              // stale announcement can never wedge the composer — but only
              // force isStreaming:false when no bubble is actually open;
              // a genuinely open, mid-stream bubble keeps streaming exactly
              // as before (its own done will finalize it normally).
              //
              // Check bucket existence BEFORE calling withBucket: withBucket
              // always creates (`?? emptySessionState()`) and writes back the
              // bucket it's given, even for a no-op `{}` patch. A session
              // this client has never otherwise heard of (no bucket yet) has
              // nothing to clear — calling withBucket unconditionally here
              // would materialize a brand-new empty bucket for it, which is
              // an observable regression: a bare "no turn in flight" snapshot
              // for a session with no other activity must stay a true no-op,
              // exactly like it was before this fix (chat.reconnect.test.ts's
              // "session_state WITHOUT active_turn leaves current behaviour
              // unchanged").
              const existing = get().sessionsById[targetSid]
              if (existing?.activeTurnId) {
                withBucket(targetSid, (b) => {
                  const bubbleOpen = !!b.activeTurnBubbleOpened
                  return {
                    activeTurnId: null,
                    activeTurnAgentId: null,
                    activeTurnBubbleOpened: false,
                    ...(bubbleOpen ? {} : { isStreaming: false }),
                  }
                })
              }
            }
          }
          break
        }

        case 'system_overload':
          useUiStore.getState().addToast({
            message: frame.message ?? 'System at capacity — agent action blocked. Retry shortly.',
            variant: 'warning',
          })
          break

        case 'replay_warning':
          // Gateway detected duplicate tool_call_ids in the transcript on
          // replay. Server-only slog.Warn was invisible to operators because
          // the count was buried in done.Stats. One-shot toast surfaces it.
          useUiStore.getState().addToast({
            message: frame.message,
            variant: 'warning',
          })
          break

        case 'cancel_stage':
          // B3: gateway is broadcasting cancel progress for this session.
          // Write the stage into the per-session bucket so the UI can update
          // the stop-button label in real time. The done handler (above) clears
          // it back to null once the turn is definitively over.
          withBucket(targetSid, () => ({ cancelStage: frame.stage }))
          break

        case 'device_pairing_request':
          // I2: a new device is requesting pairing approval. DevicesSection
          // polls the ['devices'] query while open; invalidating it surfaces the
          // new pending request immediately instead of waiting for the next poll.
          queryClient.invalidateQueries({ queryKey: ['devices'] })
          break
    default:
      return false
  }
  return true
}
