import type { ComponentProps, ReactNode } from 'react'
import { CaretDown } from '@phosphor-icons/react'

interface OutcomeDisclosureProps extends Omit<ComponentProps<'details'>, 'children'> {
  label: string
  icon?: ReactNode
  children: ReactNode
  detail?: ReactNode
}

/**
 * The domain disclosure shared by goal endings and helper events (parent-wake
 * spec §19–20). It deliberately keeps GoalOutcomeRow's native details/summary:
 * the catalogued DisclosureRow and Accordion use buttons, not these semantics.
 * No new public control, local toggle state, or focus treatment is introduced.
 */
export function OutcomeDisclosure({ label, icon, children, detail, ...props }: OutcomeDisclosureProps): ReactNode {
  return (
    <details
      {...props}
      className="group w-full min-w-0 max-w-2xl rounded-lg bg-[var(--color-surface-2)] px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-utility-xs-size)]"
    >
      <summary
        tabIndex={0}
        aria-label={label}
        className="flex min-h-[var(--target-pointer-minimum)] cursor-pointer list-none items-start gap-[var(--space-2)] pointer-coarse:min-h-[var(--target-touch-minimum)] [&::-webkit-details-marker]:hidden"
        onKeyDown={(event) => {
          if (event.key !== 'Enter' && event.key !== ' ') return
          // Use the native click activation for both keys, including WebKit.
          // Cancelling the key default avoids a second browser-generated click.
          event.preventDefault()
          if (!event.repeat) event.currentTarget.click()
        }}
      >
        {icon}
        <span className="min-w-0 flex-1">{children}</span>
        <CaretDown
          size={10}
          className="mt-[var(--space-1)] shrink-0 text-[var(--color-muted)] transition-transform group-open:rotate-180"
          aria-hidden="true"
        />
      </summary>
      {detail}
    </details>
  )
}
