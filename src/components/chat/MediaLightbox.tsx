// MediaLightbox — the single, app-root-mounted enlarged-media overlay.
//
// Reads `mediaLightbox` from the UI store and renders <ImageLightbox> with a
// kind-appropriate action toolbar (image → copy/share/download; svg → copy /
// download as PNG). Mounted once at the app root (AppShell), NOT inside a chat
// row: the virtualized message list periodically remounts its rows, so a per-row
// lightbox was torn down mid-view — and keying its open-state by content (src/svg)
// let two identical images/diagrams share state (one self-opening). A single
// store-owned instance removes both failure modes. An office-preview page
// group opens the paged PreviewPageViewer instead (office-preview spec §5.4).

import { useMemo } from 'react'
import { Copy, ShareNetwork, DownloadSimple } from '@phosphor-icons/react'
import { ImageLightbox } from './image-lightbox'
import { PreviewPageViewer } from './PreviewPageViewer'
import { MediaActionToolbar, type MediaAction } from './MediaActionToolbar'
import { useUiStore } from '@/store/ui'
import {
  canCopyImage,
  canShareFiles,
  copyImageBlob,
  shareBlob,
  downloadBlob,
  fetchImageBlob,
  fetchImagePng,
  svgToPngBlob,
} from './media-actions'

export function MediaLightbox() {
  const content = useUiStore((s) => s.mediaLightbox)
  const close = useUiStore((s) => s.closeMediaLightbox)

  // Toolbar derived from the media kind. Feature-gated: Copy hides where the
  // clipboard image API is unavailable, Share hides without the Web Share API.
  const actions = useMemo<MediaAction[]>(() => {
    // The office-preview page viewer builds its own per-page toolbar.
    if (!content || content.kind === 'pages') return []
    const acts: MediaAction[] = []

    if (content.kind === 'image') {
      const name = content.filename || content.alt || 'image.png'
      if (canCopyImage()) {
        acts.push({
          icon: <Copy size={14} />,
          label: 'Copy image',
          transientLabel: 'Copied',
          onClick: async () => copyImageBlob(await fetchImagePng(content.src)),
        })
      }
      if (canShareFiles()) {
        acts.push({
          icon: <ShareNetwork size={14} />,
          label: 'Share',
          onClick: async () => shareBlob(await fetchImageBlob(content.src), name),
        })
      }
      acts.push({
        icon: <DownloadSimple size={14} />,
        label: 'Download',
        onClick: async () => downloadBlob(await fetchImageBlob(content.src), name),
      })
      return acts
    }

    // svg (diagram) — already-sanitized markup; copy/download as a rasterized PNG.
    const name = content.filename || 'diagram.png'
    if (canCopyImage()) {
      acts.push({
        icon: <Copy size={14} />,
        label: 'Copy diagram as PNG',
        transientLabel: 'Copied',
        onClick: async () => copyImageBlob(await svgToPngBlob(content.svg)),
      })
    }
    acts.push({
      icon: <DownloadSimple size={14} />,
      label: 'Download diagram as PNG',
      onClick: async () => downloadBlob(await svgToPngBlob(content.svg), name),
    })
    return acts
  }, [content])

  if (!content) return null

  // Office-preview thumbnail group (spec §5.3/§5.4): keyed by group, never by
  // page URL, so Left/Right page changes keep the same viewer instance.
  if (content.kind === 'pages') {
    return <PreviewPageViewer key={`pages:${content.groupKey}`} content={content} onClose={close} />
  }

  // key forces fresh zoom/pan state when the enlarged media changes.
  const key = content.kind === 'image' ? `img:${content.src}` : `svg:${content.svg.length}`

  return (
    <ImageLightbox
      key={key}
      src={content.kind === 'image' ? content.src : undefined}
      alt={content.kind === 'image' ? content.alt : undefined}
      svg={content.kind === 'svg' ? content.svg : undefined}
      title={content.kind === 'svg' ? content.title : undefined}
      onClose={close}
      toolbar={<MediaActionToolbar variant="bar" actions={actions} />}
    />
  )
}
