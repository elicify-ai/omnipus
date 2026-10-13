// WU-10 founder-added #215: current agent creation/update/deletion must refresh
// the live roster and workspace team data without leaving the chat.
// Oracle: #215 Expected + team-lead's 2026-10-14 founder dispatch (canonical
// create_agent/update_agent/delete_agent; success invalidates, error does not).
// Real chat reducer, real application QueryClient and real cache operations.
// No query/reducer/helper/invalidate spy or mock, no agent_created stand-in.
import { beforeEach, afterEach, describe, expect, it } from 'vitest'
import type { ToolCallStartFrame, ToolCallResultFrame } from '@/lib/api/generated/asyncapi-types'
import { queryClient } from '@/lib/queryClient'
import { workspacesQueryKeys } from '@/lib/api'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { MIA, SALES, delegation, workspace } from '../../../tests/omnipus-l1-ui-red/fixtures'

const MAIN = 'opaque-omnipus-main-215'
const HELPER = 'opaque-omnipus-apply-helper-215'
const ARCHIVED = workspace('archived-sales', { status: 'archived', core_team: ['mia'] })
const KEYS = {
  agents: ['agents'],
  active: workspacesQueryKeys.list({ status: 'active' }),
  archived: workspacesQueryKeys.list({ status: 'archived' }),
  detail: workspacesQueryKeys.detail('sales'),
  delegation: workspacesQueryKeys.delegation('sales'),
}
const fresh = { agents: false, active: false, archived: false, detail: false, delegation: false }
const invalidated = { agents: true, active: true, archived: true, detail: true, delegation: true }

function cacheFlags() {
  return Object.fromEntries(Object.entries(KEYS).map(([label, key]) => {
    const state = queryClient.getQueryState(key)
    expect(state, `instrument: ${label} query actually exists before inspecting its flag`).toBeDefined()
    return [label, state!.isInvalidated]
  }))
}
function cacheData() {
  return Object.fromEntries(Object.entries(KEYS).map(([label, key]) => [label, queryClient.getQueryData(key)]))
}
function resolveTool(tool: string, status: ToolCallResultFrame['status'], sessionId = MAIN) {
  const callId = `${tool}-${status}`
  const start: ToolCallStartFrame = { type: 'tool_call_start', session_id: sessionId, tool, call_id: callId, params: {} }
  const result: ToolCallResultFrame = {
    type: 'tool_call_result', session_id: sessionId, tool, call_id: callId,
    status, result: status === 'success' ? { agent_id: 'penny', workspace_id: 'sales' } : { error: 'Mutation refused' },
    ...(status === 'error' ? { error: 'Mutation refused' } : {}),
  }
  useChatStore.getState().handleFrame(start)
  useChatStore.getState().handleFrame(result)
  // Positive consumption control: a dropped/wrong-scope frame cannot fake RED
  // (or a passing no-invalidation negative). The real bucket received the result.
  expect(useChatStore.getState().sessionsById[sessionId].toolCalls[callId]).toMatchObject({
    tool, status, result: result.result,
  })
}

beforeEach(() => {
  queryClient.clear()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState({ ...useSessionStore.getInitialState(), activeSessionId: MAIN, activeAgentId: 'omnipus' }, true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  useWorkspacesStore.setState({ activeWorkspaceId: 'sales' })
  // Given actual cached data, not mocked useQuery output. Inactive caches keep
  // isInvalidated observable without a fast refetch racing it back to false.
  queryClient.setQueryData(KEYS.agents, [MIA])
  queryClient.setQueryData(KEYS.active, [SALES])
  queryClient.setQueryData(KEYS.archived, [ARCHIVED])
  queryClient.setQueryData(KEYS.detail, SALES)
  queryClient.setQueryData(KEYS.delegation, delegation(SALES))
  expect(cacheFlags()).toEqual(fresh)
})
afterEach(() => {
  queryClient.clear()
  useChatStore.setState(useChatStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
})

describe('#215 tool_call_result refreshes real roster/team caches', () => {
  it.each(['create_agent', 'update_agent', 'delete_agent'])(
    '#215 successful %s invalidates agents and all cached workspace team carriers without navigation', (tool) => {
      resolveTool(tool, 'success')
      expect(cacheFlags()).toEqual(invalidated)
      expect(useSessionStore.getState().activeSessionId).toBe(MAIN)
      expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('sales')
    },
  )
  it.each(['create_agent', 'update_agent', 'delete_agent'])(
    '#215 failed %s is recorded but never invalidates or rewrites cached agents/team data', (tool) => {
      const before = cacheData()
      resolveTool(tool, 'error')
      expect(cacheFlags()).toEqual(fresh)
      expect(cacheData()).toEqual(before)
      expect(useSessionStore.getState().activeSessionId).toBe(MAIN)
    },
  )
  it('#215 a successful Omnipus apply-helper mutation refreshes the global roster while its main stays foreground', () => {
    resolveTool('create_agent', 'success', HELPER)
    expect(cacheFlags()).toEqual(invalidated)
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN)
    expect(useWorkspacesStore.getState().activeWorkspaceId).toBe('sales')
  })
  it('#215 instrument control detects public invalidation and does not misclassify a read-tool result as a mutation', async () => {
    resolveTool('list_agents', 'success')
    expect(cacheFlags()).toEqual(fresh)
    await queryClient.invalidateQueries({ queryKey: KEYS.agents })
    expect(cacheFlags()).toEqual({ ...fresh, agents: true })
  })
})
