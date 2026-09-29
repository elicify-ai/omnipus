import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PanelDefinition } from './types'
import { usePanelShell } from './usePanelShell'
import { usePanelShellStore } from './panelShellStore'

const library: PanelDefinition = {
  id: 'library',
  title: 'Library',
  content: () => null,
  expandTarget: () => '/library',
}

beforeEach(() => {
  usePanelShellStore.setState({
    activePanel: null,
    panelWidth: null,
    guardPending: false,
    historyPushed: false,
  })
})

afterEach(() => {
  vi.restoreAllMocks()
  usePanelShellStore.getState().closePanel()
})

describe('panel width persistence failure', () => {
  it('warns once per session while retaining the settled width in memory', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('quota exceeded', 'QuotaExceededError')
    })
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    usePanelShellStore.getState().openPanel('library', { workspaceId: 'ws-1' })
    const { result } = renderHook(() => usePanelShell([library], 'dana'))

    act(() => {
      result.current.settleWidth(511)
      result.current.settleWidth(512)
    })

    expect(usePanelShellStore.getState().panelWidth).toBe(512)
    expect(warn).toHaveBeenCalledTimes(1)
    expect(warn).toHaveBeenCalledWith(
      '[side-panel] Panel width could not be persisted; using session memory only.',
    )
  })
})
