// BrowserPanelPlaceholder.tsx — the wave-0 Browser panel content (SP-31):
// the REAL BrowserLiveTabStrip chrome over fixture tabs, above a static
// viewport placeholder — "no live WebRTC required" per the demo scope.
// SP-19 lives here too: Escape inside the Browser panel NEVER closes the
// panel (the shell ignores it); Escape on the driving surface releases
// driving and the panel stays open.

import { useState } from 'react'
import { BrowserLiveTabStrip } from '@/components/browser/BrowserLiveTabStrip'
import { DEMO_BROWSER_TABS } from './fixtures'

export function BrowserPanelPlaceholder() {
  const [tabState, setTabState] = useState(DEMO_BROWSER_TABS)
  const [drivingReleased, setDrivingReleased] = useState(false)

  return (
    <div data-testid="panel-content-browser" className="flex h-full min-h-0 flex-col">
      <BrowserLiveTabStrip
        tabState={tabState}
        connected={false}
        onTabSwitch={(index) => setTabState((s) => ({ ...s, activeIndex: index }))}
        onTabClose={(index) =>
          setTabState((s) => {
            const tabs = s.tabs.filter((t) => t.index !== index)
            const activeIndex = Math.max(0, Math.min(s.activeIndex, tabs.length - 1))
            return { tabs, activeIndex }
          })
        }
        onTabOpen={() =>
          setTabState((s) => {
            const index = (s.tabs.at(-1)?.index ?? -1) + 1
            return {
              tabs: [...s.tabs, { index, title: `Tab ${index + 1}`, url: 'about:blank' }],
              activeIndex: index,
            }
          })
        }
      />
      <div
        tabIndex={0}
        data-testid="browser-driving-surface"
        aria-label="Browser driving surface — press Escape to release driving"
        onKeyDown={(e) => {
          if (e.key === 'Escape') setDrivingReleased(true)
        }}
        className="flex min-h-0 flex-1 flex-col items-center justify-center gap-[var(--space-2)] px-[var(--space-4)] text-center"
      >
        <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
          Static preview — live browsing (WebRTC) is not part of the wave-0 demo.
        </p>
        <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
          {drivingReleased
            ? 'Driving released (Escape) — the panel stays open; only the header close button closes it (SP-19).'
            : 'Focus here and press Escape to release driving — the panel stays open (SP-19).'}
        </p>
      </div>
    </div>
  )
}
