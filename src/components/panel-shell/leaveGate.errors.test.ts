import { afterEach, describe, expect, it, vi } from 'vitest'
import { useUiStore } from '@/store/ui'

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
  useUiStore.setState({ toasts: [] })
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

  // silent-failure-hunter finding (feature-gate round 1): a rejected leave
  // guard is only ever `console.error`'d — no visible UI feedback. A user
  // whose click triggers this sees nothing happen; the click looks broken,
  // not "safely cancelled". Expected behaviour, from the codebase's own
  // established pattern for user-visible async failures (every mutation's
  // `onError` in MailPanel.tsx: `addToast({ message: ..., variant: 'error'
  // })` against the SAME `useUiStore` this module already imports
  // elsewhere in the shell) — never derived from what leaveGateThen
  // currently does.
  it('surfaces a visible error toast when the leave guard rejects, not just a console log', async () => {
    const failure = new Error('dialog host failed')
    guard.mockRejectedValue(failure)
    const proceed = vi.fn()
    vi.spyOn(console, 'error').mockImplementation(() => {})

    expect(useUiStore.getState().toasts).toHaveLength(0)
    leaveGateThen('library', proceed)
    await vi.waitFor(() => expect(guard).toHaveBeenCalledOnce())

    await vi.waitFor(() => {
      expect(useUiStore.getState().toasts).toHaveLength(1)
    })
    const [toast] = useUiStore.getState().toasts
    expect(toast.variant).toBe('error')
    expect(toast.message.length).toBeGreaterThan(0)
    expect(proceed).not.toHaveBeenCalled()
  })
})
