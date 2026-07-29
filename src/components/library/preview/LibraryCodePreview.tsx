// LibraryCodePreview — "any text file" view/edit (library-spec.md D-5 /
// section 4). View reuses ShikiCodeBlock VERBATIM — the same shared
// highlighted-code renderer historical-markdown.tsx's HistoricalCodeBlock and
// the live AssistantUI renderer both use (extracted to markdown-shared.tsx in
// ffa2bd27 precisely so this wouldn't need its own copy). Edit is CodeMirror
// with a best-effort language grammar (libraryLanguages.ts).

import { ShikiCodeBlock } from '@/components/chat/markdown-shared'
import { LibraryTextPreview } from './LibraryTextPreview'
import { shikiLanguageFor } from './libraryLanguages'
import type { LibraryEntry } from '@/lib/api'

interface LibraryCodePreviewProps {
  workspaceId: string
  entry: LibraryEntry
  content: string
  onSaved?: (entry: LibraryEntry) => void
}

export function LibraryCodePreview({ workspaceId, entry, content, onSaved }: LibraryCodePreviewProps) {
  const language = shikiLanguageFor(entry.name)
  return (
    <LibraryTextPreview
      workspaceId={workspaceId}
      entry={entry}
      content={content}
      editorFilename={entry.name}
      onSaved={onSaved}
      renderView={(draft) => <ShikiCodeBlock language={language} code={draft} />}
    />
  )
}
