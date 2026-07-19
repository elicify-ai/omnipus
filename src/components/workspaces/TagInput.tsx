import { useState } from 'react'
import { Plus, X } from '@phosphor-icons/react'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { validateTag } from '@/lib/tagValidation'

interface TagInputProps {
  tags: string[]
  onChange: (tags: string[]) => void
  id?: string
  ariaLabel?: string
  placeholder?: string
}

/**
 * Free-form tag editor (ADR-049 — the milestone replacement). Reuses the
 * add-button + removable-chip grammar of the todos/dependencies editors
 * (`CreateTaskSlideOver.tsx:593-643`). Validation is delegated to the pure
 * `validateTag` (SD-C8) — lowercase/trim happen silently, length/count
 * violations surface the exact inline message.
 */
export function TagInput({ tags, onChange, id, ariaLabel = 'Add tag', placeholder = 'Add a tag…' }: TagInputProps) {
  const [draft, setDraft] = useState('')
  const [error, setError] = useState('')

  function commit() {
    const result = validateTag(draft, tags)
    if (!result.ok) {
      if (result.error) setError(result.error)
      setDraft('')
      return
    }
    setError('')
    setDraft('')
    if (!tags.includes(result.value)) onChange([...tags, result.value])
  }

  function remove(tag: string) {
    onChange(tags.filter((t) => t !== tag))
  }

  return (
    <div className="flex flex-col gap-1.5">
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
          maxLength={200}
          className="text-xs flex-1"
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-9 px-2 shrink-0"
          onClick={commit}
          aria-label="Add tag"
          disabled={!draft.trim()}
        >
          <Plus size={13} />
        </Button>
      </div>
      {error && <p className="text-xs text-[var(--color-error)]">{error}</p>}
      {tags.length > 0 && (
        <div className="flex flex-wrap gap-1.5 mt-1">
          {tags.map((tag) => (
            <span
              key={tag}
              className="inline-flex items-center gap-1 rounded-full bg-[var(--color-accent)]/10 border border-[var(--color-accent)]/20 px-2 py-0.5 text-[10px] text-[var(--color-accent)] max-w-[160px]"
              title={tag}
            >
              <span className="truncate">{tag}</span>
              <button tabIndex={0}
                type="button"
                onClick={() => remove(tag)}
                aria-label={`Remove tag ${tag}`}
                className="shrink-0 hover:opacity-70"
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
