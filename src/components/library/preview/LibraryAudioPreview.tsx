// LibraryAudioPreview — plain <audio controls>, extracted from
// LibraryPreviewPane.tsx (ADR-083 embedded-content spec, Step 6 / EMB-105).
//
// It used to live inline in LibraryPreviewPane.tsx because that pane was its
// only mount point and the component was four lines. Step 6 gives it a
// second mount point — the inline note embed (`![[song.mp3]]`) — and EMB-027
// forbids a second copy for that: "There MUST be exactly one renderer per
// kind, shared between the full-screen pane and the inline embed. No
// inline-only copy may be created." So this file is that one definition;
// LibraryPreviewPane.tsx imports it rather than declaring its own.
//
// `variant` follows the exact pattern LibraryImagePreview/LibraryVideoPreview
// already use (libraryPreviewVariant.ts, EMB-028): layout only. Every fetch,
// every state and the non-happy-path render (there isn't one — the raw
// authenticated download URL IS the source, so there is nothing to fetch,
// decode or fail) stay identical between `pane` and `inline`.
//
// EMB-030 — a `|400` size modifier is PICTURES ONLY. Obsidian itself parses a
// size after the bar on audio/video and turns it into an HTML width/height
// attribute that resizes nothing audible; this product's ruling is stricter
// still — EMB-030 requires it be IGNORED at read time on every kind but
// pictures. So this component intentionally takes no `width` prop at all:
// there is nothing here for a caller to (mis)apply it to, and the display
// text a `|400` segment would otherwise be read as an alias for is decided
// entirely upstream, by the resolver that parses the embed notation — not by
// this renderer.

import { libraryDownloadUrl } from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'
import type { LibraryPreviewVariant } from './libraryPreviewVariant'

interface LibraryAudioPreviewProps {
  workspaceId: string
  entry: LibraryEntry
  variant?: LibraryPreviewVariant
}

export function LibraryAudioPreview({ workspaceId, entry, variant = 'pane' }: LibraryAudioPreviewProps) {
  const src = libraryDownloadUrl(workspaceId, entry.path)
  const inline = variant === 'inline'
  return (
    <div
      className={
        inline
          ? 'flex items-center justify-center'
          : 'flex flex-1 min-h-0 items-center justify-center overflow-auto bg-[var(--color-surface-0)] p-4'
      }
      data-testid="library-audio-preview"
      data-variant={variant}
    >
      {/* No <track>: a workspace audio file carries no caption track to attach. */}
      <audio controls src={src} className="w-full max-w-lg">
        Your browser does not support playing this audio file. Use Download instead.
      </audio>
    </div>
  )
}
