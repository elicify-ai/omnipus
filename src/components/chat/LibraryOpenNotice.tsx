// LibraryOpenNotice — the quiet "Open in Library" notice an agent leaves when
// it asked to show a file but this tab could not open the Library by itself
// (not watching this chat, unsaved edits, a pending discard decision) —
// chat-links spec "What the screen does", UI States, Accessibility.
//
// One notice per session (a newer one replaces the older), never persisted,
// gone after reload. It stays until the user acts:
//   - Open (catalogued Button): removes the notice, then opens the Library
//     through the leave gate — unsaved edits still ask first;
//   - Dismiss (catalogued IconButton, after Open in Tab order) or Escape while
//     focus is inside the notice: removes it, nothing opens. Escape in the
//     composer does not reach it (that key already closes panels).
//
// Also hosts the chat's polite live region: auto-open never moves focus, so
// "Opened <name> in Library" (or the notice sentence) is announced instead.
//
// A local component, not a catalogued one: a toast is a timed, global stack,
// and this is one persistent notice per chat (spec "Design-System Reuse").
// Rendered by the three chat views beside ChatScreen, not inside it.

import type { KeyboardEvent } from 'react'
import { FileText, X } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { openLibraryAt } from '@/components/library/openLibraryAt'
import {
  dismissLibraryOpenNotice,
  libraryFileName,
  useLibraryOpenStore,
} from '@/store/chat/libraryOpen'

export function LibraryOpenNotice({ sessionId }: { sessionId: string }) {
  const notice = useLibraryOpenStore((s) => s.notices[sessionId])
  const announcement = useLibraryOpenStore((s) => s.announcement)

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== 'Escape') return
    event.stopPropagation()
    dismissLibraryOpenNotice(sessionId)
  }

  const name = notice ? libraryFileName(notice.path) : ''

  return (
    <>
      <div className="sr-only" aria-live="polite" data-testid="library-open-announcer">
        {announcement ? <span key={announcement.id}>{announcement.text}</span> : null}
      </div>
      {notice && (
        <div
          data-testid="library-open-notice"
          onKeyDown={onKeyDown}
          className="absolute inset-x-0 top-[var(--space-2)] z-10 mx-auto flex w-fit max-w-[calc(100%-var(--space-4))] items-center gap-[var(--space-2)] rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] py-[var(--space-1)] pl-[var(--space-2-5)] pr-[var(--space-1)] shadow-[var(--elevation-floating)]"
        >
          <FileText size={14} aria-hidden="true" className="shrink-0 text-[var(--color-muted)]" />
          <p className="min-w-0 truncate text-[length:var(--type-utility-xs-size)] text-[var(--color-secondary)]">
            The agent wants to show you <span className="font-medium">{name}</span>
          </p>
          <Button
            size="sm"
            variant="outline"
            aria-label={`Open ${name} in Library`}
            data-testid="library-open-notice-open"
            onClick={() => {
              dismissLibraryOpenNotice(sessionId)
              openLibraryAt({ workspaceId: notice.workspaceId, path: notice.path })
            }}
          >
            Open
          </Button>
          <IconButton
            size="sm"
            aria-label="Dismiss"
            data-testid="library-open-notice-dismiss"
            onClick={() => dismissLibraryOpenNotice(sessionId)}
            className="text-[var(--color-muted)] hover:text-[var(--color-secondary)]"
          >
            <X size={14} />
          </IconButton>
        </div>
      )}
    </>
  )
}
