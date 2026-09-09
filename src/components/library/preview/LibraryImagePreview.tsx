// LibraryImagePreview — plain <img> for the Library preview pane
// (library-spec.md section 4). Deliberately simple: no lightbox, no
// annotate/crop — those belong to the chat attachment surface
// (chat/ChatImage.tsx), which this pane must NOT reach into (file
// ownership boundary). Sensible max sizing + a dark canvas + real alt text.
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
// the embed notation — decide size.

import { libraryDownloadUrl } from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'
import type { LibraryPreviewVariant } from './libraryPreviewVariant'

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
  const src = libraryDownloadUrl(workspaceId, entry.path)
  const inline = variant === 'inline'
  return (
    <div
      className={
        inline
          ? 'flex items-center justify-center'
          : 'flex flex-1 min-h-0 items-center justify-center overflow-auto bg-[var(--color-surface-0)] p-4'
      }
      data-testid="library-image-preview"
      data-variant={variant}
    >
      <img
        src={src}
        alt={entry.name}
        className={inline ? 'h-auto max-w-full rounded-md object-contain' : 'max-h-full max-w-full rounded-md object-contain'}
        {...(inline && width !== undefined ? { width } : {})}
      />
    </div>
  )
}
