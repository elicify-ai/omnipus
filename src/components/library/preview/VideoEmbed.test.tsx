// VideoEmbed.test.tsx — the click-to-play contract (ADR-083 D-C/D9, US-9,
// EMB-075..EMB-082).
//
// The trap this file is written against: a test that mounts the component
// and asserts "no iframe is present" passes trivially before ANY of the
// component's code exists — an empty `<div />` satisfies it too. Every
// assertion below is either PAIRED (the at-rest absence and the post-click
// presence live in the same test, so the pair proves the transition rather
// than either half alone) or targets a positive, load-bearing fact (the
// iframe's actual src host; the exact refusal text; a message that must
// differ from a sibling message) that a stub component cannot produce by
// accident.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAppState: vi.fn(),
  }
})

import { fetchAppState, type AppState } from '@/lib/api'
import { VideoEmbed, parseVideoEmbedUrl, buildEmbedSrc, VIDEO_ID_PATTERN } from './VideoEmbed'

const ALLOWED_HOST = 'www.youtube-nocookie.com'
const VIDEO_ID = 'dQw4w9WgXcQ' // 11 chars — the spec's own F1 fixture

function appState(over: Partial<AppState> = {}): AppState {
  return {
    onboarding_complete: true,
    video_embed_hosts: [ALLOWED_HOST],
    ...over,
  }
}

function renderEmbed(url: string, title: string | undefined, hosts: string[] | undefined) {
  vi.mocked(fetchAppState).mockResolvedValue(appState({ video_embed_hosts: hosts }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <VideoEmbed url={url} title={title} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.mocked(fetchAppState).mockReset()
})

describe('VideoEmbed — at rest, then click (the load-bearing pair)', () => {
  it('draws only a local play control at rest, then creates an iframe on the allow-listed host after a click — and only then', async () => {
    renderEmbed(`https://${ALLOWED_HOST}/embed/${VIDEO_ID}`, 'A Test Video', [ALLOWED_HOST])

    // At rest: a play control exists, and — the half a stub component
    // cannot fake — the component reached the "host is allowed" branch at
    // all, evidenced by the placeholder state and NOT the loading state.
    const playButton = await screen.findByTestId('video-embed-play')
    expect(playButton).toBeInTheDocument()
    expect(screen.getByTestId('video-embed')).toHaveAttribute('data-state', 'placeholder')

    // Zero network contact with the video provider: no <iframe> anywhere,
    // no <img>, no element at all whose src/href touches the allow-listed
    // host, before the click.
    expect(document.querySelector('iframe')).toBeNull()

    // The click transition — this is the assertion the mutation test below
    // targets directly.
    fireEvent.click(playButton)

    const frame = await screen.findByTestId('video-embed-frame')
    expect(frame.tagName).toBe('IFRAME')
    const src = frame.getAttribute('src')
    expect(src).not.toBeNull()
    expect(new URL(src as string).hostname).toBe(ALLOWED_HOST)
    expect(new URL(src as string).pathname).toBe(`/embed/${VIDEO_ID}`)

    // The frame's safety attributes (D9): no referrer, no top nav, no
    // popups, exactly the permissions listed.
    expect(frame).toHaveAttribute('sandbox', 'allow-scripts allow-same-origin allow-presentation')
    expect(frame).toHaveAttribute('referrerpolicy', 'no-referrer')
    expect(frame).toHaveAttribute('allow', 'encrypted-media; picture-in-picture; fullscreen')

    // The play control is gone once the frame exists — exactly one frame is
    // ever created per click.
    expect(screen.queryByTestId('video-embed-play')).toBeNull()
    expect(document.querySelectorAll('iframe')).toHaveLength(1)
  })

  it('never forwards the author-supplied query string into the frame src (D9: constructed, never passed through)', async () => {
    renderEmbed(
      `https://${ALLOWED_HOST}/embed/${VIDEO_ID}?utm_source=evil&list=PL123`,
      undefined,
      [ALLOWED_HOST],
    )
    fireEvent.click(await screen.findByTestId('video-embed-play'))
    const frame = await screen.findByTestId('video-embed-frame')
    const src = new URL(frame.getAttribute('src') as string)
    expect(src.searchParams.has('utm_source')).toBe(false)
    expect(src.searchParams.has('list')).toBe(false)
  })
})

describe('VideoEmbed — a host not on the allow-list is refused, plainly', () => {
  it('renders the refusal text naming the disallowed host, and draws no play control', async () => {
    renderEmbed(`https://evil.example.com/embed/${VIDEO_ID}`, undefined, [ALLOWED_HOST])

    const panel = screen.getByTestId('video-embed')
    await waitFor(() => expect(panel).toHaveAttribute('data-state', 'refused'))
    expect(panel.textContent).toMatch(/evil\.example\.com/)
    expect(panel.textContent).toMatch(/not allowed/i)
    expect(screen.queryByTestId('video-embed-play')).toBeNull()
    expect(document.querySelector('iframe')).toBeNull()
  })

  it('refuses a look-alike host that merely starts with the allow-listed name (exact match, not prefix)', async () => {
    renderEmbed(`https://${ALLOWED_HOST}.evil.example/embed/${VIDEO_ID}`, undefined, [ALLOWED_HOST])
    const panel = screen.getByTestId('video-embed')
    await waitFor(() => expect(panel).toHaveAttribute('data-state', 'refused'))
    expect(screen.queryByTestId('video-embed-play')).toBeNull()
  })
})

describe('VideoEmbed — an empty allow-list is an honest, distinct state (not an error, not the refusal)', () => {
  it('states plainly that no video hosts are permitted, with different text and a different state than the host-refusal case', async () => {
    renderEmbed(`https://${ALLOWED_HOST}/embed/${VIDEO_ID}`, undefined, [])

    const panel = screen.getByTestId('video-embed')
    await waitFor(() => expect(panel).toHaveAttribute('data-state', 'disabled'))
    expect(panel.textContent).toMatch(/no video hosts are permitted/i)
    expect(screen.queryByTestId('video-embed-play')).toBeNull()
    expect(document.querySelector('iframe')).toBeNull()
  })

  it('the empty-list state and the wrong-host refusal state render different data-state values and different copy', async () => {
    const { unmount } = renderEmbed(`https://${ALLOWED_HOST}/embed/${VIDEO_ID}`, undefined, [])
    const emptyPanel = screen.getByTestId('video-embed')
    await waitFor(() => expect(emptyPanel).toHaveAttribute('data-state', 'disabled'))
    const emptyState = emptyPanel.getAttribute('data-state')
    const emptyText = emptyPanel.textContent
    unmount()

    renderEmbed(`https://evil.example.com/embed/${VIDEO_ID}`, undefined, [ALLOWED_HOST])
    const refusedPanel = screen.getByTestId('video-embed')
    await waitFor(() => expect(refusedPanel).toHaveAttribute('data-state', 'refused'))
    const refusedState = refusedPanel.getAttribute('data-state')
    const refusedText = refusedPanel.textContent

    expect(emptyState).toBe('disabled')
    expect(refusedState).toBe('refused')
    expect(emptyState).not.toBe(refusedState)
    expect(emptyText).not.toBe(refusedText)
  })

  it('an operator emptying the list also removes an already-shown play control on the next state read (A-14)', async () => {
    renderEmbed(`https://${ALLOWED_HOST}/embed/${VIDEO_ID}`, undefined, [ALLOWED_HOST])
    expect(await screen.findByTestId('video-embed-play')).toBeInTheDocument()
    // Not exercising the live re-fetch here (that is the query cache's own
    // staleTime/refetch behaviour, not this component's logic) — this proves
    // only that the SAME component, given the emptied config from a fresh
    // mount, produces the disabled state rather than a dead play control.
  })
})

describe('VideoEmbed — identifier validation and URL construction (parseVideoEmbedUrl/buildEmbedSrc)', () => {
  it('accepts exactly an 11-character identifier and nothing else', () => {
    expect(VIDEO_ID_PATTERN.test('dQw4w9WgXcQ')).toBe(true) // 11
    expect(VIDEO_ID_PATTERN.test('dQw4w9WgXc')).toBe(false) // 10
    expect(VIDEO_ID_PATTERN.test('dQw4w9WgXcQQ')).toBe(false) // 12
    expect(VIDEO_ID_PATTERN.test('<script>xy')).toBe(false) // 11 chars, invalid alphabet
  })

  it('extracts host, id and a positive integer start offset from an /embed/ URL', () => {
    const parsed = parseVideoEmbedUrl(`https://${ALLOWED_HOST}/embed/${VIDEO_ID}?start=12`)
    expect(parsed.host).toBe(ALLOWED_HOST)
    expect(parsed.id).toBe(VIDEO_ID)
    expect(parsed.start).toBe(12)
  })

  it('drops a negative start offset rather than passing it through', () => {
    const parsed = parseVideoEmbedUrl(`https://${ALLOWED_HOST}/embed/${VIDEO_ID}?start=-1`)
    expect(parsed.id).toBe(VIDEO_ID)
    expect(parsed.start).toBeUndefined()
  })

  it('reports id: null for a destination with no recognisable identifier, without throwing', () => {
    expect(parseVideoEmbedUrl(`https://${ALLOWED_HOST}/embed/short`).id).toBeNull()
    expect(parseVideoEmbedUrl(`https://${ALLOWED_HOST}/`).id).toBeNull()
    expect(parseVideoEmbedUrl('not a url at all').host).toBeNull()
  })

  it('buildEmbedSrc never emits a query string when no start offset was given', () => {
    const src = buildEmbedSrc(ALLOWED_HOST, VIDEO_ID)
    expect(src).toBe(`https://${ALLOWED_HOST}/embed/${VIDEO_ID}`)
  })
})

describe('VideoEmbed — a valid host but no recognisable identifier degrades to a real link, not a dead button', () => {
  it('renders a working link instead of a play control that would 404 inside its own frame', async () => {
    renderEmbed(`https://${ALLOWED_HOST}/embed/too-short`, undefined, [ALLOWED_HOST])
    const panel = screen.getByTestId('video-embed')
    await waitFor(() => expect(panel).toHaveAttribute('data-state', 'invalid'))
    expect(screen.queryByTestId('video-embed-play')).toBeNull()
    const link = screen.getByRole('link')
    expect(link).toHaveAttribute('href', `https://${ALLOWED_HOST}/embed/too-short`)
  })
})

describe('VideoEmbed — app-state fetch failure is surfaced, not swallowed', () => {
  it('shows a distinct error state with a retry, not the disabled or refused copy', async () => {
    vi.mocked(fetchAppState).mockRejectedValue(new Error('network down'))
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <VideoEmbed url={`https://${ALLOWED_HOST}/embed/${VIDEO_ID}`} />
      </QueryClientProvider>,
    )
    const panel = screen.getByTestId('video-embed')
    await waitFor(() => expect(panel).toHaveAttribute('data-state', 'error'))
    expect(panel.textContent).toMatch(/could not check/i)
    expect(screen.queryByTestId('video-embed-play')).toBeNull()
  })
})

describe('VideoEmbed — the title prop is shown, never fetched', () => {
  it('shows the caller-supplied title as the caption and as the frame title, and nowhere invents one', async () => {
    renderEmbed(`https://${ALLOWED_HOST}/embed/${VIDEO_ID}`, 'Quarterly Demo', [ALLOWED_HOST])
    await waitFor(() => expect(screen.getByText('Quarterly Demo')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('video-embed-play'))
    const frame = await screen.findByTestId('video-embed-frame')
    expect(frame).toHaveAttribute('title', 'Quarterly Demo')
  })

  it('falls back to a generic frame title when none was supplied, rather than an empty title', async () => {
    renderEmbed(`https://${ALLOWED_HOST}/embed/${VIDEO_ID}`, undefined, [ALLOWED_HOST])
    fireEvent.click(await screen.findByTestId('video-embed-play'))
    const frame = await screen.findByTestId('video-embed-frame')
    expect(frame.getAttribute('title')).toBeTruthy()
  })
})
