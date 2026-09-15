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
// Wired into the note markdown pipeline by `bfbb05948`:
// `knowledgeMarkdown.tsx`'s `KnowledgeMarkdownLink` dispatches a STANDALONE,
// RESOLVED embed of kind `video` straight here. The promotion gate is
// `isPromotableBlockEmbedNode` recognising `data-kb-embed-kind` — there is
// no second per-kind set to keep in sync with it (an earlier draft of this
// header predicted one, and adding `video` to it was a provable no-op).

import { LazyEmbedMount } from './LazyEmbedMount'
import { LibraryVideoPreview } from './LibraryVideoPreview'
import { useResolvedEmbedEntry } from './useResolvedEmbedEntry'
import { EmbedMountPlaceholder, EmbedMountError, EmbedMountMissing } from './embedMountStates'

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
  if (resolved.status === 'error') {
    return <EmbedMountError message="Could not read this file's details." onRetry={resolved.refetch} />
  }
  // A listing that SUCCEEDED without this path is a renamed/deleted file,
  // not a failed request — stated in its own words, with no Retry that
  // cannot work (see `useResolvedEmbedEntry`'s four-state doc).
  if (resolved.status === 'missing') {
    return <EmbedMountMissing workspacePath={workspacePath} parentDir={resolved.parentDir} />
  }

  return <LibraryVideoPreview workspaceId={workspaceId} entry={resolved.entry} variant="inline" />
}
