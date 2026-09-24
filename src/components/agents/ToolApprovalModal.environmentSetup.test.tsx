// ToolApprovalModal.environmentSetup.test.tsx
//
// Focused coverage for the `environment_setup` readable approval preview
// (approvalPreviews/EnvironmentSetupApprovalPreview.tsx, registered in
// approvalPreviews/registry.ts). Driven through the REAL ToolApprovalModal +
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
// Mutations applied after green are listed in the lane status note.

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

beforeEach(() => {
  act(() => {
    useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
  })
  vi.clearAllMocks()
  vi.mocked(api.submitToolApproval).mockResolvedValue({
    approval_id: 'appr-env-setup',
    action: 'allow_once',
    status: 'ok',
  })
  queryClient.clear()
})

afterEach(() => {
  queryClient.clear()
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
  queryClient.setQueryData<Agent[]>(['agents'], [{ id: 'agent-jim', name: 'Jim' }] as unknown as Agent[])
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

describe('ToolApprovalModal — environment_setup install summary (ES-FR-01/02: the command itself is visible)', () => {
  it('renders the actual installation command as readable text (not a JSON blob)', () => {
    seedAgent()
    enqueue(INSTALL_APPROVAL)
    render(<ToolApprovalModal />)

    const commandBlock = screen.getByTestId('environment-setup-command')
    expect(commandBlock.textContent).toContain('python3 -m venv .venv')
    expect(commandBlock.textContent).toContain('pip install reportlab==4.2.0')
  })

  it('renders a multiline script with its line structure intact (ES-BDD-12 custom script)', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { command: INSTALL_SCRIPT, purpose: 'install the toolchain' } })
    render(<ToolApprovalModal />)

    const commandBlock = screen.getByTestId('environment-setup-command')
    // Every line survives, and the newlines between them survive too — the
    // approver reads the script exactly as it will run.
    expect(commandBlock.textContent).toContain('set -eu')
    expect(commandBlock.textContent).toContain('curl -fsSL https://example.com/toolchain/installer.sh -o installer.sh')
    expect(commandBlock.textContent).toContain('sh installer.sh --prefix "$OMNIPUS_INSTALL_DIR"')
    expect(commandBlock.textContent).toContain('toolchain verify --quiet')
    expect(commandBlock.textContent).toContain('\n')
  })

  it('shows the default (absent) action as an installation in the title', () => {
    seedAgent()
    enqueue(INSTALL_APPROVAL)
    render(<ToolApprovalModal />)
    expect(screen.getByText('Jim wants to install software')).toBeInTheDocument()
  })

  it('renders the stated purpose under "Why"', () => {
    seedAgent()
    enqueue(INSTALL_APPROVAL)
    render(<ToolApprovalModal />)
    expect(screen.getByText('render the quarterly report to PDF')).toBeInTheDocument()
  })

  it('shows the session-derived workspace name', () => {
    seedAgent()
    seedWorkspace()
    enqueue(INSTALL_APPROVAL)
    render(<ToolApprovalModal />)
    expect(screen.getByText('Acme Backend')).toBeInTheDocument()
  })

  it('shows an explicit target workspace over the session workspace (Admin cross-workspace targeting, GS-06 disposition)', () => {
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
    render(<ToolApprovalModal />)
    expect(screen.getByText('Backend B')).toBeInTheDocument()
    // The session's own workspace must NOT be shown as the target — showing
    // it would tell the approver the wrong destination.
    expect(screen.queryByText('Acme Backend')).not.toBeInTheDocument()
  })

  it('falls back to the raw target value when the explicit target is not in the workspaces cache', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { ...INSTALL_APPROVAL.args, target_workspace: 'ws-unlisted' } })
    render(<ToolApprovalModal />)
    expect(screen.getByText('ws-unlisted')).toBeInTheDocument()
  })

  it('defaults the scope line to the workspace when no scope is given (ES-FR-02: scope defaults to workspace)', () => {
    seedAgent()
    enqueue(INSTALL_APPROVAL)
    render(<ToolApprovalModal />)
    expect(screen.getByText('This workspace')).toBeInTheDocument()
  })

  it('renders the requested timeout when one is sent (existing-style timeout_seconds)', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { ...INSTALL_APPROVAL.args, timeout_seconds: 300 } })
    render(<ToolApprovalModal />)
    expect(screen.getByText('5m')).toBeInTheDocument()
  })

  it('leaves the timeout line out when the request sends none', () => {
    seedAgent()
    enqueue(INSTALL_APPROVAL)
    render(<ToolApprovalModal />)
    expect(screen.queryByText('Timeout')).not.toBeInTheDocument()
  })
})

describe('ToolApprovalModal — environment_setup shared scope is explicit (ES-FR-02)', () => {
  it('renders an explicit Shared scope line when scope is shared', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { ...INSTALL_APPROVAL.args, scope: 'shared' } })
    render(<ToolApprovalModal />)
    expect(screen.getByText('Shared')).toBeInTheDocument()
    expect(screen.queryByText('This workspace')).not.toBeInTheDocument()
  })

  it('states the lasting consequence of a shared install', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { ...INSTALL_APPROVAL.args, scope: 'shared' } })
    render(<ToolApprovalModal />)
    expect(screen.getByText(/installed once by Omnipus/)).toBeInTheDocument()
    expect(screen.getByText(/stay available to every agent/)).toBeInTheDocument()
  })

  it('treats scope case-insensitively ("Shared", "SHARED")', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { ...INSTALL_APPROVAL.args, scope: 'SHARED' } })
    render(<ToolApprovalModal />)
    expect(screen.getByText('Shared')).toBeInTheDocument()
  })

  it('treats scope SHARED as shared even alongside a run action spelled out', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { ...INSTALL_APPROVAL.args, action: 'run', scope: 'shared' } })
    render(<ToolApprovalModal />)
    expect(screen.getByText('Shared')).toBeInTheDocument()
  })
})

describe('ToolApprovalModal — environment_setup background session actions (ES-FR-02 poll/read/kill)', () => {
  it('renders the session id for a poll call and no install fields', () => {
    seedAgent()
    enqueue(POLL_APPROVAL)
    render(<ToolApprovalModal />)
    expect(screen.getByText('envsess-abc123')).toBeInTheDocument()
    expect(screen.queryByText('Command')).not.toBeInTheDocument()
    expect(screen.queryByText('Why')).not.toBeInTheDocument()
  })

  it('titles a poll call as checking an installation, not installing', () => {
    seedAgent()
    enqueue(POLL_APPROVAL)
    render(<ToolApprovalModal />)
    expect(screen.getByText('Jim wants to check an installation')).toBeInTheDocument()
    expect(screen.queryByText('Jim wants to install software')).not.toBeInTheDocument()
  })

  it('titles a read call as checking an installation and describes reading output', () => {
    seedAgent()
    enqueue({ ...POLL_APPROVAL, args: { action: 'read', session_id: 'envsess-abc123' } })
    render(<ToolApprovalModal />)
    expect(screen.getByText('Jim wants to check an installation')).toBeInTheDocument()
    expect(screen.getByText(/Read the output of a background installation/)).toBeInTheDocument()
  })

  it('titles a kill call as stopping an installation', () => {
    seedAgent()
    enqueue({ ...POLL_APPROVAL, approvalId: 'appr-env-kill', args: { action: 'kill', session_id: 'envsess-abc123' } })
    render(<ToolApprovalModal />)
    expect(screen.getByText('Jim wants to stop an installation')).toBeInTheDocument()
  })

  it('says so plainly when a session action arrives without a session id', () => {
    seedAgent()
    enqueue({ ...POLL_APPROVAL, args: { action: 'poll' } })
    render(<ToolApprovalModal />)
    expect(screen.getByText('No session was included with this request.')).toBeInTheDocument()
  })
})

describe('ToolApprovalModal — environment_setup honest degradation', () => {
  it('says so plainly when no command was included', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { purpose: 'something' } })
    render(<ToolApprovalModal />)
    expect(screen.getByText('No installation command was included with this request.')).toBeInTheDocument()
  })

  it('says "No reason was given." when the purpose is missing (request_mount copy precedent)', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { command: INSTALL_COMMAND } })
    render(<ToolApprovalModal />)
    expect(screen.getByText('No reason was given.')).toBeInTheDocument()
  })

  it('renders an honest card for entirely empty args — no crash, missing lines shown', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: {} })
    render(<ToolApprovalModal />)
    expect(screen.getByText('No installation command was included with this request.')).toBeInTheDocument()
    expect(screen.getByText('No reason was given.')).toBeInTheDocument()
  })
})

describe('ToolApprovalModal — environment_setup standard approval semantics preserved', () => {
  it('hides the raw Arguments JSON dump for a fully covered request (replace mode)', () => {
    seedAgent()
    enqueue(INSTALL_APPROVAL)
    render(<ToolApprovalModal />)
    expect(screen.queryByText('Arguments')).not.toBeInTheDocument()
    expect(screen.queryByText(/"command"/)).not.toBeInTheDocument()
  })

  it('offers standard Approve / Deny / Always Allow / Cancel and dispatches the standard approve action', async () => {
    seedAgent()
    enqueue(INSTALL_APPROVAL)
    render(<ToolApprovalModal />)

    // Standard four-way semantics unchanged: no environment-specific button
    // row, no hidden Approve, Cancel still offered. Always Allow remembers
    // exactly these args.
    expect(screen.getByRole('button', { name: /Approve/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Deny/i })).toBeInTheDocument()
    expect(screen.getByTestId('always-allow-toggle')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Cancel/i })).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /Approve/i }))
    await waitFor(() => {
      expect(api.submitToolApproval).toHaveBeenCalledWith('appr-env-install', 'allow_once')
    })
  })

  it('shows the raw JSON alongside the summary when the request carries a field the preview does not know (never silently dropped)', () => {
    seedAgent()
    enqueue({
      ...INSTALL_APPROVAL,
      args: {
        ...INSTALL_APPROVAL.args,
        some_future_field: 'a field a newer tool schema might send',
      },
    })
    render(<ToolApprovalModal />)
    expect(screen.getByText(/not summarized above/)).toBeInTheDocument()
    // The unknown content itself must be visible, not just admitted to.
    expect(screen.getByText(/some_future_field/)).toBeInTheDocument()
  })

  it('shows the raw JSON when command is present but not a string (unsummarizable content stays visible)', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { command: 42, purpose: 'p' } })
    render(<ToolApprovalModal />)
    expect(screen.getByText(/not summarized above/)).toBeInTheDocument()
    expect(screen.getByText(/"command": 42/)).toBeInTheDocument()
  })

  it('shows the raw JSON when the action value is unknown (unknown verbs are not silently run-style)', () => {
    seedAgent()
    enqueue({ ...INSTALL_APPROVAL, args: { action: 'reinstall', command: INSTALL_COMMAND, purpose: 'p' } })
    render(<ToolApprovalModal />)
    expect(screen.getByText(/not summarized above/)).toBeInTheDocument()
    expect(screen.getByText(/reinstall/)).toBeInTheDocument()
    // The readable command is still shown — the fallback AUGMENTS, it does
    // not replace.
    expect(screen.getByTestId('environment-setup-command')).toBeInTheDocument()
  })

  it('canonical keys only: an alias spelling the tool does not accept surfaces in the technical details instead of being parsed (no alias catalogue)', () => {
    seedAgent()
    enqueue({
      ...INSTALL_APPROVAL,
      args: {
        cmd: INSTALL_COMMAND,
        reason: 'render the quarterly report to PDF',
      },
    })
    render(<ToolApprovalModal />)
    // 'cmd'/'reason' are NOT tool schema keys: the summary must not pretend
    // they were understood — they surface as unsummarized content.
    expect(screen.getByText(/not summarized above/)).toBeInTheDocument()
    expect(screen.getByText('No installation command was included with this request.')).toBeInTheDocument()
    expect(screen.getByText(/"reason"/)).toBeInTheDocument()
  })

  it('shows the raw JSON when timeout_seconds is not a number (malformed known keys stay visible)', () => {
    seedAgent()
    enqueue({
      ...INSTALL_APPROVAL,
      args: { ...INSTALL_APPROVAL.args, timeout_seconds: 'forever' },
    })
    render(<ToolApprovalModal />)
    expect(screen.getByText(/not summarized above/)).toBeInTheDocument()
    expect(screen.getByText(/forever/)).toBeInTheDocument()
  })

  it('fully-covered requests render no technical-details block', () => {
    seedAgent()
    enqueue(INSTALL_APPROVAL)
    render(<ToolApprovalModal />)
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
describe('ToolApprovalModal — environment_setup branch-aware fidelity (MAJ-001/MIN-001 regression)', () => {
  it('shows the raw JSON when a poll carries extra known install fields (the session card never renders them)', () => {
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
    render(<ToolApprovalModal />)
    expect(screen.getByText(/not summarized above/)).toBeInTheDocument()
    // The ridden-in command text itself must be visible to the approver —
    // the fallback AUGMENTS the session summary, it does not replace it.
    expect(screen.getByText(/"command": "python3 -m venv/)).toBeInTheDocument()
    expect(screen.getByText('envsess-abc123')).toBeInTheDocument()
  })

  it('shows the raw JSON when a run carries session_id (the install card never renders it)', () => {
    seedAgent()
    enqueue({
      ...INSTALL_APPROVAL,
      args: { ...INSTALL_APPROVAL.args, session_id: 'envsess-leftover' },
    })
    render(<ToolApprovalModal />)
    expect(screen.getByText(/not summarized above/)).toBeInTheDocument()
    expect(screen.getByText(/"session_id": "envsess-leftover"/)).toBeInTheDocument()
  })

  it('shows the raw JSON when scope is not a tool-enum value (an unsupported scope is never presented as the workspace default)', () => {
    seedAgent()
    enqueue({
      ...INSTALL_APPROVAL,
      args: { ...INSTALL_APPROVAL.args, scope: 'global' },
    })
    render(<ToolApprovalModal />)
    expect(screen.getByText(/not summarized above/)).toBeInTheDocument()
    expect(screen.getByText(/"scope": "global"/)).toBeInTheDocument()
  })

  it('keeps a minimal poll (action + session_id only) fully covered — branch-aware coverage does not over-fallback', () => {
    seedAgent()
    enqueue(POLL_APPROVAL)
    render(<ToolApprovalModal />)
    expect(screen.queryByText(/not summarized above/)).not.toBeInTheDocument()
  })

  it('keeps an enum-valid explicit workspace scope fully covered', () => {
    seedAgent()
    enqueue({
      ...INSTALL_APPROVAL,
      args: { ...INSTALL_APPROVAL.args, scope: 'workspace' },
    })
    render(<ToolApprovalModal />)
    expect(screen.queryByText(/not summarized above/)).not.toBeInTheDocument()
    expect(screen.getByText('This workspace')).toBeInTheDocument()
  })
})
