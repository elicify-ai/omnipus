// KbPdfPageEmbedMount — ADR-083 embedded-content spec, Step 6 (EMB-105,
// US-12 AS-4): a `![[doc.pdf#page=3]]` standing alone in its paragraph.
// Mounts the SAME `LibraryPdfPreview` the Library preview pane uses
// (EMB-027) with its `pageFragment` prop set — never a second PDF renderer,
// and never a second worker path: `pageFragment` rides `LibraryPdfPreview`'s
// own `pdfWorkerPool` lease exactly like an ordinary whole-document embed
// does (EMB-032 — see LibraryPdfPreview.pageFragment.test.tsx's pool-sharing
// test for the proof).
//
// A whole-document PDF embed (no `#page=` fragment) is a SEPARATE,
// already-specified Step 1 feature (US-3) that dispatches through
// knowledgeMarkdown.tsx directly, mounting `LibraryPdfPreview` with no
// `pageFragment` — this component is reached ONLY for the page-fragment
// case. The `page` prop here is already-parsed: extracting an integer out of
// a `#page=3` fragment is upstream parsing work for whichever change adds it
// to knowledgeMarkdown.tsx's wikilink fragment handling (that file's
// `heading`/`block` split has no page-number case yet) — this component
// takes the page number as a plain prop and makes no assumption about the
// fragment's original text.
//
// Not wired into any note's markdown pipeline yet. This is the component
// that dispatch is expected to mount, and its call is exactly:
//
//   <KbPdfPageEmbedMount workspaceId={resolution.workspaceId} workspacePath={resolution.workspacePath} page={pageNumber} />
//
// once a `#page=N` fragment is recognised for a `pdf`-kind embed there.

import { LazyEmbedMount } from './LazyEmbedMount'
import { LibraryPdfPreview } from './LibraryPdfPreview'
import { useResolvedEmbedEntry } from './useResolvedEmbedEntry'
import { EmbedMountPlaceholder, EmbedMountError } from './embedMountStates'

/** A single-page-shaped reserved height — smaller than a whole multi-page
 *  PDF embed would reserve, since exactly one page is ever drawn here
 *  (EMB-066 / test 96: heights differ per kind). */
export const PDF_PAGE_EMBED_RESERVED_HEIGHT_PX = 480

export interface KbPdfPageEmbedMountProps {
  workspaceId: string
  workspacePath: string
  /** 1-based page number parsed from the embed's `#page=N` fragment. */
  page: number
}

export function KbPdfPageEmbedMount({ workspaceId, workspacePath, page }: KbPdfPageEmbedMountProps) {
  return (
    <LazyEmbedMount reservedHeight={PDF_PAGE_EMBED_RESERVED_HEIGHT_PX} className="my-3 block">
      <KbPdfPageEmbedContent workspaceId={workspaceId} workspacePath={workspacePath} page={page} />
    </LazyEmbedMount>
  )
}

function KbPdfPageEmbedContent({ workspaceId, workspacePath, page }: KbPdfPageEmbedMountProps) {
  const resolved = useResolvedEmbedEntry(workspaceId, workspacePath)

  if (resolved.status === 'loading') return <EmbedMountPlaceholder />
  if (resolved.status === 'error' || !resolved.entry) {
    return <EmbedMountError message="Could not read this file's details." onRetry={resolved.refetch} />
  }

  return (
    <LibraryPdfPreview workspaceId={workspaceId} entry={resolved.entry} variant="inline" pageFragment={page} />
  )
}
