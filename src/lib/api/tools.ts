// tools.ts: Tool registry, per-agent tools, global policies, MCP servers

import type { ZodType } from 'zod'
import { z } from 'zod'
import {
  ToolRegistryEntry as ToolRegistryEntrySchema,
  ToolApprovalResponse as ToolApprovalResponseSchema,
  McpServer as McpServerSchema,
  McpServerTestResponse as McpServerTestResponseSchema,
  AgentToolsResponse as AgentToolsResponseSchema,
  McpServerToolsResponse as McpServerToolsResponseSchema,
} from '@/lib/api/generated/schemas'
import type {
  ToolRegistryEntry,
  AgentToolsUpdateRequest,
  McpServer,
  McpServerCreate,
  McpServerUpdate,
  McpServerTestResponse,
  AgentToolsResponse,
  McpServerToolsResponse,
  // Tool-approval "always" grant action (commit 35447760, contract-first #8):
  ToolApprovalActionRequest,
  ToolApprovalResponse,
} from '@/lib/api/generated/openapi-types'
import { REAUTH_HEADER } from './auth'
import { request } from './http'
import { requestConfiguration } from './configuration'

// ── Tools & Channels ──────────────────────────────────────────────────────────

// Tool — type alias for ToolRegistryEntry (contract-first #8).
// GET /tools returns ToolRegistryEntry[]; this alias preserves backward compat.
// See contracts/components/schemas/ToolRegistryEntry.yaml.
export type Tool = ToolRegistryEntry

// fetchTools is a backward-compat alias for fetchRegistryTools.
// New callers should use fetchRegistryTools (or fetchBuiltinTools) directly.
export function fetchTools(): Promise<ToolRegistryEntry[]> { return fetchRegistryTools() }

// fetchMcpServers is a backward-compat alias for fetchMcpServersForAgent.
// New callers should use fetchMcpServersForAgent directly.
export function fetchMcpServers(): Promise<McpServer[]> { return fetchMcpServersForAgent() }

export function addMcpServer(data: McpServerCreate): Promise<McpServer> {
  return request<McpServer>('/mcp-servers', { method: 'POST', body: JSON.stringify(data) }, McpServerSchema as ZodType<McpServer>)
}

export function deleteMcpServer(id: string): Promise<void> {
  // no-schema: void response; DELETE has no body.
  return request<void>(`/mcp-servers/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export async function fetchMcpServerTools(id: string): Promise<string[]> {
  const resp = await request<McpServerToolsResponse>(`/mcp-servers/${encodeURIComponent(id)}/tools`, undefined, McpServerToolsResponseSchema as ZodType<McpServerToolsResponse>)
  return resp.tools
}

export function updateMcpServer(id: string, body: McpServerUpdate): Promise<McpServer> {
  return request<McpServer>(`/mcp-servers/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(body) }, McpServerSchema as ZodType<McpServer>)
}

export function testMcpServer(id: string): Promise<McpServerTestResponse> {
  return request<McpServerTestResponse>(`/mcp-servers/${encodeURIComponent(id)}/test`, { method: 'POST' }, McpServerTestResponseSchema as ZodType<McpServerTestResponse>)
}

// ── Agent Tools ───────────────────────────────────────────────────────────────

// RegistryTool — type alias for ToolRegistryEntry (contract-first #8).
// Central registry tool entry (FR-027, FR-029). Includes a source discriminator
// so the UI can badge MCP tools differently from builtin ones.
// See contracts/components/schemas/ToolRegistryEntry.yaml.
export type RegistryTool = ToolRegistryEntry

/** Backward-compat alias — existing callers that reference BuiltinTool still work. */
export type BuiltinTool = RegistryTool

// AgentToolsCfg — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/AgentToolsCfg.yaml.

// AgentToolEntry — re-exported from generated openapi-types (no local body needed).

/** Fetch all tools from the central registry (FR-027). Includes both builtin and MCP tools. */
export function fetchRegistryTools(): Promise<RegistryTool[]> {
  return request<RegistryTool[]>('/tools', undefined, z.array(ToolRegistryEntrySchema) as ZodType<RegistryTool[]>)
}

/** Backward-compat alias — callers that used fetchBuiltinTools() still work. */
export const fetchBuiltinTools = fetchRegistryTools

export function fetchMcpServersForAgent(): Promise<McpServer[]> {
  return request<McpServer[]>('/mcp-servers', undefined, z.array(McpServerSchema) as ZodType<McpServer[]>)
}

// AgentToolsResponse — imported from generated openapi-types (contract-first #8).
// AgentToolsResponseSchema — imported from generated schemas (contract-first #8).
export function fetchAgentTools(agentId: string): Promise<AgentToolsResponse> {
  return request<AgentToolsResponse>(`/agents/${encodeURIComponent(agentId)}/tools`, undefined, AgentToolsResponseSchema as ZodType<AgentToolsResponse>)
}

// updateAgentTools persists per-agent tool policies. It is re-auth gated
// server-side (requireReAuth): pass a consent token from useReAuthGate/runGated
// via reAuthToken to replay it in the X-Reauth-Token header. The first call
// may pass '' (no token); if the server demands re-auth, runGated opens the
// dialog and retries with the minted token. // not-wire-format
export function updateAgentTools(
  agentId: string,
  cfg: AgentToolsUpdateRequest,
  reAuthToken?: string,
): Promise<AgentToolsResponse> {
  return requestConfiguration<AgentToolsResponse>(`/agents/${encodeURIComponent(agentId)}/tools`, {
    method: 'PUT',
    headers: reAuthToken ? { [REAUTH_HEADER]: reAuthToken } : undefined,
    body: JSON.stringify(cfg),
  }, AgentToolsResponseSchema as ZodType<AgentToolsResponse>)
}

/**
 * POST /api/v1/tool-approvals/{approvalId} — resolve a pending tool approval.
 * FR-011, FR-082. Throws ApiError on non-2xx (e.g. 403, 404, 409).
 *
 * action is the generated ToolApprovalActionRequest['action'] union. Skills &
 * Tools C-26 replaced the retired lasting "allow" with "allow_for", which
 * needs a duration (1h / 3h / 1d); the server records a timed allowance for
 * the approval's agent and reports what it stored in `applied` and
 * `expires_at` (C-24).
 *
 * scope (ADR-092 D4/FR-024) — "exact" | "prefix" — narrows a bash card's
 * timed allowance. Only the members the caller supplies are sent, so the
 * wire body never carries an empty `duration` or `scope` key.
 */
export function submitToolApproval(
  approvalId: string,
  action: ToolApprovalActionRequest['action'],
  options?: Pick<ToolApprovalActionRequest, 'duration' | 'scope'>,
): Promise<ToolApprovalResponse> {
  const body: ToolApprovalActionRequest = { action }
  if (options?.duration) body.duration = options.duration
  if (options?.scope) body.scope = options.scope
  return request<ToolApprovalResponse>(`/tool-approvals/${encodeURIComponent(approvalId)}`, {
    method: 'POST',
    body: JSON.stringify(body),
  }, ToolApprovalResponseSchema as ZodType<ToolApprovalResponse>)
}

// The global tool policies are read and written with GET/PATCH
// /security/permissions (permissions.ts); /security/tool-policies is retired
// (Skills & Tools spec C-22 row 6).

// ── Tool Results (lazy fetch for ToolResultRef sentinels) ─────────────────────
// Endpoint: GET /api/v1/sessions/{session_id}/tool-results/{ref}
// Session-scoped: a ref is only readable in the session that produced it.
export function fetchToolResult(sessionId: string, ref: string): Promise<unknown> {
  return request<unknown>(
    `/sessions/${encodeURIComponent(sessionId)}/tool-results/${encodeURIComponent(ref)}`,
  )
}
