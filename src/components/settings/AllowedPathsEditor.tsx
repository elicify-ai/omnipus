import { useState } from 'react'
import { Trash, Plus } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

// Read-only badge with tooltip for allowed_paths rows
function ReadOnlyBadge() {
  const [tip, setTip] = useState(false)
  return (
    <span className="relative inline-block">
      <button
        type="button"
        className="inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-mono border border-[var(--color-border)] bg-[var(--color-surface-2)] text-[var(--color-muted)] cursor-default"
        onMouseEnter={() => setTip(true)}
        onMouseLeave={() => setTip(false)}
        onFocus={() => setTip(true)}
        onBlur={() => setTip(false)}
        tabIndex={0}
        aria-describedby={tip ? 'ro-tip' : undefined}
      >
        read-only
      </button>
      {tip && (
        <span
          id="ro-tip"
          role="tooltip"
          className="absolute bottom-full left-0 mb-1 z-50 w-64 rounded border border-[var(--color-border)] bg-[var(--color-surface-1)] px-2 py-1.5 text-[10px] text-[var(--color-muted)] shadow-lg pointer-events-none"
        >
          AllowedPaths entries grant read-only access. Write access is never available via this editor.
        </span>
      )}
    </span>
  )
}

interface AllowedPathsEditorProps {
  paths: string[]
  rowErrors: Record<number, string>
  restartedRows: Set<number>
  onDelete: (index: number) => void
  newPath: string
  onNewPathChange: (v: string) => void
  onAdd: () => void
  addError: string | null
}

export function AllowedPathsEditor({
  paths,
  rowErrors,
  restartedRows,
  onDelete,
  newPath,
  onNewPathChange,
  onAdd,
  addError,
}: AllowedPathsEditorProps) {
  return (
    <div className="space-y-2">
      <p className="text-xs font-semibold text-[var(--color-secondary)]">
        Filesystem paths the sandbox may read
      </p>

      {paths.length === 0 && (
        <p className="text-xs text-[var(--color-muted)] italic">No allowed paths configured.</p>
      )}

      <div className="space-y-1">
        {paths.map((p, i) => (
          <div key={i} className="flex flex-col gap-0.5">
            <div className="flex items-center gap-2 rounded border border-[var(--color-border)] bg-[var(--color-surface-2)] px-2 py-1.5">
              <span className="flex-1 text-xs font-mono text-[var(--color-secondary)] break-all">
                {p}
              </span>
              <ReadOnlyBadge />
              {restartedRows.has(i) && (
                <span className="inline-block rounded px-1.5 py-0.5 text-[10px] border border-yellow-500/40 bg-yellow-500/10 text-yellow-400">
                  restart required
                </span>
              )}
              <button
                type="button"
                aria-label={`Delete path ${p}`}
                className="text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors focus:outline-none rounded"
                onClick={() => onDelete(i)}
              >
                <Trash size={12} />
              </button>
            </div>
            {rowErrors[i] && (
              <p className="text-[10px] text-[var(--color-error)] pl-2">{rowErrors[i]}</p>
            )}
          </div>
        ))}
      </div>

      <div className="space-y-1">
        <div className="flex items-center gap-2">
          <Input
            value={newPath}
            onChange={(e) => onNewPathChange(e.target.value)}
            placeholder="/var/data/shared"
            className="h-7 text-xs font-mono flex-1"
            aria-label="New allowed path"
            onKeyDown={(e) => {
              if (e.key === 'Enter') { e.preventDefault(); onAdd() }
            }}
          />
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="h-7 px-2 gap-1 text-xs shrink-0"
            onClick={onAdd}
            aria-label="Add path"
          >
            <Plus size={11} />
            Add
          </Button>
        </div>
        {addError && (
          <p className="text-[10px] text-[var(--color-error)]">{addError}</p>
        )}
      </div>
    </div>
  )
}
