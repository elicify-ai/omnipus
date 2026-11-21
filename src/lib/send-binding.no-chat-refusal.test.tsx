// Spec 4.8 "Local origin/target refusal", C-1, C5: with no chat open and no
// new-chat key, a send is refused locally. No frame, no bubble, no placeholder.
// The text stays in the box. The toast is SEND_TEXT.noChatOpen, verbatim.
import { describe, expect, it } from 'vitest'
import { act } from 'react'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SEND_TEXT } from '@/lib/bound-send'
import { NO_CHAT_KEY } from '@/store/chat/draft-state'
import { addDraftFiles, getDraft, setDraftText } from '@/store/chat/drafts'
import { enterBlockedNoChat } from '@/store/session/newChatKey'
import { useChatStore } from '@/store/chat'
import { useUiStore } from '@/store/ui'
import {
  framesOf,
  mountComposer,
  resetBindingWorld,
  sent,
  typeIntoComposer,
} from '@/test/sendBinding1428'

const KEPT = 'kept in the box'
const NO_CHAT = 'There is no chat open to send to. Your text is kept.'

function noChat(): void {
  resetBindingWorld()
  act(() => { enterBlockedNoChat('unavailable') })
  mountComposer()
}

function expectRefusedLocally(textStillInBox: string): void {
  expect(NO_CHAT, 'spec 4.8: the toast wording is SEND_TEXT.noChatOpen').toBe(SEND_TEXT.noChatOpen)
  expect(framesOf('message', sent()), 'spec 4.8: no message frame leaves').toEqual([])
  expect(sent(), 'spec 4.8: no frame of any kind leaves').toEqual([])
  expect(screen.getByTestId('chat-input'), 'spec 4.8: the text stays in the box').toHaveValue(textStillInBox)
  expect(useChatStore.getState().messages, 'spec 4.8: no bubble and no placeholder').toEqual([])
  expect(useChatStore.getState().pendingFirstSend, 'spec 4.8: nothing is created').toBeNull()
  const toast = useUiStore.getState().toasts.find((item) => item.message === NO_CHAT)
  expect(toast?.message, 'spec 4.8: the toast is the exact no-chat sentence').toBe(NO_CHAT)
  expect(toast?.variant, 'spec 4.8: the refusal is an error').toBe('error')
}

describe('no chat open and no new-chat key (spec 4.8)', () => {
  it('a text submit is refused locally and the text stays in the box', async () => {
    noChat()
    act(() => { setDraftText(NO_CHAT_KEY, KEPT) })
    await typeIntoComposer(KEPT)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    expectRefusedLocally(KEPT)
    expect(getDraft(NO_CHAT_KEY).text, 'spec 4.8: the draft text is kept').toBe(KEPT)
  })

  it('an attachments-only submit is refused locally the same way', async () => {
    noChat()
    const file = new File(['x'], 'notes.txt', { type: 'text/plain' })
    act(() => { addDraftFiles(NO_CHAT_KEY, [file]) })
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    expect(framesOf('message', sent()), 'spec 4.8: an attachments-only send with no chat sends no frame').toEqual([])
    expect(sent(), 'spec 4.8: no frame of any kind leaves').toEqual([])
    expect(useChatStore.getState().messages, 'spec 4.8: no bubble and no placeholder').toEqual([])
    const kept = getDraft(NO_CHAT_KEY).files
    expect(kept, 'spec 4.8: the attachment stays staged').toHaveLength(1)
    expect(kept[0]?.kind === 'local' && kept[0].file.name, 'spec 4.8: the staged file is the one submitted').toBe('notes.txt')
    expect(screen.getByText('notes.txt'), 'spec 4.8: the attachment stays in the box').toBeTruthy()
    const toast = useUiStore.getState().toasts.find((item) => item.message === NO_CHAT)
    expect(toast?.message, 'spec 4.8: the toast is the exact no-chat sentence').toBe(NO_CHAT)
    expect(toast?.variant).toBe('error')
  })
})
