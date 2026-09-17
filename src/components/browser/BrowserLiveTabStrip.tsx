// Row A of the live-browser chrome: tabs + window controls.

import type { CSSProperties } from 'react'
import { ArrowSquareOut, Globe, Plus, X } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { tabLabel, TOOLBAR_ICON_BTN, type BrowserTabStripState } from './browserLiveViewModel'

export function BrowserLiveTabStrip({
  tabState,
  connected,
  onTabSwitch,
  onTabClose,
  onTabOpen,
  onPopOut,
  onClose,
}: {
  tabState: BrowserTabStripState | null
  connected: boolean
  onTabSwitch: (index: number) => void
  onTabClose: (index: number) => void
  onTabOpen: () => void
  onPopOut?: () => void
  onClose?: () => void
}) {
  return (
    /* == Row A: tabs + window controls =============================
        Header consolidation (operator direction, 2026-08-04): the panel used
        to spend FOUR rows on chrome -- identity/controls, handback hint,
        tabs, omnibox -- measured at 156px of a 900px panel (17.3 percent),
        all of it taken from the remote page. It is now two, the way Chrome
        and Safari do it: tabs share the top strip with the window controls,
        everything else rides the toolbar below.

        This row is UNCONDITIONAL even though the tab strip inside it is not.
        Close and Pop-out live here now, and gating the row on
        `tabs.length > 0` would take the only way to close the panel with it
        the moment the tab list arrived empty.

        FIXED height (h-browser-tabs), like the toolbar below: this panel
        pushes its own box as the remote viewport, so a header row that
        changes height forces a full capture rebuild. That is the same
        measured regression the handback hint was made always-mounted for. */
    <div className="flex h-browser-tabs min-h-browser-tabs shrink-0 items-center gap-1 px-2">
      {tabState && tabState.tabs.length > 0 ? (
        <div
          role="group"
          aria-label="Browser tabs"
          data-testid="browser-tab-strip"
          className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto"
          style={{ scrollbarWidth: 'none', msOverflowStyle: 'none' } as CSSProperties}
        >
          {tabState.tabs.map((tab) => {
            const active = tab.index === tabState.activeIndex
            const label = tabLabel(tab)
            return (
              <div
                key={tab.index}
                className={cn(
                  'flex shrink-0 max-w-[180px] items-center gap-1.5 rounded-t-md border-b-2 py-1 pl-2.5 pr-1 text-xs transition-colors',
                  // Active tab: Forge-Gold underline + full opacity + a
                  // heavier label weight — colour is never the only signal
                  // (WCAG). Inactive: dimmed, transparent underline.
                  active
                    ? 'border-[var(--color-accent)] bg-[var(--color-surface-2)] text-[var(--color-secondary)] opacity-100'
                    : 'border-transparent text-[var(--color-muted)] opacity-70 hover:bg-[var(--color-surface-1)] hover:opacity-100',
                )}
              >
                <button tabIndex={0}
                  type="button"
                  aria-pressed={active}
                  disabled={!connected}
                  onClick={() => onTabSwitch(tab.index)}
                  title={tab.title || tab.url || 'New tab'}
                  data-testid={`browser-tab-${tab.index}`}
                  className={cn(
                    'flex min-w-0 flex-1 items-center gap-1.5',
                    connected ? 'cursor-pointer' : 'cursor-not-allowed',
                    'disabled:cursor-not-allowed',
                  )}
                >
                  <Globe size={12} weight={active ? 'fill' : 'regular'} className="shrink-0" />
                  <span className={cn('min-w-0 flex-1 truncate', active && 'font-medium')}>{label}</span>
                </button>
                <button tabIndex={0}
                  type="button"
                  onClick={() => onTabClose(tab.index)}
                  disabled={!connected}
                  aria-label={`Close tab: ${label}`}
                  title="Close tab"
                  data-testid={`browser-tab-close-${tab.index}`}
                  className="shrink-0 rounded p-0.5 text-[var(--color-muted)] transition-colors hover:bg-[var(--color-surface-1)] hover:text-[var(--color-secondary)] disabled:cursor-not-allowed disabled:opacity-40"
                >
                  <X size={10} weight="bold" />
                </button>
              </div>
            )
          })}
          <button tabIndex={0}
            type="button"
            onClick={onTabOpen}
            disabled={!connected}
            aria-label="Open new tab"
            title="Open a new tab"
            data-testid="browser-tab-new"
            className="shrink-0 rounded p-1 text-[var(--color-muted)] transition-colors hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)] disabled:cursor-not-allowed disabled:opacity-40"
          >
            <Plus size={13} />
          </button>
        </div>
      ) : (
        <div className="min-w-0 flex-1" />
      )}
      {onPopOut && (
        <button tabIndex={0}
          type="button"
          onClick={onPopOut}
          aria-label="Pop out"
          title="Pop out into its own window"
          className={TOOLBAR_ICON_BTN}
        >
          <ArrowSquareOut size={16} />
        </button>
      )}
      {onClose && (
        <button tabIndex={0}
          type="button"
          onClick={onClose}
          aria-label="Close live browser panel"
          title="Close"
          className={TOOLBAR_ICON_BTN}
        >
          <X size={16} />
        </button>
      )}
    </div>
  )
}
