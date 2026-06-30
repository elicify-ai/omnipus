// ChatImage — shared image component with hover-revealed action toolbar and lightbox.
// Used by MarkdownImage (markdown-shared.tsx) and InlineMedia / historical media
// (ChatScreen.tsx). Presentational: no store reads, no API calls.

import { useState, useMemo } from 'react'
import { Copy, ShareNetwork, DownloadSimple, ArrowsOutSimple } from '@phosphor-icons/react'
import { ImageLightbox } from './image-lightbox'
import { MediaActionToolbar } from './MediaActionToolbar'
import type { MediaAction } from './MediaActionToolbar'
import {
  canCopyImage,
  canShareFiles,
  copyImageBlob,
  shareBlob,
  downloadBlob,
  fetchImageBlob,
} from './media-actions'

// ── Local helper: fetch + ensure PNG ─────────────────────────────────────────

async function toPng(src: string): Promise<Blob> {
  const b = await fetchImageBlob(src)
  if (b.type === 'image/png') return b
  const bmp = await createImageBitmap(b)
  const c = document.createElement('canvas')
  c.width = bmp.width
  c.height = bmp.height
  c.getContext('2d')!.drawImage(bmp, 0, 0)
  return new Promise<Blob>((res, rej) =>
    c.toBlob((x) => (x ? res(x) : rej(new Error('png convert failed'))), 'image/png'),
  )
}

// ── Props ─────────────────────────────────────────────────────────────────────

// Per-image enlarge state keyed by src — survives the virtualized list's remounts, which
// would otherwise reset a plain useState and silently close the lightbox while it's open.
const lightboxOpenCache = new Set<string>()

export interface ChatImageProps {
  src: string
  alt?: string
  filename?: string
  className?: string
}

// ── ChatImage ─────────────────────────────────────────────────────────────────

export function ChatImage({ src, alt, filename, className }: ChatImageProps) {
  const [open, setOpen] = useState(() => lightboxOpenCache.has(src))
  const openLightbox = () => {
    lightboxOpenCache.add(src)
    setOpen(true)
  }
  const closeLightbox = () => {
    lightboxOpenCache.delete(src)
    setOpen(false)
  }

  // Stable fallback filename for downloads / sharing
  const name = filename || alt || 'image.png'

  // Build hover-overlay actions (memoized on src and name so they don't
  // recreate on every render of a parent that passes stable props).
  const overlayActions = useMemo<MediaAction[]>(() => {
    const acts: MediaAction[] = []

    if (canCopyImage()) {
      acts.push({
        icon: <Copy size={14} />,
        label: 'Copy image',
        transientLabel: 'Copied',
        onClick: async () => copyImageBlob(await toPng(src)),
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
      onClick: () => {
        lightboxOpenCache.add(src)
        setOpen(true)
      },
    })

    return acts
  }, [src, name])

  // Lightbox toolbar: same copy/share/download but no Enlarge (already enlarged)
  const lightboxActions = useMemo<MediaAction[]>(
    () => overlayActions.filter((a) => a.label !== 'Enlarge'),
    [overlayActions],
  )

  return (
    <>
      {/* Wrapper with hover group for overlay toolbar */}
      <div className={`relative group/chatimg inline-block${className ? ` ${className}` : ''}`}>
        <img
          src={src}
          alt={alt || ''}
          loading="lazy"
          onClick={openLightbox}
          role="button"
          tabIndex={0}
          onKeyDown={(e) => e.key === 'Enter' && openLightbox()}
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

      {/* Lightbox portal */}
      {open && (
        <ImageLightbox
          src={src}
          alt={alt}
          onClose={closeLightbox}
          toolbar={<MediaActionToolbar variant="bar" actions={lightboxActions} />}
        />
      )}
    </>
  )
}
