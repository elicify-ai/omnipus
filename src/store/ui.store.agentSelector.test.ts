import { describe, it, expect, beforeEach } from 'vitest'
import { useUiStore } from '@/store/ui'

beforeEach(() => {
  // Reset agentSelectorOpen to false before each test
  useUiStore.getState().setAgentSelectorOpen(false)
})

describe('agentSelectorOpen UI store flag', () => {
  it('defaults to false', () => {
    expect(useUiStore.getState().agentSelectorOpen).toBe(false)
  })

  it('setAgentSelectorOpen(true) opens it', () => {
    useUiStore.getState().setAgentSelectorOpen(true)
    expect(useUiStore.getState().agentSelectorOpen).toBe(true)
  })

  it('setAgentSelectorOpen(false) closes it', () => {
    useUiStore.getState().setAgentSelectorOpen(true)
    useUiStore.getState().setAgentSelectorOpen(false)
    expect(useUiStore.getState().agentSelectorOpen).toBe(false)
  })

  it('toggling true then false returns to false', () => {
    useUiStore.getState().setAgentSelectorOpen(true)
    expect(useUiStore.getState().agentSelectorOpen).toBe(true)
    useUiStore.getState().setAgentSelectorOpen(false)
    expect(useUiStore.getState().agentSelectorOpen).toBe(false)
  })
})
