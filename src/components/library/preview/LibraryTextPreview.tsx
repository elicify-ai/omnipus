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
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import { cn } from '@/lib/utils'
import { PreviewHeaderPortal } from './previewHeaderSlot'
import { LIBRARY_ICON_BTN } from '../LibraryPreviewPane'
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
      {/* These used to be this component's OWN header row — the third of three.
          They now render into the pane's single row via a portal, as icons.
          The labels are gone, so the aria-label/title are the only accessible
          name each control has left and must stay. Mode is a two-button group
          with aria-pressed rather than a single toggle, so a screen reader
          hears which of view/edit is current instead of inferring it. */}
      <PreviewHeaderPortal>
        <div className="flex items-center gap-0.5" role="group" aria-label="View mode">
          <button
            type="button"
            tabIndex={0}
            onClick={() => setMode('view')}
            aria-pressed={mode === 'view'}
            aria-label="View"
            title="View"
            data-testid="library-preview-mode-view"
            className={cn(LIBRARY_ICON_BTN, mode === 'view' && 'text-[var(--color-accent)]')}
          >
            <Eye size={15} weight={mode === 'view' ? 'fill' : 'regular'} />
          </button>
          <button
            type="button"
            tabIndex={0}
            onClick={() => setMode('edit')}
            aria-pressed={mode === 'edit'}
            aria-label="Edit"
            title="Edit"
            data-testid="library-preview-mode-edit"
            className={cn(LIBRARY_ICON_BTN, mode === 'edit' && 'text-[var(--color-accent)]')}
          >
            <PencilSimple size={15} weight={mode === 'edit' ? 'fill' : 'regular'} />
          </button>
        </div>
        {/* Kept: it is the only feedback that a save happened at all, and it
            renders nothing while idle, so it costs no width in the common case. */}
        <AutoSaveIndicator status={status} error={error} lastSavedAt={lastSavedAt} />
        <button
          type="button"
          tabIndex={0}
          onClick={save}
          disabled={!isDirty || status === 'saving'}
          aria-label={status === 'saving' ? 'Saving' : 'Save'}
          title={status === 'saving' ? 'Saving…' : 'Save'}
          data-testid="library-preview-save"
          className={cn(LIBRARY_ICON_BTN, isDirty && status !== 'saving' && 'text-[var(--color-accent)]')}
        >
          <FloppyDisk size={15} weight={isDirty ? 'fill' : 'regular'} />
        </button>
      </PreviewHeaderPortal>

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
