/**
 * PreviewToolHeader — shared header badge for preview tool UI components.
 *
 * Used by ServeWorkspaceUI (serve_workspace) and RunInWorkspaceUI
 * (run_in_workspace) to avoid duplicating the icon + tool-name chip +
 * label chip + status icon pattern.
 *
 * Spec: FR-008 / FR-008a.
 */

import type { ReactNode } from 'react'
import { getToolBadgeStatusConfig } from '@/lib/toolStatusConfig'

export interface PreviewToolHeaderProps {
  /** Phosphor icon element (e.g. <Globe />, <Terminal />). */
  icon: ReactNode
  /** Tool name shown in monospace (e.g. "serve_workspace"). */
  toolName: string
  /** Optional code chip shown after the tool name (path or command). */
  label?: string
  /** Element rendered at the far right (status icon or port chip). */
  trailing?: ReactNode
  /** Whether the tool is still running (drives the leading status dot/spinner). */
  isRunning: boolean
  /** Whether the tool completed successfully (drives the leading status dot colour). */
  hasResult: boolean
  /** Optional data-testid for targeted e2e tests. */
  'data-testid'?: string
}

export function PreviewToolHeader({
  icon,
  toolName,
  label,
  trailing,
  isRunning,
  hasResult,
  'data-testid': testId,
}: PreviewToolHeaderProps) {
  // Flat text-line redesign (ticket "Tool components in chat", P2): the old
  // rounded-t-md/border/bg-surface-1 header frame and status-tinted border
  // are gone — status lives only in the leading dot/spinner (shared with
  // GenericToolCall/ToolCallBadge/BashOutput/BrowserTool via
  // getToolBadgeStatusConfig), never a colored border. The caller-supplied
  // `icon` still renders alongside it since (unlike those callers) it is the
  // only thing distinguishing preview kind — WebServeUI passes an
  // identical `toolName` ("web_serve") for both its static and dev modes.
  const statusConfig = getToolBadgeStatusConfig(isRunning ? 'running' : hasResult ? 'success' : 'error', {
    size: 13,
  })

  return (
    <div data-testid={testId} className="flex items-center gap-2 py-1 font-mono text-xs">
      {statusConfig.indicator}
      {icon}
      <span className="text-[var(--color-muted)] font-mono">{toolName}</span>
      {label && (
        <code className="ml-1 text-[var(--color-accent)] font-mono text-[10px] truncate max-w-[280px]">
          {label}
        </code>
      )}
      {trailing && <span className="ml-auto shrink-0">{trailing}</span>}
    </div>
  )
}
