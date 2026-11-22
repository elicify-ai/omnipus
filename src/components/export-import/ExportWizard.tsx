// ExportWizard — the export wizard shell (spec "Export · Choose agent",
// "Review", "Download"): the Stepper plus the current step.
//
// The step lives in the address (`wizard=export&step=…&plan_id=…`). The shell
// keeps a local copy initialised from the search, so a step change shows at
// once, and it also writes the address so a reload lands on the same step.
// The local copy follows the search only when the search itself changes.
import { useEffect, useRef, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { Stepper } from '@/components/ui/stepper'
import type { ExportImportSearch } from '@/routes/_app/export-import'
import { EXPORT_IMPORT_PATH, EXPORT_STEPS } from './shared'
import { ExportChooseStep, EXPORT_EXPIRED_NOTICE } from './ExportChooseStep'
import { ExportReviewStep } from './ExportReviewStep'
import { ExportDownloadStep } from './ExportDownloadStep'

type ExportStep = 'choose' | 'review' | 'download'

interface ExportView {
  step: ExportStep
  planId?: string
  notice?: string
  /** The agent chosen before Review, kept when going Back (spec "Back navigation"). */
  chosenId?: string
}

function viewFromSearch(search: ExportImportSearch): ExportView {
  const step = search.step === 'review' || search.step === 'download' ? search.step : 'choose'
  // A Review or Download address without a review to read starts at Choose.
  if (step !== 'choose' && !search.plan_id) return { step: 'choose' }
  return { step, planId: search.plan_id }
}

export function ExportWizard({ search }: { search: ExportImportSearch }) {
  const navigate = useNavigate()
  const [view, setView] = useState<ExportView>(() => viewFromSearch(search))

  const searchKey = JSON.stringify(search)
  const lastKey = useRef(searchKey)
  useEffect(() => {
    if (lastKey.current === searchKey) return
    lastKey.current = searchKey
    setView(viewFromSearch(search))
    // Only the address change re-seeds the view; `search` is read through it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchKey])

  const preselect = search.preselect

  function go(next: ExportView) {
    setView(next)
    void navigate({
      to: EXPORT_IMPORT_PATH,
      search: {
        wizard: 'export',
        step: next.step,
        scope: 'agents',
        ...(next.planId ? { plan_id: next.planId } : {}),
        ...(preselect !== undefined ? { preselect } : {}),
      },
    })
  }

  // The review is gone (expired, evicted, or the gateway restarted): back to
  // Choose with the notice; the agent stays ticked only through the address.
  function expired() {
    go({ step: 'choose', notice: EXPORT_EXPIRED_NOTICE })
  }

  const current = view.step === 'choose' ? 0 : view.step === 'review' ? 1 : 2

  return (
    <>
      <Stepper steps={EXPORT_STEPS} current={current} label="Export steps" />
      {view.step === 'choose' && (
        <ExportChooseStep
          preselect={preselect}
          notice={view.notice}
          initialChosen={view.chosenId}
          onReviewed={(planId) => go({ step: 'review', planId })}
        />
      )}
      {view.step === 'review' && view.planId && (
        <ExportReviewStep
          planId={view.planId}
          onBack={(agentId) => go({ step: 'choose', chosenId: agentId })}
          onDownload={() => go({ step: 'download', planId: view.planId })}
          onExpired={expired}
        />
      )}
      {view.step === 'download' && view.planId && (
        <ExportDownloadStep
          planId={view.planId}
          onBackToReview={() => go({ step: 'review', planId: view.planId })}
          onExpired={expired}
          onExportAnother={() => {
            setView({ step: 'choose' })
            void navigate({ to: EXPORT_IMPORT_PATH, search: { wizard: 'export', step: 'choose', scope: 'agents' } })
          }}
          onDone={() => void navigate({ to: EXPORT_IMPORT_PATH, search: {} })}
        />
      )}
    </>
  )
}
