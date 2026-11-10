import { create } from 'zustand'
import { persist } from 'zustand/middleware'

// Pin is only honoured at or above this viewport width.
// Below it the sidebar is always overlay regardless of the persisted preference.
export const SIDEBAR_PIN_BREAKPOINT = 1024

interface SidebarStore {
  isOpen: boolean
  isPinned: boolean
  open: () => void
  close: () => void
  toggle: () => void
  pin: () => void
  unpin: () => void
  /** Docked or overlay: take the sidebar off the screen. */
  hide: () => void
}

export const useSidebarStore = create<SidebarStore>()(
  persist(
    (set) => ({
      isOpen: false,
      // A fresh visit is not pinned: the sidebar stays closed until it is
      // shown or pinned (founder decision, sidebar hotfix). Returning
      // visitors who saved a pin keep it — persist rehydrates isPinned over
      // this default.
      isPinned: false,

      open: () => set({ isOpen: true }),
      // Drop the overlay only. A saved dock stays saved: on a narrow window
      // the sidebar is an overlay even when isPinned is true, and Escape, the
      // backdrop, and choosing a destination all call this. Hide is the only
      // action that forgets the dock.
      close: () => set({ isOpen: false }),
      toggle: () => set((s) => ({ isOpen: !s.isOpen })),

      pin: () => set({ isPinned: true, isOpen: true }),
      unpin: () => set({ isPinned: false }),
      hide: () => set({ isPinned: false, isOpen: false }),
    }),
    {
      name: 'omnipus-sidebar',
      // US-5: handle localStorage unavailability gracefully (private browsing)
      storage: {
        getItem: (name) => {
          try {
            const value = localStorage.getItem(name)
            return value ? JSON.parse(value) : null
          } catch (err) {
            console.warn('[sidebar] localStorage read failed:', err)
            return null
          }
        },
        setItem: (name, value) => {
          try {
            localStorage.setItem(name, JSON.stringify(value))
          } catch (err) {
            console.warn('[sidebar] localStorage write failed:', err)
          }
        },
        removeItem: (name) => {
          try {
            localStorage.removeItem(name)
          } catch (err) {
            console.warn('[sidebar] localStorage remove failed:', err)
          }
        },
      },
      // Only persist the pin preference, not open/close state
      partialize: (state) => ({ isPinned: state.isPinned }) as SidebarStore,
    }
  )
)

/**
 * True when the sidebar is on screen as an OVERLAY — open, and not docked
 * (an un-pinned sidebar, or a pin the viewport is too narrow to honour, the
 * same rule Sidebar.tsx renders with).
 *
 * This is the shared Escape-precedence predicate (combined-review R3 N6):
 * Escape acts on the TOPMOST layer only, and BOTH readers derive that from
 * this one function so they can never disagree about who owns a keypress —
 * Sidebar.tsx::handleKeydown closes the overlay when it answers true, and
 * useCancelState's global cancel handler stands down when it answers true
 * (the overlay, not the turn, is the surface the user is addressing). A live
 * `getState()` + matchMedia read, not a hook: keydown listeners own no render
 * of their own and must never act on a stale closure.
 */
export function isSidebarOverlayOpen(): boolean {
  const { isOpen, isPinned } = useSidebarStore.getState()
  if (!isOpen) return false
  if (!isPinned) return true
  return !window.matchMedia(`(min-width: ${SIDEBAR_PIN_BREAKPOINT}px)`).matches
}
