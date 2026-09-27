import { useNavigate } from '@tanstack/react-router'
import { Circle, Lightning } from '@phosphor-icons/react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { IconRenderer } from '@/components/shared/IconRenderer'
import type { Agent, ExecutorConfig } from '@/lib/api'
import { cn } from '@/lib/utils'

interface WorkerCardProps {
  agent: Agent
}

// Sub-agent worker card. Workers are delegation-only labour agents — "a tool you
// point at work, not a colleague". They differ from base AgentCards:
//   SHOW : executor/runner badge.
//   OMIT : chat/open-conversation entry, heartbeat indicator, default-★ control,
//          and any "Test run" control (removed, issue #915 — for an external
//          worker the runner check now runs only automatically, before its
//          profile saves; the profile has no manual button for it).
// Clicking the card still navigates to the agent profile (workers have a detail
// page); the card simply never surfaces the colleague affordances.

// effectiveKind normalises an absent executor to its default ("native"),
// matching ExecutorSelector's behaviour.
function effectiveKind(value: ExecutorConfig | undefined): ExecutorConfig['kind'] {
  return value?.kind ?? 'native'
}

// Human label for the executor badge. external-cli resolves to the specific CLI.
function executorLabel(executor: ExecutorConfig | undefined): string {
  const kind = effectiveKind(executor)
  if (kind === 'native') return 'Native'
  if (kind === 'remote-a2a') return 'Remote (A2A)'
  // external-cli — name the runner.
  switch (executor?.cli) {
    case 'claude-code':
      return 'Claude Code'
    case 'codex':
      return 'Codex'
    case 'opencode':
      return 'opencode'
    default:
      return 'External CLI'
  }
}

export function WorkerCard({ agent }: WorkerCardProps) {
  const navigate = useNavigate()
  const kind = effectiveKind(agent.executor)
  const isExternalCli = kind === 'external-cli'

  return (
    // Wrapper kept as the grid cell: AgentListScreen.test.tsx anchors on `.closest('div.relative')`.
    <div className="relative group/card">
      <Button
        type="button"
        variant="ghost"
        data-testid={`worker-card-${agent.id}`}
        onClick={() => navigate({ to: '/agents/$agentId', params: { agentId: agent.id } })}
        className={cn(
          'block h-auto w-full whitespace-normal rounded-xl border border-[var(--color-border)] bg-[var(--color-surface-1)] p-[var(--space-3)] text-left',
          'hover:border-[var(--color-accent)]/40 hover:bg-[var(--color-surface-2)] transition-all duration-150',
          'focus-visible:border-[var(--color-accent)]'
        )}
        aria-label={`View worker ${agent.name}`}
      >
        <div className="flex items-start gap-[var(--space-2-5)]">
          {/* Avatar */}
          <div
            className="w-10 h-10 rounded-full flex items-center justify-center shrink-0 text-[length:var(--type-body-compact-size)] font-bold"
            style={{ backgroundColor: agent.color ?? 'var(--color-surface-3)' }}
          >
            {agent.icon ? (
              <IconRenderer icon={agent.icon} size={18} className="text-[var(--color-secondary)]" />
            ) : (
              <Lightning size={18} weight="fill" className="text-[var(--color-secondary)]" />
            )}
          </div>

          {/* Info */}
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-[var(--space-2)] mb-[var(--space-0-5)] flex-wrap">
              <span className="font-headline font-bold text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)] truncate">
                {agent.name}
              </span>
              {/* Live turn status, separate from the workspace heartbeat feature. */}
              {agent.status === 'active' && (
                <span className="inline-flex items-center gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)] font-medium text-[var(--color-success)]">
                  <Circle size={7} weight="fill" aria-hidden="true" />
                  Running
                </span>
              )}
              {/* NB: no heartbeat indicator and no default-★ — workers never have them. */}
            </div>
            <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)] line-clamp-2 mb-[var(--space-2)]">
              {agent.description || 'No description'}
            </p>
            <div className="flex items-center gap-[var(--space-2)] flex-wrap">
              <Badge variant="secondary">Worker</Badge>
              <Badge
                variant="outline"
                className="gap-[var(--space-1)]"
                title={
                  isExternalCli
                    ? `External CLI runner: ${executorLabel(agent.executor)}`
                    : `Runtime: ${executorLabel(agent.executor)}`
                }
              >
                <Lightning size={12} weight="fill" className="text-[var(--color-accent)]" />
                {executorLabel(agent.executor)}
              </Badge>
              {agent.model && (
                // min-w-0 + shrink lets a long model slug give way in the
                // flex row and truncate instead of overflowing the card.
                <span
                  className="text-[length:var(--type-utility-xs-size)] font-mono text-[var(--color-muted)] truncate min-w-0 shrink"
                  title={agent.model}
                >
                  {agent.model.includes('/') ? agent.model.split('/').slice(1).join('/') : agent.model}
                </span>
              )}
            </div>
          </div>
        </div>
      </Button>
    </div>
  )
}
