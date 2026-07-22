// SubagentQuestionMessage.test.tsx — ADR-053 FE-3 / US-7 / D2.

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { SubagentQuestionMessage } from './SubagentQuestionMessage'
import type { SessionMessageQuestion } from '@/lib/api/generated/openapi-types'

function makeQuestion(overrides: Partial<SessionMessageQuestion> = {}): SessionMessageQuestion {
  return {
    message_id: 'sm_1',
    session_id: 's1',
    parent_session_id: null,
    generation: 0,
    direction: 'child_to_parent',
    kind: 'question',
    depth: 0,
    created_at: '2026-07-22T10:00:00Z',
    sender_identity: 'ray',
    untrusted_origin: true,
    text: 'Should I overwrite the existing config.json backup?',
    wait: true,
    correlation_id: 'corr_1',
    ...overrides,
  }
}

describe('SubagentQuestionMessage', () => {
  it('renders the sender identity and question text', () => {
    render(<SubagentQuestionMessage message={makeQuestion()} />)
    expect(screen.getByTestId('subagent-question-message')).toBeInTheDocument()
    expect(screen.getByText('ray')).toBeInTheDocument()
    expect(screen.getByTestId('subagent-question-text')).toHaveTextContent(
      'Should I overwrite the existing config.json backup?',
    )
  })

  it('shows the untrusted-origin tag when untrusted_origin is true', () => {
    render(<SubagentQuestionMessage message={makeQuestion({ untrusted_origin: true })} />)
    expect(screen.getByTestId('subagent-question-untrusted')).toHaveTextContent('untrusted')
  })

  it('hides the untrusted-origin tag when untrusted_origin is false', () => {
    render(<SubagentQuestionMessage message={makeQuestion({ untrusted_origin: false })} />)
    expect(screen.queryByTestId('subagent-question-untrusted')).not.toBeInTheDocument()
  })

  it('shows the in-band reply hint (no reply card, no approval UX)', () => {
    render(<SubagentQuestionMessage message={makeQuestion()} />)
    expect(screen.getByText('Reply in chat to answer')).toBeInTheDocument()
    // No buttons, forms, or approval controls
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('renders different sender identities correctly', () => {
    render(<SubagentQuestionMessage message={makeQuestion({ sender_identity: 'ava' })} />)
    expect(screen.getByText('ava')).toBeInTheDocument()
  })
})
