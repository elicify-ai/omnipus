import { Circle, Star } from '@phosphor-icons/react'
import { Badge } from '@/components/ui/badge'
import { IconRenderer } from '@/components/shared/IconRenderer'
import type { Agent } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { cn } from '@/lib/utils'

interface AgentCardProps {
  agent: Agent
  /** Override the click handler. When omitted, opens the edit slide-over. */
  onClick?: () => void
  /** Called when the user clicks "Set as default". Provided by the parent screen. */
  onSetDefault?: () => void
}

const typeBadgeVariant: Record<string, 'secondary' | 'outline' | 'default'> = {
  core: 'secondary',
  system: 'default',
}

function badgeVariantFor(type: string): 'secondary' | 'outline' | 'default' {
  return typeBadgeVariant[type] ?? 'outline'
}

export function AgentCard({ agent, onClick, onSetDefault }: AgentCardProps) {
  const openEditAgentSlideOver = useUiStore((s) => s.openEditAgentSlideOver)
  const handleOpen = onClick ?? (() => openEditAgentSlideOver(agent.id))

  return (
    <div className="relative group/card">
      <button
        type="button"
        data-testid={`agent-card-${agent.id}`}
        onClick={handleOpen}
        className={cn(
          'w-full text-left rounded-xl border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4',
          'hover:border-[var(--color-accent)]/40 hover:bg-[var(--color-surface-2)] transition-all duration-150',
          'focus-visible:outline-none'
        )}
        aria-label={`View agent ${agent.name}`}
      >
        <div className="flex items-start gap-3">
          {/* Avatar */}
          <div
            className="w-10 h-10 rounded-full flex items-center justify-center shrink-0 text-sm font-bold"
            style={{ backgroundColor: agent.color ?? 'var(--color-surface-3)' }}
          >
            {agent.icon ? (
              <IconRenderer icon={agent.icon} size={18} className="text-[var(--color-secondary)]" />
            ) : (
              <span className="text-[var(--color-secondary)]">
                {agent.name.charAt(0).toUpperCase()}
              </span>
            )}
          </div>

          {/* Info */}
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-2 mb-0.5 flex-wrap">
              <span className="font-headline font-bold text-sm text-[var(--color-secondary)] truncate">
                {agent.name}
              </span>
              {agent.status === 'active' && (
                <Circle size={7} weight="fill" className="text-[var(--color-success)] shrink-0" />
              )}
              {agent.default && (
                <>
                  <Star
                    size={11}
                    weight="fill"
                    className="text-[var(--color-accent)] shrink-0"
                    aria-label="Default agent"
                  />
                  <span className="text-[11px] font-medium uppercase tracking-wide text-[var(--color-accent)] shrink-0">
                    Default
                  </span>
                </>
              )}
            </div>
            <p className="text-sm text-[var(--color-muted)] line-clamp-2 mb-2">
              {agent.description || 'No description'}
            </p>
            <div className="flex items-center gap-2 flex-wrap">
              {agent.status === 'draft' ? (
                <Badge variant="warning" className="text-[var(--color-warning)] border-[var(--color-warning)]/30 bg-[var(--color-warning)]/10">draft</Badge>
              ) : agent.status === 'error' ? (
                <Badge variant="destructive" className="text-[var(--color-error)] border-[var(--color-error)]/30 bg-[var(--color-error)]/10">error</Badge>
              ) : (
                <Badge variant={badgeVariantFor(agent.type)} className="font-normal">
                  {agent.type}
                </Badge>
              )}
              {agent.model && (
                <span
                  className="text-xs font-mono text-[var(--color-muted)] truncate max-w-[140px]"
                  title={agent.model}
                >
                  {agent.model.includes('/') ? agent.model.split('/').slice(1).join('/') : agent.model}
                </span>
              )}
            </div>
            {agent.status === 'draft' && agent.type === 'Main' && (
              <p className="text-xs text-[var(--color-warning)]/70 mt-1">
                Set up SOUL.md to activate this agent
              </p>
            )}
            {agent.status === 'error' && (
              <p className="text-xs text-[var(--color-error)]/70 mt-1">
                Agent encountered an error — check the activity log
              </p>
            )}
          </div>
        </div>
      </button>

      {/* "Set as default" sits outside the card button to avoid nested-button HTML violation.
          Persistent (no group-hover gating — touch users would never see it) and sized for a
          44×44 tap target per WCAG 2.5.8 (token: --spacing-tap-target-min). */}
      {!agent.default && onSetDefault && (
        <button
          type="button"
          onClick={onSetDefault}
          className="absolute bottom-3 right-4 flex items-center justify-center gap-1 min-h-tap-target-min min-w-tap-target-min px-2 text-xs text-[var(--color-muted)] hover:text-[var(--color-accent)] transition-colors"
          aria-label={`Set ${agent.name} as default agent`}
        >
          <Star size={12} weight="fill" />
          Set as default
        </button>
      )}
    </div>
  )
}
