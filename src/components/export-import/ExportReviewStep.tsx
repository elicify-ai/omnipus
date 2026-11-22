// ExportReviewStep — the server's review of one agent (spec "Export ·
// Review"): name and kind, the files with plain labels and sizes, the bundle
// size, everything left out (the server's own messages, in its order), the
// secrets to enter again, the free-text notice and the fixed "Never
// included" statement. The browser computes none of it (spec: the review is
// authoritative). A reload re-reads the review by its ID; a review the server
// no longer holds sends the person back to Choose with the expired notice.
import { useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { isApiError } from '@/lib/api-error'
import { getPackageExportPlan, PACKAGE_TEXT, type PackageExportPlan } from '@/lib/api/packages'
import { Button } from '@/components/ui/button'
import { Callout, CalloutDescription } from '@/components/ui/callout'
import { Spinner } from '@/components/ui/spinner'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { BusyNotice, formatBytes, StepHeading } from './shared'

const FILE_LABELS: Record<string, string> = {
  'agent.json': 'Settings',
  'SOUL.md': 'Prompt',
  'AGENT.md': 'Instructions',
}

const KIND_LABELS: Record<PackageExportPlan['elements'][number]['kind'], string> = {
  main: 'Main',
  worker: 'Worker',
  external_worker: 'External worker',
}

export const NEVER_INCLUDED =
  "Never included: passwords, API keys and tokens stored in Omnipus, provider keys, channel tokens, this install's settings file, and an external worker's program path, environment values and extra arguments."

/** A plan read the server answered 404: the review is no longer held. */
export function isPlanGone(err: unknown): boolean {
  return isApiError(err) && err.status === 404
}

/** True when the review's own expiry time has passed on this clock. */
export function planExpired(plan: PackageExportPlan): boolean {
  const expires = Date.parse(plan.expires_at)
  return Number.isNaN(expires) || Date.now() >= expires
}

interface ExportReviewStepProps {
  planId: string
  onBack: (agentId?: string) => void
  onDownload: () => void
  onExpired: () => void
}

const sectionTitle = 'text-[length:var(--type-label-size)] font-semibold text-[var(--color-secondary)]'
const bodyText = 'text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]'

export function ExportReviewStep({ planId, onBack, onDownload, onExpired }: ExportReviewStepProps) {
  // Never retried: a 404 is the answer (the review is gone), and a network
  // failure gets an explicit Retry.
  const planQuery = useQuery({
    queryKey: ['packages', 'exportPlan', planId],
    queryFn: () => getPackageExportPlan(planId),
    retry: false,
    gcTime: 0,
  })

  const gone = planQuery.isError && isPlanGone(planQuery.error)
  useEffect(() => {
    if (gone) onExpired()
    // onExpired is a fresh closure each render; the 404 is acted on once.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [gone])

  if (planQuery.isPending || gone) {
    return (
      <section className="flex flex-col gap-[var(--space-3)]">
        <StepHeading>Review</StepHeading>
        <div className="flex items-center gap-[var(--space-2)]">
          <Spinner decorative />
          <BusyNotice>Preparing the review…</BusyNotice>
        </div>
      </section>
    )
  }

  if (planQuery.isError) {
    return (
      <section className="flex flex-col gap-[var(--space-3)]">
        <StepHeading>Review</StepHeading>
        <Callout variant="error">
          <CalloutDescription>{PACKAGE_TEXT.readFailed}</CalloutDescription>
        </Callout>
        <div className="flex flex-wrap gap-[var(--space-2)]">
          <Button variant="outline" onClick={() => onBack()}>
            Back
          </Button>
          <Button onClick={() => void planQuery.refetch()}>Retry</Button>
        </div>
      </section>
    )
  }

  const plan = planQuery.data
  const element = plan.elements[0]

  return (
    <section className="flex flex-col gap-[var(--space-4)]">
      <StepHeading>Review</StepHeading>
      {element && (
        <p className={bodyText}>
          <span className="font-semibold">{element.name}</span>
          {' · '}
          <span className="text-[var(--color-muted)]">{KIND_LABELS[element.kind]}</span>
        </p>
      )}

      <div className="flex flex-col gap-[var(--space-2)]">
        <h3 className={sectionTitle}>What goes into the bundle</h3>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Part</TableHead>
              <TableHead>File</TableHead>
              <TableHead>Size</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {(element?.files ?? []).map((file) => (
              <TableRow key={file.path}>
                <TableCell>{FILE_LABELS[file.path] ?? file.path}</TableCell>
                <TableCell>
                  <code className="font-mono text-[length:var(--type-code-size)]">{file.path}</code>
                </TableCell>
                <TableCell>{formatBytes(file.size)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        <p className={bodyText}>{`About ${formatBytes(plan.size_bytes)}`}</p>
      </div>

      {plan.left_out.length > 0 && (
        <div className="flex flex-col gap-[var(--space-2)]">
          <h3 className={sectionTitle}>Left out</h3>
          <ul className="flex list-disc flex-col gap-[var(--space-2)] pl-[var(--space-4)]">
            {plan.left_out.map((row) => (
              <li key={`${row.type}-${row.reason}`} className={bodyText}>
                <span>{row.message}</span>
                {row.items && row.items.length > 0 && (
                  <ul className="mt-[var(--space-1)] flex flex-wrap gap-[var(--space-2)]">
                    {row.items.map((item) => (
                      <li key={item} className="text-[var(--color-muted)]">
                        {item}
                      </li>
                    ))}
                  </ul>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}

      {plan.needs_secrets.length > 0 && (
        <div className="flex flex-col gap-[var(--space-2)]">
          <h3 className={sectionTitle}>Enter again after import</h3>
          <ul className="flex list-disc flex-col gap-[var(--space-1)] pl-[var(--space-4)]">
            {plan.needs_secrets.map((secret) => (
              <li key={`${secret.name}-${secret.used_by}`} className={bodyText}>
                <code className="font-mono text-[length:var(--type-code-size)]">{secret.name}</code>
                <span className="text-[var(--color-muted)]">{` · ${secret.used_by}`}</span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {plan.disclosures.map((disclosure) => (
        <Callout key={disclosure.code} variant="warning">
          <CalloutDescription>{disclosure.message}</CalloutDescription>
        </Callout>
      ))}

      <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{NEVER_INCLUDED}</p>

      <div className="flex flex-wrap gap-[var(--space-2)]">
        <Button variant="outline" onClick={() => onBack(element?.source_id)}>
          Back
        </Button>
        <Button onClick={() => (planExpired(plan) ? onExpired() : onDownload())}>Download</Button>
      </div>
    </section>
  )
}
