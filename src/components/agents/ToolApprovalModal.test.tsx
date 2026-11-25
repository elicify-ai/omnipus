// L1 amendment §1.2 R6 / §1.5: inline container replaces the modal.
// All non-container preview/action/security oracles retained; B3 stub deletion deferred.
// Unit tests for ToolApprovalPanel — FR-011, FR-082, FR-052
//
// Tests:
//  1. Modal does not render when queue is empty
//  2. Modal renders when queue has an entry
//  3. Approve button calls POST /api/v1/tool-approvals/{id} with action:"allow_once"
//  4. Deny button calls POST with action:"deny"
//  5. Cancel button calls POST with action:"cancel"
//  5b. C-26 timed choice: "Allow for 1 hour" POSTs action:"allow_for" with
//      duration:"1h" (spec Regression row, commit b7526b2e60; R2-Q1 removed
//      action:"allow" from the wire, so there is no Always Allow case anymore)
//  6. On 410 response, modal entry is dismissed without a toast
//  7. On 403 response, shows admin-required toast
//  8. On 401 response, shows re-auth toast
//  9. Countdown uses expires_in_ms — expiresAt computed as Date.now() + expires_in_ms
// 10. session_state reconciliation removes stale approvals

import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import { act } from 'react'
import type { ToolApprovalRequiredFrame } from '@/lib/api/generated/asyncapi-types'

// Real transport, ApiError, UI and logout paths. Only fetch/socket boundaries
// are doubled by the shared test fixture.

import { useToolApprovalStore } from '@/store/toolApproval'
import type { PendingToolApproval } from '@/store/toolApproval'
import { useAuthStore } from '@/store/auth'
import { useSessionStore } from '@/store/session'
import type { ComponentType } from 'react'
import { renderWithClient as render, loadApprovalPanel, resetSharedState, installApprovalNetwork, setApprovalReply, approvalPosts, toasts, cleanupApprovalState } from '../../../tests/l1-ui-red-u23/support'
let ToolApprovalPanel: ComponentType
import type { SessionStateFrame } from '@/lib/ws'


beforeEach(async () => {
  resetSharedState('sess-001')
  installApprovalNetwork()
  ToolApprovalPanel = await loadApprovalPanel()
})

// C-24 / C-26: a card renders exactly the answers the server listed when it
// raised the card (`offered_actions`, `allow_durations`; both are required in the
// wire types, contracts ToolApprovalRequiredFrame). An ordinary tool's card
// lists Approve Once, the time choices, Deny and Cancel; an exact-action tool's
// card (environment_setup, request_mount, send_email, browser_evaluate,
// set_config) lists Approve Once, Deny and Cancel and no time choice. Every
// store-entry fixture below carries the list the server sends for its tool, so no
// test depends on a rule the client would apply when a list is missing.
const ORDINARY_TOOL_ANSWERS: Pick<PendingToolApproval, 'offeredActions' | 'allowDurations'> = {
  offeredActions: ['allow_once', 'allow_for', 'deny', 'cancel'],
  allowDurations: ['1h', '3h', '1d'],
}
const EXACT_ACTION_TOOL_ANSWERS: Pick<PendingToolApproval, 'offeredActions' | 'allowDurations'> = {
  offeredActions: ['allow_once', 'deny', 'cancel'],
  allowDurations: [],
}

const SAMPLE_APPROVAL: PendingToolApproval = {
  approvalId: 'appr-001',
  toolCallId: 'call-001',
  toolName: 'fetch_url',
  args: { url: 'https://example.com' },
  agentId: 'agent-main',
  // K7 acting_agent_name: the frame names the acting agent, so the result
  // line's "<agent>" is a known value in these tests (CHECK round 1 R5–R7).
  actingAgentName: 'Mia',
  sessionId: 'sess-001',
  turnId: 'turn-001',
  expiresAt: Date.now() + 300_000, // 5 minutes
  ...ORDINARY_TOOL_ANSWERS,
}

describe('ToolApprovalPanel — rendering', () => {
  it('L1 §1.2 R6: renders nothing when the queue is empty', () => {
    const { container } = render(<ToolApprovalPanel />)
    expect(container.firstChild).toBeNull()
  })

  it('L1 §1.2 R6: renders the modal when the queue has an entry', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalPanel />)
    // Deliverable 3: the modal shows the HUMAN tool name (humanizeToolName),
    // not the raw wire identifier — 'fetch_url' has an EXPLICIT_LABELS entry
    // ('Fetch URL'), so the raw id should no longer appear as the Tool label.
    expect(screen.getByText('Fetch URL')).toBeInTheDocument()
    expect(screen.queryByText('fetch_url')).not.toBeInTheDocument()
    expect(screen.getByText('Tool Approval Required')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Approve/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Deny/i })).toBeInTheDocument()
    // Regression row (b7526b2e60) + C-26: the card offers a time limit, not
    // Always Allow (R2-Q1 removed it from every card).
    expect(screen.getByRole('button', { name: /1 hour/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Cancel/i })).toBeInTheDocument()
  })

  it('L1 §1.2 R6: shows the tool args in the modal', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalPanel />)
    expect(screen.getByText(/https:\/\/example\.com/)).toBeInTheDocument()
  })

  it('L1 §1.2 R6: shows queue depth indicator when multiple approvals are pending', () => {
    act(() => {
      useToolApprovalStore.setState({
        queue: [
          SAMPLE_APPROVAL,
          { ...SAMPLE_APPROVAL, approvalId: 'appr-002', toolName: 'exec' },
        ],
      })
    })
    render(<ToolApprovalPanel />)
    expect(screen.getByText('+1 more')).toBeInTheDocument()
  })
})

// ADR-036 §3.4: the dedicated exec-only approval flow (ExecApprovalBlock) was
// retired in favor of this generic modal. Its readable command preview
// (binary highlighted, env-prefix separated, working-dir line) was ported
// here for `bash` calls — verify it renders, in addition to the generic
// Arguments JSON dump, and does NOT render for other tools.
describe('ToolApprovalPanel — bash command preview (ADR-036 §3.4 port)', () => {
  const BASH_APPROVAL: PendingToolApproval = {
    approvalId: 'appr-bash-001',
    toolCallId: 'call-bash-001',
    toolName: 'bash',
    args: { command: 'FOO=bar npm run build', cwd: 'apps/web', run_in_background: false },
    agentId: 'agent-main',
    sessionId: 'sess-001',
    turnId: 'turn-001',
    expiresAt: Date.now() + 300_000,
    ...ORDINARY_TOOL_ANSWERS,
  }

  it('L1 §1.2 R6: renders a Command preview with the binary highlighted and env prefix separated', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [BASH_APPROVAL] })
    })
    render(<ToolApprovalPanel />)
    expect(screen.getByText('Command')).toBeInTheDocument()
    expect(screen.getByText('FOO=bar')).toBeInTheDocument()
    expect(screen.getByText('npm')).toBeInTheDocument()
  })

  it('L1 §1.2 R6: renders the working directory from args.cwd', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [BASH_APPROVAL] })
    })
    render(<ToolApprovalPanel />)
    expect(screen.getByText('apps/web')).toBeInTheDocument()
  })

  it('L1 §1.2 R6: still renders the generic Arguments JSON dump alongside the preview', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [BASH_APPROVAL] })
    })
    render(<ToolApprovalPanel />)
    expect(screen.getByText('Arguments')).toBeInTheDocument()
    expect(screen.getByText(/run_in_background/)).toBeInTheDocument()
  })

  it('L1 §1.2 R6: does NOT render a Command preview for a non-bash tool', () => {
    act(() => {
      useToolApprovalStore.setState({
        queue: [{ ...SAMPLE_APPROVAL, args: { command: 'not actually bash' } }],
      })
    })
    render(<ToolApprovalPanel />)
    expect(screen.queryByText('Command')).not.toBeInTheDocument()
  })

  it('L1 §1.2 R6: does NOT render a Command preview when bash args have no command string', () => {
    act(() => {
      useToolApprovalStore.setState({
        queue: [{ ...BASH_APPROVAL, args: { run_in_background: true } }],
      })
    })
    render(<ToolApprovalPanel />)
    expect(screen.queryByText('Command')).not.toBeInTheDocument()
  })
})

describe('ToolApprovalPanel — button dispatch', () => {
  it('L1 §1.2 R6: Approve button calls submitToolApproval with action:"allow_once"', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalPanel />)

    fireEvent.click(screen.getByRole('button', { name: /Approve/i }))

    await waitFor(() => {
      expect(approvalPosts()).toContainEqual({ id: 'appr-001', body: { action: 'allow_once' } })
    })
  })

  it('L1 §1.2 R6: Deny button calls submitToolApproval with action:"deny"', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalPanel />)

    fireEvent.click(screen.getByRole('button', { name: /Deny/i }))

    await waitFor(() => {
      expect(approvalPosts()).toContainEqual({ id: 'appr-001', body: { action: 'deny' } })
    })
  })

  it('L1 §1.2 R6: Cancel button calls submitToolApproval with action:"cancel"', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalPanel />)

    fireEvent.click(screen.getByRole('button', { name: /^Cancel$/i }))

    await waitFor(() => {
      expect(approvalPosts()).toContainEqual({ id: 'appr-001', body: { action: 'cancel' } })
    })
  })

  // Regression row "Always Allow on approval cards" (skills-tools-redesign-spec,
  // Regression Test Requirements, added in commit b7526b2e60): the R6 'Always
  // Allow' cases are rewritten to their C-26 timed-choice equivalents. C-26
  // replaced Always Allow with Approve Once / Allow for 1 hour / 3 hours /
  // 1 day / Deny, and R2-Q1 removed action:"allow" from the wire — so the
  // wiring case now dispatches the timed choice: allow_for with duration.
  it('L1 §1.2 R6 → C-26: the "Allow for 1 hour" choice calls submitToolApproval with action:"allow_for", duration:"1h"', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalPanel />)

    fireEvent.click(screen.getByRole('button', { name: /1 hour/i }))

    await waitFor(() => {
      expect(approvalPosts()).toContainEqual({
        id: 'appr-001',
        body: { action: 'allow_for', duration: '1h' },
      })
    })
  })

  // Regression row (b7526b2e60) + C-26 / spec approval-card row: when the
  // timed choice released this call but the allowance was NOT saved
  // (ToolApprovalResponse.applied.timed_allowance = false with
  // not_applied_reason), the card closes and the toast is the spec's
  // partial-failure text "Approved for this request only. The time limit was
  // not saved: <reason>." — the C-26 replacement of the old Always Allow
  // did-not-stick toast (R2-Q1).
  it('L1 §1.2 R6 → C-26: toasts when the timed choice released this call but the allowance did not stick', async () => {
    setApprovalReply({
      approval_id: 'appr-001',
      action: 'allow_for',
      status: 'ok',
      // The contract records expires_at only when the timed allowance stuck
      // (CHECK round 1, R3 reconciliation): a reply that did NOT save the
      // allowance carries no recorded expiry — supplying one here would
      // teach a wire state the server never sends.
      applied: { call_released: true, timed_allowance: false, trust_approved: false },
      not_applied_reason: 'the approval store is read-only',
    })
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalPanel />)

    fireEvent.click(screen.getByRole('button', { name: /1 hour/i }))

    await waitFor(() => {
      expect(toasts()).toContainEqual({
        message:
          'Approved for this request only. The time limit was not saved: the approval store is read-only.',
        variant: 'warning',
      })
    })
    expect(useToolApprovalStore.getState().queue).toHaveLength(0)
  })

  // Regression row (b7526b2e60) + C-26: a reply that never says the allowance
  // stuck — no ToolApprovalResponse.applied at all — is treated the same as
  // timed_allowance:false: the call was released, the time limit was not
  // saved, and the partial-failure text is shown (spec approval-card row).
  // CHECK round 1 R4: the reason is server-supplied, so the test supplies a
  // fixed one explicitly and asserts the exact sentence — a prefix regex
  // would also pass an absent reason or trailing garbage.
  it('L1 §1.2 R6 → C-26: toasts when the reply omits applied — treated as allowance-did-not-stick', async () => {
    setApprovalReply({
      approval_id: 'appr-001',
      action: 'allow_for',
      status: 'ok',
      // Recorded-only expiry (CHECK R3): no allowance saved, no expires_at.
      not_applied_reason: 'the approval store is read-only',
    })
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalPanel />)

    fireEvent.click(screen.getByRole('button', { name: /1 hour/i }))

    await waitFor(() => {
      expect(toasts()).toEqual([
        {
          message:
            'Approved for this request only. The time limit was not saved: the approval store is read-only.',
          variant: 'warning',
        },
      ])
    })
    expect(useToolApprovalStore.getState().queue).toHaveLength(0)
  })

  // S-126 fixes the clock concretely: a card answered at 13:05 with "Allow
  // for 1 hour" closes with "Allowed for Mia until 14:05.", and "Allow for
  // 1 day" with "Allowed for Mia until tomorrow 13:05." (the day shows when
  // the end is not today). The client formats the server-recorded end, so
  // the test fixes that clock input in the scripted reply instead of
  // leaving the displayed time to Date.now() (CHECK round 1 R5/R6/R7).
  // Known agent: the frame's acting_agent_name names Mia (store field
  // actingAgentName, K7).
  function endsTodayAtLocal(hour: number, minute: number): string {
    const end = new Date()
    end.setHours(hour, minute, 0, 0)
    return end.toISOString()
  }
  function endsTomorrowAtLocal(hour: number, minute: number): string {
    const end = new Date()
    end.setDate(end.getDate() + 1)
    end.setHours(hour, minute, 0, 0)
    return end.toISOString()
  }

  // Regression row (b7526b2e60) + C-26 / S-126: after a timed choice sticks
  // (ToolApprovalResponse.applied.timed_allowance = true) the card closes and
  // the toast is the spec's result line "Allowed for <agent> until <time>.",
  // with the server-recorded scope (ToolApprovalResponse.scope) carried on the
  // reply. The old "Always allowed — … for this session." toasts are removed
  // by R2-Q1, so their text must never reappear. The displayed time and the
  // agent display name are both pinned by the fixtures below (the scripted
  // reply's recorded end; the frame's acting_agent_name), so the exact spec
  // sentences are asserted (CHECK round 1 R5/R6).
  it('L1 §1.2 R6 → C-26: closes with the result line naming the recorded timed allowance when the server recorded scope "prefix"', async () => {
    setApprovalReply({
      approval_id: 'appr-shell-001',
      action: 'allow_for',
      status: 'ok',
      // S-126's fixed clock: the 1-hour choice answered at 13:05 records
      // 14:05 today.
      expires_at: endsTodayAtLocal(14, 5),
      applied: { call_released: true, timed_allowance: true, trust_approved: false },
      scope: 'prefix',
    })
    act(() => {
      useToolApprovalStore.setState({
        queue: [
          {
            approvalId: 'appr-shell-001',
            toolCallId: 'call-shell-001',
            toolName: 'bash',
            args: { command: 'npm run build' },
            agentId: 'agent-main',
            actingAgentName: 'Mia',
            sessionId: 'sess-001',
            turnId: 'turn-001',
            expiresAt: Date.now() + 300_000,
            ...ORDINARY_TOOL_ANSWERS,
          },
        ],
      })
    })
    render(<ToolApprovalPanel />)

    fireEvent.click(screen.getByRole('button', { name: /1 hour/i }))

    await waitFor(() => {
      // Exact spec sentence (S-126) with the fixed clock and known agent; a
      // wrong agent name or a wrong/relative time fails. The exact one-item
      // list also excludes every other toast — including the retired
      // "Always allowed" wording (R2-Q1), which needs no separate negative.
      expect(toasts()).toEqual([
        { message: 'Allowed for Mia until 14:05.', variant: 'success' },
      ])
    })
  })

  // Regression row (b7526b2e60) + C-26 / S-126: same exact result line when
  // the server recorded the other narrow bash scope ("exact") — the recorded
  // scope flows through without changing or dropping the sentence, and no
  // "Always allowed" wording returns (R2-Q1). CHECK round 1 R6: the two
  // scope cases share the same exact-sentence oracle deliberately — what
  // varies is the recorded scope, not the sentence.
  it('L1 §1.2 R6 → C-26: closes with the result line naming the recorded timed allowance when the server recorded scope "exact"', async () => {
    setApprovalReply({
      approval_id: 'appr-shell-002',
      action: 'allow_for',
      status: 'ok',
      expires_at: endsTodayAtLocal(14, 5),
      applied: { call_released: true, timed_allowance: true, trust_approved: false },
      scope: 'exact',
    })
    act(() => {
      useToolApprovalStore.setState({
        queue: [
          {
            approvalId: 'appr-shell-002',
            toolCallId: 'call-shell-002',
            toolName: 'bash',
            args: { command: 'npm run build' },
            agentId: 'agent-main',
            actingAgentName: 'Mia',
            sessionId: 'sess-001',
            turnId: 'turn-002',
            expiresAt: Date.now() + 300_000,
            ...ORDINARY_TOOL_ANSWERS,
          },
        ],
      })
    })
    render(<ToolApprovalPanel />)

    fireEvent.click(screen.getByRole('button', { name: /1 hour/i }))

    await waitFor(() => {
      expect(toasts()).toEqual([
        { message: 'Allowed for Mia until 14:05.', variant: 'success' },
      ])
    })
  })

  it('L1 §1.2 R6: removes approval from queue after successful Approve', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalPanel />)

    fireEvent.click(screen.getByRole('button', { name: /Approve/i }))

    await waitFor(() => {
      expect(useToolApprovalStore.getState().queue).toHaveLength(0)
    })
  })

  // Regression row (b7526b2e60) + C-26 / S-126: a timed choice whose
  // allowance stuck (applied.timed_allowance = true) resolves the card — the
  // queue empties and the result line "Allowed for <agent> until <time>."
  // names the recorded allowance (the C-26 replacement of the old silent
  // Always Allow success; R2-Q1). This leg is S-126's second sentence: the
  // 1-day choice at 13:05 shows the day because the end is not today.
  // CHECK round 1 R7/W2: the toast list is asserted exactly — one success
  // toast, no extra warning/success toast passes.
  it('L1 §1.2 R6 → C-26: removes approval from queue after a successful timed choice, showing the 1-day result line', async () => {
    setApprovalReply({
      approval_id: 'appr-001',
      action: 'allow_for',
      status: 'ok',
      // S-126's fixed clock: the 1-day choice answered at 13:05 records
      // tomorrow 13:05.
      expires_at: endsTomorrowAtLocal(13, 5),
      applied: { call_released: true, timed_allowance: true, trust_approved: false },
    })
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalPanel />)

    fireEvent.click(screen.getByRole('button', { name: /1 day/i }))

    await waitFor(() => {
      expect(useToolApprovalStore.getState().queue).toHaveLength(0)
    })
    expect(toasts()).toEqual([
      { message: 'Allowed for Mia until tomorrow 13:05.', variant: 'success' },
    ])
  })
})

describe('ToolApprovalPanel — keyboard safety (amendment §1.2 R6)', () => {
  // Binding quote: "the card never takes focus on appearance ... Escape does
  // not resolve". Modal-only Escape=Deny and initial Deny focus are superseded.
  it('L1 §1.2 R6: Escape does not resolve or dismiss, and never approves', () => {
    act(() => useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] }))
    render(<ToolApprovalPanel />)
    fireEvent.keyDown(screen.getByTestId('tool-approval-panel'), { key: 'Escape', code: 'Escape' })
    expect(approvalPosts()).toEqual([])
    expect(useToolApprovalStore.getState().queue.map(a => a.approvalId)).toEqual(['appr-001'])
    expect(useToolApprovalStore.getState().resolvedIds).toEqual([])
    expect(approvalPosts()).not.toContainEqual({ id: 'appr-001', body: { action: 'allow_once' } })
  })

  it('L1 §1.2 R6: appearing inline preserves composer focus, never Deny or Approve focus', () => {
    render(<><textarea aria-label="Chat composer" /><ToolApprovalPanel /></>)
    const composer = screen.getByLabelText('Chat composer')
    composer.focus()
    act(() => useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] }))
    expect(composer).toHaveFocus()
    expect(screen.getByRole('button', { name: /Deny/i })).not.toHaveFocus()
    expect(screen.getByRole('button', { name: /Approve/i })).not.toHaveFocus()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(approvalPosts()).toEqual([])
  })
})

describe('ToolApprovalPanel — error handling', () => {
  it('L1 §1.2 R6: dismisses silently on 410 Gone (already resolved)', async () => {
    setApprovalReply({ error: 'Gone' }, 410)

    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalPanel />)

    fireEvent.click(screen.getByRole('button', { name: /Deny/i }))

    await waitFor(() => {
      // Entry removed from queue on 410
      expect(useToolApprovalStore.getState().queue).toHaveLength(0)
    })
    // No toast should be shown for 410
    expect(toasts()).toEqual([])
  })

  it('L1 §1.2 R6: shows admin-required toast on 403', async () => {
    setApprovalReply({ error: 'Forbidden' }, 403)

    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalPanel />)

    fireEvent.click(screen.getByRole('button', { name: /Approve/i }))

    await waitFor(() => {
      expect(toasts()).toContainEqual(
        expect.objectContaining({
          message: expect.stringMatching(/must be an admin/i),
          variant: 'error',
        })
      )
    })
    // Approval stays in queue on 403 (non-dismissive error)
    expect(useToolApprovalStore.getState().queue).toHaveLength(1)
  })

  it('L1 §1.2 R6: shows re-auth toast on 401', async () => {
    setApprovalReply({ error: 'Unauthorized' }, 401)

    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalPanel />)

    fireEvent.click(screen.getByRole('button', { name: /Approve/i }))

    await waitFor(() => {
      expect(toasts()).toContainEqual(
        expect.objectContaining({
          message: expect.stringMatching(/log in again/i),
          variant: 'error',
        })
      )
    })
  })

  // D9 — this call bypasses React Query (raw await submitToolApproval), so
  // queryClient.ts's global 401 subscriber never sees it. Before this fix, a
  // 401 here left the rest of the app (Sidebar, every other open screen)
  // looking still logged-in. Assert the shared forced-logout path now fires.
  it('L1 §1.2 R6: D9: routes a 401 through the shared forceLogout() teardown', async () => {
    setApprovalReply({ error: 'Unauthorized' }, 401)

    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalPanel />)

    fireEvent.click(screen.getByRole('button', { name: /Approve/i }))

    await waitFor(() => {
      expect(useAuthStore.getState().username).toBeNull()
      expect(localStorage.getItem('omnipus_auth_username')).toBeNull()
      expect(window.location.hash).toBe('#/login')
    })
  })

  it('L1 §1.2 R6: D9: does NOT call forceLogout() on 403 (forbidden, not unauthenticated)', async () => {
    setApprovalReply({ error: 'Forbidden' }, 403)

    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalPanel />)

    fireEvent.click(screen.getByRole('button', { name: /Approve/i }))

    await waitFor(() => {
      expect(toasts().length).toBeGreaterThan(0)
    })
    expect(useAuthStore.getState().username).toBe('l1-red-user')
    expect(window.location.hash).not.toBe('#/login')
  })
})

describe('ToolApprovalPanel — expires_in_ms countdown (FR-082)', () => {
  it('L1 §1.2 R6: expiresAt is computed as Date.now() + expires_in_ms on enqueue', () => {
    const before = Date.now()
    const EXPIRES_IN_MS = 300_000

    act(() => {
      useToolApprovalStore.getState().enqueue({
        type: 'tool_approval_required',
        approval_id: 'appr-countdown',
        tool_call_id: 'call-x',
        tool_name: 'exec',
        args: {},
        agent_id: 'a',
        session_id: 's',
        turn_id: 't',
        expires_in_ms: EXPIRES_IN_MS,
      offered_actions: ['allow_once', 'allow_for', 'deny'],
      allow_durations: ['1h', '3h', '1d'],
      })
    })

    const after = Date.now()
    const entry = useToolApprovalStore.getState().queue[0]
    expect(entry).toBeDefined()
    // expiresAt should be within [before + EXPIRES_IN_MS, after + EXPIRES_IN_MS]
    expect(entry.expiresAt).toBeGreaterThanOrEqual(before + EXPIRES_IN_MS)
    expect(entry.expiresAt).toBeLessThanOrEqual(after + EXPIRES_IN_MS)
  })
})

describe('ToolApprovalPanel — session_state reset handler (FR-052, FR-073, FR-081)', () => {
  it('L1 §1.2 R6: removes stale approvals not present in session_state', () => {
    act(() => {
      useToolApprovalStore.setState({
        queue: [
          { ...SAMPLE_APPROVAL, approvalId: 'appr-stale-1' },
          { ...SAMPLE_APPROVAL, approvalId: 'appr-stale-2' },
          { ...SAMPLE_APPROVAL, approvalId: 'appr-live' },
        ],
      })
    })

    const sessionStateFrame: SessionStateFrame = {
      type: 'session_state',
      user_id: 'user-1',
      pending_approvals: [
        {
          approval_id: 'appr-live',
          tool_call_id: 'call-live',
          turn_id: 'turn-live',
          args: {},
          session_id: 'sess-001',
          tool_name: 'fetch_url',
          agent_id: 'agent-main',
          expires_in_ms: 299_000,
        offered_actions: ['allow_once', 'allow_for', 'deny'],
        allow_durations: ['1h', '3h', '1d'],
        },
      ],
      emitted_at: new Date().toISOString(),
    }

    act(() => {
      useToolApprovalStore.getState().reconcileWithSessionState(sessionStateFrame)
    })

    const queue = useToolApprovalStore.getState().queue
    expect(queue).toHaveLength(1)
    expect(queue[0].approvalId).toBe('appr-live')
  })

  it('L1 §1.2 R6: refreshes expiresAt for approvals present in session_state', () => {
    const oldExpiresAt = Date.now() + 10_000 // was 10s remaining

    act(() => {
      useToolApprovalStore.setState({
        queue: [{ ...SAMPLE_APPROVAL, approvalId: 'appr-live', expiresAt: oldExpiresAt }],
      })
    })

    const newExpiresInMs = 299_000 // gateway says 299s remaining

    const before = Date.now()
    act(() => {
      useToolApprovalStore.getState().reconcileWithSessionState({
        type: 'session_state',
        user_id: 'user-1',
        pending_approvals: [
          {
            approval_id: 'appr-live',
            tool_call_id: 'call-live',
            turn_id: 'turn-live',
            args: {},
            session_id: 'sess-001',
            tool_name: 'fetch_url',
            agent_id: 'agent-main',
            expires_in_ms: newExpiresInMs,
          offered_actions: ['allow_once', 'allow_for', 'deny'],
          allow_durations: ['1h', '3h', '1d'],
          },
        ],
        emitted_at: new Date().toISOString(),
      })
    })
    const after = Date.now()

    const entry = useToolApprovalStore.getState().queue[0]
    // expiresAt refreshed to approximately now + newExpiresInMs
    expect(entry.expiresAt).toBeGreaterThanOrEqual(before + newExpiresInMs)
    expect(entry.expiresAt).toBeLessThanOrEqual(after + newExpiresInMs)
    // Must be different from (and greater than) the old value
    expect(entry.expiresAt).toBeGreaterThan(oldExpiresAt)
  })

  it('L1 §1.2 R6: clears all approvals when session_state has empty pending_approvals', () => {
    act(() => {
      useToolApprovalStore.setState({
        queue: [SAMPLE_APPROVAL, { ...SAMPLE_APPROVAL, approvalId: 'appr-002' }],
      })
    })

    act(() => {
      useToolApprovalStore.getState().reconcileWithSessionState({
        type: 'session_state',
        user_id: 'user-1',
        pending_approvals: [],
        emitted_at: new Date().toISOString(),
      })
    })

    expect(useToolApprovalStore.getState().queue).toHaveLength(0)
  })
})

// ── Phase 5: edge-case render tests ────────────────────────────────────────────
//
// These tests verify that ToolApprovalPanel does not crash when a valid-shape
// but degenerate-value ToolApprovalRequiredFrame reaches the component. They
// mirror the exact failure case from the Ava-chat bug (args: {}) and extend to
// unicode keys, very long strings, nested objects, null values, and timing edge
// cases. Props are constructed from the generated ToolApprovalRequiredFrame
// type — no hand-written wire types.
//
// Each test: enqueue the frame → render ToolApprovalPanel → assert no throw.

// Minimal valid ToolApprovalRequiredFrame (all required fields per AsyncAPI spec)
const baseFrame: ToolApprovalRequiredFrame = {
  type: 'tool_approval_required',
  approval_id: 'appr-edge-001',
  tool_call_id: 'call-edge-001',
  tool_name: 'fetch_url',
  args: { url: 'https://example.com' },
  agent_id: 'agent-main',
  session_id: 'sess-edge',
  turn_id: 'turn-edge',
  expires_in_ms: 300_000,
offered_actions: ['allow_once', 'allow_for', 'deny'],
allow_durations: ['1h', '3h', '1d'],
}

// Edge cases: [label, partial override of ToolApprovalRequiredFrame]
const edgeCases: Array<[string, Partial<ToolApprovalRequiredFrame>]> = [
  // Original Ava-chat crash case — args is an empty object (Object.keys(null) was the bug)
  ['empty args object', { args: {} }],
  // Single key-value pair
  ['single-key args', { args: { foo: 'bar' } }],
  // Unicode keys and values (emoji, multi-byte)
  ['unicode args', { args: { '\u{1F680}': '\u{1F30D}' } }],
  // Very long string value — tests truncation / overflow handling
  ['long string arg value', { args: { x: 'x'.repeat(10_000) } }],
  // Nested object — JSON.stringify depth test
  ['nested object arg', { args: { obj: { a: { b: { c: 1 } } } } }],
  // Array value — valid JSON, rendered via JSON.stringify
  ['array arg value', { args: { list: [1, 2, 3, 4, 5] } }],
  // null value inside args object — schema allows unknown, null is valid JSON
  ['null value in args', { args: { x: null } }],
  // Multiple args
  ['multiple args', { args: { url: 'https://example.com', timeout: 5000, follow_redirects: true } }],
  // Empty string tool name — degenerate but valid per schema (string, no minLength)
  ['empty string tool name', { tool_name: '' }],
  // Very long tool name — tests truncation in the UI
  ['very long tool name', { tool_name: 'a'.repeat(500) }],
  // Tool name with special chars
  ['tool name with dots and underscores', { tool_name: 'workspace.shell_bg.run' }],
  // expires_in_ms = 0 — should render in expired state immediately
  ['expires_in_ms is 0', { expires_in_ms: 0 }],
  // expires_in_ms is MAX_SAFE_INTEGER — huge countdown, should not overflow
  ['expires_in_ms is MAX_SAFE_INTEGER', { expires_in_ms: Number.MAX_SAFE_INTEGER }],
  // Very long agent_id string
  ['very long agent_id', { agent_id: 'agent-' + 'x'.repeat(500) }],
]

describe.each(edgeCases)(
  'ToolApprovalPanel renders "%s" without throwing',
  (_label, overrides) => {
    it('L1 §1.2 R6: renders without throwing', () => {
      const frame: ToolApprovalRequiredFrame = { ...baseFrame, ...overrides }
      act(() => {
        useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
        useToolApprovalStore.getState().enqueue(frame)
      })
      act(() => useSessionStore.setState({ activeSessionId: frame.session_id }))
      expect(() => render(<ToolApprovalPanel />)).not.toThrow()
      expect(screen.getByTestId('tool-approval-panel')).toBeInTheDocument()
    })
  },
)

// ── C-26 exact-action tools: request_mount ─────────────────────────────────
//
// request_mount is one of the five exact-action tools (C-26 point 9; S-129;
// Dataset D17 rows 21–25): its card offers Approve Once and Deny only and
// NEVER a time choice — whether or not a folder path is present. The old
// "hides Always Allow when no folder path is present" case is retained as
// the missing-path exact-action case (CHECK round 1 R10/W1): no time
// choices, and one-time approve/deny remain.

const MOUNT_APPROVAL: PendingToolApproval = {
  approvalId: 'appr-mount',
  toolCallId: 'call-mount',
  toolName: 'request_mount',
  args: { host_path: '/Users/dana/Documents/projects/api', reason: 'to run the build' },
  agentId: 'agent-main',
  sessionId: 'sess-001',
  turnId: 'turn-001',
  expiresAt: Date.now() + 300_000,
  // C-26 point 9: the server never lists allow_for for an exact-action tool.
  ...EXACT_ACTION_TOOL_ANSWERS,
}

const MOUNT_APPROVAL_NO_PATH = {
  ...MOUNT_APPROVAL,
  approvalId: 'appr-mount-no-path',
  toolCallId: 'call-mount-no-path',
  turnId: 'turn-mount-no-path',
  args: { reason: 'to run the build' },
}

describe('ToolApprovalPanel — C-26 exact-action request_mount', () => {
  // Regression row (b7526b2e60) + C-26 approval-card row: the choices on an
  // ORDINARY card are Approve Once, Allow for 1 hour / 3 hours / 1 day,
  // Deny — and no Always Allow anywhere (R2-Q1).
  it('L1 §1.2 R6 → C-26: offers the three time choices for an ordinary tool, and never Always Allow', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalPanel />)
    expect(screen.getByRole('button', { name: /1 hour/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /3 hours/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /1 day/i })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Always Allow/i })).not.toBeInTheDocument()
  })

  it('L1 §1.2 R6 → C-26/S-129: request_mount is exact-action — no time choice even with a folder path present', () => {
    // C-26 point 9 / S-129 (CHECK round 1 R9): request_mount never offers a
    // time choice, args present or not — the earlier draft asserting three
    // duration buttons here carried the wrong final-spec oracle (it
    // contradicted order 64 / D17 row 22).
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalPanel />)
    expect(screen.queryByRole('button', { name: /1 hour/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /3 hours/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /1 day/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Always Allow/i })).not.toBeInTheDocument()
  })

  it('L1 §1.2 R6 → C-26/S-129: request_mount without a folder path — still no time choice, one-time approve and deny remain', () => {
    // Restored missing-path case (CHECK round 1 R10/W1): the exact-action
    // rule strengthens the old per-call negative — no time choice with no
    // path either, and the one-time approve/deny pair stays.
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL_NO_PATH] })
    })
    render(<ToolApprovalPanel />)
    expect(screen.queryByRole('button', { name: /1 hour/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /3 hours/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /1 day/i })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Add folder/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Don't add/i })).toBeInTheDocument()
  })

  it('L1 §1.2 R6: still offers a way to approve and a way to deny request_mount', () => {
    // C-26 / S-129: the exact-action card is Approve Once + Deny only — no
    // time choice (asserted above); the one-time controls remain (CHECK
    // round 1 R11).
    //
    // request_mount's copy diverges from the generic Approve/Deny labels
    // (operator-approved "Add folder" / "Don't add" — see
    // approvalPreviews/registry.ts and RequestMountApprovalPreview.tsx) but
    // still dispatches the same 'allow_once'/'deny' actions — covered by the
    // dedicated ToolApprovalPanel.readablePreviews.test.tsx suite. This test
    // only asserts BOTH decision buttons are present, whatever their label.
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalPanel />)
    expect(screen.getByRole('button', { name: /Add folder/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Don't add/i })).toBeInTheDocument()
  })
})

// Real stores/QueryClient/auth survive the test boundary; only fetch/socket doubled.
afterEach(async () => { await cleanupApprovalState() })
