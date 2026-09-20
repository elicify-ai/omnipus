import { useState } from 'react'
import { Plus, X } from '@phosphor-icons/react'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'

/**
 * The one add-a-value / removable-chip editor in the workspace forms.
 *
 * It was two: `TagInput` (ADR-049 tags) and `WriteSetInput` (ADR-053 plan
 * write sets) were near-verbatim copies of each other — same draft/error
 * `useState` pair, same `commit`, same `remove`, same Enter handler, same
 * Input+Add-button row, same inline error line, same chip wrapper. Only five
 * things genuinely differed, and they are the five props below: `validate`,
 * `chipClassName`, `maxLength`, `noun`, `keepDraftOnError`.
 *
 * That duplication was not free. The duplicate-entry defect fixed in `remove`
 * and in the chip `key` (see both, below) had to be found and fixed twice —
 * and the second copy sat unfixed for as long as the two files existed
 * separately. One component means one place to fix it.
 */

/**
 * What a validator hands back for a single draft value.
 *
 * Deliberately the exact shape both `validateTag` (src/lib/tagValidation.ts)
 * and `validateWriteSetPath` (src/lib/writeSetValidation.ts) already return,
 * so neither needed an adapter: `value` is the normalised text to commit and
 * `error` is the exact message to render inline (empty for a silent
 * rejection, e.g. an empty draft, where nothing went wrong and there is
 * nothing to tell the author).
 */
export interface ChipValidationResult {
  /** True when the value may be committed (appended to the list). */
  ok: boolean
  /** The normalised value — present even when rejected, for echoing back. */
  value: string
  /** Empty when `ok`; otherwise the exact validation message to render inline. */
  error: string
}

export interface ChipListInputProps {
  /** The committed values, owned by the caller. May legitimately contain duplicates — see `remove`. */
  values: string[]
  onChange: (values: string[]) => void
  /** Pure validator for a single draft, checked against the values already present. */
  validate: (raw: string, existing: readonly string[]) => ChipValidationResult
  /**
   * Singular noun for this list's members, used verbatim in the Add button's
   * aria-label ("Add tag") and in each chip's remove aria-label
   * ("Remove tag release"). Callers' tests query by these exact strings.
   */
  noun: string
  /** Hard cap on the draft input, matching what the validator will accept. */
  maxLength: number
  /** Chip appearance — everything past the shared `CHIP_BASE_CLASS` layout. */
  chipClassName: string
  /** Chip remove-button appearance past the shared `shrink-0`. */
  chipRemoveClassName?: string
  /** Draft input appearance past the shared `text-xs flex-1`. */
  inputClassName?: string
  /**
   * Keep a rejected draft in the box when the rejection carries a message, so
   * the author can fix it in place rather than retype it. A silent rejection
   * (empty error) always clears, either way.
   */
  keepDraftOnError?: boolean
  id?: string
  ariaLabel?: string
  placeholder?: string
  /** Optional test hook on the root element. */
  testId?: string
  /** Optional test hook on each chip. */
  chipTestId?: string
}

/** Layout shared by every chip variant; appearance comes from `chipClassName`. */
const CHIP_BASE_CLASS = 'inline-flex items-center gap-1 px-2 py-0.5 text-[length:var(--type-caption-size)]'

function classes(...parts: (string | undefined)[]): string {
  return parts.filter(Boolean).join(' ')
}

export function ChipListInput({
  values,
  onChange,
  validate,
  noun,
  maxLength,
  chipClassName,
  chipRemoveClassName = 'hover:opacity-70',
  inputClassName,
  keepDraftOnError = false,
  id,
  ariaLabel,
  placeholder,
  testId,
  chipTestId,
}: ChipListInputProps) {
  const [draft, setDraft] = useState('')
  const [error, setError] = useState('')

  function commit() {
    const result = validate(draft, values)
    if (!result.ok) {
      setError(result.error)
      if (!keepDraftOnError || !result.error) setDraft('')
      return
    }
    setError('')
    setDraft('')
    // Never append a value already present. `validateTag` deliberately
    // returns ok for an already-present tag (re-adding is a no-op, not a
    // count violation), so this guard — not the validator — is what makes
    // re-adding do nothing. `validateWriteSetPath` rejects duplicates itself,
    // so the guard is unreachable on that path and changes nothing there.
    if (!values.includes(result.value)) onChange([...values, result.value])
  }

  // Remove by POSITION, never by value. Nothing dedupes a list on the way in:
  // the task store assigns `tags`/`write_set` verbatim (pkg/task/store.go) and
  // the REST handler passes the array straight through
  // (pkg/gateway/rest_tasks.go), so `create_plan` — or any non-SPA client —
  // can persist ['pkg/a.go', 'pkg/a.go']. Filtering by value would delete BOTH
  // copies and PATCH a list the operator never asked for.
  function remove(index: number) {
    onChange(values.filter((_, i) => i !== index))
  }

  const resolvedChipClassName = classes(CHIP_BASE_CLASS, chipClassName)
  const resolvedChipRemoveClassName = classes('shrink-0', chipRemoveClassName)
  return (
    <div className="flex flex-col gap-[var(--space-1)]" data-testid={testId}>
      <div className="flex items-center gap-[var(--space-2)]">
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
          maxLength={maxLength}
          aria-invalid={!!error}
          className={classes('text-[length:var(--type-utility-xs-size)] flex-1', inputClassName)}
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-9 px-[var(--space-2)] shrink-0"
          onClick={commit}
          aria-label={`Add ${noun}`}
          disabled={!draft.trim()}
        >
          <Plus size={13} />
        </Button>
      </div>
      {error && <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-error)]">{error}</p>}
      {values.length > 0 && (
        <div className="flex flex-wrap gap-[var(--space-1)] mt-[var(--space-1)]">
          {values.map((value, index) => (
            <span
              // Position-qualified: a list can legitimately arrive from the
              // server with the same entry twice (see `remove` above), and a
              // bare `key={value}` would then hand React two identical keys —
              // a console error, and a reconciliation that can carry a chip's
              // state onto the wrong copy.
              key={`${index}:${value}`}
              data-testid={chipTestId}
              className={resolvedChipClassName}
              title={value}
            >
              {/* Truncation is plain end-truncation with the full value on
                  hover via `title`. A value long enough to truncate is rare at
                  these widths, and the tooltip covers it. */}
              <span className="truncate">{value}</span>
              <button tabIndex={0}
                type="button"
                onClick={() => remove(index)}
                aria-label={`Remove ${noun} ${value}`}
                className={resolvedChipRemoveClassName}
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
