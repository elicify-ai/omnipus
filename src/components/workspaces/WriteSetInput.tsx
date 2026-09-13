import { useState } from 'react'
import { Plus, X } from '@phosphor-icons/react'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { validateWriteSetPath } from '@/lib/writeSetValidation'

interface WriteSetInputProps {
  paths: string[]
  onChange: (paths: string[]) => void
  id?: string
  ariaLabel?: string
  placeholder?: string
}

/**
 * Write-set path editor — the authoring surface for a plan member's
 * `write_set` (ADR-053 §Contract Surface, US-11/G-16).
 *
 * Reuses the add-button + removable-chip grammar of `TagInput` (itself
 * modelled on the todos/dependencies editors), with two deliberate
 * departures, because these are PATHS and not tags:
 *
 *   - No case folding. `TagInput` lowercases silently; a path's case is
 *     load-bearing (`pkg/Plan` and `pkg/plan` are different files on a
 *     case-sensitive checkout, and the plan lint compares them literally).
 *   - Monospace, neutral-surface chips rather than accent-coloured tag pills,
 *     so a write set reads as code the lint will compare rather than as
 *     free-form labels.
 *
 * Validation is delegated to the pure `validateWriteSetPath`; see that module
 * for why each of its three rejections exists.
 */
export function WriteSetInput({
  paths,
  onChange,
  id,
  ariaLabel = 'Add a path this task writes',
  placeholder = 'e.g. pkg/plan/lint.go',
}: WriteSetInputProps) {
  const [draft, setDraft] = useState('')
  const [error, setError] = useState('')

  function commit() {
    const result = validateWriteSetPath(draft, paths)
    if (!result.ok) {
      // A rejection with a message keeps the draft in the box so the author
      // can fix it in place; a silent rejection (empty input) just clears.
      setError(result.error)
      if (!result.error) setDraft('')
      return
    }
    setError('')
    setDraft('')
    onChange([...paths, result.value])
  }

  function remove(path: string) {
    onChange(paths.filter((p) => p !== path))
  }

  return (
    <div className="flex flex-col gap-1.5" data-testid="write-set-input">
      <div className="flex items-center gap-2">
        <Input
          id={id}
          aria-label={ariaLabel}
          value={draft}
          onChange={(e) => { setDraft(e.target.value); setError('') }}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              commit()
            }
          }}
          placeholder={placeholder}
          maxLength={500}
          aria-invalid={!!error}
          className="text-xs font-mono flex-1"
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-9 px-2 shrink-0"
          onClick={commit}
          aria-label="Add path"
          disabled={!draft.trim()}
        >
          <Plus size={13} />
        </Button>
      </div>
      {error && <p className="text-xs text-[var(--color-error)]">{error}</p>}
      {paths.length > 0 && (
        <div className="flex flex-wrap gap-1.5 mt-1">
          {paths.map((path) => (
            <span
              key={path}
              data-testid="write-set-chip"
              className="inline-flex items-center gap-1 rounded border border-[var(--color-border)] bg-[var(--color-surface-2)] px-2 py-0.5 font-mono text-[10px] text-[var(--color-secondary)] max-w-[200px]"
              title={path}
            >
              {/* Truncation is plain end-truncation with the full path on
                  hover via `title`, exactly as TagInput does. A path long
                  enough to truncate is rare at this width, and the tooltip
                  covers it. */}
              <span className="truncate">{path}</span>
              <button tabIndex={0}
                type="button"
                onClick={() => remove(path)}
                aria-label={`Remove path ${path}`}
                className="shrink-0 text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors"
              >
                <X size={9} />
              </button>
            </span>
          ))}
        </div>
      )}
    </div>
  )
}
