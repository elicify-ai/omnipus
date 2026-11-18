// #1428 step 2 RED. Spec 4.4.3, 4.5 rules 1-7, B-1, B-2, B-3, B-12, B-19, B-28.
// Oracles: SPEC-C r9. Not the spike, not observed output.
import { afterEach, describe, expect, it } from 'vitest'
import { act } from 'react'
import { fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  BaseComposerRuntimeCore,
  ComposerRuntimeImpl,
  DefaultThreadComposerRuntimeCore,
  ExternalStoreThreadRuntimeCore,
} from '@assistant-ui/core/internal'
import { addLibraryAttachment, clearLibraryAttachments } from '@/lib/library-attachment'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import {
  composerProbe,
  connection,
  framesOf,
  input,
  mountComposer,
  resetBindingWorld,
  sent,
  typeIntoComposer,
} from '@/test/sendBinding1428'
import { requireFn } from './send-binding.module'

const LIB_REF = 'media://workspace/w1/lib-file-1'

interface Write { method: string; value: unknown }
const writes: Write[] = []
const implSends: Write[] = []
const cancelRuns: unknown[] = []
const composerCancels: unknown[] = []

function installSpies(): void {
  const proto = BaseComposerRuntimeCore.prototype as unknown as Record<string, (...a: unknown[]) => unknown>
  const impl = ComposerRuntimeImpl.prototype as unknown as Record<string, (...a: unknown[]) => unknown>
  const thread = ExternalStoreThreadRuntimeCore.prototype as unknown as Record<string, (...a: unknown[]) => unknown>
  for (const method of ['setText', 'send', 'cancel', 'addAttachment'] as const) {
    const original = proto[method]
    if (typeof original !== 'function' || (original as { __bound?: boolean }).__bound) continue
    const wrapped = function (this: unknown, ...args: unknown[]) {
      if (this instanceof DefaultThreadComposerRuntimeCore) writes.push({ method, value: args[0] })
      if (method === 'cancel') composerCancels.push(args[0])
      return original.apply(this, args)
    }
    ;(wrapped as { __bound?: boolean }).__bound = true
    proto[method] = wrapped
  }
  const implSend = impl.send
  if (typeof implSend === 'function' && !(implSend as { __bound?: boolean }).__bound) {
    const wrapped = function (this: unknown, ...args: unknown[]) {
      implSends.push({ method: 'send', value: args[0] })
      return implSend.apply(this, args)
    }
    ;(wrapped as { __bound?: boolean }).__bound = true
    impl.send = wrapped
  }
  const cancelRun = thread.cancelRun
  if (typeof cancelRun === 'function' && !(cancelRun as { __bound?: boolean }).__bound) {
    const wrapped = function (this: unknown, ...args: unknown[]) {
      cancelRuns.push(args[0])
      return cancelRun.apply(this, args)
    }
    ;(wrapped as { __bound?: boolean }).__bound = true
    thread.cancelRun = wrapped
  }
}

installSpies()

afterEach(() => {
  writes.length = 0
  implSends.length = 0
  cancelRuns.length = 0
  composerCancels.length = 0
  clearLibraryAttachments()
  act(() => { useUiStore.setState({ toasts: [] }) })
})

/** Spec B-12 does not quote the library-only notice. A notice is a toast or a
 *  status region other than the always-mounted composer chrome. */
function libraryNoticeText(): string {
  const toast = useUiStore.getState().toasts.map((t) => t.message).filter(Boolean).join('\n')
  const live = screen.getAllByRole('status')
    .filter((el) => {
      const id = el.getAttribute('data-testid')
      return id !== 'session-token-counter' && id !== 'slash-menu-status'
    })
    .map((el) => (el.textContent ?? '').trim())
    .filter(Boolean)
    .join('\n')
  return [toast, live].filter(Boolean).join('\n')
}

function switchTo(id: string): Write[] {
  const start = writes.length
  act(() => { useSessionStore.getState().setActiveSession(id, 'mia', 'core') })
  return writes.slice(start).filter((w) => w.method === 'setText')
}

function dropZone(): HTMLElement {
  const zone = screen.getByTestId('composer-card').parentElement
  if (!zone) throw new Error('composer drop zone missing')
  return zone
}

function dropFile(file: File): void {
  // jsdom has no DataTransfer. testing-library copies a plain dataTransfer onto the event.
  fireEvent.drop(dropZone(), { dataTransfer: { files: [file] } })
}

async function openDraftText(): Promise<string | undefined> {
  const getDraft = await requireFn('../store/chat/drafts', 'getDraft', 'spec 4.4.3 / B-1')
  const canonical = await requireFn('../store/chat/drafts', 'canonical', 'spec 4.4.1')
  const openKey = useSessionStore.getState().activeSessionId
  const key = canonical(openKey) as string
  const draft = getDraft(key) as { text?: string } | undefined
  return draft?.text
}

describe('session composer binding (spec 4.5)', () => {
  it('BR-1 per-chat box: W2 shows none of A, and A is intact on return, with no frame', async () => {
    // Spec BR-1 / B-1. "hello A" + a file in W1 must not appear in a W2 chat,
    // including during opening, and must still be there when A is open again.
    resetBindingWorld()
    mountComposer()
    await typeIntoComposer('hello A')
    act(() => {
      useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core')
    })
    expect(input().value, 'BR-1: W2 chat must not show A\'s draft').not.toBe('hello A')
    expect(input().value, 'BR-1: W2 box is A\'s text or empty of it').toBe('')
    act(() => {
      useSessionStore.getState().setActiveSession('chat-a', 'mia', 'core')
    })
    expect(input().value, 'BR-1: returning to A restores "hello A"').toBe('hello A')
    expect(framesOf('message', sent()), 'BR-1: switching chats sends no message').toEqual([])
    expect(await openDraftText(), 'BR-1: the store, not the runtime, holds A').toBe('hello A')
  })

  it('BR-2 helper switch leaves the helper box without "for A" and sends nothing', async () => {
    resetBindingWorld()
    mountComposer()
    await typeIntoComposer('for A')
    act(() => {
      useSessionStore.getState().setActiveSession('helper-1', 'mia', 'core')
    })
    expect(input().value, 'BR-2: helper box has none of "for A"').toBe('')
    expect(framesOf('message', sent()), 'BR-2: nothing sent').toEqual([])
    act(() => {
      useSessionStore.getState().setActiveSession('chat-a', 'mia', 'core')
    })
    expect(input().value, 'BR-2: A is intact').toBe('for A')
  })

  it('BR-10 library-only Enter shows a notice, sends no frame, and keeps the file staged', async () => {
    // Spec B-12 / BR-10. The notice wording is "today's notice"; the spec does
    // not quote it (recorded in RED-FE-GAPS). Quoted outcomes are asserted.
    resetBindingWorld()
    mountComposer()
    act(() => {
      addLibraryAttachment({
        mediaId: 'lib-file-1',
        ref: LIB_REF,
        filename: 'brief.pdf',
        contentType: 'application/pdf',
      })
    })
    const user = userEvent.setup()
    await user.click(input())
    await user.keyboard('{Enter}')
    expect(framesOf('message', sent()), 'BR-10 idle: no frame').toEqual([])
    expect(screen.getByTestId('library-chip-lib-file-1'), 'BR-10: file stays staged').toBeTruthy()
    expect(libraryNoticeText(), 'BR-10 idle: a notice is shown (spec B-12 does not quote the words)').not.toBe('')

    act(() => {
      useChatStore.setState({ isStreaming: true })
      useUiStore.setState({ toasts: [] })
    })
    await user.keyboard('{Enter}')
    expect(framesOf('message', sent()), 'BR-10 streaming: no frame').toEqual([])
    expect(screen.getByTestId('library-chip-lib-file-1'), 'BR-10 streaming: file stays staged').toBeTruthy()
    expect(libraryNoticeText(), 'BR-10 streaming: the notice is shown again').not.toBe('')

    await user.type(input(), 'with the file')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    const messages = framesOf('message', sent())
    expect(messages, 'BR-10: the later Send carries the library file').toEqual([
      expect.objectContaining({
        type: 'message',
        content: 'with the file',
        media: [LIB_REF],
        origin: { kind: 'chat', session_id: 'chat-a' },
      }),
    ])
  })

  it('BR-17 rapid switches keep each draft, mirror only the open draft, and never call composer.send', async () => {
    // Spec 4.5 rules 1, 5, 6 and BR-17. One key-transition write per key change.
    resetBindingWorld()
    mountComposer()
    await typeIntoComposer('alpha')
    const toB = switchTo('chat-b')
    expect(toB, 'BR-17 / A2: one key-transition setText, value is the open draft').toEqual([
      { method: 'setText', value: '' },
    ])
    await typeIntoComposer('beta')
    const toC = switchTo('chat-c')
    expect(toC, 'BR-17 / A2: one key-transition setText per key change').toEqual([
      { method: 'setText', value: '' },
    ])
    await typeIntoComposer('gamma')
    const sameKey = switchTo('chat-c')
    expect(sameKey, 'BR-17 / A2: no setText when the key does not change').toEqual([])
    expect(input().value, 'BR-17: DOM is the open draft').toBe('gamma')
    expect(composerProbe.text, 'BR-17: runtime text equals DOM').toBe(input().value)

    act(() => { useSessionStore.getState().setActiveSession('chat-a', 'mia', 'core') })
    expect(input().value, 'BR-17: A kept alpha').toBe('alpha')
    expect(composerProbe.text, 'BR-17: runtime follows the open draft').toBe('alpha')
    expect(implSends, 'BR-17 / rule 5: composer.send is never called').toEqual([])

    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    expect(implSends, 'BR-17: Send goes through submitDraft, not composer.send').toEqual([])
    expect(framesOf('message', sent()), 'BR-17: the sent frame is A\'s text with origin A').toEqual([
      expect.objectContaining({
        type: 'message',
        content: 'alpha',
        origin: { kind: 'chat', session_id: 'chat-a' },
      }),
    ])
    expect(input().value, 'BR-17: submit-clear mirrors the emptied open draft').toBe('')
    expect(composerProbe.text, 'BR-17: runtime cleared to the store').toBe('')
  })

  it('BR-17c Escape and Stop never call cancelRun, and Stop goes through cancelStream once', async () => {
    // Spec 4.5 rule 7 / B-28 / BR-17c. Today cancelOnEscape is false and nothing
    // calls cancelRun (r9). If this passes, it is a pass-by-design guard (MU-51).
    resetBindingWorld()
    const userMsg = {
      id: 'user-1', role: 'user' as const, content: 'seeded user message',
      timestamp: new Date().toISOString(), status: 'done' as const,
    }
    act(() => {
      useChatStore.setState({
        isStreaming: true,
        messages: [userMsg],
      })
    })
    mountComposer()
    expect(input().value, 'BR-17c setup: the box starts empty').toBe('')
    const user = userEvent.setup()
    await user.click(input())
    await user.keyboard('{Escape}')
    expect(cancelRuns, 'BR-17c: Escape does not call thread cancelRun').toEqual([])
    expect(composerCancels, 'BR-17c: Escape does not call composer cancel').toEqual([])
    expect(input().value, 'BR-17c: Escape does not write the user message back').toBe('')
    const afterEscape = framesOf('cancel', sent()).length
    await user.click(screen.getByTestId('stop-btn'))
    expect(cancelRuns, 'BR-17c: Stop does not call thread cancelRun').toEqual([])
    expect(composerCancels, 'BR-17c: Stop does not call composer cancel').toEqual([])
    expect(input().value, 'BR-17c: the user message is not written back').toBe('')
    expect(composerProbe.text, 'BR-17c: runtime text stays empty').toBe('')
    const cancels = framesOf('cancel', sent())
    expect(cancels.length - afterEscape, 'BR-17c: Stop goes through cancelStream exactly once').toBe(1)
    expect(cancels[cancels.length - 1], 'BR-17c: that cancel is the session stop, with origin').toEqual(
      expect.objectContaining({
        type: 'cancel',
        session_id: 'chat-a',
        origin: { kind: 'chat', session_id: 'chat-a' },
      }),
    )
  })

  it('BR-17 drop keeps the file on the chat that received it', async () => {
    resetBindingWorld()
    mountComposer()
    dropFile(new File(['a'], 'a-dropped.png', { type: 'image/png' }))
    switchTo('chat-b')
    expect(screen.queryByText('a-dropped.png'), 'BR-17: B does not show A\'s dropped file').toBeNull()
    switchTo('chat-a')
    expect(screen.getByText('a-dropped.png'), 'BR-17: A still shows its dropped file').toBeTruthy()
    expect(framesOf('message', sent()), 'BR-17: dropping a file sends nothing').toEqual([])
  })

  it('BR-17 paste of a non-image file is staged for the chat that received it', async () => {
    // Spec 4.5 rule 2 / A3: every pasted file, not only images.
    resetBindingWorld()
    mountComposer()
    const file = new File(['notes'], 'notes.pdf', { type: 'application/pdf' })
    fireEvent.paste(input(), {
      clipboardData: { items: [{ type: 'application/pdf', getAsFile: () => file }] },
    })
    expect(screen.queryByText('notes.pdf'), 'BR-17: the pasted file is staged').not.toBeNull()
    switchTo('chat-b')
    expect(screen.queryByText('notes.pdf'), 'BR-17: B does not show A\'s paste').toBeNull()
  })

  it('BR-17 harmful-file confirmation writes the file to the chat that received the drop', async () => {
    // Spec 4.5 rule 2: the key captured at the drop survives a switch before confirm.
    resetBindingWorld()
    mountComposer()
    dropFile(new File(['x'], 'malware.exe', { type: 'application/octet-stream' }))
    expect(screen.getByRole('alertdialog', { name: 'Potentially harmful file(s)' })).toBeTruthy()
    switchTo('chat-b')
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Continue' }))
    await user.click(screen.getByRole('button', { name: 'Upload' }))
    expect(screen.queryByText('malware.exe'), 'BR-17: confirming in B does not attach the file to B').toBeNull()
    switchTo('chat-a')
    expect(screen.getByText('malware.exe'), 'BR-17: the file lands in A, the chat that received the drop').toBeTruthy()
  })

  it('BR-17 picker captures the key at the button click, not at the file change', async () => {
    resetBindingWorld()
    mountComposer()
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Add files or context' }))
    switchTo('chat-b')
    const picker = document.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(picker, 'BR-17: the attach control exposes a file input').not.toBeNull()
    const file = new File(['p'], 'a-picked.txt', { type: 'text/plain' })
    await user.upload(picker!, file)
    expect(screen.queryByText('a-picked.txt'), 'BR-17: B does not receive a file picked for A').toBeNull()
    switchTo('chat-a')
    expect(screen.getByText('a-picked.txt'), 'BR-17: A keeps the file picked while A was open').toBeTruthy()
  })
})
