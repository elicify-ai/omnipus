// LibraryPreviewPane.revoke.test.tsx — UAT 2026-09-13 D-110 (SPA half),
// Codex review #11: closing an HTML preview must REVOKE its preview token.
//
// The endpoint (DELETE /api/v1/library/preview-token/{token}, generated
// operation revokeLibraryPreviewToken) shipped in the previous round, but the
// SPA never called it — preview cleanup only cleared its expiry timer, so a
// copied frame URL kept answering 200 for the rest of its 15-minute life.
// Codex called this "a direct violation of CLAUDE.md's feature-reachability
// rule". These tests pin the two cases the finding names:
//
//   1. closing the pane revokes the token the frame was using;
//   2. a mint whose request completes AFTER the pane has already unmounted is
//      revoked too — otherwise a fast close races the mint and leaks a live
//      credential nobody is holding a frame for.
//
// Both die on: removing the cleanup effect (1), or storing the token only in
// the effect and not checking `unmounted` inside the mint wrapper (2).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { LibraryEntry, LibraryContentResponse } from '@/lib/api'
import type { LibraryPreviewTokenResponse } from '@/lib/api/generated/openapi-types'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchLibraryContent: vi.fn(),
    fetchLibraryContentVersioned: vi.fn(),
    putLibraryContent: vi.fn(),
    mintLibraryPreviewToken: vi.fn(),
    revokeLibraryPreviewToken: vi.fn(),
  }
})

vi.mock('@uiw/react-codemirror', () => ({
  default: ({ value }: { value: string }) => <textarea data-testid="library-editor-textarea" value={value} readOnly />,
}))

import { fetchLibraryContent, fetchLibraryContentVersioned, revokeLibraryPreviewToken } from '@/lib/api'
import { LibraryPreviewPane } from './LibraryPreviewPane'
import type { MintLibraryPreviewToken } from './LibraryPreviewPane'

const mockedFetchContent = vi.mocked(fetchLibraryContent)
const mockedFetchContentVersioned = vi.mocked(fetchLibraryContentVersioned)
const mockedRevoke = vi.mocked(revokeLibraryPreviewToken)

const TOKEN = 'kZ8vQ2mR7xT1yB4nW6cA9pL0sD3fG5hJ8kM2nP4qR6t'

function makeToken(over: Partial<LibraryPreviewTokenResponse> = {}): LibraryPreviewTokenResponse {
  return {
    token: TOKEN,
    url: `/library-preview/${TOKEN}/report.html`,
    expires_at: '2026-08-22T14:45:00Z',
    expires_in_seconds: 900,
    scope: 'file',
    scope_root: 'report.html',
    workspace_id: 'ws-1',
    ...over,
  }
}

function htmlEntry(): LibraryEntry {
  return {
    name: 'report.html',
    path: 'report.html',
    is_dir: false,
    is_hidden: false,
    size: 40,
    modified_at: '2026-07-28T10:15:00Z',
    mime: 'text/html',
    is_text_editable: true,
  }
}

function makeContent(): LibraryContentResponse {
  return { path: 'report.html', content: '<h1>Q3</h1>', size: 11, is_text: true, too_large: false }
}

function renderPane(mint: MintLibraryPreviewToken) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <LibraryPreviewPane
        workspaceId="ws-1"
        entry={htmlEntry()}
        onClose={vi.fn()}
        onDownload={vi.fn()}
        mintPreviewToken={mint}
      />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  mockedRevoke.mockResolvedValue(undefined)
  mockedFetchContent.mockResolvedValue(makeContent())
  mockedFetchContentVersioned.mockImplementation(async (workspaceId, path) => ({
    data: await mockedFetchContent(workspaceId, path),
    version: 'v1:default',
  }))
})

describe('D-110 — closing an HTML preview revokes its token', () => {
  it('calls revokeLibraryPreviewToken with the minted token when the pane unmounts, and not before', async () => {
    const mint = vi.fn<MintLibraryPreviewToken>(() => Promise.resolve(makeToken()))
    const { unmount } = renderPane(mint)

    const frame = await screen.findByTestId('library-html-preview-frame')
    expect(frame).toHaveAttribute('src', makeToken().url)
    // Negative control: a live frame must keep its credential.
    expect(mockedRevoke).not.toHaveBeenCalled()

    unmount()

    expect(mockedRevoke).toHaveBeenCalledTimes(1)
    expect(mockedRevoke).toHaveBeenCalledWith(TOKEN)
  })

  it('revokes a token whose mint completes only after the pane has unmounted', async () => {
    let resolveMint: ((r: LibraryPreviewTokenResponse) => void) | undefined
    const mint = vi.fn<MintLibraryPreviewToken>(
      () =>
        new Promise<LibraryPreviewTokenResponse>((resolve) => {
          resolveMint = resolve
        }),
    )
    const { unmount } = renderPane(mint)
    await waitFor(() => expect(mint).toHaveBeenCalledTimes(1))
    expect(screen.getByTestId('library-html-preview-loading')).toBeInTheDocument()

    // Close while the mint request is still in flight.
    unmount()
    expect(mockedRevoke).not.toHaveBeenCalled()

    // The server answers after the reader is gone: the credential must not
    // outlive the pane that asked for it.
    resolveMint?.(makeToken({ token: 'late-token', url: '/library-preview/late-token/report.html' }))
    await waitFor(() => expect(mockedRevoke).toHaveBeenCalledWith('late-token'))
    expect(mockedRevoke).toHaveBeenCalledTimes(1)
  })

  it('a revoke failure is swallowed — the pane is already gone and there is nothing to show it on', async () => {
    mockedRevoke.mockRejectedValue(new Error('network down'))
    const mint = vi.fn<MintLibraryPreviewToken>(() => Promise.resolve(makeToken()))
    const { unmount } = renderPane(mint)
    await screen.findByTestId('library-html-preview-frame')

    expect(() => unmount()).not.toThrow()
    expect(mockedRevoke).toHaveBeenCalledWith(TOKEN)
    // Let the rejected promise settle; an unhandled rejection would fail the
    // run under vitest's default `dangerouslyIgnoreUnhandledErrors: false`.
    await new Promise((r) => setTimeout(r, 0))
  })
})
