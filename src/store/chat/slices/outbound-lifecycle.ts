// outbound-lifecycle.ts: queued sends, session kickoff, cancellation, and stream cleanup


import type { StoreApi } from 'zustand'
import { produce } from 'immer'
import { generateId } from '@/lib/constants'
import { useUiStore } from '@/store/ui'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
// Runtime (value) schema from the self-contained ws-schemas.ts, not
// schemas.ts — the latter also carries the REST Zodios `makeApi([...])`
// call, which references every REST schema and defeats tree-shaking
// (bundle-budget incident, PR #860).
import { MessageFrame as MessageFrameSchema } from '@/lib/api/generated/ws-schemas'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { logDiagnostic } from '@/lib/telemetry'
import { buildWorkspaceSetupKickoffContent, findLastAssistantMessageId, findOpenAssistantMessageId, getMessages } from '../messages'
import { EMPTY_BUCKET, inFlightReattachSids, pendingCancelAckSids, replayErrorRetryTimers, replayingClearTimers } from '../runtime-state'
import { applyMessageArray, bakeOwnedCallsAtSteerClose, bakeToolCallsByOwner, stampToolCallOffset } from '../session'
import type { ChatMessage, ChatStore, MediaAttachment, PositionedToolCall, SessionChatState } from '../types'

// ── #823 message-status helpers ──────────────────────────────────────────────
// Kept at module scope (not nested inside createOutboundLifecycleSlice/
// sendMessage) so their bodies do not count against those two functions'
// grandfathered line budgets (scripts/budgets/functions.txt) — see
// docs/internal/architecture/draft-module-map.md, "Size budgets".

/** Resolves the client-generated correlation id + queued timestamp for a
 * send, defaulting both when the caller (a fresh sendMessage call, not a
 * queue replay) didn't supply them. */
function beginSend(content: string, opts?: { clientMessageId?: string; queuedAt?: string }) {
  const clientMessageId = opts?.clientMessageId ?? generateId()
  const queuedAt = opts?.queuedAt ?? new Date().toISOString()
  return { clientMessageId, queuedAt, queuedMessage: { id: clientMessageId, content, timestamp: queuedAt } }
}

/** Builds the optimistic user bubble for a send, carrying the correlation id
 * that MessageStatusFrame will later echo back (#823 state A). `mediaRefs`
 * (review finding 17) is stored alongside the display-only `media` so a
 * later `resendMessage` call can resend the SAME attachments — `media`
 * alone carries no wire-usable ref, see ChatMessage.mediaRefs' doc comment. */
function buildQueuedUserMessage(
  sessionId: string,
  content: string,
  clientMessageId: string,
  queuedAt: string,
  attachments: MediaAttachment[] = [],
  mediaRefs: string[] = [],
): ChatMessage {
  return {
    id: clientMessageId,
    session_id: sessionId,
    role: 'user',
    content,
    timestamp: queuedAt,
    status: 'done',
    deliveryStatus: 'sending',
    ...(attachments.length > 0 ? { media: attachments } : {}),
    ...(mediaRefs.length > 0 ? { mediaRefs } : {}),
  }
}

/** Marks a user message 'failed' (#823 state A) after a send attempt that
 * never reached the gateway. Mutates an immer draft in place. */
function markUserMessageFailed(draft: { messagesById: Record<string, { status?: string; deliveryStatus?: string } | undefined> }, id: string): void {
  const um = draft.messagesById[id]
  if (um) {
    um.status = 'error'
    um.deliveryStatus = 'failed'
  }
}

/** Same as markUserMessageFailed, for an array-mapped (non-draft) message list. */
function withUserMessageFailed(m: ChatMessage, targetId: string): ChatMessage {
  // #3: UserMessage allows status:'error'; cast is safe because a caller only
  // ever passes this the message it just constructed with role:'user'. The
  // discriminated union prevents inline spread without the cast.
  return m.id === targetId ? ({ ...m, status: 'error' as const, deliveryStatus: 'failed' as const } as ChatMessage) : m
}

/** Review finding 17: resends messageId IN PLACE — same id, original
 * mediaRefs, no duplicate bubble — replacing the old `sendMessage(content)`
 * "Try again" call, which minted a fresh id (duplicate bubble) and threaded
 * only a plain content string (dropped attachments). See resendMessage's
 * doc comment on the ChatStore type for the full bug writeup. Kept at
 * module scope (not nested inside createOutboundLifecycleSlice), matching
 * this file's own established pattern (see the file-header comment) so its
 * body does not count against that function's grandfathered line budget. */
function performResendMessage(
  get: StoreApi<ChatStore>['getState'],
  getActiveSid: () => string | null,
  withBucket: (sid: string | null, updater: (bucket: SessionChatState) => Partial<SessionChatState>) => void,
  messageId: string,
): void {
  const state = get()
  const activeSid = getActiveSid()
  let sid: string | null = null
  let existing: ChatMessage | undefined
  if (activeSid && state.sessionsById[activeSid]?.messagesById[messageId]) {
    sid = activeSid
    existing = state.sessionsById[activeSid].messagesById[messageId]
  } else {
    // Not the active session — a background session's own failed send can
    // still be retried, so scan every bucket rather than bailing out.
    for (const [candidateSid, bucket] of Object.entries(state.sessionsById)) {
      const found = bucket.messagesById[messageId]
      if (found) {
        sid = candidateSid
        existing = found
        break
      }
    }
  }
  if (!sid || !existing || existing.role !== 'user') return

  const content = existing.content
  const mediaRefs = existing.mediaRefs ?? []
  const targetSid = sid

  // Reset the SAME message in place first — 'sending' — never a second
  // bubble, unlike the pre-fix `sendMessage(content)` call this replaces.
  withBucket(targetSid, (b) => produce(b, (draft) => {
    const m = draft.messagesById[messageId]
    if (m) {
      m.status = 'done'
      m.deliveryStatus = 'sending'
    }
  }) as Partial<SessionChatState>)

  const { connection, isConnected } = useConnectionStore.getState()
  if (!connection || !isConnected) {
    withBucket(targetSid, (b) => produce(b, (draft) => {
      markUserMessageFailed(draft, messageId)
    }) as Partial<SessionChatState>)
    useConnectionStore.getState().setConnectionError(
      'Message could not be resent — connection dropped. Your message was kept; press Retry to resend.'
    )
    return
  }

  const payload = {
    type: 'message' as const,
    content,
    session_id: targetSid === '__pending' ? undefined : targetSid,
    client_message_id: messageId,
    agent_id: useSessionStore.getState().activeAgentId ?? undefined,
    ...(mediaRefs.length > 0 ? { media: mediaRefs } : {}),
  }
  get()._validateOutboundFrame(payload, targetSid)
  const sent = connection.send(payload)
  if (!sent) {
    withBucket(targetSid, (b) => produce(b, (draft) => {
      markUserMessageFailed(draft, messageId)
    }) as Partial<SessionChatState>)
    useConnectionStore.getState().setConnectionError(
      'Message could not be resent — connection dropped. Your message was kept; press Retry to resend.'
    )
  }
}

type OutboundLifecycleSlice = Pick<ChatStore,
  | 'enqueueOutboundMessage'
  | 'drainOutboundQueue'
  | '_validateOutboundFrame'
  | 'sendMessage'
  | 'resendMessage'
  | 'sendWorkspaceSetupKickoff'
  | 'cancelStream'
  | 'clearStreamingState'
>

interface OutboundLifecycleContext {
  set: StoreApi<ChatStore>['setState']
  get: StoreApi<ChatStore>['getState']
  getActiveSid: () => string | null
  withBucket: (sid: string | null, updater: (bucket: SessionChatState) => Partial<SessionChatState>) => void
  bucketToForeground: (bucket: SessionChatState) => Omit<SessionChatState, 'messageOrder' | 'trimmedCount' | 'spanBySpanId' | 'pendingSpanUpdatesBySpanId' | 'toolCallOwnerMessageId'> & { messages: ChatMessage[]; lastAssistantMessageId: string | null }
  abandonPendingKickoffInternal: () => void
  maybeDrainNext: () => void
  runtime: { agentIdAtLastMintSend: string | null }
}

// #823: called when the connection drops. Extracted from
// createOutboundLifecycleSlice to keep it under its grandfathered line budget
// (scripts/budgets/functions.txt) — no behaviour change.
//   - Opus review round 2 item 7: any gap re-attach in flight for this
//     connection is moot the instant it drops — reconnecting goes through the
//     normal attach_session path (session.ts::attachToSession), not this
//     guard, so a stale entry would just block the NEXT gap's re-attach
//     forever after a future reconnect.
//   - N4: any replay_error retry timer scheduled for a PREVIOUS connection
//     would send its eventual attach_session over a connection that no
//     longer exists — same "reconnect goes through the normal path" reasoning.
function clearCatchUpSideChannelsOnDisconnect(): void {
  inFlightReattachSids.clear()
  for (const sid of Object.keys(replayErrorRetryTimers)) {
    clearTimeout(replayErrorRetryTimers[sid])
    delete replayErrorRetryTimers[sid]
  }
}

export function createOutboundLifecycleSlice({ set, get, getActiveSid, withBucket, bucketToForeground, abandonPendingKickoffInternal, maybeDrainNext, runtime }: OutboundLifecycleContext): OutboundLifecycleSlice {
  return {
    enqueueOutboundMessage: (content, queuedMessage) => {
      const MAX_QUEUE = 5
      const current = get().outboundQueue
      if (current.length >= MAX_QUEUE) {
        return false
      }
      set({ outboundQueue: [...current, queuedMessage ?? content] })
      return true
    },

    drainOutboundQueue: () => {
      const queue = get().outboundQueue
      // Kickoff hardening: the early-return used to skip straight
      // past `maybeDrainNext()` whenever `outboundQueue` was empty — but
      // several kickoff-terminal cleanup paths (a rejecting error frame, a
      // synchronous send failure, the "wrong workspace" migration) call
      // THIS function specifically (not `maybeDrainNext()` directly) to
      // release whatever is waiting, and what's waiting may already sit in
      // `pendingDrainQueue` (moved there by an EARLIER drain cycle that got
      // stuck behind the kickoff's own `isStreaming` window) rather than in
      // `outboundQueue`. Always falling through to `maybeDrainNext()` below
      // makes this function a safe, strict superset of it — correct to call
      // at every "an attempt just ended, try to free the next thing"
      // site, regardless of which queue actually holds the waiting item.
      if (queue.length === 0) {
        maybeDrainNext()
        return
      }
      // BUG FIX (2026-07): used to `for`-loop `sendMessage` over the whole
      // queue synchronously — see the `maybeDrainNext` doc comment above for
      // why that silently dropped every message after the first. Instead,
      // hand the whole batch to `pendingDrainQueue` and let `maybeDrainNext`
      // send one at a time, re-triggered as each turn completes.
      //
      // Merge (not overwrite) `pendingDrainQueue`: if a previous drain cycle
      // is still mid-flight (e.g. the connection dropped again partway
      // through and a couple of its items got kicked back to `outboundQueue`
      // via sendMessage's disconnected-WS branch) this preserves whatever is
      // still queued from that cycle instead of silently discarding it.
      //
      // BUG FIX (2026-07, ordering regression): the bounced-back `queue` items
      // were previously appended AFTER whatever remained in
      // `pendingDrainQueue`. But anything still in `pendingDrainQueue` was
      // dequeued from the FRONT of a FIFO by `maybeDrainNext()` — so an item
      // still parked there was queued LATER (chronologically) than any item
      // that already made it out to `sendMessage` and bounced back. E.g.
      // A,B,C queued → drain sends A, pendingDrainQueue=[B,C] → mid-flight
      // disconnect frees B, which bounces back to `outboundQueue=[B]`,
      // leaving `pendingDrainQueue=[C]`. Appending (`[...pendingDrainQueue,
      // ...queue]`) produced [C,B] — C (typed later) sent before B (typed
      // earlier). Prepending the bounced queue instead restores the correct
      // chronological order: [B,C].
      set((state) => ({ outboundQueue: [], pendingDrainQueue: [...queue, ...state.pendingDrainQueue] }))
      maybeDrainNext()
    },

    // Validate outbound MessageFrame against the generated Zod schema before
    // we hand it to the WebSocket. We DO NOT block the send — that would
    // freeze the composer on the first contract drift. Instead we log +
    // bump the dev counter + emit a dev-mode toast (matches the inbound
    // parseFrameSafe pattern in src/lib/ws.ts). A future required wire
    // field would otherwise be silently omitted on the way out.
    _validateOutboundFrame: (payload: unknown, sessionId?: string | null) => {
      const result = MessageFrameSchema.safeParse(payload)
      if (result.success) return
      console.warn('[chat] outbound MessageFrame failed schema validation', result.error)
      // Focused 1-line Zod message (matches src/lib/ws.ts parseFrameSafe).
      // result.error.message is multi-line JSON; downstream consumers (the
      // dev-toast below and the production telemetry record) only need the
      // first failing field + its issue.
      const first = result.error.issues[0]
      const description = first
        ? `${first.path.join('.') || 'root'}: ${first.message}`
        : result.error.message
      logDiagnostic('chatOutboundFrameValidationFailed', { issue: description, sessionId })
      // Gate the dev-toast on MODE (not DEV) so the toast also fires in
      // Vitest's 'test' mode, which bakes DEV=false at compile time. MODE
      // is 'production' for shipped builds so the toast is suppressed
      // there. Without this gate change the W2-29 / W4-15 dev-toast test
      // is unreachable (it has always been — see pre-W4 history).
      if (import.meta.env.MODE !== 'production') {
        useUiStore.getState().addToast({
          message: `Outbound frame validation failed (dev): ${description}`,
          variant: 'error',
        })
      }
    },

    sendMessage: (content, opts) => {
      const mediaRefs = opts?.mediaRefs ?? []
      const attachments = opts?.attachments ?? []
      const { clientMessageId, queuedAt, queuedMessage } = beginSend(content, opts)
      // Phase 1 / FR-010: per-turn model override. Trim and strip empty
      // strings so absent and "" are equivalent (the WS frame is omitted
      // entirely when no model was picked this session, per spec §18 Q3).
      const modelNameRaw = opts?.model_name
      const modelName = typeof modelNameRaw === 'string' ? modelNameRaw.trim() : ''

      // M4 workspace→turn binding (BLOCKER 1). When the user is chatting inside
      // a workspace, the active workspace id is the single source of truth in
      // useWorkspacesStore (set by WorkspaceTabContainer from the route param;
      // null on the global/inbox chat). We forward it as `metadata.workspace_id`
      // so the server stamps the session with this workspace and any task the
      // agent creates this turn (task_create / delegation) lands on THIS
      // workspace's board instead of the agent's default workspace. Absent (or
      // empty) when not in a workspace, matching the backend's default-workspace
      // fallback. The contract caps it at 128 chars; an over-length id is dropped
      // rather than sent (the outbound Zod validator would otherwise toast every
      // turn for a malformed local id).
      const activeWorkspaceIdRaw = useWorkspacesStore.getState().activeWorkspaceId
      const workspaceId =
        typeof activeWorkspaceIdRaw === 'string' &&
        activeWorkspaceIdRaw.length > 0 &&
        activeWorkspaceIdRaw.length <= 128
          ? activeWorkspaceIdRaw
          : ''

      // Merge model_name + workspace_id into a single `metadata` object so the
      // two payload build sites below stay in sync. The frame omits `metadata`
      // entirely when neither field is present (an empty object would be sent
      // otherwise, which the server treats the same but is noise on the wire).
      const metadata: { model_name?: string; workspace_id?: string } = {}
      if (modelName.length > 0) metadata.model_name = modelName
      if (workspaceId.length > 0) metadata.workspace_id = workspaceId
      const metadataFrame = Object.keys(metadata).length > 0 ? { metadata } : {}
      const { connection, isConnected } = useConnectionStore.getState()
      const { activeSessionId: rawActiveSessionId, activeAgentId } = useSessionStore.getState()
      const { isStreaming } = get()
      // Composer guard: a '__pending' sentinel with no in-flight
      // stream is a STUCK state, not a real session — it means a workspace-
      // setup kickoff started a turn under '__pending' and that turn ended
      // (rejected, rolled back, or otherwise never got its session_started
      // ack) without activeSessionId ever being reset back to null. Sending
      // `session_id: '__pending'` on the wire is a protocol violation the
      // gateway answers with a terminal "session not found" error. Treat it
      // exactly like "no active session yet" instead — an ordinary fresh
      // turn, no session_id on the wire — rather than propagating the
      // sentinel. A '__pending' session that IS still streaming (the kickoff
      // or a first message is actively in flight) is untouched here; that
      // case is handled by the existing mid-turn-steering '__pending' branch
      // below.
      const activeSessionId = (rawActiveSessionId === '__pending' && !isStreaming) ? null : rawActiveSessionId

      // Mid-turn steering (bugfixes3 gate 4): sendMessage USED to hard-return
      // here with a 'Please wait — a response is still generating.' toast
      // whenever isStreaming was true — the SPA composer was the only surface
      // in the app that couldn't send mid-turn; channels already deliver
      // mid-turn messages today, and the gateway queues them per-scope,
      // injecting each one into the turn that's already RUNNING (between
      // tool calls — any still-in-flight tool calls for that turn are
      // skipped server-side with "Skipped due to queued user message"). See
      // the `if (isStreaming)` branch inside the `activeSessionId !== null`
      // block below for how a mid-turn send differs from a new-turn send (no
      // second assistant placeholder is minted; `isStreaming` is left alone).
      //
      // The offline check immediately below intentionally still runs FIRST,
      // unconditional on isStreaming: mid-turn steering must NOT bypass
      // offline buffering — if the socket is down, a message typed mid-turn
      // queues into outboundQueue exactly like an idle-state message and
      // drains once drainOutboundQueue() reconnects (maybeDrainNext still
      // waits for !isStreaming before popping that queue — untouched,
      // separate mechanism from this steering path).
      if (!connection || !isConnected) {
        // WS is disconnected — buffer the message for when the connection
        // recovers rather than losing it silently or showing a hard error.
        const enqueued = get().enqueueOutboundMessage(content, queuedMessage)
        if (!enqueued) {
          useConnectionStore.getState().setConnectionError(
            'Queue full (5 messages max) — waiting to reconnect. Oldest pending messages will be sent first.'
          )
        }
        return
      }

      // When activeSessionId is null we do NOT render optimistically until
      // session_started arrives and gives us a real bucket key. This avoids
      // a temporary bucket that we'd have to migrate on the ack, at the cost
      // of ~1 round-trip of perceived latency on the very first message.
      if (activeSessionId !== null) {
        const userMsg: ChatMessage = buildQueuedUserMessage(activeSessionId, content, clientMessageId, queuedAt, attachments, mediaRefs)

        // Mid-turn steering send: a turn is already streaming, so this
        // message does NOT start a new turn — the gateway injects it into
        // the turn that's already running (between tool calls). The existing
        // streaming assistant bubble (the last message in the thread) stays
        // live and keeps receiving content for THAT SAME turn. This path
        // therefore:
        //   - appends ONLY the user bubble (no second assistant placeholder —
        //     minting one here would render a second, permanently-empty
        //     "streaming" bubble that never receives tokens, since the
        //     backend keeps streaming into the FIRST one, not this one);
        //   - leaves `isStreaming` untouched (already true; resetting it here
        //     would race the in-flight turn's own done/error frame);
        //   - skips the prevAssistantIdx tool_calls-baking logic below — that
        //     logic finalizes tool_calls onto a turn that has ALREADY ENDED
        //     (it bakes the live toolCalls state onto the previous assistant
        //     message right as a NEW turn begins); this turn hasn't ended,
        //     so there is nothing to finalize yet.
        // The appended user message lands AFTER the still-streaming assistant
        // message in message order, which is the correct chronology for a
        // steering message — it was sent while that assistant turn was still
        // producing output.
        if (isStreaming) {
          // Mid-turn steering during the '__pending' session window: the
          // FIRST message of a brand new chat streams under the optimistic
          // placeholder sid '__pending' (see the no-active-session branch
          // below) until the server's session_started frame supplies the
          // real session_id. A steer sent in that window would send
          // session_id:'__pending' on the wire — a protocol violation the
          // gateway answers with a terminal "session not found" error frame,
          // which the SPA's error handler then (incorrectly) attributes to
          // the legitimate first turn, erroring it out and losing the steer
          // text. There is no real session to steer INTO yet, so degrade to
          // the existing offline-style buffering instead: enqueue the steer
          // text and let it go out as an ordinary FOLLOW-UP message once
          // session_started resolves the real session_id (which now also
          // calls drainOutboundQueue() — see that handler) and the first
          // turn's own done/error frame calls maybeDrainNext(). That drain
          // already gates on `!isStreaming` (see maybeDrainNext's doc
          // comment), so this cannot race the in-flight first turn — it
          // simply becomes the next queued message once turn 1 completes.
          if (activeSessionId === '__pending') {
            const enqueued = get().enqueueOutboundMessage(content, queuedMessage)
            if (!enqueued) {
              useConnectionStore.getState().setConnectionError(
                'Queue full (5 messages max) — waiting to reconnect. Oldest pending messages will be sent first.'
              )
            }
            return
          }

          withBucket(activeSessionId, (b) => {
            const allMsgs = [...getMessages(b), userMsg]
            return { ...applyMessageArray(allMsgs, b), lastUserMessageAt: Date.now() }
          })

          const steerPayload = {
            type: 'message' as const,
            content,
            session_id: activeSessionId,
            client_message_id: userMsg.id,
            agent_id: activeAgentId ?? undefined,
            ...(mediaRefs.length > 0 ? { media: mediaRefs } : {}),
            ...metadataFrame,
          }
          get()._validateOutboundFrame(steerPayload, activeSessionId)
          const steerSent = connection.send(steerPayload)

          if (!steerSent) {
            // The in-flight turn is unaffected by a steering-send failure —
            // only THIS message failed to reach the gateway. Mark just the
            // user bubble as 'error' (Retry affordance) and leave
            // `isStreaming` alone; there is no assistant placeholder to roll
            // back because a mid-turn steering send never creates one.
            // ADR-070 §2.1 deliberately does NOT close the pre-steer bubble
            // in this branch: the backend never received this steer, so it
            // keeps writing into the SAME original segment exactly as
            // before — closing the bubble here would be actively wrong, not
            // just unnecessary (a real, previously-uncaught gap: closing it
            // unconditionally alongside the append broke the assertion this
            // branch's own pre-existing test pins).
            withBucket(activeSessionId, (b) => produce(b, (draft) => {
              markUserMessageFailed(draft, userMsg.id)
            }) as Partial<SessionChatState>)
            useConnectionStore.getState().setConnectionError(
              'Message could not be sent — connection dropped. Your message was kept; press Retry to resend.'
            )
            return
          }

          // ADR-070 §2.1: only NOW, once the steer has genuinely reached the
          // gateway, close the assistant bubble that was open at send-time —
          // a SEPARATE update from the append above (not merged into it),
          // specifically so a failed send (handled above) never touches the
          // bubble at all. Resolved against the POST-append bucket state:
          // findOpenAssistantMessageId's raw-tail check sees the just-
          // appended user message as the tail and falls back to the last
          // assistant message, closing it only if still genuinely
          // `isStreaming` — which correctly excludes an earlier steer's
          // already-closed bubble for a second/third rapid steer in the
          // same turn (nothing left open to close, by design — ADR §2.1).
          withBucket(activeSessionId, (b) => {
            const openId = findOpenAssistantMessageId(b.messageOrder, b.messagesById)
            if (!openId || !b.messagesById[openId]) return {}
            return produce(b, (draft) => {
              const msg = draft.messagesById[openId]
              msg.isStreaming = false
              msg.status = 'done'
              msg.closedBySteer = true
              // Code review: every other close site (markLastMessageInterrupted,
              // updateLastAssistantMessage, the C8/done sweeps) clears this
              // seam marker on close; this site didn't, leaving a
              // representable-but-meaningless true on a finalized bubble.
              msg.pendingTextBoundary = false
              // Founder-reported fix (2026-09-26): bake the closing bubble's
              // owned tool calls into its tool_calls at close time, without
              // dequeuing the live entries — see
              // bakeOwnedCallsAtSteerClose (src/store/chat/session.ts).
              bakeOwnedCallsAtSteerClose(draft, openId)
            }) as Partial<SessionChatState>
          })
          return
        }

        const assistantMsg: ChatMessage = {
          id: generateId(),
          session_id: activeSessionId,
          role: 'assistant',
          content: '',
          timestamp: new Date().toISOString(),
          status: 'streaming',
          isStreaming: true,
        }

        withBucket(activeSessionId, (b) => {
          const msgs = getMessages(b)
          // Shares the single backward-scan implementation (findLastAssistantMessageId)
          // with every other "find last assistant message" call site in this store.
          // This site is the one exception that needs an array *index* (for the
          // finalMsgs[prevAssistantIdx] splice below) rather than an id, so it
          // resolves the id to an index via msgs.findIndex.
          const prevAssistantId = findLastAssistantMessageId(b.messageOrder, b.messagesById)
          const prevAssistantIdx = prevAssistantId !== null ? msgs.findIndex((m) => m.id === prevAssistantId) : -1
          let toolCallsAfterReset: typeof b.toolCalls = b.toolCalls
          let toolCallOrderAfterReset: string[] = b.toolCallOrder
          let finalMsgs = msgs

          if (prevAssistantIdx !== -1) {
            const prev = msgs[prevAssistantIdx]
            const alreadySeen = new Set((prev.tool_calls ?? []).map((tc) => tc.id))
            const liveIds = b.toolCallOrder.filter(
              (id) => !alreadySeen.has(id) && b.toolCalls[id],
            )
            if (liveIds.length > 0) {
              const existingCalls = (prev.tool_calls ?? []) as PositionedToolCall[]
              const existingById = new Map(existingCalls.map((tc) => [tc.id, tc]))
              const baked = liveIds.map((id) =>
                stampToolCallOffset(id, b.toolCalls[id], b.textAtToolCallStart, existingById.get(id)?.textOffset),
              )
              // Dedupe the merged tool_calls list by id so a re-bake (after
              // an attach + replay, or any other path that revisits live
              // ids) cannot leave duplicate ids on the message.
              const mergedById = new Map<string, PositionedToolCall>(existingCalls.map((tc) => [tc.id, tc]))
              for (const tc of baked) mergedById.set(tc.id, tc)
              finalMsgs = [...msgs]
              // #3: prev is guaranteed assistant (prevAssistantIdx only set for
              // role:'assistant' entries above), so the role guard is defence-in-depth
              // to prevent tool_calls — which is illegal on UserMessage/SystemMessage —
              // being stamped if the invariant is ever broken by a future refactor.
              if (prev.role === 'assistant') {
                finalMsgs[prevAssistantIdx] = {
                  ...prev,
                  tool_calls: Array.from(mergedById.values()),
                } as ChatMessage
              }
              const liveSet = new Set(liveIds)
              const remainingCalls: typeof b.toolCalls = {}
              for (const [k, v] of Object.entries(b.toolCalls)) {
                if (!liveSet.has(k)) remainingCalls[k] = v
              }
              toolCallsAfterReset = remainingCalls
              toolCallOrderAfterReset = b.toolCallOrder.filter((id) => !liveSet.has(id))
            }
          }

          const allMsgs = [...finalMsgs, userMsg, assistantMsg]
          const msgArrayPatch = applyMessageArray(allMsgs, { ...b, toolCalls: toolCallsAfterReset, toolCallOrder: toolCallOrderAfterReset })
          return {
            ...msgArrayPatch,
            isStreaming: true,
            // H1-FE: record when user last sent a message so the unknown-sid
            // done handler can tell whether the active bucket is mid-stream.
            lastUserMessageAt: Date.now(),
          }
        })

        const payload = {
          type: 'message' as const,
          content,
          session_id: activeSessionId,
          client_message_id: userMsg.id,
          agent_id: activeAgentId ?? undefined,
          ...(mediaRefs.length > 0 ? { media: mediaRefs } : {}),
          ...metadataFrame,
        }
        get()._validateOutboundFrame(payload, activeSessionId)
        const sent = connection.send(payload)

        // Sticky model selection: the user's picked model PERSISTS after a
        // successful send (previously it was cleared to null here, so the
        // composer selector snapped back to the agent default and the pick had
        // to be re-made every message). Keeping it means the composer keeps
        // showing the chosen model and the NEXT message defaults to the same
        // one — matching ChatControls' re-seed-from-last-used-model intent. A
        // failed send already kept it (for Retry); switching session/agent
        // re-seeds it from that session's last-used model.

        if (!sent) {
          // #253 (P0 data loss): the user turn must NEVER be silently dropped.
          // Previously this rollback deleted BOTH the user and assistant
          // bubbles, so a failed send wiped the message the user just typed.
          // Now we KEEP the user bubble (re-marked as 'error' so the UI shows
          // a failed state + Retry affordance) and only remove the empty
          // streaming assistant placeholder. The error is also surfaced.
          withBucket(activeSessionId, (b) => {
            return produce(b, (draft) => {
              // Drop the empty assistant placeholder.
              const aIdx = draft.messageOrder.indexOf(assistantMsg.id)
              if (aIdx !== -1) draft.messageOrder.splice(aIdx, 1)
              delete draft.messagesById[assistantMsg.id]
              // Keep the user message, but flag it as failed.
              markUserMessageFailed(draft, userMsg.id)
              draft.isStreaming = false
            }) as Partial<SessionChatState>
          })
          useConnectionStore.getState().setConnectionError('Message could not be sent — connection dropped. Your message was kept; press Retry to resend.')
          // The attempted turn is over (it never started) — free the next
          // drained message to send, if any.
          maybeDrainNext()
        }
      } else {
        // Kickoff hardening: a workspace-setup kickoff already owns
        // the shared '__pending' bucket key (see `pendingKickoff`'s doc
        // comment). Building a SECOND '__pending' bucket here would collide
        // with — and, since `withBucket` appends rather than replaces,
        // silently co-mingle content with — the kickoff's own placeholder,
        // and this send's eventual `session_started` ack would then be
        // ambiguous with the kickoff's own ack. Degrade to the same
        // offline-style buffering `sendMessage` already uses for a
        // disconnected WS / mid-turn '__pending' steering: enqueue and let
        // it go out as an ordinary follow-up once the kickoff resolves
        // (every kickoff-terminal cleanup path calls `drainOutboundQueue()`
        // to release it).
        if (get().pendingKickoff !== null) {
          const enqueued = get().enqueueOutboundMessage(content, queuedMessage)
          if (!enqueued) {
            useConnectionStore.getState().setConnectionError(
              'Queue full (5 messages max) — waiting to reconnect. Oldest pending messages will be sent first.'
            )
          }
          return
        }

        // No active session — send without session_id; server will mint one
        // and ack with session_started.
        //
        // #253(a): Render the user message optimistically in a temporary bucket
        // so it is visible immediately. If the WS send fails, mark the message
        // with status:'error' so the user sees a Retry affordance instead of a
        // silent drop. The temporary bucket key '__pending' is replaced by the
        // real session_id once session_started arrives (see handleFrame case
        // 'session_started'). If the send succeeds, the message stays visible
        // until session_started migrates the bucket.
        const pendingSid = '__pending'
        const userMsg: ChatMessage = buildQueuedUserMessage(pendingSid, content, clientMessageId, queuedAt, [], mediaRefs)
        const assistantMsg: ChatMessage = {
          id: generateId(),
          session_id: pendingSid,
          role: 'assistant',
          content: '',
          timestamp: new Date().toISOString(),
          status: 'streaming',
          isStreaming: true,
        }
        // Render optimistically in the pending bucket and activate it.
        withBucket(pendingSid, (b) => {
          const allMsgs = [...getMessages(b), userMsg, assistantMsg]
          return { ...applyMessageArray(allMsgs, b), isStreaming: true, lastUserMessageAt: Date.now() }
        })
        useSessionStore.getState().setActiveSession(pendingSid, activeAgentId)
        // Remember what this mint went out under, so its session_started ack
        // can tell "server resolved an agent we didn't have" from "the user
        // has since picked a different one" (see runtime.agentIdAtLastMintSend).
        runtime.agentIdAtLastMintSend = activeAgentId ?? null

        // ADR-092 (founder, 2026-09-24): a per-chat Auto choice made before this
        // chat had a session rides on the minting message as `auto_approve`, so
        // the server records it before the first turn runs (no post-ack race).
        // Omitted when the user never touched the toggle.
        const autoApproveChoice = get().pendingAutoApproveChoice
        const payload2 = {
          type: 'message' as const,
          content,
          client_message_id: userMsg.id,
          agent_id: activeAgentId ?? undefined,
          ...(mediaRefs.length > 0 ? { media: mediaRefs } : {}),
          ...metadataFrame,
          ...(autoApproveChoice !== null ? { auto_approve: autoApproveChoice } : {}),
        }
        get()._validateOutboundFrame(payload2, pendingSid)
        const sent = connection.send(payload2)

        // Sticky model selection — see the active-session branch above: the
        // pick persists after a successful send so the composer keeps showing
        // it and the next message defaults to the same model.
        if (!sent) {
          // #253(a): Mark the user message with status:'error' and remove the
          // optimistic assistant placeholder. This preserves the typed content
          // as a retriable error bubble rather than silently dropping the message.
          withBucket(pendingSid, (b) => {
            const msgs = getMessages(b)
              .map((m) => withUserMessageFailed(m, userMsg.id))
              .filter((m) => m.id !== assistantMsg.id)
            return { ...applyMessageArray(msgs, b), isStreaming: false }
          })
          useConnectionStore.getState().setConnectionError('Message could not be sent — connection dropped. Please try again.')
          // The attempted turn is over (it never started) — free the next
          // drained message to send, if any.
          maybeDrainNext()
        }
      }
  },

    // Review finding 17: see performResendMessage's doc comment (module
    // scope, above) for the bug this replaces and why the body lives there
    // rather than here.
    resendMessage: (messageId) => performResendMessage(get, getActiveSid, withBucket, messageId),

    sendWorkspaceSetupKickoff: (opts) => {
      const { workspaceId, workspaceName, agentId, agentType } = opts
      const { connection, isConnected } = useConnectionStore.getState()
      const { activeSessionId } = useSessionStore.getState()
      const { isStreaming, pendingKickoff } = get()

      // Never kick off over an offline connection, a mid-turn stream, or an
      // existing conversation — mirrors sendMessage's own guards, but as an
      // outright bail (no queueing, no toast): a missed kickoff simply
      // leaves `setup_pending` true, so the calling hook can retry on the
      // next render once the blocking condition clears.
      if (!connection || !isConnected) return false
      if (isStreaming) return false
      if (activeSessionId !== null) return false
      // A DIFFERENT kickoff is already outstanding (its ack hasn't
      // landed yet — see `pendingKickoff`'s doc comment). This can happen
      // even with `activeSessionId === null` above: the user can navigate
      // away from the workspace that triggered the first kickoff (which
      // resets `activeSessionId` back to null via `enterWorkspaceChat`)
      // before that kickoff's `session_started`/`error` ack arrives. Firing
      // a second kickoff here would collide with the first on the shared
      // `'__pending'` bucket key. Bail exactly like the other guards; the
      // hook releases its own guard on `false` and retries later.
      if (pendingKickoff !== null) return false

      const content = buildWorkspaceSetupKickoffContent(workspaceName)
      const pendingSid = '__pending'
      const assistantMsg: ChatMessage = {
        id: generateId(),
        session_id: pendingSid,
        role: 'assistant',
        content: '',
        timestamp: new Date().toISOString(),
        status: 'streaming',
        isStreaming: true,
        // Stamp the target agent's identity on the placeholder
        // immediately (mirrors the reconnect/reattach placeholders — see
        // `resolveLastActiveAgentId` in OmnipusRuntimeProvider.tsx) so the
        // thread shows the resolved agent right away instead of waiting for
        // a later frame that also happens to carry `agent_id`.
        agentId,
      }

      // Render ONLY the optimistic streaming assistant placeholder — no user
      // bubble. This turn's "user" content is a synthetic instruction the
      // backend records as a SYSTEM-role transcript entry (a centered pill
      // on replay), not something the user typed, so echoing it as a user
      // bubble here would show text the user never wrote.
      withBucket(pendingSid, (b) => {
        const allMsgs = [...getMessages(b), assistantMsg]
        return { ...applyMessageArray(allMsgs, b), isStreaming: true, lastUserMessageAt: Date.now() }
      })
      // Activate the pending bucket AND select the agent in one write —
      // mirrors AgentPicker's auto-select (setActiveSession with the
      // resolved agent's id+type) so the composer/thread header show the
      // agent immediately, same as sendMessage's own no-session branch
      // activates '__pending' as foreground.
      useSessionStore.getState().setActiveSession(pendingSid, agentId, agentType)
      // Claim the single in-flight-kickoff slot BEFORE sending, so
      // a synchronous re-render triggered by the writes above cannot race a
      // second kickoff attempt in behind this one, and so the
      // `session_started`/`error` handlers can find it as soon as either
      // frame arrives.
      set({ pendingKickoff: { workspaceId } })

      const payload = {
        type: 'message' as const,
        content,
        agent_id: agentId,
        metadata: {
          workspace_id: workspaceId,
          workspace_setup_kickoff: true,
        },
      }
      get()._validateOutboundFrame(payload, pendingSid)
      const sent = connection.send(payload)

      if (!sent) {
        // FULL rollback — unlike sendMessage's failure rollback
        // (which keeps a user's typed bubble as a retriable 'error'), a
        // kickoff has no user-typed content to preserve, so tear the whole
        // '__pending' bucket down rather than leaving an empty husk behind.
        // Also reset session activation back to "no session" (retaining the
        // agent selection just made above) — without this, `activeSessionId`
        // would stay stuck at '__pending' forever and the hook's own guard
        // (`activeSessionId === null`) would block every future retry,
        // including the one it's about to attempt now that this call is
        // returning `false`. `abandonPendingKickoffInternal` handles the
        // `pendingKickoff` clear + bucket teardown + `kickoffAttemptStatus`
        // 'failed' marking in one place, shared with the disconnect
        // and reject-frame cleanup paths below.
        abandonPendingKickoffInternal()
        useSessionStore.getState().setActiveSession(null)
        useConnectionStore.getState().setConnectionError(
          'Could not start the workspace setup interview — connection dropped. Please try again.'
        )
        // Only `drainOutboundQueue()` (not `maybeDrainNext()`) moves
        // items actually sitting in `outboundQueue` — e.g. a message that
        // collided with THIS kickoff's `pendingKickoff` slot via the
        // collision guard in `sendMessage` — into `pendingDrainQueue` and
        // sends the head.
        get().drainOutboundQueue()
        return false
      }

      return true
    },

    cancelStream: (sessionId) => {
      const { connection } = useConnectionStore.getState()
      const { activeSessionId } = useSessionStore.getState()
      const targetSid = sessionId ?? activeSessionId
      // When a `sessionId` is passed explicitly (e.g. the browser panel's
      // pinned session), resolve ITS streaming state from that session's own
      // bucket — the flat `isStreaming` foreground field below only ever
      // reflects the ACTIVE session's projection (via bucketToForeground),
      // so reading it here for a background session would silently answer
      // for the wrong session. The default (no-argument) path keeps reading
      // the flat field exactly as before — this is what the pre-existing
      // Stop-button call site (ChatScreen.tsx) has always relied on, and
      // must stay byte-for-byte unchanged. Both reads happen BEFORE
      // markLastMessageInterrupted() below, whose own withBucket call can
      // resync the flat field out from under a post-hoc read.
      const targetIsStreaming = sessionId !== undefined
        ? (get().sessionsById[sessionId]?.isStreaming ?? false)
        : get().isStreaming

      // FR-21 / T21–T25: always mark the last assistant message as interrupted
      // when the user explicitly invokes cancel (stop button, Escape, /cancel,
      // or the browser panel's "Take over" for its pinned session). Scoped to
      // `sessionId` when the caller passed one, else defaults to the active
      // session (existing behaviour, unchanged). We do this BEFORE the
      // isStreaming guard so that a stop-button click that races a done frame
      // still produces the (interrupted) label. Without this, a turn that
      // completes in <100ms after the stop button appears but before
      // Playwright (or a real user) clicks it would silently do nothing because
      // isStreaming flips to false between render and click.
      get().markLastMessageInterrupted(sessionId)

      if (!connection) return
      if (!targetSid) {
        // No server-side session established yet — just clear local streaming
        // state. S7: activeTurn* travels with isStreaming everywhere else in
        // this file, so clear it here too even though this specific bucket
        // is very unlikely to carry an ADR-082 announcement yet.
        withBucket(getActiveSid(), () => ({
          isStreaming: false,
          activeTurnId: null,
          activeTurnAgentId: null,
        }))
        maybeDrainNext()
        return
      }

      if (targetIsStreaming) {
        // Only send the cancel frame to the server if the turn is still active.
        // Sending cancel for a completed turn is a no-op on the server but wastes
        // a round-trip and may confuse the audit log.
        const sent = connection.send({ type: 'cancel', session_id: targetSid })
        if (!sent) {
          console.warn('[chat] cancelStream: send failed — connection may be closed')
          logDiagnostic('chatCancelStreamSendFailed', { sessionId: targetSid })
          useUiStore.getState().addToast({
            message: 'Could not send cancel — connection dropped. The response may continue briefly.',
            variant: 'error',
          })
        } else {
          // F-S3: the server now owes this session a terminal ack. Track it so
          // an untagged (missing session_id) token/done/error frame that
          // arrives while a DIFFERENT session is foreground can be attributed
          // here instead of misrouted to whatever's active — see handleFrame.
          pendingCancelAckSids.add(targetSid)
        }
      }

      withBucket(targetSid, (b) => {
        const updated = { ...b.toolCalls }
        for (const key of Object.keys(updated)) {
          if (updated[key].status === 'running') {
            updated[key] = { ...updated[key], status: 'cancelled' }
          }
        }
        // cancelStage intentionally NOT reset here — hold label state until
        // the server sends the next cancel_stage frame or done/error clears it.
        // isStreaming is intentionally NOT set to false here. The done frame will
        // clear it. Clearing it here would cause the useEffect([isStreaming]) to
        // immediately reset stopLabel to 'stop', making the "Stopping..." button
        // disappear before the server confirms the cancel (T25). The done frame
        // arrives within a few seconds and performs the correct isStreaming:false
        // transition. markLastMessageInterrupted() above already set the message's
        // own isStreaming:false so AssistantUI renders it as incomplete/cancelled.
        return { toolCalls: updated }
      })
    },

    clearStreamingState: () => {
      // Kickoff hardening: a hard disconnect means no ack
      // (session_started or error) can ever arrive on THIS connection for an
      // outstanding workspace-setup kickoff — its '__pending' placeholder
      // turn is dead. Clear the slot and drop the orphaned bucket now
      // (quietly — no toast, no `activeSessionId` change: `activeSessionId`
      // staying at '__pending' until reconnect is handled by
      // `OmnipusRuntimeProvider.reattachActiveSession`) rather than
      // leaving `pendingKickoff` wedged non-null forever, which would
      // otherwise permanently block every future kickoff attempt via
      // `sendWorkspaceSetupKickoff`'s own `pendingKickoff` guard.
      abandonPendingKickoffInternal()
      // F-S3: a socket drop means no more terminal frames are coming for any
      // outstanding cancel — stale entries here would otherwise persist across
      // reconnects and could misattribute an unrelated later frame.
      pendingCancelAckSids.clear()
      clearCatchUpSideChannelsOnDisconnect()
      // S6: a socket drop means no more frames — done, error, or otherwise —
      // are coming on THIS connection for any outstanding replay either. A
      // bucket that is mid-replay (isReplaying:true) but not yet
      // isStreaming:true (session_state hasn't announced a turn, or hasn't
      // been reached in the reducer yet) would otherwise pass through the
      // per-bucket gate below untouched — nothing else ever clears
      // isReplaying for it, since the only two writers are this function and
      // setReplaying's own done/error-driven clear (immediate or via the
      // deferred timers below), neither of which fires on a hard
      // disconnect. Left alone, the composer stays permanently locked
      // behind "Loading session history…" even after reconnect regenerates
      // a fresh attach (a fresh setReplaying(true) does reset the timer, but
      // only once the user is looking at that session again — a background
      // bucket the user never revisits stays wedged for the life of the
      // tab). Cancel every pending replay-clear timer up front — the
      // connection they were waiting on is already gone, so let them fire
      // is both pointless and racy against the synchronous clear below.
      for (const timerSid of Object.keys(replayingClearTimers)) {
        clearTimeout(replayingClearTimers[timerSid])
        delete replayingClearTimers[timerSid]
      }
      // Sweep every bucket — not just the active one — because a background
      // session can be mid-stream when the socket drops. Any bucket left with
      // isStreaming=true would wedge if the user switches to it later.
      set((state) => {
        let mutated = false
        const sessionsById: Record<string, SessionChatState> = {}
        for (const [sid, bucket] of Object.entries(state.sessionsById)) {
          // Mark any still-streaming assistant message as done and flip any
          // running tool calls to cancelled so nothing renders as in-flight.
          const order = bucket.messageOrder
          let needsMsgFix = false
          for (let i = order.length - 1; i >= 0; i--) {
            const m = bucket.messagesById[order[i]]
            if (m?.role === 'assistant' && (m.isStreaming || m.status === 'streaming')) {
              needsMsgFix = true
              break
            }
          }
          // Gate on "is there anything to bake at all" (mirrors the `done` case's
          // `toolCallOrder.length > 0` gate/baking block below) rather than
          // "is something still running": a tool call flips to a terminal status
          // ('success'/'error') the instant its tool_call_result frame arrives,
          // which commonly happens before the trailing assistant text finishes
          // streaming. If the WS drops in that window the tool call is already
          // resolved but never baked — a status-'running' filter here would miss
          // it and let it silently vanish once isStreaming flips false.
          const hasPendingTools = bucket.toolCallOrder.length > 0
          // Defense-in-depth: activeTurnId should never be set while isStreaming
          // is false (both are always written together — see the 'session_state'/
          // 'done' cases above), but guard the skip on it too so a bucket never
          // slips through this sweep carrying a stale ADR-082 activeTurnId. S6:
          // also guard on isReplaying — see the doc comment above this sweep.
          if (!bucket.isStreaming && !needsMsgFix && !hasPendingTools && bucket.cancelStage === null && !bucket.activeTurnId && !bucket.isReplaying) {
            sessionsById[sid] = bucket
            continue
          }
          mutated = true
          // ADR-082 D4 edge case: a turn was announced via session_state.active_turn
          // (isStreaming:true, activeTurnId set) but the socket died before any
          // token/done ever arrived for it (server died mid-catch-up, or the
          // connection dropped between session_state and the replay-terminating
          // done that would have opened the bubble). Clear activeTurnId/
          // activeTurnAgentId here alongside isStreaming so a stale id never
          // survives to mislabel an unrelated later done as "replay-terminating,
          // turn still open" — the existing WS-close handling already prevents
          // the hang (isStreaming flips false, any bubble that did exist is
          // swept below); this just keeps the two fields' invariant intact.
          // S6: isReplaying is cleared here too, for the same reason — a hard
          // disconnect mid-replay is exactly as terminal as a done/error would
          // have been, and nothing else is coming to clear it.
          const next: SessionChatState = {
            ...bucket,
            isStreaming: false,
            isReplaying: false,
            cancelStage: null,
            activeTurnId: null,
            activeTurnAgentId: null,
          }
          // BE-DESIGN.md §6.3 / real-browser regression (orchestrator report,
          // 2026-09-24, BUG 1): a hard disconnect must NOT close the
          // still-streaming bubble. A turn never depends on a UI connection
          // (ADR-082 P1) — the agent hasn't stopped, only this tab's socket
          // has. The bucket-level isStreaming/activeTurnId/etc. above still
          // clear (so the Stop button and composer don't hang forever on a
          // dead connection), but the MESSAGE itself stays exactly as it was
          // — still isStreaming/'streaming' — so that reconnect's catch-up
          // (session_state{active_turn} -> tokens for the SAME message_id ->
          // catch_up_complete -> live tokens -> done) resumes the SAME
          // bubble. This used to flip the bubble to 'done' here, which then
          // made the §4.2 "already-complete bubble ignores further tokens
          // for its message_id" overlap rule (frames.ts::
          // resolveTokenBubbleByMessageId) silently discard the entire
          // catch-up and every live token behind it — the tab froze at
          // whatever text had streamed before the cut.
          if (hasPendingTools) {
            // Bake every pending tool call — not just terminal ones — into
            // its OWNING message's tool_calls array (routed via
            // toolCallOwnerMessageId, falling back to the last assistant
            // message for unmapped/legacy calls — never blindly "the last
            // message": a turn can produce more than one assistant bubble,
            // per Fix 5a / the sync/await-mode delegate attribution fix)
            // before clearing the live bucket state below. This still has to
            // happen even though the bubble itself is left open: the
            // renderer switches from the live toolCalls bucket to
            // message.tool_calls the instant bucket-level isStreaming flips
            // false (mirrors the `done` case's `toolCallOrder.length > 0`
            // gate/baking block below) — otherwise an in-flight tool card
            // would simply vanish. Each call keeps whatever status it
            // actually had (commonly still 'running') rather than being
            // force-flipped to 'cancelled' first — §6.3 says a disconnect
            // must not mark running tools cancelled either; the tool may
            // still resolve server-side and its real result will (mostly)
            // reconcile once catch-up delivers it.
            const lastAssistantId = findLastAssistantMessageId(order, bucket.messagesById)
            next.messagesById = { ...next.messagesById }
            bakeToolCallsByOwner(next.messagesById, bucket.toolCallOrder, bucket.toolCalls, bucket.toolCallOwnerMessageId ?? {}, lastAssistantId, bucket.textAtToolCallStart)
            next.toolCalls = {}
            next.toolCallOrder = []
            next.textAtToolCallStart = {}
            next.toolCallOwnerMessageId = {}
          }
          sessionsById[sid] = next
        }
        if (!mutated) return {}
        const activeSid = getActiveSid()
        const fg = (activeSid ? sessionsById[activeSid] : null) ?? EMPTY_BUCKET
        return { sessionsById, ...bucketToForeground(fg) }
      })
      // The stream was just force-terminated (WS close/terminal error) — if a
      // drained message is waiting, try it now. If the connection is in fact
      // down, sendMessage's own disconnected-WS branch will put it back on
      // outboundQueue (visible in the UI) rather than leaving it stranded and
      // invisible in pendingDrainQueue.
      maybeDrainNext()
    },

  }
}
