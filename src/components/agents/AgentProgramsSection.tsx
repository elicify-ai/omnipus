// AgentProgramsSection — Agent profile → Programs (spec cli-credentials S1:
// US-2, FR-007, FR-008, FR-042, S-12, S-17, S-53; UI table row
// "Agent profile → Programs").
//
// One switch per program: may this agent's commands use the program's
// login? Each change is its own person-only write of the agent's `programs`
// list (PUT /agents/{id} with the reviewed revision), separate from the
// profile's autosave, which never sends `programs`. A refusal (a private
// variable or git site that conflicts) is shown in the gateway's own words
// and the switch stays as it was. A program that is switched on but has no
// saved token says "Not connected yet": its switch is kept.
import { useId, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import type { Agent } from '@/lib/api'
import { fetchPrograms, programsQueryKeys, updateAgent } from '@/lib/api'
import type { ProgramEntry } from '@/lib/api/generated/openapi-types'
import { programErrorMessage } from '@/lib/programs'
import { ErrorState } from '@/components/ui/error-state'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'

function ProgramSwitchRow({
  entry,
  agentName,
  checked,
  disabled,
  onChange,
}: {
  entry: ProgramEntry
  agentName: string
  checked: boolean
  disabled: boolean
  onChange: (next: boolean) => void
}) {
  // The profile renders its Basics panel twice (tabs and the narrow-screen
  // accordion), so ids come from useId, never from the entry id alone.
  const labelId = `${useId()}-label`
  const notConnected = checked && entry.connection.state === 'not_connected'
  return (
    <li className="flex items-start justify-between gap-[var(--space-3)] py-[var(--space-1)]">
      <div className="min-w-0 flex-1">
        <p id={labelId} className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
          {`${entry.display_name} for ${agentName}`}
        </p>
        <p className="font-mono text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{entry.program_words.join(', ')}</p>
        {notConnected && (
          <p className="text-[length:var(--type-caption-size)] text-[var(--color-warning)]">Not connected yet</p>
        )}
      </div>
      <Switch
        checked={checked}
        disabled={disabled}
        aria-labelledby={labelId}
        data-testid={`agent-program-switch-${entry.id}`}
        onCheckedChange={onChange}
      />
    </li>
  )
}

export function AgentProgramsSection({
  agent,
  agentName,
  onSaved,
}: {
  agent: Agent
  agentName: string
  /** The gateway's updated agent, so the profile keeps its revision current. */
  onSaved: (agent: Agent) => void
}) {
  const programs = useQuery({ queryKey: programsQueryKeys.list, queryFn: fetchPrograms })
  const [refusal, setRefusal] = useState<string | null>(null)
  const save = useMutation({
    // One attempt per action: the person retries, nothing is re-sent on its own.
    retry: false,
    mutationFn: (next: string[]) => updateAgent(agent.id, { revision: agent.revision, programs: next }),
    onSuccess: (saved) => {
      setRefusal(null)
      onSaved(saved)
    },
    onError: (failure) => {
      setRefusal(programErrorMessage(failure, 'The change was not saved. Try again.'))
    },
  })

  const current = agent.programs ?? []
  const shown = save.isPending && save.variables ? save.variables : current
  const toggle = (id: string, on: boolean) => {
    if (save.isPending) return
    const next = on ? [...current.filter((existing) => existing !== id), id] : current.filter((existing) => existing !== id)
    setRefusal(null)
    save.mutate(next)
  }

  let body: React.ReactNode
  if (programs.isPending) {
    body = (
      <div className="space-y-[var(--space-2)]" data-testid="agent-programs-loading">
        <Skeleton className="h-8 w-full" />
        <Skeleton className="h-8 w-full" />
      </div>
    )
  } else if (!programs.data) {
    body = <ErrorState message="Programs could not be loaded." onRetry={() => void programs.refetch()} retryDisabled={programs.isFetching} />
  } else if (programs.data.entries.length === 0) {
    body = (
      <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
        No programs yet. Connect one in Settings → Programs.
      </p>
    )
  } else {
    body = (
      <ul className="divide-y divide-[var(--color-border)]">
        {programs.data.entries.map((entry) => (
          <ProgramSwitchRow
            key={entry.id}
            entry={entry}
            agentName={agentName}
            checked={shown.includes(entry.id)}
            disabled={save.isPending}
            onChange={(on) => toggle(entry.id, on)}
          />
        ))}
      </ul>
    )
  }

  return (
    <section className="space-y-[var(--space-2-5)]" data-testid="agent-programs-section" aria-busy={save.isPending || undefined}>
      <div>
        <p className="font-headline font-semibold text-[length:var(--type-body-size)] text-[var(--color-secondary)]">Programs</p>
        <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] leading-snug">
          Connected programs this agent&apos;s commands may use with their login. Programs are connected in Settings → Programs.
        </p>
      </div>
      {body}
      {refusal && (
        <p role="alert" className="text-[length:var(--type-caption-size)] text-[var(--color-error)]" data-testid="agent-programs-refusal">
          {refusal}
        </p>
      )}
    </section>
  )
}
