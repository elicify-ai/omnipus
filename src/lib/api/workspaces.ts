// workspaces.ts: Workspaces, their media library, delegation edges and instructions

import type { ZodType } from 'zod'
import { z } from 'zod'
import {
  // Level-1 workspaces + unified tasks + token stats (contract-first #8):
  Workspace as WorkspaceSchema,
  // M5 per-workspace delegation graph (contract-first #8):
  WorkspaceDelegation as WorkspaceDelegationSchema,
  // Workspace / Project Instructions (contract-first #8):
  WorkspaceInstructionsResponse as WorkspaceInstructionsResponseSchema,
  // ADR-051 Rev 4 — workspace media library (contract-first #8):
  MediaLibraryEntry as MediaLibraryEntrySchema,
} from '@/lib/api/generated/schemas'
import type {
  // Level-1 workspaces + unified tasks + token stats (contract-first #8):
  Workspace,
  WorkspaceCreateRequest,
  WorkspaceUpdateRequest,
  // M5 per-workspace delegation graph (contract-first #8):
  WorkspaceDelegation,
  WorkspaceDelegationEdge,
  WorkspaceDelegationUpdateRequest,
  // Workspace / Project Instructions (contract-first #8):
  WorkspaceInstructionsResponse,
  WorkspaceInstructionsRequest,
  // ADR-051 Rev 4 — workspace media library (contract-first #8):
  MediaLibraryEntry,
  MediaAttachmentRequest,
} from '@/lib/api/generated/openapi-types'
import { request } from './http'

// ── Workspaces ────────────────────────────────────────────────────────────────
//
// Workspaces are lightweight metadata records (no filesystem dirs). All types are
// re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/Workspace*.yaml.

export const workspacesQueryKeys = {
  list: (params?: { status?: string }) => ['workspaces', params] as const,
  detail: (id: string) => ['workspaces', id] as const,
  delegation: (id: string) => ['workspaces', id, 'delegation'] as const,
  instructions: (id: string) => ['workspaces', id, 'instructions'] as const,
  // ADR-051 Rev 4 — workspace media library (Slice H):
  media: (workspaceId: string) => ['workspaces', workspaceId, 'media'] as const,
}

export function fetchWorkspaces(params?: { status?: string }): Promise<Workspace[]> {
  const qs = params?.status ? '?' + new URLSearchParams({ status: params.status }).toString() : ''
  return request<Workspace[]>(`/workspaces${qs}`, undefined, z.array(WorkspaceSchema) as ZodType<Workspace[]>)
}

/**
 * Fetch a single workspace by id.
 * Used by the AgentProfile Heartbeat tab (FR-016 / US-5) to read the current
 * member_configs for the (workspace, agent) heartbeat pair.
 */
export function fetchWorkspace(id: string): Promise<Workspace> {
  return request<Workspace>(
    `/workspaces/${encodeURIComponent(id)}`,
    undefined,
    WorkspaceSchema as ZodType<Workspace>,
  )
}

export function createWorkspace(body: WorkspaceCreateRequest): Promise<Workspace> {
  return request<Workspace>(
    '/workspaces',
    { method: 'POST', body: JSON.stringify(body) },
    WorkspaceSchema as ZodType<Workspace>,
  )
}

export function updateWorkspace(id: string, body: WorkspaceUpdateRequest): Promise<Workspace> {
  return request<Workspace>(
    `/workspaces/${encodeURIComponent(id)}`,
    { method: 'PUT', body: JSON.stringify(body) },
    WorkspaceSchema as ZodType<Workspace>,
  )
}

export function deleteWorkspace(id: string): Promise<void> {
  return request<void>(`/workspaces/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

// ── ADR-051 Rev 4 — Workspace Media Library (Slice H) ─────────────────────────
//
// The workspace media library is the blob store behind chat uploads
// (`workspaces/<ws>/media/`, UUID-keyed with a manifest). NOTE: the standalone
// workspace "Media" tab that used to surface it was REMOVED when the Library
// replaced it — the Library is a file explorer over the workspace `work/` tree
// (see docs/internal/specs/library-spec.md), a different store, so do not
// reintroduce a UI that lists this manifest as if it were the Library. These
// endpoints remain live because the composer picker still attaches an existing
// library entry to a chat message by
// its `media://workspace/<workspace_id>/<media_id>` ref (FR-022) without
// re-uploading. Wire types are the generated MediaLibraryEntry /
// MediaAttachmentRequest (contract-first #8) — never hand-written.

/**
 * List a workspace's media-library entries (GET /workspaces/{id}/media).
 * Returns the full manifest; raw bytes are fetched on demand via /media/{ref}.
 */
export function fetchWorkspaceMedia(workspaceId: string): Promise<MediaLibraryEntry[]> {
  return request<MediaLibraryEntry[]>(
    `/workspaces/${encodeURIComponent(workspaceId)}/media`,
    undefined,
    z.array(MediaLibraryEntrySchema) as ZodType<MediaLibraryEntry[]>,
  )
}

/**
 * Explicitly delete one workspace media-library entry (FR-008). Removes the
 * raw bytes + manifest entry; the server emits a media.delete audit event
 * (FR-033). Returns 204 No Content.
 */
export function deleteWorkspaceMedia(workspaceId: string, mediaId: string): Promise<void> {
  return request<void>(
    `/workspaces/${encodeURIComponent(workspaceId)}/media/${encodeURIComponent(mediaId)}`,
    { method: 'DELETE' },
  )
}

/**
 * Register a workspace library entry as a chat attachment (FR-022,
 * POST /workspaces/{id}/media/attachments) without re-uploading the file.
 * Returns 204 No Content; the SPA threads the `media://workspace/<ws>/<id>`
 * ref into the outgoing message frame via the library-attachment store.
 */
export function attachWorkspaceMedia(workspaceId: string, mediaId: string): Promise<void> {
  const body: MediaAttachmentRequest = { media_id: mediaId }
  return request<void>(
    `/workspaces/${encodeURIComponent(workspaceId)}/media/attachments`,
    { method: 'POST', body: JSON.stringify(body) },
  )
}

// ── Per-workspace delegation graph (M5) ─────────────────────────────────────────
//
// The delegation graph is the workspace's source of truth for who-delegates-to-
// whom. The Team tab edits it as a node-and-edge graph and persists the WHOLE
// edge set on each change (full replace, not merge — see WorkspaceDelegation
// UpdateRequest). `team[]` on the read response is computed server-side (union of
// core_team + every agent named by an edge) so the editor can render isolated
// member nodes that have no edges yet.

export function fetchWorkspaceDelegation(id: string): Promise<WorkspaceDelegation> {
  return request<WorkspaceDelegation>(
    `/workspaces/${encodeURIComponent(id)}/delegation`,
    undefined,
    WorkspaceDelegationSchema as ZodType<WorkspaceDelegation>,
  )
}

export function updateWorkspaceDelegation(
  id: string,
  edges: WorkspaceDelegationEdge[],
): Promise<WorkspaceDelegation> {
  const body: WorkspaceDelegationUpdateRequest = { edges }
  return request<WorkspaceDelegation>(
    `/workspaces/${encodeURIComponent(id)}/delegation`,
    { method: 'PUT', body: JSON.stringify(body) },
    WorkspaceDelegationSchema as ZodType<WorkspaceDelegation>,
  )
}

// ── Workspace / Project Instructions ─────────────────────────────────────────
//
// Per-workspace AGENT.md content — applied to every agent working in the
// workspace, on top of their persona. Contract-first per Constraint #8.
// See contracts/components/schemas/WorkspaceInstructions*.yaml.

export function fetchWorkspaceInstructions(workspaceId: string): Promise<WorkspaceInstructionsResponse> {
  return request<WorkspaceInstructionsResponse>(
    `/workspaces/${encodeURIComponent(workspaceId)}/instructions`,
    undefined,
    WorkspaceInstructionsResponseSchema as ZodType<WorkspaceInstructionsResponse>,
  )
}

export function updateWorkspaceInstructions(
  workspaceId: string,
  content: string,
): Promise<WorkspaceInstructionsResponse> {
  const body: WorkspaceInstructionsRequest = { content }
  return request<WorkspaceInstructionsResponse>(
    `/workspaces/${encodeURIComponent(workspaceId)}/instructions`,
    { method: 'PUT', body: JSON.stringify(body) },
    WorkspaceInstructionsResponseSchema as ZodType<WorkspaceInstructionsResponse>,
  )
}
