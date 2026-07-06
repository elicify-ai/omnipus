import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { logError } from '@/lib/telemetry'

interface ChatPreferencesStore {
  // Verbose chat: shows every tool call in the transcript, including
  // background dispatches, status polls, and internal tool-loading calls
  // that are hidden by default. Local, per-device display preference only —
  // does not cross the gateway/API boundary and does not affect what is
  // stored in the session transcript.
  verboseChatEnabled: boolean
  setVerboseChatEnabled: (value: boolean) => void
  toggleVerboseChatEnabled: () => void
}

export const useChatPreferencesStore = create<ChatPreferencesStore>()(
  persist(
    (set, get) => ({
      verboseChatEnabled: false,

      setVerboseChatEnabled: (value) => set({ verboseChatEnabled: value }),
      toggleVerboseChatEnabled: () => {
        const { verboseChatEnabled } = get()
        set({ verboseChatEnabled: !verboseChatEnabled })
      },
    }),
    {
      name: 'omnipus-chat-preferences',
      // Handle localStorage unavailability gracefully (private browsing).
      storage: {
        getItem: (name) => {
          try {
            const value = localStorage.getItem(name)
            return value ? JSON.parse(value) : null
          } catch (err) {
            logError({
              event: 'chatPreferencesStorageReadFailed',
              name,
              message: err instanceof Error ? err.message : String(err),
            })
            return null
          }
        },
        setItem: (name, value) => {
          try {
            localStorage.setItem(name, JSON.stringify(value))
          } catch (err) {
            logError({
              event: 'chatPreferencesStorageWriteFailed',
              name,
              message: err instanceof Error ? err.message : String(err),
            })
          }
        },
        removeItem: (name) => {
          try {
            localStorage.removeItem(name)
          } catch (err) {
            logError({
              event: 'chatPreferencesStorageRemoveFailed',
              name,
              message: err instanceof Error ? err.message : String(err),
            })
          }
        },
      },
      // Only persist verboseChatEnabled — this store may grow more
      // client-side chat display preferences later.
      partialize: (state) => ({ verboseChatEnabled: state.verboseChatEnabled }) as ChatPreferencesStore,
    }
  )
)
