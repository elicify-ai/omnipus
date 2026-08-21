// Shared types for the tool-approval preview registry (see registry.ts).
//
// The registry replaces the old "if (toolName === 'bash') show a hand-rolled
// preview" special-case in ToolApprovalModal.tsx with a per-tool lookup, so
// covering a new tool with a readable summary is a new registry ENTRY, not a
// new branch in the modal's render tree. Tools with no entry keep rendering
// the generic Tool-name + raw Arguments JSON dump (the pre-existing
// fallback) — nothing breaks while tools are filled in one at a time.

import type { ComponentType } from 'react'

/** Everything a preview component needs to render a readable summary. */
export interface ToolApprovalPreviewContext {
  toolName: string
  args: Record<string, unknown>
  agentId: string
  /** Best-effort resolved display name for agentId; falls back to agentId itself when the agents cache has no match (see resolveAgentName in ToolApprovalModal.tsx). */
  agentName: string
  sessionId: string
}

export interface ToolApprovalPreviewEntry {
  /**
   * 'additive' — keep the generic "Tool" name line and the raw Arguments
   * JSON dump; render Body ABOVE the JSON dump as a supplementary preview.
   * This is bash's existing behaviour (readable command preview, JSON dump
   * still available underneath).
   *
   * 'replace' — hide the generic Tool line AND the raw Arguments JSON dump
   * entirely; Body is the whole card body. For a tool weighty enough to earn
   * this (request_mount is the first), the raw argument names are jargon
   * ("host_path") that the readable summary already fully explains — a technical
   * dump alongside it would just be a second, contradictory way to ask the
   * same question the summary already answers plainly.
   */
  mode: 'additive' | 'replace'
  Body: ComponentType<ToolApprovalPreviewContext>
  /** 'replace' only — overrides the DialogTitle text ("Tool Approval Required"). */
  title?: (ctx: ToolApprovalPreviewContext) => string
  /** 'replace' only — overrides the Approve button's label. The dispatched action is always 'approve' regardless of label. */
  primaryLabel?: string
  /** 'replace' only — overrides the Deny button's label. The dispatched action is always 'deny' regardless of label. */
  secondaryLabel?: string
  /** 'replace' only — whether to keep offering Cancel. Defaults to true; request_mount sets this false (see RequestMountApprovalPreview.tsx). */
  showCancel?: boolean
}
