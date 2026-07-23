import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'

vi.mock('@/lib/api', () => ({
  uploadFiles: vi.fn(),
  createSession: vi.fn(),
}))

vi.mock('@/components/chat/AttachmentCard', () => ({
  isImageAttachment: vi.fn((filename: string, contentType?: string) =>
    (contentType ?? '').startsWith('image/') || /\.(png|jpe?g|gif|webp|svg|bmp|avif)$/i.test(filename)
  ),
}))

import * as api from '@/lib/api'
import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import { useUiStore } from '@/store/ui'
import { useOmnipusRuntime } from './omnipus-runtime'

const DOCX = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'

/**
 * Integration test for the AssistantUI AttachmentAdapter wiring: drives the REAL
 * useExternalStoreRuntime composer (addAttachment → send) and asserts the file
 * is uploaded and threaded into our transport (sendMessage) as a media ref.
 * This is the deterministic equivalent of "click Send with a file attached".
 */
describe('useOmnipusRuntime — native attachment flow', () => {
  let sendSpy: ReturnType<typeof vi.fn>

  beforeEach(() => {
    vi.clearAllMocks()
    useSessionStore.setState({ activeSessionId: 'sess1', activeAgentId: 'jim', activeAgentType: null })
    sendSpy = vi.fn()
    useChatStore.setState({
      messages: [],
      toolCalls: {},
      toolCallOrder: [],
      textAtToolCallStart: {},
      isStreaming: false,
      sendMessage: sendSpy as never,
      cancelStream: vi.fn(),
    })
    useUiStore.setState({ addToast: vi.fn() })
  })

  it('send() with an attached file uploads it and calls sendMessage with the media ref', async () => {
    vi.mocked(api.uploadFiles).mockResolvedValue({
      files: [{ name: 'report.docx', path: 'uploads/sess1/report.docx', size: 10, content_type: DOCX, ref: 'media://abc' }],
    } as never)

    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer

    await act(async () => {
      await composer.addAttachment(new File(['x'], 'report.docx', { type: DOCX }))
    })

    await act(async () => {
      composer.setText('read the attached file')
      await composer.send()
    })

    // The adapter must have uploaded the file...
    expect(api.uploadFiles).toHaveBeenCalledWith('sess1', expect.arrayContaining([expect.any(File)]), undefined)
    // ...and onNew must have threaded the media ref into our transport.
    expect(sendSpy).toHaveBeenCalledTimes(1)
    const [text, opts] = sendSpy.mock.calls[0]
    expect(text).toContain('read the attached file')
    expect(opts?.mediaRefs).toEqual(['media://abc'])
    expect(opts?.attachments?.[0]?.filename).toBe('report.docx')
  })

  it('attachment-only send (no text) still goes through with the media ref', async () => {
    vi.mocked(api.uploadFiles).mockResolvedValue({
      files: [{ name: 'pic.png', path: 'uploads/sess1/pic.png', size: 4, content_type: 'image/png', ref: 'media://img' }],
    } as never)

    const { result } = renderHook(() => useOmnipusRuntime())
    const composer = result.current.thread.composer

    await act(async () => {
      await composer.addAttachment(new File(['x'], 'pic.png', { type: 'image/png' }))
    })
    await act(async () => {
      await composer.send()
    })

    expect(api.uploadFiles).toHaveBeenCalled()
    expect(sendSpy).toHaveBeenCalledTimes(1)
    expect(sendSpy.mock.calls[0][1]?.mediaRefs).toEqual(['media://img'])
  })

  it('upload with no media ref: text still sends, file is dropped, and onNew warns the user', async () => {
    // Upload "succeeds" (has a path) but the media-store registration produced
    // no ref → adapter returns a failedComplete (no stashed ref). onNew must
    // still send the typed text, omit the unresolved file from mediaRefs, and
    // surface a toast so the dropped attachment is not a silent failure.
    vi.mocked(api.uploadFiles).mockResolvedValue({
      files: [{ name: 'broken.docx', path: 'uploads/sess1/broken.docx', size: 9, content_type: DOCX }], // no ref
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

    // Text still sent (no data loss), with NO media ref for the dropped file.
    expect(sendSpy).toHaveBeenCalledTimes(1)
    const [text, opts] = sendSpy.mock.calls[0]
    expect(text).toContain('here is a file')
    expect(opts?.mediaRefs ?? []).toEqual([])
    // User was warned (onNew's "was not sent" toast naming the file).
    expect(addToast).toHaveBeenCalledWith(
      expect.objectContaining({ message: expect.stringContaining('was not sent'), variant: 'error' }),
    )
  })
})
