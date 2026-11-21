import { createFileRoute } from '@tanstack/react-router'

// /export-import — the Export & Import page (epic #1094 slice B1). The URL
// state is the validated search of the spec's "Address (URL) state" table:
// wizard, step, scope, preselect, plan_id, staging_id; unknown or invalid
// values drop per the table (never an error screen).
//
// RED STUB (issue #1967 slice B1, test author scaffold): the component renders
// nothing and validateSearch is the identity passthrough. GREEN implements the
// search validation table (D3), the platform-edition unavailable notice, the
// landing and the two wizards.

export interface ExportImportSearch {
  wizard?: string
  step?: string
  scope?: string
  preselect?: string
  plan_id?: string
  staging_id?: string
}

export const Route = createFileRoute('/_app/export-import')({
  validateSearch: (search: Record<string, unknown>): ExportImportSearch => {
    // RED STUB: passthrough; GREEN drops unknown/invalid values per the URL table.
    return search as ExportImportSearch
  },
  component: ExportImportPageStub,
})

/** RED STUB: renders nothing (issue #1967 slice B1). */
function ExportImportPageStub() {
  return null
}
