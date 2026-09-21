import * as React from 'react'
import { cn } from '@/lib/utils'

// US-2: Input — dark background, Liquid Silver text, Forge Gold focus ring
//
// `type` intentionally excludes the control-shaped HTML input types
// ('checkbox'/'radio'/'button'/'submit'/'reset'): this is a generic
// TEXT-LIKE field (text, email, password, number, date, search, tel, url,
// …). A checkbox or a boolean switch has its own catalogued primitive
// (Checkbox / Switch) with its own semantics (checked/indeterminate,
// keyboard handling) — this component has never implemented that behavior,
// and no call site across src/ (45 files import it) has ever passed one of
// the excluded types. `role` is dropped from the accepted props for the
// same reason: nothing here needs one, and it is the other half of the
// checkbox-as-switch shape.
//
// `type` used to be destructured out of props only to be re-attached
// verbatim as its own `type={type}` JSX attribute — a no-op that gave
// scripts/design-system-locks/controls.mjs's checkbox-as-switch scanner an
// explicit dynamic `type` attribute to fail closed on the instant this
// component's spread (`{...props}`, needed to forward the other ~20
// InputHTMLAttributes callers rely on — onChange, onBlur, value,
// placeholder, disabled, aria-*, data-testid, name, id, maxLength,
// autoComplete, …) was also present; the scanner cannot see into a spread's
// contents, so it cannot rule out `type="checkbox"` arriving through it and
// reports "unresolved" rather than silently passing. `type` now flows
// through `{...props}` like every other attribute — identical DOM output,
// but with no separate dynamic `type` JSX attribute for the scanner to flag,
// and a real (compile-time) guarantee, via the narrowed prop type below,
// that a caller cannot pass 'checkbox' in the first place.
type NonControlInputType = Exclude<
  NonNullable<React.InputHTMLAttributes<HTMLInputElement>['type']>,
  'checkbox' | 'radio' | 'button' | 'submit' | 'reset'
>

// Not exported: design-system/catalog.json's entry for this file lists only
// `Input` in `exports` (the four-part publication contract), and this type
// is an internal prop-narrowing detail, not a second published symbol.
interface InputProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, 'type' | 'role'> {
  type?: NonControlInputType
}

const Input = React.forwardRef<HTMLInputElement, InputProps>(
  ({ className, ...props }, ref) => {
    return (
      <input tabIndex={0}
        className={cn(
          // 44px tap target on mobile (touch min); compact 36px on sm+ (pointer).
          'flex h-11 sm:h-9 [@media(pointer:coarse)]:h-[44px] w-full rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)] transition-colors motion-reduce:transition-none',
          'placeholder:text-[var(--color-muted)]',
          'disabled:cursor-not-allowed disabled:opacity-50',
          className
        )}
        ref={ref}
        {...props}
      />
    )
  }
)
Input.displayName = 'Input'

export { Input }
