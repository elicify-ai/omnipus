// LibraryImagePreview — plain <img> for the Library preview pane
// (library-spec.md section 4), plus a full-screen action that opens the SAME
// zoomable media viewer chat images use (D18 scope extension,
// docs/internal/design/components/zoomable-view.md: "Library image preview
// gets a new full-screen button that opens the same media viewer ... already
// used for chat images"). No annotate/crop — those belong to the chat
// attachment surface (chat/ChatImage.tsx), which this pane must NOT reach
// into (file ownership boundary). Sensible max sizing + a dark canvas + real
// alt text.
//
// Also the "image" kind's inline embed renderer (ADR-083 spec, US-3 /
// EMB-025..EMB-031) — the SAME component the pane uses, per EMB-027. Only
// `variant` may change between the two call sites (EMB-028): the pane fills
// its ancestor (`h-full`/`flex-1`, needing a bounded parent), the inline
// embed sizes to the picture's own content so it sits in a note's text flow.
// `width` implements EMB-030 (a size given after a bar in the embed notation
// applies to pictures only) — the resolver that parses that notation is out
// of this file's scope; this component only ever applies a width it is
// handed, and never on the `pane` variant, where the pane's own bounds — not
// the embed notation — decide size. The full-screen action is `pane`-only —
// portalled into the Library preview pane's single header row
// (previewHeaderSlot), which an inline note embed never provides, so it
// renders nothing there rather than a stray button per embedded picture.

import { useEffect, useState } from 'react'
import { ArrowClockwise, ArrowsOutSimple, WarningCircle } from '@phosphor-icons/react'
import { libraryDownloadUrl } from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'
import type { LibraryPreviewVariant } from './libraryPreviewVariant'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { PreviewHeaderPortal } from './previewHeaderSlot'
import { useUiStore } from '@/store/ui'

interface LibraryImagePreviewProps {
  workspaceId: string
  entry: LibraryEntry
  variant?: LibraryPreviewVariant
  /** Pixel width from an embed's `|400` modifier (EMB-030). Ignored on the
   *  `pane` variant. Never rendered as a caption or as alt text — the img's
   *  `alt` stays the file name regardless. */
  width?: number
}

export function LibraryImagePreview({ workspaceId, entry, variant = 'pane', width }: LibraryImagePreviewProps) {
  const inline = variant === 'inline'
  const openMediaLightbox = useUiStore((s) => s.openMediaLightbox)
  // UAT D-107 (2026-09-13): a row for a file another tab had deleted opened
  // to the BROWSER'S OWN broken-image glyph beside the alt text — no message,
  // no placeholder. Row thumbnails already had an onError fallback; the pane
  // did not. `attempt` is a cache-busting nonce for the Retry button, so a
  // second try is a real re-request rather than the cached 404.
  const [failed, setFailed] = useState(false)
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    setFailed(false)
    setAttempt(0)
  }, [workspaceId, entry.path])
  const base = libraryDownloadUrl(workspaceId, entry.path)
  const src = attempt === 0 ? base : `${base}${base.includes('?') ? '&' : '?'}retry=${attempt}`
  if (failed) {
    return (
      <div
        className={
          inline
            ? 'flex items-center justify-center'
            : 'flex flex-1 min-h-0 items-center justify-center overflow-auto bg-[var(--color-surface-0)] p-[var(--space-3)]'
        }
        data-testid="library-image-preview"
        data-variant={variant}
      >
        <div
          role="status"
          data-testid="library-image-unavailable"
          className="flex flex-col items-center gap-[var(--space-2)] rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/5 px-[var(--space-2-5)] py-[var(--space-4)] text-center text-[length:var(--type-utility-xs-size)] text-[var(--color-warning)]"
        >
          <WarningCircle size={16} weight="fill" />
          <span>
            “{entry.name}” could not be loaded. It may have been deleted or moved since this list
            was read, or your session may have expired.
          </span>
          <Button
            variant="outline"
            onClick={() => {
              setFailed(false)
              setAttempt((n) => n + 1)
            }}
            data-testid="library-image-retry"
            className="h-auto gap-[var(--space-1)] rounded border-[var(--color-warning)]/60 px-[var(--space-2)] py-[var(--space-0-5)] text-[color:var(--color-warning)] text-[length:var(--type-caption-size)] font-medium hover:bg-[var(--color-warning)]/10"
          >
            <ArrowClockwise size={12} /> Try again
          </Button>
        </div>
      </div>
    )
  }
  return (
    <div
      className={
        inline
          ? 'flex items-center justify-center'
          : 'flex flex-1 min-h-0 items-center justify-center overflow-auto bg-[var(--color-surface-0)] p-[var(--space-3)]'
      }
      data-testid="library-image-preview"
      data-variant={variant}
    >
      {/* pane-only: portals into the Library preview pane's single header
          row (previewHeaderSlot). No-ops for the inline embed variant,
          which has no such slot to portal into. */}
      {!inline && (
        <PreviewHeaderPortal>
          <IconButton
            onClick={() => openMediaLightbox({ kind: 'image', src, alt: entry.name, filename: entry.name })}
            aria-label="Open full screen"
            title="Open full screen"
            data-testid="library-image-preview-fullscreen"
            className="flex h-7 w-7 shrink-0 items-center justify-center rounded transition-colors text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] disabled:cursor-not-allowed disabled:opacity-40 pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]"
          >
            <ArrowsOutSimple size={15} />
          </IconButton>
        </PreviewHeaderPortal>
      )}
      <img
        src={src}
        alt={entry.name}
        onError={() => setFailed(true)}
        className={inline ? 'h-auto max-w-full rounded-md object-contain' : 'max-h-full max-w-full rounded-md object-contain'}
        {...(inline && width !== undefined ? { width } : {})}
      />
    </div>
  )
}
