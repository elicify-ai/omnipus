// ExportWizard — the export wizard shell (spec "Export · Choose agent",
// "Review", "Download"): the Stepper plus the current step.
//
// The step lives in the address (`wizard=export&step=…&plan_id=…`); see
// useAddressView for how the local view and the address stay together.
import { Stepper } from '@/components/ui/stepper'
import type { ExportImportSearch } from '@/routes/_app/export-import'
import { EXPORT_STEPS, useAddressView } from './shared'
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
  const { view, go } = useAddressView(search, viewFromSearch)
  const preselect = search.preselect

  function goStep(next: ExportView, keepPreselect = true) {
    go(next, {
      wizard: 'export',
      step: next.step,
      scope: 'agents',
      ...(keepPreselect && preselect !== undefined ? { preselect } : {}),
      ...(next.planId ? { plan_id: next.planId } : {}),
    })
  }

  // The review is gone (expired, evicted, or the gateway restarted): back to
  // Choose with the notice; the agent stays ticked only through the address.
  function expired() {
    goStep({ step: 'choose', notice: EXPORT_EXPIRED_NOTICE })
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
          onReviewed={(planId) => goStep({ step: 'review', planId })}
        />
      )}
      {view.step === 'review' && view.planId && (
        <ExportReviewStep
          planId={view.planId}
          onBack={(agentId) => goStep({ step: 'choose', chosenId: agentId })}
          onDownload={() => goStep({ step: 'download', planId: view.planId })}
          onExpired={expired}
        />
      )}
      {view.step === 'download' && view.planId && (
        <ExportDownloadStep
          planId={view.planId}
          onBackToReview={() => goStep({ step: 'review', planId: view.planId })}
          onExpired={expired}
          onExportAnother={() => goStep({ step: 'choose' }, false)}
          onDone={() => go({ step: 'choose' }, {})}
        />
      )}
    </>
  )
}
