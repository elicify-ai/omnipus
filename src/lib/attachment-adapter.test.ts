import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('@/lib/api', () => ({
  uploadFiles: vi.fn(),
  createSession: vi.fn(),
}))

import * as api from '@/lib/api'
import { useSessionStore } from '@/store/session'
import { omnipusAttachmentAdapter, takeResolvedUpload } from './attachment-adapter'

const DOCX = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'

function mkFile(name: string, type: string): File {
  return new File(['data'], name, { type })
}

beforeEach(() => {
  vi.clearAllMocks()
  useSessionStore.setState({ activeSessionId: 'sess_existing', activeAgentId: 'jim', activeAgentType: null })
})

describe('omnipusAttachmentAdapter', () => {
  it('add() defers the upload and marks the attachment requires-action', async () => {
    const file = mkFile('report.docx', DOCX)
    const pending = await omnipusAttachmentAdapter.add({ file })

    expect(api.uploadFiles).not.toHaveBeenCalled() // upload deferred to send()
    expect(pending.status).toEqual({ type: 'requires-action', reason: 'composer-send' })
    expect(pending.file).toBe(file)
    expect(pending.name).toBe('report.docx')
    expect(pending.type).toBe('file')
  })

  it('add() classifies images as type "image"', async () => {
    const pending = await omnipusAttachmentAdapter.add({ file: mkFile('photo.png', 'image/png') })
    expect(pending.type).toBe('image')
  })

  it('send() uploads, stashes the media ref, and takeResolvedUpload drains it once', async () => {
    vi.mocked(api.uploadFiles).mockResolvedValue({
      files: [{ name: 'report.docx', path: 'uploads/sess_existing/report.docx', size: 10, content_type: DOCX, ref: 'media://abc' }],
    } as never)

    const pending = await omnipusAttachmentAdapter.add({ file: mkFile('report.docx', DOCX) })
    const complete = await omnipusAttachmentAdapter.send(pending)

    expect(api.uploadFiles).toHaveBeenCalledWith('sess_existing', [pending.file])
    expect(complete.status).toEqual({ type: 'complete' })

    const resolved = takeResolvedUpload(pending.id)
    expect(resolved?.ref).toBe('media://abc')
    expect(resolved?.url).toBe('/api/v1/uploads/sess_existing/report.docx')
    expect(resolved?.contentType).toBe(DOCX)
    // Drained — a second take returns nothing (no stale refs leak into later turns).
    expect(takeResolvedUpload(pending.id)).toBeUndefined()
  })

  it('send() throws (not silently drops) when registration yields no media ref', async () => {
    vi.mocked(api.uploadFiles).mockResolvedValue({
      files: [{ name: 'x.docx', path: 'uploads/sess_existing/x.docx', size: 10, content_type: DOCX }],
    } as never)

    const pending = await omnipusAttachmentAdapter.add({ file: mkFile('x.docx', DOCX) })
    await expect(omnipusAttachmentAdapter.send(pending)).rejects.toThrow(/could not be registered/)
    expect(takeResolvedUpload(pending.id)).toBeUndefined()
  })

  it('send() throws when the upload returns no usable path', async () => {
    vi.mocked(api.uploadFiles).mockResolvedValue({
      files: [{ name: 'x.docx', path: '', size: 0, content_type: DOCX }],
    } as never)

    const pending = await omnipusAttachmentAdapter.add({ file: mkFile('x.docx', DOCX) })
    await expect(omnipusAttachmentAdapter.send(pending)).rejects.toThrow(/Upload failed/)
  })

  it('send() auto-creates a session when none is active, then uploads against it (#252)', async () => {
    useSessionStore.setState({ activeSessionId: null, activeAgentId: 'jim', activeAgentType: null })
    vi.mocked(api.createSession).mockResolvedValue({ id: 'sess_new', agent_id: 'jim' } as never)
    vi.mocked(api.uploadFiles).mockResolvedValue({
      files: [{ name: 'x.docx', path: 'uploads/sess_new/x.docx', size: 1, content_type: DOCX, ref: 'media://n' }],
    } as never)

    const pending = await omnipusAttachmentAdapter.add({ file: mkFile('x.docx', DOCX) })
    await omnipusAttachmentAdapter.send(pending)

    expect(api.createSession).toHaveBeenCalledWith('jim')
    expect(api.uploadFiles).toHaveBeenCalledWith('sess_new', [pending.file])
    expect(useSessionStore.getState().activeSessionId).toBe('sess_new')
  })

  it('remove() drops a stashed ref', async () => {
    vi.mocked(api.uploadFiles).mockResolvedValue({
      files: [{ name: 'r.docx', path: 'uploads/sess_existing/r.docx', size: 1, content_type: DOCX, ref: 'media://z' }],
    } as never)

    const pending = await omnipusAttachmentAdapter.add({ file: mkFile('r.docx', DOCX) })
    await omnipusAttachmentAdapter.send(pending)
    await omnipusAttachmentAdapter.remove({ ...pending, status: { type: 'complete' }, content: [] } as never)

    expect(takeResolvedUpload(pending.id)).toBeUndefined()
  })
})
