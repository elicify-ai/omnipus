import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import type { WsConnection } from '@/lib/ws'
import * as api from '@/lib/api'
import { submitDraft } from '@/lib/bound-send'
import { addDraftFiles, getDraft, setDraftText } from '@/store/chat/drafts'
import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useUiStore } from '@/store/ui'
import { useOmnipusRuntime } from './omnipus-runtime'
import {
  addLibraryAttachment,
  buildWorkspaceMediaRef,
  clearLibraryAttachments,
  getLibraryAttachments,
  mediaRefURL,
} from './library-attachment'

const DOCX = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'
const CHAT = 'sess1'
const sent: unknown[] = []

vi.mock('@/lib/api', () => ({
  uploadFiles: vi.fn(),
  createSession: vi.fn(),
}))

vi.mock('@/components/chat/AttachmentCard', () => ({
  isImageAttachment: vi.fn((filename: string, contentType?: string) =>
    (contentType ?? '').startsWith('image/') || /\.(png|jpe?g|gif|webp|svg|bmp|avif)$/i.test(filename)
  ),
}))

type OutFrame = { type?: string; content?: string; media?: string[] }

function messageFrames(): OutFrame[] {
  return sent.filter((frame) => (frame as OutFrame).type === 'message') as OutFrame[]
}

function userRow(content: string) {
  const bucket = useChatStore.getState().sessionsById[CHAT]
  const messages = bucket ? bucket.messageOrder.map((id) => bucket.messagesById[id]) : []
  return messages.find((message) => message?.role === 'user' && message.content === content)
}

/**
 * Spec 4.5 rule 5 / 4.4.3: the draft store owns files and library picks.
 * submitDraft is the submit that puts them on the bound message frame
 * (`media` refs) and on the user row (the same preview URLs the old
 * sendMessage opts.attachments carried). A runtime append's submitText
 * does not carry files; these oracles moved with the draft.
 */
describe('useOmnipusRuntime — draft-owned attachment flow (spec 4.5 rule 5)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    sent.length = 0
    useSessionStore.setState({
      ...useSessionStore.getInitialState(),
      activeSessionId: CHAT,
      activeAgentId: 'jim',
      activeAgentType: null,
    })
    clearLibraryAttachments()
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState({
      ...useConnectionStore.getInitialState(),
      connection: { send: (frame: unknown) => { sent.push(frame); return true } } as unknown as WsConnection,
      isConnected: true,
    })
    useUiStore.setState(useUiStore.getInitialState(), true)
  })

  it('uploads a draft file and the message frame carries its media ref', async () => {
    vi.mocked(api.uploadFiles).mockResolvedValue({
      files: [{ name: 'report.docx', path: 'uploads/sess1/report.docx', size: 10, content_type: DOCX, ref: 'media://abc' }],
    } as never)
    setDraftText(CHAT, 'read the attached file')
    addDraftFiles(CHAT, [new File(['x'], 'report.docx', { type: DOCX })])

    await act(async () => { submitDraft('Composer Send') })
    await waitFor(() => expect(messageFrames()).toHaveLength(1))

    expect(api.uploadFiles).toHaveBeenCalledWith(CHAT, expect.arrayContaining([expect.any(File)]), undefined)
    expect(messageFrames()[0]?.content).toContain('read the attached file')
    expect(messageFrames()[0]?.media).toEqual(['media://abc'])
    expect(userRow('read the attached file')?.media?.[0]).toEqual(expect.objectContaining({
      filename: 'report.docx',
      url: mediaRefURL('media://abc'),
    }))
  })

  it('sends an attachment-only draft and the frame carries the media ref', async () => {
    vi.mocked(api.uploadFiles).mockResolvedValue({
      files: [{ name: 'pic.png', path: 'uploads/sess1/pic.png', size: 4, content_type: 'image/png', ref: 'media://img' }],
    } as never)
    addDraftFiles(CHAT, [new File(['x'], 'pic.png', { type: 'image/png' })])

    await act(async () => { submitDraft('Composer Send') })
    await waitFor(() => expect(messageFrames()).toHaveLength(1))

    expect(api.uploadFiles).toHaveBeenCalled()
    expect(messageFrames()[0]?.media).toEqual(['media://img'])
    expect(userRow('')?.media?.[0]?.url).toBe(mediaRefURL('media://img'))
  })

  it('upload with no media ref: the text frame still leaves and the file is not attached', async () => {
    // The upload runs in the attachment adapter's send(), before the runtime
    // append. With a path and no media ref the adapter's own pre-existing
    // product text is the toast below (attachment-adapter.ts, the no-ref
    // branch). The file is not stashed and never enters the draft, so there
    // is no local file left for a send-time refusal. Spec 4.6.3's sentence
    // "Upload failed. Try again." is that send-time case, not this one.
    // Spec 4.5 rule 5: the append calls submitText and the draft owns files,
    // so the message frame carries the typed text and no media.
    const NOT_REGISTERED = '"broken.docx" could not be registered for the agent — re-attach and try again.'
    vi.mocked(api.uploadFiles).mockResolvedValue({
      files: [{ name: 'broken.docx', path: 'uploads/sess1/broken.docx', size: 9, content_type: DOCX }],
    } as never)
    const addToast = vi.fn()
    useUiStore.setState({ addToast })

    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer

    await act(async () => {
      await composer.addAttachment(new File(['x'], 'broken.docx', { type: DOCX }))
    })
    await act(async () => {
      composer.setText('here is a file')
      await composer.send()
    })

    expect(addToast).toHaveBeenCalledWith({ message: NOT_REGISTERED, variant: 'error' })
    expect(messageFrames(), 'spec 4.5 rule 5: the typed text still leaves as one message').toHaveLength(1)
    expect(messageFrames()[0]?.content).toBe('here is a file')
    expect(messageFrames()[0]?.media ?? [], 'spec 4.5 rule 5: the unregistered file is not media on the frame').toEqual([])
    expect(
      sent.some((frame) => JSON.stringify(frame).includes('broken.docx') || JSON.stringify(frame).includes('media://')),
      'the failed file is on no frame',
    ).toBe(false)
    expect(getDraft(CHAT).files, 'spec 4.5 rule 5: a file that never registered is not in the draft').toEqual([])
  })

  describe('library picks on the open draft (spec 4.4.3)', () => {
    it('sends a workspace-library pick with the workspace-scoped preview URL', () => {
      addLibraryAttachment({
        mediaId: 'm-1',
        ref: buildWorkspaceMediaRef('ws-1', 'm-1'),
        filename: 'diagram.png',
        contentType: 'image/png',
      })
      setDraftText(CHAT, 'see attached')

      act(() => { submitDraft('Composer Send') })

      expect(messageFrames()).toHaveLength(1)
      expect(messageFrames()[0]?.content).toContain('see attached')
      expect(messageFrames()[0]?.media).toEqual(['media://workspace/ws-1/m-1'])
      expect(userRow('see attached')?.media).toEqual([
        expect.objectContaining({
          type: 'image',
          url: '/api/v1/media/workspace/ws-1/m-1',
          filename: 'diagram.png',
          contentType: 'image/png',
        }),
      ])
      expect(getLibraryAttachments()).toHaveLength(0)
    })

    it('builds the legacy preview URL from the ref, not from mediaId', () => {
      addLibraryAttachment({
        mediaId: 'stale-manifest-id',
        ref: 'media://legacy-1',
        filename: 'notes.txt',
        contentType: 'text/plain',
      })
      setDraftText(CHAT, 'legacy file')

      act(() => { submitDraft('Composer Send') })

      expect(messageFrames()[0]?.media).toEqual(['media://legacy-1'])
      expect(userRow('legacy file')?.media?.[0]).toEqual(
        expect.objectContaining({ type: 'file', url: '/api/v1/media/legacy-1' }),
      )
    })

    it('threads a freshly uploaded file and a library pick on one frame', async () => {
      vi.mocked(api.uploadFiles).mockResolvedValue({
        files: [{ name: 'report.docx', path: 'uploads/sess1/report.docx', size: 10, content_type: DOCX, ref: 'media://abc' }],
      } as never)
      addLibraryAttachment({
        mediaId: 'm-2',
        ref: buildWorkspaceMediaRef('ws-1', 'm-2'),
        filename: 'diagram.png',
        contentType: 'image/png',
      })
      setDraftText(CHAT, 'two attachments')
      addDraftFiles(CHAT, [new File(['x'], 'report.docx', { type: DOCX })])

      await act(async () => { submitDraft('Composer Send') })
      await waitFor(() => expect(messageFrames()).toHaveLength(1))

      expect(messageFrames()[0]?.media).toEqual(['media://abc', 'media://workspace/ws-1/m-2'])
      expect(userRow('two attachments')?.media).toEqual([
        expect.objectContaining({ filename: 'report.docx', url: '/api/v1/media/abc' }),
        expect.objectContaining({ filename: 'diagram.png', url: '/api/v1/media/workspace/ws-1/m-2' }),
      ])
    })
  })
})
