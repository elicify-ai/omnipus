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
