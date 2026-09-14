// Unit tests for CrossWorkspaceApprovalBanner — founder decision 2026-09-14.
//
// Covers:
//  1. Hidden when there are no cross-workspace approvals (queue empty, or
//     every approval is in scope for the active workspace)
//  2. Hidden for an approval with no workspace_id (in scope everywhere)
//  3. Singular/plural copy + resolved workspace name (not a raw id)
//  4. Clicking navigates to the target workspace's chat tab
//  5. Disappears once the cross-workspace approvals resolve

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { WsToolApprovalRequiredFrame } from '@/lib/ws'

const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => mockNavigate,
}))

function makeWorkspace(id: string, name: string) {
  return {
    id,
    name,
    status: 'active' as const,
    pinned: false,
    pin_order: 0,
    task_count: 0,
    created_at: '2026-09-14T00:00:00Z',
    updated_at: '2026-09-14T00:00:00Z',
  }
}

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchWorkspaces: vi.fn().mockResolvedValue([
      makeWorkspace('ws-active', 'Home Base'),
      makeWorkspace('ws-other', 'UAT-T2'),
    ]),
  }
})

import { useToolApprovalStore } from '@/store/toolApproval'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { CrossWorkspaceApprovalBanner } from './CrossWorkspaceApprovalBanner'

function requiredFrame(
  id: string,
  extra: Partial<WsToolApprovalRequiredFrame> = {},
): WsToolApprovalRequiredFrame {
  return {
    type: 'tool_approval_required',
    approval_id: id,
    tool_call_id: `call-${id}`,
    tool_name: 'write_file',
    args: {},
    agent_id: 'agent-x',
    session_id: 'sess-x',
    turn_id: 'turn-x',
    expires_in_ms: 300_000,
    ...extra,
  }
}

function renderBanner() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <CrossWorkspaceApprovalBanner />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  act(() => {
    useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-active' })
  })
  mockNavigate.mockClear()
})

describe('CrossWorkspaceApprovalBanner', () => {
  it('renders nothing when the queue is empty', () => {
    renderBanner()
    expect(screen.queryByTestId('cross-workspace-approval-banner')).not.toBeInTheDocument()
  })

  it('renders nothing when the only pending approval is in the active workspace', () => {
    act(() => {
      useToolApprovalStore.getState().enqueue(requiredFrame('a1', { workspace_id: 'ws-active' }))
    })
    renderBanner()
    expect(screen.queryByTestId('cross-workspace-approval-banner')).not.toBeInTheDocument()
  })

  it('renders nothing for an approval with no workspace_id (in scope everywhere)', () => {
    act(() => {
      useToolApprovalStore.getState().enqueue(requiredFrame('a1', { workspace_id: undefined }))
    })
    renderBanner()
    expect(screen.queryByTestId('cross-workspace-approval-banner')).not.toBeInTheDocument()
  })

  it('shows singular copy with the resolved workspace name for one cross-workspace approval', async () => {
    act(() => {
      useToolApprovalStore.getState().enqueue(requiredFrame('a1', { workspace_id: 'ws-other' }))
    })
    renderBanner()
    // The banner itself renders as soon as the queue has a cross-workspace
    // entry (it falls back to the raw id until the workspace list resolves)
    // — wait for the NAME text specifically, not just the testid, so this
    // assertion can't race the fetchWorkspaces() promise.
    await waitFor(() =>
      expect(screen.getByText('1 approval waiting in UAT-T2')).toBeInTheDocument(),
    )
    // Never the raw id when the name is available.
    expect(screen.queryByText(/ws-other/)).not.toBeInTheDocument()
  })

  it('shows plural copy for several approvals in the same other workspace', async () => {
    act(() => {
      useToolApprovalStore.getState().enqueue(requiredFrame('a1', { workspace_id: 'ws-other' }))
      useToolApprovalStore.getState().enqueue(requiredFrame('a2', { workspace_id: 'ws-other' }))
    })
    renderBanner()
    await waitFor(() =>
      expect(screen.getByText('2 approvals waiting in UAT-T2')).toBeInTheDocument(),
    )
  })

  it('clicking navigates to the target workspace and sets it active', async () => {
    act(() => {
      useToolApprovalStore.getState().enqueue(requiredFrame('a1', { workspace_id: 'ws-other' }))
    })
    renderBanner()
    const button = await screen.findByTestId('cross-workspace-approval-banner')
    fireEvent.click(button)
    expect(mockNavigate).toHaveBeenCalledWith({
      to: '/workspaces/$workspaceId/chat',
      params: { workspaceId: 'ws-other' },
    })
    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('ws-other')
  })

  it('disappears once the cross-workspace approvals resolve', async () => {
    act(() => {
      useToolApprovalStore.getState().enqueue(requiredFrame('a1', { workspace_id: 'ws-other' }))
    })
    renderBanner()
    await waitFor(() =>
      expect(screen.getByTestId('cross-workspace-approval-banner')).toBeInTheDocument(),
    )
    act(() => {
      useToolApprovalStore.getState().markResolved('a1')
    })
    await waitFor(() =>
      expect(screen.queryByTestId('cross-workspace-approval-banner')).not.toBeInTheDocument(),
    )
  })
})
