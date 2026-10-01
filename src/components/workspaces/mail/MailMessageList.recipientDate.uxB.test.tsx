import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { MailMessageSummary } from '@/lib/api'
import { MailMessageList } from './MailMessageList'

const base: MailMessageSummary = {
  message_id: '<mail@example.test>', uid: 7, uidvalidity: 42,
  folder: 'inbox', subject: 'Planning', from: 'mia@example.test', from_name: 'Mia',
  to: ['mia@example.test'], cc: [], date: '2026-09-28T10:00:00Z',
  seen: true, is_draft: false, is_omnipus_draft: false, read_by_agent: false,
}

function renderMessage(next: Partial<MailMessageSummary>) {
  return render(<MailMessageList messages={[{ ...base, ...next }]} selectedRef={null} onSelect={vi.fn()} />)
}

afterEach(() => cleanup())

describe('F8 — outgoing Mail rows and dates', () => {
  it('labels every Sent recipient with To and does not display the sender, while Inbox still displays From', () => {
    renderMessage({ folder: 'sent', to: ['alice@example.test', 'bob@example.test'] })
    const sent = screen.getByRole('button', { name: /Planning/ })
    expect(sent).toHaveTextContent('To: alice@example.test, bob@example.test')
    expect(sent).not.toHaveTextContent('mia@example.test')
    cleanup()

    renderMessage({ folder: 'inbox', from: 'ada@example.test' })
    const inbox = screen.getByRole('button', { name: /Planning/ })
    expect(inbox).toHaveTextContent('ada@example.test')
    expect(inbox).not.toHaveTextContent('To:')
  })

  it('labels a Draft recipient and omits the server zero-date 0001-01-01', () => {
    renderMessage({ folder: 'drafts', to: ['reviewer@example.test'], date: '0001-01-01T00:00:00Z' })
    const draft = screen.getByRole('button', { name: /Planning/ })
    expect(draft).toHaveTextContent('To: reviewer@example.test')
    expect(draft).not.toHaveTextContent('mia@example.test')
    expect(draft).not.toHaveTextContent('1 Jan 1')
  })

  it('omits the zero date even in a normal Inbox row', () => {
    renderMessage({ folder: 'inbox', from: 'ada@example.test', date: '0001-01-01T00:00:00Z' })
    const inbox = screen.getByRole('button', { name: /Planning/ })
    expect(inbox).toHaveTextContent('ada@example.test')
    expect(inbox).not.toHaveTextContent('1 Jan 1')
  })

  it('shows an unaddressed Draft explicitly and omits malformed dates rather than showing the sender', () => {
    renderMessage({ folder: 'drafts', to: [], date: 'not-a-date' })
    const draft = screen.getByRole('button', { name: /Planning/ })
    expect(draft).toHaveTextContent('To: No recipient')
    expect(draft).not.toHaveTextContent('mia@example.test')
    expect(draft).not.toHaveTextContent('Invalid Date')
  })
})
