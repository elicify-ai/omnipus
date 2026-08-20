// LibraryVideoPreview — plain <video controls> for the Library preview pane
// (library-spec.md section 4). Nothing existing to reuse: the SPA's only
// other <video> is the WebRTC live-browser screencast sink (browser/), which
// is a totally different media pipeline (a live stream, not a file), so this
// is a genuinely new, minimal file player.

import { libraryDownloadUrl } from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'

interface LibraryVideoPreviewProps {
  workspaceId: string
  entry: LibraryEntry
}

export function LibraryVideoPreview({ workspaceId, entry }: LibraryVideoPreviewProps) {
  const src = libraryDownloadUrl(workspaceId, entry.path)
  return (
    <div
      className="flex flex-1 min-h-0 items-center justify-center overflow-auto bg-[var(--color-surface-0)] p-4"
      data-testid="library-video-preview"
    >
      { }
      <video controls src={src} className="max-h-full max-w-full rounded-md">
        Your browser does not support playing this video. Use Download instead.
      </video>
    </div>
  )
}
