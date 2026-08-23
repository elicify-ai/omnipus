// browserAnnotate.test.ts — unit tests for the annotate-a-region-and-discuss
// submit orchestration (ADR-039 D-B1/B2/B3): upload → best-effort inspect →
// sendMessage. api.ts and the stores are mocked so this exercises ONLY the
// sequencing/fallback logic, not real network/canvas — jsdom cannot
// rasterise canvas (see media-actions.test.ts), so the cropped File is
// supplied directly rather than produced via a real crop.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { submitAnnotation, AnnotationBusyError } from './browserAnnotate'
import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import { useWorkspacesStore } from '@/store/workspacesStore'

// D18: mock only the network calls (uploadFiles, inspectBrowserElement,
// fetchModelCapabilities); keep the real modelLacksImageCapability (pure
// decision helper) via importActual so the D18 tests below exercise the
// actual integration, not a re-implemented mock of the decision logic.
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api')
  return {
    ...actual,
    uploadFiles: vi.fn(),
    inspectBrowserElement: vi.fn(),
  }
})

import { uploadFiles, inspectBrowserElement } from '@/lib/api'

const mockUploadFiles = vi.mocked(uploadFiles)
const mockInspectBrowserElement = vi.mocked(inspectBrowserElement)

function makeFile(): File {
  return new File([new Uint8Array([1, 2, 3])], 'annotation.png', { type: 'image/png' })
}

// Reviewer finding (D-B1/B2 gate #7): submitAnnotation targets the PINNED
// sessionId/agentId params (the panel's own props), not whatever chat is
// active in useSessionStore — so every case below pins 'sess-1'/'agent-1'
// (matching the active-session store state) unless a test explicitly
// exercises the mismatch/busy guards.
describe('submitAnnotation', () => {
  const sendMessageSpy = vi.fn()

  beforeEach(() => {
    vi.clearAllMocks()
    useSessionStore.setState({ activeSessionId: 'sess-1', activeAgentId: 'agent-1' })
    useChatStore.setState({ sendMessage: sendMessageSpy, isStreaming: false })
    useWorkspacesStore.setState({ activeWorkspaceId: null })
    mockUploadFiles.mockResolvedValue({
      files: [
        { name: 'annotation_abc.png', path: 'uploads/sess-1/annotation_abc.png', size: 3, content_type: 'image/png', ref: 'media://ref-1' },
      ],
    })
    mockInspectBrowserElement.mockResolvedValue({ ok: false })
  })

  it('throws when sessionId is empty', async () => {
    await expect(
      submitAnnotation({ comment: 'hi', file: makeFile(), point: { x: 1, y: 1 }, sessionId: '', agentId: 'agent-1' }),
    ).rejects.toThrow(/no active chat session/i)
    expect(mockUploadFiles).not.toHaveBeenCalled()
  })

  it('throws when agentId is empty', async () => {
    await expect(
      submitAnnotation({ comment: 'hi', file: makeFile(), point: { x: 1, y: 1 }, sessionId: 'sess-1', agentId: '' }),
    ).rejects.toThrow(/no active chat session/i)
  })

  it('uploads to the pinned session, calls inspect at the given point, and sends with the media ref', async () => {
    mockInspectBrowserElement.mockResolvedValue({ ok: true, tag: 'button', text: 'Submit' })

    await submitAnnotation({ comment: 'What does this do?', file: makeFile(), point: { x: 42, y: 84 }, sessionId: 'sess-1', agentId: 'agent-1' })

    expect(mockUploadFiles).toHaveBeenCalledWith('sess-1', [expect.any(File)], undefined)
    expect(mockInspectBrowserElement).toHaveBeenCalledWith({
      session_id: 'sess-1',
      agent_id: 'agent-1',
      x: 42,
      y: 84,
    })
    expect(sendMessageSpy).toHaveBeenCalledTimes(1)
    const [comment, opts] = sendMessageSpy.mock.calls[0]
    expect(comment).toBe(
      'What does this do?\n\n[This is a cropped screenshot region from the live browser — it may be mostly blank. Auto-detected context for this region (<button>): "Submit". An image was sent with this message. If you can see it, treat it as the source of truth and describe exactly what you see rather than saying no image was attached. If instead this message contains a note that the attachment is unavailable, trust that note instead.]',
    )
    expect(opts.mediaRefs).toEqual(['media://ref-1'])
    expect(opts.attachments).toEqual([
      { type: 'image', url: '/api/v1/media/ref-1', filename: 'annotation.png', contentType: 'image/png' },
    ])
  })

  // D16 regression (RCA-verified): when the panel is opened from a
  // workspace-scoped chat, uploadFiles() routes the file into the workspace
  // media library and returns a `media://workspace/<ws>/<id>` ref — NOT a
  // legacy session-scoped file. The attachment `url` must be built from that
  // ref (mediaRefURL) so it resolves via GET /api/v1/media/workspace/<ws>/<id>
  // (HandleMediaByRef). The old hardcoded
  // `/api/v1/uploads/${sessionId}/${uploaded.name}` 404s in this case —
  // HandleServeUpload only ever looks in uploads/<session>/<file>, which was
  // never written for a workspace-routed upload — producing the reported
  // "Image unavailable" card.
  it('D16: builds the workspace-scoped preview URL from the ref when the panel is opened from a workspace chat', async () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    mockUploadFiles.mockResolvedValue({
      files: [
        { name: 'annotation_abc.png', path: 'workspaces/ws-1/media/med-1', size: 3, content_type: 'image/png', ref: 'media://workspace/ws-1/med-1' },
      ],
    })

    await submitAnnotation({ comment: 'What is this?', file: makeFile(), point: { x: 1, y: 1 }, sessionId: 'sess-1', agentId: 'agent-1' })

    expect(mockUploadFiles).toHaveBeenCalledWith('sess-1', [expect.any(File)], 'ws-1')
    expect(sendMessageSpy).toHaveBeenCalledTimes(1)
    const [, opts] = sendMessageSpy.mock.calls[0]
    expect(opts.attachments[0].url).toBe('/api/v1/media/workspace/ws-1/med-1')
  })

  // UAT finding (Tester 2, blank-region false negative): every send now
  // carries the blank-region framing note regardless of inspect outcome —
  // this test used to assert the comment went out byte-for-byte unmodified;
  // it now asserts the base framing note is present with NO auto-detected
  // context clause (inspect resolved ok but found no text).
  it('appends only the base blank-region framing note (no auto-detected context) when inspect resolves ok but with no text', async () => {
    mockInspectBrowserElement.mockResolvedValue({ ok: true, tag: 'div' })

    await submitAnnotation({ comment: 'Look at this', file: makeFile(), point: { x: 1, y: 2 }, sessionId: 'sess-1', agentId: 'agent-1' })

    expect(sendMessageSpy).toHaveBeenCalledTimes(1)
    expect(sendMessageSpy.mock.calls[0][0]).toBe(
      'Look at this\n\n[This is a cropped screenshot region from the live browser — it may be mostly blank. An image was sent with this message. If you can see it, treat it as the source of truth and describe exactly what you see rather than saying no image was attached. If instead this message contains a note that the attachment is unavailable, trust that note instead.]',
    )
  })

  it('appends only the base blank-region framing note (no auto-detected context) when inspect resolves ok:false (best-effort, D-B3)', async () => {
    mockInspectBrowserElement.mockResolvedValue({ ok: false, reason: 'cross-origin frame' })

    await submitAnnotation({ comment: 'Look at this', file: makeFile(), point: { x: 1, y: 2 }, sessionId: 'sess-1', agentId: 'agent-1' })

    expect(sendMessageSpy).toHaveBeenCalledTimes(1)
    expect(sendMessageSpy.mock.calls[0][0]).toBe(
      'Look at this\n\n[This is a cropped screenshot region from the live browser — it may be mostly blank. An image was sent with this message. If you can see it, treat it as the source of truth and describe exactly what you see rather than saying no image was attached. If instead this message contains a note that the attachment is unavailable, trust that note instead.]',
    )
  })

  it('sends the image + comment (with the base blank-region framing note) even when the inspect REQUEST itself rejects (network error)', async () => {
    mockInspectBrowserElement.mockRejectedValue(new Error('network down'))

    await submitAnnotation({ comment: 'Still works', file: makeFile(), point: { x: 1, y: 2 }, sessionId: 'sess-1', agentId: 'agent-1' })

    expect(sendMessageSpy).toHaveBeenCalledTimes(1)
    expect(sendMessageSpy.mock.calls[0][0]).toBe(
      'Still works\n\n[This is a cropped screenshot region from the live browser — it may be mostly blank. An image was sent with this message. If you can see it, treat it as the source of truth and describe exactly what you see rather than saying no image was attached. If instead this message contains a note that the attachment is unavailable, trust that note instead.]',
    )
    expect(sendMessageSpy.mock.calls[0][1].mediaRefs).toEqual(['media://ref-1'])
  })

  it('throws and never calls sendMessage when the upload fails', async () => {
    mockUploadFiles.mockRejectedValue(new Error('upload failed'))

    await expect(
      submitAnnotation({ comment: 'hi', file: makeFile(), point: { x: 1, y: 1 }, sessionId: 'sess-1', agentId: 'agent-1' }),
    ).rejects.toThrow('upload failed')
    expect(sendMessageSpy).not.toHaveBeenCalled()
  })

  it('throws and never calls sendMessage when the upload response has no media ref', async () => {
    mockUploadFiles.mockResolvedValue({
      files: [{ name: 'annotation_abc.png', path: 'uploads/sess-1/annotation_abc.png', size: 3, content_type: 'image/png' }],
    })

    await expect(
      submitAnnotation({ comment: 'hi', file: makeFile(), point: { x: 1, y: 1 }, sessionId: 'sess-1', agentId: 'agent-1' }),
    ).rejects.toThrow(/no media reference/i)
    expect(sendMessageSpy).not.toHaveBeenCalled()
  })

  // Reviewer finding #6 (CRITICAL silent-loss): a streaming turn must never
  // silently swallow the annotation — submitAnnotation must refuse BEFORE
  // uploading, not upload+send into sendMessage's own silent isStreaming no-op.
  it('throws AnnotationBusyError and never uploads when the target session is already streaming', async () => {
    useChatStore.setState({ isStreaming: true })

    await expect(
      submitAnnotation({ comment: 'hi', file: makeFile(), point: { x: 1, y: 1 }, sessionId: 'sess-1', agentId: 'agent-1' }),
    ).rejects.toBeInstanceOf(AnnotationBusyError)
    expect(mockUploadFiles).not.toHaveBeenCalled()
    expect(sendMessageSpy).not.toHaveBeenCalled()
  })

  // Reviewer finding #7 (MEDIUM): the annotation must target the panel's
  // PINNED pair, and must refuse — not silently misdirect — when the
  // active chat has since diverged from it (sendMessage has no session
  // override; it always posts to whatever is CURRENTLY active).
  it('throws and never sends when the active session has diverged from the pinned session by send time', async () => {
    useSessionStore.setState({ activeSessionId: 'sess-OTHER', activeAgentId: 'agent-1' })

    await expect(
      submitAnnotation({ comment: 'hi', file: makeFile(), point: { x: 1, y: 1 }, sessionId: 'sess-1', agentId: 'agent-1' }),
    ).rejects.toThrow(/active chat has changed/i)
    // The upload still happens (it's addressed to the pinned session, which
    // is valid on its own) — only the final send is refused.
    expect(mockUploadFiles).toHaveBeenCalledWith('sess-1', [expect.any(File)], undefined)
    expect(sendMessageSpy).not.toHaveBeenCalled()
  })

  it('throws and never sends when the active agent has diverged from the pinned agent by send time', async () => {
    useSessionStore.setState({ activeSessionId: 'sess-1', activeAgentId: 'agent-OTHER' })

    await expect(
      submitAnnotation({ comment: 'hi', file: makeFile(), point: { x: 1, y: 1 }, sessionId: 'sess-1', agentId: 'agent-1' }),
    ).rejects.toThrow(/active chat has changed/i)
    expect(sendMessageSpy).not.toHaveBeenCalled()
  })

  // F6 coverage gap: the inspect-text truncation (280 chars + "…") had no
  // test — a large inspected element (e.g. a whole <article> or <body>) must
  // not dump a wall of text into the annotation comment.
  it('truncates an inspect-text longer than 280 characters to 280 chars + an ellipsis', async () => {
    const longText = 'x'.repeat(300)
    mockInspectBrowserElement.mockResolvedValue({ ok: true, tag: 'article', text: longText })

    await submitAnnotation({ comment: 'What is this?', file: makeFile(), point: { x: 1, y: 2 }, sessionId: 'sess-1', agentId: 'agent-1' })

    expect(sendMessageSpy).toHaveBeenCalledTimes(1)
    const [comment] = sendMessageSpy.mock.calls[0]
    const snippet = 'x'.repeat(280) + '…'
    expect(comment).toBe(
      `What is this?\n\n[This is a cropped screenshot region from the live browser — it may be mostly blank. Auto-detected context for this region (<article>): "${snippet}". An image was sent with this message. If you can see it, treat it as the source of truth and describe exactly what you see rather than saying no image was attached. If instead this message contains a note that the attachment is unavailable, trust that note instead.]`,
    )
    // The full 300-char text must NOT appear verbatim — only the capped 280 + ellipsis.
    expect(comment).not.toContain(longText)
  })

  it('does not truncate or append an ellipsis to inspect-text at or under 280 characters', async () => {
    const exactText = 'y'.repeat(280)
    mockInspectBrowserElement.mockResolvedValue({ ok: true, tag: 'p', text: exactText })

    await submitAnnotation({ comment: 'Look', file: makeFile(), point: { x: 1, y: 2 }, sessionId: 'sess-1', agentId: 'agent-1' })

    const [comment] = sendMessageSpy.mock.calls[0]
    expect(comment).toContain(`"${exactText}"`)
    expect(comment).not.toContain('…')
  })
})
