// ImportResultStep — the Result step of the import wizard (spec US-4.4): the
// success summary, warnings and secrets, Open Team / Open chat / Import
// another, the agent-lookup-driven chat action, and the pending-activation
// notice (PackageImportResult.activation + activation_message, generated
// contract).
//
// Public boundary (test 27): the wizard renders the step with the parsed
// apply result and the recorded workspace name; the step reports actions
// through callbacks. The reload rows of the Result address (stored summary
// under "Import finished") are wizard-level and live in
// ExportImportWizard.reload.test.tsx.
//
// RED STUB (issue #1967 slice B1, test author scaffold): renders nothing.
import type { PackageImportResult } from '@/lib/api/packages'

export interface ImportResultStepProps {
  result: PackageImportResult
  /** The workspace the import joined (from the wizard's record). */
  workspaceName: string
  onOpenTeam: () => void
  onImportAnother: () => void
  /** Only offered when the lookup says Main and activation is active. */
  onOpenChat: (agentId: string) => void
}

export function ImportResultStep(_props: ImportResultStepProps) {
  void _props
  return null
}
