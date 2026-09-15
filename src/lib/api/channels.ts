// channels.ts: Channel enable/configure/route, channel instances, email mailboxes

import { isApiError as isApiErrorFn } from '../api-error'
import type { ZodType } from 'zod'
import { z } from 'zod'
import {
  ChannelRouting as ChannelRoutingSchema,
  ChannelEntry as ChannelEntrySchema,
  ChannelEnabledResponse as ChannelEnabledResponseSchema,
  ChannelCreateResponse as ChannelCreateResponseSchema,
  OperationResult as OperationResultSchema,
  // M11 per-(agent, workspace) email mailbox account (contract-first #8):
  Mailbox as MailboxSchema,
  MailboxListResponse as MailboxListResponseSchema,
} from '@/lib/api/generated/schemas'
import type {
  ChannelEntry,
  ChannelEnabledResponse,
  OperationResult,
  ChannelRouting,
  // ADR-029 channel-instance CRUD (US-6/US-10/US-11):
  ChannelCreateRequest,
  ChannelCreateResponse,
  // M11 per-(agent, workspace) email mailbox account (contract-first #8):
  Mailbox,
  MailboxConfigureRequest,
} from '@/lib/api/generated/openapi-types'
import { request } from './http'

// Channel — type alias for ChannelEntry (contract-first #8).
// GET /channels returns ChannelEntry[]; this alias preserves backward compat.
// See contracts/components/schemas/ChannelEntry.yaml.
export type Channel = ChannelEntry

export function fetchChannels(): Promise<ChannelEntry[]> {
  return request<ChannelEntry[]>('/channels', undefined, z.array(ChannelEntrySchema) as ZodType<ChannelEntry[]>)
}

export function enableChannel(id: string): Promise<ChannelEnabledResponse> {
  // Backend returns ChannelEnabledResponse {id, enabled} — not a full ChannelEntry.
  return request<ChannelEnabledResponse>(
    `/channels/${encodeURIComponent(id)}/enable`,
    { method: 'PUT' },
    ChannelEnabledResponseSchema as ZodType<ChannelEnabledResponse>,
  )
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

export function configureChannel(id: string, config: Record<string, unknown>): Promise<void> {
  // no-schema: void response; channel-specific body.
  return request<void>(`/channels/${encodeURIComponent(id)}/configure`, {
    method: 'PUT',
    body: JSON.stringify(config),
  })
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

export function setChannelRouting(id: string, body: ChannelRouting): Promise<ChannelRouting> {
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
