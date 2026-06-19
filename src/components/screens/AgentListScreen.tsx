import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { CaretDown, Plus, Robot, ShareNetwork } from '@phosphor-icons/react'
import { AgentCard } from '@/components/agents/AgentCard'
import { WorkerCard } from '@/components/agents/WorkerCard'
import { CreateAgentModal } from '@/components/agents/CreateAgentModal'
import type { WizardCli } from '@/components/agents/wizard/types'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { useUiStore } from '@/store/ui'
import { fetchAgents, updateAgent, isApiError, isWorker } from '@/lib/api'

interface HostClis {
  hasClaude: boolean
  hasCodex: boolean
  hasOpencode: boolean
}

const OPTIMISTIC_HOST_CLIS: HostClis = {
  hasClaude: true,
  hasCodex: true,
  hasOpencode: true,
}

const CLI_LABELS: Record<WizardCli, string> = {
  'claude-code': 'claude-code',
  codex: 'codex',
  opencode: 'opencode',
}

const CLI_ORDER: readonly WizardCli[] = ['claude-code', 'codex', 'opencode'] as const

export function AgentListScreen() {
  const { openCreateAgentModal, addToast } = useUiStore()
  const queryClient = useQueryClient()

  const { data: agents = [], isLoading, isError, refetch } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
  })

  // Three-tier roster (W4 of agent-form-requirements): user-defined chat
  // colleagues (Main) on top, user-defined workers (Subagent + subagent_3p)
  // below, built-in roster (Mia / Jim / Ava / Ray, type=core) at the bottom.
  // Partition via isWorker() so the new wire enum values are classified
  // correctly without enumerating them here (see src/lib/api.ts:664-666).
  const baseAgents = agents.filter((a) => !isWorker(a))
  const workerAgents = agents.filter(isWorker)

  // Host-CLI detection — W4 of agent-form-requirements. We probe
  // `GET /api/v1/system/cli-detect` on mount and fall back to optimistic
  // defaults (all CLIs available) when the endpoint is missing or returns a
  // network error, so the disclosure still works in degraded modes (offline,
  // pre-onboarding, etc.). The roster's "Add Subagent (External)" picker
  // disables each CLI whose binary the gateway reports missing.
  const [hostClis, setHostClis] = useState<HostClis>(OPTIMISTIC_HOST_CLIS)
  const [externalMenuOpen, setExternalMenuOpen] = useState(false)
  useEffect(() => {
    // SSR-safe: only run in browser.
    if (typeof window === 'undefined') return
    let cancelled = false
    fetch('/api/v1/system/cli-detect')
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error('not ok'))))
      .then((d: HostClis) => {
        if (!cancelled) setHostClis(d)
      })
      .catch(() => {
        // Keep OPTIMISTIC_HOST_CLIS — endpoint missing or network error.
      })
    return () => {
      cancelled = true
    }
  }, [])

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

  // Per-CLI affordance config — drives both the disabled state and the
  // tooltip when a CLI is not installed on the host. Kept in one place so
  // the disclosure and the (future) inline fallback render the same state.
  const cliAvailable: Record<WizardCli, boolean> = {
    'claude-code': hostClis.hasClaude,
    codex: hostClis.hasCodex,
    opencode: hostClis.hasOpencode,
  }
  const cliTooltip: Record<WizardCli, string> = {
    'claude-code': 'Claude Code is not installed on this host',
    codex: 'Codex is not installed on this host',
    opencode: 'OpenCode is not installed on this host',
  }

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
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
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
          <Button onClick={() => openCreateAgentModal('Main')} className="gap-2">
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
                onClick={() => openCreateAgentModal('Main')}
                className="gap-1.5 shrink-0 text-[var(--color-muted)] hover:text-[var(--color-accent)]"
                data-testid="add-main-button"
              >
                <Plus size={12} weight="bold" /> + New Main
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
              // Grid progression: 1 col → 2 col (sm) → 3 col (lg). No xl/2xl
              // jump to 4 cols (avoids the "4th card sits alone at 1440 px"
              // regression on lg-only viewports). With 4 cards at lg the
              // layout is 3 + 1; the sm 2-col stage lets 2 + 2 sit
              // together on md. (W6-B2, I4+M3.)
              <div className="grid gap-4 grid-cols-1 sm:grid-cols-2 lg:grid-cols-3">
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
                onClick={() => openCreateAgentModal('Subagent')}
                className="gap-1.5 shrink-0 text-[var(--color-muted)] hover:text-[var(--color-accent)]"
                data-testid="add-subagent-button"
              >
                <Plus size={12} weight="bold" /> + New Subagent
              </Button>
              {/* W4 of agent-form-requirements: third +Add with CLI sub-options
                  (Subagent External). Single trigger opens a Radix Popover
                  listing the 3 supported CLIs (claude-code / codex / opencode).
                  Each sub-option passes its CLI choice through to the store
                  via `openCreateAgentModal('subagent_3p', cli)` so the wizard
                  pre-fills Step 1 + Step 3. CLIs not detected on host render
                  disabled with an explanatory tooltip. W4 spec §5.1, §13.2. */}
              <Popover open={externalMenuOpen} onOpenChange={setExternalMenuOpen}>
                <PopoverTrigger asChild>
                  <Button
                    size="sm"
                    variant="ghost"
                    className="gap-1.5 ml-0 sm:ml-2 shrink-0 text-[var(--color-muted)] hover:text-[var(--color-accent)]"
                    data-testid="add-external-trigger"
                    aria-haspopup="menu"
                    aria-expanded={externalMenuOpen}
                  >
                    <Plus size={12} weight="bold" /> + Add Subagent (External)
                    <CaretDown size={10} weight="bold" />
                  </Button>
                </PopoverTrigger>
                <PopoverContent
                  align="end"
                  sideOffset={6}
                  className="w-56 p-1"
                  data-testid="add-external-menu"
                >
                  <div role="menu" aria-label="Choose a third-party CLI">
                    {CLI_ORDER.map((cli) => {
                      const available = cliAvailable[cli]
                      return (
                        <button
                          key={cli}
                          type="button"
                          role="menuitem"
                          disabled={!available}
                          title={available ? undefined : cliTooltip[cli]}
                          onClick={() => {
                            openCreateAgentModal('subagent_3p', cli)
                            setExternalMenuOpen(false)
                          }}
                          data-testid={`add-external-${cli}`}
                          className="flex w-full items-center justify-between gap-2 rounded-sm px-2 py-1.5 text-left text-xs text-[var(--color-secondary)] transition-colors hover:bg-[var(--color-surface-2)] focus:bg-[var(--color-surface-2)] focus:outline-none disabled:cursor-not-allowed disabled:opacity-50 disabled:hover:bg-transparent"
                        >
                          <span className="font-mono">{CLI_LABELS[cli]}</span>
                          {!available && (
                            <span className="text-[10px] uppercase tracking-wide text-[var(--color-muted)]">
                              not installed
                            </span>
                          )}
                        </button>
                      )
                    })}
                  </div>
                </PopoverContent>
              </Popover>
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
              // Same 1→2→3 progression as the base grid (W6-B2, I4+M3).
              <div className="grid gap-4 grid-cols-1 sm:grid-cols-2 lg:grid-cols-3">
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
