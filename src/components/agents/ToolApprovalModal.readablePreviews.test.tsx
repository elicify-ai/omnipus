// ToolApprovalModal.readablePreviews.test.tsx
//
// Coverage for the per-tool readable-summary registry (approvalPreviews/):
//  1. request_mount renders the folder path + reason as VISIBLE TEXT, not
//     merely findable inside a JSON blob — and the raw Arguments JSON dump
//     is genuinely absent ('replace' mode, not 'additive').
//  2. The lasting-consequence closing line is shown.
//  3. request_mount's title/buttons use the operator-approved "Add folder"
//     copy, but clicking them still dispatches the SAME 'approve'/'deny'
//     wire actions — a label-only rename.
//  4. Cancel is intentionally absent for request_mount (closed 2-button
//     spec) — a deliberate, tested design decision, not an oversight.
//  5. A tool with no registered preview still renders the generic Arguments
//     JSON dump (registry fallback keeps working).
//  6. The reconnect-stub case (toolCallId/turnId both empty — see the
//     isReconnectStub note in ToolApprovalModal.tsx) renders an honest
//     notice with ONLY a Deny action.
//  7. humanizeToolName is wired into the Tool label for an ordinary tool.
//
// RequestMountApprovalPreview resolves agent/workspace names via a read-only
// queryClient.getQueryData lookup against the SAME shared singleton
// (src/lib/queryClient.ts) the Sidebar/BrowserLiveView already populate —
// deliberately not a fresh useQuery subscription (see that file's doc
// comment), so this modal never needs a QueryClientProvider in its tree.
// Seed/clear it directly, mirroring BrowserLiveView.agentChip.test.tsx.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { act } from 'react'
import { queryClient } from '@/lib/queryClient'
import { workspacesQueryKeys } from '@/lib/api'
import type { Agent, Session, Workspace } from '@/lib/api'

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api')
  return {
    ...actual,
    submitToolApproval: vi.fn(),
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn((selector) => {
    const state = { addToast: vi.fn(), toasts: [], removeToast: vi.fn() }
    return selector ? selector(state) : state
  }),
}))

vi.mock('@/lib/authLogout', () => ({
  forceLogout: vi.fn(),
}))

import * as api from '@/lib/api'
import { useToolApprovalStore } from '@/store/toolApproval'
import { ToolApprovalModal } from './ToolApprovalModal'
import type { WsSessionStateFrame } from '@/lib/ws'

beforeEach(() => {
  act(() => {
    useToolApprovalStore.setState({ queue: [] })
  })
  vi.clearAllMocks()
  vi.mocked(api.submitToolApproval).mockResolvedValue(undefined)
  queryClient.clear()
})

afterEach(() => {
  queryClient.clear()
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
  queryClient.setQueryData<Agent[]>(['agents'], [{ id: 'agent-jim', name: 'Jim' }] as unknown as Agent[])
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

describe('ToolApprovalModal — request_mount readable summary (Deliverable 2)', () => {
  it('renders the folder path and reason as visible text, and hides the raw Arguments JSON dump', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    // Found as plain rendered text, not merely present inside a JSON blob.
    expect(screen.getByText('/Users/dana/Documents/projects/api')).toBeInTheDocument()
    expect(screen.getByText('to install dependencies and run the build')).toBeInTheDocument()

    // The raw JSON dump this whole track exists to eliminate must be GONE
    // for request_mount, not just supplemented — 'replace' mode, not
    // 'additive'.
    expect(screen.queryByText('Arguments')).not.toBeInTheDocument()
    expect(screen.queryByText(/"host_path"/)).not.toBeInTheDocument()
  })

  it('states the lasting consequence', () => {
    seedAgent()
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalModal />)
    expect(
      screen.getByText('Jim will be able to read and change files in this folder until you remove it.'),
    ).toBeInTheDocument()
  })

  it('falls back to the raw agent id in the consequence line when the agent name is not cached', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalModal />)
    expect(
      screen.getByText(
        'agent-jim will be able to read and change files in this folder until you remove it.',
      ),
    ).toBeInTheDocument()
  })

  it('shows the agent-specific title "<Agent> wants to add a folder"', () => {
    seedAgent()
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalModal />)
    expect(screen.getByText('Jim wants to add a folder')).toBeInTheDocument()
  })

  it('resolves and shows the workspace name from the cached sessions/workspaces lists', () => {
    seedWorkspace()
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalModal />)
    expect(screen.getByText('Acme Backend')).toBeInTheDocument()
  })

  it('falls back to "Unknown workspace" when the session/workspace cache has no match', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalModal />)
    expect(screen.getByText('Unknown workspace')).toBeInTheDocument()
  })

  it('shows the human tool name via humanizeToolName, not the raw "request_mount" identifier', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalModal />)
    expect(screen.queryByText('request_mount')).not.toBeInTheDocument()
  })
})

describe('ToolApprovalModal — request_mount button copy (Deliverable 2/3)', () => {
  it('"Add folder" dispatches the SAME approve action as every other tool', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    fireEvent.click(screen.getByRole('button', { name: /Add folder/i }))
    await waitFor(() => {
      expect(api.submitToolApproval).toHaveBeenCalledWith('appr-mount-readable', 'approve')
    })
  })

  it('"Don\'t add" dispatches the SAME deny action as every other tool', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    fireEvent.click(screen.getByRole('button', { name: /Don't add/i }))
    await waitFor(() => {
      expect(api.submitToolApproval).toHaveBeenCalledWith('appr-mount-readable', 'deny')
    })
  })

  it('does not offer Cancel for request_mount (closed 2-button spec)', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalModal />)
    expect(screen.queryByRole('button', { name: /^Cancel$/i })).not.toBeInTheDocument()
  })
})

describe('ToolApprovalModal — registry fallback (Deliverable 1)', () => {
  it('a tool with no registered preview still renders the generic Arguments JSON dump', () => {
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
    render(<ToolApprovalModal />)
    expect(screen.getByText('Arguments')).toBeInTheDocument()
    expect(screen.getByText(/omnipus release notes/)).toBeInTheDocument()
  })

  it('shows the humanized Tool label for an ordinary tool, not the raw identifier', () => {
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
    render(<ToolApprovalModal />)
    expect(screen.getByText('Search the web')).toBeInTheDocument()
    expect(screen.queryByText('search_web')).not.toBeInTheDocument()
  })
})

describe('ToolApprovalModal — reconnect-stub (Deliverable 4)', () => {
  // Reproduces the shape a future reconcileWithSessionState hydration fix
  // would need to produce for an approval whose full frame never reached
  // this browser tab (see the isReconnectStub note in ToolApprovalModal.tsx):
  // toolCallId and turnId both empty — a shape the wire schema guarantees can
  // never come from a genuine live tool_approval_required frame (both fields
  // are required, minLength 1, on ToolApprovalRequiredFrame).
  const RECONNECT_STUB_APPROVAL = {
    approvalId: 'appr-reconnect',
    toolCallId: '',
    toolName: 'request_mount',
    args: {},
    agentId: 'agent-jim',
    sessionId: 'sess-mount',
    turnId: '',
    expiresAt: Date.now() + 120_000,
  }

  it('renders an honest notice instead of a blank or normal-looking card', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [RECONNECT_STUB_APPROVAL] })
    })
    render(<ToolApprovalModal />)
    expect(screen.getByText('Approval Details Unavailable')).toBeInTheDocument()
    expect(screen.getByText(/did not come back with it/)).toBeInTheDocument()
    // Proves this is NOT silently falling through to request_mount's normal
    // 'replace' summary (which would show a "Folder" field even with {} args).
    expect(screen.queryByText('Folder')).not.toBeInTheDocument()
  })

  it('offers ONLY Deny — no Approve, no Add folder, no Always Allow, no Cancel', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [RECONNECT_STUB_APPROVAL] })
    })
    render(<ToolApprovalModal />)
    expect(screen.getByRole('button', { name: /Deny/i })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Approve$/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Add folder/i })).not.toBeInTheDocument()
    expect(screen.queryByTestId('always-allow-toggle')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Cancel$/i })).not.toBeInTheDocument()
  })

  it('the single Deny button dispatches the real deny action, in the reconnect-stub rendering specifically', async () => {
    // Deliberately re-asserts the stub title alongside the dispatch: without
    // the fix this approval renders the NORMAL 4-button row (Approve exists
    // too, and "Deny" there is unrelated to the reconnect-stub code path), so
    // pinning both in one test is what makes this fail on unfixed code rather
    // than passing vacuously because Deny-dispatches-deny was already true.
    act(() => {
      useToolApprovalStore.setState({ queue: [RECONNECT_STUB_APPROVAL] })
    })
    render(<ToolApprovalModal />)
    expect(screen.getByText('Approval Details Unavailable')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Approve$/i })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /Deny/i }))
    await waitFor(() => {
      expect(api.submitToolApproval).toHaveBeenCalledWith('appr-reconnect', 'deny')
    })
  })

  it('default focus still lands on Deny in the reconnect-stub rendering (safe-default contract preserved)', async () => {
    // Same reasoning as above: the focus mechanism alone is unchanged by the
    // fix (denyButtonRef was always attached to whichever button dispatches
    // 'deny'), so this pins the stub-specific title too — that part DOES
    // fail on unfixed code, where this approval renders the ordinary title
    // and 4-button row instead.
    act(() => {
      useToolApprovalStore.setState({ queue: [RECONNECT_STUB_APPROVAL] })
    })
    render(<ToolApprovalModal />)
    expect(screen.getByText('Approval Details Unavailable')).toBeInTheDocument()
    const denyButton = screen.getByRole('button', { name: /Deny/i })
    await waitFor(() => {
      expect(denyButton).toHaveFocus()
    })
  })

  it('a normal request_mount approval (real toolCallId/turnId) is NOT treated as a reconnect stub', () => {
    // Guards the sentinel itself: a live frame's non-empty ids must never
    // trip this branch, or every ordinary approval would degrade to
    // Deny-only.
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalModal />)
    expect(screen.queryByText('Approval Details Unavailable')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Add folder/i })).toBeInTheDocument()
    // Real args are present, so Always Allow must be offered — it remembers
    // THIS folder, not any folder.
    expect(screen.getByTestId('always-allow-toggle')).toBeInTheDocument()
  })
})

// ── End-to-end: session_state frame → store → rendered modal ───────────────
//
// Everything above proves the CONSUMER half: given a queue entry already
// shaped like a reconnect stub, the modal renders it honestly. None of it
// proves a stub ever gets INTO the queue. These tests drive the real
// useToolApprovalStore.reconcileWithSessionState action (no store mocking)
// starting from an empty queue — the actual state a fresh page load leaves
// the store in — so the only thing standing between an outstanding
// server-side approval and a rendered "Approval Details Unavailable" card is
// the real WS frame handling. This is the test that proves the reconnect-gap
// DEFECT (not just the isReconnectStub rendering contract) is fixed; without
// it, the store's stub-creation code could regress back to doing nothing and
// every other test in this file would stay green.
describe('ToolApprovalModal — reconnect gap, end-to-end (store fix + modal rendering)', () => {
  it('a session_state frame reporting an approval this tab never queued renders the honest Deny-only card', () => {
    // Start exactly where a fresh page load starts: nobody has enqueued
    // anything locally, but the server still thinks this approval is open.
    act(() => {
      useToolApprovalStore.setState({ queue: [] })
    })

    const frame: WsSessionStateFrame = {
      type: 'session_state',
      user_id: 'user-1',
      pending_approvals: [
        {
          approval_id: 'appr-e2e-unseen',
          session_id: 'sess-mount',
          tool_name: 'request_mount',
          agent_id: 'agent-jim',
          expires_in_ms: 200_000,
        },
      ],
      emitted_at: new Date().toISOString(),
    }

    act(() => {
      useToolApprovalStore.getState().reconcileWithSessionState(frame)
    })

    render(<ToolApprovalModal />)

    expect(screen.getByText('Approval Details Unavailable')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Deny/i })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Approve$/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Add folder/i })).not.toBeInTheDocument()
    expect(screen.queryByTestId('always-allow-toggle')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Cancel$/i })).not.toBeInTheDocument()
  })

  it('an approval already queued from a live frame is refreshed, not duplicated, and keeps rendering its full card', () => {
    // Simulate: the live tool_approval_required frame already arrived (via
    // the real enqueue action, not a hand-built fixture) while this tab was
    // connected, THEN a session_state snapshot arrives (e.g. a brief WS
    // reconnect) reporting the SAME approval as still pending.
    act(() => {
      useToolApprovalStore.setState({ queue: [] })
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

    const frame: WsSessionStateFrame = {
      type: 'session_state',
      user_id: 'user-1',
      pending_approvals: [
        {
          approval_id: 'appr-e2e-known',
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

    render(<ToolApprovalModal />)

    // Renders the FULL request_mount summary (real args survived the
    // reconcile), not the reconnect-stub notice — proving refresh took the
    // "keep the existing full entry" path, not the "wipe and re-stub" path.
    expect(screen.queryByText('Approval Details Unavailable')).not.toBeInTheDocument()
    expect(screen.getByText('/Users/dana/repo')).toBeInTheDocument()
    expect(screen.getByText('to read the config')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Add folder/i })).toBeInTheDocument()
  })

  it('a stub whose reported delta has already elapsed renders as expired, not as freshly askable', () => {
    // expires_in_ms is relative to frame receipt. A delta of 0 (or already
    // negative-by-the-time-React-renders) must land on the modal's existing
    // hasExpired state, not a live countdown with a working Deny button —
    // otherwise a reconnect could show a stub approval as actionable seconds
    // after the server-side window it was actually granted has closed.
    act(() => {
      useToolApprovalStore.setState({ queue: [] })
    })

    const frame: WsSessionStateFrame = {
      type: 'session_state',
      user_id: 'user-1',
      pending_approvals: [
        {
          approval_id: 'appr-e2e-elapsed',
          session_id: 'sess-mount',
          tool_name: 'request_mount',
          agent_id: 'agent-jim',
          expires_in_ms: 0,
        },
      ],
      emitted_at: new Date().toISOString(),
    }

    act(() => {
      useToolApprovalStore.getState().reconcileWithSessionState(frame)
    })

    render(<ToolApprovalModal />)

    expect(screen.getByText('Approval expired — the agent will receive a denial.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Deny/i })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Dismiss/i })).toBeInTheDocument()
  })
})
