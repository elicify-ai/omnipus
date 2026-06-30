// ChatImage — shared image component with hover-revealed action toolbar and a
// click-to-enlarge handoff to the global MediaLightbox. Used by MarkdownImage
// (markdown-shared.tsx) and InlineMedia / historical media / user attachments
// (ChatScreen.tsx). Presentational: no API calls; the only store touch is
// opening the app-root lightbox.

import { useMemo } from 'react'
import { Copy, ShareNetwork, DownloadSimple, ArrowsOutSimple } from '@phosphor-icons/react'
import { MediaActionToolbar } from './MediaActionToolbar'
import type { MediaAction } from './MediaActionToolbar'
import { useUiStore } from '@/store/ui'
import { isDisplayableImageSrc } from '@/lib/url-safe'
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

  // Reject unsafe schemes (javascript:, blob:, file:, …) at the render boundary —
  // defense-in-depth alongside fetchImageBlob's gates, and covers the media-frame
  // call sites that don't pre-filter the URL the way the markdown path does. Resolves
  // relative URLs so same-origin uploads (/api/v1/uploads/…) are permitted.
  if (!isDisplayableImageSrc(src)) {
    return alt ? <span className="text-xs text-[var(--color-muted)] italic">[image: {alt}]</span> : null
  }

  return (
    <div className={`relative group/chatimg inline-block${className ? ` ${className}` : ''}`}>
      <img
        src={src}
        alt={alt || ''}
        loading="lazy"
        onClick={enlarge}
        role="button"
        tabIndex={0}
        onKeyDown={(e) => e.key === 'Enter' && enlarge()}
        aria-label={alt ? `View: ${alt}` : 'View image'}
        className="max-w-full rounded-md cursor-zoom-in border border-[var(--color-border)] hover:border-[var(--color-accent)]/50 transition-colors block"
      />

      {/* Hover-revealed overlay toolbar */}
      <MediaActionToolbar
        variant="overlay"
        actions={overlayActions}
        className="absolute top-2 right-2 opacity-0 group-hover/chatimg:opacity-100 focus-within:opacity-100 transition-opacity"
      />
    </div>
  )
}
