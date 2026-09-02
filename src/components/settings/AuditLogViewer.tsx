import { useId, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ListBullets, Funnel, ArrowsClockwise, CaretDown, CaretRight } from '@phosphor-icons/react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { SmartSelect } from '@/components/ui/smart-select'
import {
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
} from '@/components/ui/table'
import { fetchAuditLog, getErrorMessage } from '@/lib/api'
import type { AuditEntry } from '@/lib/api'
import { useUiStore } from '@/store/ui'

// ── Types ──────────────────────────────────────────────────────────────────────

interface AuditLogViewerProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

// ── Event badge config ─────────────────────────────────────────────────────────

const EVENT_STYLES: Record<string, string> = {
  tool_call:              'border-blue-500/30 bg-blue-500/10 text-blue-400',
  exec:                   'border-orange-500/30 bg-orange-500/10 text-orange-400',
  file_op:                'border-sky-500/30 bg-sky-500/10 text-sky-400',
  llm_call:               'border-violet-500/30 bg-violet-500/10 text-violet-400',
  policy_eval:            'border-purple-500/30 bg-purple-500/10 text-purple-400',
  rate_limit:             'border-yellow-500/30 bg-yellow-500/10 text-yellow-400',
  ssrf:                   'border-red-500/30 bg-red-500/10 text-red-400',
  startup:                'border-zinc-500/30 bg-zinc-500/10 text-zinc-400',
  shutdown:               'border-zinc-500/30 bg-zinc-500/10 text-zinc-400',
  security_setting_change: 'border-rose-500/30 bg-rose-500/10 text-rose-400',
}

const DECISION_STYLES: Record<string, string> = {
  allow: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-400',
  deny:  'border-red-500/30 bg-red-500/10 text-red-400',
  error: 'border-amber-500/30 bg-amber-500/10 text-amber-400',
}

// ── Sub-components ─────────────────────────────────────────────────────────────

const BADGE_FALLBACK = 'border-zinc-700 bg-zinc-800 text-zinc-400'
const BADGE_BASE = 'inline-flex items-center rounded-full border px-2 py-0.5 text-xs font-medium'

function EventBadge({ event }: { event: string }) {
  const style = EVENT_STYLES[event] ?? BADGE_FALLBACK
  return <span className={`${BADGE_BASE} ${style}`}>{event}</span>
}

function DecisionBadge({ decision }: { decision?: string }) {
  if (!decision) return <span className="text-xs text-[var(--color-muted)]">&mdash;</span>
  const style = DECISION_STYLES[decision] ?? BADGE_FALLBACK
  return <span className={`${BADGE_BASE} ${style}`}>{decision}</span>
}

// ChainStatusBadge surfaces the HMAC tamper-evident chain verification result (G4)
// so an operator can see at a glance whether the audit log is intact.
function ChainStatusBadge({ status, brokenIndex }: { status?: string; brokenIndex?: number }) {
  if (status === 'valid') {
    return <span data-testid="audit-chain-status" className={`${BADGE_BASE} border-emerald-500/30 bg-emerald-500/10 text-emerald-400`}>Chain verified ✓</span>
  }
  if (status === 'broken') {
    return (
      <span data-testid="audit-chain-status" className={`${BADGE_BASE} border-red-500/30 bg-red-500/10 text-red-400`}>
        Chain broken ✗{brokenIndex != null ? ` @ entry ${brokenIndex}` : ''}
      </span>
    )
  }
  return <span data-testid="audit-chain-status" className={`${BADGE_BASE} ${BADGE_FALLBACK}`}>Chain not checked</span>
}

function hasNonEmpty(obj?: Record<string, unknown>): boolean {
  return obj != null && Object.keys(obj).length > 0
}

function JsonBlock({ label, value }: { label: string; value: Record<string, unknown> }) {
  return (
    <div className="space-y-1">
      <p className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]">
        {label}
      </p>
      <pre className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] p-2 text-[11px] font-mono text-[var(--color-secondary)] overflow-auto max-h-32 whitespace-pre-wrap break-all">
        {JSON.stringify(value, null, 2)}
      </pre>
    </div>
  )
}

function AuditRow({ entry }: { entry: AuditEntry }) {
  const [expanded, setExpanded] = useState(false)
  // D20: security_setting_change records (see pkg/audit.SecurityChangeRecord)
  // carry actor/resource/old_value/new_value instead of the tool-call shape
  // (parameters/command/policy_rule) — a row must be expandable when any of
  // those fields is present too, or an armed god-mode change (and every
  // other security_setting_change) renders as an unexpandable, all-blank row.
  const hasDetail = hasNonEmpty(entry.parameters) ||
                    hasNonEmpty(entry.details) ||
                    !!entry.command ||
                    !!entry.policy_rule ||
                    !!entry.actor ||
                    !!entry.resource ||
                    hasNonEmpty(entry.old_value) ||
                    hasNonEmpty(entry.new_value)

  const detailId = useId()

  const formattedTs = useMemo(() => {
    try {
      return new Date(entry.timestamp).toLocaleString()
    } catch {
      return entry.timestamp
    }
  }, [entry.timestamp])

  return (
    <>
      <TableRow>
        <TableCell className="whitespace-nowrap text-xs font-mono text-[var(--color-muted)]">
          <div className="flex items-center gap-1.5">
            {hasDetail ? (
              <button tabIndex={0}
                type="button"
                onClick={() => setExpanded((v) => !v)}
                aria-expanded={expanded}
                aria-controls={detailId}
                aria-label={`${expanded ? 'Hide' : 'Show'} details for this ${entry.event} entry`}
                className="shrink-0 rounded text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors focus:outline-none"
              >
                {expanded
                  ? <CaretDown size={10} />
                  : <CaretRight size={10} />}
              </button>
            ) : (
              <span className="w-[10px] shrink-0" aria-hidden="true" />
            )}
            {formattedTs}
          </div>
        </TableCell>
        <TableCell>
          <EventBadge event={entry.event} />
        </TableCell>
        <TableCell className="text-xs font-mono text-[var(--color-secondary)] max-w-[120px] truncate">
          {entry.agent_id ?? <span className="text-[var(--color-muted)]">—</span>}
        </TableCell>
        <TableCell className="text-xs font-mono text-[var(--color-secondary)] max-w-[140px] truncate">
          {entry.tool ?? <span className="text-[var(--color-muted)]">—</span>}
        </TableCell>
        <TableCell>
          <DecisionBadge decision={entry.decision} />
        </TableCell>
        <TableCell className="text-xs text-[var(--color-muted)] max-w-[140px] truncate">
          {entry.policy_rule ?? <span className="text-[var(--color-muted)]">—</span>}
        </TableCell>
      </TableRow>
      {expanded && hasDetail && (
        <TableRow id={detailId}>
          <TableCell colSpan={6} className="bg-[var(--color-surface-1)]/60 p-3">
            <div className="space-y-3 pl-4 border-l-2 border-[var(--color-border)]">
              {entry.command && (
                <div className="space-y-1">
                  <p className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]">Command</p>
                  <pre className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] p-2 text-[11px] font-mono text-[var(--color-secondary)] overflow-auto max-h-20 whitespace-pre-wrap break-all">
                    {entry.command}
                  </pre>
                </div>
              )}
              {entry.policy_rule && (
                <div className="space-y-1">
                  <p className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]">Policy Rule</p>
                  <p className="text-xs font-mono text-[var(--color-secondary)]">{entry.policy_rule}</p>
                </div>
              )}
              {/* D20: security_setting_change shape (pkg/audit.SecurityChangeRecord) —
                  actor + resource + old_value/new_value, distinct from the
                  tool-call shape above. */}
              {entry.actor && (
                <div className="space-y-1">
                  <p className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]">Actor</p>
                  <p className="text-xs font-mono text-[var(--color-secondary)]">{entry.actor}</p>
                </div>
              )}
              {entry.resource && (
                <div className="space-y-1">
                  <p className="text-[10px] font-semibold uppercase tracking-wider text-[var(--color-muted)]">Resource</p>
                  <p className="text-xs font-mono text-[var(--color-secondary)]">{entry.resource}</p>
                </div>
              )}
              {hasNonEmpty(entry.parameters) && (
                <JsonBlock label="Parameters" value={entry.parameters!} />
              )}
              {hasNonEmpty(entry.details) && (
                <JsonBlock label="Details" value={entry.details!} />
              )}
              {hasNonEmpty(entry.old_value) && (
                <JsonBlock label="Old Value" value={entry.old_value!} />
              )}
              {hasNonEmpty(entry.new_value) && (
                <JsonBlock label="New Value" value={entry.new_value!} />
              )}
            </div>
          </TableCell>
        </TableRow>
      )}
    </>
  )
}

// ── Main component ─────────────────────────────────────────────────────────────

// EVENT_TYPE_BASE_VALUES — the original flat event family. This list is a
// FLOOR, not the whole vocabulary: pkg/audit declares over fifty dot-separated
// names (skill.call, browser.live.control_taken, workspace.create, …) and more
// arrive with every feature. Hardcoding the list on its own meant an operator
// could SEE a record the filter could not isolate, which is exactly the
// complaint that surfaced once browser activity became visible in the log.
// The live option list is this floor UNIONed with every event name actually
// present in the loaded entries (see eventTypeOptions below), so the filter
// maintains itself instead of rotting one release after every new event name.
//
// Keeping the floor also keeps the control above SmartSelect's
// SEARCHABLE_THRESHOLD (5), so it always renders the searchable cmdk variant
// rather than flipping between two different widgets as the log's contents
// change under the operator.
const EVENT_TYPE_BASE_VALUES = [
  'tool_call',
  'exec',
  'file_op',
  'llm_call',
  'policy_eval',
  'rate_limit',
  'ssrf',
  'startup',
  'shutdown',
  'security_setting_change',
]

const DECISION_OPTIONS = [
  { value: 'all',   label: 'All decisions' },
  { value: 'allow', label: 'Allow' },
  { value: 'deny',  label: 'Deny' },
  { value: 'error', label: 'Error' },
]

export function AuditLogViewer({ open, onOpenChange }: AuditLogViewerProps) {
  const { addToast } = useUiStore()
  const [eventFilter, setEventFilter] = useState('all')
  const [decisionFilter, setDecisionFilter] = useState('all')

  const { data: auditLog, isLoading, isError, error, refetch, isFetching } = useQuery({
    queryKey: ['audit-log'],
    queryFn: fetchAuditLog,
    enabled: open,
    refetchInterval: 30_000,
    retry: false,
  })

  const entries = useMemo(() => auditLog?.entries ?? [], [auditLog])

  // Live filter vocabulary: the base families plus every event name the loaded
  // records actually carry. The current selection is always retained so the
  // trigger never renders blank if the matching records age out of the log
  // between the 30s refreshes.
  const eventTypeOptions = useMemo(() => {
    const values = new Set<string>(EVENT_TYPE_BASE_VALUES)
    for (const e of entries) {
      if (e.event) values.add(e.event)
    }
    if (eventFilter !== 'all') values.add(eventFilter)
    return [
      { value: 'all', label: 'All events' },
      ...[...values].sort().map((value) => ({ value, label: value })),
    ]
  }, [entries, eventFilter])

  const filtered = useMemo(() => {
    return entries.filter((e) => {
      if (eventFilter !== 'all' && e.event !== eventFilter) return false
      if (decisionFilter !== 'all' && e.decision !== decisionFilter) return false
      return true
    })
  }, [entries, eventFilter, decisionFilter])

  function handleRefresh() {
    refetch().catch((err: unknown) => {
      addToast({ message: getErrorMessage(err, 'Refresh failed'), variant: 'error' })
    })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-5xl max-h-[85vh] flex flex-col gap-0 p-0 overflow-hidden">
        {/* Header */}
        <DialogHeader className="flex-none px-5 pt-5 pb-4 border-b border-[var(--color-border)]">
          <div className="flex items-center gap-2">
            <ListBullets size={16} weight="bold" style={{ color: 'var(--color-accent)' }} />
            <DialogTitle className="font-headline text-base">Audit Log</DialogTitle>
            <ChainStatusBadge status={auditLog?.chain_status} brokenIndex={auditLog?.chain_broken_index ?? undefined} />
            {isFetching && !isLoading && (
              <span className="text-xs text-[var(--color-muted)] ml-1">Refreshing...</span>
            )}
          </div>
        </DialogHeader>

        {/* Filter bar */}
        <div className="flex-none flex items-center gap-2 px-5 py-3 border-b border-[var(--color-border)] bg-[var(--color-surface-1)]/40">
          <Funnel size={13} style={{ color: 'var(--color-muted)' }} className="shrink-0" />
          <SmartSelect
            value={eventFilter}
            onValueChange={setEventFilter}
            triggerClassName="h-7 text-xs w-[150px]"
            ariaLabel="Event type filter"
            items={eventTypeOptions}
          />
          <SmartSelect
            value={decisionFilter}
            onValueChange={setDecisionFilter}
            triggerClassName="h-7 text-xs w-[140px]"
            ariaLabel="Decision filter"
            items={DECISION_OPTIONS}
          />
          <Button
            variant="outline"
            size="sm"
            className="h-7 px-2 gap-1.5 text-xs ml-auto"
            onClick={handleRefresh}
            disabled={isFetching}
          >
            <ArrowsClockwise size={11} className={isFetching ? 'animate-spin' : ''} />
            Refresh
          </Button>
        </div>

        {/* Table area — scrollable */}
        <div className="flex-1 overflow-y-auto min-h-0">
          {isLoading ? (
            <div className="space-y-0">
              {Array.from({ length: 8 }).map((_, i) => (
                <div
                  key={i}
                  className="h-10 border-b border-[var(--color-border)] animate-pulse"
                  style={{ backgroundColor: i % 2 === 0 ? 'var(--color-surface-1)' : 'transparent', opacity: 1 - i * 0.08 }}
                />
              ))}
            </div>
          ) : isError ? (
            <div className="flex flex-col items-center justify-center gap-3 py-16 text-center">
              <p className="text-sm text-red-400">
                Failed to load audit log{error instanceof Error ? `: ${error.message}` : '.'}
              </p>
              <Button variant="outline" size="sm" onClick={handleRefresh}>
                <ArrowsClockwise size={12} className="mr-1.5" />
                Retry
              </Button>
            </div>
          ) : filtered.length === 0 ? (
            <div className="flex flex-col items-center justify-center gap-2 py-16 text-center">
              <ListBullets size={28} weight="duotone" style={{ color: 'var(--color-muted)' }} />
              <p className="text-sm text-[var(--color-secondary)]">No audit entries found</p>
              <p className="text-xs text-[var(--color-muted)]">
                {entries.length > 0
                  ? 'Try adjusting your filters'
                  : 'Audit events will appear here as agents run'}
              </p>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className="w-[180px]">Timestamp</TableHead>
                  <TableHead className="w-[120px]">Event</TableHead>
                  <TableHead className="w-[130px]">Agent</TableHead>
                  <TableHead className="w-[150px]">Tool</TableHead>
                  <TableHead className="w-[90px]">Decision</TableHead>
                  <TableHead>Policy Rule</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {filtered.map((entry, idx) => (
                  <AuditRow key={`${entry.timestamp}-${idx}`} entry={entry} />
                ))}
              </TableBody>
            </Table>
          )}
        </div>

        {/* Footer — entry count */}
        {!isLoading && !isError && entries.length > 0 && (
          <div className="flex-none px-5 py-2.5 border-t border-[var(--color-border)] text-[10px] text-[var(--color-muted)]">
            Showing {filtered.length} of {entries.length} {entries.length === 1 ? 'entry' : 'entries'} — auto-refreshes every 30s
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
