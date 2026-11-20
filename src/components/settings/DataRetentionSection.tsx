// DataRetentionSection — Settings → Data & retention (Settings redesign
// #1519, US-7 frontend side, C-3, FR-044). How long chat history and memory
// are kept, read from and written to GET/PUT /settings/retention (the only
// write path), on the Save bar. Storage figures and "Clear all chat history"
// stay from the old Data section.

import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Callout, CalloutDescription, CalloutTitle } from '@/components/ui/callout'
import { Button } from '@/components/ui/button'
import { Spinner } from '@/components/ui/spinner'
import { fetchRetentionSettings, updateRetentionSettings, getErrorMessage } from '@/lib/api'
import type { RetentionSettingsUpdate } from '@/lib/api'
import { SaveBar } from './SaveBar'
import { toWriteResponse } from './saveOutcome'
import type { SaveBarField, SaveBarValue } from './useSaveBar'
import { DataSection } from './DataSection'

const RETENTION_QUERY_KEY = ['settings', 'retention'] as const

function dayRange(min: number, max: number) {
  return (v: SaveBarValue): string | null => {
    const n = typeof v === 'number' ? v : Number(v)
    if (!Number.isInteger(n) || n < min || n > max) return `Enter a whole number from ${min} to ${max}.`
    return null
  }
}

export function DataRetentionSection({ backupRemoved = false }: { backupRemoved?: boolean }) {
  const queryClient = useQueryClient()
  const { data, isLoading, isError, error, refetch } = useQuery({
    queryKey: RETENTION_QUERY_KEY,
    queryFn: fetchRetentionSettings,
  })

  const notice = backupRemoved ? (
    <Callout variant="info" className="mb-[var(--space-3)]" data-testid="settings-backup-removed">
      <CalloutTitle>Backup and restore were removed.</CalloutTitle>
      <CalloutDescription>Copy the Omnipus data folder to keep a backup.</CalloutDescription>
    </Callout>
  ) : null

  let retention: React.ReactNode
  if (isLoading) {
    retention = (
      <div className="flex items-center gap-[var(--space-2)] text-[var(--color-muted)]">
        <Spinner decorative />
        Loading Data & retention…
      </div>
    )
  } else if (isError || !data) {
    retention = (
      <Callout variant="error" data-testid="retention-error">
        <CalloutTitle>The retention settings could not be loaded</CalloutTitle>
        <CalloutDescription>{getErrorMessage(error, 'The gateway did not answer.')}</CalloutDescription>
        <Button variant="outline" size="sm" onClick={() => void refetch()}>
          Retry
        </Button>
      </Callout>
    )
  } else {
    const locked = data.keep_everything_forever
    const fields: SaveBarField[] = [
      {
        key: 'session_days',
        label: 'Keep chat history for (days)',
        value: data.session_days,
        kind: 'number',
        description: 'Default 90. Older chats are deleted each night.',
        validate: dayRange(1, 365),
        disabled: locked,
      },
      {
        key: 'memory_retros_forever',
        label: 'Keep memory forever',
        value: data.memory_retros_forever,
        kind: 'switch',
        disabled: locked,
      },
      {
        key: 'memory_retros_days',
        label: 'Keep memory for (days)',
        value: data.memory_retros_days,
        kind: 'number',
        description: 'Default 180. Agents’ reflections on past chats.',
        validate: dayRange(1, 730),
        disabled: locked,
        // FR-040: while Keep memory forever is on, the day count is off and not sent.
        disabledWhen: { key: 'memory_retros_forever', equals: true },
      },
    ]
    retention = (
      <>
        {locked && (
          <Callout variant="info" className="mb-[var(--space-3)]">
            <CalloutTitle>Nothing is deleted at the moment</CalloutTitle>
            <CalloutDescription>
              The configuration file turns off both clean-ups (storage.retention.disabled). Remove that line to use the values here.
            </CalloutDescription>
          </Callout>
        )}
        <SaveBar
          section="data"
          fields={fields}
          onSave={(body) =>
            toWriteResponse(() => updateRetentionSettings(body as RetentionSettingsUpdate))
          }
          readBack={async (key) => {
            const fresh = await fetchRetentionSettings()
            return (fresh as Record<string, unknown>)[key]
          }}
          onSaved={() => void queryClient.invalidateQueries({ queryKey: RETENTION_QUERY_KEY })}
        />
      </>
    )
  }

  return (
    <div className="space-y-[var(--space-4)]">
      {notice}
      <section aria-labelledby="settings-retention-heading">
        <h3 id="settings-retention-heading" className="font-headline font-bold text-base text-[var(--color-secondary)] mb-[var(--space-2)]">
          How long things are kept
        </h3>
        {retention}
      </section>
      <DataSection />
    </div>
  )
}
