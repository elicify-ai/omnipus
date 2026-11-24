// Channels FR-038, T-22 / S-29. Actual Panel AND actual Modal (older tests
// named Modal often load Panel instead). Only fetch/socket edges are doubled.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import type { ToolApprovalRequiredFrame } from '@/lib/api/generated/asyncapi-types'
import { useToolApprovalStore } from '@/store/toolApproval'
import { ToolApprovalPanel } from './ToolApprovalPanel'
import { ToolApprovalModal } from './ToolApprovalModal'
import {
  approvalPosts, cleanupApprovalState, installApprovalNetwork,
  renderWithClient, resetSharedState, setApprovalReply,
} from '../../../tests/l1-ui-red-u23/support'

const SESSION = 'q7-approval-session'
const NOTICE = 'This approval applies to this one request.'
const frame: ToolApprovalRequiredFrame = {
  type: 'tool_approval_required', approval_id: 'q7-approval', tool_call_id: 'q7-call',
  tool_name: 'set_channel_outbound', agent_id: 'omnipus', session_id: SESSION,
  turn_id: 'q7-turn', expires_in_ms: 300_000,
  args: { id: 'discord', policy: 'agent_may_start', targets: ['222'], expected_revision: 'q7-live-revision' },
// set_channel_outbound is always-ask (C-26 / S-125): the server computes
// Approve Once + Deny + Cancel and NO durations for it — the earlier fixture
// carried choices this tool cannot be offered. The ordinary-tool control
// below overrides both fields for enable_channel.
offered_actions: ['allow_once', 'deny', 'cancel'],
allow_durations: [],
}
// The ordinary connection tool's choices for the lasting-Allow control:
// enable_channel is neither exact-action nor always-ask, so its card offers
// the three time choices (C-26 approval-card row). Cancel presence is
// per-tool ("where it has it today") and not pinned by this fixture.
const ORDINARY_FRAME: ToolApprovalRequiredFrame = {
  ...frame,
  offered_actions: ['allow_once', 'allow_for', 'deny'],
  allow_durations: ['1h', '3h', '1d'],
}

beforeEach(() => {
  resetSharedState(SESSION)
  installApprovalNetwork()
})
afterEach(cleanupApprovalState)

for (const [name, Component] of [['Panel', ToolApprovalPanel], ['Modal', ToolApprovalModal]] as const) {
  describe(`Q7 real ${name} one-request card`, () => {
    function mount(frameToMount: ToolApprovalRequiredFrame = frame) {
      act(() => useToolApprovalStore.getState().enqueue(frameToMount))
      renderWithClient(<Component />)
    }

    it('FR-038: communicates the exact one-request scope', () => {
      mount()
      expect(screen.getByText(NOTICE, { exact: true })).toBeInTheDocument()
      expect(approvalPosts()).toEqual([])
    })

    it('FR-038/r9 Q3 A: hides lasting Allow but retains Approve Once, Deny and Cancel', () => {
      mount()
      expect(screen.getByRole('button', { name: 'Approve Once' })).toBeEnabled()
      expect(screen.getByRole('button', { name: 'Deny' })).toBeEnabled()
      expect(screen.getByRole('button', { name: 'Cancel' })).toBeEnabled()
      expect(screen.queryByRole('button', { name: 'Always Allow' })).not.toBeInTheDocument()
      expect(screen.queryByRole('radiogroup', { name: 'Grant scope for Always Allow' })).not.toBeInTheDocument()
      expect(approvalPosts()).toEqual([])
    })

    for (const [label, action] of [['Approve Once', 'allow_once'], ['Deny', 'deny'], ['Cancel', 'cancel']] as const) {
      it(`positive control: ${label} posts only ${action} and cannot request a grant`, async () => {
        setApprovalReply({ approval_id: frame.approval_id, action, status: 'ok', grant_recorded: false })
        mount()
        await act(async () => { fireEvent.click(screen.getByRole('button', { name: label })) })
        await waitFor(() => expect(approvalPosts()).toEqual([{ id: frame.approval_id, body: { action } }]))
        expect(approvalPosts().some(({ body }) => (body as { action?: string }).action === 'allow')).toBe(false)
      })
    }

    it('positive control: an ordinary connection tool offers a one-hour allowance and no special scope notice', async () => {
      setApprovalReply({
        approval_id: frame.approval_id, action: 'allow_for', status: 'ok', grant_recorded: true,
        applied: { trust_approved: false, timed_allowance: true, call_released: true }, expires_at: '2026-11-24T04:00:00Z',
      })
      mount({ ...ORDINARY_FRAME, tool_name: 'enable_channel' })
      expect(screen.queryByText(NOTICE, { exact: true })).not.toBeInTheDocument()
      await act(async () => { fireEvent.click(screen.getByRole('button', { name: /1 hour/i })) })
      await waitFor(() => expect(approvalPosts()).toEqual([
        { id: frame.approval_id, body: { action: 'allow_for', duration: '1h' } },
      ]))
      expect(screen.queryByRole('button', { name: 'Always Allow' })).not.toBeInTheDocument()
    })
  })
}
