// #1428 step 2 RED. Spec BR-5, BR-6, BR-8, BR-14, BR-16, BR-24.
import { describe, expect, it } from 'vitest'
import { act } from 'react'
import { fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ChatControls } from '@/components/chat/ChatControls'
import { useSessionStore } from '@/store/session'
import {
  SWITCHED,
  UPLOAD_NETWORK,
  apiMocks,
  assertNewChatKey,
  deleteOpenChatThroughSidebar,
  deliverFrame,
  framesOf,
  makeSession,
  makeWorkspace,
  mountComposer,
  resetBindingWorld,
  seedSavedChat,
  sent,
  typeIntoComposer,
} from '@/test/sendBinding1428'

function dropFile(name: string): void {
  const zone = screen.getByTestId('composer-card').parentElement
  if (!zone) throw new Error('drop zone missing')
  fireEvent.drop(zone, { dataTransfer: { files: [new File(['x'], name, { type: 'text/plain' })] } })
}

/** uploadFiles(sessionId, File[], workspaceId). File does not JSON-serialize its name. */
function uploadedFileName(call: readonly unknown[]): string {
  const files = call[1]
  if (!Array.isArray(files)) return ''
  const file = files[0] as { name?: unknown } | undefined
  return typeof file?.name === 'string' ? file.name : ''
}

async function reachNewChat(session: ReturnType<typeof makeSession>): Promise<void> {
  seedSavedChat(session)
  apiMocks.fetchSessions.mockResolvedValue([session])
  await deleteOpenChatThroughSidebar(session.title ?? 'Sales chat')
  expect(useSessionStore.getState().activeSessionId, '4.4.4: delete leaves no saved chat').toBeNull()
}

describe('creation, upload and open-browser interleavings (spec 4.4 / 4.6)', () => {
  it('BR-5 two new chats keep their workspaces, and K1 is restored when K2 is open', async () => {
    resetBindingWorld()
    mountComposer({ sidebar: true })
    const w1 = makeWorkspace({ id: 'w1', name: 'Sales' })
    const w2 = makeWorkspace({ id: 'w2', name: 'Support' })
    apiMocks.fetchWorkspaces.mockResolvedValue([w1, w2])
    await reachNewChat(makeSession({ id: 'saved-w1', title: 'Saved W1', workspace_id: 'w1' }))
    await typeIntoComposer('file for K1')
    dropFile('k1.txt')
    // One slot per createSession call. A single `releaseCreate` is overwritten
    // by K2's create, so releasing it would complete K2, not K1 (MU-3 / MU-9).
    const pendingCreates: Array<(value: unknown) => void> = []
    apiMocks.createSession.mockImplementation(() => new Promise((resolve) => { pendingCreates.push(resolve) }))
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    expect(pendingCreates, 'BR-5: K1\'s creation is held').toHaveLength(1)
    const k1 = assertNewChatKey(
      (apiMocks.createSession.mock.calls[0]?.[0] as { new_chat_key?: string } | undefined)?.new_chat_key,
    )
    await reachNewChat(makeSession({ id: 'saved-w2', title: 'Saved W2', workspace_id: 'w2', agent_id: 'mia' }))
    await typeIntoComposer('private K2')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    expect(pendingCreates, 'BR-5: K2\'s creation is held separately').toHaveLength(2)
    const k2 = assertNewChatKey(
      (apiMocks.createSession.mock.calls[1]?.[0] as { new_chat_key?: string } | undefined)?.new_chat_key,
    )
    expect(k2, 'BR-5: each entry mints a different key').not.toBe(k1)
    await act(async () => { pendingCreates[0]!({ id: 's1', agent_id: 'mia', workspace_id: 'w1' }) })
    expect(useSessionStore.getState().activeSessionId, 'BR-5: S1 is not activated while K2 is open').toBeNull()
    expect(screen.getByText(SWITCHED), 'BR-5: K1 is ineligible and says switched chats').toBeTruthy()
    const k1Uploads = apiMocks.uploadFiles.mock.calls.filter((call) => uploadedFileName(call) === 'k1.txt')
    expect(k1Uploads, 'BR-5: K1\'s file is uploaded').not.toEqual([])
    expect(
      k1Uploads.every((call) => call[2] === 'w1'),
      'BR-5: K1\'s file is uploaded to W1',
    ).toBe(true)
    expect(
      k1Uploads.some((call) => call[2] === 'w2'),
      'BR-5: K1\'s file is not uploaded to W2',
    ).toBe(false)
    expect(framesOf('message', sent()).some((frame) => (frame as { content?: string }).content === 'private K2'), 'BR-5: K2\'s text is its own send').toBe(true)
  })

  it('BR-5 with the barrier on the upload, K1\'s file stays in W1 while K2 is open', async () => {
    // Spec BR-5: "Repeat with the barrier on the upload."
    resetBindingWorld()
    mountComposer({ sidebar: true })
    const w1 = makeWorkspace({ id: 'w1', name: 'Sales' })
    const w2 = makeWorkspace({ id: 'w2', name: 'Support' })
    apiMocks.fetchWorkspaces.mockResolvedValue([w1, w2])
    let created = 0
    apiMocks.createSession.mockImplementation(async () => {
      created += 1
      return {
        id: created === 1 ? 's1' : 's2',
        agent_id: 'mia',
        workspace_id: created === 1 ? 'w1' : 'w2',
      }
    })
    const pendingUploads: Array<(value: unknown) => void> = []
    apiMocks.uploadFiles.mockImplementation(() => new Promise((resolve) => { pendingUploads.push(resolve) }))
    await reachNewChat(makeSession({ id: 'saved-w1b', title: 'Saved W1b', workspace_id: 'w1' }))
    await typeIntoComposer('file for K1')
    dropFile('k1.txt')
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    const k1 = assertNewChatKey(
      (apiMocks.createSession.mock.calls[0]?.[0] as { new_chat_key?: string } | undefined)?.new_chat_key,
    )
    expect(pendingUploads, 'BR-5 upload: K1\'s upload is held').toHaveLength(1)
    await reachNewChat(makeSession({ id: 'saved-w2b', title: 'Saved W2b', workspace_id: 'w2', agent_id: 'mia' }))
    await typeIntoComposer('private K2')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    const k2 = assertNewChatKey(
      (apiMocks.createSession.mock.calls.at(-1)?.[0] as { new_chat_key?: string } | undefined)?.new_chat_key,
    )
    expect(k2, 'BR-5 upload: K2 mints its own key').not.toBe(k1)
    await act(async () => {
      pendingUploads[0]!({ files: [{ ref: 'media://workspace/w1/k1', name: 'k1.txt', content_type: 'text/plain' }] })
    })
    expect(useSessionStore.getState().activeSessionId, 'BR-5 upload: S1 is not activated while K2 is open').not.toBe('s1')
    expect(screen.getByText(SWITCHED), 'BR-5 upload: K1 says switched chats').toBeTruthy()
    const k1Uploads = apiMocks.uploadFiles.mock.calls.filter((call) => uploadedFileName(call) === 'k1.txt')
    expect(k1Uploads, 'BR-5 upload: K1\'s file is uploaded').not.toEqual([])
    expect(
      k1Uploads.some((call) => call[2] === 'w2'),
      'BR-5 upload: K1\'s file is not uploaded to W2',
    ).toBe(false)
    expect(
      k1Uploads.every((call) => call[2] === 'w1'),
      'BR-5 upload: K1\'s file is uploaded to W1',
    ).toBe(true)
  })

  it('BR-6 a follow-up during an upload in the same chat is accepted there', async () => {
    resetBindingWorld()
    mountComposer()
    dropFile('pending.txt')
    let release: (value: unknown) => void = () => {}
    apiMocks.uploadFiles.mockImplementation(() => new Promise((resolve) => { release = resolve }))
    const user = userEvent.setup()
    await typeIntoComposer('first')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await typeIntoComposer('follow-up')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await act(async () => {
      release({ files: [{ ref: 'media://workspace/w1/pending', name: 'pending.txt', content_type: 'text/plain' }] })
    })
    const messages = framesOf('message', sent())
    expect(messages, 'BR-6: both sends stay in chat-a').toEqual([
      expect.objectContaining({ content: 'first', session_id: 'chat-a', origin: { kind: 'chat', session_id: 'chat-a' } }),
      expect.objectContaining({ content: 'follow-up', session_id: 'chat-a', origin: { kind: 'chat', session_id: 'chat-a' } }),
    ])
  })

  it('BR-8 a follow-up after session_started keeps the new-chat origin, and a late start does not activate', async () => {
    resetBindingWorld()
    mountComposer({ sidebar: true })
    await reachNewChat(makeSession({ id: 'saved-a', title: 'Saved A', workspace_id: 'w1' }))
    const user = userEvent.setup()
    await typeIntoComposer('hello first')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    const first = framesOf('message', sent())[0] as { origin?: { new_chat_key?: string } }
    const key = assertNewChatKey(first?.origin?.new_chat_key)
    deliverFrame({
      type: 'session_started',
      session_id: 's-new',
      agent_id: 'mia',
    } as never)
    await typeIntoComposer('follow-up')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    expect(framesOf('message', sent())[1], 'BR-8: the follow-up keeps {new_chat, K}').toEqual(
      expect.objectContaining({
        content: 'follow-up',
        session_id: 's-new',
        origin: { kind: 'new_chat', new_chat_key: key },
      }),
    )
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    deliverFrame({ type: 'session_started', session_id: 's-new', agent_id: 'mia' } as never)
    expect(useSessionStore.getState().activeSessionId, 'BR-8: a duplicate start does not pull the screen back').toBe('chat-b')
  })

  it('BR-14 a first message, text-only and with a file, is delivered for the new chat', async () => {
    resetBindingWorld()
    mountComposer({ sidebar: true })
    await reachNewChat(makeSession({ id: 'saved-14', title: 'Saved 14' }))
    const user = userEvent.setup()
    await typeIntoComposer('text only')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    const textOnly = framesOf('message', sent())[0] as { origin?: { new_chat_key?: string } }
    assertNewChatKey(textOnly?.origin?.new_chat_key)
    await reachNewChat(makeSession({ id: 'saved-14b', title: 'Saved 14b' }))
    dropFile('with.txt')
    await typeIntoComposer('with file')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    const withFile = framesOf('message', sent()).at(-1) as { origin?: { new_chat_key?: string }; media?: string[] }
    assertNewChatKey(withFile?.origin?.new_chat_key)
    expect(withFile?.media?.length, 'BR-14: the file is on the first message').toBeGreaterThan(0)
  })

  it('BR-16 Open browser does not activate a chat the person has left', async () => {
    resetBindingWorld()
    mountComposer({ sidebar: true })
    await reachNewChat(makeSession({ id: 'saved-16', title: 'Saved 16', agent_id: 'mia' }))
    const { render } = await import('@testing-library/react')
    render(<ChatControls />)
    let release: (value: unknown) => void = () => {}
    apiMocks.createSession.mockImplementation(() => new Promise((resolve) => { release = resolve }))
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Open browser' }))
    const body = apiMocks.createSession.mock.calls[0]?.[0] as { new_chat_key?: string }
    const key = assertNewChatKey(body?.new_chat_key)
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    await act(async () => { release({ id: 's-browser', agent_id: 'mia' }) })
    expect(useSessionStore.getState().activeSessionId, 'BR-16: S is not activated').toBe('chat-b')
    expect(screen.queryByRole('dialog', { name: /browser/i }), 'BR-16: the panel does not open').toBeNull()
    act(() => { useSessionStore.getState().setActiveSession('s-browser', 'mia', 'core') })
    await typeIntoComposer('later in S')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    expect(framesOf('message', sent()).at(-1), 'BR-16: the later message is accepted in S with K').toEqual(
      expect.objectContaining({
        content: 'later in S',
        session_id: 's-browser',
        origin: { kind: 'new_chat', new_chat_key: key },
      }),
    )
  })

  it('BR-24 a partial upload sends no message and keeps the failed file local', async () => {
    resetBindingWorld()
    mountComposer()
    dropFile('ok.txt')
    dropFile('bad.txt')
    apiMocks.uploadFiles
      .mockResolvedValueOnce({ files: [{ ref: 'media://workspace/w1/ok', name: 'ok.txt', content_type: 'text/plain' }] })
      .mockRejectedValueOnce(Object.assign(new Error('503'), { status: 503 }))
    const user = userEvent.setup()
    await typeIntoComposer('two files')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    expect(framesOf('message', sent()), 'BR-24: no partial message').toEqual([])
    expect(screen.getByText(UPLOAD_NETWORK), 'BR-24: the network class reason').toBeTruthy()
    expect(screen.getByText('ok.txt'), 'BR-24: the uploaded file stays').toBeTruthy()
    expect(screen.getByText('bad.txt'), 'BR-24: the failed file stays local').toBeTruthy()
    apiMocks.uploadFiles.mockResolvedValue({ files: [{ ref: 'media://workspace/w1/bad', name: 'bad.txt', content_type: 'text/plain' }] })
    const calls = apiMocks.uploadFiles.mock.calls.length
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    expect(apiMocks.uploadFiles.mock.calls.length - calls, 'BR-24: Send again uploads only the failed file').toBe(1)
    expect(framesOf('message', sent()), 'BR-24: one message after the retry').toHaveLength(1)
  })
})
