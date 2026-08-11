import { makeAssistantToolUI } from '@assistant-ui/react'
import { getToolBadgeStatusConfig, isCancelledStatus } from '@/lib/toolStatusConfig'
import { isFileExistsRefusal } from './GenericToolCall'
import { useChatStore } from '@/store/chat'
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
  isRefusal,
}: {
  label: string
  path: string
  detail?: string
  reason?: string
  isRunning: boolean
  isError?: boolean
  isCancelled?: boolean
  isRefusal?: boolean
}) {
  // A precondition refusal gets its own label, matching GenericToolCall (the
  // REPLAY renderer for this same tool). Without this the identical event read
  // "Failed" during the turn and "File already exists" after a reload — a
  // live/replay divergence introduced by the very commit that fixed the last
  // one. The distinction is also the point of W5: the file being already there
  // is frequently the correct outcome, not a failure.
  const statusConfig = isRefusal && !isRunning
    ? {
        indicator: <span className="inline-block size-[8px] shrink-0 rounded-full bg-[var(--color-warning)]" />,
        label: 'File already exists',
        textClass: 'text-[var(--color-warning)]',
      }
    : getToolBadgeStatusConfig(
        isRunning ? 'running' : isCancelled ? 'cancelled' : isError ? 'error' : 'success',
        { size: 12, cancelledVariant: 'muted' }
      )
  // Shown on failure or cancellation, and deliberately NOT truncated the way
  // the path is — a reason clipped to fit is a reason you have to go looking
  // for elsewhere, which is the state this replaces. A successful write has
  // nothing to explain.
  const showReason = (isError || isCancelled || isRefusal) && !isRunning && reason
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

// FileOpRow is a real component, not an inline render callback, because it
// needs a hook: AssistantUI's part status cannot report a failed tool call
// ONCE THAT CALL HAS A RESULT — which every finished call does.
//
// toMessagePartStatus (@assistant-ui/core, runtime/api/message-runtime.js)
// short-circuits a tool-call part to {type:'complete'} the moment the part has
// a truthy `result`. Setting the part's own `isError` would not help either —
// toMessagePartStatus never reads it. A refused write has a result, so its
// part status is
// 'complete' and `status.type === 'incomplete'` is false. Gating on that alone
// rendered a FAILED write with the success dot and a "Done" label, and the
// reason line never mounted at all.
//
// The store is the only place the outcome actually lives: ToolCall.status and
// ToolCall.error, set from the tool_call_result frame. GenericToolCall already
// works this way (`status.type === 'incomplete' || !!error`, with `error`
// threaded in from the store by ChatScreen's FallbackToolUI) — this brings the
// registered file-op UIs onto the same footing rather than inventing a
// second mechanism.
function FileOpRow({
  label,
  toolCallId,
  path,
  detail,
  result,
  status,
}: {
  label: string
  toolCallId: string
  path: string
  detail?: string
  result: unknown
  status: { type: string }
}) {
  const storeCall = useChatStore((s) => s.toolCalls[toolCallId])
  // The gateway parses the structured refusal into the frame's `result`, so
  // the object is what lands in the store — the same shape GenericToolCall
  // matches on for replay.
  const isRefusal = isFileExistsRefusal(storeCall?.result) || isFileExistsRefusal(result)
  const isRunning = status.type === 'running' || storeCall?.status === 'running'
  const isCancelled = isCancelledStatus(status) || storeCall?.status === 'cancelled'
  // Issue #617: the `status.type === 'incomplete'` disjunct is dropped — it
  // can never be true for a finished tool call carrying a result (see this
  // file's FileOpRow doc comment above), so it never contributed a real
  // signal here; `storeCall?.status === 'error'` was always doing the actual
  // work. The store hook stays (rather than switching to the render-prop
  // `isError` field omnipus-runtime.ts now also sets): FileOpRow still needs
  // the hook for `reason` (storeCall?.error), which has no equivalent on
  // that boolean, so there is no simplification to gain by reading both.
  const isError = !isCancelled && storeCall?.status === 'error'
  return (
    <FileOpBlock
      label={label}
      path={path}
      detail={detail}
      // The store's own error string is preferred: for a plain failure the
      // gateway puts the reason there and leaves `result` as the raw text,
      // and on replay `result` can be absent entirely.
      reason={failureReason(storeCall?.error) ?? failureReason(storeCall?.result ?? result)}
      isRunning={isRunning}
      isError={isError}
      isCancelled={isCancelled}
      isRefusal={isRefusal}
    />
  )
}

function makeWriteFileUI(toolName: string) {
  return makeAssistantToolUI<WriteFileArgs, unknown>({
    toolName,
    render: ({ toolCallId, args, result, status }) => (
      <FileOpRow
        label={toolName}
        toolCallId={toolCallId}
        path={args?.path ?? '(unknown)'}
        detail={byteCount(args?.content)}
        result={result}
        status={status}
      />
    ),
  })
}

export const FileWriteConfirmUI = makeWriteFileUI('write_file')

// BRD C.6.1.4 tool name (dot-notation). Backend uses Omnipus convention (write_file); both registered.
export const FileWriteAliasDotUI = makeWriteFileUI('file.write')

export const EditFileConfirmUI = makeAssistantToolUI<EditFileArgs, unknown>({
  toolName: 'edit_file',
  render: ({ toolCallId, args, result, status }) => (
    <FileOpRow
      label="edit_file"
      toolCallId={toolCallId}
      path={args?.path ?? '(unknown)'}
      result={result}
      status={status}
    />
  ),
})

export const AppendFileConfirmUI = makeAssistantToolUI<AppendFileArgs, unknown>({
  toolName: 'append_file',
  render: ({ toolCallId, args, result, status }) => (
    <FileOpRow
      label="append_file"
      toolCallId={toolCallId}
      path={args?.path ?? '(unknown)'}
      detail={byteCount(args?.content)}
      result={result}
      status={status}
    />
  ),
})
