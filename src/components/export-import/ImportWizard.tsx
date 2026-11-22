// ImportWizard — the import wizard shell (spec "Import · Choose file",
// "Preview", "Confirm", "Result", "Reload mid-import", "Leaving the wizard
// cleans up silently", "What a cancel answer proves").
//
// The step lives in the address (`wizard=import&step=…&staging_id=…`) and the
// per-tab record (importRecord.ts) is the only evidence a reload has:
//   - Preview/Confirm address, record `apply_sent` → Outcome-unknown, no cancel;
//   - Preview/Confirm address, record `previewed` or none → exactly one cancel,
//     its answer read by what it proves;
//   - Result address with a `done` record → the stored summary.
// Leaving the wizard while the record is `previewed` sends one silent cancel;
// after Import was sent it sends none. A cancel is sent at most once per
// upload unless the session refused it (then it never ran).
import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { fetchWorkspace } from '@/lib/api'
import { isApiError } from '@/lib/api-error'
import {
  cancelPackageImport,
  isSessionRefusal,
  packageErrorText,
  PACKAGE_TEXT,
  refreshSession,
  type PackageImportPreview,
  type PackageImportResult,
} from '@/lib/api/packages'
import { mainSessionIdOfMember } from '@/lib/nav/sessionCoreSeam'
import { Button } from '@/components/ui/button'
import { Spinner } from '@/components/ui/spinner'
import { Stepper } from '@/components/ui/stepper'
import type { ExportImportSearch } from '@/routes/_app/export-import'
import {
  clearImportRecord,
  readImportRecord,
  writeImportRecord,
  type ImportRecordResult,
} from './importRecord'
import { ExportImportLanding } from './landing'
import {
  BusyNotice,
  ImportOutcomeUnknown,
  ImportUncertain,
  IMPORT_STEPS,
  setLandingNotice,
  StepHeading,
  useAddressView,
  useOpenTeam,
} from './shared'
import { ImportFileStep } from './ImportFileStep'
import { ImportPreviewStep } from './ImportPreviewStep'
import { ImportConfirmStep } from './ImportConfirmStep'
import { ImportResultStep } from './ImportResultStep'

export const RELOAD_NOTICE = 'The page was reloaded. Choose the file again.'

type ImportView =
  | { kind: 'file'; notices: string[] }
  | { kind: 'reloading'; stagingId: string }
  | { kind: 'preview'; preview: PackageImportPreview }
  | { kind: 'confirm'; preview: PackageImportPreview }
  | { kind: 'result'; result: PackageImportResult; workspaceName: string }
  | { kind: 'finished'; summary: ImportRecordResult; workspaceId?: string }
  | { kind: 'uncertain'; workspaceId?: string }
  | { kind: 'unknown'; workspaceId?: string }
  | { kind: 'landing' }

type CancelAnswer = 'cancelled' | 'cleanup_failed' | 'session' | 'uncertain'

/** Sends the one cancel and reads its answer (spec "What a cancel answer proves"). */
async function sendCancel(stagingId: string): Promise<CancelAnswer> {
  try {
    await cancelPackageImport(stagingId)
    return 'cancelled'
  } catch (err) {
    if (isSessionRefusal(err)) return 'session'
    if (isApiError(err) && err.status === 500 && err.code === 'staging_cleanup_failed') return 'cleanup_failed'
    return 'uncertain'
  }
}

/** The workspace the tab record names for this upload, if any. */
function recordedWorkspace(stagingId: string | undefined): string | undefined {
  const record = readImportRecord()
  return record && record.staging_id === stagingId ? record.workspace_id : undefined
}

const FILE_ADDRESS: ExportImportSearch = { wizard: 'import', step: 'file' }

export function ImportWizard({ search }: { search: ExportImportSearch }) {
  const navigate = useNavigate()
  const openTeam = useOpenTeam()
  // The preview of the upload this wizard holds, so an in-app move between
  // Preview and Confirm (browser Back) is not mistaken for a reload.
  const held = useRef<PackageImportPreview | null>(null)
  // Uploads this wizard already sent a cancel for (never twice).
  const cancelSent = useRef(new Set<string>())

  const seed = (s: ExportImportSearch): ImportView => {
    if (s.step === 'result') {
      const record = readImportRecord()
      if (record?.phase === 'done' && record.result) {
        return { kind: 'finished', summary: record.result, workspaceId: record.workspace_id }
      }
      return { kind: 'landing' }
    }
    if ((s.step === 'preview' || s.step === 'confirm') && s.staging_id) {
      // The tab record outranks the held preview: once Import was sent for
      // this upload, Back/Forward shows the unknown outcome, never the form.
      const record = readImportRecord()
      if (record?.staging_id === s.staging_id && record.phase === 'apply_sent') {
        return { kind: 'unknown', workspaceId: record.workspace_id }
      }
      if (held.current?.staging_id === s.staging_id) {
        return { kind: s.step, preview: held.current }
      }
      return { kind: 'reloading', stagingId: s.staging_id }
    }
    return { kind: 'file', notices: [] }
  }
  const { view, go, show } = useAddressView(search, seed)

  const [cancelling, setCancelling] = useState(false)
  const [cancelError, setCancelError] = useState<string | null>(null)

  const toFile = useCallback(
    (notices: string[] = []) => go({ kind: 'file', notices }, FILE_ADDRESS),
    [go],
  )

  // A reload on Preview or Confirm: exactly one cancel, read by its answer.
  const reloadStaging = view.kind === 'reloading' ? view.stagingId : undefined
  useEffect(() => {
    if (!reloadStaging || cancelSent.current.has(reloadStaging)) return
    cancelSent.current.add(reloadStaging)
    const workspaceId = recordedWorkspace(reloadStaging)
    void sendCancel(reloadStaging).then((answer) => {
      if (answer === 'cancelled' || answer === 'cleanup_failed') {
        clearImportRecord()
        toFile(answer === 'cancelled' ? [RELOAD_NOTICE] : [RELOAD_NOTICE, packageErrorText.staging_cleanup_failed])
        return
      }
      // A session refusal on a reload proves nothing either; no second cancel.
      show({ kind: 'uncertain', workspaceId }, search)
    })
    // One cancel per reloaded upload; the answer decides the view.
  }, [reloadStaging])

  // Leaving the wizard: one silent cancel for an upload that was previewed
  // and never sent to Import. Scheduled after unmount and dropped when the
  // effect runs again (StrictMode mounts twice in development).
  const leaveTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => {
    if (leaveTimer.current !== undefined) {
      clearTimeout(leaveTimer.current)
      leaveTimer.current = undefined
    }
    const sent = cancelSent.current
    return () => {
      leaveTimer.current = setTimeout(() => {
        const record = readImportRecord()
        if (!record || record.phase !== 'previewed' || sent.has(record.staging_id)) return
        sent.add(record.staging_id)
        void sendCancel(record.staging_id).then((answer) => {
          const now = readImportRecord()
          if ((answer === 'cancelled' || answer === 'cleanup_failed') && now?.staging_id === record.staging_id) {
            clearImportRecord()
          }
        })
      }, 0)
    }
  }, [])

  // Cancel on Preview or Confirm: the one cancel, its answer decides.
  async function cancelUpload(stagingId: string) {
    // An Import already sent for this upload: no cancel, the outcome is unknown.
    const record = readImportRecord()
    if (record?.staging_id === stagingId && record.phase === 'apply_sent') {
      show({ kind: 'unknown', workspaceId: record.workspace_id }, search)
      return
    }
    if (cancelSent.current.has(stagingId)) return
    cancelSent.current.add(stagingId)
    setCancelling(true)
    setCancelError(null)
    const answer = await sendCancel(stagingId)
    setCancelling(false)
    if (answer === 'session') {
      // The cancel never ran: the person may select Cancel again.
      cancelSent.current.delete(stagingId)
      setCancelError(PACKAGE_TEXT.sessionCancel)
      void refreshSession()
      return
    }
    if (answer === 'uncertain') {
      show({ kind: 'uncertain', workspaceId: recordedWorkspace(stagingId) }, search)
      return
    }
    clearImportRecord()
    held.current = null
    setLandingNotice(answer === 'cleanup_failed' ? packageErrorText.staging_cleanup_failed : undefined)
    go({ kind: 'landing' }, {})
  }

  function startOver() {
    clearImportRecord()
    held.current = null
    toFile()
  }

  function importAnother() {
    clearImportRecord()
    held.current = null
    toFile()
  }

  async function openChat(workspaceId: string, agentId: string) {
    let sessionId: string | undefined
    try {
      const workspace = await fetchWorkspace(workspaceId)
      sessionId = mainSessionIdOfMember(workspace.member_configs?.[agentId])
    } catch {
      // The workspace chat still opens; the person picks the agent there.
    }
    void navigate({
      to: '/workspaces/$workspaceId/chat',
      params: { workspaceId },
      search: sessionId ? { open: sessionId } : {},
    })
  }

  if (view.kind === 'landing') return <ExportImportLanding />

  const stepIndex =
    view.kind === 'file'
      ? 0
      : view.kind === 'preview'
        ? 1
        : view.kind === 'confirm'
          ? 2
          : view.kind === 'result' || view.kind === 'finished'
            ? 3
            : search.step === 'confirm'
              ? 2
              : 1

  return (
    <>
      <Stepper steps={IMPORT_STEPS} current={stepIndex} label="Import steps" />
      {view.kind === 'file' && (
        <ImportFileStep
          notices={view.notices}
          onUploaded={(preview) => {
            held.current = preview
            writeImportRecord({ staging_id: preview.staging_id, phase: 'previewed' })
            setCancelError(null)
            go({ kind: 'preview', preview }, { wizard: 'import', step: 'preview', staging_id: preview.staging_id })
          }}
        />
      )}
      {view.kind === 'reloading' && (
        <section className="flex flex-col gap-[var(--space-3)]">
          <StepHeading>Checking the upload</StepHeading>
          <div className="flex items-center gap-[var(--space-2)]">
            <Spinner decorative />
            <BusyNotice>Checking what happened to the upload…</BusyNotice>
          </div>
        </section>
      )}
      {view.kind === 'preview' && (
        <ImportPreviewStep
          preview={view.preview}
          cancelError={cancelError}
          cancelling={cancelling}
          onCancel={() => void cancelUpload(view.preview.staging_id)}
          onContinue={() =>
            go(
              { kind: 'confirm', preview: view.preview },
              { wizard: 'import', step: 'confirm', staging_id: view.preview.staging_id },
            )
          }
        />
      )}
      {view.kind === 'confirm' && (
        <ImportConfirmStep
          stagingId={view.preview.staging_id}
          preview={view.preview}
          cancelError={cancelError}
          cancelling={cancelling}
          onCancel={() => void cancelUpload(view.preview.staging_id)}
          onBack={() =>
            go(
              { kind: 'preview', preview: view.preview },
              { wizard: 'import', step: 'preview', staging_id: view.preview.staging_id },
            )
          }
          onToFile={(notice) => {
            held.current = null
            show({ kind: 'file', notices: notice ? [notice] : [] }, FILE_ADDRESS)
          }}
          onApplied={(result, workspaceName) => {
            const created = result.created[0]
            const record = readImportRecord()
            writeImportRecord({
              staging_id: view.preview.staging_id,
              phase: 'done',
              workspace_id: created?.workspace_id ?? record?.workspace_id,
              workspace_name: workspaceName,
              result: {
                name: created?.name ?? '',
                new_id: created?.new_id ?? '',
                workspace_name: workspaceName,
                activation: result.activation,
              },
            })
            held.current = null
            go(
              { kind: 'result', result, workspaceName },
              { wizard: 'import', step: 'result', staging_id: view.preview.staging_id },
            )
          }}
        />
      )}
      {view.kind === 'result' && (
        <ImportResultStep
          result={view.result}
          workspaceName={view.workspaceName}
          onOpenTeam={() => {
            const workspaceId = view.result.created[0]?.workspace_id
            if (workspaceId) openTeam(workspaceId)
          }}
          onImportAnother={importAnother}
          onOpenChat={(agentId) => {
            const workspaceId = view.result.created[0]?.workspace_id
            if (workspaceId) void openChat(workspaceId, agentId)
          }}
        />
      )}
      {view.kind === 'finished' && (
        <section className="flex flex-col gap-[var(--space-3)]">
          <StepHeading>Import finished</StepHeading>
          <p className="text-[length:var(--type-body-size)] text-[var(--color-secondary)]">
            {`${view.summary.name} was added to ${view.summary.workspace_name}.`}
          </p>
          <div className="flex flex-wrap gap-[var(--space-2)]">
            {view.workspaceId && (
              <Button variant="outline" onClick={() => openTeam(view.workspaceId as string)}>
                Open Team
              </Button>
            )}
            <Button variant="outline" onClick={importAnother}>
              Import another
            </Button>
          </div>
        </section>
      )}
      {view.kind === 'uncertain' && (
        <section className="flex flex-col gap-[var(--space-3)]">
          <StepHeading>Upload not confirmed</StepHeading>
          <ImportUncertain workspaceId={view.workspaceId} onStartOver={startOver} />
        </section>
      )}
      {view.kind === 'unknown' && (
        <section className="flex flex-col gap-[var(--space-3)]">
          <StepHeading>Import not confirmed</StepHeading>
          <ImportOutcomeUnknown workspaceId={view.workspaceId} onStartOver={startOver} />
        </section>
      )}
    </>
  )
}
