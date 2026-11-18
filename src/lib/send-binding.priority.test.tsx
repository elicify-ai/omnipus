// #1428 step 2 RED. Spec BR-3c, BR-7, BR-7b, BR-9, BR-11, BR-12. B-8, B-10.
import { describe, expect, it } from 'vitest'
import { act } from 'react'
import { fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { addLibraryAttachment } from '@/lib/library-attachment'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import {
  SWITCHED,
  UPLOAD_404,
  apiMocks,
  deliverFrame,
  framesOf,
  mountComposer,
  resetBindingWorld,
  sent,
  typeIntoComposer,
} from '@/test/sendBinding1428'

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
    deliverFrame(refusal)
    expect(screen.getByTestId('chat-input'), 'BR-3c: a duplicate refusal does not restore twice').toHaveValue('for A')
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
    let release: (value: unknown) => void = () => {}
    apiMocks.uploadFiles.mockImplementation(() => new Promise((resolve) => { release = resolve }))
    const user = userEvent.setup()
    await typeIntoComposer('one')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await typeIntoComposer('two')
    await act(async () => {
      release({ files: [{ ref: 'media://workspace/w1/once', name: 'once.txt', content_type: 'text/plain' }] })
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
})

function seedOtherTurn(): void {
  act(() => {
    useChatStore.setState((s) => ({
      ...s,
      sessionsById: {
        ...(s.sessionsById ?? {}),
        'chat-c': { isStreaming: true, messagesById: {}, messageOrder: [], toolCalls: {}, toolCallOrder: [] },
      },
    }))
  })
}
