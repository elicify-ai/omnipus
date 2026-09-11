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
// already use (libraryPreviewVariant.ts, EMB-028): layout only. Every fetch
// and every state stays identical between `pane` and `inline`.
//
// There IS a non-happy path, contrary to what this header claimed while the
// element carried no `onError`: the raw authenticated download URL being the
// source removes the FETCH, not the DECODE. `.flac` and `.opus` both
// classify as this kind and neither plays everywhere, and the URL can 404 or
// 401. See mediaPreviewStates.tsx's header for the full list and why a dead
// control bar with no message is not an acceptable rendering of any of them.
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

import { useState } from 'react'
import { libraryDownloadUrl } from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'
import type { LibraryPreviewVariant } from './libraryPreviewVariant'
import { MediaUnplayableNotice, mediaPreviewContainerClass } from './mediaPreviewStates'

interface LibraryAudioPreviewProps {
  workspaceId: string
  entry: LibraryEntry
  variant?: LibraryPreviewVariant
}

export function LibraryAudioPreview({ workspaceId, entry, variant = 'pane' }: LibraryAudioPreviewProps) {
  const src = libraryDownloadUrl(workspaceId, entry.path)
  // The element's own fallback text fires ONLY when the browser does not
  // support `<audio>` at all — never when the SOURCE fails, which is the case
  // that actually happens: an extension this app maps to `audio` that the
  // browser cannot decode (`.flac` and `.opus` are not universal), a 404 after
  // the 30s-stale directory listing, a 401 after the session expired. Step 6
  // mounts this INSIDE notes, so without an onError a reader gets a dead
  // control bar in the middle of their text with nothing naming the problem.
  const [failed, setFailed] = useState(false)
  return (
    <div
      className={mediaPreviewContainerClass(variant)}
      data-testid="library-audio-preview"
      data-variant={variant}
    >
      {failed ? (
        <MediaUnplayableNotice kind="audio" name={entry.path} href={src} />
      ) : (
        /* No <track>: a workspace audio file carries no caption track to attach. */
        <audio
          controls
          src={src}
          onError={() => setFailed(true)}
          className="w-full max-w-lg"
          data-testid="library-audio-element"
        >
          Your browser does not support playing this audio file. Use Download instead.
        </audio>
      )}
    </div>
  )
}
