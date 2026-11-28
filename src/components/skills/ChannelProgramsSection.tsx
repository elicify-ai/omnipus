// ChannelProgramsSection — the Programs section of a channel's panel on the
// Connectors page (spec cli-credentials S1: US-5 AS-9, FR-009, S-148; UI
// table row "Connectors page → channel panel → Programs section").
//
// One switch per channel: may the agent this channel is bound to use
// connected programs for work that came from this channel? The bound line is
// read from the channel's routing (GET /channels/{id}/routing); an unbound
// channel reaches no agent, so its switch has no effect until it is bound.
// The switch shows what the gateway confirmed: while a write is in flight it
// shows the requested value, and a failed write returns it to the confirmed
// value with "Not confirmed" and a Retry that re-sends the same value.
import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { Agent, ChannelRouting, Workspace } from '@/lib/api'
import { fetchChannelPrograms, programsQueryKeys, setChannelPrograms } from '@/lib/api'
import { isApiError } from '@/lib/api-error'
import { programErrorMessage } from '@/lib/programs'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'

export const CHANNEL_PROGRAMS_UNBOUND_MESSAGE =
  'This channel is not bound to a workspace and agent, so no agent receives its messages. This switch takes effect once it is bound.'

export function ChannelProgramsSection({
  channelId,
  open,
  routing,
  workspaces,
  agents,
}: {
  channelId: string
  open: boolean
  /** The channel's routing, as the panel read it; undefined while loading. */
  routing: ChannelRouting | undefined
  workspaces: Workspace[]
  agents: Agent[]
}) {
  const queryClient = useQueryClient()
  const key = programsQueryKeys.channel(channelId)
  const setting = useQuery({
    queryKey: key,
    queryFn: () => fetchChannelPrograms(channelId),
    // Binding a channel resets its switch (FR-009), so an unbound channel has
    // no switch worth showing and its setting is not fetched.
    enabled: open && routing?.bound === true,
  })
  // The value whose write failed; Retry sends it again.
  const [failedValue, setFailedValue] = useState<boolean | null>(null)
  const [failureDetail, setFailureDetail] = useState<string | null>(null)
  const save = useMutation({
    // One attempt per action: the person retries, nothing is re-sent on its own.
    retry: false,
    mutationFn: (allowed: boolean) => {
      const revision = queryClient.getQueryData<{ revision: string }>(key)?.revision ?? setting.data?.revision
      if (!revision) throw new Error('The channel setting has not loaded yet.')
      return setChannelPrograms(channelId, { allowed, revision })
    },
    onSuccess: (saved) => {
      setFailedValue(null)
      setFailureDetail(null)
      queryClient.setQueryData(key, saved)
    },
    onError: (failure, allowed) => {
      setFailedValue(allowed)
      // A definite refusal (stale revision, unknown channel) says why; an
      // unknown outcome says only that it is not confirmed.
      setFailureDetail(isApiError(failure) && failure.status >= 400 && failure.status < 500 ? programErrorMessage(failure, '') || null : null)
      // Show what the gateway now holds.
      void queryClient.invalidateQueries({ queryKey: key })
    },
  })

  const bound = routing?.bound === true
  const workspaceName = bound ? (workspaces.find((ws) => ws.id === routing.workspace_id)?.name ?? routing.workspace_id) : ''
  const agentName = bound ? (agents.find((agent) => agent.id === routing.default_agent_id)?.name ?? routing.default_agent_id) : ''
  const switchLabel = bound
    ? `Let ${agentName} use connected programs for messages from this channel`
    : 'Let the bound agent use connected programs for messages from this channel'
  const confirmed = setting.data?.allowed ?? false
  const checked = save.isPending && save.variables !== undefined ? save.variables : confirmed
  const labelId = `channel-programs-label-${channelId}`

  const change = (next: boolean) => {
    if (save.isPending || !setting.data) return
    setFailedValue(null)
    save.mutate(next)
  }

  return (
    <div className="pt-[var(--space-2)] border-t border-[var(--color-border)] space-y-[var(--space-2-5)]" data-testid="channel-programs-section" aria-busy={save.isPending || undefined}>
      <h3 className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-secondary)] uppercase tracking-wider">
        Programs
      </h3>
      {routing !== undefined && (
        bound ? (
          <p className="text-[length:var(--type-caption-size)] text-[var(--color-secondary)]" data-testid="channel-programs-bound">
            {`Bound to ${workspaceName} · ${agentName}`}
          </p>
        ) : (
          <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] leading-relaxed" data-testid="channel-programs-unbound">
            {CHANNEL_PROGRAMS_UNBOUND_MESSAGE}
          </p>
        )
      )}
      {!bound ? null : setting.isError && !setting.data ? (
        <p className="flex items-center gap-[var(--space-2)] text-[length:var(--type-caption-size)]">
          <span className="text-[var(--color-error)]">The program switch could not be loaded.</span>
          <Button type="button" size="sm" variant="ghost" onClick={() => void setting.refetch()}>Load again</Button>
        </p>
      ) : (
        <div className="flex items-start justify-between gap-[var(--space-3)]">
          <p id={labelId} className="flex-1 min-w-0 text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
            {switchLabel}
          </p>
          <Switch
            checked={checked}
            disabled={!setting.data}
            aria-labelledby={labelId}
            data-testid="channel-programs-switch"
            onCheckedChange={change}
          />
        </div>
      )}
      {bound && failedValue !== null && !save.isPending && (
        <p className="flex flex-wrap items-center gap-[var(--space-2)] text-[length:var(--type-caption-size)]" data-testid="channel-programs-failure">
          <span className="text-[var(--color-error)]">Not confirmed</span>
          {failureDetail && <span className="text-[var(--color-muted)]">{failureDetail}</span>}
          <Button type="button" size="sm" variant="ghost" onClick={() => save.mutate(failedValue)}>Retry</Button>
        </p>
      )}
      <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] leading-relaxed">
        Programs themselves are connected in Settings → Programs.
      </p>
    </div>
  )
}
