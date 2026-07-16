// ActivityBar — compact, non-scrolling indicator showing live background
// agent/shell activity (native delegate spans, external-CLI 3rd-party
// delegate spans, and background `bash` runs). Click opens the ActivityPanel
// slide-out for full detail.
//
// Mounted once by OmnipusComposer (ChatScreen.tsx), rendered BELOW the
// composer card, bare on the shell background. Unlike the earlier revision, this
// renders NOTHING when idle (runningCount === 0) — mirroring RateLimitIndicator's own
// precedent of conditional mounting rather than showing an empty/idle state
// (found via /visual-qa live inspection: an always-visible, full-width
// "No active background work" bar reads as noise, not signal, exactly the
// kind of ambient clutter the Activity Bar was designed to REPLACE, not
// reintroduce). When something is running, the indicator is a compact,
// content-sized pill (no forced full-width stretch) so a single running item
// doesn't visually dominate the composer area.
//
// Tradeoff, accepted deliberately: the "recently finished" history in the
// panel is only reachable while runningCount > 0 (or was, moments ago). This
// keeps the bar a pure "something is happening right now" glance rather than
// a permanent history browser — completion is already narrated in the chat
// itself, so the bar doesn't need to be the system of record for it.

import { useState } from 'react'
import { CaretRight } from '@phosphor-icons/react'
import { ActivityAvatar } from './ActivityAvatar'
import { ActivityPanel } from './ActivityPanel'
import { useRunningActivity } from '@/hooks/useRunningActivity'

const MAX_STACK_AVATARS = 4

export function ActivityBar() {
  const { runningCount, running, recentlyFinished } = useRunningActivity()
  const [panelOpen, setPanelOpen] = useState(false)

  if (runningCount === 0) return null

  const stackItems = running.slice(0, MAX_STACK_AVATARS)

  return (
    <>
      <button tabIndex={0}
        type="button"
        data-testid="activity-bar"
        onClick={() => setPanelOpen(true)}
        aria-haspopup="dialog"
        aria-expanded={panelOpen}
        aria-label={`Activity — ${runningCount} running`}
        className="inline-flex max-w-full items-center gap-2.5 self-start rounded-full border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-1.5 text-left text-xs transition-colors hover:bg-[var(--color-surface-3)]"
      >
        <div className="flex -space-x-2 shrink-0">
          {stackItems.map((item) => (
            <div key={item.key} className="rounded-full ring-2 ring-[var(--color-surface-1)]">
              <ActivityAvatar item={item} size="sm" />
            </div>
          ))}
        </div>

        <span className="min-w-0 truncate font-medium text-[var(--color-secondary)]">{runningCount} running</span>

        <CaretRight size={12} className="shrink-0 text-[var(--color-muted)]" aria-hidden="true" />
      </button>

      <ActivityPanel
        open={panelOpen}
        onOpenChange={setPanelOpen}
        running={running}
        recentlyFinished={recentlyFinished}
      />
    </>
  )
}
