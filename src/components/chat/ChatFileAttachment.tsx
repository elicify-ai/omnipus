// ChatFileAttachment — a non-image file in a chat message (#1449 FR-002,
// FR-081; spec S-129, S-130): the shared file card plus a keyboard-operable
// download control. Used by the person's and the agent's rows of EVERY chat
// (normal and group alike — one implementation, not a group-only fork).
//
// Download fetches the file first, so a file that can no longer be fetched
// (its chat copy was deleted in the Library, D26) shows an inline error with
// Retry on the card instead of a browser error page; Retry repeats the same
// fetch. It is never a message resend or a repeat of the agent's work.

import { useCallback, useState } from 'react'
import { DownloadSimple } from '@phosphor-icons/react'
import type { MediaAttachment } from '@/store/chat/types'
import { IconButton } from '@/components/ui/icon-button'
import { ErrorState } from '@/components/ui/error-state'
import { logDiagnostic } from '@/lib/telemetry'
import { AttachmentCard } from './AttachmentCard'

type DownloadState = 'idle' | 'loading' | 'error' // not-wire-format: local control state

/** Fetch `url` and hand the bytes to the browser as a download named `filename`. */
async function downloadFile(url: string, filename: string): Promise<void> {
  const response = await fetch(url, { credentials: 'same-origin' })
  if (!response.ok) throw new Error(`HTTP ${response.status}`)
  const blob = await response.blob()
  const href = URL.createObjectURL(blob)
  try {
    const anchor = document.createElement('a')
    anchor.href = href
    anchor.download = filename
    anchor.rel = 'noopener'
    document.body.appendChild(anchor)
    anchor.click()
    anchor.remove()
  } finally {
    // Revoke after the click has been dispatched.
    setTimeout(() => URL.revokeObjectURL(href), 0)
  }
}

export function ChatFileAttachment({ media }: { media: Pick<MediaAttachment, 'url' | 'filename' | 'contentType'> }) {
  const [state, setState] = useState<DownloadState>('idle')
  const download = useCallback(async () => {
    setState('loading')
    try {
      await downloadFile(media.url, media.filename)
      setState('idle')
    } catch (error) {
      // Visible on the card (below) and recorded for diagnosis.
      logDiagnostic('chatFileDownloadFailed', { error: error instanceof Error ? error.message : 'unknown' })
      setState('error')
    }
  }, [media.url, media.filename])
  return (
    <div data-testid="chat-file-attachment" data-file-state={state} className="flex flex-col items-start gap-[var(--space-1)]">
      <AttachmentCard
        filename={media.filename}
        contentType={media.contentType}
        isImage={false}
        removeButton={
          <IconButton
            size="sm"
            variant="ghost"
            aria-label={`Download ${media.filename}`}
            data-testid="chat-file-download"
            aria-disabled={state === 'loading' || undefined}
            onClick={() => { if (state !== 'loading') void download() }}
            className="ml-auto shrink-0 text-[var(--color-muted)] hover:text-[var(--color-secondary)]"
          >
            <DownloadSimple aria-hidden="true" size={14} />
          </IconButton>
        }
      />
      {state === 'error' && (
        <ErrorState
          message={`Could not download ${media.filename}. The file may no longer be available.`}
          onRetry={() => void download()}
          className="items-start py-[var(--space-1)]"
        />
      )}
    </div>
  )
}
