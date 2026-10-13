/**
 * SystemAgentsSection — Settings → System agents (omnipus-system-agent
 * FR-106, BDD-12.6; ADR-20261011 OSA-10 U9; founder FQ-9 A).
 *
 * The home of the three system agents: Judge, Plan Supervisor and Omnipus.
 * Each opens the ONE shared agent editor (AgentProfile, hosted once in the
 * app shell and driven by useUiStore.openEditAgentSlideOver) — there is no
 * second editor here. Omnipus's model is editable even when no model works:
 * the editor is a configuration write, never a model turn. Judge and Plan
 * Supervisor keep their editable instructions; their tuning redesign is out
 * of scope (F:D1). No default-agent control (US-13 AS-5).
 *
 * States (spec §6.3 "System agents"): loading skeleton rows; a failed first
 * load shows the error with Retry; a failed refresh keeps the cached rows
 * with an out-of-date hint and Retry. The save states ("not confirmed" +
 * Retry, Saved) belong to the shared editor.
 */
import { useQuery } from '@tanstack/react-query'
import { Robot } from '@phosphor-icons/react'
import type { Agent } from '@/lib/api'
import { fetchAgents } from '@/lib/api'
import { JUDGE_AGENT_ID, OMNIPUS_AGENT_ID, PLAN_SUPERVISOR_AGENT_ID, SYSTEM_AGENT_IDS } from '@/lib/omnipus'
import { useUiStore } from '@/store/ui'
import { AgentMark } from '@/components/agents/AgentMark'
import { providerBindingCopy } from '@/components/agents/AgentCard'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { ErrorState } from '@/components/ui/error-state'
import { Skeleton } from '@/components/ui/skeleton'

const PURPOSE: Record<(typeof SYSTEM_AGENT_IDS)[number], string> = {
  [JUDGE_AGENT_ID]: 'Checks whether goals and tasks meet their acceptance criteria.',
  [PLAN_SUPERVISOR_AGENT_ID]: 'Corrects a running plan when it stalls or misses its definition of done.',
  [OMNIPUS_AGENT_ID]: 'Sets up and changes your agents, teams, skills, connectors, providers and channels.',
}

const FALLBACK_NAME: Record<(typeof SYSTEM_AGENT_IDS)[number], string> = {
  [JUDGE_AGENT_ID]: 'Judge',
  [PLAN_SUPERVISOR_AGENT_ID]: 'Plan Supervisor',
  [OMNIPUS_AGENT_ID]: 'Omnipus',
}

export function SystemAgentsSection(): React.ReactElement {
  const { data, isPending, isError, refetch } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    staleTime: 30_000,
  })

  return (
    <section className="space-y-[var(--space-3)]" data-testid="settings-system-agents" aria-labelledby="settings-system-agents-heading">
      <div className="flex items-center gap-[var(--space-2)]">
        <Robot size={18} className="text-[var(--color-secondary)]" aria-hidden="true" />
        <h2 id="settings-system-agents-heading" className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">
          System agents
        </h2>
      </div>
      <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
        The agents the system itself runs on. They are not part of any team and cannot be deleted.
        Edit opens the same agent editor every agent uses.
      </p>

      {isPending ? (
        <div className="space-y-[var(--space-2)]" data-testid="system-agents-loading" aria-busy="true">
          {SYSTEM_AGENT_IDS.map((id) => (
            <Skeleton key={id} className="h-[var(--space-7)] w-full" />
          ))}
        </div>
      ) : isError && !data ? (
        <ErrorState message="Could not load the system agents." onRetry={() => void refetch()} />
      ) : (
        <>
          {isError && (
            <div role="status" className="flex items-center gap-[var(--space-2)] text-[length:var(--type-caption-size)] text-[var(--color-warning)]">
              <span className="flex-1">Could not refresh — this list may be out of date.</span>
              <Button variant="ghost" size="sm" onClick={() => void refetch()}>Retry</Button>
            </div>
          )}
          <ul className="space-y-[var(--space-2)]">
            {SYSTEM_AGENT_IDS.map((id) => (
              <SystemAgentRow key={id} id={id} agent={data?.find((agent) => agent.id === id)} />
            ))}
          </ul>
        </>
      )}
    </section>
  )
}

function SystemAgentRow({ id, agent }: { id: (typeof SYSTEM_AGENT_IDS)[number]; agent: Agent | undefined }) {
  const openEditAgentSlideOver = useUiStore((s) => s.openEditAgentSlideOver)
  const name = agent?.name ?? FALLBACK_NAME[id]
  const binding = agent ? providerBindingCopy(agent) : null
  return (
    <li>
      <Card className="flex items-center gap-[var(--space-2-5)] p-[var(--space-2-5)]" data-testid={`system-agent-${id}`}>
        <AgentMark agent={agent} name={name} size={26} />
        <div className="min-w-0 flex-1">
          <p className="text-[length:var(--type-body-compact-size)] font-medium text-[var(--color-secondary)]">{name}</p>
          <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{PURPOSE[id]}</p>
          <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
            {agent === undefined
              ? 'Not found on this install.'
              : `Model: ${agent.model ? agent.model : 'none set'}${binding ? ` · ${binding}` : ''}`}
          </p>
        </div>
        <Button
          variant="outline"
          size="sm"
          disabled={agent === undefined}
          aria-label={`Edit ${name}`}
          data-testid={`system-agent-edit-${id}`}
          onClick={() => openEditAgentSlideOver(id)}
        >
          Edit
        </Button>
      </Card>
    </li>
  )
}
