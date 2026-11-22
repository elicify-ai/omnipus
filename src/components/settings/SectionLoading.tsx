// SectionLoading — the one loading state of a Settings section (Settings
// redesign #1519, FR-065): "Loading <section>…" with a spinner and Skeleton
// cards until the section's reads answer. The spinner and the skeleton pulse
// stop under reduced motion (their catalogued components handle that).
import { Skeleton } from '@/components/ui/skeleton'
import { Spinner } from '@/components/ui/spinner'

export interface SectionLoadingProps { // not-wire-format: React props
  /** The section's visible name, as in the section list. */
  label: string
  /** How many Skeleton cards stand in for the section's rows. */
  cards?: number
  'data-testid'?: string
}

export function SectionLoading({ label, cards = 3, 'data-testid': testId }: SectionLoadingProps) {
  return (
    <div className="space-y-[var(--space-2)]" aria-busy="true" data-testid={testId}>
      <div role="status" className="flex items-center gap-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
        <Spinner decorative />
        {`Loading ${label}…`}
      </div>
      {Array.from({ length: cards }, (_, i) => (
        <Skeleton key={i} className="h-[var(--space-8)] w-full" />
      ))}
    </div>
  )
}
