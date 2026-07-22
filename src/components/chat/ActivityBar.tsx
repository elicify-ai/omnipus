// ActivityBar — compact, non-scrolling indicator showing live background
// agent/shell activity (native delegate spans, external-CLI 3rd-party
// delegate spans, and background `bash` runs). Click opens the ActivityPanel
// slide-out for full detail.
//
// Mounted once by OmnipusComposer (ChatScreen.tsx), rendered BELOW the
// composer card, bare on the shell background. Renders NOTHING when there is
// nothing to show it for (see shouldMount below) — mirroring
// RateLimitIndicator's own precedent of conditional mounting rather than
// showing an empty/idle state (found via /visual-qa live inspection: an
// always-visible, full-width "No active background work" bar reads as
// noise, not signal, exactly the kind of ambient clutter the Activity Bar
// was designed to REPLACE, not reintroduce). When something is running, the
// indicator is a compact, content-sized pill (no forced full-width stretch)
// so a single running item doesn't visually dominate the composer area.
//
// Fix 1 (2026-07-16): the bar/panel pair now ALSO stays mounted while (a)
// panelOpen is true — an open panel must never vanish mid-inspection just
// because the last running item finished a beat earlier — or (b)
// recentlyFinished still retains any error/interrupted/timeout item, shown
// via a distinct failed-state pill variant (red dot + "N failed", no
// spinner). This replaces an earlier, now-false premise ("completion is
// already narrated in the chat itself, so the bar doesn't need to be the
// system of record for it") that died the moment delegation/background-bash
// cards were hidden from the thread by default (toolVisibility.ts) — a
// failed background span or bash session can now be genuinely invisible
// everywhere else at idle, so the panel is the designated failure-
// transparency surface and must stay reachable for it. A purely-successful
// idle history still disappears entirely, preserving the original "glance,
// not a permanent history browser" intent for the common case.

import { useState } from 'react'
import { ArrowsClockwise, CaretRight } from '@phosphor-icons/react'
import { ActivityAvatar } from './ActivityAvatar'
import { ActivityPanel, type SessionAction, type SessionActionTarget } from './ActivityPanel'
import { useRunningActivity } from '@/hooks/useRunningActivity'
import type { ActivityItem } from '@/hooks/useRunningActivity'
import { statusDot } from '@/lib/toolStatusConfig'
import { useChatStore } from '@/store/chat'
import { useUiStore } from '@/store/ui'

const MAX_STACK_AVATARS = 4

/**
 * Status values, drawn from `recentlyFinished`, that keep the pill/panel
 * reachable at idle (Fix 1) — the failure-transparency surface now that
 * delegation/background-bash cards are hidden from the thread by default.
 * Deliberately excludes 'cancelled' (a deliberate user/operator stop, not a
 * failure) and 'success' — only genuine failure/interruption states count.
 */
function isFailedStatus(status: ActivityItem['status']): boolean {
  return status === 'error' || status === 'interrupted' || status === 'timeout'
}

export function ActivityBar() {
  const { runningCount, running, recentlyFinished } = useRunningActivity()
  const [panelOpen, setPanelOpen] = useState(false)

  // ADR-053 FE-5 affordance dispatch (design §4 H-1..H-6). PEEK is handled
  // inside the panel; REPLY/STEER route through normal chat per FE-3 (the
  // human answers in the composer — no per-session reply card); STOP sends
  // a cancel targeting the child session id. The human→child direct-control
  // REST surface (delegate.peek/respond/steer for a human principal) is not
  // landed yet, so REPLY/STEER surface a toast pointing at the composer
  // until it is — STOP has a real wire path today via cancelStream.
  const onSessionAction = (action: SessionAction, target: SessionActionTarget) => {
    if (action === 'stop') {
      if (!target.sessionId) return
      useChatStore.getState().cancelStream(target.sessionId)
      return
    }
    if (action === 'reply' || action === 'steer') {
      useUiStore.getState().addToast({
        message:
          action === 'reply'
            ? 'Reply in chat to answer this session (FE-3 — answers route through the parent).'
            : 'Steering this session routes through chat (FE-3). Type your steer in the composer.',
        variant: 'default',
      })
    }
  }

  const isRunning = runningCount > 0
  const failedRecent = recentlyFinished.filter((item) => isFailedStatus(item.status))
  const hasFailedRecent = failedRecent.length > 0

  const shouldMount = isRunning || panelOpen || hasFailedRecent
  if (!shouldMount) return null

  const stackItems = running.slice(0, MAX_STACK_AVATARS)

  const label = isRunning
    ? `${runningCount} running`
    : hasFailedRecent
      ? `${failedRecent.length} failed`
      : 'Activity'

  return (
    <>
      <button tabIndex={0}
        type="button"
        data-testid="activity-bar"
        onClick={() => setPanelOpen(true)}
        aria-haspopup="dialog"
        aria-expanded={panelOpen}
        aria-label={`Activity — ${label}`}
        className="inline-flex max-w-full items-center gap-2.5 self-start rounded-full border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-1.5 text-left text-xs transition-colors hover:bg-[var(--color-surface-3)]"
      >
        <div className="flex -space-x-2 shrink-0">
          {stackItems.map((item) => (
            <div key={item.key} className="rounded-full ring-2 ring-[var(--color-surface-1)]">
              <ActivityAvatar item={item} size="sm" />
            </div>
          ))}
        </div>

        {/* Status indicator — running keeps the spinning ArrowsClockwise
            (same running-icon vocabulary as toolStatusConfig's
            getToolBadgeStatusConfig/getSpanStatusDot 'running' case);
            otherwise, while idle-but-mounted (Fix 1), an 8px statusDot in
            the same slot signals WHY the pill is still here — red for a
            retained failure, muted otherwise. The icon/dot is decorative;
            the adjacent label text already carries the same information for
            assistive tech. */}
        {isRunning ? (
          <ArrowsClockwise size={12} className="shrink-0 animate-spin text-[var(--color-accent)]" aria-hidden="true" />
        ) : hasFailedRecent ? (
          statusDot('bg-[var(--color-error)]')
        ) : (
          statusDot('bg-[var(--color-muted)]')
        )}
        <span
          data-testid="activity-bar-label"
          className={`min-w-0 truncate font-medium ${hasFailedRecent && !isRunning ? 'text-[var(--color-error)]' : 'text-[var(--color-secondary)]'}`}
        >
          {label}
        </span>

        <CaretRight size={12} className="shrink-0 text-[var(--color-muted)]" aria-hidden="true" />
      </button>

      <ActivityPanel
        open={panelOpen}
        onOpenChange={setPanelOpen}
        running={running}
        recentlyFinished={recentlyFinished}
        onSessionAction={onSessionAction}
      />
    </>
  )
}
