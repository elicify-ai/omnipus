// LibraryImagePreview — plain <img> for the Library preview pane
// (library-spec.md section 4). Deliberately simple: no lightbox, no
// annotate/crop — those belong to the chat attachment surface
// (chat/ChatImage.tsx), which this pane must NOT reach into (file
// ownership boundary). Sensible max sizing + a dark canvas + real alt text.

import { libraryDownloadUrl } from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'

interface LibraryImagePreviewProps {
  workspaceId: string
  entry: LibraryEntry
}

export function LibraryImagePreview({ workspaceId, entry }: LibraryImagePreviewProps) {
  const src = libraryDownloadUrl(workspaceId, entry.path)
  return (
    <div
      className="flex flex-1 min-h-0 items-center justify-center overflow-auto bg-[var(--color-surface-0)] p-4"
      data-testid="library-image-preview"
    >
      <img src={src} alt={entry.name} className="max-h-full max-w-full rounded-md object-contain" />
    </div>
  )
}
