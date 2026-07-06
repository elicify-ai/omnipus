// ActivityBar — persistent, non-scrolling strip above the composer showing
// live background agent/shell activity (native delegate spans, external-CLI
// 3rd-party delegate spans, and background `bash` runs). Click opens the
// ActivityPanel slide-out for full detail.
//
// Mounted once by ChatScreen.tsx, immediately after RateLimitIndicator and
// before the composer wrapper — same "static strip above composer" role, so
// it reuses that surface/border family of classes for visual consistency.
// Renders even at rest (0 running) so the "recently finished" history in the
// panel stays reachable.

import { useState } from 'react'
import { CaretRight } from '@phosphor-icons/react'
import { ActivityAvatar } from './ActivityAvatar'
import { ActivityPanel } from './ActivityPanel'
import { useRunningActivity } from '@/hooks/useRunningActivity'
import { cn } from '@/lib/utils'

const MAX_STACK_AVATARS = 4

export function ActivityBar() {
  const { runningCount, running, recentlyFinished } = useRunningActivity()
  const [panelOpen, setPanelOpen] = useState(false)

  const isIdle = runningCount === 0
  const stackItems = running.slice(0, MAX_STACK_AVATARS)

  return (
    <>
      <button
        type="button"
        data-testid="activity-bar"
        onClick={() => setPanelOpen(true)}
        aria-haspopup="dialog"
        aria-expanded={panelOpen}
        aria-label={isIdle ? 'Activity — no active background work' : `Activity — ${runningCount} running`}
        className={cn(
          'flex w-full items-center gap-2.5 rounded-lg border px-3 py-2 text-left text-xs transition-colors',
          'border-[var(--color-border)] bg-[var(--color-surface-1)] hover:bg-[var(--color-surface-2)]',
        )}
      >
        {stackItems.length > 0 ? (
          <div className="flex -space-x-2 shrink-0">
            {stackItems.map((item) => (
              <div key={item.key} className="rounded-full ring-2 ring-[var(--color-surface-1)]">
                <ActivityAvatar item={item} size="sm" />
              </div>
            ))}
          </div>
        ) : (
          <span className="h-2 w-2 shrink-0 rounded-full bg-[var(--color-muted)]" aria-hidden="true" />
        )}

        <span
          className={cn(
            'min-w-0 flex-1 truncate font-medium',
            isIdle ? 'text-[var(--color-muted)]' : 'text-[var(--color-secondary)]',
          )}
        >
          {isIdle ? 'No active background work' : `${runningCount} running`}
        </span>

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
