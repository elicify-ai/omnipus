import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api')
  return {
    ...actual,
    uploadFiles: vi.fn(),
    createSession: vi.fn(),
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: {
    getState: vi.fn(() => ({ addToast: vi.fn() })),
  },
}))

vi.mock('@/components/chat/AttachmentCard', () => ({
  isImageAttachment: vi.fn((filename: string, contentType?: string) =>
    (contentType ?? '').startsWith('image/') || /\.(png|jpe?g|gif|webp|svg|bmp|avif)$/i.test(filename)
  ),
}))

import * as api from '@/lib/api'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { omnipusAttachmentAdapter, takeResolvedUpload, isAcceptedFile } from './attachment-adapter'

const DOCX = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'

function mkFile(name: string, type: string): File {
  return new File(['data'], name, { type })
}

function getAddToast() {
  return vi.mocked(useUiStore.getState)().addToast as ReturnType<typeof vi.fn>
}

beforeEach(() => {
  vi.clearAllMocks()
  // Re-wire the mock so each test gets a fresh spy.
  const addToast = vi.fn()
  vi.mocked(useUiStore.getState).mockReturnValue({ addToast } as never)
  useSessionStore.setState({ activeSessionId: 'sess_existing', activeAgentId: 'jim', activeAgentType: null })
  useWorkspacesStore.setState({ activeWorkspaceId: null })
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

    expect(api.uploadFiles).toHaveBeenCalledWith('sess_existing', [pending.file], undefined)
    expect(complete.status).toEqual({ type: 'complete' })

    const resolved = takeResolvedUpload(pending.id)
    expect(resolved?.ref).toBe('media://abc')
    expect(resolved?.url).toBe('/api/v1/media/abc')
    expect(resolved?.contentType).toBe(DOCX)
    // Drained — a second take returns nothing (no stale refs leak into later turns).
    expect(takeResolvedUpload(pending.id)).toBeUndefined()
  })

  // D16 regression (RCA-verified): the composer's normal drag/drop/paste
  // attach path shares the exact same hardcoded-URL bug as
  // browserAnnotate.ts. When a file is attached in a workspace-scoped chat,
  // uploadFiles() routes it into the workspace media library and returns a
  // `media://workspace/<ws>/<id>` ref — the stashed preview `url` must be
  // built from that ref (mediaRefURL), not from
  // `/api/v1/uploads/${sessionId}/${name}` (which 404s — the workspace path
  // never writes to the legacy uploads/ dir).
  it('D16: send() builds the workspace-scoped preview URL from the ref when a workspace is active', async () => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-1' })
    vi.mocked(api.uploadFiles).mockResolvedValue({
      files: [{ name: 'diagram.png', path: 'workspaces/ws-1/media/med-1', size: 10, content_type: 'image/png', ref: 'media://workspace/ws-1/med-1' }],
    } as never)

    const pending = await omnipusAttachmentAdapter.add({ file: mkFile('diagram.png', 'image/png') })
    await omnipusAttachmentAdapter.send(pending)

    expect(api.uploadFiles).toHaveBeenCalledWith('sess_existing', [pending.file], 'ws-1')
    const resolved = takeResolvedUpload(pending.id)
    expect(resolved?.ref).toBe('media://workspace/ws-1/med-1')
    expect(resolved?.url).toBe('/api/v1/media/workspace/ws-1/med-1')
  })

  it('send() toasts (not throws) when registration yields no media ref', async () => {
    vi.mocked(api.uploadFiles).mockResolvedValue({
      files: [{ name: 'x.docx', path: 'uploads/sess_existing/x.docx', size: 10, content_type: DOCX }],
    } as never)

    const pending = await omnipusAttachmentAdapter.add({ file: mkFile('x.docx', DOCX) })
    // Must NOT throw — resolves to a CompleteAttachment with no stashed ref.
    const complete = await omnipusAttachmentAdapter.send(pending)
    expect(complete.status).toEqual({ type: 'complete' })
    // No ref was stashed.
    expect(takeResolvedUpload(pending.id)).toBeUndefined()
    // User was informed via a toast.
    expect(getAddToast()).toHaveBeenCalledWith(
      expect.objectContaining({ variant: 'error', message: expect.stringMatching(/could not be registered/) })
    )
  })

  it('send() toasts (not throws) when the upload returns no usable path', async () => {
    vi.mocked(api.uploadFiles).mockResolvedValue({
      files: [{ name: 'x.docx', path: '', size: 0, content_type: DOCX }],
    } as never)

    const pending = await omnipusAttachmentAdapter.add({ file: mkFile('x.docx', DOCX) })
    // Must NOT throw.
    const complete = await omnipusAttachmentAdapter.send(pending)
    expect(complete.status).toEqual({ type: 'complete' })
    expect(takeResolvedUpload(pending.id)).toBeUndefined()
    expect(getAddToast()).toHaveBeenCalledWith(
      expect.objectContaining({ variant: 'error', message: expect.stringMatching(/Upload failed/) })
    )
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
    expect(api.uploadFiles).toHaveBeenCalledWith('sess_new', [pending.file], undefined)
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

  it('resolvedUploads map is capped: oldest entry evicted when limit is exceeded', async () => {
    // Fill the map to the cap (50) using successful sends, then add one more and
    // confirm the oldest entry was evicted.
    const CAP = 50

    // We need real attachment ids — use add() to generate them.
    const pendings = []
    for (let i = 0; i < CAP + 1; i++) {
      vi.mocked(api.uploadFiles).mockResolvedValue({
        files: [{ name: `f${i}.docx`, path: `uploads/sess_existing/f${i}.docx`, size: 1, content_type: DOCX, ref: `media://r${i}` }],
      } as never)
      const p = await omnipusAttachmentAdapter.add({ file: mkFile(`f${i}.docx`, DOCX) })
      await omnipusAttachmentAdapter.send(p)
      pendings.push(p)
    }

    // The first entry (index 0) should have been evicted to make room for index CAP.
    expect(takeResolvedUpload(pendings[0].id)).toBeUndefined()
    // The last entry should still be present.
    expect(takeResolvedUpload(pendings[CAP].id)?.ref).toBe(`media://r${CAP}`)
  })
})

// ── B7: accept-list validation ────────────────────────────────────────────────

describe('isAcceptedFile (B7)', () => {
  it('accepts image/png via image/* wildcard MIME', () => {
    expect(isAcceptedFile('photo.png', 'image/png')).toBe(true)
  })

  it('accepts image/jpeg via image/* wildcard MIME', () => {
    expect(isAcceptedFile('shot.jpg', 'image/jpeg')).toBe(true)
  })

  it('accepts application/pdf via exact MIME', () => {
    expect(isAcceptedFile('report.pdf', 'application/pdf')).toBe(true)
  })

  it('accepts .txt by extension even with octet-stream MIME', () => {
    expect(isAcceptedFile('notes.txt', 'application/octet-stream')).toBe(true)
  })

  it('accepts .txt by extension with empty MIME', () => {
    expect(isAcceptedFile('notes.txt', '')).toBe(true)
  })

  it('accepts .md extension regardless of MIME', () => {
    expect(isAcceptedFile('README.md', 'application/octet-stream')).toBe(true)
  })

  it('accepts .csv extension', () => {
    expect(isAcceptedFile('data.csv', 'text/csv')).toBe(true)
  })

  it('accepts .json extension with unknown MIME', () => {
    expect(isAcceptedFile('config.json', 'application/octet-stream')).toBe(true)
  })

  it('rejects video/mp4 (not in ACCEPT_LIST)', () => {
    expect(isAcceptedFile('clip.mp4', 'video/mp4')).toBe(false)
  })

  it('rejects .exe — unknown extension, non-matching MIME', () => {
    expect(isAcceptedFile('setup.exe', 'application/x-msdownload')).toBe(false)
  })

  it('rejects application/zip (not in ACCEPT_LIST)', () => {
    expect(isAcceptedFile('archive.zip', 'application/zip')).toBe(false)
  })

  it('is case-insensitive for extensions', () => {
    expect(isAcceptedFile('NOTES.TXT', 'application/octet-stream')).toBe(true)
    expect(isAcceptedFile('PHOTO.PNG', 'IMAGE/PNG')).toBe(true)
  })
})

describe('add() accept-list gate (B7)', () => {
  it('add() throws a clear error for an unsupported file type', async () => {
    const file = mkFile('malware.exe', 'application/x-msdownload')
    await expect(omnipusAttachmentAdapter.add({ file })).rejects.toThrow(
      "Can't attach \"malware.exe\": file type not supported"
    )
    // Guard: no upload call should have escaped.
    expect(api.uploadFiles).not.toHaveBeenCalled()
  })

  it('add() throws for video/mp4', async () => {
    await expect(
      omnipusAttachmentAdapter.add({ file: mkFile('clip.mp4', 'video/mp4') })
    ).rejects.toThrow(/file type not supported/)
  })

  it('add() accepts image/png (wildcard MIME match)', async () => {
    const pending = await omnipusAttachmentAdapter.add({ file: mkFile('photo.png', 'image/png') })
    expect(pending.status).toEqual({ type: 'requires-action', reason: 'composer-send' })
    expect(pending.type).toBe('image')
  })

  it('add() accepts .txt by extension even with octet-stream MIME', async () => {
    const pending = await omnipusAttachmentAdapter.add({
      file: mkFile('log.txt', 'application/octet-stream'),
    })
    expect(pending.status).toEqual({ type: 'requires-action', reason: 'composer-send' })
    expect(pending.name).toBe('log.txt')
  })

  it('add() accepts application/pdf (exact MIME match)', async () => {
    const pending = await omnipusAttachmentAdapter.add({ file: mkFile('doc.pdf', 'application/pdf') })
    expect(pending.status).toEqual({ type: 'requires-action', reason: 'composer-send' })
  })
})

// ── G15 + G16 new tests ──────────────────────────────────────────────────────

import { MAX_UPLOAD_BYTES } from './attachment-adapter'

describe('G15 — audio-specific rejection message', () => {
  it('dropping an .mp3 toasts exactly "Audio files aren\'t supported yet." and does not call uploadFiles', async () => {
    const file = mkFile('song.mp3', 'audio/mpeg')
    await expect(omnipusAttachmentAdapter.add({ file })).rejects.toThrow("Audio files aren't supported yet.")
    expect(getAddToast()).toHaveBeenCalledOnce()
    expect(getAddToast()).toHaveBeenCalledWith(
      expect.objectContaining({ message: "Audio files aren't supported yet.", variant: 'error' })
    )
    expect(api.uploadFiles).not.toHaveBeenCalled()
  })

  it('dropping a file with audio/ MIME toasts "Audio files aren\'t supported yet."', async () => {
    const file = mkFile('track.wav', 'audio/wav')
    await expect(omnipusAttachmentAdapter.add({ file })).rejects.toThrow("Audio files aren't supported yet.")
    expect(getAddToast()).toHaveBeenCalledWith(
      expect.objectContaining({ message: "Audio files aren't supported yet." })
    )
    expect(api.uploadFiles).not.toHaveBeenCalled()
  })

  it('dropping a file with .ogg extension and audio/ MIME toasts "Audio files aren\'t supported yet."', async () => {
    const file = mkFile('podcast.ogg', 'audio/ogg')
    await expect(omnipusAttachmentAdapter.add({ file })).rejects.toThrow("Audio files aren't supported yet.")
    expect(getAddToast()).toHaveBeenCalledWith(
      expect.objectContaining({ message: "Audio files aren't supported yet." })
    )
    expect(api.uploadFiles).not.toHaveBeenCalled()
  })
})

describe('G16 — size pre-check', () => {
  it('a file exceeding 100 MB toasts the too-large message and is not uploaded', async () => {
    // Create a File whose .size property exceeds the limit.
    const oversized = new File([new ArrayBuffer(MAX_UPLOAD_BYTES + 1)], 'huge.docx', { type: DOCX })
    await expect(omnipusAttachmentAdapter.add({ file: oversized })).rejects.toThrow(/is too large/)
    expect(getAddToast()).toHaveBeenCalledOnce()
    expect(getAddToast()).toHaveBeenCalledWith(
      expect.objectContaining({ message: '"huge.docx" is too large (max 100 MB).', variant: 'error' })
    )
    expect(api.uploadFiles).not.toHaveBeenCalled()
  })

  it('a file exactly at the limit (100 MB) is accepted', async () => {
    const exactSize = new File([new ArrayBuffer(MAX_UPLOAD_BYTES)], 'exact.docx', { type: DOCX })
    const pending = await omnipusAttachmentAdapter.add({ file: exactSize })
    expect(pending.status).toEqual({ type: 'requires-action', reason: 'composer-send' })
    expect(api.uploadFiles).not.toHaveBeenCalled()
  })
})

describe('G16 — upload failure toast', () => {
  it('on uploadFiles rejection, send() returns a failedComplete and toasts the error', async () => {
    vi.mocked(api.uploadFiles).mockRejectedValue(new Error('network error'))

    const pending = await omnipusAttachmentAdapter.add({ file: mkFile('doc.docx', DOCX) })
    const complete = await omnipusAttachmentAdapter.send(pending)

    // send() must not throw — it returns a failedComplete so the typed text still sends.
    expect(complete.status).toEqual({ type: 'complete' })
    expect(getAddToast()).toHaveBeenCalledWith(
      expect.objectContaining({
        variant: 'error',
        message: expect.stringMatching(/Upload failed for/),
      })
    )
  })
})
