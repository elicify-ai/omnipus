/**
 * chat.agent-created.test.ts — agent-picker freshness fix, WS half
 * (2026-09-28 fix round; GitHub issue #1009).
 *
 * Asserts that handleFrame({ type: 'agent_created', ... }) invalidates the
 * shared `['agents']` query — the coarse, D-107-style invalidate rather than
 * splicing one row into the cache (no full Agent payload is on the wire;
 * see AgentCreatedFrame in src/lib/api/generated/asyncapi-types.ts). Before
 * this case existed a tab that did not itself create the agent (the gateway
 * broadcasts on BOTH the REST create path and the create_agent tool path,
 * from any connected tab) kept serving its stale AgentPicker list until its
 * 30 s default staleTime elapsed (queryClient.ts) or a manual reload.
 *
 * Unlike library_changed (chat.library-changed.test.ts), this is NOT routed
 * through a debounce/coalesce scheduler — agent creation does not burst the
 * way a multi-file library write can, so the invalidate fires synchronously,
 * matching chat.task-run-status.test.ts's direct-invalidate shape.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'

import { useChatStore } from './chat'
import { queryClient } from '@/lib/queryClient'
import type { AgentCreatedFrame } from '@/lib/api/generated/asyncapi-types'

describe('chat handleFrame → agent_created (agent-picker freshness fix)', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
  })

  it('invalidates the shared agents query synchronously (no debounce)', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    const frame: AgentCreatedFrame = {
      type: 'agent_created',
      agent_id: 'agent-new-1',
    }

    useChatStore.getState().handleFrame(frame)

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['agents'] })
  })

  it('invalidates on every frame, including one arriving in the creating tab itself (broadcast is fan-out, not addressed)', () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    for (const agentId of ['agent-a', 'agent-b']) {
      invalidateSpy.mockClear()
      const frame: AgentCreatedFrame = { type: 'agent_created', agent_id: agentId }

      useChatStore.getState().handleFrame(frame)

      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['agents'] })
    }
  })
})
