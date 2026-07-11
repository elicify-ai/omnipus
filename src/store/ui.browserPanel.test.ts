import { describe, it, expect, beforeEach } from 'vitest'
import { useUiStore } from '@/store/ui'

beforeEach(() => {
  useUiStore.getState().closeBrowserPanel()
})

describe('browserPanel UI store state (ADR-038)', () => {
  it('defaults to null (panel closed)', () => {
    expect(useUiStore.getState().browserPanel).toBeNull()
  })

  it('openBrowserPanel sets sessionId + agentId', () => {
    useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
    expect(useUiStore.getState().browserPanel).toEqual({ sessionId: 'sess-1', agentId: 'agent-1' })
  })

  it('closeBrowserPanel resets to null', () => {
    useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
    useUiStore.getState().closeBrowserPanel()
    expect(useUiStore.getState().browserPanel).toBeNull()
  })

  it('opening again with a different (session, agent) pair replaces the previous one', () => {
    useUiStore.getState().openBrowserPanel('sess-1', 'agent-1')
    useUiStore.getState().openBrowserPanel('sess-2', 'agent-2')
    expect(useUiStore.getState().browserPanel).toEqual({ sessionId: 'sess-2', agentId: 'agent-2' })
  })

  it('closeBrowserPanel is idempotent when already closed', () => {
    useUiStore.getState().closeBrowserPanel()
    expect(useUiStore.getState().browserPanel).toBeNull()
  })
})
