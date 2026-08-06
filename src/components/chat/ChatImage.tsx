// ChatImage — shared image component with hover-revealed action toolbar and a
// click-to-enlarge handoff to the global MediaLightbox. Used by MarkdownImage
// (markdown-shared.tsx) and InlineMedia / historical media / user attachments
// (ChatScreen.tsx). Presentational: no API calls; the only store touch is
// opening the app-root lightbox.

import { useCallback, useMemo, useState } from 'react'
import { Copy, ShareNetwork, DownloadSimple, ArrowsOutSimple, ArrowsClockwise, ImageBroken } from '@phosphor-icons/react'
import { MediaActionToolbar } from './MediaActionToolbar'
import type { MediaAction } from './MediaActionToolbar'
import { useUiStore } from '@/store/ui'
import { isDisplayableImageSrc } from '@/lib/url-safe'
import { cn } from '@/lib/utils'
import {
  canCopyImage,
  canShareFiles,
  copyImageBlob,
  shareBlob,
  downloadBlob,
  fetchImageBlob,
  fetchImagePng,
} from './media-actions'

export interface ChatImageProps {
  src: string
  alt?: string
  filename?: string
  className?: string
}

export function ChatImage({ src, alt, filename, className }: ChatImageProps) {
  const openMediaLightbox = useUiStore((s) => s.openMediaLightbox)
  const addToast = useUiStore((s) => s.addToast)

  // Track whether the <img> load failed (e.g. a 404 from a stale/mismatched
  // media route) so we can fall back to a visible placeholder instead of the
  // browser's silent broken-image glyph. Mirrors AttachmentCard's imgError
  // convention (AttachmentCard.tsx).
  //
  // Keyed on `src`: markdown-shared.tsx's MarkdownImage renders ChatImage
  // WITHOUT a React key, so a re-render that swaps in a new, valid `src` at
  // the same tree position must not keep showing a stale error card. Deriving
  // from props during render (comparing against the last-seen src, resetting
  // when it differs) is the documented React pattern for this — it avoids the
  // extra render + flash a useEffect-based reset would cause.
  const [errorFor, setErrorFor] = useState<{ src: string; error: boolean }>({ src, error: false })
  if (errorFor.src !== src) {
    setErrorFor({ src, error: false })
  }
  const imgError = errorFor.error
  const handleImgError = useCallback(() => {
    console.warn(`[ChatImage] Image failed to load: ${src}`)
    addToast({ message: `Image failed to load: ${filename || alt || src}`, variant: 'error' })
    setErrorFor({ src, error: true })
  }, [src, alt, filename, addToast])
  const retry = useCallback(() => setErrorFor({ src, error: false }), [src])

  // Stable fallback filename for downloads / sharing.
  const name = filename || alt || 'image.png'

  const enlarge = () => openMediaLightbox({ kind: 'image', src, alt, filename })

  // Hover-overlay actions (memoized on the inputs they close over).
  const overlayActions = useMemo<MediaAction[]>(() => {
    const acts: MediaAction[] = []

    if (canCopyImage()) {
      acts.push({
        icon: <Copy size={14} />,
        label: 'Copy image',
        transientLabel: 'Copied',
        onClick: async () => copyImageBlob(await fetchImagePng(src)),
      })
    }

    if (canShareFiles()) {
      acts.push({
        icon: <ShareNetwork size={14} />,
        label: 'Share',
        onClick: async () => shareBlob(await fetchImageBlob(src), name),
      })
    }

    acts.push({
      icon: <DownloadSimple size={14} />,
      label: 'Download',
      onClick: async () => downloadBlob(await fetchImageBlob(src), name),
    })

    acts.push({
      icon: <ArrowsOutSimple size={14} />,
      label: 'Enlarge',
      onClick: () => openMediaLightbox({ kind: 'image', src, alt, filename }),
    })

    return acts
  }, [src, alt, filename, name, openMediaLightbox])

  // Error-state actions: Copy / Share / Download issue their OWN independent
  // fetch() (media-actions.ts) and Enlarge mounts a fresh <img> inside the
  // lightbox (image-lightbox.tsx) — none of them replay the failed <img> tag's
  // cached load, so for a transient failure (dev-preview still booting, one
  // dropped request) they can still succeed. Retry just re-mounts the <img> to
  // give the src another attempt directly inline.
  const errorActions = useMemo<MediaAction[]>(
    () => [{ icon: <ArrowsClockwise size={14} />, label: 'Retry', onClick: retry }, ...overlayActions],
    [retry, overlayActions],
  )

  // Reject unsafe schemes (javascript:, blob:, file:, …) at the render boundary —
  // defense-in-depth alongside fetchImageBlob's gates, and covers the media-frame
  // call sites that don't pre-filter the URL the way the markdown path does. Resolves
  // relative URLs so same-origin uploads (/api/v1/uploads/…) are permitted.
  if (!isDisplayableImageSrc(src)) {
    return alt ? <span className="text-xs text-[var(--color-muted)] italic">[image: {alt}]</span> : null
  }

  // The <img> failed to load (404/network error) — degrade to a visible
  // file-card-style placeholder instead of a silent broken-image glyph.
  // Recovery actions stay available: Copy/Share/Download issue their own
  // independent fetch() and Enlarge mounts a fresh <img> in the lightbox, so
  // none of them are dead ends for a transient failure — plus an explicit
  // Retry to re-attempt the inline <img> itself. The failure is also
  // surfaced via addToast (handleImgError above), matching the sibling
  // upload-failure convention in omnipus-runtime.ts.
  if (imgError) {
    return (
      <div
        className={cn(
          'flex flex-col gap-2 pl-2 pr-3 py-2 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] max-w-[220px]',
          className,
        )}
        title={name}
      >
        <div className="flex items-center gap-2.5">
          <div className="shrink-0 w-9 h-9 rounded-md flex items-center justify-center bg-[var(--color-error)]/10 text-[var(--color-error)]">
            <ImageBroken size={20} weight="fill" />
          </div>
          <div className="min-w-0">
            <p className="truncate text-xs font-medium text-[var(--color-secondary)]">{name}</p>
            <p className="text-[10px] uppercase tracking-wide text-[var(--color-muted)]">Image unavailable</p>
          </div>
        </div>
        <MediaActionToolbar
          variant="bar"
          actions={errorActions}
          className="pt-1.5 border-t border-[var(--color-border)] flex-wrap"
        />
      </div>
    )
  }

  return (
    <div className={`relative group/chatimg inline-block${className ? ` ${className}` : ''}`}>
      <img
        src={src}
        alt={alt || ''}
        loading="lazy"
        onClick={enlarge}
        onError={handleImgError}
        role="button"
        tabIndex={0}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            enlarge()
          } else if (e.key === ' ') {
            e.preventDefault()
            enlarge()
          }
        }}
        aria-label={alt ? `View: ${alt}` : 'View image'}
        className="max-w-full rounded-md cursor-zoom-in border border-[var(--color-border)] hover:border-[var(--color-accent)]/50 transition-colors block"
      />

      {/* Hover-revealed overlay toolbar. On touch devices (no hover, e.g. iPad)
          the [@media(hover:none)] variant forces it visible so the controls are
          reachable — otherwise group-hover never fires and they'd be invisible. */}
      <MediaActionToolbar
        variant="overlay"
        actions={overlayActions}
        className="absolute top-2 right-2 opacity-0 group-hover/chatimg:opacity-100 focus-within:opacity-100 [@media(hover:none)]:opacity-100 transition-opacity"
      />
    </div>
  )
}
