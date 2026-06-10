/**
 * RestartBanner — persistent amber banner shown when restart-gated config
 * changes are pending.
 *
 * US-B4 / #330:
 * - Primary line: plain one-line summary ("1 change waits — restart to apply").
 * - Raw config diff (key → value arrows) and systemd/docker jargon are hidden
 *   behind an "Technical details" collapsible — safe to skip for non-experts.
 * - The banner auto-hides when the diff empties (no dismiss button).
 */

import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ArrowsClockwise, CaretDown, CaretUp } from '@phosphor-icons/react'
import { usePendingRestart, PENDING_RESTART_QUERY_KEY } from '@/store/restart'
import { useAuthStore } from '@/store/auth'
import type { PendingRestartEntry } from '@/lib/api'
import { isApiError } from '@/lib/api'

// formatValue produces a human-readable transition string for a config value.
// Objects and arrays are represented as "(modified)" to avoid unreadable JSON blobs.
function formatValue(value: unknown): string {
  if (value === null || value === undefined) return 'null'
  if (typeof value === 'object') return '(modified)'
  return String(value)
}

function EntryRow({ entry }: { entry: PendingRestartEntry }) {
  const from = formatValue(entry.applied_value)
  const to = formatValue(entry.persisted_value)
  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs font-mono">
      <span className="text-[var(--color-secondary)] font-semibold">{entry.key}:</span>
      <span className="text-[var(--color-muted)]">{from}</span>
      <span className="text-[var(--color-muted)]">→</span>
      <span className="text-[var(--color-secondary)]">{to}</span>
    </div>
  )
}

// RestartBannerInner is only mounted when the user is admin — this avoids
// making a /pending-restart request for non-admin users at all.
function RestartBannerInner() {
  const queryClient = useQueryClient()
  const { entries, isLoading, isError, error } = usePendingRestart()
  const [techDetailsOpen, setTechDetailsOpen] = useState(false)

  // Loading first fetch: render nothing to avoid flicker.
  if (isLoading && entries.length === 0) return null

  // Suppress the banner for expected non-error conditions:
  //   403 — non-admin path (should not reach here but guard defensively)
  //   503 — dev_mode_bypass is active; pending-restart is inoperative
  // All other errors (500, network failures, etc.) show a visible retry state
  // so an admin who just saved a restart-gated setting is not left in the dark.
  if (isError) {
    const status = isApiError(error) ? error.status : null
    if (status === 403 || status === 503) return null
    return (
      <div
        role="status"
        className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] px-4 py-3 mb-6 flex items-center justify-between gap-4"
      >
        <span className="text-sm text-[var(--color-muted)]">
          Could not check pending restart-required changes.
        </span>
        <button
          type="button"
          className="shrink-0 text-xs text-[var(--color-secondary)] underline hover:no-underline focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)] rounded"
          onClick={() => { void queryClient.invalidateQueries({ queryKey: [...PENDING_RESTART_QUERY_KEY] }) }}
        >
          Retry
        </button>
      </div>
    )
  }

  // Empty diff: no changes pending, hide banner.
  if (entries.length === 0) return null

  function handleManualRefetch() {
    void queryClient.invalidateQueries({ queryKey: [...PENDING_RESTART_QUERY_KEY] })
  }

  const changeCount = entries.length
  const summaryText = `${changeCount} change${changeCount !== 1 ? 's' : ''} saved — restart to apply`

  return (
    <div
      role="status"
      aria-live="polite"
      className="rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-3 mb-6"
      data-testid="restart-banner"
    >
      {/* Plain one-line header row */}
      <div className="flex items-center justify-between gap-2 flex-wrap">
        <p className="text-sm font-semibold text-amber-300" data-testid="restart-banner-summary">
          {summaryText}
        </p>
        <div className="flex items-center gap-2 shrink-0">
          <button
            type="button"
            onClick={handleManualRefetch}
            className="flex items-center gap-1 text-xs text-amber-300/60 hover:text-amber-300 transition-colors"
            aria-label="Refresh pending restart status"
            title="Refresh status"
          >
            <ArrowsClockwise size={13} />
            <span>Refresh status</span>
          </button>
        </div>
      </div>

      {/* Technical details — jargon behind a collapsed disclosure (US-B4) */}
      <div className="mt-2">
        <button
          type="button"
          onClick={() => setTechDetailsOpen((o) => !o)}
          className="flex items-center gap-1.5 text-[11px] text-amber-300/60 hover:text-amber-300/80 transition-colors"
          aria-expanded={techDetailsOpen}
          data-testid="restart-banner-tech-toggle"
        >
          {techDetailsOpen ? <CaretUp size={11} /> : <CaretDown size={11} />}
          Technical details
        </button>

        {techDetailsOpen && (
          <div className="mt-2 space-y-1.5" data-testid="restart-banner-tech-details">
            {/* Per-key diff rows */}
            <div className="space-y-1">
              {entries.map((entry) => (
                <EntryRow key={entry.key} entry={entry} />
              ))}
            </div>
            {/* Supervisor jargon behind the disclosure */}
            <p className="text-[11px] text-amber-300/50 leading-relaxed mt-1">
              To apply these changes, restart via your process supervisor (systemd / docker / launchd / etc.).
              This banner will clear automatically after restart.
            </p>
          </div>
        )}
      </div>
    </div>
  )
}

// RestartBanner renders a persistent amber banner at the top of the Settings
// screen whenever the gateway has config changes that require a restart.
//
// Visibility conditions (all must hold):
//   - User role is "admin"
//   - /api/v1/config/pending-restart returned a non-empty array (no error)
//
// The banner is purely data-driven: it auto-hides when the diff empties
// (set-then-revert or post-restart). There is no dismiss button.
export function RestartBanner() {
  const role = useAuthStore((s) => s.role)

  // Gate on admin role before mounting the inner component so we never
  // issue a /pending-restart request for non-admin users.
  if (role !== 'admin') return null

  return <RestartBannerInner />
}
