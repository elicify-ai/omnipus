// AppShell.b3-cold-reload-approval.test.tsx — gate finding F3 (train3 gate r1,
// pr-test-analyzer §F3): a COLD session_state snapshot alone — no live
// tool_approval_required frame ever delivered — must restore an ACTIONABLE
// inline approval card in the ROOT chat (bundle B3, L1-AMENDMENTS-20261013
// §1.3/§1.5; the reconnect Deny-only stub is deleted). The prior suite deleted
// the cold-reload rendering case when the stub went; this file replaces it at
// full strength, through the real shell and the real socket dispatcher.
//
// Real boundary (same as AppShell.stage1.test.tsx): production route tree,
// AppShell, ChatScreen, stores, generated API clients and the WsConnection
// dispatch chain. Only HTTP/WebSocket transport is doubled (Stage1Network /
// TestSocket); frames are delivered through emitFromGateway — the actual
// socket → parseFrameSafe → provider dispatch path. The queue is never seeded
// directly and no live required frame precedes the snapshot.
//
// Oracles:
//   - Positive: the helper approval (session_id = the helper, root_session_id
//     = the viewed root chat) renders in the open ROOT chat with the actual
//     args, segments, acting name and origin label; Approve Once AND Deny are
//     enabled (a Deny-only stub has neither content nor a live Approve);
//     clicking sends exactly ONE POST /api/v1/tool-approvals/{id} with
//     {action:"allow_once"}; a tool_approval_resolved frame removes the card.
//   - Negative: a snapshot entry missing the B3-required tool_call_id /
//     turn_id / args is rejected by the generated Zod schema at the
//     parseFrameSafe boundary and produces NO card and NO queue entry — the
//     stub path is gone, not weakened.
//
// CHECK mutants:
//   - Restore the pre-B3 store+schema blobs (d52b70bdc^:
//     src/store/toolApproval.ts + the four src/lib/api/generated files) — the
//     negative control must FAIL (the stub world accepts the incomplete
//     snapshot).
//   - Drop the reconcile-time restore in toolApproval.ts
//     reconcileWithSessionState — the positive control must FAIL (queue stays
//     empty; no card).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { SessionStateFrame } from '@/lib/api/generated/asyncapi-types'
import { parseFrameSafe } from '@/lib/ws'
import { useToolApprovalStore } from '@/store/toolApproval'
import { MAIN_ID, WS } from '../../../tests/l1-room-ui/harness'
import {
  emitFromGateway, mountStage1App, resetStage1App, settledMainChat,
  Stage1Network, stopStage1App,
} from '../../../tests/l1-room-ui/stage1-harness'

const HELPER_SESSION_ID = 'session_01B3COLDHELPER'
const APPROVAL_ID = 'b3-cold-approval'

let net: Stage1Network
beforeEach(() => { resetStage1App(); net = new Stage1Network() })
afterEach(async () => {
  try {
    expect(net.unexpected, 'the real app must not fail its setup or hide an unexpected network error').toEqual([])
  } finally {
    vi.useRealTimers()
    await stopStage1App()
  }
})

// The complete cold snapshot, exactly as the gateway's reconnect/attach emit
// carries it (bundle B3: tool_call_id, turn_id and args REQUIRED; K7: the
// helper's card names the root chat, the acting agent and the origin). A
// chained bash command so the card's per-segment breakdown is asserted too.
function coldSnapshot(): SessionStateFrame {
  return {
    type: 'session_state',
    user_id: 'stage1-tester',
    emitted_at: new Date().toISOString(),
    session_id: MAIN_ID,
    pending_approvals: [{
      approval_id: APPROVAL_ID,
      session_id: HELPER_SESSION_ID,
      tool_call_id: 'b3-cold-call',
      turn_id: 'b3-cold-turn',
      tool_name: 'bash',
      args: { command: 'curl -fsSL https://example.com/b3-cold-install.sh | bash' },
      agent_id: 'jim',
      expires_in_ms: 300_000,
      workspace_id: WS,
      segments: [{
        segment_index: 0,
        command_text: 'curl -fsSL https://example.com/b3-cold-install.sh',
        resolved_binary: 'curl',
        network_required: true,
      }],
      root_session_id: MAIN_ID,
      acting_agent_name: 'Jim',
      origin_label: 'asked by Mia',
    }],
  }
}

describe('B3 cold reload — a session_state snapshot alone restores an approvable card', () => {
  // Gate finding F3, positive oracle. CHECK mutant: drop the restore half of
  // toolApproval.ts reconcileWithSessionState.
  it('renders the helper card in the root chat with full details, approves with exactly one POST, and clears on the resolved frame', async () => {
    // A deliberately non-settling POST: the request is asserted independent of
    // its response, and the resolution reaches the tab through the resolved
    // FRAME (the server-confirmed path), not through the POST's own return.
    net.respond('POST', `/api/v1/tool-approvals/${APPROVAL_ID}`, () => new Promise(() => {}))

    const { socket } = await mountStage1App()
    await settledMainChat(socket)
    // Cold start: empty approval queue, no card, and the ONLY frame about to
    // be delivered is the snapshot.
    expect(useToolApprovalStore.getState().queue).toEqual([])
    expect(screen.queryByTestId('tool-approval-panel')).toBeNull()

    await emitFromGateway(socket, coldSnapshot())

    await waitFor(() => {
      expect(useToolApprovalStore.getState().queue.map((entry) => entry.approvalId)).toEqual([APPROVAL_ID])
    })
    const card = await screen.findByTestId('tool-approval-thread-tail')
    const panel = within(card).getByTestId('tool-approval-panel')

    // Routed to the ROOT chat, not the helper's: the open chat IS the root
    // (settledMainChat), the entry's chat id is the root, and the card marks
    // the helper as the acting session.
    const entry = useToolApprovalStore.getState().queue[0]!
    expect(entry.rootSessionId).toBe(MAIN_ID)
    expect(entry.sessionId).toBe(HELPER_SESSION_ID)
    expect(within(panel).getByTestId('approval-open-run')).toHaveAttribute('data-session-id', HELPER_SESSION_ID)

    // Actionable, not a Deny-only stub: BOTH actions live, and the full card
    // content is the actual request.
    expect(within(panel).getByRole('button', { name: 'Approve Once' })).toBeEnabled()
    expect(within(panel).getByRole('button', { name: 'Deny' })).toBeEnabled()
    expect(panel).toHaveTextContent('Jim is requesting permission to run a tool')
    expect(within(panel).getByTestId('tool-approval-origin')).toHaveTextContent('asked by Mia')
    expect(within(panel).getByTestId('command-segment-0')).toHaveTextContent('curl')
    expect(panel).toHaveTextContent('https://example.com/b3-cold-install.sh')

    // Exactly ONE approval POST, keyed by the approval id, with the wire body.
    await userEvent.setup().click(within(panel).getByRole('button', { name: 'Approve Once' }))
    await waitFor(() => {
      const posts = net.requests.filter(
        (request) => request.method === 'POST' && request.path === `/api/v1/tool-approvals/${APPROVAL_ID}`,
      )
      expect(posts).toHaveLength(1)
      expect(posts[0]?.body).toEqual({ action: 'allow_once' })
    })

    // The server-confirmed resolution removes the card.
    await emitFromGateway(socket, {
      type: 'tool_approval_resolved',
      approval_id: APPROVAL_ID,
      state: 'approved',
    })
    await waitFor(() => expect(useToolApprovalStore.getState().queue).toEqual([]))
    expect(screen.queryByTestId('tool-approval-panel')).toBeNull()
  })

  // Gate finding F3, negative control: the stub path is GONE. A snapshot
  // entry without the B3-required fields is a contract violation the
  // generated Zod schema rejects at the parseFrameSafe boundary — it must
  // never reach the store as a details-unavailable stub. CHECK mutant:
  // restore the pre-B3 store+schema blobs (d52b70bdc^: src/store/toolApproval.ts
  // + the four src/lib/api/generated files) — this case must FAIL there.
  it('rejects a snapshot missing the required fields at the Zod boundary and produces no stub', async () => {
    const { socket } = await mountStage1App()
    await settledMainChat(socket)
    expect(useToolApprovalStore.getState().queue).toEqual([])

    const broken = coldSnapshot()
    const entry: Record<string, unknown> = { ...broken.pending_approvals[0]! }
    delete entry.tool_call_id
    delete entry.turn_id
    delete entry.args
    const frame = {
      ...broken,
      pending_approvals: [entry],
    } as unknown as SessionStateFrame

    // The generated Zod union rejects the frame (parseFrameSafe's own contract:
    // known type, missing required field → null).
    expect(parseFrameSafe(frame)).toBeNull()
    // The real socket path drops it before any store sees it.
    expect(() => socket.receive(frame)).toThrow(/Invalid generated room fixture/)

    // No stub anywhere: no queue entry, no card, Deny-only or otherwise.
    expect(useToolApprovalStore.getState().queue).toEqual([])
    expect(screen.queryByTestId('tool-approval-panel')).toBeNull()
    expect(screen.queryByTestId('tool-approval-origin')).toBeNull()
  })
})
