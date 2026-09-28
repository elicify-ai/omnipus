/**
 * MessageItem.error-detail.test.tsx — ADR-051 disclosure parity for the
 * LIVE message renderer (src/components/chat/MessageItem.tsx).
 *
 * Verbose off  → disclosure is ABSENT from the DOM (not just hidden).
 * Verbose on   → disclosure is visible with the detail string.
 * Error without typed errorCode → disclosure never mounts (parity with
 *                               legacy error frames).
 */

import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { type ReactElement, act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MessageItem } from './MessageItem'
import { useChatStore, type ChatMessage } from '@/store/chat'
import { useChatPreferencesStore } from '@/store/chatPreferences'

function makeQueryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderWithQuery(ui: ReactElement) {
  return render(
    <QueryClientProvider client={makeQueryClient()}>{ui}</QueryClientProvider>
  )
}

const makeMsg = (overrides: Partial<ChatMessage>): ChatMessage => ({
  id: 'msg_err',
  session_id: 'sess_err',
  role: 'assistant',
  content: 'Something failed.',
  timestamp: '2026-03-29T10:00:00Z',
  status: 'error',
  ...overrides,
} as ChatMessage)

beforeEach(() => {
  act(() => {
    useChatStore.setState({ toolCalls: {} })
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
  })
})

afterEach(() => {
  act(() => {
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
  })
})

describe('MessageItem — ADR-051 "Technical details" disclosure (live render)', () => {
  it('does NOT mount the disclosure when verboseChatEnabled is false', () => {
    renderWithQuery(
      <MessageItem
        message={makeMsg({
          errorCode: 'provider_rejected',
          errorDetail: 'provider returned 400',
        })}
      />,
    )
    expect(screen.queryByTestId('error-detail-disclosure')).toBeNull()
    // And the detail text never reaches the DOM (not just visually hidden).
    expect(screen.queryByText('provider returned 400')).toBeNull()
    expect(screen.queryByText('Technical details')).toBeNull()
  })

  it('mounts the disclosure with the detail content when verboseChatEnabled is true', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    renderWithQuery(
      <MessageItem
        message={makeMsg({
          errorCode: 'provider_rejected',
          errorDetail: 'provider returned 400: bad_request',
        })}
      />,
    )
    const disclosure = screen.getByTestId('error-detail-disclosure')
    expect(disclosure).toBeInTheDocument()
    expect(screen.getByText('Technical details')).toBeInTheDocument()
    expect(disclosure.textContent).toContain('provider returned 400: bad_request')
  })

  it('does NOT mount the disclosure when the message is in error but has no typed errorCode (legacy error)', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    renderWithQuery(
      <MessageItem
        message={makeMsg({ errorCode: undefined, errorDetail: undefined })}
      />,
    )
    expect(screen.queryByTestId('error-detail-disclosure')).toBeNull()
  })

  it('does NOT mount the disclosure when verbose is on but errorDetail is empty', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    renderWithQuery(
      <MessageItem
        message={makeMsg({ errorCode: 'network', errorDetail: '' })}
      />,
    )
    expect(screen.queryByTestId('error-detail-disclosure')).toBeNull()
  })

  it('does NOT mount the disclosure on a non-error (e.g. done) assistant message', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    renderWithQuery(
      <MessageItem
        message={makeMsg({
          status: 'done',
          errorCode: 'network',
          errorDetail: 'should not show on a completed message',
        })}
      />,
    )
    expect(screen.queryByTestId('error-detail-disclosure')).toBeNull()
  })

  it('caps the rendered detail at 512 chars', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    const longDetail = 'x'.repeat(1000)
    renderWithQuery(
      <MessageItem
        message={makeMsg({ errorCode: 'network', errorDetail: longDetail })}
      />,
    )
    const disclosure = screen.getByTestId('error-detail-disclosure')
    // The <pre> renders exactly 512 chars of the 1000-char input.
    const pre = disclosure.querySelector('pre')
    expect(pre).toBeTruthy()
    expect(pre!.textContent!.length).toBe(512)
  })
})

// ═══════════════════════════════════════════════════════════════════════════
// provider-messages spec RED tests — TDD row 18.
// Traces: DG-4, DG-5, DG-6, DG-1; C-20, C-21; MAJ-018; D1 (no fetch).
//
// Oracles from the SPEC ONLY:
//   - DG-5/OBS-102: detail renders as a plain inert text node today; the
//     node is pinned byte-identical for non-JSON detail — the ONLY new
//     rendering behaviour is the JSON pretty-print.
//   - C-21: detail pretty-prints ONLY when it parses as JSON
//     (WrapHTMLResponseError means detail is not always JSON).
//   - DG-4/C-20: under Verbose chat, facts lines render (provider, model,
//     request id — request_id only under Verbose). Facts arrive on the
//     error frame's payload.llm_error.facts; the ChatMessage fixture field
//     `errorFacts` is GREEN's wiring target (store seam GREEN names).
//   - DG-1: Verbose off → no disclosure, no facts, nothing fetched.
//   - DG-6: replay carries no detail/facts → no disclosure.
//   - D1: no fetch path exists anywhere in this feature.
//
// RED status: C-21 (pretty-print) and DG-4 (facts) are assertion-red — the
// behaviours do not exist. DG-5/DG-1/DG-6 are characterization pins (spec
// keeps today's behaviour); the no-fetch probe is the instrument for D1.
// ═══════════════════════════════════════════════════════════════════════════
describe('MessageItem — provider-messages row 18 (DG-4/DG-5/DG-6/DG-1, C-20/C-21, D1)', () => {
  it('DG-5/OBS-102: non-JSON detail renders as ONE inert text node, byte-identical', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    const detail = 'provider returned 502 from edge: <img src=x onerror=alert(1)>'
    renderWithQuery(
      <MessageItem
        message={makeMsg({ errorCode: 'provider_rejected', errorDetail: detail })}
      />,
    )
    const disclosure = screen.getByTestId('error-detail-disclosure')
    const pre = disclosure.querySelector('pre')
    expect(pre).toBeTruthy()
    // Byte-identical inert text: no pretty-print, no re-render, no HTML
    // interpretation — and the node is a single text child.
    expect(pre!.textContent).toBe(detail)
    expect(pre!.childNodes.length).toBe(1)
    expect(pre!.childNodes[0].nodeType).toBe(Node.TEXT_NODE)
    // No <img>/<script> element materialized from the detail text.
    expect(disclosure.querySelector('img, script, iframe')).toBeNull()
  })

  it('C-21: JSON detail pretty-prints inside the disclosure (the only new rendering)', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    const detail = '{"b":1,"a":[1,2]}'
    renderWithQuery(
      <MessageItem
        message={makeMsg({ errorCode: 'provider_rejected', errorDetail: detail })}
      />,
    )
    const disclosure = screen.getByTestId('error-detail-disclosure')
    const pre = disclosure.querySelector('pre')
    expect(pre).toBeTruthy()
    // Canonical pretty-print: parsed and re-stringified with 2-space indent
    // (the JSON.stringify default — the indent choice is noted; the spec
    // pins "pretty-printed", not the indent).
    expect(pre!.textContent).toBe(JSON.stringify(JSON.parse(detail), null, 2))
    expect(pre!.textContent).toContain('\n  "b": 1,')
  })

  it('C-21: non-JSON HTML detail stays byte-identical (pretty-print ONLY when it parses as JSON)', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    const detail = '<html><body>oops</body></html>'
    renderWithQuery(
      <MessageItem
        message={makeMsg({ errorCode: 'provider_rejected', errorDetail: detail })}
      />,
    )
    const disclosure = screen.getByTestId('error-detail-disclosure')
    const pre = disclosure.querySelector('pre')
    expect(pre!.textContent).toBe(detail)
  })

  it('DG-4/C-20: Verbose on → facts lines render provider, model and request id', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    renderWithQuery(
      <MessageItem
        message={makeMsg({
          errorCode: 'quota_billing',
          errorDetail: 'status=402 body={"error":{"message":"insufficient credits"}}',
          errorFacts: { provider: 'OpenRouter', model: 'model-b', request_id: 'req-pm-9' },
        } as Partial<ChatMessage> as ChatMessage)}
      />,
    )
    expect(screen.getByText(/OpenRouter/)).toBeInTheDocument()
    expect(screen.getByText(/model-b/)).toBeInTheDocument()
    expect(screen.getByText(/req-pm-9/)).toBeInTheDocument()
  })

  it('DG-1: Verbose off → no facts and no disclosure (D1: nothing to fetch — the no-fetch rule is a structural property of the feature, verified by code inspection in CHECK, not a render-time spy: unrelated react-query fetches fire during any render)', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: false })
    })
    renderWithQuery(
      <MessageItem
        message={makeMsg({
          errorCode: 'quota_billing',
          errorDetail: 'status=402 body=...',
          errorFacts: { provider: 'OpenRouter', model: 'model-b', request_id: 'req-pm-9' },
        } as Partial<ChatMessage> as ChatMessage)}
      />,
    )
    expect(screen.queryByTestId('error-detail-disclosure')).toBeNull()
    expect(screen.queryByText(/req-pm-9/)).toBeNull()
  })

  it('DG-6: replay-shaped error (no detail, no facts) mounts no disclosure even with Verbose on', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    renderWithQuery(
      <MessageItem
        message={makeMsg({ errorCode: 'quota_billing', errorDetail: undefined })}
      />,
    )
    expect(screen.queryByTestId('error-detail-disclosure')).toBeNull()
    expect(screen.queryByText(/req-pm-9/)).toBeNull()
  })
})
