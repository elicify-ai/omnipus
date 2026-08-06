import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useEffect, useRef, useState } from 'react'
import {
  CaretDown,
  FunnelSimple,
  GitFork,
  Plus,
  Robot,
  Users,
  WarningCircle,
} from '@phosphor-icons/react'
import { AgentCard } from '@/components/agents/AgentCard'
import { WorkerCard } from '@/components/agents/WorkerCard'
import { CreateAgentModal } from '@/components/agents/CreateAgentModal'
import type { WizardCli } from '@/components/agents/wizard/types'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Accordion, AccordionItem, AccordionTrigger, AccordionContent } from '@/components/ui/accordion'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { useUiStore } from '@/store/ui'
import { fetchAgents, fetchWorkspaces, updateAgent, fetchCliDetect, isApiError, isWorker } from '@/lib/api'
import type { Agent, Workspace, CliDetect } from '@/lib/api'
import { useAuthStore } from '@/store/auth'
import { ScreenHeader } from '@/components/layout/ScreenHeader'

// CliDetect (external-executor-cli-path-detection spec, FR-001/FR-011) is a
// per-CLI object — `{ claude, codex, opencode }: CliDetectEntry` — each entry
// carrying `{ installed, path, source }`. WizardCli spells "claude-code" while
// the wire key is "claude"; the mapping is inlined below in `cliAvailable`.
const OPTIMISTIC_HOST_CLIS: CliDetect = {
  claude: { installed: true, path: null, source: null },
  codex: { installed: true, path: null, source: null },
  opencode: { installed: true, path: null, source: null },
}

const CLI_LABELS: Record<WizardCli, string> = {
  'claude-code': 'claude-code',
  codex: 'codex',
  opencode: 'opencode',
}

const CLI_ORDER: readonly WizardCli[] = ['claude-code', 'codex', 'opencode'] as const

// ── Workspace Teams view ──────────────────────────────────────────────────────

interface WorkspaceTeamsViewProps {
  workspaces: Workspace[]
}

function WorkspaceTeamsView({ workspaces }: WorkspaceTeamsViewProps) {
  if (workspaces.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-16 gap-4 text-center">
        <GitFork size={48} weight="thin" className="text-[var(--color-border)]" />
        <div>
          <p className="text-[var(--color-secondary)] font-medium text-sm">No workspaces yet</p>
          <p className="text-[var(--color-muted)] text-sm mt-1">
            Create a workspace to configure team delegation graphs.
          </p>
        </div>
        <Link
          to="/workspaces"
          tabIndex={0}
          className="inline-flex items-center gap-1.5 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2 text-xs font-medium text-[var(--color-secondary)] transition-colors hover:border-[var(--color-accent)]/40 hover:text-[var(--color-accent)]"
        >
          <GitFork size={14} weight="bold" /> Go to Workspaces
        </Link>
      </div>
    )
  }

  return (
    <div className="space-y-2">
      <p className="text-xs text-[var(--color-muted)] mb-4">
        Each workspace has a team delegation graph. Click a workspace to configure its team.
      </p>
      {workspaces.map((ws) => (
        <Link
          key={ws.id}
          to="/workspaces/$workspaceId/team"
          params={{ workspaceId: ws.id }}
          tabIndex={0}
          data-testid={`workspace-team-row-${ws.id}`}
          className="flex items-center justify-between gap-3 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] px-4 py-3 transition-colors hover:border-[var(--color-accent)]/40 hover:bg-[var(--color-surface-2)]"
        >
          <div className="flex items-center gap-3 min-w-0">
            <div
              className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md"
              style={{ backgroundColor: 'color-mix(in srgb, var(--color-accent) 15%, transparent)' }}
            >
              <Users size={16} style={{ color: 'var(--color-accent)' }} />
            </div>
            <div className="min-w-0">
              <p className="text-sm font-medium text-[var(--color-secondary)] truncate">
                {ws.name}
              </p>
              {ws.description && (
                <p className="text-xs text-[var(--color-muted)] truncate mt-0.5">
                  {ws.description}
                </p>
              )}
            </div>
          </div>
          <div className="flex items-center gap-3 shrink-0">
            {(ws.core_team ?? []).length > 0 && (
              <span className="text-xs text-[var(--color-muted)]">
                {ws.core_team!.length} agent{ws.core_team!.length !== 1 ? 's' : ''}
              </span>
            )}
            <GitFork size={14} className="text-[var(--color-muted)]" />
          </div>
        </Link>
      ))}
    </div>
  )
}

// ── Agents Library view ───────────────────────────────────────────────────────

interface AgentsLibraryViewProps {
  agents: Agent[]
  workspaces: Workspace[]
  onSetDefault: (agent: Agent) => void
  openCreateAgentModal: (type: 'Main' | 'Subagent' | 'subagent_3p', cli?: WizardCli) => void
  hostClis: CliDetect
  cliDetectFailed: boolean
  externalMenuOpen: boolean
  setExternalMenuOpen: (open: boolean) => void
  // Wave-3 hotfix: always a defined string (never `undefined`) — the Radix
  // Accordion primitive infers controlled-vs-uncontrolled from whether
  // `value` is `undefined` on the FIRST render; passing `undefined` then
  // later a real string flips it from uncontrolled to controlled mid-life
  // and triggers React's "changing from uncontrolled to controlled"
  // warning. `''` is the defined "nothing open" sentinel instead.
  builtInOpen: string
  setBuiltInOpen: (v: string) => void
}

function AgentsLibraryView({
  agents,
  workspaces,
  onSetDefault,
  openCreateAgentModal,
  hostClis,
  cliDetectFailed,
  externalMenuOpen,
  setExternalMenuOpen,
  builtInOpen,
  setBuiltInOpen,
}: AgentsLibraryViewProps) {
  const [workspaceFilter, setWorkspaceFilter] = useState<string>('all')
  const [filterMenuOpen, setFilterMenuOpen] = useState(false)
  // System section disclosure — collapsed by default (unlike the Built-in
  // roster's load-dependent adaptive expand, this niche admin section has no
  // "roster is otherwise empty" case to auto-open for). Wave-3 hotfix:
  // `''` (not `undefined`) so the Accordion below is controlled from its
  // very first render — see `builtInOpen`'s doc comment on the prop type.
  const [systemOpen, setSystemOpen] = useState<string>('')

  // Resolve the workspace object for the current filter (for label display).
  const activeWorkspace = workspaces.find((ws) => ws.id === workspaceFilter)

  // Filter agents by workspace membership when a filter is active.
  const filteredAgents =
    workspaceFilter === 'all'
      ? agents
      : agents.filter((agent) => {
          const ws = workspaces.find((w) => w.id === workspaceFilter)
          return ws?.core_team?.includes(agent.id) ?? false
        })

  // ADR-049 D3/SD-C16: type:system (the locked Judge) is neither a Main chat
  // colleague nor part of the Built-in core roster — it gets its own locked
  // section below, and is excluded here so it never silently renders as a
  // Main agent.
  const mainAgents = filteredAgents.filter((a) => !isWorker(a) && !(a.type === 'core' && a.locked) && a.type !== 'system')
  const workerAgents = filteredAgents.filter(isWorker)
  const builtInAgents = filteredAgents.filter((a) => a.type === 'core' && a.locked)
  const systemAgents = filteredAgents.filter((a) => a.type === 'system')

  const cliAvailable: Record<WizardCli, boolean> = {
    'claude-code': hostClis.claude.installed,
    codex: hostClis.codex.installed,
    opencode: hostClis.opencode.installed,
  }
  const cliTooltip: Record<WizardCli, string> = {
    'claude-code': 'Claude Code is not installed on this host',
    codex: 'Codex is not installed on this host',
    opencode: 'OpenCode is not installed on this host',
  }

  return (
    <div className="space-y-6">
      {/* Filter bar */}
      {workspaces.length > 0 && (
        <div className="flex items-center gap-2">
          <Popover open={filterMenuOpen} onOpenChange={setFilterMenuOpen}>
            <PopoverTrigger asChild>
              <button tabIndex={0}
                type="button"
                data-testid="workspace-filter-trigger"
                className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-xs font-medium transition-colors"
                style={{
                  borderColor:
                    workspaceFilter !== 'all'
                      ? 'var(--color-accent)'
                      : 'var(--color-border)',
                  backgroundColor: 'var(--color-surface-1)',
                  color:
                    workspaceFilter !== 'all'
                      ? 'var(--color-accent)'
                      : 'var(--color-muted)',
                }}
                aria-haspopup="dialog"
                aria-expanded={filterMenuOpen}
              >
                <FunnelSimple size={12} weight={workspaceFilter !== 'all' ? 'fill' : 'regular'} />
                {workspaceFilter === 'all'
                  ? 'Filter by workspace'
                  : (activeWorkspace?.name ?? workspaceFilter)}
                <CaretDown size={10} />
              </button>
            </PopoverTrigger>
            <PopoverContent align="start" sideOffset={4} className="w-56 p-1">
              <div role="group" aria-label="Filter by workspace">
                <button tabIndex={0}
                  type="button"
                  data-testid="workspace-filter-all"
                  onClick={() => {
                    setWorkspaceFilter('all')
                    setFilterMenuOpen(false)
                  }}
                  aria-current={workspaceFilter === 'all' ? 'true' : undefined}
                  className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-xs transition-colors hover:bg-[var(--color-surface-2)] focus:bg-[var(--color-surface-2)]"
                  style={{
                    color:
                      workspaceFilter === 'all'
                        ? 'var(--color-accent)'
                        : 'var(--color-secondary)',
                    fontWeight: workspaceFilter === 'all' ? 600 : 400,
                  }}
                >
                  All agents
                </button>
                {workspaces.map((ws) => (
                  <button tabIndex={0}
                    key={ws.id}
                    type="button"
                    data-testid={`workspace-filter-${ws.id}`}
                    onClick={() => {
                      setWorkspaceFilter(ws.id)
                      setFilterMenuOpen(false)
                    }}
                    aria-current={workspaceFilter === ws.id ? 'true' : undefined}
                    className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-xs transition-colors hover:bg-[var(--color-surface-2)] focus:bg-[var(--color-surface-2)]"
                    style={{
                      color:
                        workspaceFilter === ws.id
                          ? 'var(--color-accent)'
                          : 'var(--color-secondary)',
                      fontWeight: workspaceFilter === ws.id ? 600 : 400,
                    }}
                  >
                    <Users size={12} />
                    {ws.name}
                  </button>
                ))}
              </div>
            </PopoverContent>
          </Popover>
          {workspaceFilter !== 'all' && (
            <button tabIndex={0}
              type="button"
              data-testid="workspace-filter-clear"
              onClick={() => setWorkspaceFilter('all')}
              className="inline-flex items-center gap-1 rounded px-2 py-1 text-[10px] font-medium transition-colors"
              style={{ color: 'var(--color-muted)' }}
            >
              Clear filter
            </button>
          )}
        </div>
      )}

      {/* Roster sections */}
      {agents.length === 0 ? (
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
          {/* Built-in roster — rendered FIRST (Agents-screen IA fix): the
              locked Mia/Jim/Ava/Ray roster is 100% of a fresh install (Main
              agents and Sub-agent workers are both empty until the operator
              creates a custom one), so it must be the thing a first-time
              visitor sees immediately, not a collapsed section they have to
              scroll past two empty-state cards to reach. Two independent UAT
              testers stopped at the (empty) Main-agents / worker cards above
              the old bottom position and concluded there was no built-in
              roster at all. */}
          {builtInAgents.length > 0 && (
            <section data-testid="built-in-agents-section">
              <Accordion
                type="single"
                collapsible
                value={builtInOpen}
                onValueChange={setBuiltInOpen}
              >
                <AccordionItem value="built-in">
                  <AccordionTrigger data-testid="built-in-agents-trigger">
                    <div className="text-left">
                      <h2 className="font-headline text-sm font-bold uppercase tracking-wide text-[var(--color-secondary)]">
                        Built-in roster
                      </h2>
                      <p className="text-xs text-[var(--color-muted)] mt-0.5">
                        Core agents — Mia, Jim, Ava, Ray and any other locked system roster.
                      </p>
                    </div>
                  </AccordionTrigger>
                  <AccordionContent>
                    <div className="grid gap-4 grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 pt-2">
                      {builtInAgents.map((agent) => (
                        <AgentCard
                          key={agent.id}
                          agent={agent}
                          onSetDefault={() => onSetDefault(agent)}
                        />
                      ))}
                    </div>
                  </AccordionContent>
                </AccordionItem>
              </Accordion>
            </section>
          )}

          {/* Main agents */}
          <section data-testid="base-agents-section">
            <div className="flex items-start justify-between gap-3 mb-3">
              <div>
                <h2 className="font-headline text-sm font-bold uppercase tracking-wide text-[var(--color-secondary)]">
                  Main agents
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
            {mainAgents.length === 0 ? (
              <div
                className="rounded-lg border border-dashed border-[var(--color-border)] bg-[var(--color-surface-1)] px-4 py-5 text-center"
                data-testid="base-agents-empty"
              >
                <p className="text-sm text-[var(--color-muted)]">
                  {workspaceFilter !== 'all'
                    ? 'No Main agents on this workspace team.'
                    : 'No custom Main agents yet. Create one, or use a built-in above.'}
                </p>
              </div>
            ) : (
              <div className="grid gap-4 grid-cols-1 sm:grid-cols-2 lg:grid-cols-3">
                {mainAgents.map((agent) => (
                  <AgentCard
                    key={agent.id}
                    agent={agent}
                    onSetDefault={() => onSetDefault(agent)}
                  />
                ))}
              </div>
            )}
          </section>

          {/* Sub-agent workers */}
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
              <div className="flex items-center gap-1">
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => openCreateAgentModal('Subagent')}
                  className="gap-1.5 shrink-0 text-[var(--color-muted)] hover:text-[var(--color-accent)]"
                  data-testid="add-subagent-button"
                >
                  <Plus size={12} weight="bold" /> + New Subagent
                </Button>
                {/* W4 of agent-form-requirements: third +Add with CLI sub-options. */}
                <Popover open={externalMenuOpen} onOpenChange={setExternalMenuOpen}>
                  <PopoverTrigger asChild>
                    <Button
                      size="sm"
                      variant="ghost"
                      className="gap-1.5 ml-0 sm:ml-2 shrink-0 text-[var(--color-muted)] hover:text-[var(--color-accent)]"
                      data-testid="add-external-trigger"
                      aria-haspopup="dialog"
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
                    <div role="group" aria-label="External CLI type">
                      {CLI_ORDER.map((cli) => {
                        const available = cliAvailable[cli]
                        return (
                          <button tabIndex={0}
                            key={cli}
                            type="button"
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
            </div>

            {cliDetectFailed && (
              <div
                className="flex items-start gap-2 rounded-lg border px-3 py-2 mb-3"
                style={{
                  borderColor: 'color-mix(in srgb, var(--color-warning) 30%, transparent)',
                  backgroundColor: 'color-mix(in srgb, var(--color-warning) 10%, transparent)',
                }}
                data-testid="cli-detect-warning"
              >
                <WarningCircle size={16} weight="bold" className="mt-0.5 shrink-0" style={{ color: 'var(--color-warning)' }} />
                <p className="text-xs" style={{ color: 'var(--color-warning)' }}>
                  Could not detect installed external CLIs. External subagent availability is assumed by default.
                </p>
              </div>
            )}

            {workerAgents.length === 0 ? (
              <div
                className="rounded-lg border border-dashed border-[var(--color-border)] bg-[var(--color-surface-1)] px-4 py-5 text-center"
                data-testid="worker-agents-empty"
              >
                <p className="text-sm text-[var(--color-muted)]">
                  {workspaceFilter !== 'all'
                    ? 'No sub-agent workers on this workspace team.'
                    : 'No sub-agent workers yet.'}
                </p>
                {workspaceFilter === 'all' && (
                  <p className="text-sm text-[var(--color-muted)]/80 mt-1">
                    Create a worker to delegate labour to a third-party runtime.
                  </p>
                )}
              </div>
            ) : (
              <div className="grid gap-4 grid-cols-1 sm:grid-cols-2 lg:grid-cols-3">
                {workerAgents.map((agent) => (
                  <WorkerCard key={agent.id} agent={agent} />
                ))}
              </div>
            )}
          </section>

          {/* Built-in roster */}
          {builtInAgents.length > 0 && (
            <section data-testid="built-in-agents-section">
              <Accordion
                type="single"
                collapsible
                value={builtInOpen}
                onValueChange={setBuiltInOpen}
              >
                <AccordionItem value="built-in">
                  <AccordionTrigger data-testid="built-in-agents-trigger">
                    <div className="text-left">
                      <h2 className="font-headline text-sm font-bold uppercase tracking-wide text-[var(--color-secondary)]">
                        Built-in roster
                      </h2>
                      <p className="text-xs text-[var(--color-muted)] mt-0.5">
                        Core agents — Mia, Jim, Ava, Ray and any other locked system roster.
                      </p>
                    </div>
                  </AccordionTrigger>
                  <AccordionContent>
                    <div className="grid gap-4 grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 pt-2">
                      {builtInAgents.map((agent) => (
                        <AgentCard
                          key={agent.id}
                          agent={agent}
                          onSetDefault={() => onSetDefault(agent)}
                        />
                      ))}
                    </div>
                  </AccordionContent>
                </AccordionItem>
              </Accordion>
            </section>
          )}

          {/* System agents — ADR-049 D3/FR-095/SD-C16: a locked, non-chat,
              non-delegable roster (the seeded Judge). Cloned from the
              Built-in accordion above, minus onSetDefault (mirrors
              WorkerCard — System agents are never ★-eligible). */}
          {systemAgents.length > 0 && (
            <section data-testid="system-agents-section">
              <Accordion
                type="single"
                collapsible
                value={systemOpen}
                onValueChange={setSystemOpen}
              >
                <AccordionItem value="system">
                  <AccordionTrigger data-testid="system-agents-trigger">
                    <div className="text-left">
                      <h2 className="font-headline text-sm font-bold uppercase tracking-wide text-[var(--color-secondary)]">
                        System
                      </h2>
                      <p className="text-xs text-[var(--color-muted)] mt-0.5">
                        System agents — locked, run out-of-turn (Judge). Not a chat target, not delegable.
                      </p>
                    </div>
                  </AccordionTrigger>
                  <AccordionContent>
                    <div className="grid gap-4 grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 pt-2">
                      {systemAgents.map((agent) => (
                        <AgentCard key={agent.id} agent={agent} />
                      ))}
                    </div>
                  </AccordionContent>
                </AccordionItem>
              </Accordion>
            </section>
          )}
        </div>
      )}
    </div>
  )
}

// ── Main screen ───────────────────────────────────────────────────────────────

export function AgentListScreen() {
  const { openCreateAgentModal, addToast } = useUiStore()
  // MIN-9: defer authed fetches (including cli-detect) until we know who's
  // logged in. Auth is the omnipus-session HttpOnly cookie (US-5 / FR-010) —
  // there is no JS-visible token to gate on anymore, so this uses the
  // display-only `username` (set at the same point login/onboarding used to
  // set the token) as the "we've completed sign-in" signal instead.
  const username = useAuthStore((s) => s.username)
  const queryClient = useQueryClient()

  const { data: agents = [], isLoading: agentsLoading, isError: agentsError, refetch: refetchAgents } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
  })

  const { data: workspaces = [] } = useQuery({
    queryKey: ['workspaces'],
    queryFn: () => fetchWorkspaces(),
  })

  // Three-tier roster (W4 of agent-form-requirements §5.1/§13.2).
  const isLoading = agentsLoading
  const isError = agentsError

  // Built-in roster disclosure state — O2 adaptive expand. Wave-3 hotfix:
  // `''` (not `undefined`) so the Accordion is controlled from its very
  // first render — Radix's controllable-state hook infers controlled-vs-
  // uncontrolled from whether `value` is `undefined` on mount, so a later
  // transition to a defined string (here, on load) triggered React's
  // "changing from uncontrolled to controlled" warning.
  const [builtInOpen, setBuiltInOpen] = useState<string>('')
  const initialOpenApplied = useRef(false)
  useEffect(() => {
    if (isLoading || initialOpenApplied.current) return
    initialOpenApplied.current = true
    const hasCustom = agents.some((a) => !isWorker(a) && !(a.type === 'core' && a.locked) && a.type !== 'system')
    setBuiltInOpen(hasCustom ? '' : 'built-in')
  }, [isLoading, agents])

  // Host-CLI detection — W4 of agent-form-requirements.
  const [hostClis, setHostClis] = useState<CliDetect>(OPTIMISTIC_HOST_CLIS)
  const [externalMenuOpen, setExternalMenuOpen] = useState(false)
  const [cliDetectFailed, setCliDetectFailed] = useState(false)
  useEffect(() => {
    if (typeof window === 'undefined') return
    if (!username) return
    let cancelled = false
    // UAT fix: go through the authed `fetchCliDetect()` (request() wrapper)
    // so the omnipus-session cookie is sent (credentials:'include'). The
    // previous raw fetch had no credentials and 401'd, producing a false
    // "Could not detect installed CLIs" banner.
    fetchCliDetect()
      .then((d) => {
        if (!cancelled) setHostClis(d)
      })
      .catch((err) => {
        // Fail soft (the banner explains availability is assumed), but log for
        // debuggability so a real auth/network regression isn't silent.
        console.warn('CLI detection failed:', err)
        if (!cancelled) setCliDetectFailed(true)
      })
    return () => {
      cancelled = true
    }
  }, [username])

  const { mutate: doSetDefault } = useMutation({
    mutationFn: (agent: Agent) =>
      updateAgent(agent.id, { default: true, updated_at: agent.updated_at }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['agents'] })
      addToast({ message: 'Default agent updated', variant: 'success' })
    },
    onError: (err: unknown) => {
      if (isApiError(err) && err.status === 409) {
        addToast({
          message: 'Agent was changed elsewhere. Retrying…',
          variant: 'error',
        })
        queryClient.invalidateQueries({ queryKey: ['agents'] })
        return
      }
      addToast({
        message: isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Failed to set default',
        variant: 'error',
      })
    },
  })

  if (isLoading) {
    return (
      <div className="absolute inset-0 overflow-y-auto pb-[env(safe-area-inset-bottom)]">
        <div className="max-w-4xl mx-auto px-4 py-6">
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            {[1, 2, 3].map((i) => (
              <div
                key={i}
                className="h-32 rounded-xl border border-[var(--color-border)] bg-[var(--color-surface-1)] animate-pulse"
              />
            ))}
          </div>
        </div>
      </div>
    )
  }

  if (isError) {
    return (
      <div className="absolute inset-0 flex flex-col">
        <ScreenHeader title="Agents" />
        <div className="flex-1 overflow-y-auto pb-[env(safe-area-inset-bottom)]">
          <div className="max-w-4xl mx-auto px-4 py-6">
            <div className="flex flex-col items-center justify-center py-16 gap-3">
              <p className="text-[var(--color-muted)] text-sm">Could not load agents.</p>
              <Button variant="outline" size="sm" onClick={() => refetchAgents()}>
                Retry
              </Button>
            </div>
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="absolute inset-0 flex flex-col">
      <ScreenHeader title="Agents" />
      <div className="flex-1 overflow-y-auto pb-[env(safe-area-inset-bottom)]">
      <div className="max-w-4xl mx-auto px-4 py-6">
        {/* Header */}
        <div className="flex items-center justify-between mb-6">
          <div>
            <h1 className="font-headline text-2xl font-bold text-[var(--color-secondary)]">Agents</h1>
            <p className="text-sm text-[var(--color-muted)] mt-0.5">
              Browse, configure, and create your AI agents.
            </p>
          </div>
        </div>

        {/* Two-tab view: Library | Workspace Teams */}
        <Tabs defaultValue="library">
          <TabsList className="mb-6" data-testid="agents-tabs">
            <TabsTrigger value="library" data-testid="agents-tab-library">
              <Robot size={14} className="mr-1.5" />
              Agents
            </TabsTrigger>
            <TabsTrigger value="teams" data-testid="agents-tab-teams">
              <Users size={14} className="mr-1.5" />
              Workspace Teams
            </TabsTrigger>
          </TabsList>

          <TabsContent value="library">
            <AgentsLibraryView
              agents={agents}
              workspaces={workspaces}
              onSetDefault={doSetDefault}
              openCreateAgentModal={openCreateAgentModal}
              hostClis={hostClis}
              cliDetectFailed={cliDetectFailed}
              externalMenuOpen={externalMenuOpen}
              setExternalMenuOpen={setExternalMenuOpen}
              builtInOpen={builtInOpen}
              setBuiltInOpen={setBuiltInOpen}
            />
          </TabsContent>

          <TabsContent value="teams">
            <WorkspaceTeamsView workspaces={workspaces} />
          </TabsContent>
        </Tabs>

        <CreateAgentModal />
      </div>
      </div>
    </div>
  )
}
