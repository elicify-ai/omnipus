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

export interface ResolvedEmbedEntry {
  status: 'loading' | 'error' | 'ready'
  entry?: LibraryEntry
  refetch: () => void
}

export function useResolvedEmbedEntry(workspaceId: string, workspacePath: string): ResolvedEmbedEntry {
  const parentDir = useMemo(() => dirnameOf(workspacePath), [workspacePath])
  const entriesQuery: UseQueryResult<LibraryEntry[]> = useQuery({
    queryKey: libraryQueryKeys.entries(workspaceId, parentDir, false),
    queryFn: () => fetchLibraryEntries(workspaceId, parentDir, false),
    staleTime: 30_000,
  })

  if (entriesQuery.isLoading) {
    return { status: 'loading', refetch: () => void entriesQuery.refetch() }
  }

  const entry = entriesQuery.data?.find((e) => e.path === workspacePath)
  if (entriesQuery.isError || !entry) {
    return { status: 'error', refetch: () => void entriesQuery.refetch() }
  }

  return { status: 'ready', entry, refetch: () => void entriesQuery.refetch() }
}
