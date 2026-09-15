// LibraryCodePreview.uat.test.tsx — the three 2026-09-13 UAT findings on the
// raw text view: D-38 (a huge single-line file painted as a blank strip),
// D-118 (invalid JSON/YAML saved and shown with no warning) and D-103 (the
// language badge under AA contrast).
//
// Runs the REAL react-shiki, like LibraryCodePreview.shiki.test.tsx, because
// D-103's badge is something react-shiki draws — a mock that renders children
// verbatim could never see it.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { LibraryEntry } from '@/lib/api'
import { useUiStore } from '@/store/ui'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, putLibraryContent: vi.fn(), fetchLibraryContentVersioned: vi.fn() }
})

import { fetchLibraryContentVersioned } from '@/lib/api'
import { setLibraryEditorDirty } from './unsavedGuard'
import {
  LibraryCodePreview,
  HIGHLIGHT_MAX_CHARS,
  LONG_LINE_CHARS,
  jsonProblem,
  plainTextReason,
  yamlProblem,
} from './LibraryCodePreview'

function entry(name: string): LibraryEntry {
  return {
    name,
    path: `Assets/${name}`,
    is_dir: false,
    is_hidden: false,
    size: 1,
    modified_at: '2026-07-28T10:15:00Z',
    is_text_editable: true,
  }
}

function renderPreview(name: string, content: string) {
  // The editor hook re-reads the file for its version token on mount; the
  // fixture must answer with the SAME bytes, or the draft is replaced.
  vi.mocked(fetchLibraryContentVersioned).mockResolvedValue({
    data: { path: `Assets/${name}`, content, size: content.length, is_text: true, too_large: false },
    version: 'v1:initial',
  })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <LibraryCodePreview workspaceId="ws-1" entry={entry(name)} content={content} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  useUiStore.setState({ toasts: [] })
  setLibraryEditorDirty(false)
  vi.mocked(fetchLibraryContentVersioned).mockResolvedValue({
    data: { path: 'x', content: '', size: 0, is_text: true, too_large: false },
    version: 'v1:initial',
  })
})

describe('plainTextReason (D-38)', () => {
  it('ordinary text may be highlighted', () => {
    expect(plainTextReason('{"a": 1}\nline two\n')).toBeNull()
    expect(plainTextReason('x'.repeat(LONG_LINE_CHARS))).toBeNull()
  })
  it('one line over the limit is named, with its length', () => {
    const reason = plainTextReason(`short\n${'x'.repeat(LONG_LINE_CHARS + 1)}\n`)
    expect(reason).toMatch(/line of 10,001 characters/)
  })
  it('a file over the size limit is named, with its size', () => {
    const reason = plainTextReason('a\n'.repeat(HIGHLIGHT_MAX_CHARS / 2 + 1))
    expect(reason).toMatch(/too large to highlight/)
    expect(reason).toMatch(/KB|MB/)
  })
})

describe('jsonProblem / yamlProblem (D-118)', () => {
  it('valid documents (and empty ones) report nothing', async () => {
    expect(jsonProblem('{"a": 1, "b": [2, 3]}')).toBeNull()
    expect(jsonProblem('   ')).toBeNull()
    expect(await yamlProblem('a: 1\nb:\n  - 2\n  - 3\n')).toBeNull()
    expect(await yamlProblem('')).toBeNull()
  })
  it('the exact UAT fixture is refused, with the parser’s own reason', () => {
    const problem = jsonProblem('{"a": 1, "b": [2,3,   BROKEN]}')
    expect(problem).not.toBeNull()
    expect(problem).toMatch(/JSON|token|position/i)
  })
  it('broken YAML names the first offending line', async () => {
    const problem = await yamlProblem('ok: 1\nbad: [1, 2\nnext: 3\n')
    expect(problem).toMatch(/^syntax error near line [2-4]$/)
  })
})

describe('LibraryCodePreview view (D-118, D-38, D-103)', () => {
  it('an invalid JSON file gets a notice above its bytes, and Fix opens the editor', async () => {
    renderPreview('data.json', '{"a": 1, "b": [2,3,   BROKEN]}')
    const notice = await screen.findByTestId('library-code-invalid-notice')
    expect(notice).toHaveTextContent(/not valid JSON/)
    expect(notice).toHaveTextContent(/saved exactly as written/)
    // The bytes are still shown — the notice is IN ADDITION, never instead.
    expect(screen.getByTestId('library-code-view')).toHaveTextContent('BROKEN')
    fireEvent.click(screen.getByTestId('library-code-invalid-fix'))
    await waitFor(() =>
      expect(
        screen.queryByTestId('library-code-editor') ?? screen.queryByTestId('library-editor-loading'),
      ).not.toBeNull(),
    )
  })

  it('an invalid YAML file gets the same notice', async () => {
    renderPreview('config.yaml', 'ok: 1\nbad: [1, 2\nnext: 3\n')
    const notice = await screen.findByTestId('library-code-invalid-notice')
    expect(notice).toHaveTextContent(/not valid YAML — syntax error near line/)
  })

  it('a valid JSON file shows no notice at all', async () => {
    renderPreview('data.json', '{"a": 1}')
    await screen.findByTestId('library-code-view')
    await new Promise((r) => setTimeout(r, 20))
    expect(screen.queryByTestId('library-code-invalid-notice')).not.toBeInTheDocument()
  })

  it('a single enormous line is shown as WRAPPED plain text with a notice, not handed to the highlighter', async () => {
    const line = 'ab'.repeat(LONG_LINE_CHARS)
    renderPreview('big.txt', line)
    const pre = await screen.findByTestId('library-code-plain')
    expect(pre.className).toMatch(/whitespace-pre-wrap/)
    expect(pre.className).toMatch(/break-all/)
    expect(pre.textContent).toHaveLength(line.length)
    expect(screen.getByTestId('library-code-plain-notice')).toHaveTextContent(/line of 20,000 characters/)
    expect(screen.queryByTestId('shiki-container')).not.toBeInTheDocument()
  })

  it('the language badge is re-coloured to the theme’s muted text at 11px (D-103)', async () => {
    renderPreview('data.csv', 'a,b\n1,2\n')
    const view = await screen.findByTestId('library-code-view')
    expect(view.className).toContain('[&_[data-slot=language-label]]:!text-[var(--color-muted)]')
    expect(view.className).toContain('[&_[data-slot=language-label]]:!text-[11px]')
    // The badge this targets really is rendered inside the wrapper.
    await waitFor(() => expect(view.querySelector('[data-slot="language-label"]')).not.toBeNull())
  })
})
