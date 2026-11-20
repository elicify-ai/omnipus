// #1428 step 2, DEFECT 2 (Team B, real sidebar click): "Open" on an Unsent
// item for a chat in another workspace must land on that chat's OWN
// workspace and session (spec C r9 4.9: "Open (navigates)"; a session lives
// in exactly one workspace, so the address and the workspace chrome must name
// the item's workspace, never the one the person happens to be in).
//
// Repro: hold an upload in chat A (workspace w1), switch to Nova's chat N in
// workspace w2, release the upload (A's send is refused as switched, A gets an
// Unsent item), then press Open on that item from N.
//
// Oracle: the item's chat is chat-a in w1 (the fixture), so after Open the
// active workspace is w1 and the router is asked for w1's chat route with
// open=chat-a. The defect leaves the workspace at w2 and never navigates,
// so the address keeps /workspaces/w2/chat with ?open=chat-a.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act } from 'react'
import { fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  apiMocks, framesOf, makeAgent, makeSession, makeWorkspace, mountComposer, resetBindingWorld, sent, typeIntoComposer,
} from '@/test/sendBinding1428'
import { registerAppNavigate, type AppNavigate } from '@/lib/appNavigate'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'

function drop(name: string): void {
  const zone = screen.getByTestId('composer-card').parentElement
  if (!zone) throw new Error('drop zone missing')
  fireEvent.drop(zone, { dataTransfer: { files: [new File(['x'], name, { type: 'text/plain' })] } })
}

describe('DEFECT 2: Unsent Open lands on the chat\'s own workspace', () => {
  // The workspace chat route registers the router navigate in the app; here a recording stand-in.
  const navigate = vi.fn()
  let unregister = () => {}
  afterEach(() => { unregister() })

  it('Open from a chat in w2 on an item of chat-a (w1) switches to w1 and navigates to w1 with open=chat-a', async () => {
    resetBindingWorld()
    apiMocks.fetchWorkspaces.mockResolvedValue([makeWorkspace({ id: 'w1', name: 'W1' }), makeWorkspace({ id: 'w2', name: 'W2' })])
    apiMocks.fetchAgents.mockResolvedValue([makeAgent({ id: 'mia', name: 'Mia' }), makeAgent({ id: 'nova', name: 'Nova' })])
    apiMocks.fetchSessions.mockResolvedValue([
      makeSession({ id: 'chat-a', agent_id: 'mia', workspace_id: 'w1', title: 'Mia chat' }),
      makeSession({ id: 'chat-n', agent_id: 'nova', workspace_id: 'w2', title: 'Nova chat' }),
    ])
    mountComposer()
    drop('a.txt')
    let release: (value: unknown) => void = () => {}
    apiMocks.uploadFiles.mockImplementation(() => new Promise((resolve) => { release = resolve }))
    const user = userEvent.setup()
    await typeIntoComposer('from A')
    await user.click(screen.getByRole('button', { name: 'Send message' }))

    // The sidebar's own effect of clicking Nova's chat in w2.
    act(() => {
      useWorkspacesStore.getState().setActiveWorkspaceId('w2')
      useSessionStore.getState().setActiveSession('chat-n', 'nova', 'core')
    })
    await act(async () => {
      release({ files: [{ ref: 'media://workspace/w1/a', name: 'a.txt', content_type: 'text/plain' }] })
    })
    navigate.mockClear()
    unregister = registerAppNavigate(navigate as AppNavigate)
    const attachesBefore = framesOf('attach_session', sent()).length

    await user.click(screen.getByRole('button', { name: 'Open' }))

    expect(useSessionStore.getState().activeSessionId, 'Open opens chat-a').toBe('chat-a')
    expect(useWorkspacesStore.getState().activeWorkspaceId, 'Open switches to chat-a\'s workspace w1').toBe('w1')
    // Grok review: the chat route's entry skips an already-active session, so
    // Open itself must send the attach (as a sidebar selection does), or the
    // transcript never loads on a live connection.
    expect(framesOf('attach_session', sent()).slice(attachesBefore), 'Open attaches chat-a').toEqual([
      expect.objectContaining({ type: 'attach_session', session_id: 'chat-a' }),
    ])
    expect(navigate.mock.calls, 'Open navigates to w1\'s chat route naming chat-a').toEqual([[{ workspaceId: 'w1', open: 'chat-a' }]])
  })
})
