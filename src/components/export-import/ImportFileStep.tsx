// ImportFileStep — choosing the bundle file (spec "Import · Choose file").
//
// A catalogued Button opens a visually hidden file input. A file over the
// 64 MB limit is refused here and never sent. The upload is sent once and
// never resent (retry off here, both transport replay layers off in the
// client); a refusal shows its mapped text in one alert and the step stays,
// and an answer that proves nothing gets the did-not-confirm text.
import { useRef, useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import {
  classifyUploadOutcome,
  isSessionRefusal,
  packageErrorMessage,
  packageErrorText,
  PACKAGE_TEXT,
  refreshSession,
  uploadPackageImport,
  type PackageImportPreview,
} from '@/lib/api/packages'
import { Button } from '@/components/ui/button'
import { Callout, CalloutDescription } from '@/components/ui/callout'
import { Spinner } from '@/components/ui/spinner'
import { BusyNotice, InlineError, StepHeading } from './shared'

/** The most a bundle may weigh (spec: 64 MB); larger files are never sent. */
export const MAX_BUNDLE_BYTES = 67_108_864

interface ImportFileStepProps {
  /** Notices carried in from a reload or an earlier step, shown above the button. */
  notices?: string[]
  onUploaded: (preview: PackageImportPreview) => void
}

export function ImportFileStep({ notices = [], onUploaded }: ImportFileStepProps) {
  const inputRef = useRef<HTMLInputElement>(null)
  const [fileName, setFileName] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  const upload = useMutation({
    mutationFn: (file: File) => uploadPackageImport(file),
    retry: false,
    onSuccess: (preview) => onUploaded(preview),
    onError: (err) => {
      if (isSessionRefusal(err)) {
        setError(PACKAGE_TEXT.sessionUpload)
        void refreshSession()
      } else if (classifyUploadOutcome(err) === 'unknown') {
        setError(PACKAGE_TEXT.unknownUpload)
      } else {
        setError(packageErrorMessage(err, 'import'))
      }
    },
  })

  function onFileChosen(file: File | undefined) {
    if (!file) return
    setFileName(file.name)
    setError(null)
    if (file.size > MAX_BUNDLE_BYTES) {
      setError(packageErrorText.package_too_large)
      return
    }
    upload.mutate(file)
  }

  return (
    <section className="flex flex-col gap-[var(--space-3)]">
      <StepHeading>Choose file</StepHeading>
      {notices.map((notice) => (
        <Callout key={notice} variant="info">
          <CalloutDescription>{notice}</CalloutDescription>
        </Callout>
      ))}
      <input
        ref={inputRef}
        type="file"
        accept=".omnpkg"
        className="sr-only"
        tabIndex={-1}
        aria-hidden="true"
        onChange={(e) => {
          onFileChosen(e.target.files?.[0])
          // The same file can be chosen again after a refusal.
          e.target.value = ''
        }}
      />
      <div className="flex flex-col items-start gap-[var(--space-1)]">
        <Button disabled={upload.isPending} onClick={() => inputRef.current?.click()}>
          Choose a bundle file
        </Button>
        <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
          Bundle files end in .omnpkg
        </span>
      </div>
      {fileName && (
        <p aria-live="polite" className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
          {fileName}
        </p>
      )}
      {upload.isPending && (
        <div className="flex items-center gap-[var(--space-2)]">
          <Spinner decorative />
          <BusyNotice>Checking the bundle…</BusyNotice>
        </div>
      )}
      <InlineError text={error} />
    </section>
  )
}
