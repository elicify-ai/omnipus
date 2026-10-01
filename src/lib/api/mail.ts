// mail.ts — the workspace Mail read/write surface (email-mail-view-spec.md
// §2.3, contract-first #8). Every response validates through its generated
// schema; wire types come only from src/lib/api/generated/. Routes are the
// gateway's mail handlers (pkg/gateway/rest_mail.go::handleWorkspaceMail):
//
//   GET  /workspaces/{ws}/mail/summary
//   GET  /workspaces/{ws}/mail/{agent}/folders
//   GET  /workspaces/{ws}/mail/{agent}/folders/{folder}/messages
//   GET  /workspaces/{ws}/mail/{agent}/folders/{folder}/messages/{ref}
//   POST .../messages/{ref}/seen
//   GET  .../messages/{ref}/attachments/{partIndex}
//   POST /workspaces/{ws}/mail/{agent}/messages              (manual send)
//   PUT|DELETE /workspaces/{ws}/mail/{agent}/folders/drafts/messages/{ref}
//   POST .../folders/drafts/messages/{ref}/send
//   POST /api/v1/mail/html-preview-token                     (D13/D17 mint)
//
// mid: refs carry < > characters and are URL-encoded here with
// encodeURIComponent (the gateway PathUnescape's each segment).

import { ApiError } from '../api-error'
import { BASE_URL, buildHeaders, request } from './http'
import type { ZodType } from 'zod'
import {
  MailFolderList as MailFolderListSchema,
  MailMessagePage as MailMessagePageSchema,
  MailMessage as MailMessageSchema,
  MailSummaryList as MailSummaryListSchema,
  MailSendResponse as MailSendResponseSchema,
  MailHtmlPreviewTokenResponse as MailHtmlPreviewTokenResponseSchema,
  MailSignaturePreviewTokenResponse as MailSignaturePreviewTokenResponseSchema,
} from './generated/schemas'
import type {
  operations,
  MailFolderList,
  MailMessagePage,
  MailMessage,
  MailSummaryList,
  MailSendRequest,
  MailSendResponse,
  MailDraftUpdateRequest,
  MailDraftSendRequest,
  MailHtmlPreviewTokenRequest,
  MailHtmlPreviewTokenResponse,
  MailSignaturePreviewTokenRequest,
  MailSignaturePreviewTokenResponse,
} from './generated/openapi-types'

/** Percent-encode one path segment (mid: refs contain < > @ characters). */
function seg(raw: string): string {
  return encodeURIComponent(raw)
}

export type MailMessagePageParams = NonNullable<
  operations['listMailMessages']['parameters']['query']
>

/** Build the server's folder-scoped UID message reference. */
export function mailUidRef(uidvalidity: number, uid: number): string {
  return `uid:${uidvalidity}:${uid}`
}

/** Query-string suffix for the contract's `retry` marker (absent when false). */
function retryQs(opts: { retry?: boolean }): string {
  return opts.retry === true ? '?retry=true' : ''
}

/** GET .../mail/{agent}/folders — the folder rail (US-3). */
export async function fetchMailFolders(
  workspaceId: string,
  agentId: string,
  opts: { retry?: boolean } = {},
): Promise<MailFolderList> {
  return request<MailFolderList>(
    `/workspaces/${seg(workspaceId)}/mail/${seg(agentId)}/folders${retryQs(opts)}`,
    undefined,
    MailFolderListSchema as ZodType<MailFolderList>,
  )
}

/**
 * GET .../mail/{agent}/folders/{folder}/messages — the list page (US-3).
 * `folder` is the slug (inbox | sent | drafts); custom per-mailbox folder
 * names are resolved server-side.
 */
export async function fetchMailMessages(
  workspaceId: string,
  agentId: string,
  folder: string,
  params: MailMessagePageParams = {},
): Promise<MailMessagePage> {
  const qs = new URLSearchParams()
  if (params.limit !== undefined) qs.set('limit', String(params.limit))
  if (params.before_uid !== undefined) qs.set('before_uid', String(params.before_uid))
  if (params.retry === true) qs.set('retry', 'true')
  const suffix = qs.size > 0 ? `?${qs.toString()}` : ''
  return request<MailMessagePage>(
    `/workspaces/${seg(workspaceId)}/mail/${seg(agentId)}/folders/${seg(folder)}/messages${suffix}`,
    undefined,
    MailMessagePageSchema as ZodType<MailMessagePage>,
  )
}

/**
 * GET .../mail/{agent}/folders/{folder}/messages/{ref} — one message (US-3,
 * US-4). `ref` is `uid:<uidvalidity>:<uid>` or `mid:<Message-ID>`.
 */
export async function fetchMailMessage(
  workspaceId: string,
  agentId: string,
  folder: string,
  ref: string,
  opts: { retry?: boolean } = {},
): Promise<MailMessage> {
  return request<MailMessage>(
    `/workspaces/${seg(workspaceId)}/mail/${seg(agentId)}/folders/${seg(folder)}/messages/${seg(ref)}${retryQs(opts)}`,
    undefined,
    MailMessageSchema as ZodType<MailMessage>,
  )
}

/** POST .../messages/{ref}/seen — mark read (US-6, FR-020); 204 idempotent. */
export async function markMailSeen(
  workspaceId: string,
  agentId: string,
  folder: string,
  ref: string,
): Promise<void> {
  await request<void>(
    `/workspaces/${seg(workspaceId)}/mail/${seg(agentId)}/folders/${seg(folder)}/messages/${seg(ref)}/seen`,
    { method: 'POST' },
  )
}

/**
 * GET .../messages/{ref}/attachments/{partIndex} — raw bytes for one
 * attachment (US-3, D28). Binary transport, so this bypasses the JSON
 * request() helper the same way the Library's file download does; errors
 * still surface as ApiError.
 */
export async function fetchMailAttachment(
  workspaceId: string,
  agentId: string,
  folder: string,
  ref: string,
  partIndex: number,
  opts: { retry?: boolean } = {},
): Promise<Blob> {
  let res: Response
  try {
    res = await fetch(
      `${BASE_URL}/api/v1/workspaces/${seg(workspaceId)}/mail/${seg(agentId)}/folders/${seg(folder)}/messages/${seg(ref)}/attachments/${partIndex}${retryQs(opts)}`,
      { credentials: 'include', headers: buildHeaders() },
    )
  } catch (cause) {
    throw new ApiError(0, 'Network unavailable. Check your connection.', { cause })
  }
  if (!res.ok) throw await ApiError.fromResponse(res)
  return await res.blob()
}

/** GET /workspaces/{ws}/mail/summary — per-mailbox watcher state (D29/R2-8). */
export async function fetchMailSummary(workspaceId: string): Promise<MailSummaryList> {
  return request<MailSummaryList>(
    `/workspaces/${seg(workspaceId)}/mail/summary`,
    undefined,
    MailSummaryListSchema as ZodType<MailSummaryList>,
  )
}

/** POST .../mail/{agent}/messages — manual send from the compose dialog (US-5, D12-adjacent). */
export async function sendMailMessage(
  workspaceId: string,
  agentId: string,
  body: MailSendRequest,
): Promise<MailSendResponse> {
  return request<MailSendResponse>(
    `/workspaces/${seg(workspaceId)}/mail/${seg(agentId)}/messages`,
    { method: 'POST', body: JSON.stringify(body) },
    MailSendResponseSchema as ZodType<MailSendResponse>,
  )
}

/**
 * PUT .../folders/drafts/messages/{ref} — save an edited draft (US-7 AS-1).
 * The request carries the draft's current uidvalidity/uid as the optimistic
 * precondition; a 409 means the draft moved underneath us (stale_draft).
 */
export async function saveMailDraft(
  workspaceId: string,
  agentId: string,
  ref: string,
  body: MailDraftUpdateRequest,
): Promise<MailMessage> {
  return request<MailMessage>(
    `/workspaces/${seg(workspaceId)}/mail/${seg(agentId)}/folders/drafts/messages/${seg(ref)}`,
    { method: 'PUT', body: JSON.stringify(body) },
    MailMessageSchema as ZodType<MailMessage>,
  )
}

/** POST .../folders/drafts/messages/{ref}/send — approve + send the draft (US-7, D12). */
export async function sendMailDraft(
  workspaceId: string,
  agentId: string,
  ref: string,
  body: {
    to: MailDraftSendRequest['to']
    cc?: MailDraftSendRequest['cc']
    bcc?: MailDraftSendRequest['bcc']
    subject: string
    body_markdown: string
    uidvalidity: number
    uid: number
    keep_attachment_parts?: number[]
  },
): Promise<MailSendResponse> {
  return request<MailSendResponse>(
    `/workspaces/${seg(workspaceId)}/mail/${seg(agentId)}/folders/drafts/messages/${seg(ref)}/send`,
    { method: 'POST', body: JSON.stringify(body) },
    MailSendResponseSchema as ZodType<MailSendResponse>,
  )
}

/** DELETE .../folders/drafts/messages/{ref} — discard the draft (US-7). */
export async function discardMailDraft(
  workspaceId: string,
  agentId: string,
  ref: string,
): Promise<void> {
  await request<void>(
    `/workspaces/${seg(workspaceId)}/mail/${seg(agentId)}/folders/drafts/messages/${seg(ref)}`,
    { method: 'DELETE' },
  )
}

/**
 * POST /api/v1/mail/html-preview-token — mint the sandboxed-frame token for
 * one message's HTML body (D13/D17). The frame's src is
 * `/mail-preview/html/{token}`; `load_remote: true` is the "Load images"
 * re-mint (D17).
 */
export async function mintMailHtmlPreviewToken(
  body: MailHtmlPreviewTokenRequest,
): Promise<MailHtmlPreviewTokenResponse> {
  const res = await request<MailHtmlPreviewTokenResponse>(
    '/mail/html-preview-token',
    { method: 'POST', body: JSON.stringify(body) },
    MailHtmlPreviewTokenResponseSchema as ZodType<MailHtmlPreviewTokenResponse>,
  )
  return res
}

/**
 * POST /api/v1/mail/signature-preview-token — mint the sandboxed-frame token
 * for the mailbox signature editor's live preview (MC-10 §5.3; architect
 * decision 2026-09-28, option B). Unlike the message mint this NEVER dials
 * IMAP and NEVER persists: the signature HTML is sanitized at mint and held
 * in memory for the 2-minute MailSignaturePreviewTokenTTL. The frame's src
 * is the same-origin `/mail-preview/html/{token}`. Rate-limited per IP
 * (MC-44): 429 carries Retry-After.
 */
export async function mintMailSignaturePreviewToken(
  body: MailSignaturePreviewTokenRequest,
): Promise<MailSignaturePreviewTokenResponse> {
  const res = await request<MailSignaturePreviewTokenResponse>(
    '/mail/signature-preview-token',
    { method: 'POST', body: JSON.stringify(body) },
    MailSignaturePreviewTokenResponseSchema as ZodType<MailSignaturePreviewTokenResponse>,
  )
  return res
}
