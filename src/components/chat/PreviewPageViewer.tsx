// PreviewPageViewer — the enlarged viewer for one office-preview thumbnail
// group (office-preview spec §5.3/§5.4). Mounted by the app-root
// MediaLightbox (kind 'pages'), keyed by group — never by page URL — so
// Left/Right change the page without remounting.
//
// Focus (§5.4), all on the catalogued Dialog primitive (no second modal
// system): on open, focus moves to the image region, labelled by the
// caption; Tab/Shift-Tab stay inside (the dialog's focus containment);
// Escape or Close returns focus to the opening thumbnail, else its group,
// else the chat input (restorePreviewFocus — the thumbnail's row may have
// been virtualized away). Each page change announces "Page n of N" politely;
// at the first/last page the arrow key does nothing and the matching button
// reports disabled (aria-disabled, so a focused button never drops focus).
//
// States: loading shows a skeleton; a failed page fetch shows an error panel
// with Retry (re-fetch) inside the viewer; Left/Right keep working.

import { useCallback, useId, useRef, useState, type KeyboardEvent } from 'react'
import { CaretLeft, CaretRight, DownloadSimple } from '@phosphor-icons/react'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { IconButton } from '@/components/ui/icon-button'
import { ErrorState } from '@/components/ui/error-state'
import { Skeleton } from '@/components/ui/skeleton'
import type { PreviewPagesLightboxContent } from '@/store/ui'
import { MediaActionToolbar, type MediaAction } from './MediaActionToolbar'
import { downloadBlob, fetchImageBlob } from './media-actions'
import { restorePreviewFocus } from './previewGroups'

type LoadState = 'loading' | 'loaded' | 'error'

export interface PreviewPageViewerProps {
  content: PreviewPagesLightboxContent
  onClose: () => void
}

export function PreviewPageViewer({ content, onClose }: PreviewPageViewerProps) {
  const { pages, groupKey, openerPage } = content
  const lastIndex = pages.length - 1
  const [index, setIndex] = useState(() => Math.min(Math.max(content.index, 0), Math.max(lastIndex, 0)))
  // Load state per page src + a retry counter that re-mounts only the <img>.
  const [load, setLoad] = useState<{ key: string; state: LoadState }>({ key: '', state: 'loading' })
  const [attempt, setAttempt] = useState(0)
  // Empty until the first page change: opening is announced by the dialog
  // itself (its label), each later change by this polite region.
  const [announcement, setAnnouncement] = useState('')
  const regionRef = useRef<HTMLDivElement | null>(null)
  const captionId = useId()

  const page = pages[index]
  const total = content.pageCount > 0 ? content.pageCount : pages.length
  const loadKey = `${page?.src ?? ''}#${attempt}`
  const loadState: LoadState = load.key === loadKey ? load.state : 'loading'

  const goTo = useCallback(
    (next: number) => {
      if (next < 0 || next > lastIndex || next === index) return
      setIndex(next)
      const target = pages[next]
      if (target) setAnnouncement(`Page ${target.pageNumber} of ${total}`)
    },
    [index, lastIndex, pages, total],
  )

  const handleKeyDown = useCallback(
    (event: KeyboardEvent<HTMLDivElement>) => {
      if (event.ctrlKey || event.metaKey || event.altKey) return
      if (event.key === 'ArrowLeft') {
        event.preventDefault()
        goTo(index - 1)
      } else if (event.key === 'ArrowRight') {
        event.preventDefault()
        goTo(index + 1)
      }
    },
    [goTo, index],
  )

  const download: MediaAction[] = page
    ? [{
        icon: <DownloadSimple size={14} />,
        label: 'Download page',
        onClick: async () => downloadBlob(await fetchImageBlob(page.src), page.filename || `page-${page.pageNumber}.png`),
      }]
    : []

  const atFirst = index <= 0
  const atLast = index >= lastIndex

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent
        data-testid="preview-page-viewer"
        aria-describedby={undefined}
        className="max-w-none w-[calc(100vw-2rem)] h-[calc(100dvh-2rem)] max-h-none grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden"
        onOpenAutoFocus={(event) => {
          event.preventDefault()
          regionRef.current?.focus()
        }}
        onCloseAutoFocus={(event) => {
          event.preventDefault()
          restorePreviewFocus(groupKey, openerPage)
        }}
        onKeyDown={handleKeyDown}
      >
        <DialogTitle
          id={captionId}
          className="pr-[var(--space-7)] truncate text-[length:var(--type-body-compact-size)] font-medium leading-[var(--type-body-compact-line-height)]"
        >
          {page?.caption ?? ''}
        </DialogTitle>

        <div
          ref={regionRef}
          tabIndex={0}
          role="group"
          aria-labelledby={captionId}
          data-testid="preview-page-region"
          data-page-number={page?.pageNumber}
          className="relative flex min-h-0 items-center justify-center overflow-hidden rounded-md"
        >
          {page && loadState === 'error' ? (
            <ErrorState
              message={`Page ${page.pageNumber} could not be loaded.`}
              onRetry={() => setAttempt((n) => n + 1)}
            />
          ) : page ? (
            <>
              {loadState === 'loading' && (
                <Skeleton data-testid="preview-page-loading" className="absolute inset-0" />
              )}
              <img
                key={loadKey}
                src={page.src}
                alt={page.caption}
                draggable={false}
                onLoad={() => setLoad({ key: loadKey, state: 'loaded' })}
                onError={() => {
                  console.warn(`[PreviewPageViewer] Page image failed to load: ${page.src}`)
                  setLoad({ key: loadKey, state: 'error' })
                }}
                className={`max-h-full max-w-full object-contain ${loadState === 'loading' ? 'invisible' : ''}`}
              />
            </>
          ) : null}
        </div>

        <div className="flex items-center justify-between gap-[var(--space-2)]">
          <IconButton
            variant="secondary"
            aria-label="Previous page"
            aria-disabled={atFirst || undefined}
            className="aria-disabled:opacity-50"
            onClick={() => goTo(index - 1)}
          >
            <CaretLeft size={16} weight="bold" />
          </IconButton>
          <div className="flex items-center gap-[var(--space-2)] min-w-0">
            <span className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
              {page ? `Page ${page.pageNumber} of ${total}` : ''}
            </span>
            <MediaActionToolbar variant="bar" actions={download} />
          </div>
          <IconButton
            variant="secondary"
            aria-label="Next page"
            aria-disabled={atLast || undefined}
            className="aria-disabled:opacity-50"
            onClick={() => goTo(index + 1)}
          >
            <CaretRight size={16} weight="bold" />
          </IconButton>
        </div>

        <div role="status" aria-live="polite" aria-atomic="true" className="sr-only" data-testid="preview-page-announcement">
          {announcement}
        </div>
      </DialogContent>
    </Dialog>
  )
}
