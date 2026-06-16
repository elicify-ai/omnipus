// BDD: workspacesStore tests — initial state and setActiveWorkspaceId mutations.
// Traces to: wave4-level1-project-task-mgmt spec — Zustand store for active workspace filter.

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useWorkspacesStore } from './workspacesStore'

function resetStore() {
  act(() => {
    useWorkspacesStore.setState({ activeWorkspaceId: null })
  })
}

beforeEach(resetStore)

describe('workspacesStore — initial state', () => {
  it('activeWorkspaceId is null initially', () => {
    // BDD: Given a fresh store,
    // When activeWorkspaceId is read,
    // Then it is null.
    // Traces to: wave4-level1-project-task-mgmt spec — store initial state
    const state = useWorkspacesStore.getState()
    expect(state.activeWorkspaceId).toBeNull()
  })
})

describe('workspacesStore — setActiveWorkspaceId mutations', () => {
  it('setActiveWorkspaceId("id1") sets activeWorkspaceId to "id1"', () => {
    // BDD: Given activeWorkspaceId is null,
    // When setActiveWorkspaceId("id1") is called,
    // Then activeWorkspaceId is "id1".
    // Traces to: wave4-level1-project-task-mgmt spec — workspace selection
    act(() => {
      useWorkspacesStore.getState().setActiveWorkspaceId('id1')
    })
    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('id1')
  })

  it('setActiveWorkspaceId(null) resets activeWorkspaceId to null', () => {
    // BDD: Given activeWorkspaceId is "id1",
    // When setActiveWorkspaceId(null) is called,
    // Then activeWorkspaceId is null.
    // Traces to: wave4-level1-project-task-mgmt spec — clear workspace filter
    act(() => {
      useWorkspacesStore.getState().setActiveWorkspaceId('id1')
    })
    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('id1')

    act(() => {
      useWorkspacesStore.getState().setActiveWorkspaceId(null)
    })
    expect(useWorkspacesStore.getState().activeWorkspaceId).toBeNull()
  })

  it('setActiveWorkspaceId with different values produces different outputs (differentiation test)', () => {
    // Anti-hardcode: two different inputs must produce two different outputs.
    // Traces to: wave4-level1-project-task-mgmt spec — store mutation correctness
    act(() => {
      useWorkspacesStore.getState().setActiveWorkspaceId('workspace-a')
    })
    const stateA = useWorkspacesStore.getState().activeWorkspaceId

    act(() => {
      useWorkspacesStore.getState().setActiveWorkspaceId('workspace-b')
    })
    const stateB = useWorkspacesStore.getState().activeWorkspaceId

    expect(stateA).toBe('workspace-a')
    expect(stateB).toBe('workspace-b')
    expect(stateA).not.toBe(stateB)
  })
})
