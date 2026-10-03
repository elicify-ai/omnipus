// mailAttachmentHandoff.ts — the Mail → Library temporary-viewer handoff
// (W3 spec §2.1/§3.1; W3 publishes the handoff trigger and the return path;
// US-6/US-7).
//
// What lives here:
//   - The mount seam W3 publishes: the Library side (ADR-W8 renderer work,
//     the w4 file) registers ONE handler; Mail calls it with the frozen
//     descriptor `{ source, context }` where `source` is the GENERATED
//     MailAttachmentPreviewResponse (metadata only — no path, no
//     LibraryEntry, no fabricated identity) and `context` carries the
//     subject and the return-focus identity (MC-W3-6/8).
//   - The return path: the Library side calls `onBack()`; this module
//     revokes the grant (the grant's bounded TTL is the contract's own
//     backstop when a revoke cannot be delivered — the generated field
//     description defines expiry as exactly that), disposes the descriptor,
//     resolves the US-7 AS-2 focus fallback (attachment action → message
//     row → folder tab) and announces where focus landed (S-19).
//   - The save controller: wraps the generated MailAttachmentSaveRequest
//     with a client-generated save_operation_token (M-02) and a status
//     channel (loading | saved | failed | unknown). FR-W3-22: an explicit
//     retry of an unknown-result save carries the SAME token and resolves
//     from the prior receipt; no automatic resend ever fires.
//   - The polite status-region announcer (I-06): announcements without
//     focus movement (US-7 AS-3).

import { ApiError, isApiError } from '@/lib/api-error'
import {
  mintMailAttachmentPreview,
  revokeMailAttachmentPreview,
  saveMailAttachmentToLibrary,
} from '@/lib/api/mail'
import type {
  MailAttachmentPreviewRequest,
  MailAttachmentPreviewResponse,
  MailAttachmentSaveResponse,
  MailUnavailableError,
} from '@/lib/api/generated/openapi-types'

// ── The descriptor (§3.1's frozen handoff payload) ───────────────────────────

/** Where focus must return to when the viewer closes (US-7 AS-2's
 * three-step fallback: attachment action → message row → folder tab). */
export type MailReturnFocus =
  | { kind: 'attachment-action'; id: string }
  | { kind: 'message-row'; id: string }
  | { kind: 'folder'; id: string }

export interface MailAttachmentHandoffContext {
  subject: string
  returnFocus: MailReturnFocus
}

/** The frozen handoff payload: generated response + W3's context. No
 * workspace path, no LibraryEntry — a fabricated identity would enable
 * stored-file actions on something that does not exist (spec §6). */
export interface MailAttachmentHandoff {
  source: MailAttachmentPreviewResponse
  context: MailAttachmentHandoffContext
}

export interface MailHandoffControls {
  /** The Library side calls this for Back/Escape; Mail decides the landing
   * surface (§3.1's return path — the Library never re-opens Mail's queries
   * itself). */
  onBack(): void
}

type MailAttachmentMountHandler = (handoff: MailAttachmentHandoff, controls: MailHandoffControls) => void

let mountHandler: MailAttachmentMountHandler | null = null

/** Register the W8-owned temporary-viewer mount. One handler at a time;
 * the Library side registers on mount and clears on unmount. */
export function setMailAttachmentMountHandler(handler: MailAttachmentMountHandler | null): void {
  mountHandler = handler
}

/** True while the Library viewer side has registered its mount seam. */
export function hasMailAttachmentMountHandler(): boolean {
  return mountHandler !== null
}

// ── The 25 MB preview cap (founder-set; S-13/S-26) ───────────────────────────

export const MAIL_ATTACHMENT_PREVIEW_CAP_BYTES = 25 * 1024 * 1024

/** Over-cap attachments preview nothing: Open and Save are unavailable and
 * Download stays the browser action (US-6 AS-6). */
export function isMailAttachmentOverCap(sizeBytes: number | null | undefined): boolean {
  return sizeBytes !== null && sizeBytes !== undefined && sizeBytes > MAIL_ATTACHMENT_PREVIEW_CAP_BYTES
}

// ── Open (mint + handoff) ────────────────────────────────────────────────────

export interface MailOpenAttachmentArgs {
  workspaceId: string
  agentId: string
  folder: 'inbox' | 'sent' | 'drafts'
  /** The gateway-issued message reference from the list/read result —
   * consumed opaquely, never constructed client-side (register row 16). */
  messageRef: string
  partIndex: number
  filename: string
  subject: string
  /** The folder's display name — the S-19 announcement's `<folder>` slot. */
  folderName: string
  returnFocus: MailReturnFocus
  /** The open panel's presence observer, binding the grant's lifecycle to
   * the panel so panel close revokes it (generated request field). */
  observerId?: string
}

export type MailOpenAttachmentOutcome =
  | { stage: 'opened'; handoff: MailAttachmentHandoff }
  | { stage: 'over-cap' }
  | { stage: 'stale-reference' }
  | { stage: 'busy'; reason: MailUnavailableError['reason'] | null }
  | { stage: 'failed'; errorClass: string }

/** The generated mint request, assembled from the attachment row + open
 * message (nothing synthesized: the ref arrives from the read result). */
export function buildMailAttachmentPreviewRequest(args: MailOpenAttachmentArgs): MailAttachmentPreviewRequest {
  return {
    workspace_id: args.workspaceId,
    agent_id: args.agentId,
    folder: args.folder,
    message_ref: args.messageRef,
    part_index: args.partIndex,
    ...(args.observerId !== undefined ? { observer_id: args.observerId } : {}),
  }
}

/**
 * openMailAttachment — mint the temporary preview and hand the descriptor
 * to the registered W8 mount. Writes nothing to disk: Open only mints
 * (US-6 AS-3). A mount-less environment (W8's wave not landed) surfaces as
 * a visible failure — never a silent no-op.
 */
export async function openMailAttachment(args: MailOpenAttachmentArgs): Promise<MailOpenAttachmentOutcome> {
  let response: MailAttachmentPreviewResponse
  try {
    response = await mintMailAttachmentPreview(buildMailAttachmentPreviewRequest(args))
  } catch (err: unknown) {
    return classifyMintFailure(err)
  }
  const handoff: MailAttachmentHandoff = {
    source: response,
    context: { subject: args.subject, returnFocus: args.returnFocus },
  }
  if (mountHandler === null) {
    // No Library mount registered: revoke the grant immediately — never
    // leave it dangling with no viewer. The TTL remains the backstop if
    // this revoke cannot be delivered.
    void revokeMailAttachmentPreview(response.preview_id).catch(() => undefined)
    return { stage: 'failed', errorClass: 'preview_unavailable' }
  }
  // The return path closes over THIS mint's identity only: the descriptor
  // is freshly minted per Open and dropped on exit (§3.1).
  let disposed = false
  mountHandler(handoff, {
    onBack: () => {
      if (disposed) return
      disposed = true
      disposeMailAttachmentHandoff(handoff, args)
    },
  })
  return { stage: 'opened', handoff }
}

/** Drop the descriptor on exit and revoke the grant (best-effort), then
 * resolve the focus fallback with its S-19 announcement. */
export function disposeMailAttachmentHandoff(handoff: MailAttachmentHandoff, args: MailOpenAttachmentArgs): void {
  void revokeMailAttachmentPreview(handoff.source.preview_id).catch(() => undefined)
  const landing = resolveMailFocusReturn(args.returnFocus, args.filename, args.folderName)
  landing.focus()
  announceMailHandoff(landing.announcement)
}

// ── Mint-failure classification (S-26/S-27/S-28; US-6 AS-8) ─────────────────

/** Read the typed body of an ApiError (e.g. the generated 503
 * MailUnavailableError's `reason`, the 409's `code`) from the raw body
 * text. Returns null on anything unparsable — callers fall back to the
 * generic copy, never to a parsed-by-luck value. */
function typedErrorBody(err: ApiError): { code?: string; reason?: MailUnavailableError['reason'] } | null {
  if (typeof err.body !== 'string' || err.body === '') return null
  try {
    const parsed: unknown = JSON.parse(err.body)
    if (parsed === null || typeof parsed !== 'object') return null
    return parsed as { code?: string; reason?: MailUnavailableError['reason'] }
  } catch {
    return null
  }
}

export function classifyMintFailure(err: unknown): MailOpenAttachmentOutcome {
  if (isApiError(err)) {
    if (err.status === 413) return { stage: 'over-cap' }
    if (err.status === 409) return { stage: 'stale-reference' }
    if (err.status === 503) {
      return { stage: 'busy', reason: typedErrorBody(err)?.reason ?? null }
    }
    return { stage: 'failed', errorClass: err.code ?? err.message }
  }
  return { stage: 'failed', errorClass: err instanceof Error ? err.message : 'unknown_error' }
}

// ── Save controller (M-02 token semantics; FR-W3-22) ────────────────────────

export type MailAttachmentSaveStatus =
  | { stage: 'idle' }
  | { stage: 'loading'; token: string }
  | { stage: 'saved'; response: MailAttachmentSaveResponse }
  | { stage: 'failed'; reason: string }
  | { stage: 'unknown'; token: string }

export interface MailAttachmentSaveArgs {
  workspaceId: string
  agentId: string
  folder: 'inbox' | 'sent' | 'drafts'
  messageRef: string
  partIndex: number
  observerId?: string
}

function newSaveOperationToken(): string {
  const random = typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
    ? crypto.randomUUID()
    : `${Date.now()}-${Math.random().toString(16).slice(2)}`
  return `sv-${random}`
}

export interface MailAttachmentSaveController {
  getStatus(): MailAttachmentSaveStatus
  /** First save attempt: mints a FRESH save_operation_token. */
  save(args: MailAttachmentSaveArgs): Promise<void>
  /** Explicit user retry of an unknown-result save — the SAME token and
   * the SAME target, so the prior receipt resolves it (FR-W3-22). No-op
   * unless a save is in the unknown state; never fires on its own. */
  retry(): Promise<void>
  reset(): void
  subscribe(listener: () => void): () => void
}

/** A transport-level failure (fetch threw → ApiError status 0) means the
 * response was lost after a possible commit → the honest state is
 * "unknown" (S-17). A typed HTTP error is a visible refusal (S-16). */
function isLostResponseError(err: unknown): boolean {
  return err instanceof ApiError && err.status === 0
}

export function createMailAttachmentSaveController(): MailAttachmentSaveController {
  let status: MailAttachmentSaveStatus = { stage: 'idle' }
  /** The args of the latest save — retained so an explicit same-token retry
   * targets the same part/message even if the row re-rendered. */
  let lastArgs: MailAttachmentSaveArgs | null = null
  const listeners = new Set<() => void>()

  function setStatus(next: MailAttachmentSaveStatus): void {
    status = next
    for (const listener of listeners) listener()
  }

  async function run(args: MailAttachmentSaveArgs, token: string): Promise<void> {
    setStatus({ stage: 'loading', token })
    try {
      const response = await saveMailAttachmentToLibrary(
        args.workspaceId,
        args.agentId,
        args.folder,
        args.messageRef,
        args.partIndex,
        {
          save_operation_token: token,
          ...(args.observerId !== undefined ? { observer_id: args.observerId } : {}),
        },
      )
      announceMailHandoff(savedAnnouncement(response))
      setStatus({ stage: 'saved', response })
    } catch (err: unknown) {
      if (isLostResponseError(err)) {
        // Response lost after a possible commit: S-17, SAME token retained
        // for the explicit retry (FR-W3-22). No automatic resend.
        announceMailHandoff('Save result unknown — checking whether it saved.')
        setStatus({ stage: 'unknown', token })
        return
      }
      announceMailHandoff(saveFailedAnnouncement(err))
      setStatus({ stage: 'failed', reason: saveFailureReason(err) })
    }
  }

  return {
    getStatus: () => status,
    save(args: MailAttachmentSaveArgs): Promise<void> {
      lastArgs = args
      return run(args, newSaveOperationToken())
    },
    retry(): Promise<void> {
      if (status.stage !== 'unknown' || lastArgs === null) return Promise.resolve()
      return run(lastArgs, status.token)
    },
    reset(): void {
      status = { stage: 'idle' }
      lastArgs = null
      for (const listener of listeners) listener()
    },
    subscribe(listener: () => void): () => void {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },
  }
}

// ── Announcements (polite; no focus movement — US-7 AS-3) ───────────────────

let liveRegion: HTMLElement | null = null

/** The polite live region: created once, `role="status"`, visually
 * clipped but rendered (never display:none — screen readers skip it). */
function ensureLiveRegion(): HTMLElement | null {
  if (typeof document === 'undefined') return null
  if (liveRegion !== null && document.contains(liveRegion)) return liveRegion
  liveRegion = document.createElement('div')
  liveRegion.setAttribute('role', 'status')
  liveRegion.setAttribute('aria-live', 'polite')
  liveRegion.setAttribute('data-testid', 'mail-handoff-announcer')
  liveRegion.style.position = 'fixed'
  liveRegion.style.width = '1px'
  liveRegion.style.height = '1px'
  liveRegion.style.overflow = 'hidden'
  liveRegion.style.clipPath = 'inset(50%)'
  document.body.appendChild(liveRegion)
  return liveRegion
}

export function announceMailHandoff(text: string): void {
  const region = ensureLiveRegion()
  if (region === null) return
  // Clear first so a repeated identical outcome re-announces.
  region.textContent = ''
  window.setTimeout(() => {
    region.textContent = text
  }, 20)
}

/** S-18's opening announcement. */
export function openingAnnouncement(filename: string): string {
  return `Opening ${filename} from mail.`
}

/** S-15 (the audit-warning variant prepends the safe warning code text). */
export function savedAnnouncement(response: MailAttachmentSaveResponse): string {
  const base = `Saved to Library as ${response.entry.name}.`
  return response.warning_code !== null && response.warning_code !== undefined
    ? `${response.warning_code}: ${base}`
    : base
}

/** S-16's safe-reason line — sanitized classes/typed bodies, never raw
 * server error text. */
export function saveFailedAnnouncement(err: unknown): string {
  return `Could not save to Library. ${saveFailureReason(err)}`
}

export function saveFailureReason(err: unknown): string {
  if (isApiError(err)) {
    if (err.status === 413) return 'This attachment is larger than the 25 MB preview limit. Use Download.'
    if (err.status === 409) return 'This message changed or was deleted. Refresh the list.'
    if (err.status === 503) {
      return busyCopy(typedErrorBody(err)?.reason ?? null)
    }
    return err.userMessage
  }
  return err instanceof Error ? err.message : 'Unknown error.'
}

/** S-9's busy copy by reason (generic `busy` and every unknown reason fall
 * to the generic copy; `backoff` keeps the existing banner's wording). */
export function busyCopy(reason: string | null | undefined): string {
  if (reason === 'server_connection_limit') return 'The mail server reached its connection limit. Try again shortly.'
  return 'Mail is busy. Try again.'
}

// ── Focus return (US-7 AS-2 / MC-W3-8) ─────────────────────────────────────

export interface MailFocusLanding {
  focus(): void
  announcement: string
}

/** Resolve the three-step focus fallback: the originating attachment action
 * if it still exists, else the message's list row, else the folder tab —
 * each with its explicit S-19 announcement. The panel marks the targets
 * with data-mail-attachment-action="<id>" / data-mail-message-row /
 * data-mail-folder-tab so identity survives re-renders. */
export function resolveMailFocusReturn(returnFocus: MailReturnFocus, filename: string, folderName: string): MailFocusLanding {
  if (typeof document !== 'undefined') {
    if (returnFocus.kind === 'attachment-action') {
      const action = document.querySelector<HTMLElement>(
        `[data-mail-attachment-action="${CSS.escape(returnFocus.id)}"]`,
      )
      if (action !== null) {
        return {
          focus: () => action.focus(),
          announcement: `Returned to ${filename} in ${folderName}.`,
        }
      }
    }
    const row = document.querySelector<HTMLElement>('[data-mail-message-row="true"]')
    if (row !== null) {
      return {
        focus: () => row.focus(),
        announcement: `${filename}'s message is no longer in this folder. Focus moved to the message list.`,
      }
    }
    const tab = document.querySelector<HTMLElement>('[data-mail-folder-tab="true"]')
    if (tab !== null) {
      return {
        focus: () => tab.focus(),
        announcement: `${filename}'s message is no longer in this folder. Focus moved to the folder tab.`,
      }
    }
  }
  return {
    focus: () => undefined,
    announcement: `${filename}'s message is no longer in this folder.`,
  }
}
