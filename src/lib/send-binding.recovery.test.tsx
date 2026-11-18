// #1428 step 2 RED. Spec 4.9 BR-19, B-23 beforeunload, Unsent list.
import { describe, expect, it } from 'vitest'
import { act } from 'react'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useSessionStore } from '@/store/session'
import {
  RESTORED_LINE,
  UNCERTAIN_LINE,
  deliverFrame,
  framesOf,
  mountComposer,
  resetBindingWorld,
  sent,
  typeIntoComposer,
} from '@/test/sendBinding1428'

describe('recovery UI and the leave warning (spec 4.9 / B-23)', () => {
  it('BR-19 Open, Discard and a narrow screen keep the specified focus', async () => {
    resetBindingWorld()
    mountComposer()
    const user = userEvent.setup()
    await typeIntoComposer('left behind')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    const id = (framesOf('message', sent())[0] as { client_message_id?: string } | undefined)?.client_message_id ?? 'm-left'
    deliverFrame({
      type: 'send_refused',
      reason: 'wrong_chat',
      refused_frame: 'message',
      session_id: 'chat-b',
      origin: { kind: 'chat', session_id: 'chat-a' },
      client_message_id: id,
    })
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    await user.click(screen.getByRole('button', { name: '1 not sent' }))
    const discard = screen.getAllByRole('button', { name: 'Discard' })
    expect(discard.length, 'BR-19: more than one item').toBeGreaterThan(1)
    discard[0].focus()
    await user.click(discard[0])
    await user.click(screen.getByRole('button', { name: 'Discard' }))
    expect(document.activeElement, 'BR-19: discarding a middle item focuses the next action').toBe(
      screen.getAllByRole('button', { name: /Open|Copy/ })[0],
    )
    const last = screen.getAllByRole('button', { name: 'Discard' }).at(-1)
    last?.focus()
    await user.click(last!)
    await user.click(screen.getByRole('button', { name: 'Discard' }))
    expect(document.activeElement, 'BR-19: discarding the last item focuses the composer').toBe(
      screen.getByTestId('chat-input'),
    )
    expect(screen.getByText(RESTORED_LINE), 'BR-19: an open chat says the text was restored, not sent').toBeTruthy()
    expect(screen.getByText(UNCERTAIN_LINE), 'BR-19: an uncertain item carries the may-already line').toBeTruthy()
    expect(screen.getByText(/New chat with Mia in Sales/), 'BR-19: an uncreated chat is named').toBeTruthy()
    expect(screen.getByText('Deleted chat'), 'BR-19: a deleted origin is named').toBeTruthy()
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 375 })
    window.dispatchEvent(new Event('resize'))
    const region = screen.getByRole('status', { name: /not sent/i })
    expect(region.scrollWidth, 'BR-19: 375px does not scroll sideways').toBeLessThanOrEqual(region.clientWidth + 1)
  })

  it('B-23 warns before unload while a draft exists, and not when the box is empty', async () => {
    resetBindingWorld()
    mountComposer()
    const empty = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(empty)
    expect(empty.defaultPrevented, 'B-23: an empty box does not warn').toBe(false)
    await typeIntoComposer('keep')
    const leaving = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(leaving)
    expect(leaving.defaultPrevented, 'B-23: a non-empty draft warns').toBe(true)
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    const still = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(still)
    expect(still.defaultPrevented, 'B-23: A\'s draft still warns while B is open').toBe(true)
  })
})
