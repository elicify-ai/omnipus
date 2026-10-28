// Oracle: train 3 hands-on row F6 + founder Q70 — the provider-fallback note
// must explain WHY the Fallback model answered, so its full text has to stay
// readable: nothing may be cut off behind an ellipsis or a single clipped line
// (there is no hover tooltip). Checked on both real rendering paths: the live
// AssistantUI row (SystemMessage) and the history/reload row
// (VirtualSystemMessageRow). jsdom has no layout, so the oracle is the CSS
// contract: no element of the note may clip or forbid wrapping.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import type { ChatMessage } from '@/store/chat/types'

const NOTE_ID = 'fallback-note-f6'
// The exact long not-found wording from the F6 evidence (bad-model wording f601b2fb6).
const LONG_NOTE =
  'Answered by the Fallback model (deepseek/deepseek-v4.1-flash) because train3-at/nonexistent-2d573d50e3 was not found or is not offered by its provider.'

vi.mock('@assistant-ui/react', async () => (await import('@/test/assistantUiMock')).createAssistantUiMock({
  useMessage: () => ({ id: NOTE_ID, role: 'system', status: { type: 'complete' }, content: [] }),
}))

// Tailwind utilities that hide or clip text instead of wrapping it.
const CLIPPING = /(^|\s)(truncate|text-ellipsis|text-clip|whitespace-nowrap|whitespace-pre|overflow-hidden|overflow-x-hidden|overflow-clip|line-clamp-\d+|line-clamp-\[[^\]]*\])(?=\s|$)/

function note(pickNewModelHint = false): ChatMessage {
  return {
    id: NOTE_ID,
    role: 'system',
    content: LONG_NOTE,
    timestamp: '2026-10-26T05:00:00Z',
    providerFallbackNote: { message: LONG_NOTE, pickNewModelHint },
  } as ChatMessage
}

function expectFullyReadable(container: HTMLElement, text: string) {
  const textElement = screen.getByText(text)
  expect(textElement.textContent, 'the whole note is rendered, not a shortened copy').toBe(text)
  const root = screen.getByTestId('provider-fallback-note')
  for (let el: HTMLElement | null = textElement; el && container.contains(el); el = el.parentElement) {
    expect(el.className, `no clipping utility on <${el.tagName.toLowerCase()}> of the note`).not.toMatch(CLIPPING)
    if (el === root) break
  }
}

afterEach(() => {
  cleanup()
})

describe('F6 — the provider-fallback note wraps instead of truncating', () => {
  it.each([false, true])('history/reload row keeps the full reason readable (model-retired hint %s)', async (hint) => {
    const { VirtualSystemMessageRow } = await import('./SystemMessageRows')
    const { container } = render(<VirtualSystemMessageRow message={note(hint)} />)
    expectFullyReadable(container, LONG_NOTE)
    if (hint) expectFullyReadable(container, "Pick a new model in the agent's settings.")
  })

  it.each([false, true])('live row keeps the full reason readable (model-retired hint %s)', async (hint) => {
    const { useChatStore } = await import('@/store/chat')
    useChatStore.setState({ messagesById: { [NOTE_ID]: note(hint) } } as never)
    const { SystemMessage } = await import('./SystemMessageRows')
    const { container } = render(<SystemMessage />)
    expectFullyReadable(container, LONG_NOTE)
    if (hint) expectFullyReadable(container, "Pick a new model in the agent's settings.")
  })
})
