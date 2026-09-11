// KbAudioEmbedMount — ADR-083 embedded-content spec, Step 6 (EMB-105, US-12
// AS-1/AS-3): a `![[song.mp3]]` standing alone in its paragraph. Mounts the
// SAME `LibraryAudioPreview` the Library preview pane uses (EMB-027) — never
// a second audio renderer — through the same lazy-mount budget (EMB-065)
// every other embed kind uses, and the same directory-listing resolution
// `KbImageEmbedMount` already established for a kind whose full metadata a
// link-graph edge does not carry (`useResolvedEmbedEntry`'s own header).
//
// Not wired into any note's markdown pipeline yet — that dispatch lives in
// knowledgeMarkdown.tsx, owned by a concurrent change. This is the component
// that dispatch is expected to mount, and its call is exactly:
//
//   <KbAudioEmbedMount workspaceId={resolution.workspaceId} workspacePath={resolution.workspacePath} />
//
// once `classifyEmbedKind`'s `'audio'` case is added to
// `KINDS_WITH_INLINE_RENDERER` there.

import { LazyEmbedMount } from './LazyEmbedMount'
import { LibraryAudioPreview } from './LibraryAudioPreview'
import { useResolvedEmbedEntry } from './useResolvedEmbedEntry'
import { EmbedMountPlaceholder, EmbedMountError } from './embedMountStates'

/** A compact, single-control-bar kind — the smallest reserved height among
 *  the Step 6 kinds (EMB-066 / test 96: reserved heights differ per kind,
 *  not one constant reused everywhere). */
export const AUDIO_EMBED_RESERVED_HEIGHT_PX = 96

export interface KbAudioEmbedMountProps {
  workspaceId: string
  workspacePath: string
}

export function KbAudioEmbedMount({ workspaceId, workspacePath }: KbAudioEmbedMountProps) {
  return (
    <LazyEmbedMount reservedHeight={AUDIO_EMBED_RESERVED_HEIGHT_PX} className="my-3 block">
      <KbAudioEmbedContent workspaceId={workspaceId} workspacePath={workspacePath} />
    </LazyEmbedMount>
  )
}

function KbAudioEmbedContent({ workspaceId, workspacePath }: KbAudioEmbedMountProps) {
  const resolved = useResolvedEmbedEntry(workspaceId, workspacePath)

  if (resolved.status === 'loading') return <EmbedMountPlaceholder />
  if (resolved.status === 'error' || !resolved.entry) {
    return <EmbedMountError message="Could not read this file's details." onRetry={resolved.refetch} />
  }

  return <LibraryAudioPreview workspaceId={workspaceId} entry={resolved.entry} variant="inline" />
}
