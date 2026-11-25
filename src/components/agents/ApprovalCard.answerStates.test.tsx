// ApprovalCard.answerStates.test.tsx — the "Approval card" row of the spec's "UI
// Screens and States" table (spec line 942), the Error and Success columns, with
// C-24 (the server rules) and C-26. Written in Skills & Tools fix round 4, Part C,
// on the team-lead's ruling: "Install review and Approval card rows: write them
// this round ... They are spec requirements of this feature, and today nothing pins
// those strings." The other cells of that row are pinned elsewhere and not repeated
// here: the buttons and the "Covers <agent> ..." sentence (ApprovalCard.timeChoices,
// ApprovalCard.offeredActions), the partial line "Approved for this request only. The
// time limit was not saved: <reason>." and the timed result line "Allowed for Mia
// until 14:05." (ToolApprovalModal.test), 401/403/404/410 (ToolApprovalModal.test).
//
// What this file pins, each sentence taken from the spec and not from the page:
//   409  the reason the server gave, shown on the card (C-24 "Re-check before any
//        effect": "The skill changed after this request." / "This skill is now set to
//        Deny."; C-24 "Offered actions": "This choice is not available for this
//        request."), and nothing reported as stored;
//   400  "This choice is no longer offered. Choose Approve Once or a time limit.";
//   500  "Could not save the approval. Nothing was changed." with the card still
//        answerable: a second answer is sent for the SAME approval id (C-24 "Replay
//        and expiry": "A retry after a 500 uses the same id while it is still
//        pending");
//   success of a trust approval: "Approved this version of notes-helper.", the card
//        closes; and the control that Approve Once does not say it (C-24 closing
//        line: "Neither Approve Once nor a timed allowance records a trust approval").
//
// Readings, so a reviewer can overrule them in one place:
//  * Where the sentence appears (inside the card or as a toast) is not specified, so
//    each is accepted in either place; a lead-in such as "Failed to submit approval: "
//    is tolerated, the sentence itself is exact.
//  * The 400 sentence is the CARD's own wording. C-24 gives the server no 400 sentence
//    ("today's 400" stays for the always-ask tools, today's body is "<tool> always asks
//    for approval; it can be set to ask or deny, not allow", pkg/tools/always_ask.go::
//    AlwaysAskAllowRefusal), while the spec row quotes this sentence for a 400. So the
//    fake server answers with today's unrelated body and the card must word it. If the
//    architect rules that the server sends the sentence, change the fake body, not the
//    expectation.
//  * The skill's name in the success sentence is `skill.name`; the fixtures give it the
//    same value as `skill.display_name`, because the spec's example does not say which
//    of the two it uses.
// Only fetch is doubled (the shared approval network); the store, the panel, the API
// client and its error mapping are the real ones. GREEN and mutation belong to CHECK.
//
// RED, shown by a run: saved run fix4k-01 (the frontend head f69d7a7b7 plus this file;
// 6 failed | 2 passed; ta/fix4-ts-fix4k-01.log). Each of the six has exactly ONE failing
// assertion, the "shown" one, and its message carries what the page really showed:
//   409 (x3)  the page shows only "Failed to submit approval: This conflicts with the
//             current state. Please refresh and try again." (the generic text that
//             src/lib/api-error.ts gives every 409); the server's reason is lost. The
//             rest of each case passes: the sent action is right, nothing is reported
//             as stored, "Approved this version" is never claimed.
//   400       "Failed to submit approval: unknown action "allow_for": must be allow, ..."
//             (the server's body behind a prefix); the card does not word it itself.
//   500       "Failed to submit approval: The server is unavailable. Please try again in a
//             moment." (the generic 5xx text). The rest of the case passes today: the
//             entry stays queued, nothing is marked resolved, both buttons stay enabled,
//             and the retry posts the SAME approval id and closes the card.
//   Approve this version   nothing at all is shown (no toast, no alert or status text).
//             The body sent is exactly {"action":"approve_version"} and the card closes;
//             both of those pass today.
// The shown() probe test and the Approve Once control pass; they are instrument evidence,
// not product proof.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ToolApprovalRequiredFrame } from '@/lib/api/generated/asyncapi-types'
import { useToolApprovalStore } from '@/store/toolApproval'
import { useUiStore } from '@/store/ui'
import { ToolApprovalPanel } from './ToolApprovalPanel'
import {
  approvalPosts, cleanupApprovalState, installApprovalNetwork,
  renderWithClient, resetSharedState, setApprovalReply, toasts,
} from '../../../tests/l1-ui-red-u23/support'

const SESSION = 'answer-states-session'
const NAME = 'notes-helper'
const R1 = '1111111111111111111111111111111111111111111111111111111111111111'

// A skill load whose permission check is open: Approve Once, the three time choices,
// Deny (C-12 row 2; offered_actions as C-24 "Offered actions" lists them).
const PERMISSION_FRAME: ToolApprovalRequiredFrame = {
  type: 'tool_approval_required', approval_id: 'answer-permission', tool_call_id: 'answer-call-1',
  tool_name: 'Skill', args: { name: NAME }, agent_id: 'agent-main', acting_agent_name: 'Mia',
  session_id: SESSION, turn_id: 'answer-turn-1', expires_in_ms: 300_000,
  offered_actions: ['allow_once', 'allow_for', 'deny'], allow_durations: ['1h', '3h', '1d'],
  skill: { key: NAME, name: NAME, display_name: NAME, revision: R1, unverified: false, open_checks: ['permission'] },
}
// An unverified skill whose trust check is open: "Approve this version" and Deny, no
// time choice (C-12 row 3).
const TRUST_FRAME: ToolApprovalRequiredFrame = {
  ...PERMISSION_FRAME, approval_id: 'answer-trust', tool_call_id: 'answer-call-2', turn_id: 'answer-turn-2',
  offered_actions: ['approve_version', 'deny'], allow_durations: [],
  skill: { key: NAME, name: NAME, display_name: NAME, revision: R1, unverified: true, open_checks: ['trust'] },
}

// What the server answers when the answer was stored: C-24 `applied` reports what was
// stored, never what was asked.
function stored(frame: ToolApprovalRequiredFrame, action: string, trustApproved: boolean) {
  return {
    approval_id: frame.approval_id, action, status: 'ok', grant_recorded: false,
    applied: { call_released: true, timed_allowance: false, trust_approved: trustApproved },
  }
}

// A sentence is "shown" when it is in the page or in a toast: the spec does not say
// which, and the page renders no toast container in this file, so toasts are read from
// the real ui store.
function shown(sentence: string): boolean {
  const inPage = (document.body.textContent ?? '').includes(sentence)
  return inPage || toasts().some((toast) => toast.message.includes(sentence))
}
function successToasts() {
  return toasts().filter((toast) => toast.variant === 'success')
}
// What the person can read right now, for a failure message: the toasts and every alert
// or status region. A RED then names the actual text next to the expected sentence.
function whatIsShown(): string {
  const regions = Array.from(document.querySelectorAll('[role="alert"], [role="status"]'))
    .map((node) => node.textContent ?? '')
  return JSON.stringify({ toasts: toasts().map((toast) => toast.message), regions })
}

function mount(frame: ToolApprovalRequiredFrame) {
  act(() => useToolApprovalStore.getState().enqueue(frame))
  renderWithClient(<ToolApprovalPanel />)
}
async function press(name: string) {
  await act(async () => { fireEvent.click(screen.getByRole('button', { name })) })
}

beforeEach(() => {
  resetSharedState(SESSION)
  installApprovalNetwork()
})
afterEach(cleanupApprovalState)

describe('Approval card — the shown() instrument', () => {
  it('sees a toast, sees an inline sentence, and sees neither when the sentence is absent', () => {
    const toastSentence = 'PROBE-TOAST-SENTENCE'
    const inlineSentence = 'PROBE-INLINE-SENTENCE'
    expect(shown(toastSentence)).toBe(false)
    expect(shown(inlineSentence)).toBe(false)
    act(() => { useUiStore.getState().addToast({ message: `Failed to submit approval: ${toastSentence}`, variant: 'error' }) })
    expect(shown(toastSentence)).toBe(true)
    expect(shown(inlineSentence)).toBe(false)
    render(<p>{inlineSentence}</p>)
    expect(shown(inlineSentence)).toBe(true)
  })
})

describe('Approval card — Error column (spec line 942, C-24)', () => {
  it.each([
    ['The skill changed after this request.', 'Approve Once', 'allow_once'],
    ['This skill is now set to Deny.', 'Approve Once', 'allow_once'],
    ['This choice is not available for this request.', 'Allow for 1 hour', 'allow_for'],
  ] as const)('409 "%s": the person reads that reason, and nothing is reported as stored', async (reason, button, action) => {
    setApprovalReply({ error: reason }, 409)
    mount(PERMISSION_FRAME)

    await press(button)

    await waitFor(() => expect(approvalPosts()).toHaveLength(1))
    expect(approvalPosts()[0], 'the answer that was sent').toMatchObject({ id: PERMISSION_FRAME.approval_id, body: { action } })
    expect.soft(shown(reason), `the server's reason is shown on the card: ${reason}; the page showed ${whatIsShown()}`).toBe(true)
    expect(successToasts(), 'a refused answer reports nothing as stored').toEqual([])
    expect(shown('Approved this version'), 'a refused answer is never reported as approved').toBe(false)
  })

  it('400: the card says "This choice is no longer offered. Choose Approve Once or a time limit."', async () => {
    // Today's server body for a refused action (pkg/gateway/rest_tool_registry.go): not the card's sentence.
    setApprovalReply({ error: 'unknown action "allow_for": must be allow, allow_once, deny, or cancel' }, 400)
    mount(PERMISSION_FRAME)

    await press('Allow for 1 hour')

    await waitFor(() => expect(approvalPosts()).toHaveLength(1))
    expect.soft(
      shown('This choice is no longer offered. Choose Approve Once or a time limit.'),
      `the spec row words a 400 itself; the page showed ${whatIsShown()}`,
    ).toBe(true)
    expect(successToasts(), 'a refused answer reports nothing as stored').toEqual([])
  })

  it('500: the card says "Could not save the approval. Nothing was changed." and stays answerable for the same approval id', async () => {
    setApprovalReply({ error: 'Could not save the approval. Nothing was changed.' }, 500)
    mount(PERMISSION_FRAME)

    await press('Approve Once')

    await waitFor(() => expect(approvalPosts().length).toBeGreaterThanOrEqual(1))
    expect.soft(
      shown('Could not save the approval. Nothing was changed.'),
      `the server sentence is shown; the page showed ${whatIsShown()}`,
    ).toBe(true)
    expect(successToasts(), 'a failed save reports nothing as stored').toEqual([])
    // Answerable: still queued, its buttons usable, and nothing marked as resolved.
    expect(useToolApprovalStore.getState().queue.map((entry) => entry.approvalId)).toEqual([PERMISSION_FRAME.approval_id])
    expect(useToolApprovalStore.getState().resolvedIds).toEqual([])
    await waitFor(() => expect(screen.getByRole('button', { name: 'Approve Once' })).toBeEnabled())
    expect(screen.getByRole('button', { name: 'Deny' })).toBeEnabled()

    // C-24 "Replay and expiry": the retry uses the same id; this time the save works.
    const before = approvalPosts().length
    setApprovalReply(stored(PERMISSION_FRAME, 'allow_once', false), 200)
    await press('Approve Once')
    await waitFor(() => expect(approvalPosts()).toHaveLength(before + 1))
    expect(approvalPosts().at(-1), 'the retry').toEqual({ id: PERMISSION_FRAME.approval_id, body: { action: 'allow_once' } })
    await waitFor(() => expect(useToolApprovalStore.getState().queue).toHaveLength(0))
  })
})

describe('Approval card — Success column (spec line 942, C-24)', () => {
  it('Approve this version: the card closes and the result line says "Approved this version of notes-helper."', async () => {
    setApprovalReply(stored(TRUST_FRAME, 'approve_version', true))
    mount(TRUST_FRAME)

    await press('Approve this version')

    await waitFor(() => expect(approvalPosts()).toHaveLength(1))
    expect(approvalPosts()[0].body, 'the answer that was sent carries the action only (C-24: no skill key, no revision)')
      .toEqual({ action: 'approve_version' })
    await waitFor(() => expect(useToolApprovalStore.getState().queue, 'the card closes').toHaveLength(0))
    expect.soft(
      shown(`Approved this version of ${NAME}.`),
      `the result line says what was stored; the page showed ${whatIsShown()}`,
    ).toBe(true)
  })

  it('control: Approve Once on a permission card does not say a version was approved (it records no trust approval)', async () => {
    setApprovalReply(stored(PERMISSION_FRAME, 'allow_once', false))
    mount(PERMISSION_FRAME)

    await press('Approve Once')

    await waitFor(() => expect(approvalPosts()).toHaveLength(1))
    await waitFor(() => expect(useToolApprovalStore.getState().queue, 'the card closes').toHaveLength(0))
    expect(shown('Approved this version'), 'Approve Once never claims a trust approval').toBe(false)
  })
})
