// ImportConfirmStep — the Confirm step of the import wizard (spec US-4.3,
// US-5): states the mode ("Added as new. Existing agents are never changed."),
// pre-chooses the default workspace, applies with the exact
// {mode, workspace_id} body, owns the per-tab record transitions around its
// apply call, and maps every answer class per "Import record transitions".
//
// Public boundary (tests 26/27): the wizard renders the step with the staging
// ID and the preview it already holds; the step reports outcomes through
// onApplied / onBack and navigates to the File step itself when the upload is
// gone (404 staging_not_found, Start over after a rollback).
//
// RED STUB (issue #1967 slice B1, test author scaffold): renders nothing.
import type { PackageImportPreview, PackageImportResult } from '@/lib/api/packages'

export interface ImportConfirmStepProps {
  stagingId: string
  preview: PackageImportPreview
  onApplied: (result: PackageImportResult) => void
  onBack: () => void
}

export function ImportConfirmStep(_props: ImportConfirmStepProps) {
  void _props
  return null
}
