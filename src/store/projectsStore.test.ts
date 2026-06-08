// BDD: projectsStore tests — initial state and setActiveProjectId mutations.
// Traces to: wave4-level1-project-task-mgmt spec — Zustand store for active project filter.

import { describe, it, expect, beforeEach } from 'vitest'
import { act } from 'react'
import { useProjectsStore } from './projectsStore'

function resetStore() {
  act(() => {
    useProjectsStore.setState({ activeProjectId: null })
  })
}

beforeEach(resetStore)

describe('projectsStore — initial state', () => {
  it('activeProjectId is null initially', () => {
    // BDD: Given a fresh store,
    // When activeProjectId is read,
    // Then it is null.
    // Traces to: wave4-level1-project-task-mgmt spec — store initial state
    const state = useProjectsStore.getState()
    expect(state.activeProjectId).toBeNull()
  })
})

describe('projectsStore — setActiveProjectId mutations', () => {
  it('setActiveProjectId("id1") sets activeProjectId to "id1"', () => {
    // BDD: Given activeProjectId is null,
    // When setActiveProjectId("id1") is called,
    // Then activeProjectId is "id1".
    // Traces to: wave4-level1-project-task-mgmt spec — project selection
    act(() => {
      useProjectsStore.getState().setActiveProjectId('id1')
    })
    expect(useProjectsStore.getState().activeProjectId).toBe('id1')
  })

  it('setActiveProjectId(null) resets activeProjectId to null', () => {
    // BDD: Given activeProjectId is "id1",
    // When setActiveProjectId(null) is called,
    // Then activeProjectId is null.
    // Traces to: wave4-level1-project-task-mgmt spec — clear project filter
    act(() => {
      useProjectsStore.getState().setActiveProjectId('id1')
    })
    expect(useProjectsStore.getState().activeProjectId).toBe('id1')

    act(() => {
      useProjectsStore.getState().setActiveProjectId(null)
    })
    expect(useProjectsStore.getState().activeProjectId).toBeNull()
  })

  it('setActiveProjectId with different values produces different outputs (differentiation test)', () => {
    // Anti-hardcode: two different inputs must produce two different outputs.
    // Traces to: wave4-level1-project-task-mgmt spec — store mutation correctness
    act(() => {
      useProjectsStore.getState().setActiveProjectId('project-a')
    })
    const stateA = useProjectsStore.getState().activeProjectId

    act(() => {
      useProjectsStore.getState().setActiveProjectId('project-b')
    })
    const stateB = useProjectsStore.getState().activeProjectId

    expect(stateA).toBe('project-a')
    expect(stateB).toBe('project-b')
    expect(stateA).not.toBe(stateB)
  })
})
