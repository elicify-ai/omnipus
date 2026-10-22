// One session row in the Sessions view. Actions are siblings, never nested
// buttons. This row shows no agent mark and does not fetch an agent; the
// agent header above the group carries identity.

import { useEffect, useRef, useState } from 'react'
import { Check, PencilSimple, Trash, X } from '@phosphor-icons/react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import type { Session } from '@/lib/api'
import { formatRelative } from '@/lib/formatRelative'
import { formatTokens } from '@/lib/formatTokens'
import { cn } from '@/lib/utils'
import { isMainSession } from '@/lib/nav/sessionCoreSeam'
import { sessionDisplayTitle } from '@/lib/nav/sessionDisplayTitle'
import { interruptionReasonText } from '@/lib/subagentStatus'
import { backgroundCommandSentence, sessionKindLabel, sessionStatusLabel, type SessionStatusTone } from './sessionLabels'

const COARSE_TARGET =
  'pointer-coarse:min-h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]'

const TONE_VARIANT: Record<SessionStatusTone, 'default' | 'secondary' | 'success' | 'error' | 'warning' | 'muted'> = {
  working: 'default',
  waiting: 'warning',
  queued: 'secondary',
  done: 'success',
  failed: 'error',
  stopped: 'warning',
  // Founder ruling: an interrupt is a normal outcome (e.g. the user pressed Stop), so neutral grey.
  interrupted: 'muted',
  unavailable: 'muted',
}

export function SessionRow({ session, isActive, isHighlighted, onSelect, onRename, onDelete, deleting, onEditingChange, agentName }: {
  session: Session
  isActive: boolean
  isHighlighted: boolean
  onSelect: () => void
  onRename: (title: string) => void
  onDelete: () => void
  deleting: boolean
  /** Reports inline-rename so the dialog can keep Escape from closing it. */
  onEditingChange: (sessionId: string, editing: boolean) => void
  /**
   * Display name of the row group's agent (founder feedback 2026-10-11,
   * point 4): an untitled MAIN row reads "<Agent> - Main Session" instead of
   * "Untitled session", derived from this CURRENT name so a rename flows
   * through on the next render. Optional; an unresolved name falls back to
   * the session's own agent id ("jim - Main Session" — the server's own
   * empty-name fallback), never a fabricated placeholder (R2-S4-01 retired
   * the stale "Unknown agent" wording here).
   */
  agentName?: string
}) {
  const [editing, setEditing] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [val, setVal] = useState(session.title || '')
  const renameButtonRef = useRef<HTMLButtonElement>(null)
  const commit = () => {
    const title = val.trim()
    if (title && title !== session.title) onRename(title)
    setEditing(false)
  }
  useEffect(() => {
    onEditingChange(session.id, editing)
    return () => onEditingChange(session.id, false)
  }, [editing, onEditingChange, session.id])

  const wasEditingRef = useRef(false)
  useEffect(() => {
    if (wasEditingRef.current && !editing) renameButtonRef.current?.focus()
    wasEditingRef.current = editing
  }, [editing])

  if (editing) {
    return (
      <div className="flex items-center gap-[var(--space-2)] rounded-md px-[var(--space-2-5)] py-[var(--space-2)] bg-[var(--color-surface-2)] mx-[var(--space-2)]">
        <Input
          autoFocus
          value={val}
          onChange={(event) => setVal(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') { event.preventDefault(); commit() }
            if (event.key === 'Escape') { event.preventDefault(); setEditing(false); setVal(session.title || '') }
          }}
          className="h-7 flex-1 text-[length:var(--type-body-compact-size)]"
        />
        <IconButton onClick={commit} className={cn('h-auto w-auto shrink-0 rounded p-[var(--space-1)] text-[var(--color-success)] hover:bg-[var(--color-surface-3)]', COARSE_TARGET)} aria-label="Confirm rename"><Check size={14} weight="bold" /></IconButton>
        <IconButton onClick={() => { setEditing(false); setVal(session.title || '') }} className={cn('h-auto w-auto shrink-0 rounded p-[var(--space-1)] text-[var(--color-muted)] hover:bg-[var(--color-surface-3)]', COARSE_TARGET)} aria-label="Cancel rename"><X size={14} /></IconButton>
      </div>
    )
  }

  // Display title (founder feedback 2026-10-11, point 4; R2-S4-01): server
  // title when present; for an untitled main the "<Agent> - Main Session"
  // derivation from the CURRENT agentName prop — recomputed every render,
  // never cached, so a rename flows straight through. The session's own
  // agent_id is the 4th argument — the id fallback the ruling accepts (the
  // server's own empty-name fallback) — so an unresolved name lands on
  // "jim - Main Session", and the ?? keeps the derivation's null (no name
  // AND no id — unreachable on the wire, which requires agent_id) out of the
  // accessible names and the confirm text. Non-main rows keep "Untitled
  // session" (core owns their auto-titling). Computed before the early
  // returns so the delete-confirm wording reads the same title.
  const title = sessionDisplayTitle(session, agentName, 'Untitled session', session.agent_id) ?? 'Untitled session'

  if (confirmDelete) {
    return (
      <div className="flex items-center gap-[var(--space-2)] rounded-md px-[var(--space-2-5)] py-[var(--space-2)] bg-[var(--color-surface-2)] mx-[var(--space-2)] text-[length:var(--type-body-compact-size)]">
        <span className="flex-1 truncate text-[var(--color-secondary)]">Delete "{title}"?</span>
        <Button
          variant="ghost"
          onClick={() => { setConfirmDelete(false); onDelete() }}
          disabled={deleting}
          className={cn('h-auto shrink-0 rounded px-[var(--space-2)] py-[var(--space-1)] font-[var(--font-weight-regular)] text-[length:var(--type-utility-xs-size)] text-[var(--color-error)] hover:bg-[var(--color-error)]/10', COARSE_TARGET)}
        >
          Delete
        </Button>
        <Button
          variant="ghost"
          onClick={() => setConfirmDelete(false)}
          className={cn('h-auto shrink-0 rounded px-[var(--space-2)] py-[var(--space-1)] font-[var(--font-weight-regular)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] hover:bg-[var(--color-surface-3)]', COARSE_TARGET)}
        >
          Cancel
        </Button>
      </div>
    )
  }

  const status = sessionStatusLabel(session)
  const interruptionDetail = session.lifecycle_state === 'interrupted'
    ? interruptionReasonText(session.interruption_reason)
    : ''
  const kind = sessionKindLabel(session.type)
  const background = backgroundCommandSentence(session.background_command_count)

  return (
    <div
      id={`search-result-${session.id}`}
      data-testid="session-row"
      data-session-id={session.id}
      className={cn(
        'group flex items-center gap-[var(--space-2)] rounded-md px-[var(--space-2-5)] py-[var(--space-2)] mx-[var(--space-1)] transition-colors hover:bg-[var(--color-surface-2)]',
        isHighlighted && 'bg-[var(--color-surface-2)]',
      )}
    >
      <Button variant="ghost" onClick={onSelect} aria-label={`Open ${title}`} className={cn('h-auto min-w-0 flex-1 flex-col items-start justify-start gap-[var(--space-0)] p-0 text-left hover:bg-transparent pointer-coarse:min-h-[var(--target-touch-minimum)]', COARSE_TARGET)}>
        {/* Keep metadata below the title. Button's default inline row otherwise
            lets fixed metadata squeeze same-prefix titles to a few characters.
            Phone titles wrap; desktop retains its compact single-line title. */}
        <div className={cn('w-full min-w-0 text-[length:var(--type-body-compact-size)] font-medium flex items-start gap-[var(--space-1)]', isActive ? 'text-[var(--color-accent)]' : 'text-[var(--color-secondary)]')}>
          <span data-testid={`session-title-${session.id}`} className="min-w-0 flex-1 whitespace-normal break-words sm:truncate">{title}</span>
          {isHighlighted && (
            <span className="shrink-0 rounded border border-[var(--color-border)] px-[var(--space-1)] text-[length:var(--type-caption-size)] font-[var(--font-weight-regular)] text-[var(--color-muted)]" aria-hidden="true">↵</span>
          )}
        </div>
        <div data-testid={`session-metadata-${session.id}`} className="mt-[var(--space-0-5)] w-full flex flex-wrap items-center gap-x-[var(--space-1)] gap-y-[var(--space-0-5)] whitespace-normal text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
          <Badge variant={TONE_VARIANT[status.tone]} data-testid={`session-status-${session.id}`} title={interruptionDetail ? `Interrupted: ${interruptionDetail}` : undefined}>{status.text}</Badge>
          <Badge variant="outline" data-testid={`session-kind-${session.id}`}>{kind}</Badge>
          <span>Started {formatRelative(session.created_at) || '—'}</span>
          <span aria-hidden>·</span>
          <span>Active {formatRelative(session.updated_at) || '—'}</span>
          {session.total_tokens !== undefined && (
            <span className="hidden min-[400px]:inline" data-testid={`session-tokens-${session.id}`}>
              <span aria-hidden>· </span>
              <span className="font-mono">{formatTokens(session.total_tokens)}</span>
            </span>
          )}
          {background && (
            <span data-testid={`session-background-${session.id}`}>{background}</span>
          )}
        </div>
      </Button>
      {/* NO Rename on a main (team-lead steer, 2026-10-11): core now fixes a
          main's title server-side and rejects renames with 409 "a main
          session's title is fixed" (core commit 9b9ef5935), so the affordance
          is hidden outright. This pencil is the ONLY rename affordance in the
          SPA, so hiding it here hides rename everywhere. Pinned by
          SessionRow.main-rename.test.tsx. */}
      {!isMainSession(session) && (
        <IconButton
          ref={renameButtonRef}
          onClick={() => { setVal(session.title || ''); setEditing(true) }}
          className={cn('h-auto w-auto shrink-0 rounded p-[var(--space-1)] text-[var(--color-muted)] opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 focus-visible:opacity-100 [@media(hover:none)]:opacity-100 hover:text-[var(--color-accent)] hover:bg-[var(--color-surface-3)]', COARSE_TARGET)}
          aria-label={`Rename ${title}`}
          title="Rename"
        >
          <PencilSimple size={13} />
        </IconButton>
      )}
      <IconButton
        onClick={() => {
          if (deleting || session.protected === true) return
          setConfirmDelete(true)
        }}
        aria-disabled={deleting || session.protected === true}
        className={cn('h-auto w-auto shrink-0 rounded p-[var(--space-1)] text-[var(--color-muted)] opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 focus-visible:opacity-100 [@media(hover:none)]:opacity-100 hover:text-[var(--color-error)] hover:bg-[var(--color-surface-3)] aria-disabled:opacity-30 aria-disabled:cursor-not-allowed aria-disabled:hover:text-[var(--color-muted)] aria-disabled:hover:bg-transparent', COARSE_TARGET)}
        aria-label={`Delete ${title}`}
        title={session.protected ? 'Main chat — protected' : deleting ? 'Deleting…' : 'Delete'}
      >
        <Trash size={13} />
      </IconButton>
    </div>
  )
}
