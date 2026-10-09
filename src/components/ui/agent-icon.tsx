import { useId } from 'react'
import { IconContext, type Icon } from '@phosphor-icons/react'

import { FIGURE_ART, type AgentIconColor, type AgentIconFigure, type AgentIconRole } from '@/design-system/agent-identity'
import { cn } from '@/lib/utils'

import { agentFigureInner, agentBadgePath } from '@/lib/agentIconArt'
import { REUSED_AGENT_BADGES } from '@/lib/agentBadgeIcons'

type AgentIconSize = 18 | 26 | 40 | 48
type AgentIconMotion = 'none' | 'thinking' | 'working' | 'waiting'

type AgentIconBase = {
  figure: AgentIconFigure
  role: AgentIconRole
  /** Palette ink. Fully opaque. Passed through as currentColor. */
  color: AgentIconColor
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

// The mark never inherits unrelated Phosphor context (colour/mirroring).
const BADGE_CONTEXT = {}

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
  const inkMarkup = agentFigureInner(art, maskId(baseId, 'i'))
  const glowMarkup = agentFigureInner(art, maskId(baseId, 'g'))
  const Badge = (REUSED_AGENT_BADGES as Partial<Record<AgentIconRole, Icon>>)[role]
  const badgePath = agentBadgePath(role)
  if (!Badge && !badgePath) throw new Error(`AgentIcon has no art for ${art}/${role}`)
  const badge = (
    <g data-role={role} transform="translate(140 140) scale(0.44)">
      {Badge ? (
        <IconContext.Provider value={BADGE_CONTEXT}>
          <Badge weight="bold" size={256} color="inherit" overflow="visible" />
        </IconContext.Provider>
      ) : <path d={badgePath} />}
    </g>
  )
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
          >
            <g dangerouslySetInnerHTML={{ __html: glowMarkup }} />
            {badge}
          </svg>
        </span>
      )}
      <span data-ink="" data-art={art} className={cn('relative', markClass)} style={{ opacity: 1 }}>
        <svg
          aria-hidden="true"
          width={size}
          height={size}
          viewBox="0 0 256 256"
          fill="currentColor"
        >
          <g dangerouslySetInnerHTML={{ __html: inkMarkup }} />
          {badge}
        </svg>
      </span>
      {animate && motion === 'working' && (
        <span aria-hidden="true" data-agent-icon-sheen="" className={sheenClass} />
      )}
    </span>
  )
}
