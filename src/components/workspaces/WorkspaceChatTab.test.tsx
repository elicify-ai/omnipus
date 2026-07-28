// Unit tests for WorkspaceChatTab — the D4 fix's user-visible half.
//
// WorkspaceChatTab has exactly ONE decision to make:
//   isRestoringSession (resolvingSessionForWorkspace[workspaceId] === true)
//     ? <ChatRestoreSkeleton />
//     : <ChatScreen />
//
// Session-restore MECHANICS (enterWorkspaceChat, resolveRememberedSessionFromServer,
// sessionByWorkspace bookkeeping) are already covered by src/store/session.workspace.test.ts.
// This file covers the RENDER decision that sits on top of that state — the
// half of the D4 fix that store-level tests cannot see: before this fix, a
// cold reload could flash the "Select an agent" Welcome screen for an
// instant before the real conversation loaded. That flash is exactly what
// showing ChatRestoreSkeleton while resolvingSessionForWorkspace[workspaceId]
// is true prevents.
//
// ChatScreen itself is a large, independently and extensively tested
// component (25+ dedicated ChatScreen.*.test.tsx files, including its own
// `messages.length === 0 -> WelcomeState` branch — ChatScreen.tsx around
// line 2797-2799). It is mocked here as a self-contained stub — mirroring
// WorkspaceTabContainer.test.tsx's treatment of ChatControls — so this suite
// stays scoped to WorkspaceChatTab's own routing decision instead of
// re-testing (or fragile-mocking) ChatScreen's assistant-ui/query/store
// dependency tree.
//
// The real (unmocked) session store is used and reset via setState, per the
// established convention in session.workspace.test.ts ("verify STATE
// OUTCOMES ... to avoid Zustand singleton spy-accumulation issues").

import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { useSessionStore } from '@/store/session'

import { vi } from 'vitest'

vi.mock('@/components/chat/ChatScreen', () => ({
  ChatScreen: () => <div data-testid="chat-screen-mock">ChatScreen</div>,
}))

import { WorkspaceChatTab } from './WorkspaceChatTab'

function resetSessionStore() {
  // Merge-only reset (no replace=true) — preserves Zustand action references,
  // matching session.workspace.test.ts's resetAll() convention.
  useSessionStore.setState({
    activeSessionId: null,
    activeAgentId: null,
    activeAgentType: null,
    attachedSessionType: null,
    attachedTaskTitle: null,
    sessionByWorkspace: {},
    resolvingSessionForWorkspace: {},
  })
}

describe('WorkspaceChatTab', () => {
  beforeEach(() => {
    resetSessionStore()
  })

  it('renders the restore skeleton while resolvingSessionForWorkspace[workspaceId] is true', () => {
    // BDD: Given the workspace's most-recent session is being resolved from
    // the server (cold-reload restore in flight),
    // When WorkspaceChatTab renders,
    // Then it shows ChatRestoreSkeleton, not ChatScreen — this is the frame
    // that used to flash the Welcome screen before the D4 fix.
    useSessionStore.setState({
      resolvingSessionForWorkspace: { 'ws-restoring': true },
    })

    render(<WorkspaceChatTab workspaceId="ws-restoring" />)

    expect(screen.getByTestId('workspace-chat-restoring')).toBeTruthy()
    expect(screen.getByText(/restoring your conversation/i)).toBeTruthy()
    expect(screen.queryByTestId('chat-screen-mock')).toBeNull()
  })

  it('renders ChatScreen once resolution completes and a session was found', () => {
    // BDD: Given resolveWorkspaceSessionFromServer found and restored the
    // workspace's last conversation (resolvingSessionForWorkspace[workspaceId]
    // explicitly flipped back to false, an active session is set),
    // When WorkspaceChatTab renders,
    // Then it hands off to ChatScreen (the restored conversation renders
    // there), not the skeleton.
    useSessionStore.setState({
      resolvingSessionForWorkspace: { 'ws-active': false },
      activeSessionId: 'sess-1',
      activeAgentId: 'mia',
      sessionByWorkspace: {
        'ws-active': { id: 'sess-1', type: 'chat', title: 'A conversation', agentId: 'mia' },
      },
    })

    render(<WorkspaceChatTab workspaceId="ws-active" />)

    expect(screen.getByTestId('chat-screen-mock')).toBeTruthy()
    expect(screen.queryByTestId('workspace-chat-restoring')).toBeNull()
  })

  it('renders ChatScreen (never gets stuck on the skeleton) for a workspace with genuinely no sessions', () => {
    // BDD: Given a workspace that has never had a conversation at all —
    // resolvingSessionForWorkspace[workspaceId] was never set to true in the
    // first place (distinct from the previous test's explicit `false`: a
    // brand-new workspace's key is simply ABSENT from the record, e.g.
    // because enterWorkspaceChat's server lookup found nothing to restore),
    // When WorkspaceChatTab renders,
    // Then it still hands off to ChatScreen — landing on ChatScreen's own
    // Welcome state (a genuinely empty conversation) is the CORRECT outcome,
    // not a bug. This is the case explicitly called out by the test-coverage
    // gate: it must be told apart from "still restoring."
    //
    // This also guards a real regression shape: an implementation gated on
    // `resolvingSessionForWorkspace[workspaceId] !== false` (instead of
    // `=== true`) would pass the previous test (key present, exactly
    // `false`) but WOULD wrongly show the skeleton forever here, because
    // `undefined !== false` is true. Only reading the record with a KEY
    // ABSENT (rather than reusing the previous test's explicit `false`)
    // exercises that boundary.
    useSessionStore.setState({
      resolvingSessionForWorkspace: {}, // key for 'ws-empty' never set
      activeSessionId: null,
      sessionByWorkspace: { 'ws-empty': null },
    })

    render(<WorkspaceChatTab workspaceId="ws-empty" />)

    expect(screen.getByTestId('chat-screen-mock')).toBeTruthy()
    expect(screen.queryByTestId('workspace-chat-restoring')).toBeNull()
  })

  it('scopes the restoring check to the rendered workspaceId, not any other workspace in the record', () => {
    // BDD: Given TWO workspaces are tracked in resolvingSessionForWorkspace
    // with DIFFERENT values (ws-A mid-restore, ws-B already resolved),
    // When WorkspaceChatTab renders for each workspaceId in turn,
    // Then each reads its OWN entry — proving the lookup is keyed by the
    // workspaceId prop, not a stale/global/first-key read that would show
    // the same result for both. Catches a hardcoded-key or "just read
    // Object.values(...)[0]" bug that a same-value single-workspace test
    // cannot distinguish from correct behavior.
    useSessionStore.setState({
      resolvingSessionForWorkspace: { 'ws-a': true, 'ws-b': false },
    })

    const { unmount } = render(<WorkspaceChatTab workspaceId="ws-a" />)
    expect(screen.getByTestId('workspace-chat-restoring')).toBeTruthy()
    expect(screen.queryByTestId('chat-screen-mock')).toBeNull()
    unmount()

    render(<WorkspaceChatTab workspaceId="ws-b" />)
    expect(screen.getByTestId('chat-screen-mock')).toBeTruthy()
    expect(screen.queryByTestId('workspace-chat-restoring')).toBeNull()
  })
})
