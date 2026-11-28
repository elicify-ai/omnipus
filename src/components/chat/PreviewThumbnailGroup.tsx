// PreviewThumbnailGroup — one office-preview thumbnail group in the chat
// (office-preview spec §5.3/§5.4): the pages of one preview_document call in
// page order, each a button named by its caption that opens the paged viewer
// (PreviewPageViewer, via the app-root MediaLightbox). Partial storage shows
// "thumbnails stored: k of N"; an unavailable outcome shows the persistent
// card with Retry. A thumbnail that fails to load shows ChatImage's "Image
// unavailable" placeholder with Retry — no per-image toast; the group
// announces its load failure once.

import { useCallback, useMemo, useState } from 'react'
import { ChatImage } from './ChatImage'
import { PreviewUnavailableCard } from './PreviewUnavailableCard'
import {
  PREVIEW_GROUP_ATTR,
  PREVIEW_THUMB_ATTR,
  pageCaption,
  requestedPageCount,
  thumbAttrValue,
  type PreviewGroup,
} from './previewGroups'
import { useSessionStore } from '@/store/session'
import { useUiStore, type PreviewViewerPage } from '@/store/ui'

// Groups whose thumbnail load failure was already announced — module scope so
// a virtualized row remount does not announce the same group twice.
const announcedLoadFailures = new Set<string>()

export interface PreviewThumbnailGroupProps {
  group: PreviewGroup
}

export function PreviewThumbnailGroup({ group }: PreviewThumbnailGroupProps) {
  const sessionId = useSessionStore((s) => s.activeSessionId)
  const openMediaLightbox = useUiStore((s) => s.openMediaLightbox)
  const { preview } = group
  const sourceName = preview?.source_name ?? group.pages[0]?.filename ?? 'document'

  const viewerPages = useMemo<PreviewViewerPage[]>(
    () =>
      group.pages.map((p, i) => ({
        src: p.url,
        caption: pageCaption(p, preview),
        pageNumber: p.pageNumber ?? i + 1,
        filename: p.filename,
      })),
    [group.pages, preview],
  )

  const openAt = useCallback(
    (i: number) => {
      const page = viewerPages[i]
      if (!page) return
      openMediaLightbox({
        kind: 'pages',
        groupKey: group.key,
        pages: viewerPages,
        index: i,
        pageCount: preview?.page_count ?? viewerPages.length,
        openerPage: page.pageNumber,
      })
    },
    [viewerPages, openMediaLightbox, group.key, preview],
  )

  const failureKey = `${sessionId ?? ''}|${group.key}`
  const [loadFailureText, setLoadFailureText] = useState('')
  const handleThumbError = useCallback(() => {
    if (announcedLoadFailures.has(failureKey)) return
    announcedLoadFailures.add(failureKey)
    setLoadFailureText(`Some page previews of ${sourceName} could not be loaded`)
  }, [failureKey, sourceName])

  const partial = preview?.outcome === 'partial' && !group.unavailable

  return (
    <div
      {...{ [PREVIEW_GROUP_ATTR]: group.key }}
      tabIndex={-1}
      role="group"
      aria-label={`Page previews of ${sourceName}`}
      data-testid="preview-thumbnail-group"
      data-outcome={group.unavailable ? 'unavailable' : (preview?.outcome ?? 'complete')}
      className="flex flex-col gap-[var(--space-2)] rounded-md"
    >
      {viewerPages.length > 0 && (
        <ul className="flex flex-wrap gap-[var(--space-2)]">
          {viewerPages.map((page, i) => (
            <li key={`${page.src}-${page.pageNumber}`} className="flex w-[120px] flex-col gap-[var(--space-1)]">
              <ChatImage
                src={page.src}
                alt={page.caption}
                filename={page.filename}
                label={page.caption}
                thumbnail
                onEnlarge={() => openAt(i)}
                onError={handleThumbError}
                dataAttributes={{ [PREVIEW_THUMB_ATTR]: thumbAttrValue(group.key, page.pageNumber) }}
                className="min-h-[var(--target-touch-minimum)] w-full"
              />
              <p
                aria-hidden="true"
                className="line-clamp-2 break-words text-[length:var(--type-caption-size)] text-[var(--color-muted)]"
              >
                {page.caption}
              </p>
            </li>
          ))}
        </ul>
      )}
      {partial && preview && (
        <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]" data-testid="preview-partial-note">
          {`thumbnails stored: ${viewerPages.length} of ${requestedPageCount(preview)}`}
        </p>
      )}
      {group.unavailable && (
        <PreviewUnavailableCard
          sessionId={sessionId}
          toolCallId={group.toolCallId}
          sourceName={sourceName}
          reason={group.unavailable.reason}
          marker={group.unavailable.marker}
        />
      )}
      <span role="status" aria-live="polite" aria-atomic="true" className="sr-only" data-testid="preview-load-failure-announcement">
        {loadFailureText}
      </span>
    </div>
  )
}
