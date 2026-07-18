// ui.store.searchModal.test.ts — the search-modal TWO-MODES contract
// (session-search vs workspace-switch). See SearchModal.tsx/useSlashMenu.ts
// for the consumers: /resume and the sidebar search icon go through
// `openSearchModal` (mode stays 'sessions'); /workspace goes through the
// new `openWorkspaceSwitcher` action (mode becomes 'workspaces').

import { describe, it, expect, beforeEach } from 'vitest'
import { useUiStore } from '@/store/ui'

const initialState = useUiStore.getState()

beforeEach(() => {
  useUiStore.setState({
    searchModalOpen: initialState.searchModalOpen,
    searchModalWorkspaceFilter: initialState.searchModalWorkspaceFilter,
    searchModalMode: 'sessions',
  })
})

describe('searchModal UI store — mode', () => {
  it('defaults to closed, sessions mode, no workspace filter', () => {
    // Fresh module state (not yet touched by openSearchModal/openWorkspaceSwitcher
    // in this file) — asserted against the store's own initial snapshot rather
    // than hardcoding `false`/`null` again, so this can't drift from ui.ts's
    // actual defaults.
    expect(initialState.searchModalOpen).toBe(false)
    expect(initialState.searchModalWorkspaceFilter).toBeNull()
    expect(initialState.searchModalMode).toBe('sessions')
  })

  it('openSearchModal() opens in sessions mode with no workspace filter', () => {
    useUiStore.getState().openSearchModal()
    expect(useUiStore.getState().searchModalOpen).toBe(true)
    expect(useUiStore.getState().searchModalMode).toBe('sessions')
    expect(useUiStore.getState().searchModalWorkspaceFilter).toBeNull()
  })

  it('openSearchModal(workspaceId) opens in sessions mode, filtered to that workspace ("More…")', () => {
    useUiStore.getState().openSearchModal('ws-42')
    expect(useUiStore.getState().searchModalOpen).toBe(true)
    expect(useUiStore.getState().searchModalMode).toBe('sessions')
    expect(useUiStore.getState().searchModalWorkspaceFilter).toBe('ws-42')
  })

  it('openWorkspaceSwitcher() opens in workspaces mode with no workspace filter', () => {
    useUiStore.getState().openWorkspaceSwitcher()
    expect(useUiStore.getState().searchModalOpen).toBe(true)
    expect(useUiStore.getState().searchModalMode).toBe('workspaces')
    expect(useUiStore.getState().searchModalWorkspaceFilter).toBeNull()
  })

  it('closeSearchModal resets mode back to sessions — a prior /workspace open cannot leak into the next /resume or sidebar-icon open', () => {
    useUiStore.getState().openWorkspaceSwitcher()
    expect(useUiStore.getState().searchModalMode).toBe('workspaces')

    useUiStore.getState().closeSearchModal()

    expect(useUiStore.getState().searchModalOpen).toBe(false)
    expect(useUiStore.getState().searchModalMode).toBe('sessions')
    expect(useUiStore.getState().searchModalWorkspaceFilter).toBeNull()
  })

  it('openSearchModal after a workspaces-mode open switches back to sessions mode (no separate close required)', () => {
    useUiStore.getState().openWorkspaceSwitcher()
    expect(useUiStore.getState().searchModalMode).toBe('workspaces')

    useUiStore.getState().openSearchModal()

    expect(useUiStore.getState().searchModalMode).toBe('sessions')
  })
})
