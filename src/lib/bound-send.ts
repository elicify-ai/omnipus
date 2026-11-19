// #1428 step 2 (spec C r9, 4.6-4.10): every message, redirect and cancel the
// browser sends is bound to the chat it was typed in.
//
// - captureSubmission freezes what a path sends (text, files, library picks,
//   origin, target, workspace, agent) at the path's capture event (4.6.1).
// - sendBound runs the pipeline: local origin/target check -> capability gate
//   (4.10) -> creation for a new-chat key K -> uploads to the frozen workspace
//   -> eligibility check -> hand-over (4.6.2). Nothing re-reads the open chat
//   after capture except the eligibility check.
// - sendCommand creates the command record of every Stop, Esc, tree stop and
//   typed /stop-redirect, /cancel, /stop and hands its frame over (4.7).
// - observeBoundSendFrame settles refusals, receipts, the confirmed-404 signal
//   and the target-running reconciliation after a reconnect (4.7, 4.8).
//
// This is the only production file that may build a message, redirect or
// cancel frame literal (AL-1, scripts/check-bound-send.sh).

import type {
  CancelFrame,
  ClientFrame,
  MessageFrame,
  RedirectFrame,
  SendOrigin,
  ServerFrame,
} from '@/lib/api/generated/asyncapi-types'
import { uploadFiles } from '@/lib/api'
import { ApiError } from '@/lib/api-error'
import { generateId } from '@/lib/constants'
import { logDiagnostic } from '@/lib/telemetry'
import { WsConnection, subscribeConnectionEvents, type SendCapability } from '@/lib/ws'
import { useConnectionStore } from '@/store/connection'
import { onSignOut } from '@/store/auth'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useChatStore } from '@/store/chat/store'
import type { MediaAttachment } from '@/store/chat/types'
import { isImageAttachment } from '@/components/chat/AttachmentCard'
import { mediaRefURL } from '@/lib/library-attachment'
import { registerOwningChatCapture } from '@/lib/owning-chat-capture'
import { createForNewChat, resetNewChatCreations } from '@/lib/new-chat-creation'
import {
  NO_CHAT_KEY,
  addUnsentItem,
  canonical,
  clearNotStopped,
  currentAuthGeneration,
  establishNotStopped,
  getDraft,
  hasUnsavedInput,
  isNewChatKey,
  keyOfOrigin,
  mirrorOpenDraft,
  openChatKey,
  openDraftKey,
  originForKey,
  resetDraftsForSignOut,
  restoreIntoDraft,
  setDraftText,
  mergeRestoredText,
  settleItemsSentAgain,
  updateDraft,
  useDraftStore,
  type CommandRecord,
  type DraftFile,
  type LibraryPick,
} from '@/store/chat/drafts'

// ── Exact user-visible strings (spec 4.7-4.10) ──────────────────────────────

export const SEND_TEXT = {
  switched: 'Not sent because you switched chats',
  connFailed: 'Not sent: the connection failed. Send it again when connected.',
  notConnected: 'Not connected. Try again when connected.',
  pageError: 'Could not be sent because of a page error. Reload if it happens again.',
  unverified: 'The server could not check this chat. Try again.',
  upload404: 'This chat no longer exists',
  upload409: 'File not sent because of a page error. Reload if it happens again.',
  upload413: 'File too large',
  uploadNetwork: 'Upload failed. Try again.',
  notStopped: 'Not stopped. Press Stop again.',
  unsupportedBanner: 'This page and the server are different versions. Your unsent text is kept below. Reload the page to send.',
  restoredLine: 'Restored, not sent. Send it again or discard it.',
  uncertainLine: 'This may already have reached the agent. Check that chat before sending it again.',
} as const

// ── Submissions (4.6.1) ─────────────────────────────────────────────────────

export interface ModelChoice { // not-wire-format: per-turn model choice frozen at capture
  model_name?: string
  provider?: string
  reasoning_effort?: string
}

export interface SubmissionInputs { // not-wire-format: what a send path freezes at its capture event
  text: string
  files: DraftFile[]
  libraryPicks: LibraryPick[]
  workspaceId: string | null
  agentId: string | null
  origin: SendOrigin
  /** The saved chat the frame names; null = create for the new-chat key K. */
  target: string | null
  navigationSensitive: boolean
  clientMessageId?: string
  model?: ModelChoice
  workflowStart?: MessageFrame['workflow_start']
}

export interface Submission extends Readonly<SubmissionInputs> { // not-wire-format: a frozen submission
  readonly id: string
  readonly path: string
  readonly clientMessageId: string
  readonly authGeneration: number
}

/** Freezes a submission at its capture event (4.6.1). */
export function captureSubmission(path: string, inputs: SubmissionInputs): Submission {
  const clientMessageId = inputs.clientMessageId ?? generateId()
  return Object.freeze({
    ...inputs,
    files: Object.freeze([...inputs.files]) as DraftFile[],
    libraryPicks: Object.freeze([...inputs.libraryPicks]) as LibraryPick[],
    origin: Object.freeze({ ...inputs.origin }),
    id: generateId(),
    path,
    clientMessageId,
    authGeneration: currentAuthGeneration(),
  })
}

/** The model choice the next message carries, read at capture. */
export function modelChoiceNow(): ModelChoice | undefined {
  const { nextModel, nextModelProvider, nextEffort } = useChatStore.getState()
  if (!nextModel) return undefined
  return {
    model_name: nextModel,
    ...(nextModelProvider ? { provider: nextModelProvider } : {}),
    ...(nextEffort ? { reasoning_effort: nextEffort } : {}),
  }
}

/** Capture of the open chat for a composer path: the open key's own inputs. */
export function captureForOpenKey(path: string, key: string, inputs: Pick<SubmissionInputs, 'text' | 'files' | 'libraryPicks'>, navigationSensitive = true): Submission {
  const newChat = isNewChatKey(key) && canonical(key) === key ? useDraftStore.getState().newChats[key] : undefined
  const session = useSessionStore.getState()
  return captureSubmission(path, {
    ...inputs,
    workspaceId: newChat ? newChat.workspaceId : useWorkspacesStore.getState().activeWorkspaceId,
    agentId: newChat?.agentId ?? session.activeAgentId,
    origin: originForKey(key),
    target: newChat ? null : key,
    navigationSensitive,
    model: modelChoiceNow(),
  })
}

/**
 * Every session composer submit entry (Send, form submit, idle and mid-stream
 * Enter) lands here (spec 4.5 rule 5). Library-only input is a silent no-op
 * (BR-10). Returns whether a submission was taken.
 */
export function submitDraft(path = 'Composer Send'): boolean {
  const key = openDraftKey()
  if (key === NO_CHAT_KEY) return false
  const draft = getDraft(key)
  if (draft.text.trim() === '' && draft.files.length === 0) return false
  const submission = captureForOpenKey(path, key, { text: draft.text, files: draft.files, libraryPicks: draft.libraryPicks })
  updateDraft(key, (current) => ({ ...current, text: '', files: [], libraryPicks: [], recovery: undefined }))
  mirrorOpenDraft('submit-clear')
  void sendBound(submission)
  return true
}

/** A send pinned to a saved chat by its caller (4.6.1 non-navigation rows, e.g. the browser annotation). */
export function sendToChat(path: string, pinned: { text: string; sessionId: string; agentId: string; workspaceId: string | null; files: DraftFile[]; model?: ModelChoice }): Promise<void> {
  return sendBound(captureSubmission(path, {
    text: pinned.text,
    files: pinned.files,
    libraryPicks: [],
    workspaceId: pinned.workspaceId,
    agentId: pinned.agentId,
    origin: { kind: 'chat', session_id: pinned.sessionId },
    target: pinned.sessionId,
    navigationSensitive: false,
    ...(pinned.model ? { model: pinned.model } : {}),
  }))
}

// Mermaid fix (4.6.1): the owning chat is frozen at the click; a running turn there takes no fix.
registerOwningChatCapture((path, text) => {
  const key = openDraftKey()
  if (key === NO_CHAT_KEY) return null
  const sub = captureForOpenKey(path, key, { text, files: [], libraryPicks: [] }, false)
  return () => {
    if (sub.target && useChatStore.getState().sessionsById[sub.target]?.isStreaming) return false
    void sendBound(sub)
    return true
  }
})

/** A runtime append (thread.append) submits its own text for the open chat. */
export function submitText(path: string, text: string): boolean {
  const key = openDraftKey()
  if (key === NO_CHAT_KEY || text.trim() === '') return false
  void sendBound(captureForOpenKey(path, key, { text, files: [], libraryPicks: [] }))
  return true
}

// ── Capability (4.10) ───────────────────────────────────────────────────────

export type SendState = SendCapability | 'offline'

/**
 * The store connection's send state. A transport that is not a WsConnection
 * (an in-process stand-in) carries no handshake and is treated as supported.
 */
export function currentSendCapability(): SendState {
  const { connection, isConnected } = useConnectionStore.getState()
  if (!connection || !isConnected) return 'offline'
  return connection instanceof WsConnection ? connection.sendCapability : 'supported'
}

// ── Restore (4.8) ───────────────────────────────────────────────────────────

function restoreSubmission(sub: Submission, reason: string | null, files?: DraftFile[]): void {
  const key = keyOfOrigin(sub.origin)
  const input = { text: sub.text, files: files ?? [...sub.files], libraryPicks: [...sub.libraryPicks] }
  restoreIntoDraft(key, input, reason)
  addUnsentItem({ kind: 'not_sent', key, ...input, reason, agentId: sub.agentId, workspaceId: sub.workspaceId })
  if (canonical(key) === openDraftKey()) mirrorOpenDraft('restore')
}

/** Unsupported mode: the input stays in its draft, with no recovery line (4.10). */
function keepInDraft(key: string, text: string, files: DraftFile[] = [], picks: LibraryPick[] = []): void {
  updateDraft(key, (draft) => ({
    ...draft,
    text: mergeRestoredText(text, draft.text),
    files: [...files, ...draft.files.filter((have) => !files.some((f) => f.id === have.id))],
    libraryPicks: [...picks, ...draft.libraryPicks.filter((have) => !picks.some((p) => p.ref === have.ref))],
  }))
  if (canonical(key) === openDraftKey()) mirrorOpenDraft('restore')
}

// ── Pipeline (4.6.2) ────────────────────────────────────────────────────────

/** Submissions from one origin key go out in the order they were taken (BR-6). */
const chains = new Map<string, Promise<void>>()
/** Handed-over messages by client_message_id, for refusals (4.8). */
const sentMessages = new Map<string, { submission: Submission; files: DraftFile[] }>()

function localCheck(sub: Submission): boolean {
  if (sub.origin.kind === 'chat') return !!sub.origin.session_id && sub.target === sub.origin.session_id
  const key = sub.origin.new_chat_key
  if (!key) return false
  return sub.target === null || canonical(key) === sub.target
}

/**
 * Sends one frozen submission. The local check and, when nothing has to be
 * awaited, the whole pipeline run synchronously in the caller's step.
 */
export function sendBound(sub: Submission): Promise<void> {
  if (!localCheck(sub)) {
    logDiagnostic('boundSendLocalCheckRefused', { path: sub.path, originKind: sub.origin.kind })
    restoreSubmission(sub, SEND_TEXT.pageError)
    return Promise.resolve()
  }
  const key = keyOfOrigin(sub.origin)
  const previous = chains.get(key)
  const needsAwait = sub.files.some((file) => file.kind === 'local')
  if (!previous && !needsAwait) {
    runPipelineSync(sub)
    return Promise.resolve()
  }
  const run = (previous ?? Promise.resolve()).then(() => runPipeline(sub))
  const settled = run.catch((err: unknown) => {
    logDiagnostic('boundSendPipelineFailed', { path: sub.path, message: err instanceof Error ? err.message : String(err) })
    restoreSubmission(sub, SEND_TEXT.pageError)
  })
  chains.set(key, settled)
  void settled.then(() => { if (chains.get(key) === settled) chains.delete(key) })
  return settled
}

/** Capability gate. Returns true when the submission may proceed now. */
function passGate(sub: Submission): boolean {
  const state = currentSendCapability()
  if (state === 'supported') return true
  if (state === 'unsupported') {
    keepInDraft(keyOfOrigin(sub.origin), sub.text, [...sub.files], [...sub.libraryPicks])
    return false
  }
  const queued = useChatStore.getState().enqueueOutboundMessage(sub.text, {
    id: sub.clientMessageId,
    content: sub.text,
    timestamp: new Date().toISOString(),
    submission: sub,
  })
  if (!queued) restoreSubmission(sub, SEND_TEXT.notConnected)
  return false
}

function runPipelineSync(sub: Submission): void {
  if (!passGate(sub)) return
  deliver(sub, sub.target, [...sub.files])
}

async function runPipeline(sub: Submission): Promise<void> {
  if (!passGate(sub)) return
  let target = sub.target
  const hasLocal = sub.files.some((file) => file.kind === 'local')
  if (target === null && hasLocal) {
    target = await createForKey(sub)
    if (sub.authGeneration !== currentAuthGeneration()) return
    if (!target) {
      restoreSubmission(sub, SEND_TEXT.uploadNetwork)
      return
    }
  }
  const files = hasLocal && target ? await uploadAll(sub, target) : { files: [...sub.files], reason: null }
  if (sub.authGeneration !== currentAuthGeneration()) return
  if (files.reason !== null) {
    if (!eligible(sub)) restoreSubmission(sub, SEND_TEXT.switched, files.files)
    else restoreSubmission(sub, files.reason, files.files)
    return
  }
  deliver(sub, target, files.files)
}

function eligible(sub: Submission): boolean {
  return !sub.navigationSensitive || openDraftKey() === keyOfOrigin(sub.origin)
}

function createForKey(sub: Submission): Promise<string | null> {
  return createForNewChat(sub.origin.new_chat_key ?? '', sub.agentId ?? '', sub.workspaceId)
}

function uploadReason(err: unknown): string {
  const status = err instanceof ApiError ? err.status : (err as { status?: number } | null)?.status
  if (status === 404) return SEND_TEXT.upload404
  if (status === 409) return SEND_TEXT.upload409
  if (status === 413) return SEND_TEXT.upload413
  return SEND_TEXT.uploadNetwork
}

/** One upload per local file to the frozen workspace (4.6.3). */
async function uploadAll(sub: Submission, target: string): Promise<{ files: DraftFile[]; reason: string | null }> {
  const out: DraftFile[] = []
  let reason: string | null = null
  for (const file of sub.files) {
    if (file.kind !== 'local') {
      out.push(file)
      continue
    }
    try {
      const result = await uploadFiles(target, [file.file], sub.workspaceId ?? undefined)
      const first = result?.files?.[0] as { ref?: string; name?: string; content_type?: string } | undefined
      if (!first?.ref) throw new Error('upload returned no media ref')
      const mime = first.content_type ?? file.file.type
      out.push({ kind: 'uploaded', id: file.id, ref: first.ref, workspaceId: sub.workspaceId, name: first.name ?? file.file.name, mime, previewUrl: mediaRefURL(first.ref) })
    } catch (err) {
      logDiagnostic('boundSendUploadFailed', { message: err instanceof Error ? err.message : String(err) })
      reason ??= uploadReason(err)
      out.push(file)
    }
  }
  return { files: out, reason }
}

/** Eligibility check and hand-over, in one synchronous step (4.6.2). */
function deliver(sub: Submission, target: string | null, files: DraftFile[]): void {
  if (!eligible(sub)) {
    restoreSubmission(sub, SEND_TEXT.switched, files)
    return
  }
  const mediaRefs: string[] = []
  const attachments: MediaAttachment[] = []
  for (const file of files) {
    if (file.kind !== 'uploaded') continue
    mediaRefs.push(file.ref)
    attachments.push({ type: isImageAttachment(file.name, file.mime) ? 'image' : 'file', url: file.previewUrl, filename: file.name, contentType: file.mime })
  }
  for (const pick of sub.libraryPicks) {
    mediaRefs.push(pick.ref)
    attachments.push({ type: isImageAttachment(pick.filename, pick.contentType) ? 'image' : 'file', url: mediaRefURL(pick.ref), filename: pick.filename, contentType: pick.contentType })
  }
  const bound = target === sub.target ? sub : Object.freeze({ ...sub, target })
  sentMessages.set(sub.clientMessageId, { submission: bound, files })
  useChatStore.getState().sendMessage(sub.text, {
    submission: bound,
    clientMessageId: sub.clientMessageId,
    ...(mediaRefs.length > 0 ? { mediaRefs, attachments } : {}),
    ...(sub.model ?? {}),
    ...(sub.workflowStart ? { workflowStart: sub.workflowStart } : {}),
  })
  settleItemsSentAgain(keyOfOrigin(sub.origin), sub.text)
}

/** The stored submission of a handed-over message: Retry keeps its id, origin and agent. */
export function submissionForMessage(clientMessageId: string): Submission | undefined {
  return sentMessages.get(clientMessageId)?.submission
}

/** A queued submission drains through the same pipeline (4.6.1 queue drain). */
export function drainSubmission(sub: Submission): void {
  void sendBound(sub)
}

/** Whether a queued submission belongs to the chat that is open now. */
export function submissionIsOpen(sub: Submission): boolean {
  return keyOfOrigin(sub.origin) === openDraftKey()
}

/** A legacy string queue entry is never sent: it becomes an Unsent item (4.6.1). */
export function legacyQueueEntryToItem(text: string): void {
  logDiagnostic('boundSendLegacyQueueEntry', { textLength: text.length })
  addUnsentItem({ kind: 'not_sent', key: 'legacy-queue', text, files: [], libraryPicks: [], reason: null, agentId: null, workspaceId: null })
}

// ── Frame builders (AL-1) ───────────────────────────────────────────────────

/** The origin a frame for this target carries when no submission is at hand. */
export function originForTarget(target: string | null): SendOrigin {
  if (target && target !== '__pending') return originForKey(target)
  const key = openChatKey()
  return key && key !== NO_CHAT_KEY ? originForKey(key) : { kind: 'chat', session_id: target ?? '' }
}

export function buildMessageFrame<F extends Omit<MessageFrame, 'type' | 'origin'>>(fields: F, origin: SendOrigin): MessageFrame & F {
  return { type: 'message', ...fields, origin }
}

export function buildCancelFrame(fields: Omit<CancelFrame, 'type' | 'origin'>, origin: SendOrigin): CancelFrame {
  return { type: 'cancel', ...fields, origin }
}

export function buildRedirectFrame(fields: Omit<RedirectFrame, 'type' | 'origin'>, origin: SendOrigin): RedirectFrame {
  return { type: 'redirect', ...fields, origin }
}

/**
 * The hand-over to the socket. A send that throws on an OPEN socket is a
 * local write failure (F-hh): reported to the caller as false, never thrown
 * past the send path, and recorded for the operator.
 */
export function handOver(connection: { send: (frame: ClientFrame) => boolean }, frame: ClientFrame): boolean {
  try {
    return connection.send(frame)
  } catch (err) {
    logDiagnostic('boundSendHandOverThrew', { type: frame.type, message: err instanceof Error ? err.message : String(err) })
    return false
  }
}

// ── Commands (4.7) ──────────────────────────────────────────────────────────

export interface CommandInput { // not-wire-format: a command at its capture event
  kind: CommandRecord['kind']
  target: string
  /** The draft key the command was typed in (typed commands) or the control chat. */
  originKey: string
  /** The typed command text; '' for Stop, Esc and tree stop. */
  text: string
  instruction?: string
  scope?: CancelFrame['scope']
  shown?: CancelFrame['shown']
}

function connectionOf(): { connection: unknown; generation: number | null } {
  const { connection } = useConnectionStore.getState()
  return { connection, generation: connection instanceof WsConnection ? connection.socketGeneration : null }
}

function putBackTyped(input: CommandInput, reason: string | null, quiet: boolean): void {
  if (!input.text) return
  if (quiet) {
    keepInDraft(input.originKey, input.text)
    return
  }
  restoreIntoDraft(input.originKey, { text: input.text, files: [], libraryPicks: [] }, reason)
  addUnsentItem({ kind: 'not_sent', key: canonical(input.originKey), text: input.text, files: [], libraryPicks: [], reason, agentId: null, workspaceId: null })
  if (canonical(input.originKey) === openDraftKey()) mirrorOpenDraft('restore')
}

/** A held typed command whose chat is no longer open at its flush (4.6.2). */
export function refuseHeldCommand(key: string, text: string): void {
  putBackTyped({ kind: 'redirect', target: key, originKey: key, text }, SEND_TEXT.switched, false)
}

/** The redirect transport of the session composer (useSlashMenu's sendRedirectFrame). */
export function sendTypedRedirect(instruction: string, typed: { text: string; originKey: string }): boolean {
  const originKey = typed.originKey || openDraftKey()
  return sendCommand({ kind: 'redirect', target: canonical(originKey), originKey, text: typed.text, instruction })
}

/**
 * Creates the record and hands the command over. Returns true only when the
 * frame reached the socket; on false the typed text is back in its draft and
 * the target shows the not-stopped notice. Never retried automatically.
 */
export function sendCommand(input: CommandInput): boolean {
  const state = currentSendCapability()
  const { connection, generation } = connectionOf()
  if (state !== 'supported' || !connection) {
    putBackTyped(input, SEND_TEXT.notConnected, state === 'unsupported')
    establishNotStopped(input.target)
    return false
  }
  const origin = originForKey(input.originKey)
  const clientFrameId = generateId()
  const turnId = useChatStore.getState().sessionsById[input.target]?.activeTurnId ?? null
  const record: CommandRecord = {
    clientFrameId, kind: input.kind, origin, originKey: canonical(input.originKey), target: input.target,
    text: input.text, connection, connectionGeneration: generation, turnId, state: 'sent',
  }
  setRecord(record)
  const frame = input.kind === 'redirect'
    ? buildRedirectFrame({ session_id: input.target, instruction: input.instruction ?? '', client_frame_id: clientFrameId }, origin)
    : buildCancelFrame({
      session_id: input.target, client_frame_id: clientFrameId,
      ...(input.scope ? { scope: input.scope } : {}),
      ...(input.shown ? { shown: input.shown } : {}),
    }, origin)
  const ok = handOver(connection as { send: (frame: ClientFrame) => boolean }, frame)
  if (!ok) {
    dropRecord(clientFrameId)
    putBackTyped(input, SEND_TEXT.connFailed, false)
    establishNotStopped(input.target)
  }
  return ok
}

function setRecord(record: CommandRecord): void {
  useDraftStore.setState((s) => ({ commands: { ...s.commands, [record.clientFrameId]: record } }))
}

function dropRecord(id: string): CommandRecord | undefined {
  const record = useDraftStore.getState().commands[id]
  if (!record) return undefined
  useDraftStore.setState((s) => {
    const commands = { ...s.commands }
    delete commands[id]
    return { commands }
  })
  return record
}

// ── Frames (4.7, 4.8) ───────────────────────────────────────────────────────

type RefusalReason = Extract<ServerFrame, { type: 'send_refused' }>['reason']

/** 4.8: only origin_malformed / frame_malformed add the page-error line; wrong_chat adds none. */
const REFUSAL_REASON: Record<RefusalReason, string | null> = {
  wrong_chat: null,
  origin_malformed: SEND_TEXT.pageError,
  frame_malformed: SEND_TEXT.pageError,
  origin_unverified: SEND_TEXT.unverified,
}

function onSendRefused(frame: Extract<ServerFrame, { type: 'send_refused' }>): void {
  const known: string | null | undefined = REFUSAL_REASON[frame.reason]
  const reason = known === undefined ? SEND_TEXT.pageError : known
  if (frame.client_frame_id) {
    const record = dropRecord(frame.client_frame_id)
    if (record) {
      putBackTyped({ kind: record.kind, target: record.target, originKey: record.originKey, text: record.text }, reason, false)
      establishNotStopped(record.target)
      return
    }
  }
  const sent = frame.client_message_id ? sentMessages.get(frame.client_message_id) : undefined
  if (!sent) {
    logDiagnostic('boundSendRefusalUnmatched', { reason: frame.reason, refused: frame.refused_frame })
    return
  }
  sentMessages.delete(sent.submission.clientMessageId)
  useChatStore.getState().withdrawRefusedMessage(sent.submission.target, sent.submission.clientMessageId)
  restoreSubmission(sent.submission, reason, sent.files)
}

function onCommandReceived(frame: Extract<ServerFrame, { type: 'command_received' }>): void {
  const record = dropRecord(frame.client_frame_id)
  if (record?.kind === 'stop' || record?.kind === 'cancel') clearNotStopped(record.target)
}

/**
 * BR-12 deleted-chat recovery is PARKED (#1904): the generated ErrorFrame has
 * no `code`, so nothing on the wire sends this signal today. The read stays so
 * the parked BR-12 tests keep exercising the reducer until #1904 decides the
 * contract.
 */
function onConfirmedNotFound(sessionId: string): void {
  for (const [id, sent] of sentMessages) {
    if (sent.submission.target !== sessionId) continue
    sentMessages.delete(id)
    const key = keyOfOrigin(sent.submission.origin)
    addUnsentItem({
      kind: 'deleted', key, text: sent.submission.text, files: sent.files, libraryPicks: [...sent.submission.libraryPicks],
      reason: SEND_TEXT.upload404, agentId: sent.submission.agentId, workspaceId: sent.submission.workspaceId,
    })
  }
}

function reconcileTarget(sessionId: string, activeTurn: string | null): void {
  for (const record of Object.values(useDraftStore.getState().commands)) {
    if (record.state !== 'unconfirmed' || record.target !== sessionId) continue
    if (activeTurn !== null && (record.turnId === null || record.turnId === activeTurn)) {
      dropRecord(record.clientFrameId)
      establishNotStopped(sessionId)
    }
  }
}

function dropUnreconciled(sessionId: string): void {
  for (const record of Object.values(useDraftStore.getState().commands)) {
    if (record.state === 'unconfirmed' && record.target === sessionId) dropRecord(record.clientFrameId)
  }
}

/** Runs for every inbound frame before the reducer (store.ts::handleFrame). */
export function observeBoundSendFrame(frame: ServerFrame): void {
  switch (frame.type) {
    case 'send_refused':
      onSendRefused(frame)
      return
    case 'command_received':
      onCommandReceived(frame)
      return
    case 'session_state':
      if (frame.session_id) reconcileTarget(frame.session_id, frame.active_turn?.turn_id ?? null)
      return
    case 'catch_up_complete':
      if (frame.session_id) dropUnreconciled(frame.session_id)
      return
    case 'done':
      if (frame.session_id) clearNotStopped(frame.session_id)
      return
    case 'message_status':
      // 4.4.3 "accepted send -> submission dropped": the server took the
      // message past its binding check, so no refusal can follow. A failed
      // status keeps the submission: Retry still needs its stored identity.
      if (frame.state !== 'failed') sentMessages.delete(frame.client_message_id)
      return
    case 'session_started':
      if (frame.client_message_id) sentMessages.delete(frame.client_message_id)
      return
    case 'error': {
      if (!frame.session_id) return
      // 4.7 / B-24: a message-scoped refusal (client_message_id, no
      // first_message_error) ends no turn, so it leaves the notice alone.
      if (!(frame.client_message_id && !frame.first_message_error)) clearNotStopped(frame.session_id)
      // Parked signal (#1904): the generated ErrorFrame has no `code`; nothing sends it today.
      if ((frame as { code?: unknown }).code === 'not_found') onConfirmedNotFound(frame.session_id)
      return
    }
    default:
  }
}

// ── Connection events (4.7 close row, 4.10 drain) ───────────────────────────

subscribeConnectionEvents((event) => {
  if (event.type === 'close') {
    for (const record of Object.values(useDraftStore.getState().commands)) {
      if (record.state !== 'sent' || record.connection !== event.connection || record.connectionGeneration !== event.generation) continue
      setRecord({ ...record, state: 'unconfirmed' })
      if (record.text) {
        addUnsentItem({ kind: 'uncertain', key: record.originKey, text: record.text, files: [], libraryPicks: [], reason: SEND_TEXT.uncertainLine, agentId: null, workspaceId: null })
      }
    }
    return
  }
  if (event.type === 'capability' && event.capability === 'supported' && useConnectionStore.getState().connection === event.connection) {
    useChatStore.getState().drainOutboundQueue()
  }
})

// B-23 (Q5 = A): drafts and unsent items live only as long as this page, so
// the browser is asked to warn before it is reloaded or closed while any exist.
// Browsers may still skip the warning; the help page says so.
if (typeof window !== 'undefined') {
  window.addEventListener('beforeunload', (event: BeforeUnloadEvent) => {
    if (!hasUnsavedInput()) return
    event.preventDefault()
    event.returnValue = ''
  })
}

// Sign-out (4.4.3): drafts, items, records and the pipeline's own maps go together.
onSignOut(() => {
  resetDraftsForSignOut()
  resetBoundSendForSignOut()
})

/** Sign-out (4.4.3): pending creations and handed-over messages are forgotten. */
export function resetBoundSendForSignOut(): void {
  chains.clear()
  resetNewChatCreations()
  sentMessages.clear()
}

/** The open chat's typed text, written by a non-composer writer (slash adapter). */
export function writeOpenDraftText(text: string): void {
  const key = openDraftKey()
  if (key === NO_CHAT_KEY) return
  setDraftText(key, text)
  mirrorOpenDraft('slash-menu')
}
