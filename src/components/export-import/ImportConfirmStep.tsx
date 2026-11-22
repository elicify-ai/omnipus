// ImportConfirmStep — the workspace choice and the one Import request (spec
// "Import · Confirm", "Import record transitions", "No automatic resend").
//
// The per-tab record moves to `apply_sent` BEFORE the apply request leaves.
// The answer is read by what it proves:
//   - a success → the record is `done` (the wizard stores the summary);
//   - a known refusal that leaves the upload usable → back to `previewed`,
//     the mapped text, and a deliberate Import again;
//   - the upload is gone (404 staging_not_found) → record cleared, File step;
//   - rolled back → record cleared, the text and Start over;
//   - rollback incomplete → record cleared, the restart text;
//   - anything else → the Outcome-unknown screen; the record stays
//     `apply_sent` and the apply is never sent again.
import { useMemo, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { fetchWorkspaces } from '@/lib/api'
import { isApiError } from '@/lib/api-error'
import {
  applyPackageImport,
  classifyApplyOutcome,
  isSessionRefusal,
  packageErrorMessage,
  packageErrorText,
  PACKAGE_TEXT,
  refreshSession,
  type PackageImportPreview,
  type PackageImportResult,
} from '@/lib/api/packages'
import { Button } from '@/components/ui/button'
import { Callout, CalloutDescription } from '@/components/ui/callout'
import { Label } from '@/components/ui/label'
import { QueryErrorState } from '@/components/ui/query-error-state'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Spinner } from '@/components/ui/spinner'
import { clearImportRecord, readImportRecord, updateImportRecord, writeImportRecord } from './importRecord'
import { BusyNotice, InlineError, EXPORT_IMPORT_PATH, ImportOutcomeUnknown, StepHeading } from './shared'

interface ImportConfirmStepProps {
  stagingId: string
  preview: PackageImportPreview
  onApplied: (result: PackageImportResult, workspaceName: string) => void
  onBack: () => void
  /**
   * The step sends the person back to File (the upload is gone, or Start
   * over); the wizard shows File, with the notice when there is one.
   */
  onToFile?: (notice?: string) => void
  /** Discard the upload from this step (the wizard sends the one cancel). */
  onCancel?: () => void
  cancelError?: string | null
  cancelling?: boolean
}

type Outcome =
  | { kind: 'refused'; text: string; tryAgain?: boolean }
  | { kind: 'rolled_back' }
  | { kind: 'rollback_incomplete' }
  | { kind: 'unknown' }

export function ImportConfirmStep({
  stagingId,
  preview,
  onApplied,
  onBack,
  onToFile,
  onCancel,
  cancelError,
  cancelling,
}: ImportConfirmStepProps) {
  const navigate = useNavigate()
  const workspacesQuery = useQuery({
    queryKey: ['workspaces', { status: 'active' }],
    queryFn: ({ signal }) => fetchWorkspaces({ status: 'active', signal }),
  })
  const workspaces = useMemo(() => workspacesQuery.data ?? [], [workspacesQuery.data])
  const [chosen, setChosen] = useState<string | null>(null)
  const [outcome, setOutcome] = useState<Outcome | null>(null)

  // Pre-chosen: the workspace the tab record names, else the default one.
  const preChosen = useMemo(() => {
    const recorded = readImportRecord()
    if (recorded?.staging_id === stagingId && recorded.workspace_id && workspaces.some((w) => w.id === recorded.workspace_id)) {
      return recorded.workspace_id
    }
    return workspaces.find((w) => w.is_default)?.id ?? ''
  }, [workspaces, stagingId])
  const workspaceId = chosen !== null && workspaces.some((w) => w.id === chosen) ? chosen : preChosen
  const workspaceName = workspaces.find((w) => w.id === workspaceId)?.name ?? ''

  function goToFile(notice?: string) {
    onToFile?.(notice)
    void navigate({ to: EXPORT_IMPORT_PATH, search: { wizard: 'import', step: 'file' } })
  }

  const apply = useMutation({
    mutationFn: ({ wsId }: { wsId: string; wsName: string }) =>
      applyPackageImport(stagingId, { mode: 'new', workspace_id: wsId }),
    retry: false,
    onMutate: ({ wsId, wsName }) => {
      setOutcome(null)
      // Written before the request leaves (spec "Per-tab import record").
      writeImportRecord({ staging_id: stagingId, phase: 'apply_sent', workspace_id: wsId, workspace_name: wsName })
    },
    onSuccess: (result, { wsName }) => onApplied(result, wsName),
    onError: (err) => {
      const kind = classifyApplyOutcome(err)
      if (kind === 'cleanup_pending') {
        clearImportRecord()
        setOutcome({ kind: 'rollback_incomplete' })
        return
      }
      if (kind === 'unknown') {
        setOutcome({ kind: 'unknown' })
        return
      }
      const code = isApiError(err) ? err.code : undefined
      if (code === 'staging_not_found') {
        clearImportRecord()
        goToFile(packageErrorText.staging_not_found)
        return
      }
      if (code === 'import_rolled_back') {
        clearImportRecord()
        setOutcome({ kind: 'rolled_back' })
        return
      }
      // The upload is still usable: back to previewed, Import again on purpose.
      updateImportRecord(stagingId, { phase: 'previewed' })
      if (isSessionRefusal(err)) {
        setOutcome({ kind: 'refused', text: PACKAGE_TEXT.sessionApply })
        void refreshSession()
        return
      }
      if (isApiError(err) && err.status === 413) {
        setOutcome({ kind: 'refused', text: PACKAGE_TEXT.applyTooLarge })
        return
      }
      if (code === 'workspace_not_found') void workspacesQuery.refetch()
      setOutcome({
        kind: 'refused',
        text: packageErrorMessage(err, 'import'),
        tryAgain: code === 'import_in_progress',
      })
    },
  })

  // The one Import: never sent while one is in flight, nor again once the tab
  // record says Import was sent for this upload (its outcome is unknown).
  function sendImport() {
    if (apply.isPending) return
    const record = readImportRecord()
    if (record?.staging_id === stagingId && record.phase === 'apply_sent') {
      setOutcome({ kind: 'unknown' })
      return
    }
    apply.mutate({ wsId: workspaceId, wsName: workspaceName })
  }

  function chooseWorkspace(id: string) {
    setChosen(id)
    const name = workspaces.find((w) => w.id === id)?.name
    updateImportRecord(stagingId, { workspace_id: id || undefined, workspace_name: name })
  }

  if (outcome?.kind === 'unknown') {
    return (
      <section className="flex flex-col gap-[var(--space-3)]">
        <StepHeading>Confirm</StepHeading>
        <ImportOutcomeUnknown
          workspaceId={readImportRecord()?.workspace_id ?? (workspaceId || undefined)}
          onStartOver={() => {
            clearImportRecord()
            goToFile()
          }}
        />
      </section>
    )
  }

  if (outcome?.kind === 'rolled_back' || outcome?.kind === 'rollback_incomplete') {
    return (
      <section className="flex flex-col gap-[var(--space-3)]">
        <StepHeading>Confirm</StepHeading>
        <Callout variant="error">
          <CalloutDescription>
            {outcome.kind === 'rolled_back'
              ? packageErrorText.import_rolled_back
              : packageErrorText.import_rollback_incomplete}
          </CalloutDescription>
        </Callout>
        <div className="flex flex-wrap gap-[var(--space-2)]">
          <Button onClick={() => goToFile()}>Start over</Button>
        </div>
      </section>
    )
  }

  const willAdd = preview.will_add[0]
  const busy = apply.isPending || !!cancelling

  return (
    <section className="flex flex-col gap-[var(--space-3)]">
      <StepHeading>Confirm</StepHeading>
      {willAdd && (
        <p className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">
          {willAdd.proposed_name}
        </p>
      )}
      <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
        Added as new. Existing agents are never changed.
      </p>

      {workspacesQuery.isPending && (
        <div className="flex items-center gap-[var(--space-2)]">
          <Spinner decorative />
          <BusyNotice>Loading workspaces…</BusyNotice>
        </div>
      )}
      {workspacesQuery.isError && (
        <QueryErrorState
          layout="fill"
          message="The workspace list could not be loaded."
          onRetry={() => void workspacesQuery.refetch()}
        />
      )}
      {workspacesQuery.isSuccess && workspaces.length === 0 && (
        <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">Create a workspace first.</p>
      )}
      {workspacesQuery.isSuccess && workspaces.length > 0 && (
        <div className="flex flex-col gap-[var(--space-1)]">
          <Label htmlFor="import-workspace">Workspace</Label>
          <Select value={workspaceId} onValueChange={chooseWorkspace} disabled={busy}>
            <SelectTrigger id="import-workspace">
              <SelectValue placeholder="Choose a workspace" />
            </SelectTrigger>
            <SelectContent>
              {workspaces.map((w) => (
                <SelectItem key={w.id} value={w.id}>
                  {w.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      )}

      <InlineError text={outcome?.kind === 'refused' ? outcome.text : null} />
      {cancelError && (
        <Callout variant="warning">
          <CalloutDescription>{cancelError}</CalloutDescription>
        </Callout>
      )}
      {apply.isPending && <BusyNotice>Importing…</BusyNotice>}

      <div className="flex flex-wrap gap-[var(--space-2)]">
        <Button variant="outline" disabled={busy} onClick={onBack}>
          Back
        </Button>
        {onCancel && (
          <Button variant="outline" disabled={busy} onClick={onCancel}>
            Cancel
          </Button>
        )}
        {workspacesQuery.isSuccess && workspaces.length > 0 && (
          <Button
            disabled={!workspaceId || busy}
            onClick={sendImport}
          >
            {outcome?.kind === 'refused' && outcome.tryAgain ? 'Try again' : 'Import'}
          </Button>
        )}
      </div>
    </section>
  )
}
