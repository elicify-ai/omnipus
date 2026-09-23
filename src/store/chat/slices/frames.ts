// frames.ts: inbound websocket frame routing and reduction


import type { StoreApi } from 'zustand'
import { produce } from 'immer'
import { generateId } from '@/lib/constants'
import { useUiStore } from '@/store/ui'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { queryClient } from '@/lib/queryClient'
import { tasksQueryKeys } from '@/lib/api'
import type { Agent } from '@/lib/api'
import type {
  WsSubagentStartFrame,
  WsSubagentEndFrame,
} from '@/lib/ws'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { logDiagnostic } from '@/lib/telemetry'
import { normalizeTruncationReason } from '@/lib/truncation'
import {
  getLLMErrorDisplay,
  readEntryIdFromFrame,
  readLLMErrorFromFrame,
  sanitizeLegacyErrorMessage,
} from '@/lib/llm-error'
import { advanceEventTime, clampToolResult, findLastAssistantMessageId, findOpenAssistantMessageId, getMessages } from '../messages'
import { bufferForSpan, hasOpenSpanFast, markTurnFinished, scheduleLibraryChangedInvalidate } from '../routing'
import { CANCEL_ACK_FRAME_TYPES, EMPTY_BUCKET, SESSION_SCOPED_FRAME_TYPES, UNKNOWN_FRAME_TOAST_THRESHOLD, pendingCancelAckSids, replayingClearTimers, replayingStartedAt, sawReplayMessageThisTurn } from '../runtime-state'
import { applyMessageArray, bakeToolCallsByOwner, emptySessionState, isToolCallBakedInBucket } from '../session'
import { gateFrameBySeq, CURSOR_MINTING_FRAME_TYPES, type SeqFrameLike } from '../cursor'
import { ORPHAN_BUFFER_TTL_MS, orphanTimers, pendingByParentCallId } from '../types'
import type { ChatMessage, ChatStore, RateLimitEventData, SessionChatState, SubagentSpan, SubagentSpanRunning, SubagentSpanTerminal } from '../types'
import { handleReplayAndStatusFrame } from './replay-and-status-frames'
import { handleCatchUpFrame } from './catchup-frames'



type FrameSlice = Pick<ChatStore, 'handleFrame'>
type ToolCallResultFrame = Extract<Parameters<ChatStore['handleFrame']>[0], { type: 'tool_call_result' }>
type MessageStatusFrame = Extract<Parameters<ChatStore['handleFrame']>[0], { type: 'message_status' }>

// Both helpers below are kept at module scope (not inlined in handleFrame)
// so their bodies don't count against handleFrame's/createFrameSlice's
// grandfathered line budgets (scripts/budgets/functions.txt) — that function
// was already at its recorded ceiling before #823, so any net-new line
// inside it fails `make lint-budgets`; extracting is the prescribed fix
// ("Grandfathered entries may only shrink — do not add to one, extract
// first.", CLAUDE.md "Size budgets").

// #823 state A: received/working/failed map onto the user message's own
// deliveryStatus.
function applyMessageStatusFrame(bucket: SessionChatState, frame: MessageStatusFrame): Partial<SessionChatState> {
  return produce(bucket, (draft) => {
    const message = draft.messagesById[frame.client_message_id]
    if (!message || message.role !== 'user') return
    message.deliveryStatus = frame.state === 'failed' ? 'failed' : frame.state
    message.status = frame.state === 'failed' ? 'error' : 'done'
  }) as Partial<SessionChatState>
}

// I1: advance the reconnect `since` cursor from whatever frame carries a
// `timestamp` field. The cursor is sent as `since` on attach_session so the
// gateway skips transcript entries the SPA already saw.
//
// What this actually covers today — the generic `frame.timestamp` read
// below reads broadly, but the set of frames that can satisfy it is small
// and worth stating plainly rather than leaving as "any frame":
//   - `replay_error` is the ONLY frame the gateway currently sends with a
//     populated timestamp (pkg/gateway/replay.go, `buildReplayErrorFrame`
//     is the single `Timestamp:` assignment in the replay path).
//   - `replay_message` declares an OPTIONAL `timestamp` in the contract
//     (contracts/components/schemas/ReplayMessageFrame.yaml) but neither
//     gateway construction site populates it, so in production it never
//     advances the cursor. The reducer still honours it if that changes.
//   - `token` / `done` / `session_state` have no `timestamp` field at all
//     and never advance the cursor.
//
// So the cursor moves rarely and lags the true high-water mark. That is
// conservative in the SAFE direction: too-old a `since` costs a duplicate
// replay the dedup paths absorb, whereas too-new would silently skip
// messages. Do not "fix" the lag by advancing on a frame whose timestamp
// is not a transcript-entry time — the server compares `since` against
// TranscriptEntry.Timestamp, so only those values are meaningful here.
//
// advanceEventTime is monotonic (only moves forward) and compares
// chronologically, not lexicographically — see its doc comment for why the
// difference matters on RFC3339Nano's variable-width fractional seconds.
// Being monotonic, it is safe to run before the per-frame reducer
// regardless of dedup/early-return paths.
function advanceReceivedEventTime(
  frame: unknown,
  targetSid: string | null,
  withBucket: FrameContext['withBucket'],
): void {
  const frameTimestamp = (frame as { timestamp?: string }).timestamp
  if (frameTimestamp && targetSid) {
    withBucket(targetSid, (b) => ({
      lastReceivedEventTime: advanceEventTime(b.lastReceivedEventTime, frameTimestamp),
    }))
  }
}

// #823 catch-up redesign (BE-DESIGN.md §6.2): the SPA-side apply-rule gate,
// run once per frame ahead of the whole per-type switch below (kept out of
// handleFrame's own body — see the comment on advanceReceivedEventTime just
// above for why: handleFrame is already at its grandfathered line-budget
// ceiling, scripts/budgets/functions.txt).
//
// Returns 'apply' when the frame should proceed to the normal reducers
// (either it carries no `seq` at all — the overwhelming majority of frames
// today, since Lane A's gateway hub had not shipped `seq` on any real
// connection as of this write — or it is exactly the next expected number).
// Returns 'drop-or-gap' when the frame must NOT reach the switch: either it
// is a duplicate/already-applied (`kind: 'drop'`) or it is a genuine gap
// (`kind: 'gap'`), in which case this function ALSO fires the re-attach the
// design requires (§6.2's gap row: "send attach_session{S, cursor}") using
// whatever cursor the bucket already has — the server's own §3.3 rule
// decides whether that cursor is still servable.
function applySeqGate(
  frame: unknown,
  targetSid: string | null,
  get: FrameContext['get'],
  withBucket: FrameContext['withBucket'],
): 'apply' | 'drop-or-gap' {
  const f = frame as SeqFrameLike & { type?: string }
  if (f.seq === undefined || f.seq === null || !targetSid) return 'apply'
  if (f.type && CURSOR_MINTING_FRAME_TYPES.has(f.type)) return 'apply'
  const bucket = get().sessionsById[targetSid]
  const decision = gateFrameBySeq(bucket?.cursor ?? null, f)
  if (decision.kind === 'apply') {
    withBucket(targetSid, () => ({ cursor: decision.cursor }))
    return 'apply'
  }
  if (decision.kind === 'gap') {
    console.warn('[chat] sequence gap — re-attaching', { sessionId: targetSid, have: decision.cursor.seq, got: f.seq })
    logDiagnostic('chatSeqGapReattach', { sessionId: targetSid, have: decision.cursor.seq, got: f.seq })
    useConnectionStore.getState().connection?.send({
      type: 'attach_session',
      session_id: targetSid,
      since_seq: decision.cursor.seq,
      boot_id: decision.cursor.bootId,
    })
  }
  return 'drop-or-gap'
}

function appendUnmatchedToolError(
  bucket: SessionChatState,
  frame: ToolCallResultFrame,
  result: unknown,
): Partial<SessionChatState> {
  if (frame.status !== 'error') {
    console.debug('[chat] resolveToolCall for unknown call_id', frame.call_id)
    return {}
  }
  console.warn('[chat] unmatched tool error rendered as standalone notice', { callId: frame.call_id, tool: frame.tool })
  logDiagnostic('chatUnmatchedToolError', { callId: frame.call_id, tool: frame.tool })
  return produce(bucket, (draft) => {
    const notice: ChatMessage = {
      id: generateId(),
      role: 'assistant',
      content: '',
      timestamp: new Date().toISOString(),
      status: 'done',
      isStreaming: false,
      agentId: frame.agent_id,
      tool_calls: [{
        id: frame.call_id,
        tool: frame.tool,
        params: {},
        result,
        status: 'error',
        duration_ms: frame.duration_ms,
        error: frame.error,
      }],
    }
    draft.messagesById[notice.id] = notice
    draft.messageOrder.push(notice.id)
  }) as Partial<SessionChatState>
}

// Issue #822: a real done can arrive at the reconnect bind boundary before
// its catch-up token. That next token is the completed snapshot, not a new
// live stream. New turns are already streaming before their first token.
function isTerminalCatchUpToken(bucket: SessionChatState): boolean {
  return bucket.terminalCatchUpPending === true && !bucket.isStreaming
}

function applyTokenStreamingState(bucket: SessionChatState, message: ChatMessage): void {
  const isStreaming = !isTerminalCatchUpToken(bucket)
  message.isStreaming = isStreaming
  message.status = isStreaming ? 'streaming' : 'done'
  bucket.isStreaming = isStreaming
  bucket.terminalCatchUpPending = false
}

function needsTerminalCatchUp(bucket: SessionChatState, wasReplaying: boolean): boolean {
  return wasReplaying && !!bucket.activeTurnId && !bucket.activeTurnBubbleOpened
}

interface FrameContext {
  set: StoreApi<ChatStore>['setState']
  get: StoreApi<ChatStore>['getState']
  getActiveSid: () => string | null
  bucketToForeground: (bucket: SessionChatState) => Omit<SessionChatState, 'messageOrder' | 'trimmedCount' | 'spanByParentCallId' | 'spanBySpanId' | 'toolCallOwnerMessageId'> & { messages: ChatMessage[]; lastAssistantMessageId: string | null }
  withBucket: (sid: string | null, updater: (bucket: SessionChatState) => Partial<SessionChatState>) => void
  deleteBucket: (sid: string) => void
  resolveKickoffAttempt: (workspaceId: string, outcome: 'done' | 'failed') => void
  abandonPendingKickoffInternal: () => void
  syncForeground: () => void
  armRateLimitClear: (sid: string, event: RateLimitEventData) => void
  maybeDrainNext: () => void
  runtime: { unknownFrameCount: number; agentIdAtLastMintSend: string | null }
}
export function createFrameSlice({ set, get, getActiveSid, bucketToForeground, withBucket, deleteBucket, resolveKickoffAttempt, abandonPendingKickoffInternal, syncForeground, armRateLimitClear, maybeDrainNext, runtime }: FrameContext): FrameSlice {
  return {
    handleFrame: (frame) => {
      // Resolve which session this frame belongs to.
      // session_started is special: it carries the new id for the pending message.
      // ask_user_question nests its session id on card.session_id (required
      // by schema) rather than the top level — fall back to it so the frame
      // routes session-scoped like every other member of
      // SESSION_SCOPED_FRAME_TYPES.
      const frameSessionId =
        (frame as { session_id?: string }).session_id ??
        (frame as { card?: { session_id?: string } }).card?.session_id
      const activeSid = getActiveSid()

      // F-S1: Route to the correct bucket.
      // Session-scoped frames missing session_id are treated differently per environment.
      const targetSid: string | null = (() => {
        if (frame.type === 'session_started') return activeSid // handled below, value unused
        if (frameSessionId) return frameSessionId
        // F-S3 (UAT, browser-panel "Take over"): an untagged (no session_id)
        // token/done/error frame that is really the server's cancellation
        // acknowledgment for a BACKGROUND session (cancelStream(sessionId),
        // e.g. the browser panel's "Take over" pausing its own pinned
        // session while a different chat is foreground) must not be
        // misattributed to whatever session happens to be active — that
        // corrupts the active session's own, unrelated in-flight turn with a
        // stray (interrupted)/error status. When there is exactly one
        // session with an outstanding, unacknowledged explicit cancel and it
        // is NOT the active session, it is unambiguously the more likely
        // origin than "whatever's on screen" — route there instead. Any
        // other case (no pending cancel, more than one pending, or the
        // pending one IS the active session — the ordinary single-session
        // Stop-button flow) falls through unchanged to the existing
        // behaviour below.
        if (CANCEL_ACK_FRAME_TYPES.has(frame.type) && pendingCancelAckSids.size === 1) {
          const [onlyPendingSid] = pendingCancelAckSids
          if (onlyPendingSid !== activeSid) return onlyPendingSid
        }
        if (SESSION_SCOPED_FRAME_TYPES.has(frame.type)) {
          if (import.meta.env.MODE === 'test') {
            // In test mode: fall back to active session so test scaffolding stays simple.
            console.warn('[chat] frame missing session_id — routing to active session', { type: frame.type, activeSid })
            return activeSid
          }
          // In production: drop the frame and surface a one-shot connection error.
          console.error('[chat] server frame missing session_id — dropping', { type: frame.type })
          logDiagnostic('chatFrameMissingSessionId', { frameType: frame.type })
          useConnectionStore.getState().setConnectionError(
            'internal: server frame missing session_id — please reload'
          )
          return null
        }
        // Global frame (error, ping, pong, device_pairing_*, session_state) — use active.
        return activeSid
      })()

      const originalActiveSid = activeSid

      const store = get()

      // HIGH-2: reset unknown-frame counter on every known-good frame.
      runtime.unknownFrameCount = 0

      // I1: advance the reconnect `since` cursor — see advanceReceivedEventTime's
      // own doc comment above (moved there so this addition doesn't grow
      // handleFrame past its grandfathered line budget).
      advanceReceivedEventTime(frame, targetSid, withBucket)

      // #823 catch-up redesign (BE-DESIGN.md §6.2): the apply rule, gating
      // every SEQUENCED session-scoped frame ahead of the switch below — see
      // applySeqGate's own doc comment for why this belongs here rather than
      // per-case.
      if (applySeqGate(frame, targetSid, get, withBucket) === 'drop-or-gap') {
        syncForeground()
        return
      }

      if (handleReplayAndStatusFrame({ frame, targetSid, get, getActiveSid, withBucket, armRateLimitClear })) {
        syncForeground()
        return
      }

      if (handleCatchUpFrame({ frame, targetSid, get, withBucket })) {
        syncForeground()
        return
      }

      switch (frame.type) {
        case 'message_status':
          withBucket(targetSid, (bucket) => applyMessageStatusFrame(bucket, frame))
          break
        case 'session_started': {
          // Server minted a new session_id in response to a message sent without one.
          const newSid = frame.session_id

          // This ack may be resolving a pending workspace-setup
          // kickoff rather than an ordinary sendMessage no-session turn.
          // Capture + clear `pendingKickoff` up front — this frame always
          // resolves whichever kickoff was outstanding, regardless of which
          // branch below runs. Resolve the per-workspace attempt tracker
          // ('done') too — this ack is always a SUCCESS for the kickoff
          // (a failure arrives as an 'error' frame instead, never as
          // session_started), regardless of which branch below handles it.
          const resolvedKickoff = get().pendingKickoff
          if (resolvedKickoff) {
            set({ pendingKickoff: null })
            resolveKickoffAttempt(resolvedKickoff.workspaceId, 'done')
          }
          const stillOnOriginatingWorkspace =
            resolvedKickoff === null ||
            useWorkspacesStore.getState().activeWorkspaceId === resolvedKickoff.workspaceId
          // Plausibility gate: even when still on the originating
          // workspace, only treat this ack as safe to FOREGROUND when the
          // '__pending' placeholder is STILL the local foreground turn
          // (activeSessionId === '__pending'). If the user has since
          // attached to a different, real session WITHOUT leaving the
          // workspace — e.g. picked an existing conversation from the
          // sidebar/search while the kickoff was still resolving —
          // `activeSessionId` is that real session's id, not '__pending';
          // forefronting the kickoff's session here would silently evict
          // the session the user actually selected (and, with `sendMessage`'s
          // no-longer-possible collision — the collision guard prevents
          // a stray plain message from ever sharing the '__pending' bucket
          // with the kickoff — this is the one remaining way a late kickoff
          // ack could still misattribute). Treat it exactly like "wrong
          // workspace": migrate silently, do not disturb whatever is
          // actually foreground.
          const kickoffStillForeground =
            resolvedKickoff === null || useSessionStore.getState().activeSessionId === '__pending'

          if (resolvedKickoff && (!stillOnOriginatingWorkspace || !kickoffStillForeground)) {
            // Late ack, wrong workspace, or superseded foreground:
            // do NOT foreground it — leave whatever the user is currently
            // looking at completely alone. Record the real session id under
            // the ORIGINATING workspace's descriptor instead, so a later
            // enterWorkspaceChat for that workspace attaches to (and
            // replays) this session normally.
            useSessionStore.getState().setWorkspaceSessionDescriptor(resolvedKickoff.workspaceId, {
              id: newSid,
              type: 'chat',
              title: null,
              agentId: frame.agent_id ?? null,
            })
            // Free the '__pending' bucket key. Its content so far (Ava's
            // greeting, still streaming) isn't needed locally — re-entering
            // the originating workspace triggers a fresh server replay via
            // attachToSession regardless, and leaving it here would risk
            // colliding with the next unrelated no-session turn that reuses
            // the same '__pending' key.
            deleteBucket('__pending')
            queryClient.invalidateQueries({ queryKey: ['sessions'] })
            // A message that collided with this kickoff's slot (via
            // the `sendMessage` collision guard) sits in `outboundQueue` —
            // nothing else will release it now that this kickoff has
            // resolved via this (non-foregrounding) branch.
            get().drainOutboundQueue()
            break
          }

          // Plain sendMessage ack, OR a kickoff ack while the user is still
          // on the workspace that triggered it — foreground exactly as
          // before (byte-for-byte unchanged from the pre-fix logic).
          //
          // Register in session store and create the bucket.
          //
          // Only adopt the server's agent while the selection is still the one
          // this mint was sent under. If the user switched the picker while the
          // ack was in flight, their newer explicit choice wins — a stale echo
          // must not silently reassign the agent out from under them.
          const currentAgentId = useSessionStore.getState().activeAgentId
          const userReselected =
            runtime.agentIdAtLastMintSend !== null && currentAgentId !== runtime.agentIdAtLastMintSend
          runtime.agentIdAtLastMintSend = null
          useSessionStore
            .getState()
            .setActiveSession(newSid, userReselected ? currentAgentId : (frame.agent_id ?? currentAgentId))
          // Bucket is lazily created by first withBucket call; ensure it exists now
          // so the foreground syncs immediately.
          // FR-21 / T21–T25: session_started fires when the server begins a new turn
          // for a message sent without a session_id. The agent is about to stream —
          // pre-set isStreaming:true so the Stop button appears immediately without
          // waiting for the first token or tool_call_start frame.
          //
          // #253(a): If a '__pending' bucket exists (from the no-session optimistic
          // render path), migrate its messages into the real session bucket so the
          // user sees a continuous conversation rather than a blank slate + re-render.
          set((state) => {
            const pendingBucket = state.sessionsById['__pending']
            if (state.sessionsById[newSid]) {
              // Real bucket already exists — drop the pending bucket if present.
              if (!pendingBucket) return {}
              const sessionsById = { ...state.sessionsById }
              delete sessionsById['__pending']
              return { sessionsById }
            }
            // Migrate pending bucket messages into the new bucket, or start fresh.
            const baseBucket: SessionChatState = pendingBucket
              ? {
                  ...pendingBucket,
                  isStreaming: true,
                  lastUserMessageAt: Date.now(),
                }
              : { ...emptySessionState(), isStreaming: true }
            const sessionsById = { ...state.sessionsById, [newSid]: baseBucket }
            // Remove the temporary pending bucket.
            delete sessionsById['__pending']
            return { sessionsById, ...bucketToForeground(baseBucket) }
          })
          // Invalidate sessions list so the session lists (SearchModal, sidebar
          // accordion) re-fetch and show the new session.
          queryClient.invalidateQueries({ queryKey: ['sessions'] })
          // A steer sent while this turn was still under the '__pending'
          // placeholder sid (see sendMessage's isStreaming branch) was
          // buffered into outboundQueue instead of sent — drainOutboundQueue
          // was previously only invoked on WS reconnect (OmnipusRuntimeProvider),
          // so without this call that buffered steer would sit inert until
          // the NEXT disconnect/reconnect cycle, which may never happen in a
          // healthy session. Draining here moves it into pendingDrainQueue;
          // maybeDrainNext() (called inside drainOutboundQueue) reads
          // isStreaming fresh via get() — which bucketToForeground above just
          // set to true for this brand-new turn — so it correctly no-ops now
          // and the buffered message goes out automatically as an ordinary
          // next-turn message once THIS turn's own done/error frame calls
          // maybeDrainNext() again. A no-op (queue empty) the vast majority
          // of the time, so unconditional here is cheap and safe.
          get().drainOutboundQueue()
          break
        }

        case 'token':
          if (targetSid) {
            withBucket(targetSid, (b) => {
              return produce(b, (draft) => {
                let lastMsgId = findLastAssistantMessageId(draft.messageOrder, draft.messagesById)
                // FR-21 / T21–T26: if the last assistant message was already
                // interrupted (user clicked Stop / pressed Escape / used /cancel),
                // discard any trailing tokens the server sends before it processes
                // the cancel. markLastMessageInterrupted() sets isStreaming:false on
                // the message so that AssistantUI renders the correct "incomplete/cancelled"
                // status. We must NOT append to the interrupted message or create a new
                // placeholder — either would erase the (interrupted) label or create a
                // ghost streaming bubble without the label.
                if (lastMsgId && draft.messagesById[lastMsgId].status === 'interrupted') return
                // Same rule, same reason, for a terminally errored bubble.
                //
                // A failed turn delivers its explanation TWICE by design, on two
                // independent paths: the typed `error` frame (rich — code,
                // retryable, detail, Retry button), and then a plain `token` +
                // `done` from the outbound-publish fallback that exists so a
                // turn which produced no tokens still terminates the stream
                // instead of leaving a stuck "thinking" spinner
                // (pkg/gateway/websocket.go, wsStreamer.Finalize's markStreamed
                // guard). Both carry the same user-facing sentence.
                //
                // Without this guard the trailing token crosses the closed-bubble
                // segment boundary below and mints a SECOND bubble holding a
                // duplicate of the text already shown — and the duplicate carries
                // none of the error treatment, so the copy the user is most
                // likely to read is the one with no Retry button. Worse, the two
                // paths classify independently (the frame from the provider's
                // HTTP status, the fallback from the error STRING via
                // TranslateTurnError with a nil ProviderError), so they can
                // legitimately disagree and show the user two different
                // explanations for one failure.
                //
                // Discarding here rather than suppressing server-side is
                // deliberate: the client is the only party that KNOWS the error
                // frame arrived. A backend suppression would have to assume
                // delivery, and a wrong assumption there turns a duplicate
                // message into no message at all — trading a cosmetic defect for
                // the silence this whole fix exists to remove.
                if (lastMsgId && draft.messagesById[lastMsgId].status === 'error') return
                // Track the bubble being left behind by either boundary check
                // below so any tool calls it already holds can be baked onto
                // it (see the baking block right after both checks) instead
                // of silently accumulating in the shared bucket-level
                // toolCallOrder to be baked at `done` time onto whichever
                // bubble happens to be LAST at that point — which, once a
                // turn produces more than one bubble (Fix 5a; the Bug 2
                // sync/await-mode attribution fix), may be a completely
                // different producer's bubble than the one the tool call
                // actually started on.
                let abandonedMsgId: string | null = null
                // Empty-response variant of the "delivered twice" defect
                // documented on the status==='error' guard above: when the
                // LLM call itself produced no tokens at all (not a
                // classified error — the engine's success-path empty-content
                // fallback, pkg/agent/loop.go's `defaultResponse` sentinel),
                // NO `error` frame is ever sent, so the guard above never
                // fires. The turn still finalizes the optimistic placeholder
                // via `done` with content:'' (nothing was ever streamed to
                // abandon it against), and THEN webchatChannel.Send()'s
                // markStreamed fallback (pkg/gateway/webchat_channel.go —
                // markStreamed is only called when `accumulated.Len() > 0`)
                // delivers the actual fallback text as a second token+done
                // pair so the turn doesn't strand the user on a stuck
                // "thinking" spinner. Without this check, the boundary rule
                // right below (closed bubble = new segment) abandons the
                // now-closed EMPTY placeholder and mints a brand-new bubble
                // for that fallback text — leaving the original placeholder
                // stranded on screen as a permanent empty bubble with a Copy
                // button that copies nothing (D-fix's terminal-empty
                // variant). A closed bubble that finalized holding
                // absolutely nothing — no text, no tool call, no media, no
                // subagent span — was never actually shown as content, so
                // reusing it here (rather than abandoning it) collapses the
                // two deliveries back into the single bubble the user
                // actually needs to see.
                const lastMsg = lastMsgId ? draft.messagesById[lastMsgId] : null
                const lastMsgIsEmptyTerminal =
                  !!lastMsg &&
                  !lastMsg.isStreaming &&
                  !lastMsg.content?.trim().length &&
                  !lastMsg.tool_calls?.length &&
                  !lastMsg.media?.length &&
                  !lastMsg.spans?.length
                // Only reuse the last assistant bubble if it is still
                // streaming (or, per the empty-terminal case just above, if
                // it finalized holding nothing at all). A closed bubble
                // that DID hold something (status=done, real content/tool
                // calls/media/spans already shown) means the prior LLM call
                // has finalized and any new tokens are part of a *new*
                // turn-segment — typically a follow-up call after a tool
                // returned. Stuffing them back into that closed bubble is
                // what produced the "text-then-image-at-bottom" ordering.
                if (lastMsgId && !draft.messagesById[lastMsgId].isStreaming && !lastMsgIsEmptyTerminal) {
                  abandonedMsgId = lastMsgId
                  lastMsgId = null
                }
                // Delegate-attribution fix: a still-streaming bubble is ALSO a
                // "new segment" boundary when the incoming frame's producer
                // differs from the bubble's already-known producer. Without
                // this, a background delegate's own token stream — which per
                // Fix 5a correctly carries the DELEGATE's agent_id on the wire
                // — lands on the delegator's still-open bubble (the delegator's
                // turn is not "done" while it waits on the delegate) and the
                // unconditional `msg.agentId = frame.agent_id` write below
                // silently relabels the ENTIRE bubble — including the
                // delegator's own already-rendered lead-in reasoning text — as
                // the delegate's. That produced both the persisted
                // misattribution (the delegate's tokens are the last writer
                // before the bubble finalizes) and the transient live flicker
                // (the delegator's own follow-up tokens later re-claim it).
                // Only split when BOTH ids are known and they actually
                // disagree — an unset bubble agentId (e.g. the optimistic
                // placeholder from sendMessage()) or a frame that omits
                // agent_id (legacy) must keep the existing permissive
                // behavior.
                if (
                  lastMsgId &&
                  frame.agent_id &&
                  draft.messagesById[lastMsgId].agentId &&
                  draft.messagesById[lastMsgId].agentId !== frame.agent_id
                ) {
                  abandonedMsgId = lastMsgId
                  lastMsgId = null
                }
                // Copy (not move) any tool calls OWNED by the bubble we are
                // abandoning onto it, BEFORE it stops being "the last
                // message". ChatScreen.tsx's VirtualizedMessageListInner
                // renders every message except the CURRENT last one via the
                // historical VirtualAssistantMessageRow — which only shows
                // tool calls already present in message.tool_calls, not the
                // live bucket — so the instant a NEW bubble opens (this
                // frame), the abandoned bubble is no longer last and stops
                // being live-rendered. Without this, an unbaked tool call
                // would stay invisible until a turn-end bake (`done`,
                // `clearStreamingState`) finally routes it.
                //
                // Deliberately a COPY, not a move: the call may still be
                // 'running' (e.g. the delegator's own "Delegate task" call —
                // its tool_call_result only arrives once the delegate's
                // whole sub-turn, including its reply tokens that just
                // triggered this very abandonment, has finished). The live
                // bucket entry (toolCalls/toolCallOrder/textAtToolCallStart)
                // is deliberately left untouched here so tool_call_result
                // can still update it, and the eventual turn-end bake
                // (bakeToolCallsByOwner, routed via toolCallOwnerMessageId)
                // overwrites this copy with the final resolved status —
                // baking a 'running' snapshot here and then clearing the
                // live bucket would freeze the pill at "Running..." forever,
                // since nothing would be left to apply the later result to.
                if (abandonedMsgId && draft.toolCallOrder.length > 0) {
                  const ownedIds = draft.toolCallOrder.filter(
                    (id) => draft.toolCalls[id] && draft.toolCallOwnerMessageId?.[id] === abandonedMsgId,
                  )
                  if (ownedIds.length > 0) {
                    bakeToolCallsByOwner(draft.messagesById, ownedIds, draft.toolCalls, draft.toolCallOwnerMessageId ?? {}, abandonedMsgId, draft.textAtToolCallStart)
                  }
                }
                if (!lastMsgId) {
                  const placeholder: ChatMessage = {
                    id: generateId(),
                    role: 'assistant',
                    content: '',
                    timestamp: new Date().toISOString(),
                    status: 'streaming',
                    isStreaming: true,
                    // Fix 5a: prefer the real producer's agent id (populated by the
                    // backend at token-emission time) over the client-side
                    // activeAgentId guess — the guess is wrong for background/
                    // delegated sub-turns where the true producer isn't "whoever
                    // the user happens to be chatting with". Falls back to the
                    // guess only for legacy/older frames that omit agent_id.
                    agentId: frame.agent_id ?? useSessionStore.getState().activeAgentId ?? undefined,
                  }
                  draft.messagesById[placeholder.id] = placeholder
                  draft.messageOrder.push(placeholder.id)
                  lastMsgId = placeholder.id
                }
                const msg = draft.messagesById[lastMsgId]
                // Fix 5a (cont.): the bubble being appended to here may be the
                // OPTIMISTIC placeholder created synchronously in sendMessage()
                // (which has no agentId yet — the true producer isn't known at
                // send time) rather than the one created above. Stamp/refresh the
                // attribution from the frame as soon as the backend tells us,
                // rather than only at placeholder-creation time — this is the
                // path that actually fires for the common "user message → agent
                // reply" case. Strict improvement: only overrides when the frame
                // carries agent_id; otherwise the existing value (possibly
                // undefined, falling back to activeAgentId at render time) is
                // left untouched — no behavior change for legacy frames.
                if (frame.agent_id) {
                  msg.agentId = frame.agent_id
                }
                // #823 catch-up redesign (BE-DESIGN.md §6.3): TokenFrame now
                // carries turn_id (Step 0's contract change) — stamp it the
                // same way agentId is stamped above, additive-only (never
                // overwrites an already-known value with a different one,
                // same permissive rule as the agentId boundary check).
                if (frame.turn_id && !msg.turnId) {
                  msg.turnId = frame.turn_id
                }
                // Consume the seam marker: a tool call started on this bubble
                // since the last token was appended, so this frame begins a
                // new logical unit — insert a paragraph break instead of
                // gluing it directly onto the trailing narration (live-UAT
                // regression — e.g. "...now.Now delegating…" / "...inline:ping"
                // with zero separator).
                if (msg.pendingTextBoundary) {
                  if (msg.content) msg.content += '\n\n'
                  msg.pendingTextBoundary = false
                }
                msg.content = msg.content + frame.content
                applyTokenStreamingState(draft, msg)
                // ADR-082 review S1/CR1: a token proves the announced turn's
                // bubble now exists, regardless of which frame order got us
                // here (fixed-contract session_state-first, an older
                // gateway's session_state-last, or anything racing in
                // between). Without this, an out-of-order attach where a
                // token arrives before the replay-terminating `done` ever
                // gets a chance to open the placeholder (see the 'done' case
                // below) would leave `activeTurnBubbleOpened` false while a
                // real, content-bearing bubble is already streaming — and
                // the turn's OWN done would then misclassify itself as
                // "still awaiting catch-up" and open a second, empty
                // placeholder instead of finalizing this one.
                if (draft.activeTurnId) {
                  draft.activeTurnBubbleOpened = true
                }
              }) as Partial<SessionChatState>
            })
          }
          break

        case 'done':
          if (targetSid) {
            // B1.3d: when done arrives for a targetSid that isn't in sessionsById yet,
            // the session was probably switched away mid-stream. The active bucket's
            // isStreaming flag would otherwise stay true forever (infinite spinner).
            // Log a diagnostic warning and conditionally force-clear isStreaming on
            // the active bucket.
            //
            // H1-FE: Guard against corrupting an active mid-stream session.
            // Two cases where we must NOT force-clear the active bucket:
            //   1. targetSid === activeSid — the active session itself produced an
            //      unknown-sid done, which should never happen; the normal path below
            //      will handle it correctly, so do not fall through to the break.
            //   2. The active bucket sent a user message recently (< 10 s ago) and is
            //      still streaming — the done belongs to a different (wiped/replayed)
            //      session and the active spinner is legitimate.
            const knownSid = !!get().sessionsById[targetSid]
            if (!knownSid) {
              console.warn('chat.done_unknown_sid', { targetSid, activeSid: activeSid })
              logDiagnostic('chatDoneUnknownSid', { targetSid, activeSid })
              const STREAM_GRACE_MS = 10_000
              if (activeSid && activeSid !== targetSid && get().sessionsById[activeSid]) {
                const activeBucket = get().sessionsById[activeSid]!
                const isActiveMidStream =
                  activeBucket.isStreaming &&
                  activeBucket.lastUserMessageAt !== null &&
                  Date.now() - activeBucket.lastUserMessageAt < STREAM_GRACE_MS
                if (!isActiveMidStream) {
                  // Active bucket spinner is likely a stale remnant from the wiped
                  // session — safe to clear.
                  withBucket(activeSid, () => ({ isStreaming: false }))
                  maybeDrainNext()
                } else {
                  console.warn('chat.done_unknown_sid_skipped_active_mid_stream', {
                    targetSid,
                    activeSid,
                    lastUserMessageAt: activeBucket.lastUserMessageAt,
                  })
                  logDiagnostic('chatDoneUnknownSidSkippedActiveMidStream', {
                    targetSid,
                    activeSid,
                    lastUserMessageAt: activeBucket.lastUserMessageAt,
                  })
                }
              } else {
                // Defensive (boundary case, not an observed failure): no active
                // bucket to force-clear here — e.g. no active session at all, or
                // its bucket doesn't exist yet. isStreaming may already be false
                // via other means, but drain anyway so a message queued behind
                // this unknown-sid done can't get permanently stranded.
                maybeDrainNext()
              }
              break
            }

            // Decide whether isReplaying must clear now vs. defer to a setTimeout.
            // The clear happens INSIDE the same withBucket return below — never via
            // a nested withBucket call, because the outer set() commits the bucket
            // last and clobbers any nested writes that ran during the updater.
            const sid = targetSid
            // F-S3: this session's cancellation (if any) has now been
            // terminally acknowledged — stop treating it as "pending" so a
            // later, unrelated untagged frame doesn't get misattributed here.
            pendingCancelAckSids.delete(sid)
            const priorBucket = get().sessionsById[sid] ?? EMPTY_BUCKET
            const wasReplaying = priorBucket.isReplaying
            const elapsed = wasReplaying ? Date.now() - (replayingStartedAt[sid] ?? 0) : 0
            // FR-I-014: mirror the same MIN_REPLAY_DISPLAY_MS used in setReplaying above.
            // Both code paths that clear isReplaying must use the same threshold.
            const MIN_REPLAY_DISPLAY_MS = 750
            const clearReplayingNow = wasReplaying && elapsed >= MIN_REPLAY_DISPLAY_MS
            if (wasReplaying) {
              sawReplayMessageThisTurn[sid] = false
              if (!clearReplayingNow) {
                if (replayingClearTimers[sid]) {
                  clearTimeout(replayingClearTimers[sid])
                }
                replayingClearTimers[sid] = setTimeout(() => {
                  delete replayingClearTimers[sid]
                  withBucket(sid, () => ({ isReplaying: false }))
                }, MIN_REPLAY_DISPLAY_MS - elapsed)
              }
            }
            // ADR-082 D3/D4 (FR-007/FR-009), review S1/CR1: a `done` frame
            // arrives TWICE for a mid-turn attach — once marking the end of
            // transcript replay (carries `stats.frames_emitted`, never
            // `stats.tokens`/`stats.cost` — pkg/gateway/replay.go's
            // terminator emit), and again later when the announced turn
            // itself actually finishes (`stats.tokens`/`stats.cost` always
            // stamped, even a zero-token turn — pkg/gateway/websocket.go's
            // Finalize). Tell them apart PURELY by this stats shape — never
            // by activeTurnId/activeTurnBubbleOpened/isReplaying state. The
            // gateway contract fixes the wire order (session_state →
            // replay_message* → replay-terminator done → catch-up token →
            // live token* → the turn's own done), but this store must not
            // assume any particular order arrived: an older gateway sent
            // session_state LAST, and even under the fixed contract a fast
            // concurrent turn can race its own done ahead of the replay
            // terminator. Classifying by activeTurn* state alone (the
            // pre-review version of this code) broke under both: with
            // session_state arriving late, the turn's REAL done would find
            // activeTurnId set && activeTurnBubbleOpened still false (no
            // frame had ever flipped it — see the 'token' case's own fix)
            // and wrongly treat itself as the replay terminator, opening a
            // second empty placeholder and `break`ing without ever
            // finalizing the real, content-bearing bubble — permanent Stop,
            // locked composer.
            const doneStats = frame.stats
            const isReplayTerminatorDone =
              doneStats?.frames_emitted !== undefined &&
              doneStats?.tokens === undefined &&
              doneStats?.cost === undefined
            if (isReplayTerminatorDone) {
              // This done marks the end of transcript replay only — it is
              // NEVER the signal to finalize a bubble. If session_state
              // already announced a turn for this session and no bubble has
              // opened for it yet (the ordinary, in-order case), open the
              // empty streaming placeholder now: this IS the correct
              // position for it, because every replay_message for this
              // attach has already landed (pushed onto messageOrder in
              // arrival order, strictly before this done — see case
              // 'replay_message' above) — and let the catch-up token (case
              // 'token' above) append into it exactly like the first token
              // of any ordinary turn. If a bubble is already open (an
              // out-of-order token beat this terminator here) or no turn
              // was announced at all, there is nothing to open — just let
              // the isReplaying clear/defer above stand.
              // FX-E (ADR-082 D9): bake any tool calls still left over from
              // replay reconstruction before this replay-terminator done —
              // closes the remaining gap the 'replay_message' case's own
              // fix (see its bake immediately before constructing a new
              // NON-assistant-role message) doesn't cover: a tool call that
              // is the LITERAL LAST transcript entry, with no further
              // message of ANY role replayed after it. Without this, that
              // call stays stranded in toolCallOrder forever whenever the
              // session has no live turn to continue (the ordinary
              // completed-session reload case) — never reaching
              // message.tool_calls, so its dedicated renderer (e.g.
              // SetGoalCardBlock) never sees it on reload.
              if (priorBucket.toolCallOrder.length > 0) {
                withBucket(sid, (b) => {
                  if (b.toolCallOrder.length === 0) return {}
                  return produce(b, (draft) => {
                    const fallbackMsgId = findLastAssistantMessageId(draft.messageOrder, draft.messagesById)
                    bakeToolCallsByOwner(draft.messagesById, draft.toolCallOrder, draft.toolCalls, draft.toolCallOwnerMessageId ?? {}, fallbackMsgId, draft.textAtToolCallStart)
                    draft.toolCalls = {}
                    draft.toolCallOrder = []
                    draft.textAtToolCallStart = {}
                    draft.toolCallOwnerMessageId = {}
                  }) as Partial<SessionChatState>
                })
              }
              const awaitingCatchUp =
                !!priorBucket.activeTurnId && !priorBucket.activeTurnBubbleOpened
              if (awaitingCatchUp) {
                withBucket(sid, (b) => {
                  return produce(b, (draft) => {
                    const placeholder: ChatMessage = {
                      id: generateId(),
                      role: 'assistant',
                      content: '',
                      timestamp: new Date().toISOString(),
                      status: 'streaming',
                      isStreaming: true,
                      agentId: draft.activeTurnAgentId ?? undefined,
                    }
                    draft.messagesById[placeholder.id] = placeholder
                    draft.messageOrder.push(placeholder.id)
                    draft.activeTurnBubbleOpened = true
                    draft.replayCompletedForSession = draft.isReplaying ? sid : draft.replayCompletedForSession
                    if (clearReplayingNow) {
                      draft.isReplaying = false
                    }
                  }) as Partial<SessionChatState>
                })
              } else if (clearReplayingNow) {
                withBucket(sid, () => ({ isReplaying: false }))
              }
              // Mirrors the normal finalization path's own drain call below —
              // harmless here too (maybeDrainNext no-ops while isStreaming).
              maybeDrainNext()
              break
            }
            // ADR-087 D2 (finding #10) — live-path counterpart of the
            // `replayTruncated`/`replayTruncationReason` pair computed for
            // 'replay_message' above. Same `normalizeTruncationReason`
            // legacy-default rule, same wire shape, different carrier
            // (DoneStats instead of ReplayMessageFrame) so a turn truncated
            // while the user is watching renders the suffix immediately.
            const doneTruncated = doneStats?.truncated === true
            const doneTruncationReason = normalizeTruncationReason(
              doneStats?.truncated,
              doneStats?.truncation_reason,
            )
            withBucket(sid, (b) => {
              return produce(b, (draft) => {
                const lastMsgId = findLastAssistantMessageId(draft.messageOrder, draft.messagesById)
                // Sweep the UNION of {the last assistant message} (unchanged
                // — always normalized exactly as before, even when it isn't
                // flagged "streaming" at all, e.g. a replay-reconstructed
                // bubble whose `isStreaming` is `undefined` rather than
                // `true`/`false` — see the 'replay_message' case's own doc
                // comment on why replay bubbles are finalized the instant
                // they're created) ∪ {every OTHER still-streaming assistant
                // message in the bucket} (defense-in-depth addition —
                // mirrors clearStreamingState's own sweep, used on a hard
                // WS-drop, see the identical backward-scan loop there). A
                // mid-turn steer (sendMessage's `isStreaming` branch) appends
                // the steering text as a new USER message AFTER the
                // still-open assistant bubble, so that bubble is no longer
                // "the last message" by the time `done` arrives — finalizing
                // only the last assistant message would leave the ORIGINAL
                // bubble permanently stuck at isStreaming:true (permanent
                // shimmer, Copy bar suppressed) even though the turn is over.
                for (let i = draft.messageOrder.length - 1; i >= 0; i--) {
                  const id = draft.messageOrder[i]
                  const m = draft.messagesById[id]
                  if (m?.role !== 'assistant') continue
                  if (id !== lastMsgId && !m.isStreaming && m.status !== 'streaming') continue
                  // FR-21 / T21–T25: do NOT overwrite 'interrupted' status with 'done'.
                  //
                  // 'error' is preserved for the same reason, and it is not a
                  // theoretical case: a failed turn emits its typed `error`
                  // frame and then, microseconds later, the streamer's
                  // deferred finalize emits `done` for the SAME session. The
                  // error bubble IS lastMsgId, so the `continue` above does not
                  // protect it, and demoting it to 'done' silently erased every
                  // bit of the error treatment the frame had just earned:
                  // the "Error" label, the Retry button (AssistantUI maps
                  // 'error' → incomplete), and the verbose "Technical details"
                  // disclosure. errorCode was left set, so the bubble ended in
                  // a state no renderer can act on — status 'done' carrying an
                  // errorCode, while every renderer gates on status === 'error'.
                  //
                  // A terminal status is terminal. `done` means "the turn is
                  // over", which is already true of an errored turn; it must
                  // never be read as "the turn succeeded". The rendering layer
                  // owns this property rather than inheriting it from whatever
                  // order the backend happens to emit its frames in.
                  m.isStreaming = false
                  m.status =
                    m.status === 'interrupted' || m.status === 'error' ? m.status : 'done'
                  // Clear the tool-call text-boundary marker on finalize. If the
                  // turn's last event was a tool call with no trailing narration
                  // token before `done`, pendingTextBoundary would otherwise be
                  // left `true` on a message with no next token coming — a
                  // representable-but-meaningless state for a finalized bubble.
                  m.pendingTextBoundary = false
                  // ADR-087 D2 (finding #10): the live `done` frame carries the
                  // same truncation signal ReplayMessageFrame carries on
                  // reattach (DoneStats.truncated/truncation_reason mirror
                  // Message.truncated/truncation_reason). Stamp it on the
                  // bubble the turn actually finished on (lastMsgId) so a
                  // turn cut off at the output limit — or cancelled — renders
                  // its suffix immediately, without waiting for a reload or a
                  // reconnect to replay it in. Only lastMsgId: the other
                  // still-streaming bubbles this sweep also finalizes (the
                  // mid-turn-steer defense-in-depth case above) are not the
                  // entry the backend actually marked truncated.
                  if (id === lastMsgId && doneTruncated) {
                    m.truncated = true
                    m.truncationReason = doneTruncationReason
                  }
                }
                // Bake any pending tool calls into the last assistant message so
                // VirtualAssistantMessageRow can render them from message.tool_calls.
                // This covers two cases:
                //   1. Replay: replay_message coalesces into the empty placeholder and
                //      returns before baking; done is the only signal that all frames
                //      for the final entry are complete.
                //   2. Live turns: tool calls stay in toolCallOrder until the next
                //      sendMessage bakes them, causing them to disappear the moment
                //      isStreaming goes false and the message moves to the historical
                //      renderer (VirtualAssistantMessageRow reads message.tool_calls, not
                //      the bucket live map). Confirmed: ChatScreen.tsx switches to
                //      VirtualAssistantMessageRow at isStreaming=false, so baking at
                //      done is required for live turns too (hotfix/v0.1.1 aff2caa).
                // Routed via toolCallOwnerMessageId (falling back to
                // lastMsgId for unmapped/legacy calls) rather than blindly
                // "the last message" — a turn can now legitimately produce
                // more than one assistant bubble (Fix 5a; the sync/await-
                // mode delegate attribution fix), and each tool call must
                // land on the bubble that actually issued it, not whichever
                // bubble happens to be last when the turn ends.
                if (draft.toolCallOrder.length > 0) {
                  bakeToolCallsByOwner(draft.messagesById, draft.toolCallOrder, draft.toolCalls, draft.toolCallOwnerMessageId ?? {}, lastMsgId, draft.textAtToolCallStart)
                  draft.toolCalls = {}
                  draft.toolCallOrder = []
                  draft.textAtToolCallStart = {}
                  draft.toolCallOwnerMessageId = {}
                }
                const tokenDelta = frame.stats?.tokens ?? 0
                const costDelta = frame.stats?.cost ?? 0
                draft.isStreaming = false
                draft.sessionTokens = draft.sessionTokens + tokenDelta
                draft.sessionCost = draft.sessionCost + costDelta
                draft.replayCompletedForSession = draft.isReplaying ? sid : draft.replayCompletedForSession
                draft.cancelStage = null
                // ADR-082 D4 (FR-009 "finalize once"), review S1/S2: this is
                // the turn's OWN done — the isReplayTerminatorDone branch
                // above always `break`s before reaching here, so every path
                // that gets here carries real turn stats (or is the
                // stats-less outbound-publish fallback done, which is also
                // always a real turn's own completion — see
                // pkg/gateway/webchat_channel.go). The announced turn, if
                // any, is now finalized: record its id as finished BEFORE
                // clearing so a stale/racing session_state.active_turn that
                // re-announces this same turn_id later (S2) is recognized
                // and ignored rather than re-opening streaming state with no
                // second done ever coming to close it. Then clear
                // activeTurnId/activeTurnAgentId/activeTurnBubbleOpened so a
                // later, unrelated replay-terminating done for this session
                // never mistakes a stale id for a still-open turn.
                markTurnFinished(sid, draft.activeTurnId)
                draft.activeTurnId = null
                draft.activeTurnAgentId = null
                draft.activeTurnBubbleOpened = false
                draft.terminalCatchUpPending = needsTerminalCatchUp(priorBucket, wasReplaying)
                if (clearReplayingNow) {
                  draft.isReplaying = false
                }
              }) as Partial<SessionChatState>
            })
            // The turn that just completed may be one we sent from the
            // offline-queue drain — send the next queued message, if any.
            maybeDrainNext()
          } else {
            // Defensive (boundary case, not an observed failure): a 'done'
            // frame with no session_id at all (a protocol-violating/malformed
            // frame) skips the whole block above, including the
            // maybeDrainNext() call inside it. isStreaming may already be
            // false via other means, but drain anyway so a message queued
            // behind this malformed done can't get permanently stranded.
            maybeDrainNext()
          }
          break

        case 'error':
          {
            // ADR-051 — typed error payload. Live ErrorFrame optionally carries
            // `payload.llm_error` (a stable, enumerated code + message + an
            // optional verbose `detail`) and (on the replay sibling) an
            // `entry_id`. Legacy frames (pre-ADR-051, gateway-synthesized
            // cancel-acks, etc.) lack the typed payload; for those, fall back
            // to `frame.message` verbatim — exactly the pre-ADR-051 behavior.
            // `errorEntryId` powers the live→replay dedup (a reloaded session
            // must not re-render the same error twice). Read once here at the
            // top so all three error sub-paths below (kickoff reject, no-bucket
            // sweep, per-bucket render) see the same values.
            const llmError = readLLMErrorFromFrame(frame)
            const errorEntryId = readEntryIdFromFrame(frame)
            const verboseChatEnabled = useChatPreferencesStore.getState().verboseChatEnabled
            // D5 fix (UAT Site 3): a legacy/synthesized ErrorFrame (no typed
            // llm_error payload) falls back to the raw wire `message` for
            // display in several branches below (translatedMessage here, the
            // kickoff-reject toast, setConnectionError x2, and the
            // coalesced/fresh bubble content). Sanitize ONCE up front so
            // every one of those reads the same safe value — see
            // sanitizeLegacyErrorMessage's doc comment (lib/llm-error.ts) for
            // what it catches. Pattern-matching checks below (isCancelAck,
            // isDuplicate, console.warn/logDiagnostic) intentionally keep
            // reading `frame.message` directly — the original wire string,
            // not the sanitized display copy — since those are content
            // classifiers/logs, not user-facing text.
            const safeMessage = sanitizeLegacyErrorMessage(frame.message ?? '')
            // Resolve the visible message: translated code→display copy when
            // the typed payload is present, else the sanitized `frame.message`
            // (legacy). `errorDetail` is non-undefined only when verbose is on
            // AND detail was actually carried — the renderers key off its
            // presence to decide whether to mount the "Technical details"
            // disclosure.
            const { message: translatedMessage, detail: errorDetail } = llmError
              ? getLLMErrorDisplay(llmError, verboseChatEnabled)
              : { message: safeMessage, detail: undefined }
            // ADR-051 — live→replay dedup. If an error bubble carrying the
            // same `errorEntryId` is already in the foreground bucket (or any
            // bucket routed below), do not push a second one. Pre-checks the
            // FOREGROUND bucket here; the per-bucket branch below re-checks
            // against the routed bucket's own messages so a background-session
            // duplicate is also caught. (Live error then reload → the replay
            // path's case 'replay_error' performs the symmetric check.)
            const errorDedupId = errorEntryId

            // A kickoff rejection (duplicate kickoff, unknown
            // workspace, or a server-side kickoff error) is a hard REJECT of
            // a turn that never really started — there is no real session
            // behind it, only the local '__pending' sentinel. A kickoff
            // rejection therefore NEVER carries a session_id on the wire
            // (there is no real session to tag it with) — a
            // session-TAGGED error frame is by definition never a kickoff
            // reject, so `!frameSessionId` gates the ENTIRE kickoff-error
            // branch (both sub-cases below), never just the still-foreground
            // one. A genuinely tagged error always falls through unchanged
            // to the generic routing further down.
            const rejectedKickoff = get().pendingKickoff
            if (rejectedKickoff && !frameSessionId) {
              const sessionStillPending = useSessionStore.getState().activeSessionId === '__pending'
              // "already ran" style messages are a benign DUPLICATE — an
              // EARLIER attempt already succeeded server-side (setup_pending
              // is already correctly false), not a real failure. Everything
              // else is a genuine failure, worded differently so the user
              // knows re-opening the workspace will retry it.
              const isDuplicate = /already/i.test(frame.message ?? '')
              // `abandonPendingKickoffInternal` handles the shared cleanup
              // trio for BOTH sub-cases below: clear `pendingKickoff`, drop
              // the orphaned '__pending' bucket, and mark this workspace's
              // `kickoffAttemptStatus` 'failed' so
              // useWorkspaceSetupKickoff's invalidate-on-failure effect can
              // react and roll back its own premature optimistic
              // setup_pending:false cache write.
              abandonPendingKickoffInternal()
              console.warn('chat.workspace_setup_kickoff_rejected', { message: frame.message, duplicate: isDuplicate, stillForeground: sessionStillPending })
              logDiagnostic('chatWorkspaceSetupKickoffRejected', { message: frame.message, duplicate: isDuplicate, stillForeground: sessionStillPending })
              if (sessionStillPending) {
                // The kickoff's placeholder is still what the user is
                // looking at — reset the composer back to a normal, empty
                // state (not to be left as a stuck 'error'-status bubble
                // under '__pending', which would otherwise poison every
                // subsequent sendMessage call with a protocol-violating
                // session_id:'__pending' frame — see sendMessage's composer
                // guard) and surface a toast.
                useSessionStore.getState().setActiveSession(null)
                useUiStore.getState().addToast(
                  isDuplicate
                    ? {
                        message: 'Workspace setup already ran — find the interview in your sessions list.',
                        variant: 'default',
                      }
                    : {
                        // D5 fix (Site 3): safeMessage (computed once at the
                        // top of this case block) instead of the raw
                        // frame.message.
                        message: frame.message
                          ? `Could not start the workspace setup interview: ${safeMessage}`
                          : 'Could not start the workspace setup interview — reopen the workspace to retry.',
                        variant: 'warning',
                      },
                )
              }
              // Reject-after-navigation fall-through: when the
              // kickoff's placeholder is NO LONGER the local foreground
              // (the user already navigated away, or attached to a
              // different real session — either resets `activeSessionId`
              // away from '__pending'), this untagged reject belongs to a
              // turn nothing is currently showing. No toast — there is
              // nothing on screen to retry FROM; the workspace-open hook is
              // what will retry, once it re-checks server truth on the next
              // open. Swallow quietly and do NOT let this frame fall through
              // to the generic routing below — it would otherwise
              // misattribute to whatever IS foreground (erroring an
              // unrelated conversation's last bubble, or — if nothing is
              // foreground — raising a bogus global connection-error banner
              // via the `!targetSid` branch just below).
              //
              // Only `drainOutboundQueue()` (not `maybeDrainNext()`,
              // which the ORIGINAL version of this branch used — a bug: it
              // only ever moves `pendingDrainQueue`, never `outboundQueue`,
              // so it never actually freed a message buffered by the
              // collision guard or mid-turn '__pending' steering) releases
              // whatever is actually queued.
              get().drainOutboundQueue()
              break
            }

            // C8: a terminal error frame must always resolve the in-flight turn.
            // When the frame can't be routed to a bucket (no active session /
            // missing session_id in production), fall back to a global sweep so
            // no bucket is left wedged in a streaming state.
            if (!targetSid) {
              const isCancelAck = /turn.cancel/i.test(frame.message ?? '')
              if (!isCancelAck) {
                // D5 fix (Site 3): safeMessage, not the raw frame.message —
                // this is the global connection-error banner (AppShell),
                // visible on every screen.
                useConnectionStore.getState().setConnectionError(safeMessage)
              }
              get().clearStreamingState()
              break
            }
            // An error frame arriving during replay must also clear isReplaying —
            // otherwise the session is permanently wedged behind the "Loading
            // session history…" overlay with a disabled composer, and (unlike the
            // done path) nothing else ever clears it. Mirror the done-frame logic:
            // clear immediately once MIN_REPLAY_DISPLAY_MS has elapsed, otherwise
            // defer to a timer (cancelling any stale one first).
            const sid = targetSid
            // F-S3: see the matching comment in the 'done' case above.
            pendingCancelAckSids.delete(sid)
            const wasReplaying = (get().sessionsById[sid] ?? EMPTY_BUCKET).isReplaying
            const replayElapsed = wasReplaying ? Date.now() - (replayingStartedAt[sid] ?? 0) : 0
            const MIN_REPLAY_DISPLAY_MS = 750
            const clearReplayingNow = wasReplaying && replayElapsed >= MIN_REPLAY_DISPLAY_MS
            if (wasReplaying) {
              sawReplayMessageThisTurn[sid] = false
              if (!clearReplayingNow) {
                if (replayingClearTimers[sid]) {
                  clearTimeout(replayingClearTimers[sid])
                }
                replayingClearTimers[sid] = setTimeout(() => {
                  delete replayingClearTimers[sid]
                  withBucket(sid, () => ({ isReplaying: false }))
                }, MIN_REPLAY_DISPLAY_MS - replayElapsed)
              }
            }
            withBucket(targetSid, (b) => {
              const isCancelAck = /turn.cancel/i.test(frame.message ?? '')
              // ADR-051 — live→replay dedup (per-bucket half; the foreground
              // pre-check ran above). If a bubble carrying the same
              // `errorEntryId` is already in this bucket, the live error has
              // already been rendered — skip the toast + bubble push but
              // STILL close out streaming state (an idempotent re-close is a
              // no-op on already-closed bubbles), so a replay-late-arriving
              // live frame doesn't leave a bubble stuck streaming.
              if (errorDedupId) {
                const alreadyRendered = getMessages(b).some(
                  (m) => m.errorEntryId === errorDedupId,
                )
                if (alreadyRendered) {
                  return produce(b, (draft) => {
                    draft.isStreaming = false
                    if (clearReplayingNow) draft.isReplaying = false
                    // S7/S1: an error frame terminates a turn exactly like a
                    // done would — see the matching comment on the C8 sweep
                    // branch below for why every branch here clears this.
                    markTurnFinished(targetSid, draft.activeTurnId)
                    draft.activeTurnId = null
                    draft.activeTurnAgentId = null
                    draft.activeTurnBubbleOpened = false
                  }) as Partial<SessionChatState>
                }
              }
              const lastMsgId = findLastAssistantMessageId(b.messageOrder, b.messagesById)
              const lastMsg = lastMsgId ? b.messagesById[lastMsgId] : undefined
              // Mirror replay_error: only coalesce into lastMsgId when that
              // bubble is THIS turn (streaming, or an empty placeholder).
              // A second tab / reload has history and no placeholder —
              // lastMsgId is the last successful reply and must not be
              // restamped as the failure. SessionID delivery made that
              // live frame reach the second tab; this guard is the pair.
              const lastContentEmpty = !!lastMsg && (!lastMsg.content || !lastMsg.content.trim())
              // ADR-070 §2.6: `lastContentEmpty` alone would otherwise pull
              // an already-closed, closedBySteer bubble (content still ''
              // because the steer landed before any text streamed) into
              // this sweep and re-stamp it with the error — mirrors how
              // `lastIsTerminalError` already excludes 'error' below.
              const lastIsThisTurn = !!lastMsg && !lastMsg.closedBySteer && (
                lastMsg.isStreaming
                || lastMsg.status === 'streaming'
                || lastContentEmpty
              )
              const lastIsTerminalError = !!lastMsg
                && lastMsg.status === 'error'
                && !lastMsg.isStreaming
              const lastIsSameError = lastIsTerminalError
                && !!llmError
                && (
                  errorEntryId
                    ? lastMsg?.errorEntryId === errorEntryId
                    : (lastMsg?.errorCode === llmError.code
                      || lastMsg?.content === translatedMessage)
                )
              // C8: close every still-streaming assistant (a mid-turn steer
              // appends a USER message after the open bubble, so it is no
              // longer last). Include last only when it belongs to this turn.
              const streamingIds: string[] = []
              for (let i = b.messageOrder.length - 1; i >= 0; i--) {
                const id = b.messageOrder[i]
                const m = b.messagesById[id]
                if (m?.role !== 'assistant') continue
                const isLastThisTurn = id === lastMsgId && lastIsThisTurn && !lastIsTerminalError
                if (isLastThisTurn || m.isStreaming || m.status === 'streaming') {
                  // ADR-051 — when the incoming frame carries a typed
                  // payload, do NOT include an already-terminal 'error'
                  // bubble in the C8 close sweep. The sweep's purpose is to
                  // finalize in-flight streaming bubbles on a hard error; a
                  // PRIOR turn's already-error bubble (which lands here only
                  // because `id === lastMsgId` pulls in the last assistant
                  // message unconditionally) has nothing to close. Without
                  // this guard, a second typed ErrorFrame would re-close
                  // the first's bubble and (via the typed-field stamping
                  // below) overwrite its errorCode/errorEntryId, breaking
                  // the per-entry-id dedup contract (different entry_ids
                  // must yield different bubbles). Legacy frames (no typed
                  // payload) keep the pre-ADR-051 behavior — re-closing is
                  // an unobservable no-op for them, so the regression risk
                  // is zero.
                  if (
                    llmError
                    && m.status === 'error'
                    && !m.isStreaming
                  ) continue
                  streamingIds.push(id)
                }
              }
              if (streamingIds.length > 0) {
                return produce(b, (draft) => {
                  for (const id of streamingIds) {
                    const msg = draft.messagesById[id]
                    const prevStatus = msg.status
                    // FR-21 / T21–T23: do NOT overwrite 'interrupted' status with 'error'.
                    const resolvedStatus = (prevStatus === 'interrupted' || isCancelAck)
                      ? 'interrupted'
                      : 'error'
                    // ADR-051: when the typed payload is present, prefer the
                    // translated code→display copy as the bubble content —
                    // but only when the bubble has NO partial content yet
                    // (msg.content is empty). A bubble that already streamed
                    // narration text keeps that text as its content and just
                    // gets the typed error fields stamped on for the
                    // "Technical details" disclosure. Pre-ADR-051 fallback
                    // (`frame.message`, sanitized — D5 fix Site 3) is
                    // preserved when no typed payload.
                    const fallbackContent = llmError
                      ? translatedMessage
                      : safeMessage
                    msg.content = (resolvedStatus === 'interrupted')
                      ? msg.content
                      : (msg.content || (fallbackContent ?? ''))
                    msg.isStreaming = false
                    msg.status = resolvedStatus
                    msg.pendingTextBoundary = false
                    // ADR-051 — stamp typed fields on the closed bubble so
                    // the "Technical details" disclosure renders even when
                    // we coalesced into a streaming bubble instead of
                    // pushing a fresh error bubble below. ONLY stamp on the
                    // transition into 'error' (prevStatus !== 'error'): the
                    // C8 union sweep unconditionally includes the last
                    // assistant message even when it's already a finalized
                    // error bubble, so without this guard a second live
                    // error frame would overwrite the first's typed fields
                    // (same id, different entry_id → lost dedup signal).
                    // Cancel-acks and interrupted resolutions are also
                    // excluded by the resolvedStatus guard.
                    if (
                      resolvedStatus === 'error'
                      && prevStatus !== 'error'
                      && llmError
                    ) {
                      msg.errorCode = llmError.code
                      if (errorDetail !== undefined) msg.errorDetail = errorDetail
                      if (errorEntryId) msg.errorEntryId = errorEntryId
                    }
                  }
                  draft.isStreaming = false
                  if (clearReplayingNow) {
                    draft.isReplaying = false
                  }
                  // S7/S1: a terminal error frame ends the turn exactly like
                  // a `done` would — clear the ADR-082 active-turn
                  // announcement (and record it finished, S2) here too, or a
                  // later, unrelated done for this session could misread a
                  // stale activeTurnId as "replay still awaiting catch-up"
                  // and open a stray empty placeholder.
                  markTurnFinished(targetSid, draft.activeTurnId)
                  draft.activeTurnId = null
                  draft.activeTurnAgentId = null
                  draft.activeTurnBubbleOpened = false
                }) as Partial<SessionChatState>
              }
              // Same catalogue already on the last bubble (replay drew it,
              // then the live frame arrived): do not mint a duplicate.
              if (lastIsSameError) {
                return produce(b, (draft) => {
                  draft.isStreaming = false
                  if (clearReplayingNow) draft.isReplaying = false
                  markTurnFinished(targetSid, draft.activeTurnId)
                  draft.activeTurnId = null
                  draft.activeTurnAgentId = null
                  draft.activeTurnBubbleOpened = false
                }) as Partial<SessionChatState>
              }
              // No this-turn assistant to coalesce into — last is a prior
              // healthy reply, or the bucket is empty. Push one new bubble
              // so the error is not silently dropped or written onto history.
              if (!isCancelAck) {
                // D5 fix (Site 3): safeMessage, not the raw frame.message.
                useConnectionStore.getState().setConnectionError(safeMessage)
              }
              // ADR-051 — fresh error bubble: use the translated copy when
              // the typed payload is present (matches the streaming-coalesce
              // branch above), else the sanitized legacy `frame.message`
              // (D5 fix Site 3). Stamp the typed fields so the "Technical
              // details" disclosure can render.
              const errMsg: ChatMessage = {
                id: generateId(),
                role: 'assistant',
                content: isCancelAck ? '' : (llmError ? translatedMessage : safeMessage),
                timestamp: new Date().toISOString(),
                status: isCancelAck ? 'interrupted' : 'error',
                isStreaming: false,
                ...(!isCancelAck && llmError
                  ? {
                      errorCode: llmError.code,
                      ...(errorDetail !== undefined ? { errorDetail } : {}),
                      ...(errorEntryId ? { errorEntryId } : {}),
                    }
                  : {}),
              }
              const msgs = [...getMessages(b), errMsg]
              markTurnFinished(targetSid, b.activeTurnId)
              return {
                ...applyMessageArray(msgs, b),
                isStreaming: false,
                ...(clearReplayingNow ? { isReplaying: false } : {}),
                activeTurnId: null,
                activeTurnAgentId: null,
                activeTurnBubbleOpened: false,
              }
            })
            // The failed turn may have been one we sent from the offline-queue
            // drain — send the next queued message, if any.
            maybeDrainNext()
          }
          break

        case 'tool_call_start': {
          if (!targetSid) break
          const parentCallId = frame.parent_call_id
          if (parentCallId) {
            const b = get().sessionsById[targetSid] ?? emptySessionState()
            if (hasOpenSpanFast(b, parentCallId)) {
              // Temporarily patch active session for attachStepToSpan.
              if (targetSid === originalActiveSid) {
                store.attachStepToSpan(parentCallId, {
                  id: frame.call_id,
                  call_id: frame.call_id,
                  tool: frame.tool,
                  params: frame.params,
                  status: 'running',
                })
              } else {
                withBucket(targetSid, (bucket) => {
                  const entry = bucket.spanByParentCallId[parentCallId]
                  if (!entry) return {}
                  return produce(bucket, (draft) => {
                    const msg = draft.messagesById[entry.messageId]
                    if (!msg?.spans) return
                    const span = msg.spans[entry.spanIdx]
                    if (!span) return
                    span.steps.push({
                      kind: 'tool' as const,
                      tool: { id: frame.call_id, call_id: frame.call_id, tool: frame.tool, params: frame.params, status: 'running' as const },
                    })
                  }) as Partial<SessionChatState>
                })
              }
            } else {
              const bufferKey = `${targetSid}:${parentCallId}`
              bufferForSpan(bufferKey, frame, (buffered) => {
                console.warn(`[chat] orphan frame: parent_call_id="${parentCallId}" session="${targetSid}" — subagent_start never arrived within ${ORPHAN_BUFFER_TTL_MS}ms. Releasing as flat tool calls.`)
                logDiagnostic('chatOrphanFrameReleased', { parentCallId, sessionId: targetSid, ttlMs: ORPHAN_BUFFER_TTL_MS })
                useUiStore.getState().addToast({
                  variant: 'default',
                  message: 'Some subagent steps arrived without their span — displayed as flat tool calls',
                })
                withBucket(targetSid, (bucket) => {
                  const patchToolCalls = { ...bucket.toolCalls }
                  let patchOrder = [...bucket.toolCallOrder]
                  const patchText = { ...bucket.textAtToolCallStart }
                  let patchMsgs = getMessages(bucket)
                  for (const { frame: bf } of buffered) {
                    if (bf.type === 'tool_call_start') {
                      const lastMsg = patchMsgs[patchMsgs.length - 1]
                      const textSnapshot = (lastMsg?.role === 'assistant' ? lastMsg.content : '') ?? ''
                      if (!lastMsg || lastMsg.role !== 'assistant') {
                        const ph: ChatMessage = { id: generateId(), role: 'assistant', content: '', timestamp: new Date().toISOString(), status: 'streaming', isStreaming: true, agentId: bf.agent_id ?? useSessionStore.getState().activeAgentId ?? undefined }
                        patchMsgs = [...patchMsgs, ph]
                      }
                      patchToolCalls[bf.call_id] = { id: bf.call_id, call_id: bf.call_id, tool: bf.tool, params: bf.params, status: 'running' }
                      patchOrder = [...patchOrder, bf.call_id]
                      patchText[bf.call_id] = textSnapshot
                    } else if (bf.type === 'tool_call_result') {
                      if (patchToolCalls[bf.call_id]) {
                        patchToolCalls[bf.call_id] = { ...patchToolCalls[bf.call_id], result: clampToolResult(bf.result), status: bf.status, duration_ms: bf.duration_ms, error: bf.error }
                      }
                    }
                  }
                  const msgArrayPatch = applyMessageArray(patchMsgs, { ...bucket, toolCalls: patchToolCalls, toolCallOrder: patchOrder })
                  return { ...msgArrayPatch, textAtToolCallStart: patchText }
                })
              })
            }
          } else {
            withBucket(targetSid, (b) => {
              // Anchor resolution. Default: the raw message-order tail, same
              // as before — this is REPLAY-SAFE and must stay unconditional
              // on role alone (NOT isStreaming): replay-reconstructed
              // assistant bubbles (see the 'replay_message' case) are
              // finalized (isStreaming:false) the INSTANT they're created —
              // that's by design, not a live "closed segment" — and
              // tool_call_start frames replayed right after them must still
              // reuse that same bubble (mirrors the 'replay_message' case's
              // own doc comment on why isStreaming can't serve as the
              // open/closed signal during replay).
              //
              // Fallback ONLY when the raw tail is NOT an assistant message:
              // this is the mid-turn-steer case (sendMessage's `isStreaming`
              // branch appends the steer text as a new USER message AFTER
              // the still-open assistant bubble, so the raw tail is no
              // longer assistant by the time this frame arrives) — anchor
              // instead on the last assistant message further back, but
              // ONLY if it is still genuinely STREAMING (never a
              // replay-finalized or turn-closed one — reusing a closed
              // bubble here would be the "text-then-image-at-bottom"
              // reconnect-bug class of mistake the 'token' handler's own
              // still-streaming check already guards against). Without this
              // fallback, tool_call_start would wrongly mint a brand-new
              // assistant placeholder while the ORIGINAL bubble is left
              // behind still marked isStreaming:true — nothing else ever
              // finalizes an abandoned bubble like that (the done/error
              // handlers below now sweep every still-streaming assistant
              // message, but only once a terminal frame actually arrives),
              // so it would render a permanent shimmer/spinner (Copy bar
              // suppressed) until a manual reload replayed history.
              const rawTailId = b.messageOrder[b.messageOrder.length - 1]
              const rawTail = rawTailId ? b.messagesById[rawTailId] : undefined
              let lastMsgId: string | undefined
              let lastMsg: ChatMessage | undefined
              if (rawTail?.role === 'assistant') {
                lastMsgId = rawTailId
                lastMsg = rawTail
              } else {
                const lastAssistantId = findLastAssistantMessageId(b.messageOrder, b.messagesById)
                const lastAssistantMsg = lastAssistantId ? b.messagesById[lastAssistantId] : undefined
                if (lastAssistantMsg?.isStreaming) {
                  lastMsgId = lastAssistantId ?? undefined
                  lastMsg = lastAssistantMsg
                }
              }
              const textSnapshot = (lastMsg?.role === 'assistant' ? lastMsg.content : '') ?? ''
              // Reconnect/replay safety: if this call_id is already recorded
              // (we have a textAtToolCallStart snapshot for it), keep the
              // ORIGINAL snapshot. A reattach replays from the start of the
              // transcript while the bucket already holds the completed
              // assistant text — without this guard every snapshot gets
              // overwritten with "end of full text", which makes the
              // runtime adapter render every tool call AFTER the text
              // (the "tool calls grouped at the bottom" reconnect bug).
              // Likewise, don't downgrade a tool call's status from
              // success/error back to running.
              const orderHasCall = b.toolCallOrder.includes(frame.call_id)
              const existingSnapshot = b.textAtToolCallStart[frame.call_id]
              const existingOwner = b.toolCallOwnerMessageId?.[frame.call_id]
              const existingTC = b.toolCalls[frame.call_id]
              // Defense-in-depth alongside stampToolCallOffset's prevOffset-
              // first invariant (see that function's doc comment): a
              // reconnect can replay `tool_call_start` for a call that has
              // ALREADY been baked into a historical message's tool_calls
              // (the done-bake / replay-merge bakes wipe toolCallOrder/
              // textAtToolCallStart, so orderHasCall/existingSnapshot alone
              // can't detect this). When that's the case, the frame must be
              // treated as a no-op start — see isToolCallBakedInBucket's doc
              // comment for why re-recording a snapshot or re-queuing the id
              // into toolCallOrder here would risk corrupting the already-
              // correct stamped offset. A still-streaming turn's calls
              // reattaching mid-turn are NOT yet baked into any message, so
              // this is false for them and they take the unchanged path below.
              const alreadyBaked = isToolCallBakedInBucket(b.messagesById, frame.call_id)
              // FR-21 / T21–T25: a tool_call_start for a top-level (non-subagent)
              // tool means the agent is actively working — set isStreaming:true so
              // the Stop button appears even when the LLM emits a tool call without
              // streaming any text first (e.g. glm-5v-turbo immediately calling
              // write_file).  Do not set it during replay (b.isReplaying) because
              // replay frames reconstruct history and should not trigger the spinner.
              const shouldMarkStreaming = !b.isReplaying
              return produce(b, (draft) => {
                // Owning message for THIS call — recorded below into
                // toolCallOwnerMessageId (reconnect/replay-safe: only set
                // when not already recorded, mirroring existingSnapshot).
                let ownerMsgId: string | null = null
                if (!lastMsg || lastMsg.role !== 'assistant') {
                  const ph: ChatMessage = { id: generateId(), role: 'assistant', content: '', timestamp: new Date().toISOString(), status: 'streaming', isStreaming: true, agentId: frame.agent_id ?? useSessionStore.getState().activeAgentId ?? undefined }
                  draft.messagesById[ph.id] = ph
                  draft.messageOrder.push(ph.id)
                  ownerMsgId = ph.id
                } else if (lastMsgId) {
                  ownerMsgId = lastMsgId
                  // A tool call is starting on the bubble that's still holding
                  // the delegator's own narration — flag the seam so the next
                  // token append (the post-tool-call continuation, or a
                  // synchronous delegate's own reply riding the same bubble)
                  // gets a paragraph break instead of gluing on with no
                  // separator. See `pendingTextBoundary` on ChatMessage.
                  draft.messagesById[lastMsgId].pendingTextBoundary = true
                  // Sync/await-mode delegation attribution fix: frame.agent_id
                  // on a tool_call_start is always the CALLER of the tool
                  // (pkg/agent/turn.go's appendToolCallTranscript stamps the
                  // invoking turnState's own resolveActiveAgentID; replay.go's
                  // buildStart sources it from the owning TranscriptEntry's
                  // AgentID) — for a top-level "delegate" call, that's the
                  // DELEGATOR, never the delegate. Stamp it here the same way
                  // the 'token' case already stamps from its own frame
                  // (Fix 5a) — without this, a bubble whose agentId is still
                  // unset (e.g. the optimistic placeholder sendMessage()
                  // creates, which carries no agentId at creation) stays
                  // unattributed through the entire tool call. In synchronous
                  // (await) delegation the delegator's OWN turn is blocked on
                  // the delegate and gets no chance to stamp the bubble with
                  // its own id first (unlike background delegation, where the
                  // delegator's turn continues and typically stamps the
                  // bubble before the delegate's async tokens ever arrive).
                  // The delegate's reply tokens then land first and, per the
                  // 'token' case's agent_id-boundary rule (only a KNOWN
                  // mismatch opens a new bubble), silently claim the whole
                  // bubble — including the delegator's own "Delegate task"
                  // tool-call card. Stamping here locks the bubble's identity
                  // to its rightful owner (the tool's caller) as soon as the
                  // call starts, so the boundary check correctly reroutes the
                  // delegate's later same-turn tokens to a new bubble instead
                  // (mirrors the already-correct async/background behavior).
                  if (frame.agent_id) {
                    draft.messagesById[lastMsgId].agentId = frame.agent_id
                  }
                }
                if (shouldMarkStreaming) draft.isStreaming = true
                if (!existingTC || existingTC.status === 'running') {
                  draft.toolCalls[frame.call_id] = { id: frame.call_id, call_id: frame.call_id, tool: frame.tool, params: frame.params, status: 'running' }
                }
                if (!orderHasCall && !alreadyBaked) draft.toolCallOrder.push(frame.call_id)
                if (existingSnapshot === undefined && !alreadyBaked) {
                  draft.textAtToolCallStart[frame.call_id] = textSnapshot
                }
                if (existingOwner === undefined && ownerMsgId) {
                  if (!draft.toolCallOwnerMessageId) draft.toolCallOwnerMessageId = {}
                  draft.toolCallOwnerMessageId[frame.call_id] = ownerMsgId
                }
              }) as Partial<SessionChatState>
            })
          }
          break
        }

        case 'tool_call_result': {
          if (!targetSid) break
          const clampedResult = clampToolResult(frame.result)
          const parentCallId = frame.parent_call_id
          if (parentCallId) {
            const b = get().sessionsById[targetSid] ?? emptySessionState()
            if (hasOpenSpanFast(b, parentCallId)) {
              withBucket(targetSid, (bucket) => {
                const entry = bucket.spanByParentCallId[parentCallId]
                if (entry) {
                  return produce(bucket, (draft) => {
                    const msg = draft.messagesById[entry.messageId]
                    if (!msg?.spans) return
                    const span = msg.spans[entry.spanIdx]
                    if (!span) return
                    // No `params` key here (bug fix — was `params: {}`, which
                    // clobbered the real start-time params via the spread
                    // below and made e.g. a hidden `bash {action:'poll'}`
                    // step misclassify as visible to ToolCallBadge's
                    // shouldRenderToolCall, since it saw params={} instead of
                    // the real args). Mirrors the buffered merge paths above
                    // (startSpan / subagent_start), which never carried this
                    // bug because they never included a params key at all.
                    const step = { id: frame.call_id, call_id: frame.call_id, tool: frame.tool, result: clampedResult, status: frame.status ?? 'success' as const, duration_ms: frame.duration_ms, error: frame.error }
                    const existingIdx = span.steps.findIndex((s) => s.kind === 'tool' && s.tool.call_id === frame.call_id)
                    if (existingIdx !== -1) {
                      const existingStep = span.steps[existingIdx]
                      if (existingStep.kind === 'tool') {
                        // Spread order matters: existingStep.tool's params
                        // (recorded at tool_call_start) survive because
                        // `step` no longer carries a params key to overwrite it.
                        span.steps[existingIdx] = { kind: 'tool', tool: { ...existingStep.tool, ...step } }
                      }
                    } else {
                      // Genuine race — no tool_call_start was ever recorded
                      // for this call_id on this span (result arrived first).
                      // There is no start-time params to inherit here, so
                      // (only in this orphan-step branch) default to {}.
                      span.steps.push({ kind: 'tool' as const, tool: { ...step, params: {} } })
                    }
                  }) as Partial<SessionChatState>
                }
                // Fallback: O(N) scan (index miss — log a warning).
                console.warn('[chat] tool_call_result: span index miss, falling back to O(N) scan', { parentCallId, callId: frame.call_id })
                logDiagnostic('chatToolCallResultSpanIndexMiss', { parentCallId, callId: frame.call_id, sessionId: targetSid })
                for (let i = bucket.messageOrder.length - 1; i >= 0; i--) {
                  const msgId = bucket.messageOrder[i]
                  const msg = bucket.messagesById[msgId]
                  if (msg.role !== 'assistant' || !msg.spans) continue
                  const spanIdx = msg.spans.findIndex((s) => s.parentCallId === parentCallId)
                  if (spanIdx === -1) continue
                  return produce(bucket, (draft) => {
                    const draftMsg = draft.messagesById[msgId]
                    const span = draftMsg.spans![spanIdx]
                    // See the primary (index-hit) branch above for why
                    // `params` is intentionally absent from this object.
                    const step = { id: frame.call_id, call_id: frame.call_id, tool: frame.tool, result: clampedResult, status: frame.status ?? 'success' as const, duration_ms: frame.duration_ms, error: frame.error }
                    const existingIdx = span.steps.findIndex((s) => s.kind === 'tool' && s.tool.call_id === frame.call_id)
                    if (existingIdx !== -1) {
                      const existingStep = span.steps[existingIdx]
                      if (existingStep.kind === 'tool') {
                        span.steps[existingIdx] = { kind: 'tool', tool: { ...existingStep.tool, ...step } }
                      }
                    } else {
                      span.steps.push({ kind: 'tool' as const, tool: { ...step, params: {} } })
                    }
                  }) as Partial<SessionChatState>
                }
                return {}
              })
            } else {
              const bufferKey = `${targetSid}:${parentCallId}`
              bufferForSpan(bufferKey, frame, (buffered) => {
                console.warn(`[chat] orphan frame: parent_call_id="${parentCallId}" session="${targetSid}" — subagent_start never arrived within ${ORPHAN_BUFFER_TTL_MS}ms. Releasing as flat tool calls.`)
                logDiagnostic('chatOrphanFrameReleased', { parentCallId, sessionId: targetSid, ttlMs: ORPHAN_BUFFER_TTL_MS })
                useUiStore.getState().addToast({
                  variant: 'default',
                  message: 'Some subagent steps arrived without their span — displayed as flat tool calls',
                })
                withBucket(targetSid, (bucket) => {
                  const patchToolCalls = { ...bucket.toolCalls }
                  let patchOrder = [...bucket.toolCallOrder]
                  const patchText = { ...bucket.textAtToolCallStart }
                  const patchMsgs = getMessages(bucket)
                  for (const { frame: bf } of buffered) {
                    if (bf.type === 'tool_call_start') {
                      const lastMsg = patchMsgs[patchMsgs.length - 1]
                      const textSnapshot = (lastMsg?.role === 'assistant' ? lastMsg.content : '') ?? ''
                      patchToolCalls[bf.call_id] = { id: bf.call_id, call_id: bf.call_id, tool: bf.tool, params: bf.params, status: 'running' }
                      patchOrder = [...patchOrder, bf.call_id]
                      patchText[bf.call_id] = textSnapshot
                    } else if (bf.type === 'tool_call_result') {
                      if (patchToolCalls[bf.call_id]) {
                        patchToolCalls[bf.call_id] = { ...patchToolCalls[bf.call_id], result: clampToolResult(bf.result), status: bf.status, duration_ms: bf.duration_ms, error: bf.error }
                      }
                    }
                  }
                  const msgArrayPatch = applyMessageArray(patchMsgs, { ...bucket, toolCalls: patchToolCalls, toolCallOrder: patchOrder })
                  return { ...msgArrayPatch, textAtToolCallStart: patchText }
                })
              })
            }
          } else {
            withBucket(targetSid, (b) => {
              if (!b.toolCalls[frame.call_id]) {
                return appendUnmatchedToolError(b, frame, clampedResult)
              }
              return produce(b, (draft) => {
                const tc = draft.toolCalls[frame.call_id]
                tc.result = clampedResult
                tc.status = frame.status ?? 'success'
                tc.duration_ms = frame.duration_ms
                tc.error = frame.error
              }) as Partial<SessionChatState>
            })
          }
          break
        }

        case 'subagent_start': {
          if (!targetSid) break
          const sf = frame as WsSubagentStartFrame
          withBucket(targetSid, (b) => {
            return produce(b, (draft) => {
              // ADR-070 §2.1/F2: a bare findLastAssistantMessageId scan here
              // would reattach this span to a closed, closedBySteer bubble
              // (positioned before a mid-turn steer's user message) if the
              // span is the first live frame to arrive after the steer.
              // findOpenAssistantMessageId refuses that; when it resolves to
              // null, open a fresh placeholder — mirroring
              // case 'tool_call_start' — rather than silently dropping a
              // real delegation span (worse than the mis-ordering itself).
              let lastMsgId = findOpenAssistantMessageId(draft.messageOrder, draft.messagesById)
              if (!lastMsgId) {
                const placeholder: ChatMessage = {
                  id: generateId(),
                  role: 'assistant',
                  content: '',
                  timestamp: new Date().toISOString(),
                  status: 'streaming',
                  isStreaming: true,
                  agentId: sf.agent_id ?? useSessionStore.getState().activeAgentId ?? undefined,
                }
                draft.messagesById[placeholder.id] = placeholder
                draft.messageOrder.push(placeholder.id)
                lastMsgId = placeholder.id
              }
              const span: SubagentSpanRunning = {
                spanId: sf.span_id,
                parentCallId: sf.parent_call_id,
                taskLabel: sf.task_label,
                status: 'running',
                steps: [],
                agentId: sf.agent_id,
                // ADR-057 FR-013/W5c: the child's own routable session id,
                // when the gateway sent one (see SubagentSpanBase's doc).
                childSessionId: sf.producing_session_id,
              }
              const bufferKey = `${targetSid}:${sf.parent_call_id}`
              const buffered = pendingByParentCallId[bufferKey] ?? []
              delete pendingByParentCallId[bufferKey]
              if (orphanTimers[bufferKey]) {
                clearTimeout(orphanTimers[bufferKey])
                delete orphanTimers[bufferKey]
              }
              for (const { frame: bf } of buffered) {
                if (bf.type === 'tool_call_start') {
                  span.steps.push({ kind: 'tool', tool: { id: bf.call_id, call_id: bf.call_id, tool: bf.tool, params: bf.params, status: 'running' } })
                } else if (bf.type === 'tool_call_result') {
                  const existingIdx = span.steps.findIndex((s) => s.kind === 'tool' && s.tool.call_id === bf.call_id)
                  if (existingIdx !== -1) {
                    const existing = span.steps[existingIdx]
                    if (existing.kind === 'tool') {
                      span.steps[existingIdx] = { kind: 'tool', tool: { ...existing.tool, result: clampToolResult(bf.result), status: bf.status, duration_ms: bf.duration_ms, error: bf.error } }
                    }
                  }
                }
              }
              const lastMsg = draft.messagesById[lastMsgId]
              const spanIdx = (lastMsg.spans ?? []).length
              if (!lastMsg.spans) lastMsg.spans = []
              lastMsg.spans.push(span)
              draft.spanByParentCallId[sf.parent_call_id] = { messageId: lastMsgId, spanIdx }
              // Parallel index keyed by span_id, consumed by the
              // subagent_end handler below for an O(1) lookup instead of a
              // backward linear scan.
              if (!draft.spanBySpanId) draft.spanBySpanId = {}
              draft.spanBySpanId[sf.span_id] = { messageId: lastMsgId, spanIdx }
            }) as Partial<SessionChatState>
          })
          break
        }

        case 'subagent_end': {
          if (!targetSid) break
          const ef = frame as WsSubagentEndFrame
          withBucket(targetSid, (b) => {
            return produce(b, (draft) => {
              // Builds the terminal span record from whichever running span
              // (indexed or scanned) matched ef.span_id — shared by both
              // resolution paths below so they can never diverge.
              function buildTerminalSpan(existingSpan: SubagentSpan): SubagentSpanTerminal {
                return {
                  spanId: existingSpan.spanId,
                  parentCallId: existingSpan.parentCallId,
                  taskLabel: existingSpan.taskLabel,
                  steps: existingSpan.steps,
                  // Defensive fallback: SubagentEndFrame carries its own optional
                  // agent_id; prefer it if the server ever populates it, else
                  // keep the value already stamped by subagent_start.
                  agentId: ef.agent_id ?? existingSpan.agentId,
                  // Same fallback shape for the child session id (FR-013):
                  // prefer whatever subagent_end itself carries, else keep
                  // what subagent_start already stamped.
                  childSessionId: ef.producing_session_id ?? existingSpan.childSessionId,
                  status: ef.status,
                  durationMs: ef.duration_ms ?? 0,
                  finalResult: ef.final_result,
                  reason: ef.reason,
                }
              }

              // O(1) lookup first (mirrors spanByParentCallId/
              // hasOpenSpanFast). Re-verify the indexed span's own id still
              // matches before trusting it — cheap, and guards against any
              // staleness (e.g. an index entry surviving a code path that
              // doesn't maintain it) rather than silently mutating the wrong
              // span.
              const indexEntry = draft.spanBySpanId?.[ef.span_id]
              const indexedMsg = indexEntry ? draft.messagesById[indexEntry.messageId] : undefined
              const indexedSpan = indexEntry ? indexedMsg?.spans?.[indexEntry.spanIdx] : undefined
              if (indexEntry && indexedMsg?.spans && indexedSpan && indexedSpan.spanId === ef.span_id) {
                indexedMsg.spans[indexEntry.spanIdx] = buildTerminalSpan(indexedSpan)
                delete draft.spanByParentCallId[indexedSpan.parentCallId]
                delete draft.spanBySpanId![ef.span_id]
                return
              }

              // Fallback: O(N) scan (legacy path, index miss — e.g. a bucket
              // built before this index existed, or an out-of-band mutation
              // that didn't maintain it).
              console.warn('[chat] subagent_end: span index miss, falling back to O(N) scan', { spanId: ef.span_id })
              logDiagnostic('chatSubagentEndSpanIndexMiss', { spanId: ef.span_id, sessionId: targetSid })
              for (let i = draft.messageOrder.length - 1; i >= 0; i--) {
                const msgId = draft.messageOrder[i]
                const msg = draft.messagesById[msgId]
                if (msg.role !== 'assistant' || !msg.spans) continue
                const spanIdx = msg.spans.findIndex((s) => s.spanId === ef.span_id)
                if (spanIdx === -1) continue
                const existingSpan = msg.spans[spanIdx]
                msg.spans[spanIdx] = buildTerminalSpan(existingSpan)
                delete draft.spanByParentCallId[existingSpan.parentCallId]
                if (draft.spanBySpanId) delete draft.spanBySpanId[existingSpan.spanId]
                return
              }
              console.warn('[chat] subagent_end received for unknown span_id', { spanId: ef.span_id })
              logDiagnostic('chatSubagentEndUnknownSpanId', { spanId: ef.span_id, sessionId: targetSid })
            }) as Partial<SessionChatState>
          })
          break
        }

        case 'task_status_changed':
          queryClient.invalidateQueries({ queryKey: ['tasks'] })
          break

        // D-107 (2026-09-14): a Library write landed on the REST surface —
        // usually in ANOTHER tab, which is the whole point. GLOBAL frame (no
        // session_id — it describes a workspace's file tree, not a chat).
        // Invalidate EVERY cached library query for the named workspace
        // (partial-key ['library', workspace_id] covers entries in every
        // folder, both include_hidden variants, and content) rather than
        // trying to compute which folders were affected — a wrong folder set
        // would reintroduce exactly the stale-listing defect this exists to
        // remove. The workspaces list carries entry_count, so it goes too.
        // The originating tab's redundant invalidate is a no-op (its own
        // mutation already invalidated), and the focus-path pull half lives
        // in useLibraryCrossTabRefresh for tabs that never see a WS event.
        //
        // F3 (2026-09-14, SILENT-FAILURES-rate-limits-dd25339bf.md): routed
        // through scheduleLibraryChangedInvalidate rather than invalidating
        // synchronously here — a burst of these frames (bulk trash, several
        // tabs writing at once) used to cost one full reload pass PER FRAME.
        // See that function's own doc comment for the debounce/coalesce
        // rationale; a real change still refreshes every open view, just
        // once per short window instead of once per frame.
        case 'library_changed':
          scheduleLibraryChangedInvalidate(frame.workspace_id)
          break

        // Per-task run history (ADR-050 / task-run-history-spec §3.8): fires
        // at run open AND close (not just terminal), so the calendar chip
        // flips to "In progress" immediately, not only on completion. Unlike
        // task_status_changed this frame carries no session_id (it is not
        // session-scoped — see SESSION_SCOPED_FRAME_TYPES above, deliberately
        // NOT listed there) since a run can be materialized for a future
        // occurrence with no session yet attached to Task itself. Invalidate
        // BOTH the occurrence overlay (every workspace/range/tz variant —
        // partial-key match) and this task's own run-history list so the
        // calendar chips and the Runs list (TaskRunsList) update live.
        case 'task_run_status':
          queryClient.invalidateQueries({ queryKey: ['tasks', 'occurrences'] })
          queryClient.invalidateQueries({ queryKey: tasksQueryKeys.runs(frame.task_id) })
          break

        case 'agent_switched': {
          const newAgentId = frame.agent_id
          const sessionStore = useSessionStore.getState()
          // Use the frame's session_id if present; fall back to active.
          const switchSid = frameSessionId ?? sessionStore.activeSessionId
          if (newAgentId) {
            // Precedence rule 1 (src/store/session.ts's AGENT PRECEDENCE
            // RULE): this frame reports a handover the BACKEND already
            // performed, so it outranks an explicit user pick rather than
            // being filtered out as a session-derived hint — the picker has
            // to name whoever is actually answering.
            sessionStore.applyServerAgentSwitch(switchSid, newAgentId)
          } else {
            // agent_id ABSENT/nil is the backend's deliberate wire shape for
            // a return-to-default switch (pkg/gateway/websocket.go's
            // agent_switched frame builder omits AgentId specifically for
            // that case). Without this branch the picker silently keeps
            // showing whoever the conversation was handed off to, since
            // `if (newAgentId)` above never fires.
            //
            // There is no dedicated "clear the server-side override" action,
            // so resolve the configured default agent the same way the rest
            // of the app already does — the cached `['agents']` list
            // (useChatAgents/AgentCard/WorkspaceTeamTab all key off
            // `Agent.default === true`, itself derived server-side from
            // `cfg.Agents.Defaults.DefaultAgentID`) — and apply it via the
            // same server-authoritative path as a named switch.
            const agents = queryClient.getQueryData<Agent[]>(['agents'])
            const defaultAgent = agents?.find((a) => a.default)
            if (defaultAgent) {
              sessionStore.applyServerAgentSwitch(switchSid, defaultAgent.id, defaultAgent.type)
            } else {
              // No cached agent list yet (e.g. first frame before the
              // AgentPicker/mention menu has mounted) — refetch it so a
              // subsequent read finds the default agent. Never silently do
              // nothing here: that is exactly the bug this branch fixes.
              queryClient.invalidateQueries({ queryKey: ['agents'] })
            }
          }
          queryClient.invalidateQueries({ queryKey: ['sessions'] })
          break
        }

        default:
          runtime.unknownFrameCount++
          console.warn('[chat] Unknown frame type', { type: (frame as { type?: string }).type, count: runtime.unknownFrameCount })
          logDiagnostic('chatUnknownFrameType', { frameType: (frame as { type?: string }).type, count: runtime.unknownFrameCount })
          if (runtime.unknownFrameCount >= UNKNOWN_FRAME_TOAST_THRESHOLD) {
            useUiStore.getState().addToast({
              message: "Server is sending events this UI doesn't understand — refresh to update.",
              variant: 'warning',
            })
            // Reset so we don't spam the toast on every subsequent unknown frame.
            runtime.unknownFrameCount = 0
          }
          break
      }

      // After processing a frame for the foreground session, re-sync foreground fields
      // in case withBucket targeted a non-foreground session (background sessions).
      // When the target was foreground, withBucket already synced; this call is idempotent.
      syncForeground()
    },
  }
}
