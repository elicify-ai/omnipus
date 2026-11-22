// ApprovalCard.timeChoices.test.tsx — Skills & Tools redesign (#1963, epic
// #1519, page F), order 62 (S-119 shown-text half; S-125/S-129 card half;
// C-12/C-26 card scope line).
//
// Oracle: skills-tools-redesign-spec.md —
//   S-119: the card offers Approve Once, Allow for 1 hour / 3 hours /
//        1 day, Deny and Cancel, and NO text "Always Allow", "Always
//        allow" or "Allow everywhere" appears on the card (R2-Q1).
//   C-12 row 2 / C-26 card row (spec §932): under the time choices the
//        card shows "Covers <agent> in all its chats, tasks and its own
//        helpers until the time ends or Omnipus restarts. Other agents
//        still ask."
//   S-125/S-129 card half: a card the server offers without allow_for
//        (always-ask tool, exact-action tool) shows NO time choices —
//        what the server computes (offered_actions / allow_durations,
//        C-24) decides the buttons.
//
// The S-126 result line ("Allowed for Mia until 14:05.") is already
// pinned by ToolApprovalModal.test.tsx's C-26 toast tests (orders 21-31
// pack, commit b7526b2e60) — this file does not duplicate them.
//
// RED today: ToolApprovalPanel.tsx still renders the old card — an
// "Always Allow" button with its exact/prefix scope selector, no
// "Allow for" buttons, no covers line, and the store entry drops
// offered_actions/allow_durations (src/store/toolApproval.ts), so the
// offered-actions leg pins the wiring GREEN must add (control — it
// passes while no buttons exist at all and must KEEP passing once the
// buttons are driven by the server's choices).
import { describe, it, expect, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { act } from 'react'
import type { ComponentType } from 'react'

import { useToolApprovalStore } from '@/store/toolApproval'
import {
  renderWithClient as render,
  loadApprovalPanel,
  resetSharedState,
  installApprovalNetwork,
} from '../../../tests/l1-ui-red-u23/support'
import type { PendingToolApproval } from '@/store/toolApproval'

let ToolApprovalPanel: ComponentType

beforeEach(async () => {
  resetSharedState('sess-001')
  installApprovalNetwork()
  ToolApprovalPanel = await loadApprovalPanel()
})

// The plain delete_task card fixture: Mia's card, arguments A. The
// offeredActions/allowDurations members are the C-24 server-computed
// choices the store entry will carry once GREEN wires them through
// (src/store/toolApproval.ts::enqueue); the cast keeps the fixture
// typechecking until then.
function timedCard(): PendingToolApproval {
  return {
    approvalId: 'appr-timed-001',
    toolCallId: 'call-timed-001',
    toolName: 'delete_task',
    args: { task_id: 't1' },
    agentId: 'agent-main',
    sessionId: 'sess-001',
    turnId: 'turn-timed-001',
    expiresAt: Date.now() + 300_000,
    offeredActions: ['allow_once', 'allow_for', 'deny', 'cancel'],
    allowDurations: ['1h', '3h', '1d'],
  } as PendingToolApproval
}

function cardWithoutTimeChoices(): PendingToolApproval {
  return {
    ...timedCard(),
    approvalId: 'appr-ask-001',
    toolCallId: 'call-ask-001',
    toolName: 'set_channel_outbound',
    offeredActions: ['allow_once', 'deny', 'cancel'],
    allowDurations: [],
  } as PendingToolApproval
}

describe('ApprovalCard — C-26 time choices (order 62)', () => {
  it('S-119: the ordinary card offers the time choices and no Always Allow anywhere', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [timedCard()] })
    })
    render(<ToolApprovalPanel />)

    expect(screen.getByRole('button', { name: /Approve Once/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Allow for 1 hour/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Allow for 3 hours/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Allow for 1 day/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Deny/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Cancel/i })).toBeInTheDocument()

    // R2-Q1: the retired wording never appears on the card (both spellings
    // match the case-insensitive pattern).
    expect(screen.queryByText(/Always Allow/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/Allow everywhere/i)).not.toBeInTheDocument()
  })

  it('C-12/C-26: the scope line sits under the time choices', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [timedCard()] })
    })
    render(<ToolApprovalPanel />)

    expect(
      screen.getByText(/^Covers .+ until the time ends or Omnipus restarts\. Other agents still ask\.$/),
    ).toBeInTheDocument()
  })

  it('S-125/S-129 card half: a card offered without allow_for shows no time choices', () => {
    // Control: pins the wiring GREEN adds — the buttons are the server's
    // offered_actions/allow_durations, never a component default. Today it
    // passes because no time buttons render at all; once they do, this leg
    // must KEEP passing for the always-ask and exact-action cards.
    act(() => {
      useToolApprovalStore.setState({ queue: [cardWithoutTimeChoices()] })
    })
    render(<ToolApprovalPanel />)

    expect(screen.queryByRole('button', { name: /Allow for 1 hour/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Allow for 3 hours/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Allow for 1 day/i })).not.toBeInTheDocument()
  })

  it('C-12/C-26: the scope line appears only when time choices are offered', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [cardWithoutTimeChoices()] })
    })
    render(<ToolApprovalPanel />)

    expect(
      screen.queryByText(/^Covers .+ until the time ends or Omnipus restarts\. Other agents still ask\.$/),
    ).not.toBeInTheDocument()
  })
})
