import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Plus, Robot, ShareNetwork } from '@phosphor-icons/react'
import { AgentCard } from '@/components/agents/AgentCard'
import { WorkerCard } from '@/components/agents/WorkerCard'
import { CreateAgentModal } from '@/components/agents/CreateAgentModal'
import { Button } from '@/components/ui/button'
import { useUiStore } from '@/store/ui'
import { fetchAgents, updateAgent, isApiError } from '@/lib/api'

export function AgentListScreen() {
  const { openCreateAgentModal, addToast } = useUiStore()
  const queryClient = useQueryClient()

  const { data: agents = [], isLoading, isError, refetch } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
  })

  // Two-tier roster (locked v0.3 concept): base agents are chat colleagues;
  // sub-agent workers are delegation-only labour. Partition strictly on type.
  const baseAgents = agents.filter((a) => a.type !== 'worker')
  const workerAgents = agents.filter((a) => a.type === 'worker')

  const { mutate: doSetDefault } = useMutation({
    mutationFn: (agentId: string) => updateAgent(agentId, { default: true }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['agents'] })
      addToast({ message: 'Default agent updated', variant: 'success' })
    },
    onError: (err: unknown) =>
      addToast({
        message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to set default',
        variant: 'error',
      }),
  })

  return (
    <div className="absolute inset-0 overflow-y-auto pb-[env(safe-area-inset-bottom)]">
    <div className="max-w-4xl mx-auto px-4 py-6">
      {/* Header */}
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="font-headline text-2xl font-bold text-[var(--color-secondary)]">Agents</h1>
          <p className="text-sm text-[var(--color-muted)] mt-0.5">
            Browse, configure, and create your AI agents.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Link
            to="/agents/trust"
            className="inline-flex items-center gap-1.5 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2 text-xs font-medium text-[var(--color-secondary)] transition-colors hover:border-[var(--color-accent)]/40 hover:text-[var(--color-accent)]"
          >
            <ShareNetwork size={14} weight="bold" /> Delegation Graph
          </Link>
        </div>
      </div>

      {/* Content */}
      {isLoading ? (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {[1, 2, 3].map((i) => (
            <div
              key={i}
              className="h-32 rounded-xl border border-[var(--color-border)] bg-[var(--color-surface-1)] animate-pulse"
            />
          ))}
        </div>
      ) : isError ? (
        <div className="flex flex-col items-center justify-center py-16 gap-3">
          <p className="text-[var(--color-muted)] text-sm">Could not load agents.</p>
          <Button variant="outline" size="sm" onClick={() => refetch()}>
            Retry
          </Button>
        </div>
      ) : agents.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-16 gap-4 text-center">
          <Robot size={48} weight="thin" className="text-[var(--color-border)]" />
          <div>
            <p className="text-[var(--color-secondary)] font-medium text-sm">No agents yet</p>
            <p className="text-[var(--color-muted)] text-sm mt-1">
              Create your first agent to get started.
            </p>
          </div>
          <Button onClick={() => openCreateAgentModal('custom')} className="gap-2">
            <Plus size={14} weight="bold" /> New agent
          </Button>
        </div>
      ) : (
        <div className="space-y-8">
          {/* Base agents — chat colleagues (type !== 'worker'). Header + New
              button are always rendered so the affordance is reachable on a
              fresh install (when no agents exist yet). Empty section renders
              a brief empty-state message + the same New button. */}
          <section data-testid="base-agents-section">
            <div className="flex items-start justify-between gap-3 mb-3">
              <div>
                <h2 className="font-headline text-sm font-bold uppercase tracking-wide text-[var(--color-secondary)]">
                  Base agents
                </h2>
                <p className="text-xs text-[var(--color-muted)] mt-0.5">
                  Chat colleagues — message them, set a default, and delegate work.
                </p>
              </div>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => openCreateAgentModal('custom')}
                className="gap-1.5 shrink-0 text-[var(--color-muted)] hover:text-[var(--color-accent)]"
                data-testid="new-base-agent-button"
              >
                <Plus size={12} weight="bold" /> New agent
              </Button>
            </div>
            {baseAgents.length === 0 ? (
              <div
                className="rounded-lg border border-dashed border-[var(--color-border)] bg-[var(--color-surface-1)] px-4 py-5 text-center"
                data-testid="base-agents-empty"
              >
                <p className="text-sm text-[var(--color-muted)]">
                  No base agents yet.
                </p>
                <p className="text-sm text-[var(--color-muted)]/80 mt-1">
                  Create your first chat colleague to get started.
                </p>
              </div>
            ) : (
              <div
                className={`grid gap-4 ${
                  baseAgents.length < 4
                    ? 'grid-cols-1'
                    : 'grid-cols-1 sm:grid-cols-2 lg:grid-cols-3'
                }`}
              >
                {baseAgents.map((agent) => (
                  <AgentCard
                    key={agent.id}
                    agent={agent}
                    onSetDefault={() => doSetDefault(agent.id)}
                  />
                ))}
              </div>
            )}
          </section>

          {/* Sub-agent workers — delegation-only labour (type === 'worker').
              Same treatment: header + New button always rendered. Empty
              section renders a brief empty-state message + the same button. */}
          <section data-testid="worker-agents-section">
            <div className="flex items-start justify-between gap-3 mb-3">
              <div>
                <h2 className="font-headline text-sm font-bold uppercase tracking-wide text-[var(--color-secondary)]">
                  Sub-agent workers
                </h2>
                <p className="text-xs text-[var(--color-muted)] mt-0.5">
                  Delegation-only labour agents — invoked by other agents, not chat targets.
                </p>
              </div>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => openCreateAgentModal('worker')}
                className="gap-1.5 shrink-0 text-[var(--color-muted)] hover:text-[var(--color-accent)]"
                data-testid="new-worker-button"
              >
                <Plus size={12} weight="bold" /> New worker
              </Button>
            </div>
            {workerAgents.length === 0 ? (
              <div
                className="rounded-lg border border-dashed border-[var(--color-border)] bg-[var(--color-surface-1)] px-4 py-5 text-center"
                data-testid="worker-agents-empty"
              >
                <p className="text-sm text-[var(--color-muted)]">
                  No sub-agent workers yet.
                </p>
                <p className="text-sm text-[var(--color-muted)]/80 mt-1">
                  Create a worker to delegate labour to a third-party runtime.
                </p>
              </div>
            ) : (
              <div
                className={`grid gap-4 ${
                  workerAgents.length < 4
                    ? 'grid-cols-1'
                    : 'grid-cols-1 sm:grid-cols-2 lg:grid-cols-3'
                }`}
              >
                {workerAgents.map((agent) => (
                  <WorkerCard key={agent.id} agent={agent} />
                ))}
              </div>
            )}
          </section>
        </div>
      )}

      <CreateAgentModal />
    </div>
    </div>
  )
}
