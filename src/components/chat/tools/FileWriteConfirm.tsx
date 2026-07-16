import { makeAssistantToolUI } from '@assistant-ui/react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { statusDot } from '@/lib/toolStatusConfig'

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

function byteCount(s?: string): string {
  if (!s) return '0 B'
  const bytes = new TextEncoder().encode(s).length
  if (bytes < 1024) return `${bytes} B`
  return `${(bytes / 1024).toFixed(1)} KB`
}

// Flat text-line design (ticket "Tool components in chat", P2): no card
// frame — matches GenericToolCall.tsx/toolStatusConfig.tsx. This row is not
// expandable (write/edit/append have no detail panel), so it's a single flat
// line: status dot/spinner, op label, path, optional byte-count detail. The
// decorative per-op icon (FloppyDisk/PencilSimple/Plus) is gone — the status
// dot is the only leading indicator, same as the other rows.
function FileOpBlock({
  label,
  path,
  detail,
  isRunning,
  isError,
}: {
  label: string
  path: string
  detail?: string
  isRunning: boolean
  isError?: boolean
}) {
  return (
    <div className="mt-2 flex items-center gap-2 py-1 text-xs font-mono">
      {isRunning ? (
        <ArrowsClockwise size={12} className="animate-spin text-[var(--color-accent)] shrink-0" />
      ) : isError ? (
        statusDot('bg-[var(--color-error)]')
      ) : (
        statusDot('bg-[var(--color-success)]')
      )}
      <span className="text-[var(--color-muted)] shrink-0">{label}</span>
      <span className="font-mono text-[var(--color-secondary)] truncate flex-1 min-w-0">
        {basename(path)}
      </span>
      {detail && !isRunning && (
        <span className="text-[var(--color-muted)] shrink-0">{detail}</span>
      )}
    </div>
  )
}

function makeWriteFileUI(toolName: string) {
  return makeAssistantToolUI<WriteFileArgs, unknown>({
    toolName,
    render: ({ args, status }) => (
      <FileOpBlock
        label={toolName}
        path={args?.path ?? '(unknown)'}
        detail={byteCount(args?.content)}
        isRunning={status.type === 'running'}
        isError={status.type === 'incomplete'}
      />
    ),
  })
}

export const FileWriteConfirmUI = makeWriteFileUI('write_file')

// BRD C.6.1.4 tool name (dot-notation). Backend uses Omnipus convention (write_file); both registered.
export const FileWriteAliasDotUI = makeWriteFileUI('file.write')

export const EditFileConfirmUI = makeAssistantToolUI<EditFileArgs, unknown>({
  toolName: 'edit_file',
  render: ({ args, status }) => (
    <FileOpBlock
      label="edit_file"
      path={args?.path ?? '(unknown)'}
      isRunning={status.type === 'running'}
      isError={status.type === 'incomplete'}
    />
  ),
})

export const AppendFileConfirmUI = makeAssistantToolUI<AppendFileArgs, unknown>({
  toolName: 'append_file',
  render: ({ args, status }) => (
    <FileOpBlock
      label="append_file"
      path={args?.path ?? '(unknown)'}
      detail={byteCount(args?.content)}
      isRunning={status.type === 'running'}
      isError={status.type === 'incomplete'}
    />
  ),
})
