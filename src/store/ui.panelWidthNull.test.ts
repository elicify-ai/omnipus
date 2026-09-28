import { beforeEach, describe, expect, it } from 'vitest'
import { useUiStore } from './ui'

beforeEach(() => {
  useUiStore.getState().closePanel()
})

describe('panel width unset state', () => {
  it('uses null across initialization, panel replacement, reset and close', () => {
    expect(useUiStore.getState().panelWidth).toBeNull()

    useUiStore.getState().openPanel('library', { workspaceId: 'ws-1' })
    useUiStore.getState().setPanelWidth(555)
    useUiStore.getState().openPanel('library', { workspaceId: 'ws-2' })
    expect(useUiStore.getState().panelWidth).toBe(555)

    useUiStore.getState().openPanel('mail', { workspaceId: 'ws-2' })
    expect(useUiStore.getState().panelWidth).toBeNull()

    useUiStore.getState().setPanelWidth(444)
    useUiStore.getState().resetPanelWidth()
    expect(useUiStore.getState().panelWidth).toBeNull()

    useUiStore.getState().closePanel()
    expect(useUiStore.getState().panelWidth).toBeNull()
  })
})
