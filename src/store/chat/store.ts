// store.ts: Zustand chat-store creation, frame handling, and foreground synchronization

import { create } from 'zustand'
import { produce } from 'immer'
import { generateId } from '@/lib/constants'
import { useSessionStore } from '@/store/session'
import type { Message } from '@/lib/api'
import { logDiagnostic } from '@/lib/telemetry'
import { findLastAssistantMessageId, findOpenAssistantMessageId, getMessages } from './messages'
import { EMPTY_BUCKET, RATE_LIMIT_CLEAR_MS, rateLimitClearTimers, replayingClearTimers, replayingStartedAt, sawReplayMessageThisTurn } from './runtime-state'
import { applyMessageArray, emptySessionState, omitKeys } from './session'
import type { ChatMessage, ChatStore, OutboundQueueItem, RateLimitEventData, SessionChatState } from './types'
import { createOutboundResponseSlice } from './slices/outbound-responses'
import { createOutboundLifecycleSlice } from './slices/outbound-lifecycle'
import { createFrameSlice } from './slices/frames'

// HIGH-2: consecutive unknown frame counter. Reset on any known-good frame.
// On threshold (5), promotes to a user-visible warning toast.
const chatRuntime = { unknownFrameCount: 0, agentIdAtLastMintSend: null as string | null }

/** Sends one drained queue item, forwarding its #823 correlation id/timestamp
 * for a QueuedOutboundMessage (a legacy plain-string entry has neither).
 * Module scope so it doesn't count against maybeDrainNext's/create()'s
 * grandfathered line budget (scripts/budgets/functions.txt). */
function drainQueuedMessage(get: () => ChatStore, next: OutboundQueueItem): void {
  if (typeof next === 'string') {
    get().sendMessage(next)
  } else {
    get().sendMessage(next.content, { clientMessageId: next.id, queuedAt: next.timestamp })
  }
}

// agentIdAtLastMintSend records the agent that was active when the most recent
// session-minting message went out (a send with no session_id). The
// `session_started` ack for that mint carries the agent the SERVER resolved,
// and adopting it unconditionally lets a late ack silently override a newer
// explicit user choice: pick Mia, send, switch the picker to Jim, then the
// in-flight ack (resolved under Mia) snaps the picker back to Mia and the next
// turn runs as an agent the user did not choose. Explicit user intent must win
// over a stale server echo, so the ack only adopts frame.agent_id while the
// selection is still the one the mint was sent under. null means "no mint in
// flight" (the ordinary case, where adopting the server's answer is correct).

export const useChatStore = create<ChatStore>((set, get) => {
  // ── Internal helpers that mutate a named session bucket ─────────────────────
  // These read/write sessionsById[sid] and then re-sync foreground fields.

  // ADR-091 D7/FR-E-002: returns null when no session is active, in every
  // environment — the test-mode FALLBACK_SID ('__default') fallback is
  // deleted (cross-family review finding 18). Frame writers must
  // early-return on null.
  function getActiveSid(): string | null {
    return useSessionStore.getState().activeSessionId
  }

  /**
   * Project a session bucket to foreground ChatStore fields
   * (messageOrder+messagesById → messages[], and messagesById passed
   * through as-is for O(1) per-id lookups — see ChatStore.messagesById's
   * doc comment). Also derives `lastAssistantMessageId` (see
   * ChatStore.lastAssistantMessageId's doc comment) — a backward linear
   * scan with early-exit, strictly dominated by the getMessages() O(N)
   * build already happening here.
   */
  function bucketToForeground(bucket: SessionChatState): Omit<SessionChatState, 'messageOrder' | 'trimmedCount' | 'spanBySpanId' | 'pendingSpanUpdatesBySpanId' | 'toolCallOwnerMessageId'> & { messages: ChatMessage[]; lastAssistantMessageId: string | null } {
    const rest = omitKeys(bucket, ['messageOrder', 'trimmedCount', 'spanBySpanId', 'pendingSpanUpdatesBySpanId', 'toolCallOwnerMessageId'] as const)
    return {
      ...rest,
      messages: getMessages(bucket),
      // ADR-070 §2.7 (grill-spec round 2, NEW-002): routed through the same
      // eligibility helper the write-side handlers use, so the ARIA "New
      // response" announcement this field solely drives cannot fire early
      // for a bubble that was only closed because a mid-turn steer landed
      // after it. Traced against the ordinary (non-steer) case: the final
      // reply is always the raw tail once it's genuinely done, so this is a
      // no-op change there — same id, same behavior as before.
      lastAssistantMessageId: findOpenAssistantMessageId(bucket.messageOrder, bucket.messagesById),
    }
  }

  /** Find or lazily create a bucket for sid. No-op if sid is null. */
  function withBucket(sid: string | null, updater: (bucket: SessionChatState) => Partial<SessionChatState>): void {
    if (!sid) return
    set((state) => {
      const existing = state.sessionsById[sid] ?? emptySessionState()
      const patch = updater(existing)
      const updated: SessionChatState = { ...existing, ...patch }
      const sessionsById = { ...state.sessionsById, [sid]: updated }
      const activeSid = getActiveSid()
      const activeBucket = (activeSid ? sessionsById[activeSid] : null) ?? EMPTY_BUCKET
      return { sessionsById, ...bucketToForeground(activeBucket) }
    })
  }

  /**
   * Remove a bucket entirely (unlike `withBucket`, which can only
   * patch/create). Used for FULL teardown of the `'__pending'` bucket —
   * kickoff rollback, kickoff rejection cleanup, and the
   * "kickoff acked while the user navigated to a different workspace"
   * branch — none of which want a lingering empty/errored bucket
   * left under the shared `'__pending'` key. No-op if the bucket doesn't
   * exist. Resyncs foreground fields the same way `withBucket` does, so a
   * delete of the CURRENTLY-foreground bucket correctly collapses to
   * `EMPTY_BUCKET` rather than leaving stale foreground fields behind.
   */
  function deleteBucket(sid: string): void {
    set((state) => {
      if (!(sid in state.sessionsById)) return {}
      const sessionsById = { ...state.sessionsById }
      delete sessionsById[sid]
      const activeSid = getActiveSid()
      const activeBucket = (activeSid ? sessionsById[activeSid] : null) ?? EMPTY_BUCKET
      return { sessionsById, ...bucketToForeground(activeBucket) }
    })
  }

  /**
   * Resolve a workspace's `kickoffAttemptStatus` slot. `'done'`
   * deletes the entry entirely (success needs no further tracking — the
   * server's own `setup_pending:false` already blocks a refire via the
   * hook's ordinary guard); `'failed'` marks it so the hook's
   * invalidate-on-failure effect can react.
   */
  function resolveKickoffAttempt(workspaceId: string, outcome: 'done' | 'failed'): void {
    set((state) => {
      const kickoffAttemptStatus = { ...state.kickoffAttemptStatus }
      if (outcome === 'done') {
        delete kickoffAttemptStatus[workspaceId]
      } else {
        kickoffAttemptStatus[workspaceId] = 'failed'
      }
      return { kickoffAttemptStatus }
    })
  }

  /**
   * Full, quiet teardown of an outstanding workspace-setup
   * kickoff — see the public `abandonPendingKickoff` action's doc comment
   * on the ChatStore interface for the exact contract. No-op when
   * `pendingKickoff` is already null.
   */
  function abandonPendingKickoffInternal(): void {
    const kickoff = get().pendingKickoff
    if (kickoff === null) return
    deleteBucket('__pending')
    set({ pendingKickoff: null })
    resolveKickoffAttempt(kickoff.workspaceId, 'failed')
  }

  /** Re-sync foreground fields from sessionsById after an external session switch. */
  function syncForeground(): void {
    set((state) => {
      const activeSid = getActiveSid()
      const fg = (activeSid ? state.sessionsById[activeSid] : null) ?? EMPTY_BUCKET
      return bucketToForeground(fg)
    })
  }

  /**
   * Sets a session's rate-limit event and arms the shared auto-clear timer:
   * cancels any existing timer for the session, stores the new event, then
   * after RATE_LIMIT_CLEAR_MS clears it — but only if the bucket's event is
   * still referentially the one just set (a newer rate_limit event that
   * replaced it before the timer fired is left alone). Shared by
   * setRateLimitEvent and the 'rate_limit' WS-frame handler so the
   * "set + timeout + clear" logic isn't implemented twice.
   */
  function armRateLimitClear(sid: string, event: RateLimitEventData): void {
    if (rateLimitClearTimers[sid] != null) {
      clearTimeout(rateLimitClearTimers[sid])
      delete rateLimitClearTimers[sid]
    }
    withBucket(sid, () => ({ rateLimitEvent: event }))
    rateLimitClearTimers[sid] = setTimeout(() => {
      delete rateLimitClearTimers[sid]
      set((state) => {
        const bucket = state.sessionsById[sid]
        if (!bucket || bucket.rateLimitEvent !== event) return {}
        const updated: SessionChatState = { ...bucket, rateLimitEvent: null }
        const sessionsById = { ...state.sessionsById, [sid]: updated }
        const activeSid = getActiveSid()
        const fg = (activeSid ? sessionsById[activeSid] : null) ?? EMPTY_BUCKET
        return { sessionsById, ...bucketToForeground(fg) }
      })
    }, RATE_LIMIT_CLEAR_MS)
  }

  /**
   * BUG FIX (2026-07, offline-queue drain): pop and send the next drained
   * message, but ONLY if no turn is currently in flight. Called every time a
   * turn ends (done/error frames, explicit cancel, the C8 stream-clear sweep,
   * or a failed send) so queued messages go out one at a time instead of the
   * old synchronous loop that fired `sendMessage` for the whole queue in one
   * tick — which silently dropped every message after the first because the
   * very first call flips `isStreaming` synchronously and every subsequent
   * call in that same loop hit the `isStreaming` guard in `sendMessage` (that
   * guard shows a connection-error banner and returns WITHOUT re-enqueuing,
   * unlike the disconnected-WS branch checked immediately after the
   * isStreaming guard in sendMessage).
   *
   * Reads `isStreaming` fresh via `get()` rather than trusting a closed-over
   * value, since callers invoke this synchronously from inside a `set()`
   * update that may have just flipped it.
   */
  function maybeDrainNext(): void {
    const { pendingDrainQueue, isStreaming } = get()
    if (pendingDrainQueue.length === 0 || isStreaming) return
    const [next, ...rest] = pendingDrainQueue
    set({ pendingDrainQueue: rest })
    drainQueuedMessage(get, next)
  }

  return {
    sessionsById: {},

    // ── Outbound queue initial state ─────────────────────────────────────────
    outboundQueue: [],
    pendingDrainQueue: [],

    // No kickoff outstanding at store init.
    pendingKickoff: null,
    // No per-workspace kickoff attempts tracked at store init.
    kickoffAttemptStatus: {},
    markKickoffInFlight: (workspaceId) => {
      set((state) => ({
        kickoffAttemptStatus: { ...state.kickoffAttemptStatus, [workspaceId]: 'in-flight' },
      }))
    },
    resolveKickoffAttempt: (workspaceId, outcome) => {
      resolveKickoffAttempt(workspaceId, outcome)
    },
    abandonPendingKickoff: () => {
      abandonPendingKickoffInternal()
    },

    // Foreground selectors — derived from sessionsById[activeSessionId].
    // Initial values are the empty-session defaults projected through bucketToForeground.
    // Note: `messages` (the derived ordered array) and `messagesById` (the
    // raw per-id map, for O(1) lookups) are both exported on ChatStore;
    // messageOrder itself is not — see bucketToForeground.
    messages: [],
    messagesById: {},
    lastAssistantMessageId: null,
    isStreaming: false,
    isReplaying: false,
    replayCompletedForSession: null,
    toolCalls: {},
    toolCallOrder: [],
    textAtToolCallStart: {},
    sessionTokens: 0,
    sessionCost: 0,
    rateLimitEvent: null,
    goalStatus: null,
    goalPills: {},
    loopStatus: null,
    pendingAsk: null,
    lastUserMessageAt: null,
    cancelStage: null,
    lastReceivedEventTime: null,
    // Phase 1 / FR-008/009/010: per-thread model override for the next
    // outgoing message. null means "no override" — the server uses the
    // agent's `model` config. The composer writes here on picker
    // change; the runtime reads here in onNew and clears it.
    nextModel: null,
    setNextModel: (model) => set({ nextModel: model }),

    setReplaying: (value) => {
      const sid = getActiveSid()
      if (!sid) return
      if (value) {
        // Always reset the window start on setReplaying(true), even when
        // isReplaying is already true. A second attach to the same session
        // (e.g. user re-clicks the session button, or the SPA re-fires attach
        // after a session_started frame) must give a fresh MIN_REPLAY_DISPLAY_MS
        // window — otherwise a stale `replayingStartedAt` from minutes earlier
        // makes the elapsed-time computation in the false-path collapse the
        // disabled window to zero on the next 'done' frame.
        replayingStartedAt[sid] = Date.now()
        // Cancel any pending false-flip timer scheduled by a previous attach;
        // letting it fire would clobber the freshly-started replay window.
        if (replayingClearTimers[sid]) {
          clearTimeout(replayingClearTimers[sid])
          delete replayingClearTimers[sid]
        }
        withBucket(sid, () => ({ isReplaying: true }))
        return
      }
      // No-op if already false.
      const current = get().sessionsById[sid]
      if (!current?.isReplaying) {
        if (sawReplayMessageThisTurn[sid]) {
          console.warn('[chat] setReplaying(false) ignored — isReplaying was already false despite replay_message having been processed. Likely attachToSession race.')
          logDiagnostic('chatSetReplayingIgnored', { sessionId: sid })
        }
        return
      }
      sawReplayMessageThisTurn[sid] = false
      const elapsed = Date.now() - (replayingStartedAt[sid] ?? 0)
      // FR-I-014: keep the "Loading session history…" overlay visible for at least
      // MIN_REPLAY_DISPLAY_MS after replay starts.  250ms was too short — Playwright's
      // page.click() waits for network-idle before returning, which can take 250+ ms,
      // meaning the timer fired before the test could observe the disabled state.
      // 750ms is a conservative minimum that survives typical CI latency while still
      // clearing quickly enough for interactive use.
      const MIN_REPLAY_DISPLAY_MS = 750
      if (elapsed >= MIN_REPLAY_DISPLAY_MS) {
        withBucket(sid, () => ({ isReplaying: false }))
      } else {
        // Cancel any previous pending timer before scheduling a new one so a
        // burst of `done` frames doesn't queue multiple stale clears.
        if (replayingClearTimers[sid]) {
          clearTimeout(replayingClearTimers[sid])
        }
        replayingClearTimers[sid] = setTimeout(() => {
          delete replayingClearTimers[sid]
          withBucket(sid, () => ({ isReplaying: false }))
        }, MIN_REPLAY_DISPLAY_MS - elapsed)
      }
    },

    setMessages: (messages) => {
      const sid = getActiveSid()
      if (!sid) return
      const empty = emptySessionState()
      const msgs = messages as ChatMessage[]
      const msgById: Record<string, ChatMessage> = {}
      const msgOrder: string[] = []
      for (const m of msgs) { msgById[m.id] = m; msgOrder.push(m.id) }
      withBucket(sid, () => ({
        ...empty,
        messagesById: msgById,
        messageOrder: msgOrder,
      }))
    },

    // ADR-049 D2/D4/SD-C10 (verdict-card fix): originally, the WS
    // live/replayed `judge_verdict` frame (see `case 'judge_verdict'` below)
    // never inserted a thread message — it was a deliberately GLOBAL frame
    // (no `session_id` on the wire, JudgeVerdictFrame.yaml), so it was
    // routed to `useJudgeActivityStore` (the ActivityPanel) only. The ONLY
    // carrier that could place a verdict in a specific chat thread was the
    // persisted REST transcript (`type: judge_verdict`, forwarded by
    // rawToMessage). But ChatScreen.tsx's ordinary `historyData` effect only
    // calls `setMessages` (a full bucket OVERWRITE) when the bucket is still
    // empty and WS replay hasn't already populated it — in the normal
    // (WS-connected) case, WS replay wins that race almost every time, so
    // the REST fetch resolves into a no-op and any judge_verdict entry it
    // carried was silently lost, reload or not (reproduced live: two real
    // judge rounds recorded in the transcript, ActivityPanel showed them,
    // the thread never did, even after a hard reload).
    //
    // Live-thread-card fix (2026-09-14): the frame now OPTIONALLY carries
    // `session_id` (scope=task/scope=goal), and `case 'judge_verdict'`
    // inserts the SAME thread card directly (src/lib/judgeVerdictThread.ts),
    // keyed by this same entry id — so in the normal WS-connected case this
    // action is now a no-op (its `if (draft.messagesById[verdictMsg.id])
    // continue` guard below skips a card already inserted live/on replay).
    // It remains the ONLY path for a scope this frame doesn't cover
    // (scope=plan — no session_id) and for a session whose WS replay never
    // ran (a cold REST-only load with no live connection).
    //
    // This action is the fix: called whenever `historyData` resolves AND the
    // active bucket is populated with replay not in flight (ChatScreen.tsx's
    // gated effect — see its own doc comment for why the gate is
    // load-bearing), it walks the REST-fetched transcript and inserts any
    // `judge_verdict` entry the bucket doesn't already have — positioned by
    // TURN-ID anchor first (the judged turn's assistant message; see the
    // anchor-1 comment below), content-match fallback second, append-at-end
    // last. Neither raw entry ids nor timestamps work as the position key
    // against a replay-populated bucket — live-verified against a real
    // gateway: WS replay's assistant-bubble coalescing does not preserve the
    // underlying transcript entry's own id on the resulting ChatMessage
    // (an id-neighbor scan found no match and dropped every verdict at
    // position 0), and replay frames carry no timestamp so replay-created
    // messages are stamped with ARRIVAL time (a timestamp comparison made
    // every bucket entry "newer" than every persisted verdict — same
    // position-0 symptom). Id-based dedup (via messagesById), so a
    // live/replayed duplicate delivery (there isn't one today, but
    // future-proofing) or a repeat REST fetch never double-inserts.
    mergeJudgeVerdictHistory: (sessionId, historyMessages) => {
      const verdictEntries = historyMessages.filter(
        (m): m is Message & { type: 'judge_verdict'; verdict: NonNullable<Message['verdict']> } =>
          m.type === 'judge_verdict' && !!m.verdict,
      )
      if (verdictEntries.length === 0) return
      withBucket(sessionId, (b) => {
        return produce(b, (draft) => {
          for (const verdictMsg of verdictEntries) {
            if (draft.messagesById[verdictMsg.id]) continue
            const historyIdx = historyMessages.indexOf(verdictMsg)
            // Anchor 1 — TURN ID (the reliable one, live-verified necessary):
            // scan the REST list backward from the verdict for the nearest
            // preceding entry that carries a turnId (the judged turn's own
            // assistant message — `writeGoalVerdictTranscript` writes the
            // verdict immediately after that turn's entries), then insert
            // after the LAST bucket message carrying that same turnId.
            // Timestamps CANNOT order against a replay-populated bucket:
            // replay frames carry no timestamp (pkg/gateway/replay.go's
            // generic ReplayMessageFrame sets Role/Content/AgentId/TurnId/
            // Model only), so every replay-created ChatMessage is stamped
            // with its ARRIVAL time — live-verified to make every bucket
            // "timestamp" newer than every persisted verdict timestamp,
            // which dumped both cards at index 0. turn_id is the one stable
            // per-turn correlator both carriers share (REST Message.turn_id
            // ↔ ReplayMessageFrame.turn_id → ChatMessage.turnId).
            let insertPos = -1
            for (let j = historyIdx - 1; j >= 0 && insertPos === -1; j--) {
              const anchorTurnId = historyMessages[j].turnId
              if (!anchorTurnId) continue
              for (let k = draft.messageOrder.length - 1; k >= 0; k--) {
                const m = draft.messagesById[draft.messageOrder[k]]
                if (m?.turnId === anchorTurnId) { insertPos = k + 1; break }
              }
            }
            // Anchor 2 — CONTENT (legacy fallback, best-effort): nearest
            // preceding user/assistant entry with non-empty content; insert
            // after the last bucket message with identical content. Only
            // reachable for transcripts whose entries predate turn-id
            // stamping. Best-effort by nature: identical contents across
            // turns resolve to the LAST match, which can over-shoot for a
            // repeat-reply pattern — accepted, since without turn ids there
            // is no better signal on either carrier.
            if (insertPos === -1) {
              for (let j = historyIdx - 1; j >= 0 && insertPos === -1; j--) {
                const anchor = historyMessages[j]
                if ((anchor.role !== 'user' && anchor.role !== 'assistant') || !anchor.content) continue
                for (let k = draft.messageOrder.length - 1; k >= 0; k--) {
                  const m = draft.messagesById[draft.messageOrder[k]]
                  if (m && (m.role === 'user' || m.role === 'assistant') && m.content === anchor.content) {
                    insertPos = k + 1
                    break
                  }
                }
              }
            }
            // No anchor found at all (verdict precedes every bucket message,
            // or empty bucket) — append at the end.
            if (insertPos === -1) insertPos = draft.messageOrder.length
            draft.messagesById[verdictMsg.id] = verdictMsg as ChatMessage
            draft.messageOrder.splice(insertPos, 0, verdictMsg.id)
          }
        }) as Partial<SessionChatState>
      })
    },

    appendMessage: (message) => {
      const sid = getActiveSid()
      if (!sid) return
      withBucket(sid, (b) => {
        const msgs = [...getMessages(b), message]
        return applyMessageArray(msgs, b)
      })
    },

    updateLastAssistantMessage: (content, done = false) => {
      const sid = getActiveSid()
      if (!sid) return
      withBucket(sid, (b) => {
        return produce(b, (draft) => {
          let msgId = findLastAssistantMessageId(draft.messageOrder, draft.messagesById)
          if (msgId === null) {
            const placeholder: ChatMessage = {
              id: generateId(),
              role: 'assistant',
              content: '',
              timestamp: new Date().toISOString(),
              status: 'streaming',
              isStreaming: true,
            }
            draft.messagesById[placeholder.id] = placeholder
            draft.messageOrder.push(placeholder.id)
            msgId = placeholder.id
          }
          const msg = draft.messagesById[msgId]
          msg.content = msg.content + content
          msg.isStreaming = !done
          msg.status = done ? 'done' : 'streaming'
          // Clear the seam marker whenever the bubble finalizes — a "boundary
          // pending" flag is meaningless once no further token is coming.
          if (done) msg.pendingTextBoundary = false
          draft.isStreaming = !done
        }) as Partial<SessionChatState>
      })
    },

    markLastMessageInterrupted: (sessionId) => {
      const sid = sessionId ?? getActiveSid()
      withBucket(sid, (b) => {
        // ADR-070 §2.7 (grill-spec round 2, NEW-001): a bare
        // findLastAssistantMessageId scan would resolve to an already-closed
        // closedBySteer bubble if Stop/Escape/`/cancel` fires in the window
        // between a steer closing the pre-steer bubble and the next frame
        // opening a new one — mislabeling an already-finished, correct
        // reply segment as 'interrupted'. findOpenAssistantMessageId
        // refuses that; null is then handled by the SAME "no assistant
        // message exists yet" placeholder branch immediately below, which
        // already exists for the analogous pre-existing case (cancel fired
        // before the first token frame) — the trigger condition just got
        // broader, not the branch itself.
        const lastMsgId = findOpenAssistantMessageId(b.messageOrder, b.messagesById)
        if (!lastMsgId) {
          // FR-21 / T21–T23: No assistant message exists yet (cancel fired between
          // session_started and the first token frame). The server may still send
          // "Error processing message: turn canceled" as token+done frames via the
          // outbound bus. We must create a placeholder interrupted message NOW so:
          //   1. The UI shows the (interrupted) label immediately.
          //   2. The token handler discards the error-string token (it checks
          //      msgs[lastIdx].status === 'interrupted' and returns {} on match).
          //   3. The done handler preserves 'interrupted' status over 'done'.
          // Bucket-level isStreaming is set to false immediately here because the
          // server may take several seconds to process the cancel and send the done
          // frame. Clearing isStreaming now lets the useEffect([isStreaming]) fire
          // and schedule the "Stopping…" → "stop" label reset via the T25 minimum-
          // display timer (stoppingStartedAt was set BEFORE cancelStream() was called
          // by the Escape/click handler, so the timer fires after the remaining
          // portion of the 1000ms minimum window — not immediately).
          const placeholder: ChatMessage = {
            id: generateId(),
            role: 'assistant',
            content: '',
            timestamp: new Date().toISOString(),
            status: 'interrupted',
            isStreaming: false,
          }
          const msgs = [...getMessages(b), placeholder]
          // S7: clear any ADR-082 active-turn announcement alongside
          // isStreaming — see the produce() branch below for why.
          return {
            ...applyMessageArray(msgs, b),
            isStreaming: false,
            activeTurnId: null,
            activeTurnAgentId: null,
            activeTurnBubbleOpened: false,
          }
        }
        // FR-21 / T21–T26: set isStreaming:false AND status:'interrupted' on the message.
        // Setting isStreaming:false is necessary so that buildMessageStatus() in
        // omnipus-runtime.ts returns { type: "incomplete", reason: "cancelled" } rather
        // than { type: "running" } — only then does AssistantUI properly render the
        // message as cancelled and the (interrupted) label becomes visible.
        // Trailing tokens from the server are handled in the 'token' case handler which
        // now checks `status === 'interrupted'` FIRST and discards any trailing tokens
        // rather than creating a second placeholder or overwriting the interrupted status.
        return produce(b, (draft) => {
          const m = draft.messagesById[lastMsgId!]
          if (m) { m.isStreaming = false; m.status = 'interrupted'; m.pendingTextBoundary = false }
          // T24b fix: bucket-level isStreaming must ALSO clear immediately
          // here, exactly like the no-message placeholder branch above does —
          // clearing only the message's own isStreaming left the BUCKET (and
          // therefore the foreground `isStreaming` ChatScreen/useCancelState
          // read for the Stop button's `isStreaming || stopLabel==='stopping'`
          // render condition and its reset effect) gated on the server's
          // terminal `done` frame. For an ordinary cancel that frame arrives
          // in well under a second, but an AWAITED delegate cancel cascade
          // (the server must first unwind the running subagent turn) can
          // intermittently take longer than the 5s the e2e allots the button
          // to disappear. Since a last assistant message already exists by
          // the time Stop is clicked in every real scenario (T21/T24a/T24b
          // all have one), this branch — not the placeholder one — is the one
          // that actually runs, so it must carry the same immediate clear.
          draft.isStreaming = false
          // S7: an explicit user cancel ends streaming here, synchronously —
          // clear any ADR-082 active-turn announcement in the same write.
          // The invariant elsewhere in this file ("activeTurnId is never set
          // while isStreaming is false") is written by every path that
          // stops streaming, not just the 'session_state'/'done' cases —
          // this is one of them. Leaving activeTurnId set here would let a
          // later, unrelated done for this session misread it as "replay
          // still awaiting catch-up" and open a stray empty placeholder.
          draft.activeTurnId = null
          draft.activeTurnAgentId = null
          draft.activeTurnBubbleOpened = false
        }) as Partial<SessionChatState>
      })
      // An explicit `sessionId` (e.g. the browser panel's pinned session)
      // must never leak into interrupting a message in some OTHER,
      // unrelated session — the cross-bucket fallback scan below exists only
      // to cover legacy edge cases for the DEFAULT (active-session) call
      // site, where a message can land in a bucket other than the one
      // `getActiveSid()` currently names (e.g. a race in test scaffolding).
      // Skip it whenever the caller named a specific target.
      if (sessionId !== undefined) {
        maybeDrainNext()
        return
      }
      // If no streaming assistant message was found in the active bucket, search
      // all buckets. This handles scenarios where a message was appended to a
      // different bucket before the active session was set (e.g. in test scaffolding).
      const state = get()
      const activeBucket = sid ? state.sessionsById[sid] : undefined
      const hasInterruptedInActive = activeBucket && getMessages(activeBucket).some(
        (m: ChatMessage) => m.role === 'assistant' && m.status === 'interrupted'
      )
      if (!hasInterruptedInActive) {
        for (const [bucketSid, bucket] of Object.entries(state.sessionsById)) {
          if (bucketSid === sid) continue
          const lastMsgId = findLastAssistantMessageId(bucket.messageOrder, bucket.messagesById)
          if (lastMsgId && bucket.messagesById[lastMsgId].isStreaming) {
            // Update the background bucket AND sync the updated messages to the foreground
            // flat field so callers reading get().messages can see the change.
            useChatStore.setState((s) => {
              const b = s.sessionsById[bucketSid]
              if (!b) return {}
              const updatedById = { ...b.messagesById }
              const target = updatedById[lastMsgId!]
              if (target && target.role === 'assistant') {
                // #3: role guard ensures status:'interrupted' is only stamped onto
                // AssistantMessage where that status is legal per the discriminated union.
                // The outer loop already restricts lastMsgId to role:'assistant' entries,
                // so this guard is defence-in-depth against future refactors that could
                // break that invariant.
                updatedById[lastMsgId!] = { ...target, isStreaming: false, status: 'interrupted', pendingTextBoundary: false } as ChatMessage
              }
              const updated: SessionChatState = { ...b, messagesById: updatedById }
              const updatedSessions = { ...s.sessionsById, [bucketSid]: updated }
              // Propagate to flat foreground so observers see the interrupted message.
              return {
                sessionsById: updatedSessions,
                messages: getMessages(updated),
              }
            })
            break
          }
        }
      }
      // This turn is over (interrupted) — send the next drained message, if any.
      maybeDrainNext()
    },

    startToolCall: (callId, tool, params) => {
      const sid = getActiveSid()
      if (!sid) return
      withBucket(sid, (b) => {
        const lastMsgId = b.messageOrder[b.messageOrder.length - 1]
        const lastMsg = lastMsgId ? b.messagesById[lastMsgId] : undefined
        const textSnapshot = (lastMsg?.role === 'assistant' ? lastMsg.content : '') ?? ''
        return {
          toolCalls: {
            ...b.toolCalls,
            [callId]: { id: callId, call_id: callId, tool, params, status: 'running' },
          },
          toolCallOrder: [...b.toolCallOrder, callId],
          textAtToolCallStart: { ...b.textAtToolCallStart, [callId]: textSnapshot },
        }
      })
    },

    resolveToolCall: (callId, result, status, durationMs, error) => {
      const sid = getActiveSid()
      if (!sid) return
      withBucket(sid, (b) => {
        if (!b.toolCalls[callId]) {
          console.debug('[chat] resolveToolCall for unknown call_id', callId)
          return {}
        }
        return {
          toolCalls: {
            ...b.toolCalls,
            [callId]: { ...b.toolCalls[callId], result, status, duration_ms: durationMs, error },
          },
        }
      })
    },

    cancelToolCall: (callId) => {
      const sid = getActiveSid()
      if (!sid) return
      withBucket(sid, (b) => {
        if (!b.toolCalls[callId]) return {}
        return {
          toolCalls: {
            ...b.toolCalls,
            [callId]: { ...b.toolCalls[callId], status: 'cancelled' },
          },
        }
      })
    },

    updateSessionStats: (tokens, cost) => {
      const sid = getActiveSid()
      if (!sid) return
      withBucket(sid, (b) => ({
        sessionTokens: b.sessionTokens + tokens,
        sessionCost: b.sessionCost + cost,
      }))
    },

    seedSessionTokens: (total) => {
      const sid = getActiveSid()
      if (!sid) return
      withBucket(sid, (b) => {
        // Only seed when bucket is fresh — don't overwrite live-accumulated totals.
        if (b.sessionTokens !== 0) return {}
        return { sessionTokens: total }
      })
    },

    setRateLimitEvent: (event) => {
      const sid = getActiveSid()
      if (!sid) return
      armRateLimitClear(sid, event)
    },

    clearRateLimitEvent: () => {
      const sid = getActiveSid()
      if (!sid) return
      if (rateLimitClearTimers[sid] != null) {
        clearTimeout(rateLimitClearTimers[sid])
        delete rateLimitClearTimers[sid]
      }
      withBucket(sid, () => ({ rateLimitEvent: null }))
    },

    resetSession: () => {
      const sid = getActiveSid()
      if (!sid) return
      if (rateLimitClearTimers[sid] != null) {
        clearTimeout(rateLimitClearTimers[sid])
        delete rateLimitClearTimers[sid]
      }
      // Cancel any pending deferred replay-clear timer for this session, or it
      // later fires withBucket(sid, {isReplaying:false}) on the freshly-reset
      // bucket. Mirror the sibling timer maps above.
      if (replayingClearTimers[sid] != null) {
        clearTimeout(replayingClearTimers[sid])
        delete replayingClearTimers[sid]
      }
      sawReplayMessageThisTurn[sid] = false
      withBucket(sid, () => emptySessionState())
    },

    resetSessionForReplay: (sessionId) => {
      // Clear all transient state so the upcoming replay rebuilds from
      // scratch. This is the targeted reset for WS reconnect: without it,
      // replay frames append duplicate bubbles to the existing bucket.
      sawReplayMessageThisTurn[sessionId] = false
      replayingStartedAt[sessionId] = Date.now()
      // Re-attach refreshes the replay window — cancel any stale
      // setReplaying(false) timer left over from the previous attach so it
      // can't fire mid-window and prematurely re-enable the composer.
      if (replayingClearTimers[sessionId]) {
        clearTimeout(replayingClearTimers[sessionId])
        delete replayingClearTimers[sessionId]
      }
      withBucket(sessionId, () => ({
        ...emptySessionState(),
        isReplaying: true,
      }))
    },

    // ── Outbound queue actions ────────────────────────────────────────────────

    ...createOutboundLifecycleSlice({ set, get, getActiveSid, withBucket, bucketToForeground, abandonPendingKickoffInternal, maybeDrainNext, runtime: chatRuntime }),
    ...createOutboundResponseSlice(),

    ...createFrameSlice({ set, get, getActiveSid, bucketToForeground, withBucket, deleteBucket, resolveKickoffAttempt, abandonPendingKickoffInternal, syncForeground, armRateLimitClear, maybeDrainNext, runtime: chatRuntime }),
  }
})

// Expose syncForeground so setActiveSession can call it after switching sessions.
// Avoiding a direct import of the session store here to keep the cycle-break intact.
export function syncChatForeground(): void {
  // Re-read active session from the session store and sync foreground fields.
  const activeSid = useSessionStore.getState().activeSessionId
  useChatStore.setState((state) => {
    const fg = (activeSid ? state.sessionsById[activeSid] : null) ?? EMPTY_BUCKET
    // Project messageOrder+messagesById → messages for foreground consumers
    // (messagesById itself passes through as-is — see bucketToForeground).
    const rest = omitKeys(fg, ['messageOrder', 'trimmedCount', 'spanBySpanId', 'pendingSpanUpdatesBySpanId', 'toolCallOwnerMessageId'] as const)
    return { ...rest, messages: getMessages(fg) }
  })
}
