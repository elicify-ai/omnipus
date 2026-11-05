import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'
import type { AttentionMotion } from '@/components/layout/sidebar/attentionCue'
import '@/components/layout/sidebar/attention.css'

/**
 * The attention halo: the warning-coloured ring that loops (or stays still
 * under reduced motion) around an agent mark that is waiting for the person.
 * One implementation for the sidebar agent rows and the group chat's waiting
 * members (#1449 D15/ADR-20261028 C-I7: shared, never copied). The
 * `data-attention-halo` attribute is the test oracle; attention.css carries
 * the motion.
 */
export function AttentionHalo({
  halo,
  motion,
  children,
}: {
  halo: boolean
  motion: AttentionMotion
  children: ReactNode
}) {
  return (
    <span
      data-attention-halo={halo ? 'warning' : undefined}
      className={cn(
        'inline-flex shrink-0 rounded-full',
        halo && 'sidebar-attention-halo',
        halo && motion === 'loop' && 'sidebar-attention-halo-loop',
      )}
    >
      {children}
    </span>
  )
}
