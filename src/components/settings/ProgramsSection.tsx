/**
 * ProgramsSection — Settings → Programs (spec cli-credentials S1: US-1, US-4,
 * US-7, FR-002, FR-009 web-chat setting, FR-019, FR-023; UI table rows
 * "Settings → Programs tab", "Row state badge", "Web-chat setting").
 *
 * Lists every program (presets and custom entries) with its words, state and
 * account, and offers paste / replace, check again, disconnect, and for
 * custom entries edit and delete. While any account check runs the list is
 * read again every second, so a row never stays "Checking the account…" on
 * screen after the gateway finished (FR-047). The list is value-free: no
 * token ever reaches this screen from the gateway.
 */
import { useEffect, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plug } from '@phosphor-icons/react'
import type { ProgramEntry, ProgramList } from '@/lib/api/generated/openapi-types'
import { fetchPrograms, programsQueryKeys, recheckProgram, updateProgramSettings } from '@/lib/api'
import {
  anyChecking,
  connectionBadgeText,
  hasCredential,
  programErrorMessage,
} from '@/lib/programs'
import { Badge, type BadgeProps } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Callout, CalloutActions, CalloutDescription } from '@/components/ui/callout'
import { Card } from '@/components/ui/card'
import { ErrorState } from '@/components/ui/error-state'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ProgramPasteDialog, replaceProgramEntry } from './ProgramPasteDialog'
import { ProgramCustomDialog } from './ProgramCustomDialog'
import { ProgramDeleteDialog, ProgramDisconnectDialog } from './ProgramConfirmDialogs'

const CHECKING_POLL_MS = 1_000

const BADGE_VARIANT: Record<ProgramEntry['connection']['state'], BadgeProps['variant']> = {
  not_connected: 'muted',
  checking: 'secondary',
  connected: 'success',
  saved_not_checked: 'warning',
  check_failed: 'error',
}

/** The state badge plus the gateway's value-free message for a failed or unchecked save. */
function ProgramState({ entry }: { entry: ProgramEntry }) {
  const { connection } = entry
  const showMessage = (connection.state === 'check_failed' || connection.state === 'saved_not_checked') && connection.message
  return (
    <div className="space-y-[var(--space-1)]">
      <Badge variant={BADGE_VARIANT[connection.state]}>{connectionBadgeText(connection)}</Badge>
      {showMessage && (
        <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] break-words">{connection.message}</p>
      )}
    </div>
  )
}

interface RowActions { // not-wire-format: callbacks from a program row to the section's dialogs
  onPaste: (entry: ProgramEntry) => void
  onDisconnect: (entry: ProgramEntry) => void
  onEdit: (entry: ProgramEntry) => void
  onDelete: (entry: ProgramEntry) => void
}

function ProgramActions({ entry, actions }: { entry: ProgramEntry; actions: RowActions }) {
  const queryClient = useQueryClient()
  const [error, setError] = useState<string | null>(null)
  const recheck = useMutation({
    // One attempt per action: the person retries, nothing is re-sent on its own.
    retry: false,
    mutationFn: () => recheckProgram(entry.id),
    onSuccess: (saved) => {
      setError(null)
      queryClient.setQueryData<ProgramList>(programsQueryKeys.list, (list) => replaceProgramEntry(list, saved))
      void queryClient.invalidateQueries({ queryKey: programsQueryKeys.list })
    },
    onError: (failure) => {
      setError(programErrorMessage(failure, 'The check did not start. Try again.'))
      void queryClient.invalidateQueries({ queryKey: programsQueryKeys.list })
    },
  })
  const connected = hasCredential(entry)
  return (
    <div className="space-y-[var(--space-1)]">
      <div className="flex flex-wrap items-center gap-[var(--space-1)]">
        <Button type="button" size="sm" variant={connected ? 'outline' : 'default'} onClick={() => actions.onPaste(entry)}>
          {connected ? 'Replace token' : 'Paste a token'}
        </Button>
        {connected && (
          <>
            <Button type="button" size="sm" variant="outline" actionState={recheck.isPending ? 'pending' : 'idle'} onClick={() => recheck.mutate()}>
              Check again
            </Button>
            <Button type="button" size="sm" variant="ghost" onClick={() => actions.onDisconnect(entry)}>
              Disconnect
            </Button>
          </>
        )}
        {entry.kind === 'custom' && (
          <>
            <Button type="button" size="sm" variant="ghost" onClick={() => actions.onEdit(entry)}>
              Edit
            </Button>
            <Button type="button" size="sm" variant="ghost" onClick={() => actions.onDelete(entry)}>
              Delete
            </Button>
          </>
        )}
      </div>
      {error && <p role="alert" className="text-[length:var(--type-caption-size)] text-[var(--color-error)]">{error}</p>}
    </div>
  )
}

function ProgramWords({ entry }: { entry: ProgramEntry }) {
  return <span className="font-mono text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{entry.program_words.join(', ')}</span>
}

/** Wide screens: a table. Narrow screens: the same rows as a list (Accessibility and Keyboard). */
function ProgramRows({ entries, actions }: { entries: ProgramEntry[]; actions: RowActions }) {
  return (
    <>
      <Table containerProps={{ className: 'hidden sm:block' }} data-testid="programs-table">
        <TableHeader>
          <TableRow>
            <TableHead>Program</TableHead>
            <TableHead>Program words</TableHead>
            <TableHead>State</TableHead>
            <TableHead>Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {entries.map((entry) => (
            <TableRow key={entry.id} data-testid={`program-${entry.id}`}>
              <TableCell className="font-medium text-[var(--color-secondary)]">{entry.display_name}</TableCell>
              <TableCell><ProgramWords entry={entry} /></TableCell>
              <TableCell><ProgramState entry={entry} /></TableCell>
              <TableCell><ProgramActions entry={entry} actions={actions} /></TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <ul className="space-y-[var(--space-2)] sm:hidden" data-testid="programs-list">
        {entries.map((entry) => (
          <li key={entry.id} className="rounded-md border border-[var(--color-border)] p-[var(--space-2-5)] space-y-[var(--space-2)]">
            <div className="flex flex-wrap items-baseline gap-[var(--space-2)]">
              <span className="font-medium text-[var(--color-secondary)]">{entry.display_name}</span>
              <ProgramWords entry={entry} />
            </div>
            <ProgramState entry={entry} />
            <ProgramActions entry={entry} actions={actions} />
          </li>
        ))}
      </ul>
    </>
  )
}

/** The web-chat setting (FR-009, founder Q1 = A: work with no starting message follows it). */
function WebChatSetting({ list }: { list: ProgramList }) {
  const queryClient = useQueryClient()
  const [failedValue, setFailedValue] = useState<boolean | null>(null)
  const save = useMutation({
    // One attempt per action: the person retries, nothing is re-sent on its own.
    retry: false,
    mutationFn: (allowed: boolean) => updateProgramSettings({ web_chat_allowed: allowed, revision: list.revision }),
    onSuccess: (saved) => {
      setFailedValue(null)
      queryClient.setQueryData(programsQueryKeys.list, saved)
    },
    onError: (_failure, allowed) => {
      setFailedValue(allowed)
      void queryClient.invalidateQueries({ queryKey: programsQueryKeys.list })
    },
  })
  const checked = save.isPending && save.variables !== undefined ? save.variables : list.web_chat_allowed
  return (
    <Card className="p-[var(--space-3)] space-y-[var(--space-2)]" aria-busy={save.isPending || undefined}>
      <div className="flex items-start justify-between gap-[var(--space-3)]">
        <div className="flex-1 min-w-0">
          <p id="programs-web-chat-label" className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
            Agents may use connected programs for work that comes from the web chat
          </p>
          <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] mt-[var(--space-0-5)]">
            On by default. Work that has no starting message, such as a heartbeat, follows this setting too.
            Each chat channel has its own switch on the Connectors page.
          </p>
        </div>
        <Switch
          checked={checked}
          aria-labelledby="programs-web-chat-label"
          data-testid="programs-web-chat-switch"
          onCheckedChange={(next) => { if (!save.isPending) save.mutate(next) }}
        />
      </div>
      {failedValue !== null && !save.isPending && (
        <p className="flex items-center gap-[var(--space-2)] text-[length:var(--type-caption-size)]">
          <span className="text-[var(--color-error)]">Not confirmed</span>
          <Button type="button" size="sm" variant="ghost" onClick={() => save.mutate(failedValue)}>Retry</Button>
        </p>
      )}
    </Card>
  )
}

/** Announces each row's state change politely (Accessibility and Keyboard). */
function useStateAnnouncement(entries: ProgramEntry[] | undefined): string {
  const previous = useRef(new Map<string, string>())
  const [announcement, setAnnouncement] = useState('')
  useEffect(() => {
    if (!entries) return
    const changed: string[] = []
    for (const entry of entries) {
      const text = connectionBadgeText(entry.connection)
      const before = previous.current.get(entry.id)
      if (before !== undefined && before !== text) changed.push(`${entry.display_name}: ${text}`)
      previous.current.set(entry.id, text)
    }
    if (changed.length > 0) setAnnouncement(changed.join('. '))
  }, [entries])
  return announcement
}

export function ProgramsSection(): React.ReactElement {
  const query = useQuery({
    queryKey: programsQueryKeys.list,
    queryFn: fetchPrograms,
    refetchInterval: (current) => (anyChecking(current.state.data?.entries) ? CHECKING_POLL_MS : false),
  })
  const [pasteFor, setPasteFor] = useState<ProgramEntry | null>(null)
  const [disconnectFor, setDisconnectFor] = useState<ProgramEntry | null>(null)
  const [deleteFor, setDeleteFor] = useState<ProgramEntry | null>(null)
  const [customOpen, setCustomOpen] = useState(false)
  const [editFor, setEditFor] = useState<ProgramEntry | null>(null)
  const announcement = useStateAnnouncement(query.data?.entries)

  const actions: RowActions = {
    onPaste: setPasteFor,
    onDisconnect: setDisconnectFor,
    onDelete: setDeleteFor,
    onEdit: (entry) => { setEditFor(entry); setCustomOpen(true) },
  }

  const list = query.data
  const customCount = list?.entries.filter((entry) => entry.kind === 'custom').length ?? 0

  return (
    <div className="space-y-[var(--space-3)]" data-testid="programs-section">
      <div className="flex items-center gap-[var(--space-2)]">
        <Plug size={18} className="text-[var(--color-secondary)]" aria-hidden="true" />
        <h2 className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">Connected programs</h2>
      </div>
      <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
        Connect a command-line program such as gh or fly once, then switch it on for the agents that may use it in their profile.
        Their commands then run with the program&apos;s login, and the token is hidden wherever output is shown or stored.
      </p>
      <p role="status" aria-live="polite" className="sr-only">{announcement}</p>

      {query.isPending ? (
        <div className="space-y-[var(--space-2)]" data-testid="programs-loading">
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
        </div>
      ) : !list ? (
        <ErrorState message="Programs could not be loaded." onRetry={() => void query.refetch()} retryDisabled={query.isFetching} />
      ) : (
        <>
          {query.isError && (
            <Callout variant="warning" data-testid="programs-refresh-error">
              <CalloutDescription>Could not refresh — this list may be out of date.</CalloutDescription>
              <CalloutActions>
                <Button type="button" size="sm" variant="outline" onClick={() => void query.refetch()}>Retry</Button>
              </CalloutActions>
            </Callout>
          )}
          <ProgramRows entries={list.entries} actions={actions} />
          <div className="flex flex-wrap items-center justify-between gap-[var(--space-2)]">
            {customCount === 0 ? (
              <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">No custom programs</p>
            ) : <span />}
            <Button type="button" size="sm" variant="outline" onClick={() => { setEditFor(null); setCustomOpen(true) }}>
              Add a custom program
            </Button>
          </div>
          <WebChatSetting list={list} />
        </>
      )}

      <ProgramPasteDialog entry={pasteFor} onOpenChange={(open) => { if (!open) setPasteFor(null) }} />
      <ProgramDisconnectDialog entry={disconnectFor} onOpenChange={(open) => { if (!open) setDisconnectFor(null) }} />
      <ProgramDeleteDialog entry={deleteFor} onOpenChange={(open) => { if (!open) setDeleteFor(null) }} />
      <ProgramCustomDialog open={customOpen} entry={editFor} onOpenChange={(open) => { setCustomOpen(open); if (!open) setEditFor(null) }} />
    </div>
  )
}
