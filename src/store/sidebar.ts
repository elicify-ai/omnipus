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
  togglePin: () => void
  /** Docked or overlay: take the sidebar off the screen. */
  hide: () => void
}

export const useSidebarStore = create<SidebarStore>()(
  persist(
    (set, get) => ({
      isOpen: false,
      // A wide window starts with the sidebar docked. Hide persists as unpinned.
      isPinned: true,

      open: () => set({ isOpen: true }),
      // Drop the overlay only. A saved dock stays saved: on a narrow window
      // the sidebar is an overlay even when isPinned is true, and Escape, the
      // backdrop, and choosing a destination all call this. Hide is the only
      // action that forgets the dock.
      close: () => set({ isOpen: false }),
      toggle: () => set((s) => ({ isOpen: !s.isOpen })),

      pin: () => set({ isPinned: true, isOpen: true }),
      unpin: () => set({ isPinned: false }),
      togglePin: () => {
        const { isPinned } = get()
        if (isPinned) {
          set({ isPinned: false, isOpen: false })
        } else {
          set({ isPinned: true, isOpen: true })
        }
      },
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
