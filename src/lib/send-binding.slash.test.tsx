// #1428 step 2 RED. Spec BR-3b, BR-4, B-4, B-11.
import { describe, expect, it } from 'vitest'
import { act } from 'react'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useSessionStore } from '@/store/session'
import {
  SWITCHED,
  apiMocks,
  framesOf,
  mountComposer,
  resetBindingWorld,
  sent,
  typeIntoComposer,
} from '@/test/sendBinding1428'

describe('slash capture (spec 4.6.1 / 4.6.2)', () => {
  it('BR-3b Enter in B does not redirect with A\'s instruction; Enter in A does', async () => {
    resetBindingWorld()
    mountComposer()
    const user = userEvent.setup()
    await typeIntoComposer('/stop-redirect private A')
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    await user.click(screen.getByTestId('chat-input'))
    await user.keyboard('{Enter}')
    expect(
      framesOf('redirect', sent()).filter((frame) => (frame as { instruction?: string }).instruction === 'private A'),
      'BR-3b: B\'s Enter does not send A\'s redirect',
    ).toEqual([])
    expect(framesOf('message', sent()), 'BR-3b: B submits only its own draft').toEqual([])

    act(() => { useSessionStore.getState().setActiveSession('chat-a', 'mia', 'core') })
    await user.click(screen.getByTestId('chat-input'))
    await user.keyboard('{Enter}')
    expect(framesOf('redirect', sent()), 'BR-3b control: Enter in A redirects A').toEqual([
      expect.objectContaining({
        type: 'redirect',
        session_id: 'chat-a',
        instruction: 'private A',
        origin: { kind: 'chat', session_id: 'chat-a' },
        client_frame_id: expect.any(String),
      }),
    ])
  })

  it('BR-4 a deferred /stop-redirect flushed after a switch is not sent', async () => {
    resetBindingWorld()
    let release: (value: unknown) => void = () => {}
    apiMocks.fetchCommands.mockImplementation(() => new Promise((resolve) => { release = resolve }))
    mountComposer()
    const user = userEvent.setup()
    await typeIntoComposer('/stop-redirect x')
    await user.keyboard('{Enter}')
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    await act(async () => {
      release([
        { name: 'stop-redirect', label: '/stop-redirect', delivery: 'client', available_while_streaming: true, argument_hint: '<instruction>' },
      ])
      await new Promise((r) => setTimeout(r, 50))
    })
    expect(framesOf('redirect', sent()), 'BR-4: flush after leaving A sends nothing').toEqual([])
    act(() => { useSessionStore.getState().setActiveSession('chat-a', 'mia', 'core') })
    expect(screen.getByTestId('chat-input'), 'BR-4: the command is back in A').toHaveValue('/stop-redirect x')
    expect(screen.getByText(SWITCHED), 'BR-4: the switched-chats reason is shown').toBeTruthy()
  })

  it('BR-4 control: the same deferred command with no navigation redirects A', async () => {
    resetBindingWorld()
    let release: (value: unknown) => void = () => {}
    apiMocks.fetchCommands.mockImplementation(() => new Promise((resolve) => { release = resolve }))
    mountComposer()
    const user = userEvent.setup()
    await typeIntoComposer('/stop-redirect x')
    await user.keyboard('{Enter}')
    await act(async () => {
      release([
        { name: 'stop-redirect', label: '/stop-redirect', delivery: 'client', available_while_streaming: true, argument_hint: '<instruction>' },
      ])
      await new Promise((r) => setTimeout(r, 50))
    })
    expect(framesOf('redirect', sent()), 'BR-4 control: no navigation sends the captured redirect to A').toEqual([
      expect.objectContaining({
        type: 'redirect',
        session_id: 'chat-a',
        instruction: 'x',
        origin: { kind: 'chat', session_id: 'chat-a' },
      }),
    ])
  })
})
