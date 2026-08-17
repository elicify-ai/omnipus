// Unit tests for ToolApprovalModal — FR-011, FR-082, FR-052
//
// Tests:
//  1. Modal does not render when queue is empty
//  2. Modal renders when queue has an entry
//  3. Approve button calls POST /api/v1/tool-approvals/{id} with action:"approve"
//  4. Deny button calls POST with action:"deny"
//  5. Cancel button calls POST with action:"cancel"
//  5b. Always Allow button calls POST with action:"always" (ADR-036 §3.4 gap closure)
//  6. On 410 response, modal entry is dismissed without a toast
//  7. On 403 response, shows admin-required toast
//  8. On 401 response, shows re-auth toast
//  9. Countdown uses expires_in_ms — expiresAt computed as Date.now() + expires_in_ms
// 10. session_state reconciliation removes stale approvals

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { act } from 'react'
import type { ToolApprovalRequiredFrame } from '@/lib/api/generated/asyncapi-types'

// We mock submitToolApproval but pass-through ApiError + isApiError so the
// component's `isApiError(err)` branch matches errors thrown from inside the
// mock implementation. Without re-exporting them, the mock would shadow them
// with undefined and crash on any thrown error.
vi.mock('@/lib/api', async () => {
  const actual = await vi.importActual<typeof import('@/lib/api')>('@/lib/api')
  return {
    ...actual,
    submitToolApproval: vi.fn(),
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn((selector) => {
    const state = {
      addToast: vi.fn(),
      toasts: [],
      removeToast: vi.fn(),
    }
    return selector ? selector(state) : state
  }),
}))

// D9: forceLogout is mocked so a 401 in these tests doesn't exercise the real
// sessionStorage/hash-redirect side effects — just asserts the call happened.
const mockForceLogout = vi.fn()
vi.mock('@/lib/authLogout', () => ({
  forceLogout: (...args: unknown[]) => mockForceLogout(...args),
}))

import * as api from '@/lib/api'
import { useToolApprovalStore } from '@/store/toolApproval'
import { ToolApprovalModal } from './ToolApprovalModal'
import type { WsSessionStateFrame } from '@/lib/ws'

// Capture the mock addToast for assertion
let capturedAddToast: ReturnType<typeof vi.fn>

beforeEach(async () => {
  capturedAddToast = vi.fn()
  const { useUiStore } = await import('@/store/ui')
  vi.mocked(useUiStore).mockImplementation(((selector?: (s: unknown) => unknown) => {
    const state = { addToast: capturedAddToast, toasts: [], removeToast: vi.fn() }
    return selector ? selector(state) : state
  }) as unknown as typeof useUiStore)

  // Reset store
  act(() => {
    useToolApprovalStore.setState({ queue: [] })
  })
  vi.clearAllMocks()
  vi.mocked(api.submitToolApproval).mockResolvedValue({
    approval_id: 'appr-001',
    action: 'approve',
    status: 'ok',
  })
})

const SAMPLE_APPROVAL = {
  approvalId: 'appr-001',
  toolCallId: 'call-001',
  toolName: 'fetch_url',
  args: { url: 'https://example.com' },
  agentId: 'agent-main',
  sessionId: 'sess-001',
  turnId: 'turn-001',
  expiresAt: Date.now() + 300_000, // 5 minutes
}

describe('ToolApprovalModal — rendering', () => {
  it('renders nothing when the queue is empty', () => {
    const { container } = render(<ToolApprovalModal />)
    expect(container.firstChild).toBeNull()
  })

  it('renders the modal when the queue has an entry', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalModal />)
    // Deliverable 3: the modal shows the HUMAN tool name (humanizeToolName),
    // not the raw wire identifier — 'fetch_url' has an EXPLICIT_LABELS entry
    // ('Fetch URL'), so the raw id should no longer appear as the Tool label.
    expect(screen.getByText('Fetch URL')).toBeInTheDocument()
    expect(screen.queryByText('fetch_url')).not.toBeInTheDocument()
    expect(screen.getByText('Tool Approval Required')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Approve/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Deny/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Always Allow/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Cancel/i })).toBeInTheDocument()
  })

  it('shows the tool args in the modal', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalModal />)
    expect(screen.getByText(/https:\/\/example\.com/)).toBeInTheDocument()
  })

  it('shows queue depth indicator when multiple approvals are pending', () => {
    act(() => {
      useToolApprovalStore.setState({
        queue: [
          SAMPLE_APPROVAL,
          { ...SAMPLE_APPROVAL, approvalId: 'appr-002', toolName: 'exec' },
        ],
      })
    })
    render(<ToolApprovalModal />)
    expect(screen.getByText('+1 more')).toBeInTheDocument()
  })
})

// ADR-036 §3.4: the dedicated exec-only approval flow (ExecApprovalBlock) was
// retired in favor of this generic modal. Its readable command preview
// (binary highlighted, env-prefix separated, working-dir line) was ported
// here for `bash` calls — verify it renders, in addition to the generic
// Arguments JSON dump, and does NOT render for other tools.
describe('ToolApprovalModal — bash command preview (ADR-036 §3.4 port)', () => {
  const BASH_APPROVAL = {
    approvalId: 'appr-bash-001',
    toolCallId: 'call-bash-001',
    toolName: 'bash',
    args: { command: 'FOO=bar npm run build', cwd: 'apps/web', run_in_background: false },
    agentId: 'agent-main',
    sessionId: 'sess-001',
    turnId: 'turn-001',
    expiresAt: Date.now() + 300_000,
  }

  it('renders a Command preview with the binary highlighted and env prefix separated', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [BASH_APPROVAL] })
    })
    render(<ToolApprovalModal />)
    expect(screen.getByText('Command')).toBeInTheDocument()
    expect(screen.getByText('FOO=bar')).toBeInTheDocument()
    expect(screen.getByText('npm')).toBeInTheDocument()
  })

  it('renders the working directory from args.cwd', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [BASH_APPROVAL] })
    })
    render(<ToolApprovalModal />)
    expect(screen.getByText('apps/web')).toBeInTheDocument()
  })

  it('still renders the generic Arguments JSON dump alongside the preview', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [BASH_APPROVAL] })
    })
    render(<ToolApprovalModal />)
    expect(screen.getByText('Arguments')).toBeInTheDocument()
    expect(screen.getByText(/run_in_background/)).toBeInTheDocument()
  })

  it('does NOT render a Command preview for a non-bash tool', () => {
    act(() => {
      useToolApprovalStore.setState({
        queue: [{ ...SAMPLE_APPROVAL, args: { command: 'not actually bash' } }],
      })
    })
    render(<ToolApprovalModal />)
    expect(screen.queryByText('Command')).not.toBeInTheDocument()
  })

  it('does NOT render a Command preview when bash args have no command string', () => {
    act(() => {
      useToolApprovalStore.setState({
        queue: [{ ...BASH_APPROVAL, args: { run_in_background: true } }],
      })
    })
    render(<ToolApprovalModal />)
    expect(screen.queryByText('Command')).not.toBeInTheDocument()
  })
})

describe('ToolApprovalModal — button dispatch', () => {
  it('Approve button calls submitToolApproval with action:"approve"', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    fireEvent.click(screen.getByRole('button', { name: /Approve/i }))

    await waitFor(() => {
      expect(api.submitToolApproval).toHaveBeenCalledWith('appr-001', 'approve')
    })
  })

  it('Deny button calls submitToolApproval with action:"deny"', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    fireEvent.click(screen.getByRole('button', { name: /Deny/i }))

    await waitFor(() => {
      expect(api.submitToolApproval).toHaveBeenCalledWith('appr-001', 'deny')
    })
  })

  it('Cancel button calls submitToolApproval with action:"cancel"', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    fireEvent.click(screen.getByRole('button', { name: /^Cancel$/i }))

    await waitFor(() => {
      expect(api.submitToolApproval).toHaveBeenCalledWith('appr-001', 'cancel')
    })
  })

  // ADR-036 §3.4 gap closure: the retired ExecApprovalBlock's 3-way decision
  // (Allow / Deny / "Always Allow") is now fully reachable from the generic
  // modal for every tool, not just bash.
  it('Always Allow button calls submitToolApproval with action:"always"', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    fireEvent.click(screen.getByRole('button', { name: /Always Allow/i }))

    await waitFor(() => {
      expect(api.submitToolApproval).toHaveBeenCalledWith('appr-001', 'always')
    })
  })

  it('toasts when Always Allow approved this call but the grant did not stick', async () => {
    vi.mocked(api.submitToolApproval).mockResolvedValue({
      approval_id: 'appr-001',
      action: 'always',
      status: 'ok',
      grant_recorded: false,
    })
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    fireEvent.click(screen.getByRole('button', { name: /Always Allow/i }))

    await waitFor(() => {
      expect(capturedAddToast).toHaveBeenCalledWith({
        message: 'This call is allowed, but Always Allow did not stick. The next identical call will ask again.',
        variant: 'warning',
      })
    })
    expect(useToolApprovalStore.getState().queue).toHaveLength(0)
  })

  it('toasts when Always Allow omits grant_recorded — treated as did-not-stick', async () => {
    vi.mocked(api.submitToolApproval).mockResolvedValue({
      approval_id: 'appr-001',
      action: 'always',
      status: 'ok',
    })
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    fireEvent.click(screen.getByRole('button', { name: /Always Allow/i }))

    await waitFor(() => {
      expect(capturedAddToast).toHaveBeenCalledWith({
        message: 'This call is allowed, but Always Allow did not stick. The next identical call will ask again.',
        variant: 'warning',
      })
    })
    expect(useToolApprovalStore.getState().queue).toHaveLength(0)
  })

  it('removes approval from queue after successful Approve', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    fireEvent.click(screen.getByRole('button', { name: /Approve/i }))

    await waitFor(() => {
      expect(useToolApprovalStore.getState().queue).toHaveLength(0)
    })
  })

  it('removes approval from queue after successful Always Allow', async () => {
    vi.mocked(api.submitToolApproval).mockResolvedValue({
      approval_id: 'appr-001',
      action: 'always',
      status: 'ok',
      grant_recorded: true,
    })
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    fireEvent.click(screen.getByRole('button', { name: /Always Allow/i }))

    await waitFor(() => {
      expect(useToolApprovalStore.getState().queue).toHaveLength(0)
    })
    expect(capturedAddToast).not.toHaveBeenCalled()
  })
})

describe('ToolApprovalModal — a11y safe-default contract (C2)', () => {
  // This is a SECURITY-CRITICAL control. Two invariants must hold so a stray
  // keypress can never auto-approve a tool call:
  //   1. Escape resolves to DENY (the safe default), never approve, never a
  //      silent dismiss that leaves the agent hanging.
  //   2. Default keyboard focus lands on the Deny button on open, so a stray
  //      Enter on open denies rather than approves.

  it('Escape triggers DENY (the safe default), never approve', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    // Radix Dialog listens for Escape on the document via the dialog content.
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape', code: 'Escape' })

    await waitFor(() => {
      expect(api.submitToolApproval).toHaveBeenCalledWith('appr-001', 'deny')
    })
    // The safe default must NEVER be an approve.
    expect(api.submitToolApproval).not.toHaveBeenCalledWith('appr-001', 'approve')
  })

  it('lands default focus on the Deny button when the modal opens', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    const denyButton = screen.getByRole('button', { name: /Deny/i })
    // onOpenAutoFocus moves focus to Deny (overriding Radix's first-focusable
    // heuristic which would land on the X close button or Approve).
    await waitFor(() => {
      expect(denyButton).toHaveFocus()
    })
    // Defence-in-depth: focus is NOT on Approve.
    expect(screen.getByRole('button', { name: /Approve/i })).not.toHaveFocus()
  })
})

describe('ToolApprovalModal — error handling', () => {
  it('dismisses silently on 410 Gone (already resolved)', async () => {
    vi.mocked(api.submitToolApproval).mockRejectedValue(new api.ApiError(410, 'Gone'))

    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    fireEvent.click(screen.getByRole('button', { name: /Deny/i }))

    await waitFor(() => {
      // Entry removed from queue on 410
      expect(useToolApprovalStore.getState().queue).toHaveLength(0)
    })
    // No toast should be shown for 410
    expect(capturedAddToast).not.toHaveBeenCalled()
  })

  it('shows admin-required toast on 403', async () => {
    vi.mocked(api.submitToolApproval).mockRejectedValue(new api.ApiError(403, 'Forbidden'))

    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    fireEvent.click(screen.getByRole('button', { name: /Approve/i }))

    await waitFor(() => {
      expect(capturedAddToast).toHaveBeenCalledWith(
        expect.objectContaining({
          message: expect.stringMatching(/must be an admin/i),
          variant: 'error',
        })
      )
    })
    // Approval stays in queue on 403 (non-dismissive error)
    expect(useToolApprovalStore.getState().queue).toHaveLength(1)
  })

  it('shows re-auth toast on 401', async () => {
    vi.mocked(api.submitToolApproval).mockRejectedValue(new api.ApiError(401, 'Unauthorized'))

    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    fireEvent.click(screen.getByRole('button', { name: /Approve/i }))

    await waitFor(() => {
      expect(capturedAddToast).toHaveBeenCalledWith(
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
  it('D9: routes a 401 through the shared forceLogout() teardown', async () => {
    vi.mocked(api.submitToolApproval).mockRejectedValue(new api.ApiError(401, 'Unauthorized'))

    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    fireEvent.click(screen.getByRole('button', { name: /Approve/i }))

    await waitFor(() => {
      expect(mockForceLogout).toHaveBeenCalledOnce()
    })
  })

  it('D9: does NOT call forceLogout() on 403 (forbidden, not unauthenticated)', async () => {
    vi.mocked(api.submitToolApproval).mockRejectedValue(new api.ApiError(403, 'Forbidden'))

    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    fireEvent.click(screen.getByRole('button', { name: /Approve/i }))

    await waitFor(() => {
      expect(capturedAddToast).toHaveBeenCalled()
    })
    expect(mockForceLogout).not.toHaveBeenCalled()
  })
})

describe('ToolApprovalModal — expires_in_ms countdown (FR-082)', () => {
  it('expiresAt is computed as Date.now() + expires_in_ms on enqueue', () => {
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

describe('ToolApprovalModal — session_state reset handler (FR-052, FR-073, FR-081)', () => {
  it('removes stale approvals not present in session_state', () => {
    act(() => {
      useToolApprovalStore.setState({
        queue: [
          { ...SAMPLE_APPROVAL, approvalId: 'appr-stale-1' },
          { ...SAMPLE_APPROVAL, approvalId: 'appr-stale-2' },
          { ...SAMPLE_APPROVAL, approvalId: 'appr-live' },
        ],
      })
    })

    const sessionStateFrame: WsSessionStateFrame = {
      type: 'session_state',
      user_id: 'user-1',
      pending_approvals: [
        {
          approval_id: 'appr-live',
          session_id: 'sess-001',
          tool_name: 'fetch_url',
          agent_id: 'agent-main',
          expires_in_ms: 299_000,
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

  it('refreshes expiresAt for approvals present in session_state', () => {
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
            session_id: 'sess-001',
            tool_name: 'fetch_url',
            agent_id: 'agent-main',
            expires_in_ms: newExpiresInMs,
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

  it('clears all approvals when session_state has empty pending_approvals', () => {
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
// These tests verify that ToolApprovalModal does not crash when a valid-shape
// but degenerate-value ToolApprovalRequiredFrame reaches the component. They
// mirror the exact failure case from the Ava-chat bug (args: {}) and extend to
// unicode keys, very long strings, nested objects, null values, and timing edge
// cases. Props are constructed from the generated ToolApprovalRequiredFrame
// type — no hand-written wire types.
//
// Each test: enqueue the frame → render ToolApprovalModal → assert no throw.

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
  'ToolApprovalModal renders "%s" without throwing',
  (_label, overrides) => {
    it('renders without throwing', () => {
      const frame: ToolApprovalRequiredFrame = { ...baseFrame, ...overrides }
      act(() => {
        useToolApprovalStore.setState({ queue: [] })
        useToolApprovalStore.getState().enqueue(frame)
      })
      expect(() => render(<ToolApprovalModal />)).not.toThrow()
    })
  },
)

// ── "Always Allow" for request_mount ────────────────────────────────────────
//
// Grants remember the whole arguments object, so Always Allow on Add folder
// means "this folder, this session" — not "any folder". The button is shown
// whenever we have real args. It stays hidden only on reconnect stubs, which
// have no arguments to remember.

const MOUNT_APPROVAL = {
  approvalId: 'appr-mount',
  toolCallId: 'call-mount',
  toolName: 'request_mount',
  args: { host_path: '/Users/dana/Documents/projects/api', reason: 'to run the build' },
  agentId: 'agent-main',
  sessionId: 'sess-001',
  turnId: 'turn-001',
  expiresAt: Date.now() + 300_000,
}

describe('ToolApprovalModal — Always Allow for request_mount', () => {
  it('offers Always Allow for an ordinary tool', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [SAMPLE_APPROVAL] })
    })
    render(<ToolApprovalModal />)
    expect(screen.getByTestId('always-allow-toggle')).toBeInTheDocument()
  })

  it('offers Always Allow for request_mount when args are present', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalModal />)
    expect(screen.getByTestId('always-allow-toggle')).toBeInTheDocument()
  })

  it('hides Always Allow for request_mount when no folder path is present', () => {
    act(() => {
      useToolApprovalStore.setState({
        queue: [{ ...MOUNT_APPROVAL, args: { reason: 'UAT folder A' } }],
      })
    })
    render(<ToolApprovalModal />)
    expect(screen.queryByTestId('always-allow-toggle')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Add folder|Approve/i })).toBeInTheDocument()
  })

  it('still offers a way to approve and a way to deny request_mount', () => {
    // Always Allow is offered when args are present (a grant is this folder,
    // this session). Approve and Deny must still be there either way.
    //
    // request_mount's copy diverges from the generic Approve/Deny labels
    // (operator-approved "Add folder" / "Don't add" — see
    // approvalPreviews/registry.ts and RequestMountApprovalPreview.tsx) but
    // still dispatches the same 'approve'/'deny' actions — covered by the
    // dedicated ToolApprovalModal.readablePreviews.test.tsx suite. This test
    // only asserts BOTH decision buttons are present, whatever their label.
    act(() => {
      useToolApprovalStore.setState({ queue: [MOUNT_APPROVAL] })
    })
    render(<ToolApprovalModal />)
    expect(screen.getByRole('button', { name: /Add folder/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Don't add/i })).toBeInTheDocument()
  })
})
