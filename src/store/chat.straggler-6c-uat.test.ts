// Chief's 2026-11-02 CHECK follow-up F4 / C3: an untagged late tool
// behind a discarded/failed user must not acquire an active turn's identity.
// Real socket, reducers, latest-history adapter and ownership binding; only
// the server/browser transport boundaries are controlled by the shared harness.
import { act } from 'react'
import { afterEach, beforeEach, expect, it } from 'vitest'
import { useChatStore } from '@/store/chat'
import { AVAILABLE, STAMP, cleanup, reset } from '../../tests/chat-history-paging/final-gate-harness'
import { A, SID, TURN, assistantRows, begin, bucket, prose, rest, socket } from '../../tests/chat-history-paging/s11-fold-harness'

beforeEach(() => { reset(); begin() })
afterEach(cleanup)

it.each(['discarded', 'failed'] as const)(
  'F4 C3: a %s tail leaves its straggler unstamped and unadopted by latest history',
  async state => {
    act(() => { useChatStore.getState().sendMessage('hello') })
    const users = bucket().messageOrder.map(id => bucket().messagesById[id]).filter(row => row.role === 'user')
    expect(users, 'instrument: a real send created exactly one user row').toHaveLength(1)
    socket().deliver({ type: 'message_status', session_id: SID, client_message_id: users[0].id, state })
    expect(bucket().messagesById[users[0].id].deliveryStatus, 'instrument: the user tail has the required disposition').toBe(state)
    expect(bucket().isStreaming, 'C3: the rejected send has no live reply').toBe(false)

    // A late state announcement still identifies the stopped turn. Without
    // this nonempty precondition, even a missing !tailUser guard would pass.
    socket().deliver({ ...AVAILABLE, active_turn: { turn_id: TURN, agent_id: 'mia', started_at: STAMP } })
    expect(bucket().activeTurnId, 'instrument: a turn id is genuinely available to stamp').toBe(TURN)
    expect(bucket().messagesById[bucket().messageOrder.at(-1)!].id, 'instrument: the rejected user is still the raw tail').toBe(users[0].id)
    const call = '6c-discard-straggler'
    socket().deliver({ type: 'tool_call_start', session_id: SID, call_id: call, tool: 'bash', agent_id: 'mia', params: {} })
    const owner = bucket().toolCallOwnerMessageId?.[call]
    expect(typeof owner, 'instrument: the real start created an owned tool card').toBe('string')
    const placeholder = bucket().messagesById[owner!]
    expect(placeholder, 'F4: the live placeholder must not borrow the announced turn').toMatchObject({
      role: 'assistant', content: '', status: 'done', isStreaming: false,
    })
    expect.soft(placeholder.turnId, 'F4: a discarded/failed straggler carries no turnId').toBeUndefined()
    expect(bucket().toolCalls[call].status, 'instrument: check while the real card is still pending').toBe('running')

    // J2's otherwise-eligible target exists at the latest cut. The straggler
    // must remain on its own row, not get moved into A's saved turn segment.
    await rest([prose(1, A, 'Saved answer.')])
    expect(bucket().activeTurnId, 'instrument: the latest read did not remove the active-turn precondition').toBe(TURN)
    expect(bucket().toolCallOwnerMessageId?.[call], 'F4: latest history cannot adopt the straggler into A').toBe(owner)
    expect(bucket().messagesById[owner!]?.turnId, 'F4: the retained straggler remains unstamped').toBeUndefined()
    expect(assistantRows().map(row => row.id), 'F4: saved A and the unrelated straggler remain distinct').toEqual([A, owner])
    expect(bucket().messagesById[A].content).toBe('Saved answer.')
    expect(bucket().messagesById[A].tool_calls ?? [], 'F4: the saved answer does not acquire the stray card').toEqual([])

    socket().deliver({ type: 'tool_call_result', session_id: SID, call_id: call, tool: 'bash', status: 'success', result: 'ok' }, AVAILABLE)
    // Preserve the original C3 no-streaming/no-stuck-Stop invariants after the
    // stale active-turn announcement is withdrawn by a real state frame.
    const rows = bucket().messageOrder.map(id => bucket().messagesById[id])
    expect(rows.some(row => row.isStreaming || row.status === 'streaming'), 'C3: no streaming bubble minted for the straggler').toBe(false)
    expect(Object.values(bucket().toolCalls).some(tool => tool.status === 'running'), 'C3: no tool remains running after its result').toBe(false)
    expect(bucket().isStreaming, 'C3: Stop must not remain up with no activity').toBe(false)
  },
)
