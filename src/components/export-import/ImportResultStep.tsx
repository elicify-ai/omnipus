// ImportResultStep — the import's result (spec "Import · Result"): the
// summary at once, the server's warnings and secrets to enter again, Open
// Team and Import another. The chat action needs the new agent's type, so it
// is looked up once (a plain effect, never retried): a Main agent gets "Open
// chat"; a failed lookup keeps the result and points to the Team instead.
// While the agent is still starting (activation pending) the activation
// message is shown and there is no lookup and no chat action.
import { useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { fetchAgent, invalidateAfterAgentCreate } from '@/lib/api'
import type { PackageImportResult } from '@/lib/api/packages'
import { Button } from '@/components/ui/button'
import { Callout, CalloutDescription } from '@/components/ui/callout'
import { Spinner } from '@/components/ui/spinner'
import { StepHeading } from './shared'
import { MessageList, SecretList } from './ImportPreviewStep'

interface ImportResultStepProps {
  result: PackageImportResult
  workspaceName: string
  onOpenTeam: () => void
  onImportAnother: () => void
  onOpenChat: (agentId: string) => void
}

type Lookup = 'loading' | 'main' | 'other' | 'failed'

export function ImportResultStep({ result, workspaceName, onOpenTeam, onImportAnother, onOpenChat }: ImportResultStepProps) {
  const queryClient = useQueryClient()
  const created = result.created[0]
  const active = result.activation === 'active'
  const [lookup, setLookup] = useState<Lookup>('loading')

  // The agent and workspace lists change once per import.
  const invalidated = useRef(false)
  useEffect(() => {
    if (invalidated.current) return
    invalidated.current = true
    invalidateAfterAgentCreate(queryClient)
  }, [queryClient])

  const newId = created?.new_id
  useEffect(() => {
    if (!active || !newId) return
    let current = true
    fetchAgent(newId).then(
      (agent) => {
        if (current) setLookup(agent.type === 'Main' ? 'main' : 'other')
      },
      () => {
        if (current) setLookup('failed')
      },
    )
    return () => {
      current = false
    }
  }, [active, newId])

  return (
    <section className="flex flex-col gap-[var(--space-3)]">
      <StepHeading>Result</StepHeading>
      {created && (
        <p className="text-[length:var(--type-body-size)] text-[var(--color-secondary)]">
          {`${created.name} was added to ${workspaceName}.`}
        </p>
      )}
      {!active && result.activation_message && (
        <Callout variant="info">
          <CalloutDescription>{result.activation_message}</CalloutDescription>
        </Callout>
      )}
      {result.warnings.length > 0 && <MessageList rows={result.warnings} />}
      <SecretList secrets={result.needs_secrets} />
      {active && lookup === 'failed' && (
        <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">Open it from the Team.</p>
      )}
      <div className="flex flex-wrap items-center gap-[var(--space-2)]">
        <Button variant="outline" onClick={onOpenTeam}>
          Open Team
        </Button>
        {active && lookup === 'loading' && <Spinner label="Checking the new agent" />}
        {active && lookup === 'main' && newId && <Button onClick={() => onOpenChat(newId)}>Open chat</Button>}
        <Button variant="outline" onClick={onImportAnother}>
          Import another
        </Button>
      </div>
    </section>
  )
}
