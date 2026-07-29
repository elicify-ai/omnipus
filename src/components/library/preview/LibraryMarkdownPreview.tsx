// LibraryMarkdownPreview — markdown view/edit (library-spec.md D-5 / section
// 4). View reuses HistoricalMessageMarkdown VERBATIM — the same
// static-string ({ content }) renderer chat uses for finalized messages,
// which already routes ```mermaid fences to <MermaidDiagram> and (as of
// D-6/3ed49f01/ffa2bd27) shares Shiki highlighting for block code. This is
// deliberately NOT a second markdown pipeline — see this task's brief.

import { HistoricalMessageMarkdown } from '@/components/chat/historical-markdown'
import { LibraryTextPreview } from './LibraryTextPreview'
import type { LibraryEntry } from '@/lib/api'

interface LibraryMarkdownPreviewProps {
  workspaceId: string
  entry: LibraryEntry
  content: string
  onSaved?: (entry: LibraryEntry) => void
}

export function LibraryMarkdownPreview({ workspaceId, entry, content, onSaved }: LibraryMarkdownPreviewProps) {
  return (
    <LibraryTextPreview
      workspaceId={workspaceId}
      entry={entry}
      content={content}
      editorFilename={entry.name}
      onSaved={onSaved}
      renderView={(draft) => <HistoricalMessageMarkdown content={draft} />}
    />
  )
}
