// #1428 step 2 RED. Spec BR-3c, BR-7, BR-7b, BR-9, BR-11, BR-12. B-8, B-10.
import { describe, expect, it } from 'vitest'
import {
  SWITCHED,
  UPLOAD_404,
  apiMocks,
  assertNewChatKey,
  deleteOpenChatThroughSidebar,
  deliverFrame,
  framesOf,
  makeSession,
  mountComposer,
  resetBindingWorld,
  seedSavedChat,
  sent,
  typeIntoComposer,
} from '@/test/sendBinding1428'
import { act } from 'react'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { submissionForMessage } from '@/lib/bound-send'
import { ApiError } from '@/lib/api-error'
import { addLibraryAttachment } from '@/lib/library-attachment'
import { useChatStore } from '@/store/chat'
import { getDraft, useDraftStore } from '@/store/chat/drafts'
import { emptySessionState } from '@/store/chat/session'
import { useSessionStore } from '@/store/session'

const LIB = 'media://workspace/w1/lib-next'

function drop(name: string): void {
  const zone = screen.getByTestId('composer-card').parentElement
  if (!zone) throw new Error('drop zone missing')
  fireEvent.drop(zone, { dataTransfer: { files: [new File(['x'], name, { type: 'text/plain' })] } })
}

describe('refusal, restore and remount (spec 4.8 / 4.9)', () => {
  it('BR-3c a wrong_chat refusal settles only that submission, once', async () => {
    resetBindingWorld()
    seedOtherTurn()
    mountComposer()
    await typeIntoComposer('for A')
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    const sentMessage = framesOf('message', sent())[0] as { client_message_id?: string }
    const id = sentMessage?.client_message_id ?? 'missing-id'
    const refusal = {
      type: 'send_refused' as const,
      reason: 'wrong_chat' as const,
      refused_frame: 'message' as const,
      session_id: 'chat-b',
      origin: { kind: 'chat' as const, session_id: 'chat-a' },
      client_message_id: id,
    }
    deliverFrame(refusal)
    expect(screen.getByTestId('chat-input'), 'BR-3c: the text is back in A').toHaveValue('for A')
    expect(useChatStore.getState().sessionsById['chat-c']?.isStreaming, 'BR-3c: another chat keeps running').toBe(true)
    // Spec 4.8: the first terminal outcome wins. The pending record is released,
    // so a duplicate cannot append another recovery item (merge would hide that
    // in the box text).
    expect(useDraftStore.getState().unsent, 'BR-3c: one recovery item').toHaveLength(1)
    expect(submissionForMessage(id), 'BR-3c: the pending record is released').toBeUndefined()
    deliverFrame(refusal)
    expect(screen.getByTestId('chat-input'), 'BR-3c: a duplicate refusal does not restore twice').toHaveValue('for A')
    expect(useDraftStore.getState().unsent, 'BR-3c: a duplicate refusal adds no second item').toHaveLength(1)
    expect(submissionForMessage(id), 'BR-3c: the record stays released').toBeUndefined()
    await user.click(screen.getByRole('button', { name: 'Discard' }))
    expect(screen.getByTestId('chat-input'), 'BR-3c: Discard clears the restored box').toHaveValue('')
    const afterDiscard = useDraftStore.getState().unsent.length
    deliverFrame(refusal)
    expect(screen.getByTestId('chat-input'), 'BR-3c: a refusal after Discard does not recreate the text').toHaveValue('')
    expect(useDraftStore.getState().unsent, 'BR-3c: a refusal after Discard does not recreate an item').toHaveLength(afterDiscard)
  })

  it('BR-7 an upload released after leaving A sends nothing and lists A from C', async () => {
    resetBindingWorld()
    mountComposer()
    drop('a.txt')
    let release: (value: unknown) => void = () => {}
    apiMocks.uploadFiles.mockImplementation(() => new Promise((resolve) => { release = resolve }))
    const user = userEvent.setup()
    await typeIntoComposer('from A')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    await typeIntoComposer('newer in B')
    act(() => { useSessionStore.getState().setActiveSession('chat-c', 'mia', 'core') })
    await act(async () => {
      release({ files: [{ ref: 'media://workspace/w1/a', name: 'a.txt', content_type: 'text/plain' }] })
    })
    expect(framesOf('message', sent()), 'BR-7: eligibility sends no frame').toEqual([])
    expect(screen.getByText('1 not sent'), 'BR-7: C lists A\'s unsent item').toBeTruthy()
    expect(screen.getByText(SWITCHED), 'BR-7: the reason names the switch').toBeTruthy()
    await user.click(screen.getByRole('button', { name: 'Open' }))
    expect(screen.getByTestId('chat-input'), 'BR-7: Open shows A\'s text').toHaveValue('from A')
    expect(screen.getByText('a.txt'), 'BR-7: the file is the uploaded ref, still in A').toBeTruthy()
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    expect(screen.getByTestId('chat-input'), 'BR-7: B is exactly the newer draft').toHaveValue('newer in B')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    expect(framesOf('message', sent()), 'BR-7: Send in B sends only B').toEqual([
      expect.objectContaining({ content: 'newer in B', origin: { kind: 'chat', session_id: 'chat-b' } }),
    ])
  })

  it('BR-7b a restore merges onto the newer same-chat draft once', async () => {
    resetBindingWorld()
    mountComposer()
    drop('once.txt')
    let fail: (e: unknown) => void = () => {}
    apiMocks.uploadFiles.mockImplementation(() => new Promise((_r, reject) => { fail = reject }))
    const user = userEvent.setup()
    await typeIntoComposer('one')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await typeIntoComposer('two')
    await act(async () => {
      fail(new ApiError(503, 'down'))
    })
    expect(screen.getByTestId('chat-input'), 'BR-7b: one then the newer two').toHaveValue('one\ntwo')
    expect(screen.getAllByText('once.txt'), 'BR-7b: the file appears once').toHaveLength(1)
  })

  it('BR-9 a remount does not lose A\'s upload, and B can send', async () => {
    resetBindingWorld()
    const first = mountComposer()
    drop('keep.txt')
    let release: (value: unknown) => void = () => {}
    apiMocks.uploadFiles.mockImplementation(() => new Promise((resolve) => { release = resolve }))
    const user = userEvent.setup()
    await typeIntoComposer('keep me')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    first.unmount()
    mountComposer()
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    await typeIntoComposer('from B')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await act(async () => {
      release({ files: [{ ref: 'media://workspace/w1/keep', name: 'keep.txt', content_type: 'text/plain' }] })
    })
    expect(framesOf('message', sent()), 'BR-9: only B\'s send goes out').toEqual([
      expect.objectContaining({ content: 'from B', origin: { kind: 'chat', session_id: 'chat-b' } }),
    ])
    act(() => { useSessionStore.getState().setActiveSession('chat-a', 'mia', 'core') })
    expect(screen.getByTestId('chat-input'), 'BR-9: A is restored after the remount').toHaveValue('keep me')
    expect(screen.getByText('keep.txt'), 'BR-9: A\'s file survived the remount').toBeTruthy()
  })

  it('BR-11 a library file picked during an upload is not in the older frame', async () => {
    resetBindingWorld()
    mountComposer()
    drop('older.txt')
    let release: (value: unknown) => void = () => {}
    apiMocks.uploadFiles.mockImplementation(() => new Promise((resolve) => { release = resolve }))
    const user = userEvent.setup()
    await typeIntoComposer('older')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    act(() => {
      addLibraryAttachment({ mediaId: 'next', ref: LIB, filename: 'next.pdf', contentType: 'application/pdf' })
    })
    await act(async () => {
      release({ files: [{ ref: 'media://workspace/w1/older', name: 'older.txt', content_type: 'text/plain' }] })
    })
    const older = framesOf('message', sent())[0] as { media?: string[] }
    expect(older?.media ?? [], 'BR-11: the library file is not in the older frame').not.toContain(LIB)
    expect(screen.getByTestId('library-chip-next'), 'BR-11: the library file stays staged').toBeTruthy()
  })

  it('BR-12 a confirmed 404 is a deleted chat; a 500 is not', async () => {
    // Live-positive control for the REST-404 test below (spec r9 BR-12).
    // The confirmed-404 signal is this error frame, not an HTTP status.
    resetBindingWorld()
    mountComposer()
    await typeIntoComposer('gone')
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    const id = (framesOf('message', sent())[0] as { client_message_id?: string })?.client_message_id ?? 'm'
    deliverFrame({
      type: 'error',
      code: 'not_found',
      message: '404',
      client_message_id: id,
      session_id: 'chat-a',
    } as never)
    expect(screen.getByText('Deleted chat'), 'BR-12: 404 is deletion').toBeTruthy()
    expect(screen.getByRole('button', { name: 'Copy' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Discard' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Move text here' })).toBeTruthy()
    deliverFrame({
      type: 'error',
      code: 'internal',
      message: '500',
      client_message_id: 'other',
      session_id: 'chat-b',
    } as never)
    expect(screen.getAllByText('Deleted chat'), 'BR-12: a 500 does not add a deleted item').toHaveLength(1)
    expect(screen.getByText(UPLOAD_404)).toBeTruthy()
  })

  it('BR-12 a REST 404 alone does not mark the chat deleted', async () => {
    // Spec r9 BR-12: a REST 404 is not the confirmed-404 signal. Spec 4.6.3:
    // an upload HTTP 404 restores the draft with "This chat no longer exists"
    // and sends no message. The error{code:not_found} frame is the signal that
    // does mark "Deleted chat"; that control is the previous test in this file.
    resetBindingWorld()
    mountComposer()
    drop('gone.txt')
    apiMocks.uploadFiles.mockRejectedValueOnce(new ApiError(404, 'missing'))
    const user = userEvent.setup()
    await typeIntoComposer('still here')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    expect(apiMocks.uploadFiles, 'BR-12: the file is a REST upload').toHaveBeenCalled()
    const upload = apiMocks.uploadFiles.mock.results.at(-1)?.value as Promise<unknown>
    await expect(upload, 'BR-12: the upload answer is HTTP 404').rejects.toMatchObject({ status: 404 })
    expect(
      screen.queryByText('Deleted chat'),
      'BR-12: a REST 404 alone does not mark the chat deleted',
    ).toBeNull()
    expect(framesOf('message', sent()), 'BR-12: an upload 404 sends no message').toEqual([])
    expect(screen.getByText(UPLOAD_404), 'BR-12: the upload 404 uses the class reason').toBeTruthy()
    expect(screen.getByTestId('chat-input'), 'BR-12: the text stays in the draft').toHaveValue('still here')
  })

  it('BR-18 a failed creation shows New chat (not created) and sends nothing', async () => {
    // Spec 4.8 line 620: creation for K failed restores the draft and, once K
    // is not open, the item line is exactly "New chat (not created)".
    // Spec 4.6.3: no partial message is sent.
    resetBindingWorld()
    mountComposer({ sidebar: true })
    const saved = makeSession({ id: 'saved-create', title: 'Saved Create', workspace_id: 'w1', agent_id: 'mia' })
    seedSavedChat(saved, undefined, undefined)
    apiMocks.fetchSessions.mockResolvedValue([saved])
    apiMocks.fetchAgents.mockResolvedValue([{ id: 'mia', name: 'Mia', type: 'core' } as never])
    await deleteOpenChatThroughSidebar('Saved Create')
    const key = assertNewChatKey(useSessionStore.getState().newChatKey)
    drop('plan.txt')
    apiMocks.createSession.mockRejectedValueOnce(new ApiError(500, 'nope'))
    const user = userEvent.setup()
    await typeIntoComposer('make the chat')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await waitFor(() => expect(apiMocks.createSession, 'BR-18: creation was attempted').toHaveBeenCalled())
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    await waitFor(() => {
      expect(screen.getByText('New chat (not created)'), 'BR-18: the pinned creation-failure line').toBeTruthy()
    })
    expect(framesOf('message', sent()), 'BR-18: a failed creation sends no message').toEqual([])
    expect(getDraft(key).text, 'BR-18: the text is restored into K').toBe('make the chat')
  })

  it('BR-18 sign-out while a creation is in flight sends nothing when it completes', async () => {
    // Spec BR-18: a creation in flight, then sign out, then it completes.
    // Nothing reappears and no partial message is sent.
    resetBindingWorld()
    const first = mountComposer({ sidebar: true })
    const saved = makeSession({ id: 'saved-flight', title: 'Saved Flight', workspace_id: 'w1', agent_id: 'mia' })
    seedSavedChat(saved, undefined, undefined)
    apiMocks.fetchSessions.mockResolvedValue([saved])
    apiMocks.fetchAgents.mockResolvedValue([{ id: 'mia', name: 'Mia', type: 'core' } as never])
    await deleteOpenChatThroughSidebar('Saved Flight')
    drop('late.txt')
    let release: (value: unknown) => void = () => {}
    apiMocks.createSession.mockImplementation(() => new Promise((resolve) => { release = resolve }))
    const user = userEvent.setup()
    await typeIntoComposer('in flight')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await waitFor(() => expect(apiMocks.createSession, 'BR-18: the creation started').toHaveBeenCalled())
    await user.click(screen.getByRole('button', { name: 'Open user menu' }))
    await user.click(screen.getByRole('menuitem', { name: 'Sign out' }))
    await act(async () => {
      release({ id: 'created-late', agent_id: 'mia' })
      await Promise.resolve()
    })
    expect(framesOf('message', sent()), 'BR-18: the late creation sends nothing').toEqual([])
    first.unmount()
    mountComposer()
    expect(screen.getByTestId('chat-input'), 'BR-18: nothing reappears').toHaveValue('')
    expect(screen.queryByText('1 not sent'), 'BR-18: the Unsent list is gone').toBeNull()
  })
})

function seedOtherTurn(): void {
  act(() => {
    useChatStore.setState((s) => ({
      ...s,
      sessionsById: {
        ...(s.sessionsById ?? {}),
        'chat-c': {
          ...emptySessionState(),
          isStreaming: true,
          messagesById: {},
          messageOrder: [],
          toolCalls: {},
          toolCallOrder: [],
        },
      },
    }))
  })
}
