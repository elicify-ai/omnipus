// #1428 step 2 RED. Spec 4.6.1, B-4, BR-3a, BR-15.
import { describe, expect, it } from 'vitest'
import { act } from 'react'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  apiMocks,
  connection,
  controlFrames,
  framesOf,
  makeWorkspace,
  mountComposer,
  resetBindingWorld,
  seedStreamingAnswer,
  sent,
  typeIntoComposer,
} from '@/test/sendBinding1428'
// The harness mocks `@/lib/api`. Everything below must stay under that
// import so a direct importer binds the mock, not the real upload client.
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { openOmnipusChat } from '@/store/omnipusChat'
import { makeBucketMessages } from '@/store/chat/messages'
import { emptyHistoryState } from '@/store/chat/history-state'
import { submitAnnotation } from '@/lib/browserAnnotate'
import { MermaidDiagram } from '@/components/chat/mermaid-renderer'
import { requireFn } from './send-binding.module'

const PANEL = 'panel-1'
const OMNIPUS_MAIN = 'omnipus-main'

describe('capture and non-navigation paths (spec 4.6.1)', () => {
  it('BR-3a local guard: forced target B sends nothing and restores the text to A once', async () => {
    resetBindingWorld()
    mountComposer()
    await typeIntoComposer('secret A')
    const capture = await requireFn('./bound-send', 'captureSubmission', 'spec 4.6.1 / BR-3a')
    const frozen = capture('composer-send', {
      text: 'secret A',
      files: [],
      libraryPicks: [],
      workspaceId: 'w1',
      agentId: 'mia',
      origin: { kind: 'chat', session_id: 'chat-a' },
      target: 'chat-b',
      navigationSensitive: true,
    }) as { text?: string; target?: string; origin?: { kind: string; session_id?: string } }
    expect(frozen, 'BR-3a: the frozen submission keeps origin A and target B').toMatchObject({
      text: 'secret A',
      target: 'chat-b',
      origin: { kind: 'chat', session_id: 'chat-a' },
    })
    const sendsBefore = apiMocks.uploadFiles.mock.calls.length + apiMocks.createSession.mock.calls.length
    const sendBound = await requireFn('./bound-send', 'sendBound', 'spec 4.6.1 / AL-1 / BR-3a')
    sendBound(frozen)
    expect(controlFrames(sent()), 'BR-3a: zero frames; the fake server is not asked').toEqual([])
    expect(
      apiMocks.uploadFiles.mock.calls.length + apiMocks.createSession.mock.calls.length,
      'BR-3a: zero REST calls for this submission',
    ).toBe(sendsBefore)
    expect(screen.getByTestId('chat-input'), 'BR-3a: text restored once into A').toHaveValue('secret A')
  })

  it('BR-15 typed /cancel in A then Enter in B submits only B', async () => {
    resetBindingWorld()
    mountComposer()
    await typeIntoComposer('/cancel')
    const user = userEvent.setup()
    await user.keyboard('{Enter}')
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    await typeIntoComposer('only B')
    await user.keyboard('{Enter}')
    const cancels = framesOf('cancel', sent())
    expect(cancels.every((frame) => (frame as { session_id?: string }).session_id !== 'chat-b'), 'BR-15: B does not receive A\'s cancel').toBe(true)
    expect(framesOf('message', sent()), 'BR-15: Enter in B sends B\'s draft only').toEqual([
      expect.objectContaining({
        type: 'message',
        content: 'only B',
        session_id: 'chat-b',
        origin: { kind: 'chat', session_id: 'chat-b' },
      }),
    ])
  })

  it('BR-15 Generate again sends the saved message with origin of that chat', () => {
    resetBindingWorld()
    const message = {
      id: 'user-saved',
      role: 'user' as const,
      content: 'saved question',
      timestamp: '2026-01-01T00:00:00Z',
      status: 'done' as const,
      firstSendStatus: 'unfinished' as const,
    }
    act(() => {
      useChatStore.setState((s) => ({
        ...s,
        sessionsById: {
          'chat-a': {
            ...makeBucketMessages([message]),
            history: emptyHistoryState(),
            isStreaming: false,
            isReplaying: false,
            toolCalls: {},
            toolCallOrder: [],
          },
        },
      }))
    })
    act(() => { useChatStore.getState().generateFirstSendAgain('user-saved') })
    expect(framesOf('message', connection().sent), 'BR-15: Generate again keeps the saved chat').toEqual([
      expect.objectContaining({
        type: 'message',
        content: 'saved question',
        session_id: 'chat-a',
        origin: { kind: 'chat', session_id: 'chat-a' },
      }),
    ])
  })

  it('BR-15 annotation switched mid-upload still sends to the panel chat', async () => {
    resetBindingWorld()
    let release: (value: unknown) => void = () => {}
    apiMocks.uploadFiles.mockImplementation(() => new Promise((resolve) => { release = resolve }))
    const file = new File(['png'], 'shot.png', { type: 'image/png' })
    const pending = submitAnnotation({
      comment: 'look here',
      file,
      point: null,
      isPointCurrent: () => false,
      sessionId: PANEL,
      agentId: 'mia',
    })
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    release({ files: [{ ref: 'media://workspace/w1/shot', name: 'shot.png', content_type: 'image/png' }] })
    await expect(pending, 'BR-15: a switch does not abort a non-navigation annotation').resolves.toBeUndefined()
    expect(framesOf('message', connection().sent), 'BR-15: origin stays the panel chat').toEqual([
      expect.objectContaining({
        type: 'message',
        session_id: PANEL,
        origin: { kind: 'chat', session_id: PANEL },
        media: ['media://workspace/w1/shot'],
      }),
    ])
  })

  it('BR-15 Mermaid fix clicked in A still targets A after a switch during the import', async () => {
    resetBindingWorld()
    mountComposer()
    const { render } = await import('@testing-library/react')
    render(<MermaidDiagram code={'not mermaid [[['} />)
    const fix = await screen.findByRole('button', { name: /fix/i })
    const user = userEvent.setup()
    const click = user.click(fix)
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    await click
    const messages = framesOf('message', connection().sent)
    expect(messages, 'BR-15: the fix is one message').toHaveLength(1)
    expect(messages[0], 'BR-15: navigation during the import does not retarget the fix').toEqual(
      expect.objectContaining({
        session_id: 'chat-a',
        origin: { kind: 'chat', session_id: 'chat-a' },
      }),
    )
  })

  it('BR-15 workflow start targets Omnipus main, not the chat that was open', () => {
    resetBindingWorld()
    const home = {
      ...makeWorkspace({ id: 'home', name: 'Home' }),
      is_default: true,
      member_configs: { omnipus: { main_session_id: OMNIPUS_MAIN } },
    }
    const outcome = openOmnipusChat({
      workspaces: [home],
      sessions: [{ id: OMNIPUS_MAIN, agent_id: 'omnipus', type: 'main', title: 'Omnipus', workspace_id: 'home' } as never],
      workflow: { workflow: 'W18', workspaceId: 'w1', workspaceName: 'Sales' },
    })
    expect(outcome, 'BR-15: the workflow opens').toBe('opened')
    expect(framesOf('message', connection().sent), 'BR-15: origin is Omnipus main, not the hosting chat').toEqual([
      expect.objectContaining({
        type: 'message',
        session_id: OMNIPUS_MAIN,
        origin: { kind: 'chat', session_id: OMNIPUS_MAIN },
      }),
    ])
  })

  it('BR-15 Stop and Escape carry the control chat as origin', async () => {
    resetBindingWorld()
    seedStreamingAnswer('chat-a')
    mountComposer()
    const user = userEvent.setup()
    await user.click(screen.getByTestId('stop-btn'))
    expect(framesOf('cancel', sent()), 'BR-15: Stop origin is the control chat').toEqual([
      expect.objectContaining({
        type: 'cancel',
        session_id: 'chat-a',
        origin: { kind: 'chat', session_id: 'chat-a' },
      }),
    ])
  })
})
