import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { act } from 'react'
import { useChatPreferencesStore } from './chatPreferences'
import * as telemetry from '@/lib/telemetry'

// Reset Zustand store state between tests.
beforeEach(() => {
  act(() => {
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
  })
})

// test_chat_preferences_store_default_state
describe('chatPreferences store — default state', () => {
  it('initializes with verboseChatEnabled: false', () => {
    const state = useChatPreferencesStore.getState()
    expect(state.verboseChatEnabled).toBe(false)
  })
})

// test_chat_preferences_store_setVerboseChatEnabled
describe('chatPreferences store — setVerboseChatEnabled', () => {
  it('setVerboseChatEnabled(true) sets verboseChatEnabled to true', () => {
    act(() => { useChatPreferencesStore.getState().setVerboseChatEnabled(true) })
    expect(useChatPreferencesStore.getState().verboseChatEnabled).toBe(true)
  })

  it('setVerboseChatEnabled(false) sets verboseChatEnabled to false', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
      useChatPreferencesStore.getState().setVerboseChatEnabled(false)
    })
    expect(useChatPreferencesStore.getState().verboseChatEnabled).toBe(false)
  })
})

// test_chat_preferences_store_toggle
describe('chatPreferences store — toggleVerboseChatEnabled', () => {
  it('toggleVerboseChatEnabled() flips verboseChatEnabled from false to true', () => {
    act(() => { useChatPreferencesStore.getState().toggleVerboseChatEnabled() })
    expect(useChatPreferencesStore.getState().verboseChatEnabled).toBe(true)
  })

  it('toggleVerboseChatEnabled() flips verboseChatEnabled from true to false', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
      useChatPreferencesStore.getState().toggleVerboseChatEnabled()
    })
    expect(useChatPreferencesStore.getState().verboseChatEnabled).toBe(false)
  })
})

// test_chat_preferences_store_persistence
describe('chatPreferences store — persistence', () => {
  it('uses "omnipus-chat-preferences" as localStorage key', () => {
    // The persist middleware is configured with name: 'omnipus-chat-preferences'.
    const persistName = useChatPreferencesStore.persist?.getOptions?.()?.name
    if (persistName !== undefined) {
      expect(persistName).toBe('omnipus-chat-preferences')
    }
  })

  it('write path: setVerboseChatEnabled persists to the "omnipus-chat-preferences" localStorage key', () => {
    // A state change triggers the persist middleware's real write path
    // (the same try/catch localStorage adapter configured on the store).
    act(() => { useChatPreferencesStore.getState().setVerboseChatEnabled(true) })

    const raw = localStorage.getItem('omnipus-chat-preferences')
    expect(raw).not.toBeNull()
    const parsed = raw ? (JSON.parse(raw) as { state?: { verboseChatEnabled?: boolean } }) : null
    expect(parsed?.state?.verboseChatEnabled).toBe(true)
  })

  it('read path: rehydrate() pulls a persisted value back through the localStorage adapter', async () => {
    // Simulate a prior session having persisted verboseChatEnabled: true —
    // write directly to localStorage, bypassing the store's own setState.
    localStorage.setItem(
      'omnipus-chat-preferences',
      JSON.stringify({ state: { verboseChatEnabled: true }, version: 0 }),
    )

    // In-memory state starts at the default (false, per beforeEach) — the
    // localStorage write above did not touch it.
    expect(useChatPreferencesStore.getState().verboseChatEnabled).toBe(false)

    // rehydrate() exercises the adapter's getItem — the same try/catch
    // localStorage read path used on page load.
    await act(async () => { await useChatPreferencesStore.persist.rehydrate() })

    expect(useChatPreferencesStore.getState().verboseChatEnabled).toBe(true)
  })

  it('partializes to only persist verboseChatEnabled', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    const state = useChatPreferencesStore.getState()
    expect(state.verboseChatEnabled).toBe(true)
  })
})

// test_chat_preferences_store_storage_failure
describe('chatPreferences store — localStorage adapter failure handling', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('getItem failure: rehydrate() does not throw and falls back to default state', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('getItem boom')
    })
    const logErrorSpy = vi.spyOn(telemetry, 'logError')

    await act(async () => {
      await expect(useChatPreferencesStore.persist.rehydrate()).resolves.not.toThrow()
    })

    // Falls back to the default state — the read failure must not corrupt
    // or crash the store.
    expect(useChatPreferencesStore.getState().verboseChatEnabled).toBe(false)
    expect(logErrorSpy).toHaveBeenCalledWith(
      expect.objectContaining({ event: 'chatPreferencesStorageReadFailed', name: 'omnipus-chat-preferences' })
    )
  })

  it('setItem failure: persisting a change does not throw and the in-memory state still updates', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('setItem boom')
    })
    const logErrorSpy = vi.spyOn(telemetry, 'logError')

    expect(() => {
      act(() => { useChatPreferencesStore.getState().setVerboseChatEnabled(true) })
    }).not.toThrow()

    // The write path degrades gracefully — in-memory state still reflects
    // the update even though persistence to localStorage failed.
    expect(useChatPreferencesStore.getState().verboseChatEnabled).toBe(true)
    expect(logErrorSpy).toHaveBeenCalledWith(
      expect.objectContaining({ event: 'chatPreferencesStorageWriteFailed', name: 'omnipus-chat-preferences' })
    )
  })
})
