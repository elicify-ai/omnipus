// KbVideoEmbedMount — ADR-083 embedded-content spec, Step 6 (EMB-105, US-12
// AS-2/AS-3): a `![[clip.mp4]]` standing alone in its paragraph, for a LOCAL
// video file this workspace's own Library serves. Mounts the SAME
// `LibraryVideoPreview` the Library preview pane uses (EMB-027) — never a
// second video renderer — through the same lazy-mount budget (EMB-065) and
// directory-listing resolution (`useResolvedEmbedEntry`) the other Step 6
// mounts use.
//
// NOT the allow-listed external video host (`VideoEmbed.tsx`, US-9) — that
// notation is a markdown link/image naming an external URL and is
// recognised upstream of this component entirely (EMB-075: "recognised only
// from a markdown-link form", never a wikilink). This mount is reached only
// for a wikilink embed whose target classifies as the `video` KIND — a file
// this workspace's own Library serves.
//
// Not wired into any note's markdown pipeline yet — that dispatch lives in
// knowledgeMarkdown.tsx, owned by a concurrent change. This is the component
// that dispatch is expected to mount, and its call is exactly:
//
//   <KbVideoEmbedMount workspaceId={resolution.workspaceId} workspacePath={resolution.workspacePath} />
//
// once `classifyEmbedKind`'s `'video'` case is added to
// `KINDS_WITH_INLINE_RENDERER` there.

import { LazyEmbedMount } from './LazyEmbedMount'
import { LibraryVideoPreview } from './LibraryVideoPreview'
import { useResolvedEmbedEntry } from './useResolvedEmbedEntry'
import { EmbedMountPlaceholder, EmbedMountError } from './embedMountStates'

/** A 16:9-shaped reserved height, distinct from the audio bar and the base/
 *  image/transclusion reservations already in knowledgeMarkdown.tsx
 *  (EMB-066 / test 96: heights differ per kind, never one constant reused
 *  everywhere). */
export const VIDEO_EMBED_RESERVED_HEIGHT_PX = 320

export interface KbVideoEmbedMountProps {
  workspaceId: string
  workspacePath: string
}

export function KbVideoEmbedMount({ workspaceId, workspacePath }: KbVideoEmbedMountProps) {
  return (
    <LazyEmbedMount reservedHeight={VIDEO_EMBED_RESERVED_HEIGHT_PX} className="my-3 block">
      <KbVideoEmbedContent workspaceId={workspaceId} workspacePath={workspacePath} />
    </LazyEmbedMount>
  )
}

function KbVideoEmbedContent({ workspaceId, workspacePath }: KbVideoEmbedMountProps) {
  const resolved = useResolvedEmbedEntry(workspaceId, workspacePath)

  if (resolved.status === 'loading') return <EmbedMountPlaceholder />
  if (resolved.status === 'error' || !resolved.entry) {
    return <EmbedMountError message="Could not read this file's details." onRetry={resolved.refetch} />
  }

  return <LibraryVideoPreview workspaceId={workspaceId} entry={resolved.entry} variant="inline" />
}
