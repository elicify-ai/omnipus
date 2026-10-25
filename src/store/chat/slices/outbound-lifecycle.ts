// outbound-lifecycle.ts: queued sends, cancellation, and stream cleanup


import type { StoreApi } from 'zustand'
import { produce } from 'immer'
import { generateId } from '@/lib/constants'
import { isGoalRunning } from '@/lib/goalActivity'
import { useUiStore } from '@/store/ui'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { blockSendForWorkspaceEntry } from '@/store/session/workspaceSendGate'
// Runtime (value) schema from the self-contained ws-schemas.ts, not
// schemas.ts — the latter also carries the REST Zodios `makeApi([...])`
// call, which references every REST schema and defeats tree-shaking
// (bundle-budget incident, PR #860).
import { MessageFrame as MessageFrameSchema } from '@/lib/api/generated/ws-schemas'
import type { CancelFrame, MessageFrame } from '@/lib/api/generated/asyncapi-types'
import type { components } from '@/lib/api/generated/openapi-types'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { isOmnipusAgentId } from '@/lib/omnipus'
import { logDiagnostic } from '@/lib/telemetry'
import { findLastAssistantMessageId, findOpenAssistantMessageId, getMessages } from '../messages'
import { EMPTY_BUCKET, gapReattachRetryTimers, inFlightReattachSids, pendingCancelAckSids, pendingRedirectSids, replayErrorRetryTimers, replayingClearTimers } from '../runtime-state'
import { isClearCommandText, isClearContextMarker } from '@/lib/clearMarker'
import { checkAddressedAgent, recipientAddressFailedBlockMessage, recipientAddressResendBlockMessage, recipientAddressUnresolvedBlockMessage, resolveAddressedAgent, retryAgentsRoster } from '../recipientSend'
import { armClearRefresh } from '../clear-refetch'
import { applyMessageArray, bakeOwnedCallsAtSteerClose, bakeToolCallsByOwner, stampToolCallOffset } from '../session'
import type { ChatMessage, ChatStore, MediaAttachment, OutboundTarget, PositionedToolCall, SendMessageOptions, SessionChatState } from '../types'
import { captureOutboundTarget, loadedOutboundSession, outboundTargetIsForeground, resolveOutboundTarget } from '../outbound-target'
import { getPendingFirstSend, startOrdinaryFirstSend } from '../first-send'
import { discardHistoryStaging } from '../history'
import { armMessageDeliveryDeadline, remainingDeliveryDelay } from '../delivery-deadline'
import { armQueuedDeliveryDeadline, bindQueuedProvisionalChat, retryQueuedMessage } from '../outbound-queue'

/** Accepted sends request a fresh current window; failures never move a reader. */
function followAcceptedHistorySend(get: () => ChatStore, sid: string): void {
  if (get().sessionsById[sid]?.history.viewMode !== 'historical') return
  queueMicrotask(() => {
    if (useSessionStore.getState().activeSessionId === sid && get().sessionsById[sid]?.history.viewMode === 'historical') void get().returnToLatestHistory(sid)
  })
}

// ── #823 message-status helpers ──────────────────────────────────────────────
// Kept at module scope (not nested inside createOutboundLifecycleSlice/
// sendMessage) so their bodies do not count against those two functions'
// grandfathered line budgets (scripts/budgets/functions.txt) — see
// docs/internal/architecture/draft-module-map.md, "Size budgets".

/** FR-122 (omnipus-system-agent K16): there is no default agent — a NEW chat
 * names its agent on the minting frame, and the gateway refuses an agent-less
 * one with the error frame "an agent must be named". A fresh sendMessage
 * refuses here instead: visible warning, nothing put on the wire. Retained
 * first-send retries reuse their original named-agent payload. Returns true
 * when the caller must stop.
 * (Follow-up frames on a REAL session are unaffected — the gateway routes
 * those to the session's canonical owner.) */
function refuseAgentlessNewChat(agentId: string | null | undefined, where: string): boolean {
  if (agentId) return false
  useUiStore.getState().addToast({
    message: 'Select an agent before sending — there is no default agent.',
    variant: 'warning',
  })
  logDiagnostic('chatNewChatRefusedNoAgentNamed', { where })
  return true
}

/** Resolves the client-generated correlation id + queued timestamp for a
 * send, defaulting both when the caller (a fresh sendMessage call, not a
 * queue replay) didn't supply them. */
function beginSend(content: string, target: OutboundTarget, opts?: SendMessageOptions) {
  const clientMessageId = opts?.clientMessageId ?? generateId()
  const queuedAt = opts?.queuedAt ?? new Date().toISOString()
  const options = { mediaRefs: opts?.mediaRefs, attachments: opts?.attachments, model_name: opts?.model_name, workflowStart: opts?.workflowStart }
  return { clientMessageId, queuedAt, queuedMessage: { id: clientMessageId, content, timestamp: queuedAt, target, options, deadlineAt: opts?.deliveryDeadlineAt } }
}

/** Builds the optimistic user bubble for a send, carrying the correlation id
 * that MessageStatusFrame will later echo back (#823 state A). `mediaRefs`
 * (review finding 17) is stored alongside the display-only `media` so a
 * later `resendMessage` call can resend the SAME attachments — `media`
 * alone carries no wire-usable ref, see ChatMessage.mediaRefs' doc comment.
 * Founder ruling 2026-10-11: a /clear shows as QUEUED from the moment it is
 * sent — idle or mid-turn — and stays queued until the server's clear lands
 * and the fresh view replaces the screen (or the "/clear was not performed"
 * reply resolves the row); it never wears the transient "sending" look. */
function buildQueuedUserMessage(
  sessionId: string,
  content: string,
  clientMessageId: string,
  queuedAt: string,
  attachments: MediaAttachment[] = [],
  mediaRefs: string[] = [],
): ChatMessage {
  const isClear = isClearCommandText(content)
  return {
    id: clientMessageId,
    session_id: sessionId,
    role: 'user',
    content,
    timestamp: queuedAt,
    status: 'done',
    deliveryStatus: isClear ? 'queued' : 'sending',
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

/** A mid-turn steering send that never reached the gateway: the in-flight
 * turn is unaffected — only THIS message failed. Mark just the user bubble
 * as 'error' (Retry affordance) and leave `isStreaming` alone; there is no
 * assistant placeholder to roll back because a mid-turn steering send never
 * creates one. ADR-070 §2.1 deliberately does NOT close the pre-steer bubble
 * here: the backend never received this steer, so it keeps writing into the
 * SAME original segment exactly as before — closing the bubble would be
 * actively wrong, not just unnecessary (a real, previously-uncaught gap:
 * closing it unconditionally alongside the append broke the assertion that
 * branch's own pre-existing test pins).
 * Extracted out of sendMessage (pure relocation, 2026-10-10, same move
 * performResendMessage made) so sendMessage stays within its grandfathered
 * function-size budget; the behavior is byte-identical. */
function handleFailedSteerSend(
  withBucket: (sid: string | null, updater: (bucket: SessionChatState) => Partial<SessionChatState>) => void,
  sessionId: string,
  userMsgId: string,
): void {
  withBucket(sessionId, (b) => produce(b, (draft) => {
    markUserMessageFailed(draft, userMsgId)
  }) as Partial<SessionChatState>)
  useConnectionStore.getState().setConnectionError(
    'Message could not be sent — connection dropped. Your message was kept; press Retry to resend.'
  )
}

/** FR-030/031 (U10b): a /clear just went out on the wire — record the
 * transcript re-read the command owes the view (the server's marker entry is
 * never pushed live). One shared arm for EVERY path that can put a /clear on
 * the wire: the normal sendMessage branches and the Retry/resend path. A
 * send that failed never reaches here, so a failed send arms nothing.
 * Founder ruling 2026-10-11: the arm also snapshots the marker ids the
 * session's view already carries — the server keeps no link between a /clear
 * row and the marker it produced, so the acceptance keys on a marker the
 * view did not already have (clear-refetch.ts::projectionCarriesFreshMarker). */
function armClearRefreshForSend(sessionId: string, clientMessageId: string, get: StoreApi<ChatStore>['getState']): void {
  const bucket = get().sessionsById[sessionId]
  const knownMarkerIds = bucket
    ? bucket.messageOrder.filter((id) => isClearContextMarker(bucket.messagesById[id] ?? {}))
    : []
  const latestKnown = bucket?.history.pageMetadata?.page_info.latest_clear?.entry_id
  if (latestKnown && !knownMarkerIds.includes(latestKnown)) knownMarkerIds.push(latestKnown)
  armClearRefresh(sessionId, clientMessageId, bucket?.activeTurnId ?? null, knownMarkerIds)
}

/** The @-recipient on an outbound frame (founder decision 2026-10-11 Q1 B —
 * the restored @ agent menu addresses the MESSAGE; FR-029, no in-chat
 * handover). Derived AT SEND TIME from the leading "@<Agent name>" prefix of
 * the content, so it is not sticky: an edited prefix sends no recipient and
 * the @-text travels verbatim either way. Attaching it is legal ONLY next to
 * a session_id (contracts/asyncapi.yaml MessageFrame.recipient — the server
 * refuses the pair alone), so every caller attaches it only in the
 * session-carrying payloads; the composer's own visible block
 * (ChatScreen's recipientAddressBlocked) is what stops an @-addressed
 * submit in a brand-new chat long before this line. */
function outgoingRecipient(content: string, target?: OutboundTarget): { workspace_id: string; agent_id: string } | null {
  return resolveAddressedAgent(content, target)?.recipient ?? null
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
  if (blockSendForWorkspaceEntry('resend')) return

  const ordinary = getPendingFirstSend(state)
  if (ordinary && (existing.clientMessageId ?? existing.id) === ordinary.clientMessageId) {
    state.retryFirstSend()
    return
  }

  const content = existing.content
  const mediaRefs = existing.mediaRefs ?? []
  const targetSid = sid

  // C8 (R3 delta re-review): never drop the @ silently on a resend. A
  // '__pending' target has no session for the recipient pair to ride on (the
  // recipient-only-with-session_id wire rule), and an agents cache that has
  // never resolved cannot check the address at all — the old code sent both
  // unaddressed, a silent drop. Both refuse VISIBLY, the same way the
  // composer blocks an @-send in a brand-new chat, and the row stays in its
  // failed state (before the 'sending' reset below) so Retry remains. A
  // roster that FAILED outright (S3-08) says so and offers a Retry of the
  // load, instead of the loading copy's "try again in a moment".
  const addressCheck = checkAddressedAgent(content)
  if (addressCheck.kind === 'unresolvable') {
    useUiStore.getState().addToast({
      message: recipientAddressUnresolvedBlockMessage(addressCheck.nameGuess),
      variant: 'warning',
    })
    logDiagnostic('chatRecipientResendBlockedAgentsUnavailable', { nameGuess: addressCheck.nameGuess })
    return
  }
  if (addressCheck.kind === 'failed') {
    useUiStore.getState().addToast({
      message: recipientAddressFailedBlockMessage(addressCheck.nameGuess),
      variant: 'warning',
      action: { label: 'Retry', onClick: retryAgentsRoster },
    })
    logDiagnostic('chatRecipientResendBlockedAgentsErrored', { nameGuess: addressCheck.nameGuess })
    return
  }
  if (targetSid === '__pending' && addressCheck.kind === 'addressed') {
    useUiStore.getState().addToast({
      message: recipientAddressResendBlockMessage(addressCheck.addressed.agentName),
      variant: 'warning',
    })
    logDiagnostic('chatRecipientResendBlockedNoSession', { agentName: addressCheck.addressed.agentName })
    return
  }
  // A background Retry belongs to its target session, not the foreground
  // agent. If its owner is not loaded, keep the failed row retriable and
  // explain how to recover instead of guessing or minting another chat.
  const targetSession = loadedOutboundSession(targetSid)
  const targetAgentId = targetSession?.agent_id
  if (!targetAgentId?.trim()) {
    logDiagnostic('chatResendBlockedOwnerUnavailable', {
      sessionId: targetSid,
      messageId,
      reason: targetSession ? 'missing_or_blank_owner' : 'session_metadata_unavailable',
    })
    useUiStore.getState().addToast({
      message: 'Could not retry this message — the conversation\'s agent is not loaded. Reopen the conversation and try again.',
      variant: 'error',
    })
    return
  }

  // Reset the SAME message in place first — 'sending' — never a second
  // bubble, unlike the pre-fix `sendMessage(content)` call this replaces.
  withBucket(targetSid, (b) => produce(b, (draft) => {
    const m = draft.messagesById[messageId]
    if (m) {
      m.status = 'done'
      m.deliveryStatus = 'sending'
      delete m.deliveryUnconfirmed
    }
    // A new answer was requested for this question: it is no longer unanswered.
    if (draft.unansweredLastUserMessageId === messageId) draft.unansweredLastUserMessageId = null
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

  const workspaceId = targetSid === '__pending' ? '' : outboundWorkspaceId(targetSid)
  const payload = {
    type: 'message' as const,
    content,
    session_id: targetSid === '__pending' ? undefined : targetSid,
    client_message_id: messageId,
    agent_id: targetAgentId,
    ...(workspaceId ? { metadata: { workspace_id: workspaceId } } : {}),
    ...(mediaRefs.length > 0 ? { media: mediaRefs } : {}),
    // Recipient re-derived from the kept content at resend time. A '__pending'
    // target has no session for the pair to ride on, so the recipient is
    // omitted there (the recipient-only-with-session_id wire rule) — a corner
    // the retained-first-send retry path (`retryFirstSend`) normally owns.
    ...(targetSid !== '__pending' ? (() => {
      const recipient = outgoingRecipient(content)
      return recipient ? { recipient } : {}
    })() : {}),
  }
  get()._validateOutboundFrame(payload, targetSid)
  if (!isClearCommandText(content)) armMessageDeliveryDeadline(get, withBucket, targetSid, messageId, () => get().drainOutboundQueue())
  const sent = connection.send(payload)
  if (!sent) {
    withBucket(targetSid, (b) => produce(b, (draft) => {
      markUserMessageFailed(draft, messageId)
    }) as Partial<SessionChatState>)
    useConnectionStore.getState().setConnectionError(
      'Message could not be resent — connection dropped. Your message was kept; press Retry to resend.'
    )
    return
  }
  // FR-030/031 (U10b): a Retry that puts a /clear back on the wire arms the
  // same transcript re-read the original send would have (review round 1,
  // F3) — the retry IS the send, so the refresh belongs to it.
  if (isClearCommandText(content)) armClearRefreshForSend(targetSid, messageId, get)
}

type OutboundLifecycleSlice = Pick<ChatStore,
  | 'enqueueOutboundMessage'
  | 'drainOutboundQueue'
  | '_validateOutboundFrame'
  | 'sendMessage'
  | 'resendMessage'
  | 'cancelStream'
  | 'clearStreamingState'
>

interface OutboundLifecycleContext {
  set: StoreApi<ChatStore>['setState']
  get: StoreApi<ChatStore>['getState']
  getActiveSid: () => string | null
  withBucket: (sid: string | null, updater: (bucket: SessionChatState) => Partial<SessionChatState>) => void
  bucketToForeground: (bucket: SessionChatState) => Omit<SessionChatState, 'messageOrder' | 'trimmedCount' | 'spanBySpanId' | 'pendingSpanUpdatesBySpanId' | 'toolCallOwnerMessageId'> & { messages: ChatMessage[]; lastAssistantMessageId: string | null }
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
//   - Safety hardening (subagent-control-plane stream): the gap-reattach
//     retry timer (scheduleGapReattachRetry, frames.ts) is the same
//     shape of hazard as the replay_error timer just above — cancel it too,
//     for the same reason. gapReattachRetryAttempts is intentionally left
//     alone (same as replayErrorRetryAttempts): the backoff count should
//     keep escalating across a flapping connection, not reset on every
//     disconnect.
function clearCatchUpSideChannelsOnDisconnect(): void {
  discardHistoryStaging()
  inFlightReattachSids.clear()
  for (const sid of Object.keys(replayErrorRetryTimers)) {
    clearTimeout(replayErrorRetryTimers[sid])
    delete replayErrorRetryTimers[sid]
  }
  for (const sid of Object.keys(gapReattachRetryTimers)) {
    clearTimeout(gapReattachRetryTimers[sid])
    delete gapReattachRetryTimers[sid]
  }
}

/** The immutable session binding, never the ambient workspace for a follow-up. */
function outboundWorkspaceId(sessionId: string | null, target?: OutboundTarget): string {
  let workspaceId: components['schemas']['Session']['workspace_id'] | null
  if (sessionId && sessionId !== '__pending') {
    // These validated reads already back the saved-chat route and sidebar.
    // Resolve by the TARGET id so a background message's Retry cannot borrow
    // the foreground session's binding. Missing metadata, like a known
    // workspace-less session, omits the field: the server retains its binding.
    workspaceId = loadedOutboundSession(sessionId)?.workspace_id
  } else {
    // Only a NEW chat has no session binding yet. Keep its creation context
    // (and the retained first-send payload's retry context) unchanged.
    // Omnipus's one chat lives in the default workspace; workflow_start,
    // not this metadata, names the workspace its workflow acts on.
    if (isOmnipusAgentId(target ? target.agentId : useSessionStore.getState().activeAgentId)) return ''
    workspaceId = target ? target.workspaceId : useWorkspacesStore.getState().activeWorkspaceId
  }
  // Preserve the MessageFrame contract's workspace-id bound.
  return typeof workspaceId === 'string' && workspaceId.length > 0 && workspaceId.length <= 128
    ? workspaceId
    : ''
}

export function createOutboundLifecycleSlice({ set, get, getActiveSid, withBucket, bucketToForeground, maybeDrainNext, runtime }: OutboundLifecycleContext): OutboundLifecycleSlice {
  return {
    enqueueOutboundMessage: (content, queuedMessage) => {
      const MAX_QUEUE = 5
      const current = get().outboundQueue
      if (current.length >= MAX_QUEUE) {
        return false
      }
      if (queuedMessage) armQueuedDeliveryDeadline(get, set, queuedMessage)
      set({ outboundQueue: [...current, queuedMessage ?? content] })
      return true
    },

    drainOutboundQueue: () => {
      const queue = get().outboundQueue
      // The early-return used to skip straight past `maybeDrainNext()`
      // whenever `outboundQueue` was empty — but terminal cleanup paths call
      // THIS function specifically (not `maybeDrainNext()` directly) to
      // release whatever is waiting, and what's waiting may already sit in
      // `pendingDrainQueue` (moved there by an EARLIER drain cycle that got
      // stuck behind an `isStreaming` window) rather than in
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
      const target = resolveOutboundTarget(opts?.target ?? captureOutboundTarget(get()), get())
      if (outboundTargetIsForeground(target) && blockSendForWorkspaceEntry('send')) return false
      const mediaRefs = opts?.mediaRefs ?? []
      const attachments = opts?.attachments ?? []
      const { clientMessageId, queuedAt, queuedMessage } = beginSend(content, target, opts)
      // Phase 1 / FR-010: per-turn model override. Trim and strip empty
      // strings so absent and "" are equivalent (the WS frame is omitted
      // entirely when no model was picked this session, per spec §18 Q3).
      const modelNameRaw = opts?.model_name
      const modelName = typeof modelNameRaw === 'string' ? modelNameRaw.trim() : ''

      const { sessionId: rawActiveSessionId, agentId: activeAgentId } = target
      // Ordinary sends, mid-turn steering and new-answer requests share the
      // attached session's canonical binding; only a first send uses context.
      const workspaceId = outboundWorkspaceId(rawActiveSessionId, target)
      // K8 (FR-012): target data naming the workflow a screen button started.
      const workflowStartFrame = opts?.workflowStart ? { workflow_start: opts.workflowStart } : {}

      // Merge model_name + workspace_id into a single `metadata` object so the
      // two payload build sites below stay in sync. The frame omits `metadata`
      // entirely when neither field is present (an empty object would be sent
      // otherwise, which the server treats the same but is noise on the wire).
      const metadata: NonNullable<MessageFrame['metadata']> = {}
      if (modelName.length > 0) metadata.model_name = modelName
      if (workspaceId.length > 0) metadata.workspace_id = workspaceId
      const metadataFrame = Object.keys(metadata).length > 0 ? { metadata } : {}
      const { connection, isConnected } = useConnectionStore.getState()
      const isStreaming = outboundTargetIsForeground(target) ? get().isStreaming : rawActiveSessionId ? get().sessionsById[rawActiveSessionId]?.isStreaming ?? false : false
      // A stale, idle __pending sentinel cannot cross the wire. A live
      // provisional chat keeps its own queue until its correlated save arrives.
      const activeSessionId = (rawActiveSessionId === '__pending' && !isStreaming) ? null : rawActiveSessionId

      // A steer targets its own running turn; disconnected sends still queue.
      if (!connection || !isConnected || ((!rawActiveSessionId || rawActiveSessionId === '__pending') && !outboundTargetIsForeground(target))) {
        // WS is disconnected — buffer the message for when the connection
        // recovers rather than losing it silently or showing a hard error.
        const enqueued = get().enqueueOutboundMessage(content, queuedMessage)
        if (!enqueued) {
          useConnectionStore.getState().setConnectionError(
            'Queue full (5 messages max) — waiting to reconnect. Oldest pending messages will be sent first.'
          )
        }
        return enqueued
      }

      // An uncertain ordinary send owns __pending until confirmed or abandoned.
      if (getPendingFirstSend(get()) && (rawActiveSessionId === null || rawActiveSessionId === '__pending')) {
        const enqueued = get().enqueueOutboundMessage(content, queuedMessage)
        if (!enqueued) {
          useConnectionStore.getState().setConnectionError('Queue full (5 messages max) — confirm delivery or start a new chat.')
        }
        return enqueued
      }
      if (activeSessionId !== null) {
        const userMsg: ChatMessage = buildQueuedUserMessage(activeSessionId, content, clientMessageId, queuedAt, attachments, mediaRefs)

        // A steer adds only its user row. It never creates a second assistant
        // placeholder or changes whether the existing turn is running.
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
            return enqueued
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
            ...workflowStartFrame,
            // @-addressed steer (founder 2026-10-11 Q1 B): legal here — a
            // steer always carries a real session_id ('__pending' steers are
            // enqueued above, never put on the wire).
            ...(() => {
              const recipient = outgoingRecipient(content, target)
              return recipient ? { recipient } : {}
            })(),
          }
          get()._validateOutboundFrame(steerPayload, activeSessionId)
          if (!isClearCommandText(content)) armMessageDeliveryDeadline(get, withBucket, activeSessionId, userMsg.id, maybeDrainNext, remainingDeliveryDelay(opts?.deliveryDeadlineAt))
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
            handleFailedSteerSend(withBucket, activeSessionId, userMsg.id)
            return true
          }

          // Only this send owns its clear confirmation or explicit follow request.
          if (isClearCommandText(content)) armClearRefreshForSend(activeSessionId, clientMessageId, get)
          else followAcceptedHistorySend(get, activeSessionId)

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
          return true
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
          ...workflowStartFrame,
          // @-addressed ordinary send (founder 2026-10-11 Q1 B): the pair
          // rides WITH this session_id, never alone.
          ...(() => {
            const recipient = outgoingRecipient(content, target)
            return recipient ? { recipient } : {}
          })(),
        }
        get()._validateOutboundFrame(payload, activeSessionId)
        if (!isClearCommandText(content)) armMessageDeliveryDeadline(get, withBucket, activeSessionId, userMsg.id, maybeDrainNext, remainingDeliveryDelay(opts?.deliveryDeadlineAt))
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
        } else {
          // Only this send owns its clear confirmation or explicit follow request.
          if (isClearCommandText(content)) armClearRefreshForSend(activeSessionId, clientMessageId, get)
          else followAcceptedHistorySend(get, activeSessionId)
        }
      } else {
        if (refuseAgentlessNewChat(activeAgentId, 'send')) return false
        runtime.agentIdAtLastMintSend = activeAgentId ?? null
        const autoApproveChoice = get().pendingAutoApproveChoice
        set(state => ({ outboundQueue: bindQueuedProvisionalChat(state.outboundQueue, target, clientMessageId), pendingDrainQueue: bindQueuedProvisionalChat(state.pendingDrainQueue, target, clientMessageId) }))
        startOrdinaryFirstSend({ set, get, withBucket }, {
          type: 'message',
          content,
          client_message_id: clientMessageId,
          agent_id: activeAgentId ?? undefined,
          ...(mediaRefs.length > 0 ? { media: mediaRefs } : {}),
          ...metadataFrame,
          ...(autoApproveChoice !== null ? { auto_approve: autoApproveChoice } : {}),
        }, queuedAt, workspaceId || null, attachments, opts?.deliveryDeadlineAt)
      }
      return true
  },

    // Review finding 17: see performResendMessage's doc comment (module
    // scope, above) for the bug this replaces and why the body lives there
    // rather than here.
    resendMessage: (messageId) => {
      if (!retryQueuedMessage(get, set, messageId)) performResendMessage(get, getActiveSid, withBucket, messageId)
    },

    cancelStream: (sessionId, scope) => {
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

      // G1: between a goal's turns nothing streams but the goal keeper will resume
      // the session, so an active goal counts as stoppable. No goal, nothing sent.
      const goalRunning = isGoalRunning(targetSid ? get().sessionsById[targetSid]?.goalStatus : null)
      // A Stop marks the answer interrupted only while a turn is actually streaming.
      // An idle gap (goal running, nothing streaming) sends the cancel but leaves
      // the finished answer untouched; an idle chat with no goal sends nothing and
      // marks nothing. A Stop that races a `done` frame therefore marks nothing: the
      // answer did finish. The one idle case that still shows "Stopping..." is `/stop`
      // (cancelUnconditional): by design, the founder's second-Stop tree rule needs
      // its first press to arm the confirmation window even on an idle parent chat.
      if (targetIsStreaming) get().markLastMessageInterrupted(sessionId)
      // A plain Stop supersedes any pending redirect: its turn_canceled is a
      // stop, and must keep "(interrupted)".
      if (targetSid) pendingRedirectSids.delete(targetSid)

      // W2: a cancel that cannot even reach a transport is reported — to the
      // caller (return false, so the Stop window is never armed for a click
      // that sent nothing) and to the user (visible error toast).
      if (!connection) {
        useUiStore.getState().addToast({
          message: 'Could not send cancel — the gateway connection is down. The response may keep running; reconnect and press Stop again.',
          variant: 'error',
        })
        return false
      }
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
        return true
      }

      // `delivered` is false only when the cancel could not be handed to the
      // socket (the send-failed toast below is already shown). A completed
      // turn with nothing to send is not a failure.
      let delivered = true

      // ADR-20260928 D9: a CONFIRMED tree stop sends even when this
      // session's own turn already ended locally (the first activation
      // marked it interrupted): the confirmation targets the whole tree,
      // whose descendants may still be running. Session-scoped cancels keep
      // the completed-turn no-op gate exactly as before — sending cancel for
      // a completed turn is a no-op on the server but wastes a round-trip
      // and may confuse the audit log.
      if (targetIsStreaming || goalRunning || scope === 'tree') {
        // ADR-20260928 MAJ-002: `scope` rides the generated CancelFrame
        // verbatim. Omitted (every pre-existing call path) goes out WITHOUT
        // the key — the wire default is `session`, a single-session stop the
        // server must never cascade — and keeps the frame byte-identical to
        // the pre-stop-all shape. Only the confirmed tree stop passes
        // 'tree' (server dispatches on scope before cancelSteeredSubtree;
        // never up or sideways — ADR D7).
        const cancelFrame: CancelFrame = scope
          ? { type: 'cancel', session_id: targetSid, scope }
          : { type: 'cancel', session_id: targetSid }
        const sent = connection.send(cancelFrame)
        if (!sent) {
          delivered = false
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
      return delivered
    },

    clearStreamingState: () => {
      if (getPendingFirstSend(get())) get().markFirstSendDisconnected()
      // F-S3: a socket drop means no more terminal frames are coming for any
      // outstanding cancel — stale entries here would otherwise persist across
      // reconnects and could misattribute an unrelated later frame.
      pendingCancelAckSids.clear()
      pendingRedirectSids.clear()
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
