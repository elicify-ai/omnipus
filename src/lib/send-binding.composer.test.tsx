// #1428 step 2 RED. Spec 4.4.3, 4.5 rules 1-7, B-1, B-2, B-3, B-12, B-19, B-28.
// Oracles: SPEC-C r9. Not the spike, not observed output.
import { afterEach, describe, expect, it } from 'vitest'
import {
  apiMocks,
  composerProbe,
  framesOf,
  input,
  mountComposer,
  resetBindingWorld,
  runtimeProbe,
  sent,
  typeIntoComposer,
} from '@/test/sendBinding1428'
import { act } from 'react'
import { fireEvent, screen, waitFor } from '@testing-library/react'
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
import { requireFn } from '@/test/send-binding.module'

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

/** Spec B-12 (r9): a library-only Enter shows no notice. The activity poll's
 *  own warning toast is not that notice (it fires because the test has no
 *  activity-runs route). A send notice, in the composer or as any other toast,
 *  still counts. */
const ACTIVITY_POLL_TOAST = "Couldn't refresh task and scheduler runs. The list may be out of date."

function libraryNoticeText(): string {
  const root = screen.getByTestId('composer-card').parentElement
  const toast = useUiStore.getState().toasts
    .map((t) => t.message)
    .filter((message) => message && message !== ACTIVITY_POLL_TOAST)
    .join('\n')
  const live = Array.from(root?.querySelectorAll('[role="status"]') ?? [])
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

/** The chip is committed by composerRuntime.addAttachment, which resolves
 *  on a microtask. A synchronous query misses a chip that is about to render. */
async function waitStaged(name: string, message: string): Promise<void> {
  await waitFor(() => {
    expect(stagedFile(name), message).not.toBeNull()
  })
}

/** A staged file is visible text, or (images) only the thumbnail alt / Remove name. */
function stagedFile(name: string): HTMLElement | null {
  return screen.queryByText(name)
    ?? screen.queryByAltText(name)
    ?? screen.queryByRole('button', { name: `Remove ${name}` })
}

async function openDraftText(): Promise<string | undefined> {
  // Specifiers are relative to src/test/send-binding.module.ts.
  const getDraft = await requireFn('../store/chat/drafts', 'getDraft', 'spec 4.4.3 / B-1')
  const canonical = await requireFn('../store/chat/drafts', 'canonical', 'spec 4.4.1')
  const openKey = useSessionStore.getState().activeSessionId
  const key = canonical(openKey) as string
  const draft = getDraft(key) as { text?: string } | undefined
  return draft?.text
}

describe('session composer binding (spec 4.5)', () => {
  registerComposerDraftCases()
  registerComposerFileCases()
})

function registerComposerDraftCases(): void {
  it('BR-1 per-chat box: W2 shows none of A, and A is intact on return, with no frame', async () => {
    // Spec BR-1 / B-1. "hello A" + a file in W1 must not appear in a W2 chat,
    // including during opening, and must still be there when A is open again.
    resetBindingWorld()
    mountComposer()
    await typeIntoComposer('hello A')
    dropFile(new File(['a'], 'hello-a.txt', { type: 'text/plain' }))
    await waitStaged('hello-a.txt', 'BR-1: the file is staged before the switch')
    act(() => {
      useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core')
    })
    expect(input().value, 'BR-1: W2 chat must not show A\'s draft').not.toBe('hello A')
    expect(input().value, 'BR-1: W2 box is A\'s text or empty of it').toBe('')
    expect(stagedFile('hello-a.txt'), 'BR-1: W2 must not show A\'s file').toBeNull()
    act(() => {
      useSessionStore.getState().setActiveSession('chat-a', 'mia', 'core')
    })
    expect(input().value, 'BR-1: returning to A restores "hello A"').toBe('hello A')
    expect(stagedFile('hello-a.txt'), 'BR-1: returning to A restores the file').not.toBeNull()
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

  it('BR-10 library-only Enter sends nothing and keeps the file, then a later Send carries it', async () => {
    // Spec B-12 / BR-10 (chief 2026-11-18, option a). Silent no-op: no frame,
    // no notice, file stays staged. There is no notice leg. MU-17 is
    // library_only_enter_sends.
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
    expect(screen.getByTestId('library-chip-lib-file-1'), 'BR-10 idle: file stays staged').toBeTruthy()
    expect(libraryNoticeText(), 'BR-10 idle: no notice').toBe('')
    expect(input().value, 'BR-10 idle: the box stays empty').toBe('')

    act(() => {
      useChatStore.setState({ isStreaming: true })
    })
    await user.keyboard('{Enter}')
    expect(framesOf('message', sent()), 'BR-10 streaming: no frame').toEqual([])
    expect(screen.getByTestId('library-chip-lib-file-1'), 'BR-10 streaming: file stays staged').toBeTruthy()
    expect(libraryNoticeText(), 'BR-10 streaming: no notice').toBe('')

    // The two Enters are done. Send is the idle control; Stop replaces it while streaming.
    act(() => { useChatStore.setState({ isStreaming: false }) })
    await user.type(input(), 'with the file')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    const messages = framesOf('message', sent())
    expect(messages, 'BR-10: typing text and Send carries the library file').toEqual([
      expect.objectContaining({
        type: 'message',
        content: 'with the file',
        media: [LIB_REF],
        origin: { kind: 'chat', session_id: 'chat-a' },
      }),
    ])
    expect(messages, 'BR-10: exactly one frame').toHaveLength(1)
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

}

function registerComposerFileCases(): void {
  it('BR-17 drop keeps the file on the chat that received it', async () => {
    // Images render as a thumbnail: the name is the alt and the Remove label,
    // not a text node. queryByText cannot see it even when the chip is there.
    resetBindingWorld()
    mountComposer()
    dropFile(new File(['a'], 'a-dropped.png', { type: 'image/png' }))
    await waitStaged('a-dropped.png', 'BR-17 drop: the file is staged on A before any switch')
    switchTo('chat-b')
    expect(stagedFile('a-dropped.png'), 'BR-17: B does not show A\'s dropped file').toBeNull()
    switchTo('chat-a')
    expect(stagedFile('a-dropped.png'), 'BR-17: A still shows its dropped file').not.toBeNull()
    expect(framesOf('message', sent()), 'BR-17: dropping a file sends nothing').toEqual([])
  })

  it('BR-17 paste of a non-image file is staged for the chat that received it', async () => {
    // Spec 4.5 rule 2 / A3: every pasted file, not only images. kind:'file' is
    // what a browser clipboard item carries; a missing kind must not hide it.
    resetBindingWorld()
    mountComposer()
    const file = new File(['notes'], 'notes.pdf', { type: 'application/pdf' })
    fireEvent.paste(input(), {
      clipboardData: {
        files: [file],
        items: [{ kind: 'file', type: 'application/pdf', getAsFile: () => file }],
      },
    })
    await waitStaged('notes.pdf', 'BR-17: the pasted file is staged')
    switchTo('chat-b')
    expect(stagedFile('notes.pdf'), 'BR-17: B does not show A\'s paste').toBeNull()
    switchTo('chat-a')
    expect(stagedFile('notes.pdf'), 'BR-17: A still shows the pasted file').not.toBeNull()
  })

  it('BR-17 harmful-file confirmation writes the file to the chat that received the drop', async () => {
    // Spec 4.5 rule 2 keeps size/type validation, so the file must be one
    // isAcceptedFile accepts. .exe is the product's harmful extension; a PDF
    // MIME is on the accept list, so the chip can appear. An octet-stream
    // .exe is rejected after confirm and never tests the captured key.
    resetBindingWorld()
    mountComposer()
    dropFile(new File(['%PDF-1.4'], 'payload.exe', { type: 'application/pdf' }))
    expect(screen.getByRole('alertdialog', { name: 'Potentially harmful file(s)' })).toBeTruthy()
    expect(screen.getByText(/payload\.exe/), 'BR-17: the dialog names the dropped file').toBeTruthy()
    switchTo('chat-b')
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Continue' }))
    await user.click(screen.getByRole('button', { name: 'Upload' }))
    await act(async () => { await Promise.resolve() })
    expect(stagedFile('payload.exe'), 'BR-17: confirming in B does not attach the file to B').toBeNull()
    switchTo('chat-a')
    expect(stagedFile('payload.exe'), 'BR-17: the file lands in A, the chat that received the drop').not.toBeNull()
  })

  it('BR-17 removing a chip deletes that chat\'s file only', async () => {
    resetBindingWorld()
    mountComposer()
    dropFile(new File(['a'], 'remove-me.txt', { type: 'text/plain' }))
    await waitStaged('remove-me.txt', 'BR-17 remove: the file is staged')
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Remove remove-me.txt' }))
    expect(stagedFile('remove-me.txt'), 'BR-17 remove: the chip is gone').toBeNull()
    switchTo('chat-b')
    dropFile(new File(['b'], 'keep-b.txt', { type: 'text/plain' }))
    await waitStaged('keep-b.txt', 'BR-17 remove: B staged its own file')
    switchTo('chat-a')
    expect(stagedFile('remove-me.txt'), 'BR-17 remove: A does not get the file back').toBeNull()
    expect(stagedFile('keep-b.txt'), 'BR-17 remove: B\'s file is not on A').toBeNull()
    switchTo('chat-b')
    expect(stagedFile('keep-b.txt'), 'BR-17 remove: B kept its own file').not.toBeNull()
  })

  it('BR-17 a file alone can be sent, and a form submit does not call composer.send', async () => {
    resetBindingWorld()
    mountComposer()
    dropFile(new File(['only'], 'only.txt', { type: 'text/plain' }))
    await waitStaged('only.txt', 'BR-17 file-only: the file is staged')
    const send = screen.getByRole('button', { name: 'Send message' })
    expect(send, 'BR-17 file-only: Send is eligible with a file and no text').toBeEnabled()
    apiMocks.uploadFiles.mockResolvedValue({ files: [{ ref: 'media://workspace/w1/only', path: 'only.txt', name: 'only.txt', content_type: 'text/plain' }] })
    const user = userEvent.setup()
    await user.click(send)
    expect(implSends, 'BR-17 file-only: composer.send is not called').toEqual([])
    expect(framesOf('message', sent()), 'BR-17 file-only: the frame carries the file and origin A').toEqual([
      expect.objectContaining({
        type: 'message',
        origin: { kind: 'chat', session_id: 'chat-a' },
      }),
    ])
    const fileFrame = framesOf('message', sent())[0] as { media?: string[]; content?: string }
    expect(fileFrame?.content ?? '', 'BR-17 file-only: no invented text').toBe('')
    expect(fileFrame?.media?.length ?? 0, 'BR-17 file-only: the file is on the frame').toBeGreaterThan(0)
  })

  it('BR-17 form submit and streaming Enter go through submitDraft', async () => {
    resetBindingWorld()
    mountComposer()
    await typeIntoComposer('from the form')
    const form = input().closest('form')
    expect(form, 'BR-17 form: the composer is a form').not.toBeNull()
    fireEvent.submit(form!)
    expect(implSends, 'BR-17 form: composer.send is not called').toEqual([])
    expect(framesOf('message', sent()), 'BR-17 form: one message with origin A').toEqual([
      expect.objectContaining({
        type: 'message',
        content: 'from the form',
        origin: { kind: 'chat', session_id: 'chat-a' },
      }),
    ])
    act(() => { useChatStore.setState({ isStreaming: true }) })
    await typeIntoComposer('while streaming')
    const user = userEvent.setup()
    await user.click(input())
    await user.keyboard('{Enter}')
    expect(implSends, 'BR-17 streaming Enter: composer.send is not called').toEqual([])
    expect(framesOf('message', sent()).at(-1), 'BR-17 streaming Enter: the open draft is submitted').toEqual(
      expect.objectContaining({
        type: 'message',
        content: 'while streaming',
        origin: { kind: 'chat', session_id: 'chat-a' },
      }),
    )
  })

  it('BR-17 a runtime append submits the open draft and a remount keeps it', async () => {
    resetBindingWorld()
    const view = mountComposer()
    await typeIntoComposer('kept across remount')
    dropFile(new File(['f'], 'kept.txt', { type: 'text/plain' }))
    await waitStaged('kept.txt', 'BR-17 remount: the file is staged before unmount')
    view.unmount()
    mountComposer()
    expect(input().value, 'BR-17 remount: DOM text is the open draft').toBe('kept across remount')
    expect(composerProbe.text, 'BR-17 remount: runtime text is the open draft').toBe('kept across remount')
    expect(stagedFile('kept.txt'), 'BR-17 remount: the file is still staged').not.toBeNull()
    const before = framesOf('message', sent()).length
    act(() => { runtimeProbe.append('appended by the runtime') })
    await act(async () => { await Promise.resolve() })
    expect(implSends, 'BR-17 runtime append: composer.send is not called').toEqual([])
    expect(framesOf('message', sent()).slice(before), 'BR-17 runtime append: one message for the open chat').toEqual([
      expect.objectContaining({
        type: 'message',
        content: 'appended by the runtime',
        origin: { kind: 'chat', session_id: 'chat-a' },
      }),
    ])
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
}

