import { makeAssistantToolUI } from '@assistant-ui/react'
import { getToolBadgeStatusConfig, isCancelledStatus } from '@/lib/toolStatusConfig'
import { cn } from '@/lib/utils'

interface WriteFileArgs {
  path?: string
  content?: string
}

interface EditFileArgs {
  path?: string
  old_string?: string
  new_string?: string
  replace_all?: boolean
}

interface AppendFileArgs {
  path?: string
  content?: string
}

function basename(p: string): string {
  return p.split(/[/\\]/).pop() ?? p
}

// A failed file op used to render as `write_file · a.svg · 1.2 KB · Failed`
// with no reason at all, which is the same row whether the file already
// existed, the disk was full, or the path was denied. ADR-059 W5 A4-3 fixes
// the human-facing half of that.
//
// Two shapes arrive here. A structured refusal (write_file's FileExistsRefusal)
// is an object carrying a prose `reason`; every other failure arrives as the
// error text itself. Both are rendered the same way — the point is that the
// row stops being silent, not that the user learns which shape it was.
function failureReason(result: unknown): string | undefined {
  if (typeof result === 'string') {
    const trimmed = result.trim()
    return trimmed === '' ? undefined : trimmed
  }
  if (result !== null && typeof result === 'object') {
    const reason = (result as Record<string, unknown>)['reason']
    if (typeof reason === 'string' && reason.trim() !== '') return reason.trim()
  }
  return undefined
}

function byteCount(s?: string): string {
  if (!s) return '0 B'
  const bytes = new TextEncoder().encode(s).length
  if (bytes < 1024) return `${bytes} B`
  return `${(bytes / 1024).toFixed(1)} KB`
}

// Flat text-line design (ticket "Tool components in chat", P2): no card
// frame — matches GenericToolCall.tsx/toolStatusConfig.tsx. This row is not
// expandable (write/edit/append have no detail panel), so it's a single flat
// line: status dot/spinner, op label, path, optional byte-count detail, and
// a status label. The decorative per-op icon (FloppyDisk/PencilSimple/Plus)
// is gone — the status dot is the only leading indicator, same as the other
// rows. Status text (Running.../Done/Failed/Cancelled) is rendered via
// getToolBadgeStatusConfig like BashOutput does — a dot color alone is not
// enough to distinguish a failed write from a successful one for
// colorblind users or screen readers (WCAG 1.4.1).
function FileOpBlock({
  label,
  path,
  detail,
  reason,
  isRunning,
  isError,
  isCancelled,
}: {
  label: string
  path: string
  detail?: string
  reason?: string
  isRunning: boolean
  isError?: boolean
  isCancelled?: boolean
}) {
  const statusConfig = getToolBadgeStatusConfig(
    isRunning ? 'running' : isCancelled ? 'cancelled' : isError ? 'error' : 'success',
    { size: 12, cancelledVariant: 'muted' }
  )
  // Only on failure. A successful write has nothing to explain, and the
  // reason line is deliberately NOT truncated the way the path is — a reason
  // clipped to fit is a reason you have to go looking for elsewhere, which is
  // the state this replaces.
  const showReason = isError && !isRunning && reason
  // The reason wraps onto its own line via flex-wrap + basis-full rather than
  // living inside a nested row container. That keeps the status indicator as
  // this element's FIRST CHILD, which FileTools.edge.test.tsx asserts
  // positionally — a wrapper div would have broken four existing tests for a
  // purely cosmetic reason.
  return (
    <div className="mt-2 flex flex-wrap items-center gap-2 py-1 text-xs font-mono">
      {statusConfig.indicator}
      <span className="text-[var(--color-muted)] shrink-0">{label}</span>
      <span className="font-mono text-[var(--color-secondary)] truncate flex-1 min-w-0">
        {basename(path)}
      </span>
      {detail && !isRunning && (
        <span className="text-[var(--color-muted)] shrink-0">{detail}</span>
      )}
      <span className={cn('text-[var(--color-muted)] shrink-0', statusConfig.textClass)}>
        {statusConfig.label}
      </span>
      {showReason && (
        <span className={cn('basis-full pl-5 break-words', statusConfig.textClass)}>{reason}</span>
      )}
    </div>
  )
}

function makeWriteFileUI(toolName: string) {
  return makeAssistantToolUI<WriteFileArgs, unknown>({
    toolName,
    render: ({ args, result, status }) => (
      <FileOpBlock
        label={toolName}
        path={args?.path ?? '(unknown)'}
        detail={byteCount(args?.content)}
        reason={failureReason(result)}
        isRunning={status.type === 'running'}
        isError={status.type === 'incomplete'}
        isCancelled={isCancelledStatus(status)}
      />
    ),
  })
}

export const FileWriteConfirmUI = makeWriteFileUI('write_file')

// BRD C.6.1.4 tool name (dot-notation). Backend uses Omnipus convention (write_file); both registered.
export const FileWriteAliasDotUI = makeWriteFileUI('file.write')

export const EditFileConfirmUI = makeAssistantToolUI<EditFileArgs, unknown>({
  toolName: 'edit_file',
  render: ({ args, result, status }) => (
    <FileOpBlock
      label="edit_file"
      path={args?.path ?? '(unknown)'}
      reason={failureReason(result)}
      isRunning={status.type === 'running'}
      isError={status.type === 'incomplete'}
      isCancelled={isCancelledStatus(status)}
    />
  ),
})

export const AppendFileConfirmUI = makeAssistantToolUI<AppendFileArgs, unknown>({
  toolName: 'append_file',
  render: ({ args, result, status }) => (
    <FileOpBlock
      label="append_file"
      path={args?.path ?? '(unknown)'}
      detail={byteCount(args?.content)}
      reason={failureReason(result)}
      isRunning={status.type === 'running'}
      isError={status.type === 'incomplete'}
      isCancelled={isCancelledStatus(status)}
    />
  ),
})
