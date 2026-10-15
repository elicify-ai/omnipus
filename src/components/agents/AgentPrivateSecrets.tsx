// AgentPrivateSecrets — the agent editor's private secrets list
// (omnipus-system-agent US-7, FR-073, FR-086, FR-241; spec §6.3 row "Agent
// editor private secrets list"; K6, K49; WU-5).
//
// Names and metadata only — never a value. One row per stored version of
// this agent's private variables, grouped by workspace (a private variable
// belongs to one workspace AND one agent). Each row says whether that
// version is in use, stored but unused, or replaced, what uses it, and
// carries the "Available in channel conversations" switch (off by default;
// only a person in the web app can turn it on; audited server-side).
//
// States: loading; "No private secrets"; error + Retry; a failed refresh
// keeps the cached rows with an out-of-date hint; a switch change whose
// outcome is unknown says "not confirmed" with Retry.
import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import type { Workspace } from '@/lib/api'
import type { SecretNameEntry } from '@/lib/api/generated/openapi-types'
import { listSecrets, secretsQueryKeys, updateSecretOptions, workspacesQueryKeys } from '@/lib/api'
import { classifyMutationFailure, mutationFailureMessage } from '@/lib/mutationOutcome'
import { useUiStore } from '@/store/ui'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import { ErrorState } from '@/components/ui/error-state'
import { Skeleton } from '@/components/ui/skeleton'
import { PRIVATE_SECRET_PROMISE } from '@/components/chat/SecretRequestCard'
import { useQueryRefreshFailed } from '@/hooks/useQueryRefreshFailed'

const STATE_LABEL: Record<SecretNameEntry['state'], string> = {
  selected: 'In use',
  stored: 'Stored, not in use',
  superseded: 'Replaced — can be removed',
}

export function AgentPrivateSecrets({ agentId }: { agentId: string }) {
  const query = useQuery({ queryKey: secretsQueryKeys.list(), queryFn: listSecrets, staleTime: 15_000 })
  const refreshFailed = useQueryRefreshFailed(secretsQueryKeys.list())
  const queryClient = useQueryClient()
  const workspaces = queryClient.getQueryData<Workspace[]>(workspacesQueryKeys.list({ status: 'active' })) ?? []
  const workspaceName = (id: string) => workspaces.find((w) => w.id === id)?.name ?? id

  const mine = (query.data ?? []).filter(
    (entry) => entry.kind === 'agent_private' && entry.owner?.agent_id === agentId,
  )
  const byWorkspace = new Map<string, SecretNameEntry[]>()
  for (const entry of mine) {
    const key = entry.owner?.workspace_id ?? ''
    byWorkspace.set(key, [...(byWorkspace.get(key) ?? []), entry])
  }

  return (
    <section className="space-y-[var(--space-2-5)]" data-testid="agent-private-secrets" aria-labelledby="agent-private-secrets-heading">
      <h3 id="agent-private-secrets-heading" className="font-headline text-[length:var(--type-body-compact-size)] font-bold text-[var(--color-secondary)]">
        Private secrets
      </h3>
      <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
        Variables only this agent's own commands see, separately in each workspace. {PRIVATE_SECRET_PROMISE} To add or
        replace one, ask the agent in its chat — it asks for the value on a secure card. To remove one, ask Omnipus.
      </p>
      {query.isPending ? (
        <div className="space-y-[var(--space-2)]" aria-busy="true" data-testid="agent-private-secrets-loading">
          <Skeleton className="h-[var(--space-6)] w-full" />
          <Skeleton className="h-[var(--space-6)] w-full" />
        </div>
      ) : query.isError && !query.data ? (
        <ErrorState message="Could not load this agent's private secrets." onRetry={() => void query.refetch()} />
      ) : (
        <>
          {(refreshFailed || (query.isError && !!query.data)) && (
            <div role="status" className="flex items-center gap-[var(--space-2)] text-[length:var(--type-caption-size)] text-[var(--color-warning)]">
              <span className="flex-1">Could not refresh — this list may be out of date.</span>
              <Button variant="ghost" size="sm" onClick={() => void query.refetch()}>Retry</Button>
            </div>
          )}
          {mine.length === 0 ? (
            <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]" data-testid="agent-private-secrets-empty">
              No private secrets
            </p>
          ) : (
            [...byWorkspace.entries()].map(([workspaceId, entries]) => (
              <div key={workspaceId} className="space-y-[var(--space-1)]">
                <p className="text-[length:var(--type-caption-size)] font-semibold uppercase tracking-widest text-[var(--color-muted)]">
                  {workspaceName(workspaceId)}
                </p>
                <ul className="space-y-[var(--space-1)]">
                  {entries
                    .slice()
                    .sort((a, b) => b.version - a.version)
                    .map((entry) => <PrivateSecretRow key={entry.reference} entry={entry} />)}
                </ul>
              </div>
            ))
          )}
        </>
      )}
    </section>
  )
}

function PrivateSecretRow({ entry }: { entry: SecretNameEntry }) {
  const queryClient = useQueryClient()
  const addToast = useUiStore((s) => s.addToast)
  const [saving, setSaving] = useState(false)
  const [uncertain, setUncertain] = useState<boolean | null>(null)
  const [saved, setSaved] = useState(false)
  const checked = entry.available_in_channel_turns === true

  const write = async (next: boolean) => {
    setSaving(true)
    setSaved(false)
    try {
      const updated = await updateSecretOptions(entry.reference, { available_in_channel_turns: next })
      queryClient.setQueryData<SecretNameEntry[]>(secretsQueryKeys.list(), (prev) =>
        prev?.map((row) => (row.reference === updated.reference ? updated : row)),
      )
      setUncertain(null)
      setSaved(true)
    } catch (error) {
      if (classifyMutationFailure(error) === 'uncertain') {
        setUncertain(next)
        void queryClient.invalidateQueries({ queryKey: secretsQueryKeys.list() })
      } else {
        setUncertain(null)
        addToast({ variant: 'error', message: `Could not change ${entry.reference}: ${mutationFailureMessage(error)}` })
      }
    } finally {
      setSaving(false)
    }
  }

  const label = entry.recipient.lines.join(' · ')
  const checkboxId = `secret-channel-${entry.reference}`
  return (
    <li className="rounded-[var(--radius-medium)] border border-[var(--color-border)] px-[var(--space-2-5)] py-[var(--space-2)]" data-testid={`private-secret-${entry.reference}`}>
      <p className="break-words text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
        {label} <span className="text-[var(--color-muted)]">· version {entry.version}</span>
      </p>
      <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
        {STATE_LABEL[entry.state]}
        {entry.consumers.length > 0 ? ` · used by ${entry.consumers.join(', ')}` : ''}
      </p>
      <div className="mt-[var(--space-1)] flex items-center gap-[var(--space-2)]">
        <Checkbox
          id={checkboxId}
          checked={checked}
          disabled={saving}
          onCheckedChange={(value) => void write(value === true)}
        />
        <Label htmlFor={checkboxId} className="text-[length:var(--type-caption-size)] text-[var(--color-secondary)]">
          Available in channel conversations
        </Label>
      </div>
      {/* §6.3 "Success: Saved" — only after the server confirmed the change. */}
      <p role="status" aria-live="polite" className="mt-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
        {saved ? 'Saved' : ''}
      </p>
      {uncertain !== null && (
        <div role="status" className="mt-[var(--space-1)] flex items-center gap-[var(--space-2)] text-[length:var(--type-caption-size)] text-[var(--color-warning)]">
          <span className="flex-1">The change was not confirmed.</span>
          <Button variant="ghost" size="sm" disabled={saving} onClick={() => void write(uncertain)}>Retry</Button>
        </div>
      )}
    </li>
  )
}
