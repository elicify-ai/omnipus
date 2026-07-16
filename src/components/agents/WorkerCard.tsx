import { useMutation } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { Lightning, Spinner, CheckCircle, WarningCircle, XCircle } from '@phosphor-icons/react'
import { Badge } from '@/components/ui/badge'
import { IconRenderer } from '@/components/shared/IconRenderer'
import { testAgentRunner, isApiError } from '@/lib/api'
import type { Agent, ExecutorConfig, RunnerTestResponse } from '@/lib/api'
import { cn } from '@/lib/utils'

interface WorkerCardProps {
  agent: Agent
}

// Sub-agent worker card. Workers are delegation-only labour agents — "a tool you
// point at work, not a colleague". They differ from base AgentCards:
//   SHOW : executor/runner badge, a "Test run" affordance.
//   OMIT : chat/open-conversation entry, heartbeat indicator, default-★ control.
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
    <div className="relative group/card">
      <button tabIndex={0}
        type="button"
        data-testid={`worker-card-${agent.id}`}
        onClick={() => navigate({ to: '/agents/$agentId', params: { agentId: agent.id } })}
        className={cn(
          'w-full text-left rounded-xl border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4',
          'hover:border-[var(--color-accent)]/40 hover:bg-[var(--color-surface-2)] transition-all duration-150',
          'focus-visible:border-[var(--color-accent)]'
        )}
        aria-label={`View worker ${agent.name}`}
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
              <Lightning size={18} weight="fill" className="text-[var(--color-secondary)]" />
            )}
          </div>

          {/* Info */}
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-2 mb-0.5 flex-wrap">
              <span className="font-headline font-bold text-sm text-[var(--color-secondary)] truncate">
                {agent.name}
              </span>
              {/* NB: no heartbeat indicator and no default-★ — workers never have them. */}
            </div>
            <p className="text-sm text-[var(--color-muted)] line-clamp-2 mb-2">
              {agent.description || 'No description'}
            </p>
            <div className="flex items-center gap-2 flex-wrap">
              <Badge variant="secondary">Worker</Badge>
              <Badge
                variant="outline"
                className="gap-1"
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
                // UAT 4d: keep the model slug from overflowing into the
                // "Test run" affordance. min-w-0 + shrink lets it give way in
                // the flex row, and the max-width cap truncates the remainder.
                <span
                  className="text-xs font-mono text-[var(--color-muted)] truncate min-w-0 shrink max-w-[140px]"
                  title={agent.model}
                >
                  {agent.model.includes('/') ? agent.model.split('/').slice(1).join('/') : agent.model}
                </span>
              )}
            </div>
          </div>
        </div>
      </button>

      {/* Test run — sits outside the card button to avoid nested-button HTML. */}
      <WorkerTestRun agentId={agent.id} agentName={agent.name} isExternalCli={isExternalCli} />
    </div>
  )
}

// WorkerTestRun wires the "Test run" affordance to the existing Spec-4 runner-test
// endpoint (POST /api/v1/agents/{id}/runner/test). The endpoint only validates an
// EXTERNAL CLI runner (binary present + handshake + auth), so the button is enabled
// only when the worker's executor is external-cli. A native worker has no external
// runner to probe → the button is disabled with an explanatory tooltip.
function WorkerTestRun({ agentId, agentName, isExternalCli }: { agentId: string; agentName: string; isExternalCli: boolean }) {
  const { mutate, data, error, isPending, reset } = useMutation<RunnerTestResponse, Error>({
    mutationFn: () => testAgentRunner(agentId),
  })

  if (!isExternalCli) {
    return (
      <button tabIndex={0}
        type="button"
        disabled
        data-testid={`worker-test-run-${agentId}`}
        title="Native runners have no external connection to test."
        className="absolute bottom-3 right-4 flex items-center gap-1 text-xs text-[var(--color-muted)]/50 cursor-not-allowed"
        aria-label={`Test run for ${agentName} (unavailable for native runtime)`}
      >
        <Lightning size={12} />
        Test run
      </button>
    )
  }

  return (
    <div className="absolute bottom-3 right-4 flex flex-col items-end gap-1">
      <button tabIndex={0}
        type="button"
        disabled={isPending}
        data-testid={`worker-test-run-${agentId}`}
        onClick={() => {
          reset()
          mutate()
        }}
        className="flex items-center gap-1 text-xs text-[var(--color-muted)] hover:text-[var(--color-accent)] transition-colors disabled:opacity-60"
        aria-label={`Test run for ${agentName}`}
      >
        {isPending ? <Spinner size={12} className="animate-spin" /> : <Lightning size={12} />}
        {isPending ? 'Testing…' : 'Test run'}
      </button>
      {error && (
        <span className="text-xs text-[var(--color-error)] max-w-[180px] truncate" title={isApiError(error) ? error.userMessage : error.message}>
          Test failed
        </span>
      )}
      {data && <WorkerTestResultPill result={data} />}
    </div>
  )
}

function WorkerTestResultPill({ result }: { result: RunnerTestResponse }) {
  // Key success off the authoritative `ok` boolean the backend returns, not an
  // inference from reason === '' — reason carries the failure classifier and is
  // kept for the tooltip/data-reason attribute below.
  const ok = result.ok
  const warn = !ok && result.reason === 'unauthenticated'
  const Icon = ok ? CheckCircle : warn ? WarningCircle : XCircle
  const tone = ok
    ? 'text-[var(--color-success)]'
    : warn
      ? 'text-[var(--color-warning)]'
      : 'text-[var(--color-error)]'
  return (
    <span
      className={`flex items-center gap-1 text-xs ${tone} max-w-[200px]`}
      data-testid={`worker-test-result-${result.cli || 'cli'}`}
      data-reason={result.reason || 'ok'}
      title={result.message}
    >
      <Icon size={12} weight="fill" className="shrink-0" />
      <span className="truncate">{result.message}</span>
    </span>
  )
}
