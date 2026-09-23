// ToolApprovalModal.scopeAndSegments.test.tsx
//
// ADR-092 D4/FR-024/FR-025/FR-027 coverage — the two pieces of the approval
// dialog that were previously left as "still open work in this lane" (see
// the note atop ToolApprovalModal.tsx):
//
//  1. Grant scope (exact vs. prefix). A shell (`bash`) approval offers a
//     scope choice; the choice actually sent to the server on Always Allow
//     must match what the user picked. Every other action (Approve/Deny) —
//     and every other tool's Always Allow — must NOT send a scope at all.
//  2. Per-segment display. When the frame carries
//     ToolApprovalRequiredFrame.segments (a chained command), each segment's
//     command text renders as its own, separately-readable block, with the
//     resolved binary distinguished from the rest of the text.
//
// These tests fail (not just "would fail" — verified by temporarily
// reverting the scope-send and segment-render changes) if either piece
// regresses: a scope-less "allow" POST, or a chained command collapsing
// back to a single opaque JSON blob.

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

beforeEach(() => {
  act(() => {
    useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
  })
  vi.clearAllMocks()
  vi.mocked(api.submitToolApproval).mockResolvedValue({
    approval_id: 'appr-shell-001',
    action: 'allow',
    status: 'ok',
    grant_recorded: true,
  })
})

const BASH_APPROVAL_NO_SEGMENTS = {
  approvalId: 'appr-shell-001',
  toolCallId: 'call-shell-001',
  toolName: 'bash',
  args: { command: 'npm run test', cwd: 'apps/web' },
  agentId: 'agent-main',
  sessionId: 'sess-001',
  turnId: 'turn-001',
  expiresAt: Date.now() + 300_000,
}

const CHAINED_APPROVAL = {
  approvalId: 'appr-shell-002',
  toolCallId: 'call-shell-002',
  toolName: 'bash',
  args: { command: 'npm run build && git push', cwd: 'apps/web' },
  agentId: 'agent-main',
  sessionId: 'sess-001',
  turnId: 'turn-002',
  expiresAt: Date.now() + 300_000,
  segments: [
    {
      segment_index: 0,
      command_text: 'npm run build',
      resolved_binary: 'npm',
      classification: 'write' as const,
      network_required: false,
      suggested_prefix: 'npm run build',
      prefix_available: true,
    },
    {
      segment_index: 1,
      command_text: 'git push',
      resolved_binary: 'git',
      classification: 'none' as const,
      network_required: true,
      suggested_prefix: 'git push',
      prefix_available: true,
    },
  ],
}

const NON_SHELL_APPROVAL = {
  approvalId: 'appr-fetch-001',
  toolCallId: 'call-fetch-001',
  toolName: 'fetch_url',
  args: { url: 'https://example.com' },
  agentId: 'agent-main',
  sessionId: 'sess-001',
  turnId: 'turn-003',
  expiresAt: Date.now() + 300_000,
}

describe('ToolApprovalModal — grant scope (ADR-092 D4/FR-024)', () => {
  it('defaults to "exact" and sends scope:"exact" on Always Allow for a shell command', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [BASH_APPROVAL_NO_SEGMENTS] })
    })
    render(<ToolApprovalModal />)

    expect(screen.getByText('Allow this exact command')).toBeInTheDocument()
    const exactOption = screen.getByRole('radio', { name: /Allow this exact command/i })
    expect(exactOption).toHaveAttribute('aria-checked', 'true')

    fireEvent.click(screen.getByRole('button', { name: /Always Allow/i }))

    await waitFor(() => {
      expect(api.submitToolApproval).toHaveBeenCalledWith('appr-shell-001', 'allow', 'exact')
    })
  })

  it('does not offer a prefix option when the server sent no segments/suggested prefix', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [BASH_APPROVAL_NO_SEGMENTS] })
    })
    render(<ToolApprovalModal />)

    expect(screen.queryByTestId('scope-prefix')).not.toBeInTheDocument()
  })

  it('offers and sends scope:"prefix" once the server supplies a suggested prefix', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [CHAINED_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    const prefixOption = screen.getByTestId('scope-prefix')
    expect(prefixOption).toBeInTheDocument()
    expect(screen.getByText('npm run build')).toBeInTheDocument()

    fireEvent.click(prefixOption)
    expect(prefixOption).toHaveAttribute('aria-checked', 'true')

    fireEvent.click(screen.getByRole('button', { name: /Always Allow/i }))

    await waitFor(() => {
      expect(api.submitToolApproval).toHaveBeenCalledWith('appr-shell-002', 'allow', 'prefix')
    })
  })

  it('never sends a scope for Approve (allow_once), even on a shell command', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [BASH_APPROVAL_NO_SEGMENTS] })
    })
    render(<ToolApprovalModal />)

    fireEvent.click(screen.getByRole('button', { name: /Approve/i }))

    await waitFor(() => {
      expect(api.submitToolApproval).toHaveBeenCalledWith('appr-shell-001', 'allow_once')
    })
    expect(api.submitToolApproval).not.toHaveBeenCalledWith('appr-shell-001', 'allow_once', 'exact')
  })

  it('never sends a scope for Deny, even on a shell command', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [BASH_APPROVAL_NO_SEGMENTS] })
    })
    render(<ToolApprovalModal />)

    fireEvent.click(screen.getByRole('button', { name: /Deny/i }))

    await waitFor(() => {
      expect(api.submitToolApproval).toHaveBeenCalledWith('appr-shell-001', 'deny')
    })
  })

  it('does not render a scope choice at all for a non-shell tool, and Always Allow sends no scope', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [NON_SHELL_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    expect(screen.queryByText('Allow this exact command')).not.toBeInTheDocument()
    expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /Always Allow/i }))

    await waitFor(() => {
      expect(api.submitToolApproval).toHaveBeenCalledWith('appr-fetch-001', 'allow')
    })
    expect(api.submitToolApproval).not.toHaveBeenCalledWith('appr-fetch-001', 'allow', 'exact')
  })
})

describe('ToolApprovalModal — per-segment display (ADR-092 D4/FR-025/FR-027)', () => {
  it('renders each chained-command segment as its own readable block with the binary highlighted', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [CHAINED_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    const seg0 = screen.getByTestId('command-segment-0')
    const seg1 = screen.getByTestId('command-segment-1')
    expect(seg0).toBeInTheDocument()
    expect(seg1).toBeInTheDocument()

    // The resolved binary is rendered as its own element, distinguishable
    // from the rest of the segment's command text — not just present
    // somewhere in a JSON dump.
    expect(seg0.querySelector('.text-\\[var\\(--color-accent\\)\\]')).toHaveTextContent('npm')
    expect(seg1.querySelector('.text-\\[var\\(--color-accent\\)\\]')).toHaveTextContent('git')

    expect(seg0).toHaveTextContent('npm run build')
    expect(seg1).toHaveTextContent('git push')
  })

  it('labels the network-requiring segment and the filesystem-write segment differently', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [CHAINED_APPROVAL] })
    })
    render(<ToolApprovalModal />)

    expect(screen.getByTestId('command-segment-0')).toHaveTextContent('write')
    expect(screen.getByTestId('command-segment-1')).toHaveTextContent('network')
  })

  it('renders no segment breakdown when the frame carried none (single, unchained command)', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [BASH_APPROVAL_NO_SEGMENTS] })
    })
    render(<ToolApprovalModal />)

    expect(screen.queryByTestId('command-segments')).not.toBeInTheDocument()
  })
})
