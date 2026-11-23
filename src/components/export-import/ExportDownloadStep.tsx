// ExportDownloadStep — the bundle download (spec "Export · Download").
//
// On arrival it re-reads the review straight from the server (never a cached
// copy): a review the server no longer holds goes back to Choose with the
// expired notice and sends no export. Otherwise one export request — never
// resent (retry off here, both transport replay layers off in the client) —
// and the browser saves the returned bundle. "Download again" saves the bytes
// already held; it sends nothing.
import { useEffect, useRef, useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import {
  exportPackage,
  getPackageExportPlan,
  isSessionRefusal,
  packageErrorMessage,
  PACKAGE_TEXT,
  refreshSession,
  type PackageDownload,
} from '@/lib/api/packages'
import { Button } from '@/components/ui/button'
import { Callout, CalloutDescription } from '@/components/ui/callout'
import { Spinner } from '@/components/ui/spinner'
import { BusyNotice, formatBytes, StepHeading } from './shared'
import { isPlanGone } from './ExportReviewStep'

/** Hands the bundle to the browser's download (an object URL on a temporary link). */
function saveBundle(download: PackageDownload): void {
  if (typeof URL.createObjectURL !== 'function') return
  const url = URL.createObjectURL(download.blob)
  const link = document.createElement('a')
  link.href = url
  link.download = download.fileName
  document.body.appendChild(link)
  link.click()
  link.remove()
  setTimeout(() => URL.revokeObjectURL(url), 0)
}

interface ExportDownloadStepProps {
  planId: string
  onBackToReview: () => void
  onExpired: () => void
  onExportAnother: () => void
  onDone: () => void
}

type Phase = 'checking' | 'creating' | 'saved' | 'failed'

export function ExportDownloadStep({ planId, onBackToReview, onExpired, onExportAnother, onDone }: ExportDownloadStepProps) {
  const [phase, setPhase] = useState<Phase>('checking')
  const [errorText, setErrorText] = useState<string | null>(null)
  const started = useRef(false)
  // Tracks mounting apart from the one-shot start, so StrictMode's
  // mount-unmount-mount keeps the answer of the single request.
  const mounted = useRef(false)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])

  const exportMutation = useMutation({
    mutationFn: (agentIds: string[]) => exportPackage(agentIds),
    retry: false,
    onSuccess: (download) => {
      setPhase('saved')
      saveBundle(download)
    },
    onError: (err) => {
      setPhase('failed')
      if (isSessionRefusal(err)) {
        setErrorText(PACKAGE_TEXT.sessionExport)
        void refreshSession()
      } else {
        setErrorText(packageErrorMessage(err, 'export'))
      }
    },
  })
  const { mutate } = exportMutation

  useEffect(() => {
    if (started.current) return
    started.current = true
    getPackageExportPlan(planId).then(
      (plan) => {
        if (!mounted.current) return
        const agentId = plan.elements[0]?.source_id
        if (!agentId) {
          onExpired()
          return
        }
        setPhase('creating')
        mutate([agentId])
      },
      (err: unknown) => {
        if (!mounted.current) return
        if (isPlanGone(err)) {
          onExpired()
          return
        }
        setPhase('failed')
        setErrorText(packageErrorMessage(err, 'export'))
      },
    )
    // One re-read and one export per arrival at this step.
  }, [])

  const download = exportMutation.data

  return (
    <section className="flex flex-col gap-[var(--space-3)]">
      <StepHeading>Download</StepHeading>
      {(phase === 'checking' || phase === 'creating') && (
        <div className="flex items-center gap-[var(--space-2)]">
          <Spinner decorative />
          <BusyNotice>{phase === 'checking' ? 'Checking the review…' : 'Creating the bundle…'}</BusyNotice>
        </div>
      )}
      {phase === 'saved' && download && (
        <>
          <Callout variant="success">
            <CalloutDescription>{`Saved ${download.fileName} (${formatBytes(download.blob.size)}).`}</CalloutDescription>
          </Callout>
          <div className="flex flex-wrap gap-[var(--space-2)]">
            <Button variant="outline" onClick={() => saveBundle(download)}>
              Download again
            </Button>
            <Button variant="outline" onClick={onExportAnother}>
              Export another
            </Button>
            <Button onClick={onDone}>Done</Button>
          </div>
        </>
      )}
      {phase === 'failed' && (
        <>
          <Callout variant="error">
            <CalloutDescription>{errorText}</CalloutDescription>
          </Callout>
          <div className="flex flex-wrap gap-[var(--space-2)]">
            <Button onClick={onBackToReview}>Back to review</Button>
          </div>
        </>
      )}
    </section>
  )
}
