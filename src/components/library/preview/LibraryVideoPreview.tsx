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

import { useRef, useState } from 'react'
import { ArrowsOutSimple } from '@phosphor-icons/react'
import { libraryDownloadUrl } from '@/lib/api'
import { cn } from '@/lib/utils'
import type { LibraryEntry } from '@/lib/api'
import type { LibraryPreviewVariant } from './libraryPreviewVariant'
import { IconButton } from '@/components/ui/icon-button'
import { MediaUnplayableNotice } from './mediaPreviewStates'

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
  const videoRef = useRef<HTMLVideoElement>(null)

  const handleFullscreen = () => {
    if (videoRef.current?.requestFullscreen) {
      void videoRef.current.requestFullscreen()
    }
  }

  // No <track>: a workspace video file carries no caption track to attach.
  // Shared by both variants; only the inline variant adds its own frame.
  const player = (
    <>
      <video
        ref={videoRef}
        controls
        // UAT D-102: same native-control colour rule as LibraryAudioPreview.
        style={{ colorScheme: 'dark' }}
        src={src}
        onError={() => setFailed(true)}
        className="block max-h-full max-w-full rounded-md"
        data-testid="library-video-element"
      >
        Your browser does not support playing this video. Use Download instead.
      </video>
      <IconButton
        onClick={handleFullscreen}
        aria-label="Open full screen"
        className="absolute top-2 right-2 h-7 w-7 flex items-center justify-center rounded transition-colors text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]"
        data-testid="library-video-fullscreen"
      >
        <ArrowsOutSimple size={14} />
      </IconButton>
    </>
  )

  return (
    <div
      // Same container as LibraryAudioPreview — keep both literal strings in sync by hand.
      className={cn(
        variant === 'inline'
          ? 'flex items-center justify-center'
          : 'flex flex-1 min-h-0 items-center justify-center overflow-auto bg-[var(--color-surface-0)] p-[var(--space-3)]',
        'relative',
      )}
      data-testid="library-video-preview"
      data-variant={variant}
    >
      {failed ? (
        <MediaUnplayableNotice kind="video" name={entry.path} href={src} />
      ) : variant === 'inline' ? (
        // The frame shrink-wraps the rendered video, so the full-screen
        // button sits in the video's own corner rather than the note column's.
        <div className="relative inline-block" data-testid="library-video-frame">
          {player}
        </div>
      ) : (
        player
      )}
    </div>
  )
}
