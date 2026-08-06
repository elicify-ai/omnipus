// LibraryMermaidPreview — .mmd/.mermaid view/edit (library-spec.md D-5 /
// section 4). View reuses <MermaidDiagram> VERBATIM (the same shared diagram
// renderer chat markdown routes ```mermaid fences to) — a drop-in, not a
// second diagram-rendering path. Edit is plain CodeMirror source (no
// dedicated CodeMirror grammar for Mermaid exists — see libraryLanguages.ts's
// doc comment).

import { MermaidDiagram } from '@/components/chat/mermaid-renderer'
import { LibraryTextPreview } from './LibraryTextPreview'
import type { LibraryEntry } from '@/lib/api'

interface LibraryMermaidPreviewProps {
  workspaceId: string
  entry: LibraryEntry
  content: string
  onSaved?: (entry: LibraryEntry) => void
}

export function LibraryMermaidPreview({ workspaceId, entry, content, onSaved }: LibraryMermaidPreviewProps) {
  return (
    <LibraryTextPreview
      workspaceId={workspaceId}
      entry={entry}
      content={content}
      editorFilename={entry.name}
      onSaved={onSaved}
      renderView={(draft) => <MermaidDiagram code={draft} />}
    />
  )
}
