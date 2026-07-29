// LibraryTextPreview — the shared view/edit shell for the three editable
// Library file kinds (library-spec.md section 4: markdown, mermaid, "any
// text file"). Owns the toggle, the save button + status indicator, and the
// dirty-guard wiring (useLibraryFileEditor); each kind supplies only its own
// `renderView` (the read-only renderer) and `editorFilename` (what
// LibraryCodeEditor uses to pick a grammar).
//
// View mode renders the LIVE DRAFT (not just the last-saved text) — toggling
// to View after editing previews the in-progress change, which is what makes
// "view/edit toggle" actually useful for markdown/mermaid rather than just
// showing stale content.

import { useState } from 'react'
import type { ReactNode } from 'react'
import { Eye, PencilSimple, FloppyDisk } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { cn } from '@/lib/utils'
import { useLibraryFileEditor } from './useLibraryFileEditor'
import { LibraryCodeEditor } from './LibraryCodeEditor'
import type { LibraryEntry } from '@/lib/api'

interface LibraryTextPreviewProps {
  workspaceId: string
  entry: LibraryEntry
  content: string
  /** Renders the read-only view for the CURRENT draft text (may include
   * unsaved edits — see module doc comment above). */
  renderView: (draft: string) => ReactNode
  /** Filename passed to LibraryCodeEditor for language-grammar selection —
   * normally just `entry.name`. */
  editorFilename: string
  onSaved?: (entry: LibraryEntry) => void
}

export function LibraryTextPreview({
  workspaceId,
  entry,
  content,
  renderView,
  editorFilename,
  onSaved,
}: LibraryTextPreviewProps) {
  const [mode, setMode] = useState<'view' | 'edit'>('view')
  const { draft, setDraft, isDirty, save, status, error, lastSavedAt } = useLibraryFileEditor({
    workspaceId,
    path: entry.path,
    initialContent: content,
    onSaved,
  })

  return (
    <div className="flex flex-1 min-h-0 flex-col">
      <div className="flex shrink-0 items-center justify-between gap-2 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-1.5">
        <div className="flex items-center gap-1" role="group" aria-label="View mode">
          <button
            type="button"
            tabIndex={0}
            onClick={() => setMode('view')}
            aria-pressed={mode === 'view'}
            data-testid="library-preview-mode-view"
            className={cn(
              'flex items-center gap-1.5 rounded px-2 py-1 text-xs transition-colors',
              mode === 'view'
                ? 'bg-[var(--color-surface-2)] text-[var(--color-accent)]'
                : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
            )}
          >
            <Eye size={13} /> View
          </button>
          <button
            type="button"
            tabIndex={0}
            onClick={() => setMode('edit')}
            aria-pressed={mode === 'edit'}
            data-testid="library-preview-mode-edit"
            className={cn(
              'flex items-center gap-1.5 rounded px-2 py-1 text-xs transition-colors',
              mode === 'edit'
                ? 'bg-[var(--color-surface-2)] text-[var(--color-accent)]'
                : 'text-[var(--color-muted)] hover:text-[var(--color-secondary)]',
            )}
          >
            <PencilSimple size={13} /> Edit
          </button>
        </div>

        <div className="flex items-center gap-3">
          <AutoSaveIndicator status={status} error={error} lastSavedAt={lastSavedAt} />
          <Button
            size="sm"
            onClick={save}
            disabled={!isDirty || status === 'saving'}
            data-testid="library-preview-save"
            className="gap-1.5"
          >
            <FloppyDisk size={13} />
            {status === 'saving' ? 'Saving…' : 'Save'}
          </Button>
        </div>
      </div>

      <div className="flex-1 min-h-0 overflow-auto">
        {mode === 'view' ? (
          <div className="p-4" data-testid="library-preview-view-body">
            {renderView(draft)}
          </div>
        ) : (
          <LibraryCodeEditor value={draft} onChange={setDraft} filename={editorFilename} />
        )}
      </div>
    </div>
  )
}
