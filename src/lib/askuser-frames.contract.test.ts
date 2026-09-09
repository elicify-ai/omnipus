// askuser-frames.contract.test.ts — askuserquestion-tool-spec v3 test 8
// (SPA zod half): the ask_user_question / ask_user_answer frames validate
// through the generated zod schemas and the WsFrame discriminated union
// (the SPA edge validator ws.ts drives), ask_user_answer is a registered
// client→server type, and session_state's pending_asks snapshot parses.

import { describe, it, expect } from 'vitest'
import {
  AskUserQuestionFrame,
  AskUserAnswerFrame,
  SessionStateFrame,
  WsFrame,
  WsFrameType,
} from '@/lib/api/generated/schemas'
import { ClientFrameTypes } from '@/lib/api/generated/asyncapi-types'

const validCard = {
  card_id: 'ask_1',
  session_id: 's1',
  agent_id: 'mia',
  status: 'pending',
  created_at: '2026-09-06T12:00:00Z',
  default_safe_at: '2026-09-06T12:30:00Z',
  auto_resolved: ['Sending'],
  questions: [
    {
      header: 'Scope',
      question: 'Which emails should this goal cover?',
      options: [
        { label: 'Only unanswered', description: 'The 14 currently waiting.' },
        { label: 'All customer email' },
      ],
      recommended: 'Only unanswered',
      context: '- every question answered\n- one concrete next step',
    },
    {
      header: 'Sending',
      question: 'Draft or send directly?',
      options: [{ label: 'Draft only' }, { label: 'Send directly' }],
      recommended: 'Draft only',
      default_safe: true,
      multi_select: true,
    },
  ],
}

describe('ask_user_question frame (server → client)', () => {
  it('is a WsFrameType member and validates through the union', () => {
    expect(WsFrameType.options).toContain('ask_user_question')
    const frame = { type: 'ask_user_question', card: validCard }
    expect(AskUserQuestionFrame.safeParse(frame).success).toBe(true)
    const viaUnion = WsFrame.safeParse(frame)
    expect(viaUnion.success).toBe(true)
  })

  it('accepts a terminal card carrying the answers record', () => {
    const frame = {
      type: 'ask_user_question',
      card: {
        ...validCard,
        status: 'answered',
        answers: [
          {
            header: 'Scope',
            question: 'Which emails should this goal cover?',
            selected: ['Only unanswered'],
            auto_default: false,
          },
          {
            header: 'Sending',
            question: 'Draft or send directly?',
            free_text: 'my own plan',
            auto_default: true,
          },
        ],
      },
    }
    expect(WsFrame.safeParse(frame).success).toBe(true)
  })

  it('rejects a card with zero questions / a bogus status', () => {
    expect(
      AskUserQuestionFrame.safeParse({ type: 'ask_user_question', card: { ...validCard, questions: [] } })
        .success,
    ).toBe(false)
    expect(
      AskUserQuestionFrame.safeParse({ type: 'ask_user_question', card: { ...validCard, status: 'weird' } })
        .success,
    ).toBe(false)
  })

  it('rejects unknown keys (strict schemas — the drop-at-the-edge contract)', () => {
    const frame = { type: 'ask_user_question', card: validCard, extra: true }
    expect(AskUserQuestionFrame.safeParse(frame).success).toBe(false)
  })
})

describe('ask_user_answer frame (client → server)', () => {
  it('is a registered client→server type', () => {
    expect(WsFrameType.options).toContain('ask_user_answer')
    expect(ClientFrameTypes).toContain('ask_user_answer')
  })

  it('validates a submission and a cancel', () => {
    expect(
      AskUserAnswerFrame.safeParse({
        type: 'ask_user_answer',
        card_id: 'ask_1',
        session_id: 's1',
        answers: [
          { header: 'Scope', selected: ['Only unanswered'] },
          { header: 'Sending', free_text: 'something else' },
          { header: 'Extra', selected: ['Draft only'], auto_default: true },
        ],
      }).success,
    ).toBe(true)
    expect(
      AskUserAnswerFrame.safeParse({
        type: 'ask_user_answer',
        card_id: 'ask_1',
        session_id: 's1',
        cancel: true,
      }).success,
    ).toBe(true)
  })

  it('rejects a frame without card_id/session_id', () => {
    expect(AskUserAnswerFrame.safeParse({ type: 'ask_user_answer', card_id: 'ask_1' }).success).toBe(false)
    expect(AskUserAnswerFrame.safeParse({ type: 'ask_user_answer', session_id: 's1' }).success).toBe(false)
  })
})

describe('session_state pending_asks snapshot', () => {
  it('validates with and without pending_asks (older-gateway compat)', () => {
    const base = {
      type: 'session_state',
      user_id: 'daniel',
      pending_approvals: [],
      emitted_at: '2026-09-06T12:00:00Z',
    }
    expect(SessionStateFrame.safeParse(base).success).toBe(true)
    expect(SessionStateFrame.safeParse({ ...base, pending_asks: [validCard] }).success).toBe(true)
    expect(SessionStateFrame.safeParse({ ...base, pending_asks: [{ bogus: true }] }).success).toBe(false)
  })
})
