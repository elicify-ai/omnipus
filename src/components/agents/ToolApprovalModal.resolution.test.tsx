// ToolApprovalModal — an approval the server no longer holds must never stick
// (UAT 2026-09-14: Approve / Deny / Cancel / Close all toasted "Failed to
// submit approval: The requested resource was not found" and the dialog
// stayed, re-surfacing on every navigation and in other workspaces).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { act } from 'react'

vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api')
  return {
    ...actual,
    submitToolApproval: vi.fn(),
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(),
}))

vi.mock('@/lib/authLogout', () => ({
  forceLogout: vi.fn(),
}))

import * as api from '@/lib/api'
import { useToolApprovalStore, type PendingToolApproval } from '@/store/toolApproval'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { ToolApprovalModal } from './ToolApprovalModal'

let addToast: ReturnType<typeof vi.fn>

const STUCK: PendingToolApproval = {
  approvalId: 'appr-stuck',
  toolCallId: 'call-1',
  toolName: 'write_file',
  args: { path: 'e3-marker.txt' },
  agentId: 'agent-deleted',
  sessionId: 'sess-1',
  turnId: 'turn-1',
  expiresAt: Date.now() + 300_000,
}

const queue = () => useToolApprovalStore.getState().queue
const resolvedIds = () => useToolApprovalStore.getState().resolvedIds

beforeEach(async () => {
  vi.clearAllMocks()
  addToast = vi.fn()
  const { useUiStore } = await import('@/store/ui')
  vi.mocked(useUiStore).mockImplementation(((selector?: (s: unknown) => unknown) => {
    const state = { addToast, toasts: [], removeToast: vi.fn() }
    return selector ? selector(state) : state
  }) as unknown as typeof useUiStore)
  act(() => {
    useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
    useWorkspacesStore.setState({ activeWorkspaceId: null })
  })
})

function show(...entries: PendingToolApproval[]) {
  act(() => {
    useToolApprovalStore.setState({ queue: entries })
  })
  render(<ToolApprovalModal />)
}

describe('ToolApprovalModal — an approval the server no longer holds (404 / 410)', () => {
  it('404 on Deny: the card goes, is remembered as resolved, and no error toast appears', async () => {
    vi.mocked(api.submitToolApproval).mockRejectedValue(new api.ApiError(404, 'Not Found'))
    show(STUCK)

    fireEvent.click(screen.getByRole('button', { name: /^Deny$/ }))

    await waitFor(() => expect(queue()).toHaveLength(0))
    expect(resolvedIds()).toContain('appr-stuck')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(addToast).not.toHaveBeenCalled()
  })

  it('404 on Approve: the card goes, with a warning that the approval was not applied', async () => {
    vi.mocked(api.submitToolApproval).mockRejectedValue(new api.ApiError(404, 'Not Found'))
    show(STUCK)

    fireEvent.click(screen.getByRole('button', { name: /^Approve$/ }))

    await waitFor(() => expect(queue()).toHaveLength(0))
    expect(resolvedIds()).toContain('appr-stuck')
    expect(addToast).toHaveBeenCalledTimes(1)
    expect(addToast).toHaveBeenCalledWith(
      expect.objectContaining({ variant: 'warning', message: expect.stringMatching(/not applied/i) }),
    )
  })

  it('410 on Always Allow: same dismissal and warning as a 404', async () => {
    vi.mocked(api.submitToolApproval).mockRejectedValue(new api.ApiError(410, 'Gone'))
    show(STUCK)

    fireEvent.click(screen.getByRole('button', { name: /Always Allow/ }))

    await waitFor(() => expect(queue()).toHaveLength(0))
    expect(resolvedIds()).toContain('appr-stuck')
    expect(addToast).toHaveBeenCalledWith(expect.objectContaining({ variant: 'warning' }))
  })

  it('a successful decision is remembered as resolved, not merely hidden', async () => {
    vi.mocked(api.submitToolApproval).mockResolvedValue({
      approval_id: 'appr-stuck',
      action: 'deny',
      status: 'ok',
    })
    show(STUCK)

    fireEvent.click(screen.getByRole('button', { name: /^Deny$/ }))

    await waitFor(() => expect(queue()).toHaveLength(0))
    expect(resolvedIds()).toContain('appr-stuck')
  })

  it('a 500 on Approve keeps the card so the decision can be retried', async () => {
    vi.mocked(api.submitToolApproval).mockRejectedValue(new api.ApiError(500, 'boom'))
    show(STUCK)

    fireEvent.click(screen.getByRole('button', { name: /^Approve$/ }))

    await waitFor(() => expect(addToast).toHaveBeenCalledWith(expect.objectContaining({ variant: 'error' })))
    expect(queue()).toHaveLength(1)
    expect(resolvedIds()).toEqual([])
  })
})

describe('ToolApprovalModal — Close and Cancel always dismiss locally', () => {
  it('the X close button dismisses even when the deny request fails', async () => {
    vi.mocked(api.submitToolApproval).mockRejectedValue(new api.ApiError(500, 'boom'))
    show(STUCK)

    fireEvent.click(screen.getByRole('button', { name: /close/i }))

    await waitFor(() => expect(api.submitToolApproval).toHaveBeenCalledWith('appr-stuck', 'deny'))
    expect(queue()).toHaveLength(0)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    // The deny did not land, so this is NOT a confirmed resolution: a later
    // snapshot may legitimately bring the still-pending approval back.
    await waitFor(() => expect(addToast).toHaveBeenCalled())
    expect(resolvedIds()).toEqual([])
  })

  it('Escape dismisses even when the network is down', async () => {
    vi.mocked(api.submitToolApproval).mockRejectedValue(new api.ApiError(0, 'Network unavailable.'))
    show(STUCK)

    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape', code: 'Escape' })

    await waitFor(() => expect(api.submitToolApproval).toHaveBeenCalledWith('appr-stuck', 'deny'))
    expect(queue()).toHaveLength(0)
  })

  it('Cancel dismisses even when the server answers 404', async () => {
    vi.mocked(api.submitToolApproval).mockRejectedValue(new api.ApiError(404, 'Not Found'))
    show(STUCK)

    fireEvent.click(screen.getByRole('button', { name: /^Cancel$/ }))

    await waitFor(() => expect(api.submitToolApproval).toHaveBeenCalledWith('appr-stuck', 'cancel'))
    expect(queue()).toHaveLength(0)
    await waitFor(() => expect(resolvedIds()).toContain('appr-stuck'))
  })
})

describe('ToolApprovalModal — workspace scope', () => {
  it('does not show an approval from another workspace, but keeps it queued', () => {
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-b' })
    })
    show({ ...STUCK, workspaceId: 'ws-a' })

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(queue()).toHaveLength(1)
  })

  it('shows it once its own workspace becomes active', () => {
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-b' })
    })
    show({ ...STUCK, workspaceId: 'ws-a' })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()

    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-a' })
    })
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('shows an approval that belongs to no workspace in any workspace', () => {
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-b' })
    })
    show(STUCK)
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('shows the in-scope approval first and counts only in-scope approvals', () => {
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-a' })
    })
    show(
      { ...STUCK, approvalId: 'appr-other-ws', workspaceId: 'ws-b', toolName: 'bash', args: { command: 'ls' } },
      { ...STUCK, approvalId: 'appr-here-1', workspaceId: 'ws-a' },
      { ...STUCK, approvalId: 'appr-here-2', workspaceId: 'ws-a' },
    )
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(document.getElementById('tool-approval-title-appr-here-1')).not.toBeNull()
    expect(screen.getByText('+1 more')).toBeInTheDocument()
  })
})
