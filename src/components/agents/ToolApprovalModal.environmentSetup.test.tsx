// L1 amendment §1.2 R6 / §1.5: inline container replaces the modal.
// All non-container preview/action/security oracles retained; B3 stub deletion deferred.
// ToolApprovalModal.environmentSetup.test.tsx — inline surface
//
// Focused coverage for the `environment_setup` readable approval preview
// (approvalPreviews/EnvironmentSetupApprovalPreview.tsx, registered in
// approvalPreviews/registry.ts). Driven through the REAL ToolApprovalPanel +
// registry so the integration (entry lookup, replace-mode rendering, title)
// is what is under test, not a component in isolation.
//
// Test plan (test-plan-and-write skill) — expectations derived from the
// SPECIFICATION, never from the implementation:
//
// Behaviour under test: the environment_setup approval card renders a
// readable summary of the AGENT-SUPPLIED installation command/script (the
// actual text, multiline scripts included), the stated purpose, the resolved
// destination workspace and the installation scope; for poll/read/kill
// session actions it renders the action + session instead of install fields.
// Missing fields degrade honestly. Unknown or unsummarizable request content
// re-reveals the raw JSON instead of being hidden by the summary. Standard
// approval semantics (Approve/Deny/Always Allow/Cancel, raw JSON hidden in
// replace mode) are preserved unchanged.
//
// Specification sources (oracle):
// - GENERIC-INSTALL-DECISION.md (option A): agent supplies the installation
//   command or inline script, shown in the EXISTING Ask approval; no
//   hardcoded libraries/recipes; async via existing Bash-style sessions.
// - docs/internal/specs/adr-090-environment-setup-spec.md
//   · ES-FR-01: the normal approval display identifies the requested
//     installation, reason, workspace and scope; standard approve/reject.
//   · ES-FR-02: inputs are the installation command/inline script, a short
//     purpose, scope DEFAULTING TO WORKSPACE, an optional authorized target
//     workspace, existing-style timeout/session actions; "The command/script
//     itself and destination are visible in the normal approval display";
//     background start returns session_id; poll/read/kill per the bash
//     pattern.
//   · ES-FR-03: setup approval authorizes the installation, not a general
//     shell escape — hence the approver must see WHAT will run.
// - Coordinator GS-06 disposition: explicit target workspace (Admin
//   cross-workspace authority) wins over the session's workspace.
// - Lane brief: exact keys target_workspace/command/purpose/scope/action/
//   session_id (+ existing-style timeout_seconds); no guessed alias
//   catalogue; unsupported content surfaces via augment/raw fallback; no new
//   approval workflow, standard buttons preserved.
//
// Case table: 35 cases (24 positive, 11 negative — 31% negative).
// Original suite has historical mutation receipts. This L1 RED pack defers
// GREEN, mutation checks and Proof-of-failability to a different CHECK instance.

import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import { act } from 'react'
import { queryClient } from '@/lib/queryClient'
import { workspacesQueryKeys } from '@/lib/api'
import type { Agent, Session, Workspace } from '@/lib/api'

import { useToolApprovalStore } from '@/store/toolApproval'
import type { ComponentType } from 'react'
import { renderWithClient as render, loadApprovalPanel, resetSharedState, installApprovalNetwork, approvalPosts, cleanupApprovalState } from '../../../tests/l1-ui-red-u23/support'
let ToolApprovalPanel: ComponentType

beforeEach(async () => {
  resetSharedState('sess-env')
  installApprovalNetwork()
  ToolApprovalPanel = await loadApprovalPanel()
})

/** Approval-queue entry shape with args wide open — the wire `args` object is generic JSON. */
interface FixtureApproval {
  approvalId: string
  toolCallId: string
  toolName: 'environment_setup'
  args: Record<string, unknown>
  agentId: string
  sessionId: string
  turnId: string
  expiresAt: number
}

/** A one-line package-manager style command (ES-FR-02 input form 1). */
const INSTALL_COMMAND =
  'python3 -m venv .venv && .venv/bin/pip install reportlab==4.2.0'

/** A multiline inline script (ES-FR-02 input form 2, ES-BDD-12). */
const INSTALL_SCRIPT = [
  'set -eu',
  'curl -fsSL https://example.com/toolchain/installer.sh -o installer.sh',
  'sh installer.sh --prefix "$OMNIPUS_INSTALL_DIR"',
  'toolchain verify --quiet',
].join('\n')

const INSTALL_APPROVAL: FixtureApproval = {
  approvalId: 'appr-env-install',
  toolCallId: 'call-env-install',
  toolName: 'environment_setup',
  args: {
    command: INSTALL_COMMAND,
    purpose: 'render the quarterly report to PDF',
  },
  agentId: 'agent-jim',
  sessionId: 'sess-env',
  turnId: 'turn-env',
  expiresAt: Date.now() + 300_000,
}

const POLL_APPROVAL: FixtureApproval = {
  approvalId: 'appr-env-poll',
  toolCallId: 'call-env-poll',
  toolName: 'environment_setup',
  args: { action: 'poll', session_id: 'envsess-abc123' },
  agentId: 'agent-jim',
  sessionId: 'sess-env',
  turnId: 'turn-env-poll',
  expiresAt: Date.now() + 300_000,
}

function seedAgent() {
  queryClient.setQueryData<Agent[]>(['agents'], [{ figure: 'Omnipus', role: 'general', id: 'agent-jim', name: 'Jim' }] as unknown as Agent[])
}

function seedWorkspace() {
  queryClient.setQueryData<Session[]>(
    ['sessions'],
    [{ id: 'sess-env', workspace_id: 'ws-1' }] as unknown as Session[],
  )
  queryClient.setQueryData<Workspace[]>(
    workspacesQueryKeys.list({ status: 'active' }),
    [{ id: 'ws-1', name: 'Acme Backend' }] as unknown as Workspace[],
  )
}

function enqueue(approval: FixtureApproval) {
  act(() => {
    useToolApprovalStore.setState({ queue: [approval] })
  })
}

describe('ToolApprovalPanel — environment_setup install summary (ES-FR-01/02: the command itself is visible)', () => {
  it('L1 §1.2 R6: renders the actual installation command as readable text (not a JSON blob)', () => {
    seedAgent()
    enqueue(INSTALL_APPROVAL)
    render(<ToolApprovalPanel />)

    const commandBlock = screen.getByTestId('environment-setup-command')
    expect(commandBlock.textContent).toContain('python3 -m venv .venv')
    expect(commandBlock.textContent).toContain('pip install reportlab==4.2.0')
  })

  it('L1 §1.2 R6: renders a multiline script with its line structure intact (ES-BDD-12 custom script)', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { command: INSTALL_SCRIPT, purpose: 'install the toolchain' } })
    render(<ToolApprovalPanel />)

    const commandBlock = screen.getByTestId('environment-setup-command')
    // Every line survives, and the newlines between them survive too — the
    // approver reads the script exactly as it will run.
    expect(commandBlock.textContent).toContain('set -eu')
    expect(commandBlock.textContent).toContain('curl -fsSL https://example.com/toolchain/installer.sh -o installer.sh')
    expect(commandBlock.textContent).toContain('sh installer.sh --prefix "$OMNIPUS_INSTALL_DIR"')
    expect(commandBlock.textContent).toContain('toolchain verify --quiet')
    expect(commandBlock.textContent).toContain('\n')
  })

  it('L1 §1.2 R6: shows the default (absent) action as an installation in the title', () => {
    seedAgent()
    enqueue(INSTALL_APPROVAL)
    render(<ToolApprovalPanel />)
    expect(screen.getByText('Jim wants to install software')).toBeInTheDocument()
  })

  it('L1 §1.2 R6: renders the stated purpose under "Why"', () => {
    seedAgent()
    enqueue(INSTALL_APPROVAL)
    render(<ToolApprovalPanel />)
    expect(screen.getByText('render the quarterly report to PDF')).toBeInTheDocument()
  })

  it('L1 §1.2 R6: shows the session-derived workspace name', () => {
    seedAgent()
    seedWorkspace()
    enqueue(INSTALL_APPROVAL)
    render(<ToolApprovalPanel />)
    expect(screen.getByText('Acme Backend')).toBeInTheDocument()
  })

  it('L1 §1.2 R6: shows an explicit target workspace over the session workspace (Admin cross-workspace targeting, GS-06 disposition)', () => {
    seedAgent()
    seedWorkspace()
    enqueue({
      ...INSTALL_APPROVAL,
      args: { ...INSTALL_APPROVAL.args, target_workspace: 'ws-2' },
    })
    queryClient.setQueryData<Workspace[]>(
      workspacesQueryKeys.list({ status: 'active' }),
      [
        { id: 'ws-1', name: 'Acme Backend' },
        { id: 'ws-2', name: 'Backend B' },
      ] as unknown as Workspace[],
    )
    render(<ToolApprovalPanel />)
    expect(screen.getByText('Backend B')).toBeInTheDocument()
    // The session's own workspace must NOT be shown as the target — showing
    // it would tell the approver the wrong destination.
    expect(screen.queryByText('Acme Backend')).not.toBeInTheDocument()
  })

  it('L1 §1.2 R6: falls back to the raw target value when the explicit target is not in the workspaces cache', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { ...INSTALL_APPROVAL.args, target_workspace: 'ws-unlisted' } })
    render(<ToolApprovalPanel />)
    expect(screen.getByText('ws-unlisted')).toBeInTheDocument()
  })

  it('L1 §1.2 R6: defaults the scope line to the workspace when no scope is given (ES-FR-02: scope defaults to workspace)', () => {
    seedAgent()
    enqueue(INSTALL_APPROVAL)
    render(<ToolApprovalPanel />)
    expect(screen.getByText('This workspace')).toBeInTheDocument()
  })

  it('L1 §1.2 R6: renders the requested timeout when one is sent (existing-style timeout_seconds)', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { ...INSTALL_APPROVAL.args, timeout_seconds: 300 } })
    render(<ToolApprovalPanel />)
    expect(screen.getByText('5m')).toBeInTheDocument()
  })

  it('L1 §1.2 R6: leaves the timeout line out when the request sends none', () => {
    seedAgent()
    enqueue(INSTALL_APPROVAL)
    render(<ToolApprovalPanel />)
    expect(screen.queryByText('Timeout')).not.toBeInTheDocument()
  })
})

describe('ToolApprovalPanel — environment_setup shared scope is explicit (ES-FR-02)', () => {
  it('L1 §1.2 R6: renders an explicit Shared scope line when scope is shared', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { ...INSTALL_APPROVAL.args, scope: 'shared' } })
    render(<ToolApprovalPanel />)
    expect(screen.getByText('Shared')).toBeInTheDocument()
    expect(screen.queryByText('This workspace')).not.toBeInTheDocument()
  })

  it('L1 §1.2 R6: states the lasting consequence of a shared install', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { ...INSTALL_APPROVAL.args, scope: 'shared' } })
    render(<ToolApprovalPanel />)
    expect(screen.getByText(/installed once by Omnipus/)).toBeInTheDocument()
    expect(screen.getByText(/stay available to every agent/)).toBeInTheDocument()
  })

  it('L1 §1.2 R6: treats scope case-insensitively ("Shared", "SHARED")', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { ...INSTALL_APPROVAL.args, scope: 'SHARED' } })
    render(<ToolApprovalPanel />)
    expect(screen.getByText('Shared')).toBeInTheDocument()
  })

  it('L1 §1.2 R6: treats scope SHARED as shared even alongside a run action spelled out', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { ...INSTALL_APPROVAL.args, action: 'run', scope: 'shared' } })
    render(<ToolApprovalPanel />)
    expect(screen.getByText('Shared')).toBeInTheDocument()
  })
})

describe('ToolApprovalPanel — environment_setup background session actions (ES-FR-02 poll/read/kill)', () => {
  it('L1 §1.2 R6: renders the session id for a poll call and no install fields', () => {
    seedAgent()
    enqueue(POLL_APPROVAL)
    render(<ToolApprovalPanel />)
    expect(screen.getByText('envsess-abc123')).toBeInTheDocument()
    expect(screen.queryByText('Command')).not.toBeInTheDocument()
    expect(screen.queryByText('Why')).not.toBeInTheDocument()
  })

  it('L1 §1.2 R6: titles a poll call as checking an installation, not installing', () => {
    seedAgent()
    enqueue(POLL_APPROVAL)
    render(<ToolApprovalPanel />)
    expect(screen.getByText('Jim wants to check an installation')).toBeInTheDocument()
    expect(screen.queryByText('Jim wants to install software')).not.toBeInTheDocument()
  })

  it('L1 §1.2 R6: titles a read call as checking an installation and describes reading output', () => {
    seedAgent()
    enqueue({ ...POLL_APPROVAL, args: { action: 'read', session_id: 'envsess-abc123' } })
    render(<ToolApprovalPanel />)
    expect(screen.getByText('Jim wants to check an installation')).toBeInTheDocument()
    expect(screen.getByText(/Read the output of a background installation/)).toBeInTheDocument()
  })

  it('L1 §1.2 R6: titles a kill call as stopping an installation', () => {
    seedAgent()
    enqueue({ ...POLL_APPROVAL, approvalId: 'appr-env-kill', args: { action: 'kill', session_id: 'envsess-abc123' } })
    render(<ToolApprovalPanel />)
    expect(screen.getByText('Jim wants to stop an installation')).toBeInTheDocument()
  })

  it('L1 §1.2 R6: says so plainly when a session action arrives without a session id', () => {
    seedAgent()
    enqueue({ ...POLL_APPROVAL, args: { action: 'poll' } })
    render(<ToolApprovalPanel />)
    expect(screen.getByText('No session was included with this request.')).toBeInTheDocument()
  })
})

describe('ToolApprovalPanel — environment_setup honest degradation', () => {
  it('L1 §1.2 R6: says so plainly when no command was included', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { purpose: 'something' } })
    render(<ToolApprovalPanel />)
    expect(screen.getByText('No installation command was included with this request.')).toBeInTheDocument()
  })

  it('L1 §1.2 R6: says "No reason was given." when the purpose is missing (request_mount copy precedent)', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { command: INSTALL_COMMAND } })
    render(<ToolApprovalPanel />)
    expect(screen.getByText('No reason was given.')).toBeInTheDocument()
  })

  it('L1 §1.2 R6: renders an honest card for entirely empty args — no crash, missing lines shown', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: {} })
    render(<ToolApprovalPanel />)
    expect(screen.getByText('No installation command was included with this request.')).toBeInTheDocument()
    expect(screen.getByText('No reason was given.')).toBeInTheDocument()
  })
})

describe('ToolApprovalPanel — environment_setup standard approval semantics preserved', () => {
  it('L1 §1.2 R6: hides the raw Arguments JSON dump for a fully covered request (replace mode)', () => {
    seedAgent()
    enqueue(INSTALL_APPROVAL)
    render(<ToolApprovalPanel />)
    expect(screen.queryByText('Arguments')).not.toBeInTheDocument()
    expect(screen.queryByText(/"command"/)).not.toBeInTheDocument()
  })

  it('L1 §1.2 R6: offers standard Approve / Deny / Always Allow / Cancel and dispatches the standard approve action', async () => {
    seedAgent()
    enqueue(INSTALL_APPROVAL)
    render(<ToolApprovalPanel />)

    // Standard four-way semantics unchanged: no environment-specific button
    // row, no hidden Approve, Cancel still offered. Always Allow remembers
    // exactly these args.
    expect(screen.getByRole('button', { name: /Approve/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Deny/i })).toBeInTheDocument()
    expect(screen.getByTestId('always-allow-toggle')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Cancel/i })).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /Approve/i }))
    await waitFor(() => {
      expect(approvalPosts()).toContainEqual({ id: 'appr-env-install', body: { action: 'allow_once' } })
    })
  })

  it('L1 §1.2 R6: shows the raw JSON alongside the summary when the request carries a field the preview does not know (never silently dropped)', () => {
    seedAgent()
    enqueue({
      ...INSTALL_APPROVAL,
      args: {
        ...INSTALL_APPROVAL.args,
        some_future_field: 'a field a newer tool schema might send',
      },
    })
    render(<ToolApprovalPanel />)
    expect(screen.getByText(/not summarized above/)).toBeInTheDocument()
    // The unknown content itself must be visible, not just admitted to.
    expect(screen.getByText(/some_future_field/)).toBeInTheDocument()
  })

  it('L1 §1.2 R6: shows the raw JSON when command is present but not a string (unsummarizable content stays visible)', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { command: 42, purpose: 'p' } })
    render(<ToolApprovalPanel />)
    expect(screen.getByText(/not summarized above/)).toBeInTheDocument()
    expect(screen.getByText(/"command": 42/)).toBeInTheDocument()
  })

  it('L1 §1.2 R6: shows the raw JSON when the action value is unknown (unknown verbs are not silently run-style)', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { action: 'reinstall', command: INSTALL_COMMAND, purpose: 'p' } })
    render(<ToolApprovalPanel />)
    expect(screen.getByText(/not summarized above/)).toBeInTheDocument()
    expect(screen.getByText(/reinstall/)).toBeInTheDocument()
    // The readable command is still shown — the fallback AUGMENTS, it does
    // not replace.
    expect(screen.getByTestId('environment-setup-command')).toBeInTheDocument()
  })

  it('L1 §1.2 R6: canonical keys only: an alias spelling the tool does not accept surfaces in the technical details instead of being parsed (no alias catalogue)', () => {
    seedAgent()
    enqueue({
      ...INSTALL_APPROVAL,
      args: {
        cmd: INSTALL_COMMAND,
        reason: 'render the quarterly report to PDF',
      },
    })
    render(<ToolApprovalPanel />)
    // 'cmd'/'reason' are NOT tool schema keys: the summary must not pretend
    // they were understood — they surface as unsummarized content.
    expect(screen.getByText(/not summarized above/)).toBeInTheDocument()
    expect(screen.getByText('No installation command was included with this request.')).toBeInTheDocument()
    expect(screen.getByText(/"reason"/)).toBeInTheDocument()
  })

  it('L1 §1.2 R6: shows the raw JSON when timeout_seconds is not a number (malformed known keys stay visible)', () => {
    seedAgent()
    enqueue({
      ...INSTALL_APPROVAL,
      args: { ...INSTALL_APPROVAL.args, timeout_seconds: 'forever' },
    })
    render(<ToolApprovalPanel />)
    expect(screen.getByText(/not summarized above/)).toBeInTheDocument()
    expect(screen.getByText(/forever/)).toBeInTheDocument()
  })

  it('L1 §1.2 R6: fully-covered requests render no technical-details block', () => {
    seedAgent()
    enqueue(INSTALL_APPROVAL)
    render(<ToolApprovalPanel />)
    expect(screen.queryByText(/not summarized above/)).not.toBeInTheDocument()
  })
})

// MAJ-001/MIN-001 regression (generic-frontend-review.md): coverage is
// BRANCH-AWARE. The two cards render disjoint subsets of the known keys —
// the session card shows only the action + session id; the install card
// never shows session_id — so a known, well-typed key can still be content
// the rendered summary hides. Oracle: the component's replace-mode contract
// (raw JSON hidden ONLY when the summary covers everything) and the tool
// schema's scope enum (`workspace` | `shared`).
describe('ToolApprovalPanel — environment_setup branch-aware fidelity (MAJ-001/MIN-001 regression)', () => {
  it('L1 §1.2 R6: shows the raw JSON when a poll carries extra known install fields (the session card never renders them)', () => {
    seedAgent()
    enqueue({
      ...POLL_APPROVAL,
      args: {
        action: 'poll',
        session_id: 'envsess-abc123',
        command: INSTALL_COMMAND,
        purpose: 'ride-along content the session card cannot show',
      },
    })
    render(<ToolApprovalPanel />)
    expect(screen.getByText(/not summarized above/)).toBeInTheDocument()
    // The ridden-in command text itself must be visible to the approver —
    // the fallback AUGMENTS the session summary, it does not replace it.
    expect(screen.getByText(/"command": "python3 -m venv/)).toBeInTheDocument()
    expect(screen.getByText('envsess-abc123')).toBeInTheDocument()
  })

  it('L1 §1.2 R6: shows the raw JSON when a run carries session_id (the install card never renders it)', () => {
    seedAgent()
    enqueue({
      ...INSTALL_APPROVAL,
      args: { ...INSTALL_APPROVAL.args, session_id: 'envsess-leftover' },
    })
    render(<ToolApprovalPanel />)
    expect(screen.getByText(/not summarized above/)).toBeInTheDocument()
    expect(screen.getByText(/"session_id": "envsess-leftover"/)).toBeInTheDocument()
  })

  it('L1 §1.2 R6: shows the raw JSON when scope is not a tool-enum value (an unsupported scope is never presented as the workspace default)', () => {
    seedAgent()
    enqueue({
      ...INSTALL_APPROVAL,
      args: { ...INSTALL_APPROVAL.args, scope: 'global' },
    })
    render(<ToolApprovalPanel />)
    expect(screen.getByText(/not summarized above/)).toBeInTheDocument()
    expect(screen.getByText(/"scope": "global"/)).toBeInTheDocument()
  })

  it('L1 §1.2 R6: keeps a minimal poll (action + session_id only) fully covered — branch-aware coverage does not over-fallback', () => {
    seedAgent()
    enqueue(POLL_APPROVAL)
    render(<ToolApprovalPanel />)
    expect(screen.queryByText(/not summarized above/)).not.toBeInTheDocument()
  })

  it('L1 §1.2 R6: keeps an enum-valid explicit workspace scope fully covered', () => {
    seedAgent()
    enqueue({
      ...INSTALL_APPROVAL,
      args: { ...INSTALL_APPROVAL.args, scope: 'workspace' },
    })
    render(<ToolApprovalPanel />)
    expect(screen.queryByText(/not summarized above/)).not.toBeInTheDocument()
    expect(screen.getByText('This workspace')).toBeInTheDocument()
  })
})

// Real stores/QueryClient/auth survive the test boundary; only fetch/socket doubled.
afterEach(async () => { await cleanupApprovalState() })
