// L1 amendment §1.2 R6 / §1.5: inline container replaces the modal.
// All non-container preview/action/security oracles retained; B3 stub deletion deferred.
// ToolApprovalModal.scopeAndSegments.test.tsx — inline surface
//
// ADR-092 D4/FR-024/FR-025/FR-027 coverage — the two pieces of the approval
// dialog that were previously left as "still open work in this lane" (see
// the note atop ToolApprovalPanel.tsx):
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

import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import { act } from 'react'

import { useToolApprovalStore } from '@/store/toolApproval'
import type { ComponentType } from 'react'
import { renderWithClient as render, loadApprovalPanel, resetSharedState, installApprovalNetwork, approvalPosts, cleanupApprovalState } from '../../../tests/l1-ui-red-u23/support'
let ToolApprovalPanel: ComponentType

beforeEach(async () => {
  resetSharedState('sess-001')
  installApprovalNetwork()
  ToolApprovalPanel = await loadApprovalPanel()
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

// resolved_binary as the server actually sends it: a resolved absolute path
// (pkg/tools' D3 matcher resolves through PATH), not the bare command name
// the agent typed. A naive substring search for "/bin/rm" inside "rm -rf x"
// never matches — highlightBinaryInText must fall back to the basename.
const CHAINED_APPROVAL_RESOLVED_PATHS = {
  approvalId: 'appr-shell-003',
  toolCallId: 'call-shell-003',
  toolName: 'bash',
  args: { command: 'rm -rf /tmp/scratch && curl https://example.com/x', cwd: 'apps/web' },
  agentId: 'agent-main',
  sessionId: 'sess-001',
  turnId: 'turn-004',
  expiresAt: Date.now() + 300_000,
  segments: [
    {
      segment_index: 0,
      command_text: 'rm -rf /tmp/scratch',
      resolved_binary: '/bin/rm',
      classification: 'write' as const,
      network_required: false,
      suggested_prefix: undefined,
      prefix_available: false,
    },
    {
      segment_index: 1,
      command_text: 'curl https://example.com/x',
      resolved_binary: '/usr/bin/curl',
      classification: 'none' as const,
      network_required: true,
      suggested_prefix: undefined,
      prefix_available: false,
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

describe('ToolApprovalPanel — grant scope (ADR-092 D4/FR-024)', () => {
  it('L1 §1.2 R6: defaults to "exact" and sends scope:"exact" on Always Allow for a shell command', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [BASH_APPROVAL_NO_SEGMENTS] })
    })
    render(<ToolApprovalPanel />)

    expect(screen.getByText('Allow this exact command')).toBeInTheDocument()
    const exactOption = screen.getByRole('radio', { name: /Allow this exact command/i })
    expect(exactOption).toHaveAttribute('aria-checked', 'true')

    fireEvent.click(screen.getByRole('button', { name: /Always Allow/i }))

    await waitFor(() => {
      expect(approvalPosts()).toContainEqual({ id: 'appr-shell-001', body: { action: 'allow', scope: 'exact' } })
    })
  })

  it('L1 §1.2 R6: does not offer a prefix option when the server sent no segments/suggested prefix', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [BASH_APPROVAL_NO_SEGMENTS] })
    })
    render(<ToolApprovalPanel />)

    expect(screen.queryByTestId('scope-prefix')).not.toBeInTheDocument()
  })

  it('L1 §1.2 R6: offers and sends scope:"prefix" once the server supplies a suggested prefix', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [CHAINED_APPROVAL] })
    })
    render(<ToolApprovalPanel />)

    const prefixOption = screen.getByTestId('scope-prefix')
    expect(prefixOption).toBeInTheDocument()
    expect(screen.getByText('npm run build')).toBeInTheDocument()

    fireEvent.click(prefixOption)
    expect(prefixOption).toHaveAttribute('aria-checked', 'true')

    fireEvent.click(screen.getByRole('button', { name: /Always Allow/i }))

    await waitFor(() => {
      expect(approvalPosts()).toContainEqual({ id: 'appr-shell-002', body: { action: 'allow', scope: 'prefix' } })
    })
  })

  it('L1 §1.2 R6: never sends a scope for Approve (allow_once), even on a shell command', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [BASH_APPROVAL_NO_SEGMENTS] })
    })
    render(<ToolApprovalPanel />)

    fireEvent.click(screen.getByRole('button', { name: /Approve/i }))

    await waitFor(() => {
      expect(approvalPosts()).toContainEqual({ id: 'appr-shell-001', body: { action: 'allow_once' } })
    })
    expect(approvalPosts()).not.toContainEqual({ id: 'appr-shell-001', body: { action: 'allow_once', scope: 'exact' } })
  })

  it('L1 §1.2 R6: never sends a scope for Deny, even on a shell command', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [BASH_APPROVAL_NO_SEGMENTS] })
    })
    render(<ToolApprovalPanel />)

    fireEvent.click(screen.getByRole('button', { name: /Deny/i }))

    await waitFor(() => {
      expect(approvalPosts()).toContainEqual({ id: 'appr-shell-001', body: { action: 'deny' } })
    })
  })

  it('L1 §1.2 R6: does not render a scope choice at all for a non-shell tool, and Always Allow sends no scope', async () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [NON_SHELL_APPROVAL] })
    })
    render(<ToolApprovalPanel />)

    expect(screen.queryByText('Allow this exact command')).not.toBeInTheDocument()
    expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /Always Allow/i }))

    await waitFor(() => {
      expect(approvalPosts()).toContainEqual({ id: 'appr-fetch-001', body: { action: 'allow' } })
    })
    expect(approvalPosts()).not.toContainEqual({ id: 'appr-fetch-001', body: { action: 'allow', scope: 'exact' } })
  })
})

describe('ToolApprovalPanel — per-segment display (ADR-092 D4/FR-025/FR-027)', () => {
  it('L1 §1.2 R6: renders each chained-command segment as its own readable block with the binary highlighted', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [CHAINED_APPROVAL] })
    })
    render(<ToolApprovalPanel />)

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

  it('L1 §1.2 R6: labels the network-requiring segment and the filesystem-write segment differently', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [CHAINED_APPROVAL] })
    })
    render(<ToolApprovalPanel />)

    expect(screen.getByTestId('command-segment-0')).toHaveTextContent('write')
    expect(screen.getByTestId('command-segment-1')).toHaveTextContent('network')
  })

  it('L1 §1.2 R6: highlights the binary even when resolved_binary is a resolved absolute path and command_text is what the agent typed', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [CHAINED_APPROVAL_RESOLVED_PATHS] })
    })
    render(<ToolApprovalPanel />)

    const seg0 = screen.getByTestId('command-segment-0')
    const seg1 = screen.getByTestId('command-segment-1')

    // "/bin/rm" never appears literally in "rm -rf /tmp/scratch" — the
    // basename ("rm") must be the part highlighted, not nothing.
    const seg0Highlight = seg0.querySelector('.text-\\[var\\(--color-accent\\)\\]')
    expect(seg0Highlight).toHaveTextContent('rm')
    expect(seg0Highlight?.textContent).not.toBe('')

    const seg1Highlight = seg1.querySelector('.text-\\[var\\(--color-accent\\)\\]')
    expect(seg1Highlight).toHaveTextContent('curl')
    expect(seg1Highlight?.textContent).not.toBe('')

    // The full segment text is still shown, unhighlighted parts included.
    expect(seg0).toHaveTextContent('rm -rf /tmp/scratch')
    expect(seg1).toHaveTextContent('curl https://example.com/x')
  })

  it('L1 §1.2 R6: renders no segment breakdown when the frame carried none (single, unchained command)', () => {
    act(() => {
      useToolApprovalStore.setState({ queue: [BASH_APPROVAL_NO_SEGMENTS] })
    })
    render(<ToolApprovalPanel />)

    expect(screen.queryByTestId('command-segments')).not.toBeInTheDocument()
  })
})

// Real stores/QueryClient/auth survive the test boundary; only fetch/socket doubled.
afterEach(async () => { await cleanupApprovalState() })
