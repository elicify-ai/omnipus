import { useId } from 'react'

import type { AgentColor, AgentFigure, AgentRole } from '@/lib/api/generated/openapi-types'
import { FIGURE_ART } from '@/lib/agentIdentity'
import { cn } from '@/lib/utils'

import { agentIconInner } from './agent-icon-art'

type AgentIconSize = 18 | 26 | 40 | 48
type AgentIconMotion = 'none' | 'thinking' | 'working' | 'waiting'

type AgentIconBase = {
  figure: AgentFigure
  role: AgentRole
  /** Palette ink. Fully opaque. Passed through as currentColor. */
  color: AgentColor
  size: AgentIconSize
  /** Default `none`. The icon does not own the state phrase. */
  motion?: AgentIconMotion
  /**
   * When omitted, follow `prefers-reduced-motion`.
   * `true` forces the still mark. `false` forces motion (tests).
   */
  reducedMotion?: boolean
}

export type AgentIconProps = AgentIconBase & (
  | { decorative?: true; name?: string }
  | { decorative: false; name: string }
)

const MARK_CLASS = {
  none: '',
  working: 'origin-center animate-[agent-icon-pump_1.6s_ease-in-out_infinite] motion-reduce:animate-none',
  thinking: 'origin-center animate-[agent-icon-breathe_2.6s_ease-in-out_infinite] motion-reduce:animate-none',
  waiting: 'origin-center animate-[agent-icon-pop_3.4s_ease-in-out_infinite] motion-reduce:animate-none',
} as const

const MARK_CLASS_FORCED = {
  none: '',
  working: 'origin-center animate-[agent-icon-pump_1.6s_ease-in-out_infinite]',
  thinking: 'origin-center animate-[agent-icon-breathe_2.6s_ease-in-out_infinite]',
  waiting: 'origin-center animate-[agent-icon-pop_3.4s_ease-in-out_infinite]',
} as const

const GLOW_CLASS = {
  none: '',
  working: 'animate-[agent-icon-glow_1.6s_ease-in-out_infinite] motion-reduce:animate-none',
  thinking: 'animate-[agent-icon-think-glow_2.6s_ease-in-out_infinite] motion-reduce:animate-none',
  waiting: 'animate-[agent-icon-flash_3.4s_ease-in-out_infinite] motion-reduce:animate-none',
} as const

const GLOW_CLASS_FORCED = {
  none: '',
  working: 'animate-[agent-icon-glow_1.6s_ease-in-out_infinite]',
  thinking: 'animate-[agent-icon-think-glow_2.6s_ease-in-out_infinite]',
  waiting: 'animate-[agent-icon-flash_3.4s_ease-in-out_infinite]',
} as const

const SHEEN_CLASS = 'agent-icon-sheen pointer-events-none absolute inset-y-0 left-0 w-1/3 animate-[agent-icon-sheen_2.4s_ease-in-out_infinite] motion-reduce:animate-none'
const SHEEN_CLASS_FORCED = 'agent-icon-sheen pointer-events-none absolute inset-y-0 left-0 w-1/3 animate-[agent-icon-sheen_2.4s_ease-in-out_infinite]'

function withBadgeRole(markup: string, role: AgentRole) {
  const needle = '<g transform="translate('
  const index = markup.lastIndexOf(needle)
  if (index < 0) return markup
  return `${markup.slice(0, index)}<g data-role="${role}" transform="translate(${markup.slice(index + needle.length)}`
}

function maskId(raw: string, suffix: string) {
  const safe = raw.replace(/[^a-zA-Z0-9_-]/g, '')
  return `ai${safe}${suffix}`
}

/**
 * AgentIcon draws the supplied figure, role badge, and ink colour.
 * It does not fetch an agent, read a store, or map an old hex.
 */
export function AgentIcon(props: AgentIconProps) {
  const { figure, role, color, size, motion = 'none', reducedMotion } = props
  const decorative = props.decorative !== false
  const name = props.name
  if (!decorative && !name) {
    throw new Error('AgentIcon requires name when decorative is false')
  }

  const baseId = useId()
  const art = FIGURE_ART[figure].art
  const inkMarkup = withBadgeRole(agentIconInner(art, role, maskId(baseId, 'i')), role)
  const glowMarkup = withBadgeRole(agentIconInner(art, role, maskId(baseId, 'g')), role)
  const animate = reducedMotion !== true && motion !== 'none'
  const markClass = !animate ? '' : reducedMotion === false ? MARK_CLASS_FORCED[motion] : MARK_CLASS[motion]
  const glowClass = !animate ? '' : reducedMotion === false ? GLOW_CLASS_FORCED[motion] : GLOW_CLASS[motion]
  const sheenClass = reducedMotion === false ? SHEEN_CLASS_FORCED : SHEEN_CLASS

  return (
    <span
      data-testid="agent-icon"
      data-figure={figure}
      data-size={size}
      data-motion={motion}
      className="relative inline-flex shrink-0 forced-colors:!text-[CanvasText]"
      style={{ color }}
      {...(decorative
        ? { 'aria-hidden': true as const }
        : { role: 'img' as const, 'aria-label': name })}
    >
      {animate && (
        <span data-glow="" data-art={art} className={cn('pointer-events-none absolute inset-0', glowClass)}>
          <svg
            aria-hidden="true"
            width={size}
            height={size}
            viewBox="0 0 256 256"
            fill="currentColor"
            dangerouslySetInnerHTML={{ __html: glowMarkup }}
          />
        </span>
      )}
      <span data-ink="" data-art={art} className={cn('relative', markClass)} style={{ opacity: 1 }}>
        <svg
          aria-hidden="true"
          width={size}
          height={size}
          viewBox="0 0 256 256"
          fill="currentColor"
          dangerouslySetInnerHTML={{ __html: inkMarkup }}
        />
      </span>
      {animate && motion === 'working' && (
        <span aria-hidden="true" data-agent-icon-sheen="" className={sheenClass} />
      )}
    </span>
  )
}
