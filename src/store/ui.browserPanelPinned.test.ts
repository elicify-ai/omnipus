// ui.browserPanelPinned.test.ts — ADR-040 D4 "Take the wheel" Pin/side-by-side.
// Mirrors ui.browserPanel.test.ts's scope/style: this file covers ONLY the
// `browserPanelPinned` boolean + its toggle, not the (session, agent) pair
// state already covered there.

import { describe, it, expect, beforeEach } from 'vitest'
import { useUiStore } from '@/store/ui'

beforeEach(() => {
  useUiStore.setState({ browserPanelPinned: false })
  useUiStore.getState().closeBrowserPanel()
})

describe('browserPanelPinned UI store state (ADR-040 D4)', () => {
  it('defaults to false (unpinned overlay Sheet)', () => {
    expect(useUiStore.getState().browserPanelPinned).toBe(false)
  })

  it('toggleBrowserPanelPinned flips false -> true', () => {
    useUiStore.getState().toggleBrowserPanelPinned()
    expect(useUiStore.getState().browserPanelPinned).toBe(true)
  })

  it('toggleBrowserPanelPinned flips true -> false -> true across repeated calls', () => {
    const { toggleBrowserPanelPinned } = useUiStore.getState()
    toggleBrowserPanelPinned()
    expect(useUiStore.getState().browserPanelPinned).toBe(true)
    toggleBrowserPanelPinned()
    expect(useUiStore.getState().browserPanelPinned).toBe(false)
    toggleBrowserPanelPinned()
    expect(useUiStore.getState().browserPanelPinned).toBe(true)
  })

  it('is independent of browserPanel open/close — closing the panel does not reset the pin preference', () => {
    useUiStore.getState().toggleBrowserPanelPinned()
    useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
    useUiStore.getState().closeBrowserPanel()
    expect(useUiStore.getState().browserPanelPinned).toBe(true)
    expect(useUiStore.getState().browserPanel).toBeNull()
  })

  it('opening a browser panel does not itself change the pin preference', () => {
    expect(useUiStore.getState().browserPanelPinned).toBe(false)
    useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
    expect(useUiStore.getState().browserPanelPinned).toBe(false)
  })
})
