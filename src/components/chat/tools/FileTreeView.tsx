import { useState } from 'react'
import { makeAssistantToolUI } from '@assistant-ui/react'
import { Folder, File, ArrowsClockwise, CaretDown, CaretUp } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { statusDot } from '@/lib/toolStatusConfig'

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
    // Flat text-line design (ticket "Tool components in chat", P2): no card
    // frame — see GenericToolCall.tsx/toolStatusConfig.tsx for the reference
    // language. The header's Folder/FolderOpen toggle icon is gone (leading
    // slot is the status dot/spinner only, like the other rows); each tree
    // entry below keeps its own Folder/File icon and indentation — that's
    // the file tree's identity, preserved per the flat-redesign spec.
    <div className="mt-2 text-xs font-mono">
      {/* Header */}
      <button tabIndex={0}
        type="button"
        onClick={() => !isRunning && setExpanded((e) => !e)}
        className={cn(
          'flex w-full items-center gap-2 py-1 transition-colors text-left',
          !isRunning && 'hover:bg-[var(--color-surface-2)]/60 cursor-pointer',
          isRunning && 'cursor-default'
        )}
        aria-expanded={expanded}
      >
        {isRunning ? (
          <ArrowsClockwise size={12} className="animate-spin text-[var(--color-accent)] shrink-0" />
        ) : content ? (
          statusDot('bg-[var(--color-success)]')
        ) : null}
        <span className="font-mono text-[var(--color-secondary)] truncate flex-1 min-w-0">{path}</span>
        <span className="flex items-center gap-1.5 text-[var(--color-muted)] shrink-0">
          {content && <span>{entries.length} entries</span>}
          {!isRunning && (
            <span className="ml-1">{expanded ? <CaretUp size={10} /> : <CaretDown size={10} />}</span>
          )}
        </span>
      </button>

      {/* Tree panel — left-accent block, no bordered card. Entries keep their
          Folder/File icons and paddingLeft-based indentation unchanged. */}
      {expanded && !isRunning && (
        <div className="ml-[3px] border-l-2 border-[var(--color-border)] max-h-64 overflow-auto py-1 pl-3 space-y-0.5">
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
