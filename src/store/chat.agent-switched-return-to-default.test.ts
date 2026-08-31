/**
 * chat.agent-switched-return-to-default.test.ts — FIX 3 regression.
 *
 * The backend (pkg/gateway/websocket.go's agent_switched frame builder)
 * deliberately omits `agent_id` on the wire specifically for a
 * return-to-default switch_agent call — that is the correct, intentional
 * wire shape (see pkg/tools/handoff.go / ADR-071 §5.2.2).
 *
 * Before this fix, chat.ts's `case 'agent_switched'` handler was gated on
 * `if (newAgentId)`, so an absent agent_id silently did nothing: the agent
 * picker kept showing whoever the conversation had most recently been
 * handed off to, and never updated to reflect the return to the default
 * agent.
 *
 * The fix resolves the configured default agent the same way the rest of
 * the app already does — the cached `['agents']` react-query list, keying
 * off `Agent.default === true` (itself derived server-side from
 * cfg.Agents.Defaults.DefaultAgentID; see AgentCard.tsx, WorkspaceTeamTab.tsx,
 * AddAgentPicker.tsx for the same pattern) — and applies it via the same
 * server-authoritative `applyServerAgentSwitch` path a named switch uses.
 */

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import { queryClient } from '@/lib/queryClient'
import { makeAgent } from '@/test/factories'

const SID = 'sess-return-to-default'

function resetStores() {
  useSessionStore.setState({
    activeSessionId: SID,
    activeAgentId: 'ray',
    activeAgentType: 'core',
    agentSelectionSource: 'auto',
    agentSelectionWorkspaceId: null,
  })
  queryClient.removeQueries({ queryKey: ['agents'] })
}

beforeEach(() => {
  resetStores()
})

describe('chat store — agent_switched with agent_id absent (return to default, FIX 3)', () => {
  it('resolves the configured default agent from the cached agents list and applies it', () => {
    queryClient.setQueryData(
      ['agents'],
      [
        makeAgent({ id: 'ray', name: 'Ray', default: false }),
        makeAgent({ id: 'mia', name: 'Mia', default: true }),
      ],
    )

    // The conversation was previously handed off to Ray.
    expect(useSessionStore.getState().activeAgentId).toBe('ray')

    useChatStore.getState().handleFrame({
      type: 'agent_switched',
      session_id: SID,
      // agent_id deliberately absent — the backend's return-to-default shape.
    })

    // The picker must now show the DEFAULT agent (mia), not stay on ray.
    expect(useSessionStore.getState().activeAgentId).toBe('mia')
    expect(useSessionStore.getState().activeAgentType).toBe('Main')
  })

  it('is a no-op-but-visible fallback (refetches ["agents"]) when the agents list is not yet cached', () => {
    // No queryClient.setQueryData call — simulates the frame arriving before
    // the agent list has ever been fetched.
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    useChatStore.getState().handleFrame({
      type: 'agent_switched',
      session_id: SID,
    })

    // Must not silently do nothing: with no cached default to apply, it
    // must at least trigger a refetch so a subsequent frame/render can
    // resolve correctly, rather than leaving the picker on the stale agent
    // forever with no recovery path.
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['agents'] })
    // activeAgentId is unchanged — there was nothing to apply yet.
    expect(useSessionStore.getState().activeAgentId).toBe('ray')
  })

  it('still applies a NAMED switch normally when agent_id is present (regression guard)', () => {
    queryClient.setQueryData(
      ['agents'],
      [
        makeAgent({ id: 'ray', name: 'Ray', default: false }),
        makeAgent({ id: 'mia', name: 'Mia', default: true }),
      ],
    )

    useChatStore.getState().handleFrame({
      type: 'agent_switched',
      session_id: SID,
      agent_id: 'jim',
    })

    expect(useSessionStore.getState().activeAgentId).toBe('jim')
  })
})
