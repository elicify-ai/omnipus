// U3-C01–C05. Binding L1 amendment §1.2 R2/R6, §1.5; K7,
// FR-092/240/251; BDD-11.2/11.3. B3 completeness/stub removal excluded.
import { it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, act, waitFor, within } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { queryClient } from '@/lib/queryClient'
import { useToolApprovalStore } from '@/store/toolApproval'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'
import type { ToolApprovalRequiredFrame } from '@/lib/api/generated/asyncapi-types'
import { approvalPosts, cleanupApprovalState, deferred, installApprovalNetwork, installNetwork, jsonResponse, loadApprovalPanel, loadApprovalThreadTail, requests, resetSharedState } from '../../../tests/l1-ui-red-u23/support'
import type { ComponentType } from 'react'

const request: ToolApprovalRequiredFrame = {
  type: 'tool_approval_required', approval_id: 'appr-l1', tool_call_id: 'call-l1',
  tool_name: 'bash', args: { command: 'npm run build && git push', cwd: 'work' },
  agent_id: 'helper', session_id: 'helper-child', root_session_id: 'omnipus-main',
  acting_agent_name: 'Setup helper', workspace_id: 'ws-sales',
  origin_label: 'asked by Omnipus', turn_id: 'turn-l1', expires_in_ms: 600_000,
  segments: [
    { segment_index: 0, command_text: 'npm run build', resolved_binary: 'npm', classification: 'write', suggested_prefix: 'npm run build', prefix_available: true, network_required: false },
    { segment_index: 1, command_text: 'git push', resolved_binary: 'git', classification: 'none', suggested_prefix: 'git push', prefix_available: true, network_required: true },
  ],
}
let Panel: ComponentType
function mount() {
  return render(<QueryClientProvider client={queryClient}><Panel /><textarea aria-label="Chat composer" data-testid="chat-input" /></QueryClientProvider>)
}
function enqueue(frame: ToolApprovalRequiredFrame = request) {
  act(() => useToolApprovalStore.getState().enqueue(frame))
}
beforeEach(async () => {
  resetSharedState('omnipus-main')
  installApprovalNetwork()
  Panel = await loadApprovalPanel()
})
afterEach(async () => { await cleanupApprovalState() })

it('R6 composition: thread-tail mounts the root panel without locking or focusing the adjacent composer', async () => {
  const Tail = await loadApprovalThreadTail()
  render(<QueryClientProvider client={queryClient}><Tail /><textarea aria-label="Chat composer" /></QueryClientProvider>)
  const composer = screen.getByLabelText('Chat composer')
  composer.focus()
  enqueue()
  expect(screen.getByTestId('tool-approval-thread-tail')).toContainElement(screen.getByTestId('tool-approval-panel'))
  expect(composer).not.toBeDisabled()
  expect(composer).toHaveFocus()
  expect(approvalPosts()).toEqual([])
})

it('FR-092 / BDD-11.2 / R6: helper approval is inline in the Omnipus root while another workspace is active', () => {
  act(() => useWorkspacesStore.setState({ activeWorkspaceId: 'ws-other' }))
  mount()
  enqueue()
  const panel = screen.getByTestId('tool-approval-panel')
  expect(panel).toBeInTheDocument()
  expect(panel).toHaveTextContent('Setup helper')
  expect(panel).toHaveTextContent('asked by Omnipus')
  expect(screen.getByTestId('approval-open-run')).toHaveTextContent(/open run/i)
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(screen.getByTestId('command-segment-0')).toHaveTextContent('npm run build')
  expect(screen.getByTestId('command-segment-1')).toHaveTextContent('git push')
  expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('ws-other')
  expect(approvalPosts()).toEqual([])
})

it.each(['helper-child', 'other-main'])('BDD-11.3 / R6: root approval never renders in %s, even in the matching workspace', viewed => {
  act(() => {
    useWorkspacesStore.setState({ activeWorkspaceId: 'ws-sales' })
    useSessionStore.setState({ activeSessionId: viewed })
  })
  mount()
  enqueue()
  expect(screen.queryByTestId('tool-approval-panel')).not.toBeInTheDocument()
  expect(useToolApprovalStore.getState().queue.map(a => a.approvalId)).toEqual(['appr-l1'])
  expect(approvalPosts()).toEqual([])
})

it('K7 / R6: omitted root_session_id renders only in the acting session', () => {
  mount()
  enqueue({ ...request, root_session_id: undefined, session_id: 'direct-main' })
  expect(screen.queryByTestId('tool-approval-panel')).not.toBeInTheDocument()
  act(() => useSessionStore.setState({ activeSessionId: 'direct-main' }))
  expect(screen.getByTestId('tool-approval-panel')).toHaveTextContent('Setup helper')
})

it.each([
  { origin: 'while answering Anna on tg-1', room: undefined },
  { origin: 'asked by Mia', room: undefined },
  { origin: 'for group Sales', room: { room_id: 'room-sales', room_name: 'Sales', request_id: 'delivery-1', role: 'request' as const } },
])('FR-240/251 / R2/R6: receiver root displays exact server origin "$origin"', ({ origin, room }) => {
  act(() => useSessionStore.setState({ activeSessionId: 'receiver-main' }))
  mount()
  enqueue({ ...request, root_session_id: 'receiver-main', origin_label: origin, room_ref: room })
  expect(screen.getByTestId('tool-approval-panel')).toHaveTextContent(origin)
  expect(screen.getByTestId('tool-approval-panel')).toHaveTextContent('Setup helper')
  // K7 element-level pin (Train 3 CI cause F re-aim): the origin renders in its
  // own labelled element — exact text, not merely a substring somewhere in
  // the card.
  const panel = screen.getByTestId('tool-approval-panel')
  expect(within(panel).getByTestId('tool-approval-origin').textContent).toBe(origin)
  expect(approvalPosts()).toEqual([])
})

it('K7: an omitted origin renders no origin element — no empty label, no guessed origin', () => {
  act(() => useSessionStore.setState({ activeSessionId: 'receiver-main' }))
  mount()
  enqueue({ ...request, root_session_id: 'receiver-main', origin_label: undefined })
  const panel = screen.getByTestId('tool-approval-panel')
  expect(within(panel).queryByTestId('tool-approval-origin')).not.toBeInTheDocument()
  expect(within(panel).getByRole('button', { name: /^Approve Once$/i })).toBeEnabled()
  expect(approvalPosts()).toEqual([])
})

it('R6 safety floor: appearance, composer Enter and Escape preserve focus/pending state and never POST', () => {
  mount()
  const composer = screen.getByLabelText('Chat composer')
  composer.focus()
  enqueue()
  expect(composer).toHaveFocus()
  expect(composer).not.toBeDisabled()
  fireEvent.change(composer, { target: { value: 'yes approve it' } })
  fireEvent.keyDown(composer, { key: 'Enter', code: 'Enter' })
  fireEvent.keyDown(composer, { key: 'Escape', code: 'Escape' })
  fireEvent.keyDown(screen.getByTestId('tool-approval-panel'), { key: 'Escape', code: 'Escape' })
  expect(composer).toHaveFocus()
  expect(composer).toHaveValue('yes approve it')
  expect(approvalPosts()).toEqual([])
  expect(useToolApprovalStore.getState().queue.map(a => a.approvalId)).toEqual(['appr-l1'])
  expect(useToolApprovalStore.getState().resolvedIds).toEqual([])
})

it.each([
  { label: /^Approve Once$/i, action: 'allow_once' },
  { label: /^Deny$/i, action: 'deny' },
  { label: /^Cancel$/i, action: 'cancel' },
])('R6 / ADR-092 D4: explicit $action resolves only through authenticated id-keyed POST, without scope', async ({ label, action }) => {
  mount()
  enqueue()
  fireEvent.click(screen.getByRole('button', { name: label }))
  await waitFor(() => expect(useToolApprovalStore.getState().queue).toEqual([]))
  expect(approvalPosts()).toEqual([{ id: 'appr-l1', body: { action } }])
  const post = requests.find(c => c.method === 'POST')!
  expect(post.credentials).toBe('include')
  expect(post.headers.get('X-CSRF-Token')).toBe('l1-red-csrf')
  expect(useToolApprovalStore.getState().resolvedIds).toEqual(['appr-l1'])
})

it.each(['exact', 'prefix'] as const)('R6 / ADR-092 D4: Always Allow sends only the deliberately chosen bash %s scope', async scope => {
  mount()
  enqueue()
  expect(screen.getByTestId('scope-exact')).toHaveAttribute('aria-checked', 'true')
  fireEvent.click(screen.getByTestId(`scope-${scope}`))
  expect(screen.getByTestId(`scope-${scope}`)).toHaveAttribute('aria-checked', 'true')
  fireEvent.click(screen.getByRole('button', { name: /^Always Allow$/i }))
  await waitFor(() => expect(useToolApprovalStore.getState().queue).toEqual([]))
  expect(approvalPosts()).toEqual([{ id: 'appr-l1', body: { action: 'allow', scope } }])
})

it('R6 multi-tab floor: rapid clicks cannot resolve twice; confirmed resolution rejects late duplicate frame', async () => {
  const pending = deferred<Response>()
  installNetwork(() => pending.promise)
  mount()
  enqueue()
  const approve = screen.getByRole('button', { name: /^Approve Once$/i })
  fireEvent.click(approve)
  fireEvent.click(approve)
  expect(approvalPosts()).toEqual([{ id: 'appr-l1', body: { action: 'allow_once' } }])
  expect(approve).toBeDisabled()
  await act(async () => pending.resolve(jsonResponse({ approval_id: 'appr-l1', action: 'allow_once', status: 'ok' })))
  await waitFor(() => expect(useToolApprovalStore.getState().resolvedIds).toEqual(['appr-l1']))
  enqueue()
  expect(useToolApprovalStore.getState().queue).toEqual([])
  expect(screen.queryByTestId('tool-approval-panel')).not.toBeInTheDocument()
  expect(approvalPosts()).toHaveLength(1)
})

it('R6: 600-second expiry never auto-approves or copies the question-card five-minute grace timer', () => {
  vi.useFakeTimers()
  mount()
  enqueue()
  act(() => vi.advanceTimersByTime(300_000)) // question-card grace point, explicitly forbidden for approvals
  expect(approvalPosts()).toEqual([])
  expect(screen.getByRole('button', { name: /^Approve Once$/i })).not.toBeDisabled()
  act(() => vi.advanceTimersByTime(300_000)) // specified 600-second timeout
  expect(approvalPosts()).toEqual([])
  expect(screen.getByText(/expired unanswered/i)).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /^Approve Once$/i })).not.toBeInTheDocument()
  // Simulate the real server's timeout resolution at the socket boundary.
  act(() => useToolApprovalStore.getState().markResolved(request.approval_id))
  expect(screen.queryByTestId('tool-approval-panel')).not.toBeInTheDocument()
})
