// L1 amendment §1.2 R6 / §1.5: inline container replaces the modal.
// All non-container preview/action/security oracles retained; B3 stub deletion deferred.
// SUPERSESSION (Train 3, amendment section 1 / bundle B3): the snapshot
// requires the full request, so the reconnect-stub entries this file's
// hand-built describe renders are unreachable from the wire; the two
// end-to-end stub cases (Deny-only card, elapsed stub) are retired with that
// subject, and the stub UI branches themselves await the dev's fold.
// ToolApprovalModal.readablePreviews.test.tsx — inline surface
//
// Coverage for the per-tool readable-summary registry (approvalPreviews/):
//  1. request_mount renders the folder path + reason as VISIBLE TEXT, not
//     merely findable inside a JSON blob — and the raw Arguments JSON dump
//     is genuinely absent ('replace' mode, not 'additive').
//  2. The lasting-consequence closing line is shown.
//  3. request_mount's title/buttons use the operator-approved "Add folder"
//     copy, but clicking them still dispatches the SAME 'allow_once'/'deny'
//     wire actions — a label-only rename.
//  4. Cancel is intentionally absent for request_mount (closed 2-button
//     spec) — a deliberate, tested design decision, not an oversight.
//  5. A tool with no registered preview still renders the generic Arguments
//     JSON dump (registry fallback keeps working).
//  6. The reconnect-stub case (toolCallId/turnId both empty — see the
//     isReconnectStub note in ToolApprovalPanel.tsx) renders an honest
//     notice with ONLY a Deny action.
//  7. humanizeToolName is wired into the Tool label for an ordinary tool.
//
// RequestMountApprovalPreview resolves agent/workspace names via a read-only
// queryClient.getQueryData lookup against the SAME shared singleton
// (src/lib/queryClient.ts) the Sidebar/BrowserLiveView already populate —
// deliberately not a fresh useQuery subscription (see that file's doc
// comment), so this modal never needs a QueryClientProvider in its tree.
// Seed/clear it directly, mirroring BrowserLiveView.agentChip.test.tsx.

import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import { act } from 'react'
import { queryClient } from '@/lib/queryClient'
import { workspacesQueryKeys } from '@/lib/api'
import type { Agent, Session, Workspace } from '@/lib/api'
import { makeAgent } from '@/test/factories'

import { useToolApprovalStore } from '@/store/toolApproval'
import { useSessionStore } from '@/store/session'
import type { ComponentType } from 'react'
import { renderWithClient as render, loadApprovalPanel, resetSharedState, installApprovalNetwork, approvalPosts, cleanupApprovalState } from '../../../tests/l1-ui-red-u23/support'
let ToolApprovalPanel: ComponentType
import type { SessionStateFrame } from '@/lib/ws'

beforeEach(async () => {
  resetSharedState('sess-mount')
  installApprovalNetwork()
  ToolApprovalPanel = await loadApprovalPanel()
})

const MOUNT_APPROVAL = {
  approvalId: 'appr-mount-readable',
  toolCallId: 'call-mount-readable',
  toolName: 'request_mount',
  args: {
    host_path: '/Users/dana/Documents/projects/api',
    reason: 'to install dependencies and run the build',
  },
  agentId: 'agent-jim',
  sessionId: 'sess-mount',
  turnId: 'turn-mount',
  expiresAt: Date.now() + 300_000,
}

function seedAgent() {
  queryClient.setQueryData<Agent[]>(['agents'], [makeAgent({ id: 'agent-jim', name: 'Jim' })])
}

function seedWorkspace() {
  queryClient.setQueryData<Session[]>(
    ['sessions'],
    [{ id: 'sess-mount', workspace_id: 'ws-1' }] as unknown as Session[],
  )
  queryClient.setQueryData<Workspace[]>(
    workspacesQueryKeys.list({ status: 'active' }),
    [{ id: 'ws-1', name: 'Acme Backend' }] as unknown as Workspace[],
  )
}

describe('ToolApprovalPanel — request_mount readable summary (Deliverable 2)', () => {
  it('L1 §1.2 R6: renders the folder path and reason as visible text, and hides the raw Arguments JSON dump', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalPanel />)

    // Found as plain rendered text, not merely present inside a JSON blob.
    expect(screen.getByText('/Users/dana/Documents/projects/api')).toBeInTheDocument()
    expect(screen.getByText('to install dependencies and run the build')).toBeInTheDocument()

    // The raw JSON dump this whole track exists to eliminate must be GONE
    // for request_mount, not just supplemented — 'replace' mode, not
    // 'additive'.
    expect(screen.queryByText('Arguments')).not.toBeInTheDocument()
    expect(screen.queryByText(/"host_path"/)).not.toBeInTheDocument()
  })

  it('L1 §1.2 R6: states the lasting consequence', () => {
    seedAgent()
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalPanel />)
    expect(
      screen.getByText('Jim will be able to change files in this folder until you remove it.'),
    ).toBeInTheDocument()
    // UAT 2026-09-13 D-15: the decision is about write access only; the copy
    // must never claim that declining withholds reading (ADR-068 opens reads).
    expect(screen.queryByText(/able to read and change/)).toBeNull()
    expect(screen.getByText(/declining keeps this folder read-only for Jim, it does not hide it/)).toBeInTheDocument()
  })

  it('L1 §1.2 R6: falls back to the raw agent id in the consequence line when the agent name is not cached', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalPanel />)
    expect(
      screen.getByText(
        'agent-jim will be able to change files in this folder until you remove it.',
      ),
    ).toBeInTheDocument()
  })

  it('L1 §1.2 R6: shows the agent-specific title "<Agent> wants to add a folder"', () => {
    seedAgent()
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalPanel />)
    expect(screen.getByText('Jim wants to add a folder')).toBeInTheDocument()
  })

  it('L1 §1.2 R6: resolves and shows the workspace name from the cached sessions/workspaces lists', () => {
    seedWorkspace()
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalPanel />)
    expect(screen.getByText('Acme Backend')).toBeInTheDocument()
  })

  it('L1 §1.2 R6: falls back to "Unknown workspace" when the session/workspace cache has no match', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalPanel />)
    expect(screen.getByText('Unknown workspace')).toBeInTheDocument()
  })

  it('L1 §1.2 R6: shows the human tool name via humanizeToolName, not the raw "request_mount" identifier', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalPanel />)
    expect(screen.queryByText('request_mount')).not.toBeInTheDocument()
  })
})

describe('ToolApprovalPanel — request_mount button copy (Deliverable 2/3)', () => {
  it('L1 §1.2 R6: "Add folder" dispatches the SAME approve action as every other tool', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalPanel />)

    fireEvent.click(screen.getByRole('button', { name: /Add folder/i }))
    await waitFor(() => {
      expect(approvalPosts()).toContainEqual({ id: 'appr-mount-readable', body: { action: 'allow_once' } })
    })
  })

  it('L1 §1.2 R6: "Don\'t add" dispatches the SAME deny action as every other tool', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalPanel />)

    fireEvent.click(screen.getByRole('button', { name: /Don't add/i }))
    await waitFor(() => {
      expect(approvalPosts()).toContainEqual({ id: 'appr-mount-readable', body: { action: 'deny' } })
    })
  })

  // MERGE 2026-09-15: the "does not offer Cancel for request_mount" case is
  // dropped. It pinned integrate's D-90 decision (Cancel control removed,
  // Escape sets the approval aside); the founder ruled tool approval comes
  // from release/v0.1.1 (#683), whose modal keeps an explicit Cancel action
  // and treats Escape as the safe-default deny. Recorded as a contested
  // product decision in the merge decision record.
})

describe('ToolApprovalPanel — registry fallback (Deliverable 1)', () => {
  it('L1 §1.2 R6: a tool with no registered preview still renders the generic Arguments JSON dump', () => {
    act(() => useSessionStore.setState({ activeSessionId: 'sess-001' }))
    act(() => {
      useToolApprovalStore.setState({
        queue: [
          {
            approvalId: 'appr-fallback',
            toolCallId: 'call-fallback',
            toolName: 'search_web',
            args: { query: 'omnipus release notes' },
            agentId: 'agent-main',
            sessionId: 'sess-001',
            turnId: 'turn-001',
            expiresAt: Date.now() + 300_000,
          },
        ],
      })
    })
    render(<ToolApprovalPanel />)
    expect(screen.getByText('Arguments')).toBeInTheDocument()
    expect(screen.getByText(/omnipus release notes/)).toBeInTheDocument()
  })

  it('L1 §1.2 R6: shows the humanized Tool label for an ordinary tool, not the raw identifier', () => {
    act(() => useSessionStore.setState({ activeSessionId: 'sess-001' }))
    act(() => {
      useToolApprovalStore.setState({
        queue: [
          {
            approvalId: 'appr-fallback-2',
            toolCallId: 'call-fallback-2',
            toolName: 'search_web',
            args: { query: 'x' },
            agentId: 'agent-main',
            sessionId: 'sess-001',
            turnId: 'turn-001',
            expiresAt: Date.now() + 300_000,
          },
        ],
      })
    })
    render(<ToolApprovalPanel />)
    expect(screen.getByText('Search the web')).toBeInTheDocument()
    expect(screen.queryByText('search_web')).not.toBeInTheDocument()
  })
})

describe('ToolApprovalPanel — a normal approval renders the full approvable card (bundle B3)', () => {
  // RETIRED with bundle B3 (founder ruling / amendment section 1; commit
  // 8f740826e): the four reconnect-stub rendering cases ("honest notice",
  // "offers ONLY Deny", "Deny dispatches in the stub rendering", "never
  // steals composer focus (B3 deferred)") died with their subject — the
  // isReconnectStub UI branch is deleted, and the wire can no longer produce
  // a stub entry (the snapshot requires tool_call_id/turn_id/args). The
  // full-card pin below is their surviving positive counterpart.
  it('L1 §1.2 R6: a normal request_mount approval renders the full approvable card', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalPanel />)
    expect(screen.getByRole('button', { name: /Add folder/i })).toBeInTheDocument()
    // Real args are present, so Always Allow must be offered — it remembers
    // THIS folder, not any folder.
    expect(screen.getByTestId('always-allow-toggle')).toBeInTheDocument()
    // The retired stub surface never returns.
    expect(screen.queryByText('Approval Details Unavailable')).not.toBeInTheDocument()
  })
})

// ── End-to-end: session_state frame → store → rendered modal ───────────────
//
// These tests drive the real
// useToolApprovalStore.reconcileWithSessionState action (no store mocking)
// starting from an empty queue — the actual state a fresh page load leaves
// the store in — so the only thing standing between an outstanding
// server-side approval and a rendered "Approval Details Unavailable" card is
// the real WS frame handling. This is the test that proves the reconnect-gap
// DEFECT (not just the isReconnectStub rendering contract) is fixed; without
// it, the store's stub-creation code could regress back to doing nothing and
// every other test in this file would stay green.
describe('ToolApprovalPanel — reconnect gap, end-to-end (store fix + modal rendering)', () => {
  // RETIRED with bundle B3 (Train 3, amendment section 1): "a session_state
  // frame reporting an approval this tab never queued renders the honest
  // Deny-only card". Its subject — a Deny-only stub entry — is unreachable:
  // SessionStatePendingApproval now REQUIRES tool_call_id, turn_id and args,
  // so reconcile restores the full approvable card; the dev recommends
  // deleting the stub UI branches in the fold, and this case dies with that
  // subject (the full-restore pin now lives in the store tests and the
  // refreshed case below).

  it('L1 §1.2 R6: an approval already queued from a live frame is refreshed, not duplicated, and keeps rendering its full card', () => {
    // Simulate: the live tool_approval_required frame already arrived (via
    // the real enqueue action, not a hand-built fixture) while this tab was
    // connected, THEN a session_state snapshot arrives (e.g. a brief WS
    // reconnect) reporting the SAME approval as still pending.
    act(() => {
      useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
      useToolApprovalStore.getState().enqueue({
        type: 'tool_approval_required',
        approval_id: 'appr-e2e-known',
        tool_call_id: 'call-e2e-known',
        tool_name: 'request_mount',
        args: { host_path: '/Users/dana/repo', reason: 'to read the config' },
        agent_id: 'agent-jim',
        session_id: 'sess-mount',
        turn_id: 'turn-e2e-known',
        expires_in_ms: 300_000,
      })
    })

    const frame: SessionStateFrame = {
      type: 'session_state',
      user_id: 'user-1',
      pending_approvals: [
        {
          approval_id: 'appr-e2e-known',
          tool_call_id: 'call-e2e-known',
          turn_id: 'turn-e2e-known',
          args: { host_path: '/Users/dana/repo', reason: 'to read the config' },
          session_id: 'sess-mount',
          tool_name: 'request_mount',
          agent_id: 'agent-jim',
          expires_in_ms: 290_000,
        },
      ],
      emitted_at: new Date().toISOString(),
    }

    act(() => {
      useToolApprovalStore.getState().reconcileWithSessionState(frame)
    })

    // Refreshed in place, not duplicated.
    expect(useToolApprovalStore.getState().queue).toHaveLength(1)

    render(<ToolApprovalPanel />)

    // Renders the FULL request_mount summary (real args survived the
    // reconcile), not the reconnect-stub notice — proving refresh took the
    // "keep the existing full entry" path, not the "wipe and re-stub" path.
    expect(screen.queryByText('Approval Details Unavailable')).not.toBeInTheDocument()
    expect(screen.getByText('/Users/dana/repo')).toBeInTheDocument()
    expect(screen.getByText('to read the config')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Add folder/i })).toBeInTheDocument()
  })

  // RETIRED with bundle B3 (Train 3, amendment section 1): "a stub whose
  // reported delta has already elapsed renders as expired". Same subject
  // death as the Deny-only case above — the wire can no longer produce a
  // stub entry (the snapshot requires the full request), so there is no
  // stub whose delta could elapse. The relative-delta expiry rule itself
  // survives in src/store/toolApproval.test.ts's delta-of-0 full-restore
  // case.
})

// Real stores/QueryClient/auth survive the test boundary; only fetch/socket doubled.
afterEach(async () => { await cleanupApprovalState() })
