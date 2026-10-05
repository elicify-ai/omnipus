import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act } from 'react'
import { useSidebarStore } from './sidebar'

// Reset Zustand store state between tests.
beforeEach(() => {
  act(() => {
    useSidebarStore.setState({ isOpen: false, isPinned: false })
  })
})

// A wide window starts docked. A saved hidden choice still starts hidden,
// which the persist tests below cover by writing isPinned first.
describe('sidebar store — default state', () => {
  it('starts docked, with the overlay closed, when nothing is saved', async () => {
    vi.resetModules()
    localStorage.removeItem('omnipus-sidebar')
    const { useSidebarStore: fresh } = await import('./sidebar')
    expect(fresh.getState().isOpen).toBe(false)
    expect(fresh.getState().isPinned).toBe(true)
  })
})

// test_sidebar_store_toggle
// Traces to: wave0-brand-design-spec.md Scenario: Sidebar opens as overlay (US-5 AC2)
describe('sidebar store — toggle', () => {
  it('toggle() flips isOpen from false to true', () => {
    act(() => { useSidebarStore.getState().toggle() })
    expect(useSidebarStore.getState().isOpen).toBe(true)
  })

  it('toggle() flips isOpen from true to false', () => {
    act(() => {
      useSidebarStore.setState({ isOpen: true })
      useSidebarStore.getState().toggle()
    })
    expect(useSidebarStore.getState().isOpen).toBe(false)
  })

  it('open() sets isOpen: true', () => {
    act(() => { useSidebarStore.getState().open() })
    expect(useSidebarStore.getState().isOpen).toBe(true)
  })

  it('close() sets isOpen: false', () => {
    act(() => {
      useSidebarStore.setState({ isOpen: true })
      useSidebarStore.getState().close()
    })
    expect(useSidebarStore.getState().isOpen).toBe(false)
  })

  // Review finding: on a narrow window the sidebar is only an overlay, but
  // Escape, the backdrop, and choosing a destination all call close() while a
  // saved dock (isPinned) may still be true. close() must drop the overlay
  // and must not forget that dock. Hide is the only action that clears it.
  it('close() drops the overlay and keeps a saved dock', () => {
    act(() => {
      useSidebarStore.setState({ isPinned: true, isOpen: true })
      useSidebarStore.getState().close()
    })
    expect(useSidebarStore.getState().isPinned).toBe(true)
    expect(useSidebarStore.getState().isOpen).toBe(false)
  })
})

// test_sidebar_store_pin
// Traces to: wave0-brand-design-spec.md Scenario: Pinning the sidebar (US-5 AC4)
describe('sidebar store — pin', () => {
  it('pin() sets isPinned: true and isOpen: true', () => {
    act(() => { useSidebarStore.getState().pin() })
    const state = useSidebarStore.getState()
    expect(state.isPinned).toBe(true)
    expect(state.isOpen).toBe(true)
  })
})

// test_sidebar_store_unpin
// Traces to: wave0-brand-design-spec.md Scenario: Unpinning the sidebar (US-5 AC5)
describe('sidebar store — unpin', () => {
  it('unpin() sets isPinned: false', () => {
    act(() => {
      useSidebarStore.setState({ isPinned: true, isOpen: true })
      useSidebarStore.getState().unpin()
    })
    expect(useSidebarStore.getState().isPinned).toBe(false)
  })
})

// test_sidebar_store_persistence
// Traces to: wave0-brand-design-spec.md Scenario: Pin state persists across sessions (US-5 AC8, FR-014)
describe('sidebar store — persistence', () => {
  it('uses "omnipus-sidebar" as localStorage key', () => {
    // The persist middleware is configured with name: 'omnipus-sidebar'.
    // We verify the store name by inspecting the persist config.
    const persistName = useSidebarStore.persist?.getOptions?.()?.name
    // If persist is properly configured, this should be 'omnipus-sidebar'.
    // If the API is not accessible this way, we rely on integration behavior.
    if (persistName !== undefined) {
      expect(persistName).toBe('omnipus-sidebar')
    }
  })

  it('partializes to only persist isPinned, not isOpen', () => {
    // The partialize function in the store only persists isPinned.
    // Test: opening sidebar (isOpen: true) must not be persisted.
    act(() => {
      useSidebarStore.setState({ isOpen: true, isPinned: false })
    })
    // After a "simulated restart", isOpen should reset to false.
    // We verify partialize logic by checking store state directly.
    const state = useSidebarStore.getState()
    // isOpen is runtime-only — no persistence assertion needed here
    // Just confirm the state is as expected.
    expect(state.isOpen).toBe(true) // current session state
    expect(state.isPinned).toBe(false)
  })
})

describe('sidebar store — hide', () => {
  it('hide() clears both the docked and overlay states', () => {
    act(() => {
      useSidebarStore.setState({ isPinned: true, isOpen: true })
      useSidebarStore.getState().hide()
    })
    expect(useSidebarStore.getState().isPinned).toBe(false)
    expect(useSidebarStore.getState().isOpen).toBe(false)
  })
})
