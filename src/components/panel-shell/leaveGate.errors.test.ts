import { afterEach, describe, expect, it, vi } from 'vitest'

const { guard } = vi.hoisted(() => ({ guard: vi.fn() }))

vi.mock('./registry', () => ({
  getPanelDefinition: () => ({
    id: 'library',
    title: 'Library',
    content: () => null,
    fullScreen: { toSearch: () => ({}), fromSearch: () => ({}) },
    beforeLeave: guard,
    beforeLeaveRequired: () => true,
  }),
}))

import { leaveGateThen } from './leaveGate'

afterEach(() => {
  vi.restoreAllMocks()
  guard.mockReset()
})

describe('leaveGateThen failure handling', () => {
  it('logs a rejected guard and cancels the transition', async () => {
    const failure = new Error('dialog host failed')
    guard.mockRejectedValue(failure)
    const proceed = vi.fn()
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})

    leaveGateThen('library', proceed)
    await vi.waitFor(() => expect(guard).toHaveBeenCalledOnce())
    await vi.waitFor(() => expect(error).toHaveBeenCalledWith(
      '[side-panel] Leave guard failed; transition cancelled.',
      failure,
    ))

    expect(proceed).not.toHaveBeenCalled()
  })
})
