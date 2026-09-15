// useResolvedEmbedEntry.ts — fetches the real `LibraryEntry` an inline embed's
// shared renderer needs (size, modified_at, is_text_editable — none of which
// a link-graph edge carries), by reusing the SAME parent-directory listing
// `KbImageEmbedMount`/`KbBaseEmbedMount` already fetch in knowledgeMarkdown.tsx
// (there is no single-entry GET; see that file's own comments on
// `KbImageEmbedContent`/`KbBaseEmbedMount` for why the directory listing is
// the one endpoint that already returns them). Same query key, so TanStack
// Query dedupes a directory already open in the Library pane or requested by
// a sibling embed in the same note — no extra request per embed.
//
// Factored out into its own hook (rather than copied three times into
// KbAudioEmbedMount/KbVideoEmbedMount/KbPdfPageEmbedMount) so the fetch/loading
// /error contract for "resolve an embed's LibraryEntry" exists exactly once
// across this ADR-083 Step 6 work.

import { useMemo } from 'react'
import { useQuery, type UseQueryResult } from '@tanstack/react-query'
import { fetchLibraryEntries, libraryQueryKeys } from '@/lib/api'
import type { LibraryEntry } from '@/lib/api'

function dirnameOf(path: string): string {
  const i = path.lastIndexOf('/')
  return i <= 0 ? '' : path.slice(0, i)
}

/** FOUR states, and `missing` is not a flavour of `error`.
 *
 *  A directory listing that came back FINE and simply does not contain this
 *  file (someone renamed `media/song.mp3` to `media/intro.mp3`; a wikilink
 *  that resolved to a path in another directory) is a different fact from a
 *  listing that never arrived — different cause, different remedy, and only
 *  one of them can be fixed by trying again. Collapsed into one state with
 *  one sentence and one Retry button, every renamed file read as "the server
 *  is flaky": the reader clicks Retry, gets the identical box, and never
 *  learns the link needs updating. The graph layer above already keeps
 *  `unresolved` and `graph_unavailable` apart for exactly this reason
 *  (EMB-012); this hook used to throw that distinction away one level down.
 *
 *  Written as a DISCRIMINATED UNION so `entry` exists only on `ready`. As a
 *  `status` plus an optional `entry`, `{ status: 'ready' }` with no entry
 *  type-checked, and all three consumers hand-wrote the same compensating
 *  `|| !resolved.entry` check — three copies of a guard the type should have
 *  made unnecessary, and a fourth mount that forgot it would draw an empty
 *  player that looks functional and plays nothing. */
export type ResolvedEmbedEntry =
  | { status: 'loading'; refetch: () => void }
  /** The listing request itself failed. Retrying can help. */
  | { status: 'error'; refetch: () => void }
  /** The listing succeeded and this path is not in it. Retrying cannot
   *  help, so no consumer should offer it as the remedy. */
  | { status: 'missing'; parentDir: string; refetch: () => void }
  | { status: 'ready'; entry: LibraryEntry; refetch: () => void }

export function useResolvedEmbedEntry(workspaceId: string, workspacePath: string): ResolvedEmbedEntry {
  const parentDir = useMemo(() => dirnameOf(workspacePath), [workspacePath])
  const entriesQuery: UseQueryResult<LibraryEntry[]> = useQuery({
    queryKey: libraryQueryKeys.entries(workspaceId, parentDir, false),
    queryFn: () => fetchLibraryEntries(workspaceId, parentDir, false),
    staleTime: 30_000,
  })
  const refetch = () => void entriesQuery.refetch()

  if (entriesQuery.isLoading) return { status: 'loading', refetch }
  if (entriesQuery.isError) return { status: 'error', refetch }

  const entry = entriesQuery.data?.find((e) => e.path === workspacePath)
  // `data` undefined with neither isLoading nor isError is the paused case
  // (offline, `networkMode: 'online'`): the listing has NOT succeeded, so
  // this is not evidence the file is gone.
  if (entriesQuery.data === undefined) return { status: 'loading', refetch }
  if (!entry) return { status: 'missing', parentDir, refetch }

  return { status: 'ready', entry, refetch }
}
