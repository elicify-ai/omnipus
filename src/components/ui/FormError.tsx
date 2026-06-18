// FormError — shared form-field error renderer
//
// Wave 6 / A3 / C7 (WCAG 3.3.1, 3.3.3, 4.1.3): a single source of truth for
// field-level error messages. The contract is intentionally narrow so that
// W6-B / W6-C consumers (B1 slide-over, B3 icon-case, B4 identity-section,
// C1 config-levers) can wire it without diverging on semantics.
//
// Contract:
//   - When `error` is a non-empty string, render the message inside a
//     `role="alert"` region. Screen readers announce role=alert content
//     when it is inserted into the accessibility tree, so the user hears
//     the error without having to move focus.
//   - When `error` is null/empty, render nothing (no empty <p> tags clutter
//     the DOM and no role=alert region is announced silently).
//   - `id` is exposed so callers can wire `aria-describedby` on the
//     associated input (the standard ARIA pattern for field errors).
//   - The styling reuses the existing --color-error token so the look is
//     consistent with the in-place name/executor error rows in
//     CreateAgentModal today.
//
// Usage:
//   <label htmlFor="x">Name</label>
//   <Input id="x" aria-describedby={error ? nameErrorId : undefined} aria-invalid={!!error} required />
//   <FormError id={nameErrorId} error={error} />

import { cn } from '@/lib/utils'

export interface FormErrorProps {
  /**
   * The error message to announce. When falsy (null, undefined, or empty
   * string), the component renders nothing — no role=alert region, no DOM
   * node. Pass `null` for "no error" and a non-empty string for "show".
   */
  error: string | null | undefined
  /**
   * Optional element id. Expose it on the associated input via
   * `aria-describedby` so screen readers link the field to its error.
   */
  id?: string
  /**
   * Optional className for layout placement (margin, alignment). The
   * default text size and color are baked in.
   */
  className?: string
}

export function FormError({ error, id, className }: FormErrorProps) {
  if (!error) return null
  return (
    <p
      id={id}
      role="alert"
      aria-live="polite"
      className={cn('mt-1 text-xs text-[var(--color-error)]', className)}
    >
      {error}
    </p>
  )
}
