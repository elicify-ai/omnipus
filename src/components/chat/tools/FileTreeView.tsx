import { useState } from 'react'
import { makeAssistantToolUI } from '@assistant-ui/react'
import {
  Folder,
  FolderOpen,
  File,
  ArrowsClockwise,
  CheckCircle,
  CaretDown,
  CaretUp,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

interface ListDirArgs {
  path?: string
}

interface TreeEntry {
  name: string
  isDir: boolean
  indent: number
}

/**
 * Parses a text directory listing (ls-style or tree-style) into structured entries.
 * Handles leading whitespace/tree chars (│, ├, └, ─) to infer indent depth.
 * Directories are identified by a trailing "/" or "\".
 * Capped at 200 entries to prevent runaway rendering on huge directories.
 */
function parseTree(text: string): TreeEntry[] {
  const lines = text.split('\n').filter((l) => l.trim() !== '')
  const entries: TreeEntry[] = []

  for (const line of lines) {
    // Count leading whitespace / tree chars
    const stripped = line.replace(/^[\s│├└─]+/, '')
    const indent = Math.floor((line.length - stripped.length) / 2)
    const name = stripped.trim()
    if (!name) continue

    // Dirs: end with / or common dir indicators
    const isDir = name.endsWith('/') || name.endsWith('\\')
    entries.push({ name: isDir ? name.slice(0, -1) : name, isDir, indent })
  }

  return entries.slice(0, 200) // cap at 200 entries
}

function FileTreeBlock({
  args,
  result,
  isRunning,
}: {
  args: ListDirArgs
  result: unknown
  isRunning: boolean
}) {
  const [expanded, setExpanded] = useState(false)

  const path = args.path ?? '.'
  const content = result != null ? String(result) : ''
  const entries = content ? parseTree(content) : []

  return (
    <div className="mt-2 rounded-md border border-[var(--color-border)] overflow-hidden text-xs">
      {/* Header */}
      <button
        type="button"
        onClick={() => !isRunning && setExpanded((e) => !e)}
        className={cn(
          'flex w-full items-center gap-2 px-3 py-2 bg-[var(--color-surface-1)] transition-colors text-left',
          !isRunning && 'hover:bg-[var(--color-surface-3)] cursor-pointer',
          isRunning && 'cursor-default'
        )}
        aria-expanded={expanded}
      >
        {expanded
          ? <FolderOpen size={13} weight="duotone" className="text-[var(--color-accent)] shrink-0" />
          : <Folder size={13} weight="duotone" className="text-[var(--color-accent)] shrink-0" />
        }
        <span className="font-mono text-[var(--color-secondary)] truncate flex-1 min-w-0">{path}</span>
        <span className="flex items-center gap-1.5 text-[var(--color-muted)] shrink-0">
          {isRunning ? (
            <ArrowsClockwise size={12} className="animate-spin text-[var(--color-accent)]" />
          ) : content ? (
            <>
              <CheckCircle size={12} weight="fill" className="text-[var(--color-success)]" />
              <span>{entries.length} entries</span>
            </>
          ) : null}
          {!isRunning && (
            <span className="ml-1">{expanded ? <CaretUp size={10} /> : <CaretDown size={10} />}</span>
          )}
        </span>
      </button>

      {/* Tree panel */}
      {expanded && !isRunning && (
        <div className="border-t border-[var(--color-border)] bg-[var(--color-surface-1)] max-h-64 overflow-auto px-3 py-2 space-y-0.5">
          {entries.length > 0 ? (
            entries.map((entry, i) => (
              <div
                key={i}
                className="flex items-center gap-1.5 font-mono text-[10px] text-[var(--color-secondary)]"
                style={{ paddingLeft: `${entry.indent * 12}px` }}
              >
                {entry.isDir
                  ? <Folder size={11} weight="duotone" className="text-[var(--color-accent)] shrink-0" />
                  : <File size={11} className="text-[var(--color-muted)] shrink-0" />
                }
                <span>{entry.name}</span>
              </div>
            ))
          ) : (
            <pre className="text-[10px] text-[var(--color-secondary)] whitespace-pre-wrap break-all">
              {content}
            </pre>
          )}
        </div>
      )}
    </div>
  )
}

export const FileTreeViewUI = makeAssistantToolUI<ListDirArgs, unknown>({
  toolName: 'list_dir',
  render: ({ args, result, status }) => (
    <FileTreeBlock
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
    />
  ),
})

// BRD C.6.1.4 tool name (dot-notation). Backend uses Omnipus convention (list_dir); both registered.
export const FileListAliasDotUI = makeAssistantToolUI<ListDirArgs, unknown>({
  toolName: 'file.list',
  render: ({ args, result, status }) => (
    <FileTreeBlock
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
    />
  ),
})
