import { useContext, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { QueryClient, QueryClientContext, QueryClientProvider } from '@tanstack/react-query'
import { queryClient } from '@/lib/queryClient'
import { currentIdentityMode } from '@/lib/api/identityMode'
import { ExportImportLanding, ExportImportUnavailable } from '@/components/export-import/landing'
import { ExportWizard } from '@/components/export-import/ExportWizard'
import { ImportWizard } from '@/components/export-import/ImportWizard'
import { readImportRecord } from '@/components/export-import/importRecord'

// /export-import — the Export & Import page (epic #1094 slice B1, issue
// #1967). The URL state is the validated search of the spec's "Address
// (URL) state" table: wizard, step, scope, preselect, plan_id, staging_id.
// Unknown or invalid values drop per the table, never to an error screen.
// The page is offered only when the gateway reported the self-hosted
// (local) identity mode; anything else, including not yet known, shows the
// unavailable notice and sends no package request.

export interface ExportImportSearch { // not-wire-format: validated address search of the /export-import page, never sent
  wizard?: 'export' | 'import'
  step?: string
  scope?: 'agents'
  preselect?: string
  plan_id?: string
  staging_id?: string
}

const STEPS = {
  export: ['choose', 'review', 'download'],
  import: ['file', 'preview', 'confirm', 'result'],
} as const

function nonEmpty(value: unknown): string | undefined {
  return typeof value === 'string' && value !== '' ? value : undefined
}

export function validateExportImportSearch(search: Record<string, unknown>): ExportImportSearch {
  const out: ExportImportSearch = {}
  const wizard = search.wizard === 'export' || search.wizard === 'import' ? search.wizard : undefined
  if (wizard) {
    out.wizard = wizard
    if (search.step !== undefined) {
      const steps: readonly string[] = STEPS[wizard]
      out.step = typeof search.step === 'string' && steps.includes(search.step) ? search.step : steps[0]
    }
  }
  // B1 knows one scope; any other value (a later slice's scope) is read as agents.
  if (search.scope !== undefined) out.scope = 'agents'
  // A malformed preselect is kept: the Choose step explains why nothing is chosen.
  if (typeof search.preselect === 'string') out.preselect = search.preselect
  const planId = nonEmpty(search.plan_id)
  if (planId) out.plan_id = planId
  const stagingId = nonEmpty(search.staging_id)
  if (stagingId) out.staging_id = stagingId
  return out
}

export const Route = createFileRoute('/_app/export-import')({
  validateSearch: validateExportImportSearch,
  component: ExportImportPage,
})

function ExportImportPage() {
  // In the app the page shares the app's QueryClient. Mounted outside the
  // app's provider it is self-contained: its own client with the app's
  // defaults, so no cached list outlives the mount.
  const ambient = useContext(QueryClientContext)
  const [ownClient] = useState(() =>
    ambient ? null : new QueryClient({ defaultOptions: queryClient.getDefaultOptions() }),
  )
  if (ambient || !ownClient) return <ExportImportContent />
  return (
    <QueryClientProvider client={ownClient}>
      <ExportImportContent />
    </QueryClientProvider>
  )
}

function ExportImportContent() {
  const search = Route.useSearch()
  if (currentIdentityMode() !== 'local') return <ExportImportUnavailable />

  let body
  if (search.wizard === 'export') {
    body = <ExportWizard search={search} />
  } else if (search.wizard === 'import') {
    // A Result address claims nothing without this tab's finished record.
    const finished = search.step === 'result' && readImportRecord()?.phase === 'done'
    body = search.step === 'result' && !finished ? <ExportImportLanding /> : <ImportWizard search={search} />
  } else {
    body = <ExportImportLanding />
  }
  return (
    <div className="mx-auto flex w-full max-w-3xl flex-col gap-[var(--space-4)] px-[var(--space-3)] py-[var(--space-4)]">
      {body}
    </div>
  )
}
