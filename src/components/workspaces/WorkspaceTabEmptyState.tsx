import { motion } from 'framer-motion'
import type { Icon } from '@phosphor-icons/react'

interface WorkspaceTabEmptyStateProps {
  Icon: Icon
  title: string
  description: string
  /** Short list of capabilities the finished view will offer. */
  bullets?: string[]
}

/**
 * Tasteful "arriving this sprint" empty state for workspace tabs whose full
 * implementation lands in a later wave (Graph DAG, Team delegation graph).
 * Sovereign Deep styling — centered, spring-eased entrance, gold accent.
 *
 * Component boundary marker: the F2 wave replaces the tab body that renders
 * this with the real view. The route + tab stay; only the body swaps.
 */
export function WorkspaceTabEmptyState({
  Icon,
  title,
  description,
  bullets,
}: WorkspaceTabEmptyStateProps) {
  return (
    <div className="absolute inset-0 flex items-center justify-center overflow-y-auto p-6">
      <motion.div
        initial={{ opacity: 0, y: 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.45, ease: [0.34, 1.56, 0.64, 1] }}
        className="flex flex-col items-center text-center max-w-md"
      >
        <div className="relative mb-5">
          <div className="absolute inset-0 rounded-2xl bg-[var(--color-accent)]/10 blur-xl" />
          <div className="relative flex h-16 w-16 items-center justify-center rounded-2xl border border-[var(--color-accent)]/30 bg-[var(--color-surface-2)]">
            <Icon size={30} weight="duotone" className="text-[var(--color-accent)]" />
          </div>
        </div>

        <span className="mb-2 inline-flex items-center gap-1.5 rounded-full border border-[var(--color-accent)]/30 bg-[var(--color-accent)]/10 px-2.5 py-0.5 text-[10px] font-semibold uppercase tracking-widest text-[var(--color-accent)]">
          Coming this sprint
        </span>

        <h2 className="font-headline text-xl font-bold text-[var(--color-secondary)]">{title}</h2>
        <p className="mt-2 text-sm leading-relaxed text-[var(--color-muted)]">{description}</p>

        {bullets && bullets.length > 0 && (
          <ul className="mt-5 flex w-full flex-col gap-2 text-left">
            {bullets.map((b) => (
              <li
                key={b}
                className="flex items-start gap-2 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2 text-xs text-[var(--color-secondary)]"
              >
                <span className="mt-1.5 h-1.5 w-1.5 flex-shrink-0 rounded-full bg-[var(--color-accent)]" />
                <span>{b}</span>
              </li>
            ))}
          </ul>
        )}
      </motion.div>
    </div>
  )
}
