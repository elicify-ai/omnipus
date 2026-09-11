// LibraryVideoPreview — plain <video controls> for the Library preview pane
// (library-spec.md section 4). Nothing existing to reuse: the SPA's only
// other <video> is the WebRTC live-browser screencast sink (browser/), which
// is a totally different media pipeline (a live stream, not a file), so this
// is a genuinely new, minimal file player.
//
// Also the "video" kind's inline embed renderer for a LOCAL file
// (`![[clip.mp4]]` — ADR-083 embedded-content spec, Step 6 / EMB-105/EMB-027)
// — the SAME component the pane uses. Only `variant` may change between the
// two call sites (EMB-028, libraryPreviewVariant.ts): the pane fills its
// ancestor, the inline embed sizes to the video's own content so it sits in
// a note's text flow. This is NOT the allow-listed external host player
// (`VideoEmbed.tsx`, US-9) — that is a different notation (a markdown
// link/image naming an external URL) and a different kind entirely; this
// component only ever plays a file this workspace's own Library serves.
//
// EMB-030 — a `|400` size modifier is PICTURES ONLY. Like LibraryAudioPreview,
// this component takes no `width` prop: there is nothing here for a caller to
// apply one to, so a stray size segment on a video embed is inert by
// construction rather than merely "ignored by convention".

import { useState } from 'react'
import { libraryDownloadUrl } from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'
import type { LibraryPreviewVariant } from './libraryPreviewVariant'
import { MediaUnplayableNotice, mediaPreviewContainerClass } from './mediaPreviewStates'

interface LibraryVideoPreviewProps {
  workspaceId: string
  entry: LibraryEntry
  variant?: LibraryPreviewVariant
}

export function LibraryVideoPreview({ workspaceId, entry, variant = 'pane' }: LibraryVideoPreviewProps) {
  const src = libraryDownloadUrl(workspaceId, entry.path)
  // See mediaPreviewStates.tsx's header: `.mkv` and `.avi` both classify as
  // this kind and neither plays in every browser, so an undecodable source
  // is the ordinary case, not the exotic one. Without this, the reader gets a
  // black rectangle inside their note that does nothing when pressed.
  const [failed, setFailed] = useState(false)
  return (
    <div
      className={mediaPreviewContainerClass(variant)}
      data-testid="library-video-preview"
      data-variant={variant}
    >
      {failed ? (
        <MediaUnplayableNotice kind="video" name={entry.path} href={src} />
      ) : (
        /* No <track>: a workspace video file carries no caption track to attach. */
        <video
          controls
          src={src}
          onError={() => setFailed(true)}
          className="max-h-full max-w-full rounded-md"
          data-testid="library-video-element"
        >
          Your browser does not support playing this video. Use Download instead.
        </video>
      )}
    </div>
  )
}
