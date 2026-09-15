// mediaPreviewStates.tsx — the non-happy path `LibraryAudioPreview` and
// `LibraryVideoPreview` share, plus the container class they already shared
// by copy.
//
// WHY THIS EXISTS AT ALL. Both players used to say, in their own headers,
// that there IS no non-happy path: "the raw authenticated download URL IS
// the source, so there is nothing to fetch, decode or fail". The first half
// is true and the conclusion does not follow — the BROWSER still has to
// decode it, and it often cannot:
//
//   - `libraryPreviewKind.ts` maps `mkv` and `avi` to the `video` kind and
//     `flac`/`opus` to `audio`. Chrome plays no `.avi` at all; Safari and
//     Firefox play no `.mkv`. These are ordinary files a person will drop in
//     a workspace, not exotica.
//   - The URL can 404 (the file was deleted after the directory listing was
//     cached for its 30s staleTime) or 401 (the session expired).
//
// In every one of those cases a bare `<audio>`/`<video>` renders a control
// bar or a black rectangle that does nothing when pressed, with no message.
// The elements' own fallback CHILDREN do not help: they display only when
// the element TYPE is unsupported, never when the source fails. ADR-083
// Step 6 newly mounts both players inside notes, so that dead box now
// appears in the middle of a reader's prose. A failure nobody can see is the
// defect this project deleted a whole working code path over (CLAUDE.md, the
// JPEG screencast fallback) — so both players state it, and offer the one
// thing that still works: downloading the file.

import { DownloadSimple, Warning } from '@phosphor-icons/react'
import type { LibraryPreviewVariant } from './libraryPreviewVariant'

/** The container both media players wrap their element in. Identical in both
 *  by copy before this; one definition now, so an audio-only or video-only
 *  layout change has to be a deliberate divergence rather than a missed
 *  second edit. `LibraryImagePreview`/`LibraryPdfPreview` have genuinely
 *  different class strings and deliberately do NOT use this. */
export function mediaPreviewContainerClass(variant: LibraryPreviewVariant): string {
  return variant === 'inline'
    ? 'flex items-center justify-center'
    : 'flex flex-1 min-h-0 items-center justify-center overflow-auto bg-[var(--color-surface-0)] p-4'
}

/** Shown in place of a player whose source the browser refused. Names the
 *  file, says plainly that THIS BROWSER could not play it (rather than
 *  implying the file is broken — the same file usually plays elsewhere), and
 *  keeps a real download link, which is the one action that still works. */
export function MediaUnplayableNotice({
  kind,
  name,
  href,
}: {
  kind: 'audio' | 'video'
  name: string
  href: string
}) {
  const basename = name.slice(name.lastIndexOf('/') + 1)
  return (
    <div
      data-testid={`library-${kind}-unplayable`}
      role="status"
      className="flex flex-col items-center gap-2 rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/5 px-3 py-6 text-center text-xs text-[var(--color-warning)]"
    >
      <Warning size={16} />
      <span>This browser could not play “{basename}”.</span>
      <a
        tabIndex={0}
        href={href}
        download
        className="inline-flex items-center gap-1 text-[11px] underline underline-offset-2"
      >
        <DownloadSimple size={12} /> Download it instead
      </a>
    </div>
  )
}
