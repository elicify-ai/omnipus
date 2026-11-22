// ExportChooseStep — the export wizard's agent choice (spec "Export · Choose
// agent"): the person's own agents only (built-in, locked and system agents
// are never offered), a radio group with kind labels, a filter field, the
// one-agent note, Review gating, the empty state and the refusal shown inline.
//
// Review asks the server for the review (POST /packages/export/plan); a
// refusal there (blank prompt, unreadable file, a broken agent rule) is shown
// above Review and the step stays.
import { useMemo, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { fetchAgents, type Agent } from '@/lib/api'
import { createPackageExportPlan, packageErrorMessage } from '@/lib/api/packages'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Callout, CalloutDescription } from '@/components/ui/callout'
import { FormError } from '@/components/ui/FormError'
import { Input } from '@/components/ui/input'
import { QueryErrorState } from '@/components/ui/query-error-state'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Skeleton } from '@/components/ui/skeleton'
import { BusyNotice, StepHeading } from './shared'

export const EXPORT_EXPIRED_NOTICE = 'The review expired. Check your choice and review again.'

const KIND_LABELS: Partial<Record<Agent['type'], string>> = {
  Main: 'Main',
  Subagent: 'Worker',
  subagent_3p: 'External worker',
}

/** Built-in agents are on every install: locked agents and the core/system types. */
export function isBuiltInAgent(agent: Pick<Agent, 'type' | 'locked'>): boolean {
  return agent.locked || agent.type === 'core' || agent.type === 'system'
}

type PreselectResult = { id: string } | { notice: string } | null

/** Reads `preselect=agent:<id>` against the full agent list (spec US-3.3, Dataset D3). */
function resolvePreselect(preselect: string | undefined, agents: Agent[]): PreselectResult {
  if (preselect === undefined) return null
  const match = /^agent:(.+)$/.exec(preselect)
  if (!match) return { notice: 'This kind of item cannot be exported yet.' }
  const agent = agents.find((a) => a.id === match[1])
  if (!agent) return { notice: 'The agent chosen from its page no longer exists.' }
  if (isBuiltInAgent(agent)) return { notice: 'Built-in agents are on every install and cannot be exported.' }
  return { id: agent.id }
}

interface ExportChooseStepProps {
  preselect?: string
  /** The agent chosen before going to Review and back. */
  initialChosen?: string
  /** A notice from a later step (the review expired). */
  notice?: string
  onReviewed: (planId: string, agentId: string) => void
}

export function ExportChooseStep({ preselect, initialChosen, notice, onReviewed }: ExportChooseStepProps) {
  const agentsQuery = useQuery({ queryKey: ['agents'], queryFn: fetchAgents })
  const [filter, setFilter] = useState('')
  const [chosen, setChosen] = useState<string | null>(initialChosen ?? null)

  const agents = useMemo(() => agentsQuery.data ?? [], [agentsQuery.data])
  const own = useMemo(
    () =>
      agents
        .filter((a) => !isBuiltInAgent(a))
        .sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' })),
    [agents],
  )
  const duplicateNames = useMemo(() => {
    const counts = new Map<string, number>()
    for (const a of own) counts.set(a.name, (counts.get(a.name) ?? 0) + 1)
    return new Set([...counts].filter(([, n]) => n > 1).map(([name]) => name))
  }, [own])

  const preselected = agentsQuery.data ? resolvePreselect(preselect, agents) : null
  const chosenId = chosen ?? (preselected && 'id' in preselected ? preselected.id : '')

  const plan = useMutation({
    mutationFn: (agentId: string) => createPackageExportPlan({ scope: 'agents', agent_ids: [agentId] }),
    retry: false,
    onSuccess: (result, agentId) => onReviewed(result.plan_id, agentId),
  })

  const query = filter.trim().toLowerCase()
  const shown = query === '' ? own : own.filter((a) => a.name.toLowerCase().includes(query))

  return (
    <section className="flex flex-col gap-[var(--space-3)]">
      <StepHeading>Choose agent</StepHeading>
      <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
        A bundle holds one agent in this version.
      </p>
      {/* Shown with the list it refers to, once the list has loaded. */}
      {notice && !agentsQuery.isPending && (
        <Callout variant="info">
          <CalloutDescription>{notice}</CalloutDescription>
        </Callout>
      )}
      {preselected && 'notice' in preselected && (
        <Callout variant="info">
          <CalloutDescription>{preselected.notice}</CalloutDescription>
        </Callout>
      )}
      {agentsQuery.isPending && (
        <div className="flex flex-col gap-[var(--space-2)]" aria-hidden="true">
          <Skeleton className="h-[var(--space-7)] w-full" />
          <Skeleton className="h-[var(--space-7)] w-full" />
        </div>
      )}
      {agentsQuery.isError && (
        <QueryErrorState
          layout="fill"
          message="The agent list could not be loaded."
          onRetry={() => void agentsQuery.refetch()}
        />
      )}
      {agentsQuery.isSuccess && own.length === 0 && (
        <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
          You have no agents of your own yet. Built-in agents are on every install and are not exported.
        </p>
      )}
      {agentsQuery.isSuccess && own.length > 0 && (
        <>
          <Input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter agents"
            aria-label="Filter agents"
          />
          {shown.length === 0 ? (
            <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">No agent matches.</p>
          ) : (
            <RadioGroup
              aria-label="Agent to export"
              orientation="vertical"
              value={chosenId}
              onValueChange={(id) => setChosen(id)}
            >
              {shown.map((agent) => {
                const kind = KIND_LABELS[agent.type]
                const label = duplicateNames.has(agent.name) ? `${agent.name} (${agent.id.slice(0, 8)})` : agent.name
                return (
                  <RadioGroupItem key={agent.id} value={agent.id} aria-label={label}>
                    <span className="flex min-w-0 flex-col items-start gap-[var(--space-1)]">
                      <span className="flex flex-wrap items-center gap-[var(--space-2)]">
                        <span>{label}</span>
                        {kind && <Badge variant="outline">{kind}</Badge>}
                      </span>
                      {agent.description && (
                        <span className="whitespace-normal text-[length:var(--type-caption-size)] font-normal text-[var(--color-muted)]">
                          {agent.description}
                        </span>
                      )}
                    </span>
                  </RadioGroupItem>
                )
              })}
            </RadioGroup>
          )}
          <FormError error={plan.isError ? packageErrorMessage(plan.error, 'export') : null} />
          {plan.isPending && <BusyNotice>Preparing the review…</BusyNotice>}
          <div className="flex flex-wrap items-center gap-[var(--space-2-5)]">
            <Button disabled={!chosenId || plan.isPending} onClick={() => plan.mutate(chosenId)}>
              Review
            </Button>
            {!chosenId && (
              <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
                Choose an agent to continue.
              </span>
            )}
          </div>
        </>
      )}
    </section>
  )
}
