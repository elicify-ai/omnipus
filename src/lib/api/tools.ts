// tools.ts: Tool registry, per-agent tools, global policies, MCP servers

import type { ZodType } from 'zod'
import { z } from 'zod'
import {
  GlobalToolPolicies as GlobalToolPoliciesSchema,
  ToolRegistryEntry as ToolRegistryEntrySchema,
  ToolApprovalResponse as ToolApprovalResponseSchema,
  McpServer as McpServerSchema,
  McpServerTestResponse as McpServerTestResponseSchema,
  AgentToolsResponse as AgentToolsResponseSchema,
  McpServerToolsResponse as McpServerToolsResponseSchema,
} from '@/lib/api/generated/schemas'
import type {
  ToolRegistryEntry,
  GlobalToolPolicies,
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
 * FR-011, FR-082. Throws with status code prefix on non-2xx (e.g. "403: ...").
 *
 * action is the generated ToolApprovalActionRequest['action'] union — includes
 * "always" (approve this call AND record a session-scoped Always-Allow grant
 * via ApprovalGrantStore.Record; see pkg/gateway/rest_tool_registry.go).
 * On "always", grant_recorded is present: true if the standing grant stuck,
 * false if this call was approved once but the next identical call will ask
 * again.
 */
export function submitToolApproval(
  approvalId: string,
  action: ToolApprovalActionRequest['action'],
): Promise<ToolApprovalResponse> {
  return request<ToolApprovalResponse>(`/tool-approvals/${encodeURIComponent(approvalId)}`, {
    method: 'POST',
    body: JSON.stringify({ action }),
  }, ToolApprovalResponseSchema as ZodType<ToolApprovalResponse>)
}

// ── Global Tool Policies ──────────────────────────────────────────────────────
// GlobalToolPolicies — re-exported from generated openapi-types (no local body needed).

export function fetchGlobalToolPolicies(): Promise<GlobalToolPolicies> {
  return request<GlobalToolPolicies>('/security/tool-policies', undefined, GlobalToolPoliciesSchema)
}

// updateGlobalToolPolicies persists the global tool-policy grant. It is re-auth
// gated (Spec-3 FR-3.3 / Spec-6 FR-12.2): the server rejects the PUT with 403
// unless a single-use consent token (from reAuth) is replayed in the
// X-Reauth-Token header.
export function updateGlobalToolPolicies(
  cfg: GlobalToolPolicies,
  reAuthToken?: string,
): Promise<GlobalToolPolicies> {
  return request<GlobalToolPolicies>('/security/tool-policies', {
    method: 'PUT',
    headers: reAuthToken ? { [REAUTH_HEADER]: reAuthToken } : undefined,
    body: JSON.stringify(cfg),
  }, GlobalToolPoliciesSchema)
}

// ── Tool Results (lazy fetch for ToolResultRef sentinels) ─────────────────────
// Endpoint: GET /api/v1/sessions/{session_id}/tool-results/{ref}
// Session-scoped: a ref is only readable in the session that produced it.
export function fetchToolResult(sessionId: string, ref: string): Promise<unknown> {
  return request<unknown>(
    `/sessions/${encodeURIComponent(sessionId)}/tool-results/${encodeURIComponent(ref)}`,
  )
}
