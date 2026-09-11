// KbPdfPageEmbedMount.test.tsx — ADR-083 embedded-content spec, Step 6
// (EMB-105, US-12 AS-4): "Given a note embedding a specific page of a PDF,
// When it is read, Then that page is shown." Every negative assertion below
// is paired with a positive one proving the real PDF renderer mounted, so a
// deleted dispatch fails the pair instead of passing trivially — see this
// task's own report for the mutation-test proof on this exact file.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { LibraryEntry } from '@/lib/api'

const ENTRY: LibraryEntry = {
  name: 'doc.pdf',
  path: 'reports/doc.pdf',
  is_dir: false,
  is_hidden: false,
  size: 4096,
  modified_at: '2026-08-22T10:15:00Z',
  is_text_editable: false,
}

const MANIFEST = {
  cmaps: ['78-EUC-H.bcmap'],
  standard_fonts: ['FoxitSans.pfb'],
  wasm: ['openjpeg.wasm'],
  iccs: ['sRGB.icc'],
}

function jsonResponse(body: unknown): Response {
  return {
    ok: true,
    status: 200,
    headers: new Headers({ 'content-type': 'application/json' }),
    json: () => Promise.resolve(body),
  } as unknown as Response
}

function binaryResponse(): Response {
  return {
    ok: true,
    status: 200,
    headers: new Headers({ 'content-type': 'application/octet-stream' }),
    arrayBuffer: () => Promise.resolve(new ArrayBuffer(8)),
  } as unknown as Response
}

class FakeWorker extends EventTarget {
  terminate() {}
  postMessage() {}
}

function makePage() {
  return {
    getViewport: ({ scale }: { scale: number }) => ({ width: 600 * scale, height: 800 * scale, rotation: 0 }),
    render: () => ({ promise: Promise.resolve(), cancel: () => {} }),
    streamTextContent: () => 'text-content-stream',
    getAnnotations: () => Promise.resolve([]),
  }
}

vi.mock('pdfjs-dist', () => {
  class TextLayer {
    render() {
      return Promise.resolve()
    }
    cancel() {}
  }
  return {
    AnnotationMode: { ENABLE: 1 },
    AnnotationEditorType: { INK: 15 },
    AnnotationLayer: class {},
    TextLayer,
    PDFWorker: { create: () => ({ destroy: () => Promise.resolve() }) },
    getDocument: () => ({
      promise: Promise.resolve({
        numPages: 5,
        getPage: () => Promise.resolve(makePage()),
        annotationStorage: { onSetModified: null, onResetModified: null },
        getFieldObjects: () => Promise.resolve(null),
        saveDocument: () => Promise.resolve(new Uint8Array()),
      }),
      destroy: () => Promise.resolve(),
    }),
  }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchLibraryEntries: vi.fn(),
  }
})

import { fetchLibraryEntries } from '@/lib/api'
import { KbPdfPageEmbedMount } from './KbPdfPageEmbedMount'

function renderMount(page = 3) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <KbPdfPageEmbedMount workspaceId="ws-1" workspacePath="reports/doc.pdf" page={page} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.stubGlobal(
    'fetch',
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.startsWith('/pdfjs/')) {
        const rel = url.slice('/pdfjs/'.length)
        return Promise.resolve(rel === 'asset-manifest.json' ? jsonResponse(MANIFEST) : binaryResponse())
      }
      if (url.includes('/api/v1/library/')) return Promise.resolve(binaryResponse())
      return Promise.reject(new Error(`unexpected fetch: ${url}`))
    }),
  )
  vi.stubGlobal('Worker', FakeWorker)
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({} as unknown as CanvasRenderingContext2D)
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('KbPdfPageEmbedMount', () => {
  it('mounts the real PDF renderer, showing only the requested page, once the entry resolves', async () => {
    vi.mocked(fetchLibraryEntries).mockResolvedValue([ENTRY])
    const { container } = renderMount(3)

    await waitFor(() => expect(within(container).getAllByTestId('library-pdf-page')).toHaveLength(1))
    expect(within(container).getByTestId('library-pdf-page')).toHaveAttribute('data-page-number', '3')
    expect(within(container).getByTestId('library-pdf-preview')).toHaveAttribute('data-variant', 'inline')
    // Paired negative: the embed-level loading/error placeholders are gone.
    expect(screen.queryByTestId('kb-embed-mount-loading')).not.toBeInTheDocument()
    expect(screen.queryByTestId('kb-embed-mount-error')).not.toBeInTheDocument()
  })

  it('shows the loading placeholder before the directory listing resolves', () => {
    vi.mocked(fetchLibraryEntries).mockReturnValue(new Promise(() => {}))
    renderMount(3)

    expect(screen.getByTestId('kb-embed-mount-loading')).toBeInTheDocument()
    expect(screen.queryByTestId('library-pdf-preview')).not.toBeInTheDocument()
  })

  it('shows a visible, named error when the listing request FAILS', async () => {
    vi.mocked(fetchLibraryEntries).mockRejectedValue(new Error('listing failed'))
    renderMount(3)

    const error = await screen.findByTestId('kb-embed-mount-error')
    expect(error).toHaveTextContent(/could not read this file/i)
    expect(screen.queryByTestId('library-pdf-preview')).not.toBeInTheDocument()
  })

  // Silent-failure audit M1, same distinction as the audio/video mounts.
  it('shows the distinct "missing" state, with no Retry, when the listing succeeded without the file', async () => {
    vi.mocked(fetchLibraryEntries).mockResolvedValue([])
    renderMount(3)

    const missing = await screen.findByTestId('kb-embed-mount-missing')
    expect(missing.querySelector('button')).toBeNull()
    expect(screen.queryByTestId('kb-embed-mount-error')).not.toBeInTheDocument()
    expect(screen.queryByTestId('library-pdf-preview')).not.toBeInTheDocument()
  })
})
