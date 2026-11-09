// channels.ts: Channel enable/configure/route, channel instances, email mailboxes

import { ApiError, isApiError as isApiErrorFn, parseServerErrorField } from '../api-error'
import type { ZodType } from 'zod'
import { z } from 'zod'
import {
  ChannelRouting as ChannelRoutingSchema,
  ChannelEntry as ChannelEntrySchema,
  ChannelEnabledResponse as ChannelEnabledResponseSchema,
  ChannelCreateResponse as ChannelCreateResponseSchema,
  OperationResult as OperationResultSchema,
  // Channels spec FR-055 / T-6c: the Configure 200 and 500 bodies, and the
  // availability read the panel shows after a `failed` outcome.
  ChannelConfigureResponse as ChannelConfigureResponseSchema,
  ChannelConfigureError as ChannelConfigureErrorSchema,
  ChannelOutboundRead as ChannelOutboundReadSchema,
  // Channels spec FR-026: the "Starting messages" write and its refusal.
  ChannelOutboundWriteResponse as ChannelOutboundWriteResponseSchema,
  ChannelOutboundRejection as ChannelOutboundRejectionSchema,
  // M11 per-(agent, workspace) email mailbox account (contract-first #8):
  Mailbox as MailboxSchema,
  MailboxListResponse as MailboxListResponseSchema,
} from '@/lib/api/generated/schemas'
import type {
  ChannelEntry,
  ChannelEnableRequest,
  ChannelEnabledResponse,
  OperationResult,
  ChannelRouting,
  ChannelRoutingUpdateRequest,
  // ADR-029 channel-instance CRUD (US-6/US-10/US-11):
  ChannelCreateRequest,
  ChannelCreateResponse,
  ChannelConfigureResponse,
  ChannelOutboundRead,
  ChannelOutboundUpdateRequest,
  ChannelOutboundWriteResponse,
  ChannelOutboundRejection,
  // M11 per-(agent, workspace) email mailbox account (contract-first #8):
  Mailbox,
  MailboxConfigureRequest,
} from '@/lib/api/generated/openapi-types'
import { maybeDevToast } from '../dev-toast'
import { ApiSchemaError, _recordApiSchemaError, request } from './http'

// Channel — type alias for ChannelEntry (contract-first #8).
// GET /channels returns ChannelEntry[]; this alias preserves backward compat.
// See contracts/components/schemas/ChannelEntry.yaml.
export type Channel = ChannelEntry

export function fetchChannels(): Promise<ChannelEntry[]> {
  return request<ChannelEntry[]>('/channels', undefined, z.array(ChannelEntrySchema) as ZodType<ChannelEntry[]>)
}

export async function enableChannel(id: string, body?: ChannelEnableRequest): Promise<ChannelEnabledResponse> {
  // Backend returns ChannelEnabledResponse {id, enabled} — not a full ChannelEntry.
  // Only an explicit pairing_retry body claims chat ownership; ordinary enables
  // remain bodyless and grant no pairing ownership.
  // A 500 ErrorResponse names what was saved (a start that failed, a restart
  // that was not confirmed). The generic 500 sentence must not replace it,
  // the same way Configure keeps its own 500 text (#1568).
  try {
    return await request<ChannelEnabledResponse>(
      `/channels/${encodeURIComponent(id)}/enable`,
      { method: 'PUT', ...(body === undefined ? {} : { body: JSON.stringify(body) }) },
      ChannelEnabledResponseSchema as ZodType<ChannelEnabledResponse>,
    )
  } catch (error) {
    throw withEnableErrorText(error)
  }
}

function withEnableErrorText(error: unknown): unknown {
  if (!isApiErrorFn(error) || error.status !== 500) return error
  const text = parseServerErrorField(error.body)
  if (!text) return error
  return new ApiError(error.status, text, {
    code: error.code,
    body: error.body,
    cause: error,
  })
}

export function disableChannel(id: string): Promise<ChannelEnabledResponse> {
  // Backend returns ChannelEnabledResponse {id, enabled} — not a full ChannelEntry.
  return request<ChannelEnabledResponse>(
    `/channels/${encodeURIComponent(id)}/disable`,
    { method: 'PUT' },
    ChannelEnabledResponseSchema as ZodType<ChannelEnabledResponse>,
  )
}

export function fetchChannelConfig(id: string): Promise<Record<string, unknown>> {
  // no-schema: channel config structure varies per channel type; no generated schema component.
  return request<Record<string, unknown>>(`/channels/${encodeURIComponent(id)}`)
}

// configureChannel saves a channel's settings (channels spec FR-055, T-6c).
// It resolves only with a ChannelConfigureResponse that passed its generated
// schema, so the caller always has an apply_outcome; an empty or
// schema-invalid 200 rejects with ApiSchemaError. A 500 whose body is a
// ChannelConfigureError rejects with an ApiError carrying the server's text
// (userMessage) and code: these texts say what the save durably changed
// (credential stored, file written but not refreshed), so the generic 500
// wording must not replace them.
export async function configureChannel(id: string, config: Record<string, unknown>): Promise<ChannelConfigureResponse> {
  const path = `/channels/${encodeURIComponent(id)}/configure`
  let response: ChannelConfigureResponse | undefined
  try {
    response = await request<ChannelConfigureResponse>(
      path,
      { method: 'PUT', body: JSON.stringify(config) },
      ChannelConfigureResponseSchema as ZodType<ChannelConfigureResponse>,
    )
  } catch (error) {
    throw withConfigureErrorText(error)
  }
  // request() resolves an empty 2xx (204, Content-Length: 0) as undefined
  // without running the schema; a Configure save has no outcome-less success.
  if (response === undefined) {
    const endpoint = `PUT /api/v1${path}`
    _recordApiSchemaError(endpoint, 1)
    throw new ApiSchemaError(endpoint, [{ path: ['apply_outcome'], message: 'Required' }], undefined)
  }
  return response
}

function withConfigureErrorText(error: unknown): unknown {
  if (!isApiErrorFn(error) || error.status !== 500 || !error.body) return error
  let raw: unknown
  try {
    raw = JSON.parse(error.body)
  } catch {
    // Not a ChannelConfigureError body: keep the generic 500 error.
    return error
  }
  const parsed = ChannelConfigureErrorSchema.safeParse(raw)
  if (!parsed.success) return error
  return new ApiError(error.status, parsed.data.error, {
    code: parsed.data.code,
    body: error.body,
    cause: error,
  })
}

// fetchChannelOutbound reads GET /channels/{id}/outbound (channels spec
// FR-021). The Configure panel uses its `availability` after a `failed`
// save outcome (T-6c).
export function fetchChannelOutbound(id: string): Promise<ChannelOutboundRead> {
  return request<ChannelOutboundRead>(
    `/channels/${encodeURIComponent(id)}/outbound`,
    undefined,
    ChannelOutboundReadSchema as ZodType<ChannelOutboundRead>,
  )
}

// setChannelOutbound saves a connection's "Starting messages" setting through
// its dedicated route, PUT /channels/{id}/outbound (channels spec FR-021,
// FR-026). A refusal rejects with the real ApiError from request(); read its
// ChannelOutboundRejection with readOutboundRejection.
export function setChannelOutbound(id: string, body: ChannelOutboundUpdateRequest): Promise<ChannelOutboundWriteResponse> {
  return request<ChannelOutboundWriteResponse>(
    `/channels/${encodeURIComponent(id)}/outbound`,
    { method: 'PUT', body: JSON.stringify(body) },
    ChannelOutboundWriteResponseSchema as ZodType<ChannelOutboundWriteResponse>,
  )
}

// The statuses whose body the contract defines as ChannelOutboundRejection
// (400, 409, 422, 502). 500 and 503 carry the ordinary ErrorResponse.
const OUTBOUND_REJECTION_STATUSES = new Set([400, 409, 422, 502])

export type OutboundRejectionRead = // not-wire-format: client-side decode result; the wire body is the generated ChannelOutboundRejection
  | { kind: 'rejection'; rejection: ChannelOutboundRejection }
  // The body was a rejection status but failed the generated schema (for
  // example a string outside the safe-echo set, S-83 case 4). It is recorded
  // as a contract violation and must not be shown.
  | { kind: 'invalid' }
  // Not a rejection: another status, a transport error, or a body the error
  // decoder discarded. The caller shows the error's own userMessage.
  | { kind: 'none' }

// readOutboundRejection decodes the rejection carried by a failed
// setChannelOutbound call, validated by its generated runtime schema.
export function readOutboundRejection(error: unknown, id: string): OutboundRejectionRead {
  if (!isApiErrorFn(error) || !OUTBOUND_REJECTION_STATUSES.has(error.status) || !error.body) return { kind: 'none' }
  let raw: unknown
  try {
    raw = JSON.parse(error.body)
  } catch {
    // Not JSON at all: nothing to validate, the generic message applies.
    return { kind: 'none' }
  }
  const parsed = ChannelOutboundRejectionSchema.safeParse(raw)
  if (parsed.success) return { kind: 'rejection', rejection: parsed.data as ChannelOutboundRejection }
  const path = `/channels/${encodeURIComponent(id)}/outbound`
  _recordApiSchemaError(`PUT /api/v1${path}`, parsed.error.issues.length)
  void maybeDevToast(
    `[api] Schema mismatch: ${path} — ${parsed.error.issues[0]?.message ?? 'unknown'}`,
    `PUT:${path}:rejection-schema`,
  )
  return { kind: 'invalid' }
}

// ChannelRouting — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/ChannelRouting.yaml.

export function fetchChannelRouting(id: string): Promise<ChannelRouting> {
  return request<ChannelRouting>(
    `/channels/${encodeURIComponent(id)}/routing`,
    undefined,
    ChannelRoutingSchema as ZodType<ChannelRouting>,
  )
}

// PUT body is ChannelRoutingUpdateRequest — {workspace_id, default_agent_id},
// both required and nonblank (omnipus-system-agent K14): there is no unbound
// mode and no global default agent to fall back to. The read-back shape
// (ChannelRouting) additionally carries `bound`; an unbound instance reports
// bound:false with empty ids and drops inbound messages until bound (FR-128).
export function setChannelRouting(id: string, body: ChannelRoutingUpdateRequest): Promise<ChannelRouting> {
  return request<ChannelRouting>(
    `/channels/${encodeURIComponent(id)}/routing`,
    { method: 'PUT', body: JSON.stringify(body) },
    ChannelRoutingSchema as ZodType<ChannelRouting>,
  )
}

// ── Channel-instance CRUD (ADR-029 US-6 / US-10 / US-11) ─────────────────────
//
// createChannelInstance  — POST /channels with {type, slug}; backend derives the
//   instance key as "<type>.<slug>" (FR-017). Returns 201 ChannelCreateResponse
//   on success; 400 for unknown type or malformed slug; 409 if the key already
//   exists. Slug validation against [a-z0-9-]{1,32} is enforced client-side too
//   (the dialog blocks submit) but the backend is the authoritative validator.
//
// deleteChannelInstance — DELETE /channels/{id}; returns 204 on success; 404 for
//   unknown instance; 400 for malformed id. Removes config + credential refs +
//   per-instance state directory (e.g. WhatsApp store.db). "webchat" is a
//   built-in and cannot be deleted (backend returns 400).

export function createChannelInstance(body: ChannelCreateRequest): Promise<ChannelCreateResponse> {
  return request<ChannelCreateResponse>(
    '/channels',
    { method: 'POST', body: JSON.stringify(body) },
    ChannelCreateResponseSchema as ZodType<ChannelCreateResponse>,
  )
}

export function deleteChannelInstance(id: string): Promise<void> {
  return request<void>(`/channels/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

// ── Email Mailbox Account ─────────────────────────────────────────────────────
//
// Email is a TOOL (not a channel) with a per-(agent, workspace) mailbox
// account: a mailbox belongs to exactly one (agent, workspace) pair — the
// same agent can hold a different mailbox in each workspace it belongs to
// (different roles, different inboxes). Configured via the pair endpoints
// (GET/PUT/DELETE /api/v1/agents/{id}/mailboxes/{workspaceId}); wire types
// Mailbox + MailboxConfigureRequest are generated from contracts/openapi.yaml
// (#8). Both ids ride in the path — MailboxConfigureRequest carries no
// workspace_id member.

export const EMAIL_CHANNEL_ID = 'email'

/**
 * Fetch the mailbox an agent holds in a given workspace (M11 — email is a
 * TOOL surface, not a conversational channel). Routes through
 * GET /api/v1/agents/{id}/mailboxes/{workspaceId}. Returns null when that
 * pair has no mailbox configured (404) — every other error is rethrown. The
 * password is never returned; `configured` reports whether a stored
 * credential resolves.
 *
 * (The legacy GET /channels/email path is dead: the ADR-029 instance-key
 * grammar gate rejects "email" because it is deliberately not a channel type.)
 */
export async function fetchAgentMailbox(agentId: string, workspaceId: string): Promise<Mailbox | null> {
  try {
    return await request<Mailbox>(
      `/agents/${encodeURIComponent(agentId)}/mailboxes/${encodeURIComponent(workspaceId)}`,
      undefined,
      MailboxSchema,
    )
  } catch (err) {
    if (isApiErrorFn(err) && err.status === 404) return null
    throw err
  }
}

/**
 * List every configured mailbox via GET /api/v1/mailboxes (one per configured
 * (agent, workspace) pair). Never 404s — an empty list means none configured.
 * Preferred over per-pair probing: each probe 404 lands in the browser
 * console as an error and trips the e2e zero-console-errors gate.
 */
export async function fetchMailboxes(): Promise<Mailbox[]> {
  const res = await request<{ mailboxes: Mailbox[] }>(
    '/mailboxes',
    undefined,
    MailboxListResponseSchema as ZodType<{ mailboxes: Mailbox[] }>,
  )
  return res.mailboxes
}

/**
 * The first configured mailbox, or null. Convenience for callers that only
 * need "is any mailbox configured" — with multiple mailboxes, use
 * fetchMailboxes and address them individually.
 */
export async function findConfiguredMailbox(): Promise<Mailbox | null> {
  const mailboxes = await fetchMailboxes()
  return mailboxes[0] ?? null
}

/**
 * Configure the mailbox an agent holds in a given workspace via
 * PUT /api/v1/agents/{id}/mailboxes/{workspaceId}. The backend
 * credential-routes the password (never persisted in plaintext); omit
 * `password` to keep the stored credential. `req` carries no workspace_id —
 * both ids ride in the path.
 */
export function saveAgentMailbox(
  agentId: string,
  workspaceId: string,
  req: MailboxConfigureRequest,
): Promise<Mailbox> {
  return request<Mailbox>(
    `/agents/${encodeURIComponent(agentId)}/mailboxes/${encodeURIComponent(workspaceId)}`,
    { method: 'PUT', body: JSON.stringify(req) },
    MailboxSchema,
  )
}

/**
 * Delete the mailbox an agent holds in a given workspace
 * (DELETE /api/v1/agents/{id}/mailboxes/{workspaceId}). Mailboxes the agent
 * holds in other workspaces are untouched.
 */
export function deleteAgentMailbox(agentId: string, workspaceId: string): Promise<OperationResult> {
  return request<OperationResult>(
    `/agents/${encodeURIComponent(agentId)}/mailboxes/${encodeURIComponent(workspaceId)}`,
    { method: 'DELETE' },
    OperationResultSchema,
  )
}
