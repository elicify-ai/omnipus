import { ChipListInput } from '@/components/workspaces/ChipListInput'
import { validateTag } from '@/lib/tagValidation'

interface TagInputProps {
  tags: string[]
  onChange: (tags: string[]) => void
  id?: string
  ariaLabel?: string
  placeholder?: string
}

/**
 * Free-form tag editor (ADR-049 — the milestone replacement).
 *
 * The editor itself is `ChipListInput`; this is the TAG configuration of it —
 * the validator, the accent-pill chip styling, the 200-character draft cap and
 * the "tag" noun that both call sites (the Create Task form and the task
 * detail panel) would otherwise repeat verbatim. Validation is delegated to
 * the pure `validateTag` (SD-C8): lowercase/trim happen silently, length/count
 * violations surface the exact inline message.
 */
export function TagInput({ tags, onChange, id, ariaLabel = 'Add tag', placeholder = 'Add a tag…' }: TagInputProps) {
  return (
    <ChipListInput
      values={tags}
      onChange={onChange}
      validate={validateTag}
      noun="tag"
      maxLength={200}
      chipClassName="rounded-full bg-[var(--color-accent)]/10 border border-[var(--color-accent)]/20 text-[var(--color-accent)] max-w-[160px]"
      id={id}
      ariaLabel={ariaLabel}
      placeholder={placeholder}
    />
  )
}
