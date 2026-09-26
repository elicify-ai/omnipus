// MailExplorer — the mail browsing surface (the LibraryExplorer's role for
// mail): folder rail, message list, reading pane. This is the one component
// both panel shapes render — the docked MailPanel and the fullscreen pop-out
// (D11) — exactly as LibraryExplorer serves the docked Library panel and the
// /library route.
//
// PROTOTYPE POSTURE (D35): presentational only. Folder/selection state lives
// here as ordinary view state over sample data; no endpoint is called, and
// the 30 s poll (D25) and seen endpoint (FR-020) are deliberately absent.
import { useState } from 'react'
import { SegmentedControl, SegmentedControlItem } from '@/components/ui/segmented-control'
import { cn } from '@/lib/utils'
import { MailFolderRail } from './MailFolderRail'
import { MailMessageList } from './MailMessageList'
import { MailReadingPane } from './MailReadingPane'
import type { FolderKey, MailSampleData, MessageDetail } from './sampleMail'

export interface MailExplorerProps {
  data: MailSampleData
  /** Show skeleton rows instead of the list (loading state story). */
  loading?: boolean
  /** Initial folder (empty-folder story). */
  initialFolder?: FolderKey
  /** Initial selection (reading states). */
  initialSelectedId?: string
  className?: string
}

export function MailExplorer({ data, loading, initialFolder = 'inbox', initialSelectedId, className }: MailExplorerProps) {
  const [folder, setFolder] = useState<FolderKey>(initialFolder)
  const [selected, setSelected] = useState<MessageDetail | null>(
    initialSelectedId ? data.messages.find((m) => m.id === initialSelectedId) ?? null : null,
  )
  const activeFolder = data.folders.find((f) => f.key === folder)
  const folderMessages = data.messages.filter((m) => m.folder === folder)
  // Narrow layout: the reading pane REPLACES the list (mobile mail
  // convention); the selection clears back to the list via the pane's
  // close button.
  const showReading = selected !== null
  return (
    <div className={cn('flex min-h-0 flex-1 overflow-hidden', className)}>
      {/* Folder rail — hidden on narrow; SegmentedControl takes over. */}
      <MailFolderRail
        className="hidden sm:flex"
        folders={data.folders}
        active={folder}
        onFolderChange={(key) => {
          setFolder(key)
          setSelected(null)
        }}
      />
      <div className={cn('flex min-w-0 flex-1 flex-col', showReading && 'hidden sm:flex')}>
        <SegmentedControl
          value={folder}
          onValueChange={(v) => {
            setFolder(v as FolderKey)
            setSelected(null)
          }}
          className="sm:hidden"
          aria-label="Mail folders"
        >
          {data.folders.map((f) => (
            <SegmentedControlItem key={f.key} value={f.key}>
              {f.label}
            </SegmentedControlItem>
          ))}
        </SegmentedControl>
        <MailMessageList
          folderLabel={activeFolder?.label ?? 'Mail'}
          messages={folderMessages}
          selectedId={selected?.id}
          loading={loading}
          onSelect={setSelected}
          onCompose={() => { /* prototype: compose is a story-level state */ }}
        />
      </div>
      {selected && (
        <MailReadingPane
          className={cn('border-l border-[var(--color-border)]', showReading ? 'flex' : 'hidden sm:flex')}
          message={selected}
          onReply={() => {}}
          onClose={() => setSelected(null)}
        />
      )}
    </div>
  )
}
