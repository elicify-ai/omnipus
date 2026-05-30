import { Circle } from '@phosphor-icons/react'
import { useNavigate } from '@tanstack/react-router'
import { Badge } from '@/components/ui/badge'
import { IconRenderer } from '@/components/shared/IconRenderer'
import type { Agent } from '@/lib/api'
import { cn } from '@/lib/utils'

interface AgentCardProps {
  agent: Agent
}

const typeBadgeVariant = {
  core: 'secondary',
  custom: 'outline',
  system: 'default',
} as const

export function AgentCard({ agent }: AgentCardProps) {
  const navigate = useNavigate()

  return (
    <button
      type="button"
      data-testid={`agent-card-${agent.id}`}
      onClick={() => navigate({ to: '/agents/$agentId', params: { agentId: agent.id } })}
      className={cn(
        'group w-full text-left rounded-xl border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4',
        'hover:border-[var(--color-accent)]/40 hover:bg-[var(--color-surface-2)] transition-all duration-150',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)] focus-visible:ring-offset-2 focus-visible:ring-offset-[var(--color-primary)]'
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
          </div>
          <p className="text-xs text-[var(--color-muted)] line-clamp-2 mb-2">
            {agent.description || 'No description'}
          </p>
          <div className="flex items-center gap-2 flex-wrap">
            {agent.status === 'draft' ? (
              <Badge variant="warning" className="text-[var(--color-warning)] border-[var(--color-warning)]/30 bg-[var(--color-warning)]/10">draft</Badge>
            ) : agent.status === 'error' ? (
              <Badge variant="destructive" className="text-[var(--color-error)] border-[var(--color-error)]/30 bg-[var(--color-error)]/10">error</Badge>
            ) : (
              <Badge variant={typeBadgeVariant[agent.type]}>{agent.type}</Badge>
            )}
            {agent.model && (
              <span className="text-[10px] font-mono text-[var(--color-muted)] truncate max-w-[120px]">
                {agent.model}
              </span>
            )}
          </div>
          {agent.status === 'draft' && agent.type === 'custom' && (
            <p className="text-[10px] text-[var(--color-warning)]/70 mt-1">
              Set up SOUL.md to activate this agent
            </p>
          )}
          {agent.status === 'error' && (
            <p className="text-[10px] text-[var(--color-error)]/70 mt-1">
              Agent encountered an error — check the activity log
            </p>
          )}
        </div>
      </div>
    </button>
  )
}
