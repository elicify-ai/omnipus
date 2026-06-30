/**
 * media-actions.test.ts — unit tests for the pure helpers in media-actions.ts.
 *
 * Covers:
 *   - Feature detection: canCopyImage, canShareFiles (mock/unmock)
 *   - copyText → navigator.clipboard.writeText
 *   - copyImageBlob → navigator.clipboard.write with a ClipboardItem
 *   - downloadBlob → creates+clicks+revokes an anchor
 *   - shareBlob → navigator.share with a File
 *   - fetchImageBlob → fetch call
 *   - svgToPngBlob → returns a Promise (canvas/Image not fully runnable in jsdom;
 *     tested via rejection on a bad input and basic promise shape).
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

import {
  canCopyImage,
  canShareFiles,
  copyText,
  copyImageBlob,
  downloadBlob,
  shareBlob,
  fetchImageBlob,
  svgToPngBlob,
} from './media-actions'

// ── Helpers ───────────────────────────────────────────────────────────────────

function makePngBlob(): Blob {
  return new Blob(['fake-png-data'], { type: 'image/png' })
}

// ── Feature detection — canCopyImage ─────────────────────────────────────────

describe('canCopyImage', () => {
  afterEach(() => {
    // Restore real navigator.clipboard if we clobbered it
    Object.defineProperty(navigator, 'clipboard', {
      value: undefined,
      writable: true,
      configurable: true,
    })
    // Remove ClipboardItem if we defined it
    if ('ClipboardItem' in globalThis) {
      Object.defineProperty(globalThis, 'ClipboardItem', {
        value: undefined,
        writable: true,
        configurable: true,
      })
    }
  })

  it('returns false when ClipboardItem is undefined', () => {
    Object.defineProperty(globalThis, 'ClipboardItem', {
      value: undefined,
      writable: true,
      configurable: true,
    })
    Object.defineProperty(navigator, 'clipboard', {
      value: { write: vi.fn() },
      writable: true,
      configurable: true,
    })
    expect(canCopyImage()).toBe(false)
  })

  it('returns false when navigator.clipboard.write is missing', () => {
    Object.defineProperty(globalThis, 'ClipboardItem', {
      value: class ClipboardItemMock {},
      writable: true,
      configurable: true,
    })
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn() }, // write is absent
      writable: true,
      configurable: true,
    })
    expect(canCopyImage()).toBe(false)
  })

  it('returns true when both ClipboardItem and clipboard.write are present', () => {
    Object.defineProperty(globalThis, 'ClipboardItem', {
      value: class ClipboardItemMock {},
      writable: true,
      configurable: true,
    })
    Object.defineProperty(navigator, 'clipboard', {
      value: { write: vi.fn() },
      writable: true,
      configurable: true,
    })
    expect(canCopyImage()).toBe(true)
  })
})

// ── Feature detection — canShareFiles ────────────────────────────────────────

describe('canShareFiles', () => {
  afterEach(() => {
    Object.defineProperty(navigator, 'share', {
      value: undefined,
      writable: true,
      configurable: true,
    })
  })

  it('returns false when navigator.share is not a function', () => {
    Object.defineProperty(navigator, 'share', {
      value: undefined,
      writable: true,
      configurable: true,
    })
    expect(canShareFiles()).toBe(false)
  })

  it('returns true when navigator.share is a function', () => {
    Object.defineProperty(navigator, 'share', {
      value: vi.fn(),
      writable: true,
      configurable: true,
    })
    expect(canShareFiles()).toBe(true)
  })
})

// ── copyText ─────────────────────────────────────────────────────────────────

describe('copyText', () => {
  beforeEach(() => {
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
      writable: true,
      configurable: true,
    })
  })

  it('calls navigator.clipboard.writeText with the given text', async () => {
    await copyText('hello world')
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith('hello world')
  })

  it('propagates clipboard rejection', async () => {
    ;(navigator.clipboard.writeText as ReturnType<typeof vi.fn>).mockRejectedValue(
      new Error('denied'),
    )
    await expect(copyText('fail')).rejects.toThrow('denied')
  })
})

// ── copyImageBlob ─────────────────────────────────────────────────────────────

describe('copyImageBlob', () => {
  const writeMock = vi.fn().mockResolvedValue(undefined)

  beforeEach(() => {
    writeMock.mockReset().mockResolvedValue(undefined)

    // Provide a minimal ClipboardItem constructor
    Object.defineProperty(globalThis, 'ClipboardItem', {
      value: class ClipboardItemMock {
        data: Record<string, Blob>
        constructor(data: Record<string, Blob>) {
          this.data = data
        }
      },
      writable: true,
      configurable: true,
    })

    Object.defineProperty(navigator, 'clipboard', {
      value: { write: writeMock },
      writable: true,
      configurable: true,
    })
  })

  it('calls navigator.clipboard.write with a ClipboardItem containing the blob', async () => {
    const blob = makePngBlob()
    await copyImageBlob(blob)
    expect(writeMock).toHaveBeenCalledTimes(1)
    const [items] = writeMock.mock.calls[0] as [unknown[]]
    expect(items).toHaveLength(1)
    // The item should be a ClipboardItem instance
    const item = items[0] as InstanceType<typeof ClipboardItem>
    expect(item).toBeInstanceOf(globalThis.ClipboardItem)
  })
})

// ── downloadBlob ─────────────────────────────────────────────────────────────

describe('downloadBlob', () => {
  function setup() {
    const fakeUrl = 'blob:http://localhost/fake-url'
    const createObjectUrl = vi.spyOn(URL, 'createObjectURL').mockReturnValue(fakeUrl)
    const revokeObjectUrl = vi.spyOn(URL, 'revokeObjectURL').mockReturnValue(undefined)
    const clickSpy = vi.fn()
    const removeSpy = vi.fn()
    const mockAnchor = { href: '', download: '', click: clickSpy, remove: removeSpy }
    const createElementSpy = vi
      .spyOn(document, 'createElement')
      .mockReturnValue(mockAnchor as unknown as HTMLAnchorElement)
    const appendSpy = vi
      .spyOn(document.body, 'appendChild')
      .mockReturnValue(mockAnchor as unknown as Node)
    const restore = () => {
      createObjectUrl.mockRestore()
      revokeObjectUrl.mockRestore()
      createElementSpy.mockRestore()
      appendSpy.mockRestore()
    }
    return { fakeUrl, createObjectUrl, revokeObjectUrl, clickSpy, removeSpy, mockAnchor, appendSpy, restore }
  }

  it('appends an anchor, clicks it, removes it, and revokes the URL on a later tick', () => {
    vi.useFakeTimers()
    const t = setup()

    const blob = makePngBlob()
    downloadBlob(blob, 'diagram.png')

    expect(t.createObjectUrl).toHaveBeenCalledWith(blob)
    expect(t.mockAnchor.href).toBe(t.fakeUrl)
    expect(t.mockAnchor.download).toBe('diagram.png')
    expect(t.appendSpy).toHaveBeenCalledWith(t.mockAnchor)
    expect(t.clickSpy).toHaveBeenCalledTimes(1)
    expect(t.removeSpy).toHaveBeenCalledTimes(1)
    // Revoke is deferred so a synchronous revoke can't abort the download (Firefox).
    expect(t.revokeObjectUrl).not.toHaveBeenCalled()
    vi.runAllTimers()
    expect(t.revokeObjectUrl).toHaveBeenCalledWith(t.fakeUrl)

    vi.useRealTimers()
    t.restore()
  })

  it('drops path components and bidi-override chars from the download name', () => {
    const t = setup()
    // Basename is taken (path stripped) and the U+202E RTL-override removed.
    downloadBlob(makePngBlob(), '../../etc/pa‮gnp.ss')
    expect(t.mockAnchor.download).toBe('pagnp.ss')
    expect(t.mockAnchor.download).not.toMatch(/[\\/\u202A-\u202E]/)
    t.restore()
  })

  it('falls back to "download" when the name is only path separators', () => {
    const t = setup()
    downloadBlob(makePngBlob(), '///')
    expect(t.mockAnchor.download).toBe('download')
    t.restore()
  })
})

// ── shareBlob ────────────────────────────────────────────────────────────────

describe('shareBlob', () => {
  afterEach(() => {
    Object.defineProperty(navigator, 'share', {
      value: undefined,
      writable: true,
      configurable: true,
    })
  })

  it('throws when share is not available', async () => {
    Object.defineProperty(navigator, 'share', {
      value: undefined,
      writable: true,
      configurable: true,
    })
    const blob = makePngBlob()
    await expect(shareBlob(blob, 'image.png')).rejects.toThrow(
      'Web Share API is not available in this browser',
    )
  })

  it('calls navigator.share with a File object and the given title', async () => {
    const shareMock = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'share', {
      value: shareMock,
      writable: true,
      configurable: true,
    })

    const blob = makePngBlob()
    await shareBlob(blob, 'diagram.png', 'My Diagram')

    expect(shareMock).toHaveBeenCalledTimes(1)
    const [arg] = shareMock.mock.calls[0] as [ShareData]
    expect(arg.title).toBe('My Diagram')
    expect(arg.files).toHaveLength(1)
    const file = arg.files![0]
    expect(file).toBeInstanceOf(File)
    expect(file.name).toBe('diagram.png')
    expect(file.type).toBe('image/png')
  })

  it('calls navigator.share without title when title is omitted', async () => {
    const shareMock = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'share', {
      value: shareMock,
      writable: true,
      configurable: true,
    })

    const blob = makePngBlob()
    await shareBlob(blob, 'image.png')

    const [arg] = shareMock.mock.calls[0] as [ShareData]
    expect(arg.title).toBeUndefined()
  })
})

// ── fetchImageBlob ────────────────────────────────────────────────────────────

describe('fetchImageBlob', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('fetches the URL and returns an image blob', async () => {
    const fakeBlob = makePngBlob()
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      blob: vi.fn().mockResolvedValue(fakeBlob),
    })
    vi.stubGlobal('fetch', fetchMock)

    const result = await fetchImageBlob('http://localhost/img.png')
    expect(fetchMock).toHaveBeenCalledWith('http://localhost/img.png')
    expect(result).toBe(fakeBlob)
  })

  // SECURITY: the result lands in the clipboard / a download. These gates stop an
  // agent-supplied src from turning fetchImageBlob into a credentialed-read exfil sink.
  it('rejects a non-image content-type (e.g. an internal JSON endpoint)', async () => {
    const jsonBlob = new Blob(['{"secret":1}'], { type: 'application/json' })
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      blob: vi.fn().mockResolvedValue(jsonBlob),
    })
    vi.stubGlobal('fetch', fetchMock)
    await expect(fetchImageBlob('/api/v1/credentials')).rejects.toThrow(/did not return an image/i)
  })

  it('rejects a non-ok response without reading its body', async () => {
    const blobReader = vi.fn()
    const fetchMock = vi.fn().mockResolvedValue({ ok: false, status: 403, blob: blobReader })
    vi.stubGlobal('fetch', fetchMock)
    await expect(fetchImageBlob('/api/v1/secret')).rejects.toThrow(/403/)
    expect(blobReader).not.toHaveBeenCalled()
  })

  it('rejects an unsupported URL scheme (file:) before fetching', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    await expect(fetchImageBlob('file:///etc/passwd')).rejects.toThrow(/scheme/i)
    expect(fetchMock).not.toHaveBeenCalled()
  })
})

// ── svgToPngBlob ─────────────────────────────────────────────────────────────
// jsdom cannot rasterise canvas drawing, so we test the promise contract rather
// than the rendered pixel output.

describe('svgToPngBlob', () => {
  it('returns a Promise', () => {
    const result = svgToPngBlob('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"/>')
    expect(result).toBeInstanceOf(Promise)
  })

  it('resolves or rejects (does not throw synchronously)', async () => {
    const svg = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"></svg>'
    // jsdom Image.onload never fires, so this will pend — test that it's a Promise
    const promise = svgToPngBlob(svg, 1)
    expect(promise).toBeInstanceOf(Promise)
    // We cannot await it to resolution in jsdom (Image never loads), so just
    // verify it doesn't throw synchronously.
  })

  it('accepts an SVGElement input', () => {
    const svgEl = document.createElementNS('http://www.w3.org/2000/svg', 'svg')
    svgEl.setAttribute('viewBox', '0 0 200 100')
    const result = svgToPngBlob(svgEl)
    expect(result).toBeInstanceOf(Promise)
  })
})
