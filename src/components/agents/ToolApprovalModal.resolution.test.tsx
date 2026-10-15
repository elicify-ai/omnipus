// L1 amendment §1.2 R6 / §1.5: inline container replaces the modal.
// All non-container preview/action/security oracles retained; B3 stub deletion deferred.
// ToolApprovalPanel — an approval the server no longer holds must never stick
// (UAT 2026-09-14: Approve / Deny / Cancel / Close all toasted "Failed to
// submit approval: The requested resource was not found" and the dialog
// stayed, re-surfacing on every navigation and in other workspaces).

import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import { act } from 'react'

import { useToolApprovalStore, type PendingToolApproval } from '@/store/toolApproval'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useSessionStore } from '@/store/session'
import type { ComponentType } from 'react'
import { renderWithClient as render, loadApprovalPanel, resetSharedState, installApprovalNetwork, setApprovalReply, setApprovalOffline, approvalPosts, toasts, cleanupApprovalState } from '../../../tests/l1-ui-red-u23/support'
let ToolApprovalPanel: ComponentType

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
  resetSharedState('sess-1')
  installApprovalNetwork()
  ToolApprovalPanel = await loadApprovalPanel()
})

function show(...entries: PendingToolApproval[]) {
  act(() => {
    useToolApprovalStore.setState({ queue: entries })
  })
  render(<ToolApprovalPanel />)
}

describe('ToolApprovalPanel — an approval the server no longer holds (404 / 410)', () => {
  it('L1 §1.2 R6: 404 on Deny: the card goes, is remembered as resolved, and no error toast appears', async () => {
    setApprovalReply({ error: 'Not Found' }, 404)
    show(STUCK)

    fireEvent.click(screen.getByRole('button', { name: /^Deny$/ }))

    await waitFor(() => expect(queue()).toHaveLength(0))
    expect(resolvedIds()).toContain('appr-stuck')
    expect(screen.queryByTestId('tool-approval-panel')).not.toBeInTheDocument()
    expect(toasts()).toEqual([])
  })

  it('L1 §1.2 R6: 404 on Approve: the card goes, with a warning that the approval was not applied', async () => {
    setApprovalReply({ error: 'Not Found' }, 404)
    show(STUCK)

    fireEvent.click(screen.getByRole('button', { name: /^Approve Once$/ }))

    await waitFor(() => expect(queue()).toHaveLength(0))
    expect(resolvedIds()).toContain('appr-stuck')
    expect(toasts()).toHaveLength(1)
    expect(toasts()).toContainEqual(
      expect.objectContaining({ variant: 'warning', message: expect.stringMatching(/not applied/i) }),
    )
  })

  it('L1 §1.2 R6: 410 on Always Allow: same dismissal and warning as a 404', async () => {
    setApprovalReply({ error: 'Gone' }, 410)
    show(STUCK)

    fireEvent.click(screen.getByRole('button', { name: /Always Allow/ }))

    await waitFor(() => expect(queue()).toHaveLength(0))
    expect(resolvedIds()).toContain('appr-stuck')
    expect(toasts()).toContainEqual(expect.objectContaining({ variant: 'warning' }))
  })

  it('L1 §1.2 R6: a successful decision is remembered as resolved, not merely hidden', async () => {
    setApprovalReply({
      approval_id: 'appr-stuck',
      action: 'deny',
      status: 'ok',
    })
    show(STUCK)

    fireEvent.click(screen.getByRole('button', { name: /^Deny$/ }))

    await waitFor(() => expect(queue()).toHaveLength(0))
    expect(resolvedIds()).toContain('appr-stuck')
  })

  it('L1 §1.2 R6: a 500 on Approve keeps the card so the decision can be retried', async () => {
    setApprovalReply({ error: 'boom' }, 500)
    show(STUCK)

    fireEvent.click(screen.getByRole('button', { name: /^Approve Once$/ }))

    await waitFor(() => expect(toasts()).toContainEqual(expect.objectContaining({ variant: 'error' })))
    expect(queue()).toHaveLength(1)
    expect(resolvedIds()).toEqual([])
  })
})

describe('ToolApprovalPanel — explicit decisions, not dialog dismissal (amendment R6)', () => {
  // Binding: "resolves only through POST ... Escape does not resolve". There
  // is no dialog X; explicit Deny still goes through the same failure path.
  it('L1 §1.2 R6: no modal X; failed explicit Deny remains unconfirmed with a visible error', async () => {
    setApprovalReply({ error: 'boom' }, 500)
    show(STUCK)
    expect(screen.queryByRole('button', { name: /close/i })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /^Deny$/ }))
    await waitFor(() => expect(approvalPosts()).toEqual([{ id: 'appr-stuck', body: { action: 'deny' } }]))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    await waitFor(() => expect(toasts()).toContainEqual(expect.objectContaining({ variant: 'error' })))
    expect(queue()).toHaveLength(1)
    expect(resolvedIds()).toEqual([])
  })

  it('L1 §1.2 R6: Escape never sends or dismisses even when the network is down', () => {
    setApprovalOffline()
    show(STUCK)
    fireEvent.keyDown(screen.getByTestId('tool-approval-panel'), { key: 'Escape', code: 'Escape' })
    expect(approvalPosts()).toEqual([])
    expect(queue().map(a => a.approvalId)).toEqual(['appr-stuck'])
    expect(resolvedIds()).toEqual([])
  })

  it('L1 §1.2 R6: Cancel dismisses even when the server answers 404', async () => {
    setApprovalReply({ error: 'Not Found' }, 404)
    show(STUCK)

    fireEvent.click(screen.getByRole('button', { name: /^Cancel$/ }))

    await waitFor(() => expect(approvalPosts()).toContainEqual({ id: 'appr-stuck', body: { action: 'cancel' } }))
    expect(queue()).toHaveLength(0)
    await waitFor(() => expect(resolvedIds()).toContain('appr-stuck'))
  })
})

describe('ToolApprovalPanel — owning-chat scope (amendment §1.2 R6 / BDD-11.3)', () => {
  // Binding quote: "chat whose session id equals root_session_id (or
  // session_id when ... omitted)"; active workspace alone never grants scope.
  it('L1 §1.2 R6: another chat remains hidden but queued, even in the same workspace', () => {
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-a' })
      useSessionStore.setState({ activeSessionId: 'another-chat' })
    })
    show({ ...STUCK, workspaceId: 'ws-a' })
    expect(screen.queryByTestId('tool-approval-panel')).not.toBeInTheDocument()
    expect(queue()).toHaveLength(1)
  })

  it('L1 §1.2 R6: shows it when its owning chat opens regardless of active workspace', () => {
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-b' })
      useSessionStore.setState({ activeSessionId: 'another-chat' })
    })
    show({ ...STUCK, workspaceId: 'ws-a' })
    expect(screen.queryByTestId('tool-approval-panel')).not.toBeInTheDocument()
    act(() => useSessionStore.setState({ activeSessionId: 'sess-1' }))
    expect(screen.getByTestId('tool-approval-panel')).toBeInTheDocument()
    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('ws-b')
  })

  it('L1 §1.2 R6: no workspace is not global chat scope; omitted root uses the acting session', () => {
    act(() => {
      useWorkspacesStore.setState({ activeWorkspaceId: 'ws-b' })
      useSessionStore.setState({ activeSessionId: 'another-chat' })
    })
    show(STUCK)
    expect(screen.queryByTestId('tool-approval-panel')).not.toBeInTheDocument()
    act(() => useSessionStore.setState({ activeSessionId: 'sess-1' }))
    expect(screen.getByTestId('tool-approval-panel')).toBeInTheDocument()
    expect(queue()).toHaveLength(1)
  })

  it('L1 §1.2 R6: shows the front owning-chat approval and counts only that chat’s queue', () => {
    act(() => useWorkspacesStore.setState({ activeWorkspaceId: 'ws-a' }))
    show(
      { ...STUCK, approvalId: 'appr-other-chat', sessionId: 'other-chat', workspaceId: 'ws-a', toolName: 'bash', args: { command: 'ls' } },
      { ...STUCK, approvalId: 'appr-here-1', workspaceId: 'ws-b' },
      { ...STUCK, approvalId: 'appr-here-2', workspaceId: 'ws-a' },
    )
    expect(screen.getByTestId('tool-approval-panel')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^Deny$/ })).toBeInTheDocument()
    expect(document.getElementById('tool-approval-title-appr-here-1')).not.toBeNull()
    expect(screen.getByText('+1 more')).toBeInTheDocument()
    expect(screen.queryByText('ls')).not.toBeInTheDocument()
    expect(queue()).toHaveLength(3)
  })
})

// Real stores/QueryClient/auth survive the test boundary; only fetch/socket doubled.
afterEach(async () => { await cleanupApprovalState() })
