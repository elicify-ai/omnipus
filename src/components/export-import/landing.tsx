// Landing — the Export & Import page's two entry cards (spec "UI Screens and
// States": heading "Export & Import", **Start export** ("Save one of your
// agents as a bundle file."), **Start import** ("Add an agent from a bundle
// file.")); outside the self-hosted edition the page shows the unavailable
// notice instead and offers no wizard.
import { useNavigate } from '@tanstack/react-router'
import { PageHeader } from '@/components/ui/page-header'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Callout, CalloutDescription } from '@/components/ui/callout'
import { EXPORT_IMPORT_PATH } from './shared'

const PAGE_TITLE = 'Export & Import'

export function ExportImportLanding({ notice }: { notice?: string }) {
  const navigate = useNavigate()
  return (
    <>
      <PageHeader title={PAGE_TITLE} />
      {notice && (
        <Callout variant="info">
          <CalloutDescription>{notice}</CalloutDescription>
        </Callout>
      )}
      <div className="grid gap-[var(--space-3)] sm:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Export</CardTitle>
            <CardDescription>Save one of your agents as a bundle file.</CardDescription>
          </CardHeader>
          <CardContent>
            <Button onClick={() => void navigate({ to: EXPORT_IMPORT_PATH, search: { wizard: 'export', step: 'choose', scope: 'agents' } })}>
              Start export
            </Button>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Import</CardTitle>
            <CardDescription>Add an agent from a bundle file.</CardDescription>
          </CardHeader>
          <CardContent>
            <Button onClick={() => void navigate({ to: EXPORT_IMPORT_PATH, search: { wizard: 'import', step: 'file' } })}>
              Start import
            </Button>
          </CardContent>
        </Card>
      </div>
    </>
  )
}

export function ExportImportUnavailable() {
  return (
    <div className="mx-auto flex w-full max-w-3xl flex-col gap-[var(--space-4)] px-[var(--space-3)] py-[var(--space-4)]">
      <PageHeader title={PAGE_TITLE} />
      <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
        Export &amp; Import is not available in this edition.
      </p>
    </div>
  )
}
