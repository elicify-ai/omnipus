/**
 * RED — signature preview via the minted token URL. Oracle: architect
 * decision (coordination/logs/email-arch-sigcsp.log, final result — option B)
 * + the contract route POST /api/v1/mail/signature-preview-token + the email
 * spec's MC-10 normative iframe block (§5.3):
 *
 *   - the SignaturePreviewFrame src is a SAME-ORIGIN `/mail-preview/html/<token>`
 *     URL, where <token> comes from the signature mint (the frame renders
 *     gateway-sanitized bytes under the MC-10 header set — never raw editor
 *     HTML);
 *   - NEVER blob:, NEVER data:, NEVER srcdoc (blob: is CSP-blanked in
 *     production — measured, session experiment 2026-09-28; srcdoc is
 *     spec-banned; this blob-based implementation is the defect the mint
 *     decision replaces);
 *   - the sandbox token set is EXACTLY what
 *     EmailMailboxPanel.signature.test.tsx already pins (this pack must not
 *     weaken that test — it adds the mint contract on top);
 *   - the mint is DEBOUNCED per edit (architect: "one extra round-trip per
 *     debounced edit"), not per keystroke;
 *   - a 429 from the mint (MC-44 limiter) has an EXPLICIT VISIBLE state: a
 *     stale-preview notice + Retry — never a silent stale frame
 *     ("never a blank panel or a silent degrade" posture).
 *
 * The api function name `mintMailSignaturePreviewToken` follows the existing
 * `mintMailHtmlPreviewToken` wrapper/operationId precedent for
 * /mail/html-preview-token; the contract edit itself is backend-lead's
 * pending 5-step (architect action item 1) — flagged in the RED report.
 * No spec number pins a debounce duration or a token TTL, so none is
 * asserted here.
 */
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const { mintMailSignaturePreviewToken } = vi.hoisted(() => ({
  mintMailSignaturePreviewToken: vi.fn(),
}))

vi.mock('@/store/ui', () => ({ useUiStore: vi.fn() }))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchChannels: vi.fn(),
    fetchMailboxes: vi.fn(),
    saveAgentMailbox: vi.fn(),
    deleteAgentMailbox: vi.fn(),
    fetchAgents: vi.fn(),
    fetchWorkspace: vi.fn(),
    fetchWorkspaces: vi.fn(),
    fetchChannelRouting: vi.fn(),
    isApiError: vi.fn(() => false),
  }
})

vi.mock('@/lib/api/mail', () => ({ mintMailSignaturePreviewToken }))

vi.mock('framer-motion', () => ({
  motion: new Proxy({}, {
    get: (_: object, prop: string) =>
      ({ children, ...props }: Record<string, unknown>) =>
        React.createElement(prop as string, props, children as React.ReactNode),
  }),
  AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { useUiStore } from '@/store/ui'
import { fetchWorkspace, saveAgentMailbox, ApiError } from '@/lib/api'
import { EmailMailboxPanel } from './EmailMailboxPanel'

// MailHtmlPreviewTokenResponse token hygiene (MC-43): 43-char base64url.
const TOKEN = 'kZ8vQ2mR7xT1yB4nW6cA9pL0sD3fG5hJ8kM2nP4qR6t'

function renderPanel() {
  vi.mocked(useUiStore).mockReturnValue({ addToast: vi.fn() } as never)
  vi.mocked(fetchWorkspace).mockResolvedValue({
    id: 'ws-1', name: 'My Workspace', status: 'active', core_team: ['mia'],
  } as never)
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  client.setQueryData(['agents'], [{ id: 'mia', name: 'Mia', type: 'core', locked: true }])
  client.setQueryData(['workspaces'], [{ id: 'ws-1', name: 'My Workspace', status: 'active', pinned: false, pin_order: 0 }])
  return render(
    <QueryClientProvider client={client}>
      <EmailMailboxPanel open mailbox={null} mailboxes={[]} onOpenChange={vi.fn()} />
    </QueryClientProvider>,
  )
}

function signatureField(): HTMLTextAreaElement {
  return screen.getByRole('textbox', { name: /signature/i }) as HTMLTextAreaElement
}

async function frame(): Promise<HTMLIFrameElement> {
  return (await screen.findByTitle(/signature preview/i, {}, { timeout: 10_000 })) as HTMLIFrameElement
}

/** Generously past any sane debounce window (no spec number pins one). */
const DEBOUNCE_CEILING_MS = 2_000

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(saveAgentMailbox).mockResolvedValue({} as never)
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('EmailMailboxPanel signature preview — minted token URL (MC-10 §5.3; architect decision 2026-09-28)', () => {
  it('renders the preview frame from a same-origin /mail-preview/html/<token> URL obtained from the mint — never blob:, never data: (MC-10; architect option B)', async () => {
    mintMailSignaturePreviewToken.mockResolvedValue({ token: TOKEN, expires_in_seconds: 120 })
    renderPanel()
    fireEvent.change(signatureField(), { target: { value: '<b>hello</b>' } })
    await act(async () => { await vi.advanceTimersByTimeAsync(DEBOUNCE_CEILING_MS) })

    expect(mintMailSignaturePreviewToken).toHaveBeenCalled()
    // findBy* polls on a real-timer interval that never fires under
    // vi.useFakeTimers — return to real time before polling for the frame
    // (measured: without this the poll hung the full 120 s test timeout).
    vi.useRealTimers()
    const f = await frame()
    const src = f.getAttribute('src') ?? ''
    expect(src).toContain(`/mail-preview/html/${TOKEN}`)
    expect(src.startsWith('blob:')).toBe(false)
    expect(src.startsWith('data:')).toBe(false)
    // Same-origin only: either a relative /mail-preview/... URL or an
    // absolute URL carrying THIS document's origin — a foreign frame host
    // would defeat the MC-10 CSP confinement.
    if (src.startsWith('http')) {
      expect(src.startsWith(window.location.origin)).toBe(true)
    }
    // The mint rode the edited signature text.
    expect(mintMailSignaturePreviewToken).toHaveBeenCalledWith(
      expect.objectContaining({ signature_html: '<b>hello</b>' }),
    )
  })

  it('keeps the sandboxed-frame posture EXACTLY as the existing signature test pins it (no srcdoc; banned sandbox tokens absent)', async () => {
    mintMailSignaturePreviewToken.mockResolvedValue({ token: TOKEN, expires_in_seconds: 120 })
    renderPanel()
    fireEvent.change(signatureField(), { target: { value: 'sig' } })
    await act(async () => { await vi.advanceTimersByTimeAsync(DEBOUNCE_CEILING_MS) })

    // findBy* polls on a real-timer interval that never fires under
    // vi.useFakeTimers — return to real time before polling for the frame
    // (measured: without this the poll hung the full 120 s test timeout).
    vi.useRealTimers()
    const f = await frame()
    expect(f.tagName).toBe('IFRAME')
    expect(f).toHaveAttribute('sandbox', 'allow-popups allow-popups-to-escape-sandbox')
    expect(f).toHaveAttribute('referrerPolicy', 'no-referrer')
    expect(f.getAttribute('allow')).toBe('')
    expect(f).not.toHaveAttribute('srcdoc')
    const sandbox = f.getAttribute('sandbox') ?? ''
    for (const banned of ['allow-scripts', 'allow-same-origin', 'allow-forms', 'allow-top-navigation', 'allow-downloads', 'allow-modals', 'allow-pointer-lock']) {
      expect(sandbox, banned).not.toContain(banned)
    }
  })

  it('DEBOUNCES the mint per edit: typing within the window does not mint until the burst settles, then mints ONCE with the latest text (architect: "per debounced edit")', async () => {
    mintMailSignaturePreviewToken.mockResolvedValue({ token: TOKEN, expires_in_seconds: 120 })
    renderPanel()
    // Flush any mount-time work and take a baseline (a mount-time mint for
    // the initial value is allowed — the pin is per-EDIT behaviour).
    await act(async () => { await vi.advanceTimersByTimeAsync(DEBOUNCE_CEILING_MS) })
    const baseline = mintMailSignaturePreviewToken.mock.calls.length

    fireEvent.change(signatureField(), { target: { value: 'h' } })
    fireEvent.change(signatureField(), { target: { value: 'he' } })
    fireEvent.change(signatureField(), { target: { value: 'hel' } })
    // No mint DURING the burst — a per-keystroke mint would hammer the
    // MC-44-limited route.
    expect(mintMailSignaturePreviewToken.mock.calls.length).toBe(baseline)

    await act(async () => { await vi.advanceTimersByTimeAsync(DEBOUNCE_CEILING_MS) })
    expect(mintMailSignaturePreviewToken.mock.calls.length).toBe(baseline + 1)
    const lastCall = mintMailSignaturePreviewToken.mock.calls[mintMailSignaturePreviewToken.mock.calls.length - 1]
    expect(lastCall[0]).toEqual(expect.objectContaining({ signature_html: 'hel' }))
  })

  it('on a 429 from the mint the preview state is EXPLICIT: a visible stale-preview notice plus Retry that re-mints — never a silent stale frame', async () => {
    mintMailSignaturePreviewToken.mockRejectedValue(
      new ApiError(429, 'rate limited', { code: 'rate_limited' }),
    )
    renderPanel()
    fireEvent.change(signatureField(), { target: { value: 'sig' } })
    await act(async () => { await vi.advanceTimersByTimeAsync(DEBOUNCE_CEILING_MS) })

    // The stale state is visible and names the preview.
    const notice = screen.getByTestId('signature-preview-stale')
    expect(notice).toBeVisible()
    // Retry is offered…
    const retry = screen.getByRole('button', { name: /retry/i })
    // …and clicking it re-mints: the limiter window passed, so the mint now
    // succeeds and the frame src carries the fresh token.
    mintMailSignaturePreviewToken.mockResolvedValue({ token: TOKEN, expires_in_seconds: 120 })
    fireEvent.click(retry)
    await act(async () => { await vi.advanceTimersByTimeAsync(DEBOUNCE_CEILING_MS) })
    // findBy* polls on a real-timer interval that never fires under
    // vi.useFakeTimers — return to real time before polling for the frame
    // (measured: without this the poll hung the full 120 s test timeout).
    vi.useRealTimers()
    const f = await frame()
    expect(f.getAttribute('src') ?? '').toContain(`/mail-preview/html/${TOKEN}`)
  })
})
