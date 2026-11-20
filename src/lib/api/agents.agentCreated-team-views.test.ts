// agents.agentCreated-team-views.test.ts — T-7 of epic #1094 slice S1a (RED).
// Oracle: export-import-s1-spec.md revision 7 — FR-056 ("On agent_created, the
// app MUST also invalidate every workspace detail and delegation query"),
// "Integration Boundaries → App cache (T-7)" (workspacesQueryKeys.detail(*) and
// .delegation(*) for ALL workspaces, because the frame carries no workspace ID
// and its contract is coarse on purpose), BDD "Team tab refreshes while open",
// and the regression-table row "agent_created refresh" (the list refresh still
// happens). Expected values come from the spec and the existing key shapes in
// workspaces.ts, never from the implementation.
//
// The unit is real TanStack QueryClient v5 (no rendering, no mocked
// invalidation): lists get real QueryObservers so a refetch is observable —
// that is what makes the "no duplicate refetch storm" assertion able to fail.
import { describe, expect, it, vi } from 'vitest'
import { QueryClient, QueryObserver } from '@tanstack/react-query'
import { invalidateAfterAgentCreate } from './agents'
import { workspacesQueryKeys } from './workspaces'

// Two seeded workspaces: FR-056 says EVERY workspace's detail and delegation
// query is invalidated ("all workspaces", not just one).
const WS_IDS = ['ws-one', 'ws-two'] as const

function seedClient(): QueryClient {
  const client = new QueryClient({
    defaultOptions: { queries: { queryFn: () => Promise.resolve({}), retry: false } },
  })
  client.setQueryData(['agents'], [{ id: 'agent-1' }])
  client.setQueryData(workspacesQueryKeys.list({ status: 'active' }), [{ id: WS_IDS[0] }])
  client.setQueryData(workspacesQueryKeys.list({ status: 'archived' }), [])
  for (const id of WS_IDS) {
    client.setQueryData(workspacesQueryKeys.detail(id), { id, name: `WS ${id}` })
    client.setQueryData(workspacesQueryKeys.delegation(id), { id, delegation: [] })
  }
  return client
}

// attachObserver subscribes a real QueryObserver so the query is ACTIVE:
// invalidateQueries then actually refetches it, and dataUpdateCount moves.
async function attachObserver(client: QueryClient, key: readonly unknown[]) {
  const observer = new QueryObserver(client, { queryKey: [...key] })
  const unsubscribe = observer.subscribe(() => {})
  await vi.waitFor(() => {
    expect(observer.getCurrentResult().isFetching, `initial fetch of ${JSON.stringify(key)}`).toBe(false)
  })
  return { observer, unsubscribe }
}

// fetchCountDelta subscribes to the query cache and counts fetch starts for
// exactly `key` from now on. This is the storm meter: each invalidateQueries
// call that matches an active query starts exactly one fetch.
function fetchCountDelta(client: QueryClient, key: readonly unknown[]) {
  let count = 0
  const unsubscribe = client.getQueryCache().subscribe((event) => {
    if (event.type === 'updated' && event.action?.type === 'fetch' && JSON.stringify(event.query.queryKey) === JSON.stringify(key)) {
      count += 1
    }
  })
  return {
    read: () => count,
    unsubscribe,
  }
}

describe('invalidateAfterAgentCreate (T-7, FR-056)', () => {
  it('invalidates every workspace detail and delegation query on agent_created', async () => {
    const client = seedClient()

    invalidateAfterAgentCreate(client)

    // invalidateQueries marks matching queries invalid before it returns, so
    // this is exact, not a timed wait. Today neither key is touched.
    for (const id of WS_IDS) {
      expect(
        client.getQueryState(workspacesQueryKeys.detail(id))?.isInvalidated,
        `workspace detail of ${id} must be invalidated (FR-056)`,
      ).toBe(true)
      expect(
        client.getQueryState(workspacesQueryKeys.delegation(id))?.isInvalidated,
        `workspace delegation of ${id} must be invalidated (FR-056)`,
      ).toBe(true)
    }
  })

  // CONTROL — passes before T-7 lands. Guards the regression-table promise
  // ("the list refresh still happens") while the new keys are added.
  it('still refreshes the agent list and both workspace status lists (control)', async () => {
    const client = seedClient()
    const agents = await attachObserver(client, ['agents'])
    const active = await attachObserver(client, workspacesQueryKeys.list({ status: 'active' }))
    const archived = await attachObserver(client, workspacesQueryKeys.list({ status: 'archived' }))

    // dataUpdateCount lives on the query state. The observer result's copy is
    // not the source the assertion reads, so both sides use getQueryState.
    const agentsBefore = client.getQueryState(['agents'])?.dataUpdateCount
    const activeBefore = client.getQueryState(workspacesQueryKeys.list({ status: 'active' }))?.dataUpdateCount
    const archivedBefore = client.getQueryState(workspacesQueryKeys.list({ status: 'archived' }))?.dataUpdateCount
    if (agentsBefore === undefined || activeBefore === undefined || archivedBefore === undefined) {
      throw new Error('the list queries did not record a dataUpdateCount — the refetch instrument is blind')
    }

    invalidateAfterAgentCreate(client)

    await vi.waitFor(() => {
      expect(client.getQueryState(['agents'])?.dataUpdateCount).toBe(agentsBefore + 1)
      expect(client.getQueryState(workspacesQueryKeys.list({ status: 'active' }))?.dataUpdateCount).toBe(activeBefore + 1)
      expect(client.getQueryState(workspacesQueryKeys.list({ status: 'archived' }))?.dataUpdateCount).toBe(archivedBefore + 1)
    })

    agents.unsubscribe()
    active.unsubscribe()
    archived.unsubscribe()
  })

  // CONTROL — passes before T-7 lands (the current implementation touches each
  // list once). It fails if the new detail/delegation invalidation is done with
  // one blanket ['workspaces'] invalidate: that key also matches both list
  // queries, so each list would refetch twice — the duplicate refetch storm.
  it('refetches each list exactly once — no duplicate refetch storm (control)', async () => {
    const client = seedClient()
    const observers = [
      await attachObserver(client, ['agents']),
      await attachObserver(client, workspacesQueryKeys.list({ status: 'active' })),
      await attachObserver(client, workspacesQueryKeys.list({ status: 'archived' })),
    ]

    const agentFetches = fetchCountDelta(client, ['agents'])
    const activeFetches = fetchCountDelta(client, workspacesQueryKeys.list({ status: 'active' }))
    const archivedFetches = fetchCountDelta(client, workspacesQueryKeys.list({ status: 'archived' }))

    invalidateAfterAgentCreate(client)

    await vi.waitFor(() => {
      expect(agentFetches.read()).toBe(1)
      expect(activeFetches.read()).toBe(1)
      expect(archivedFetches.read()).toBe(1)
    })

    agentFetches.unsubscribe()
    activeFetches.unsubscribe()
    archivedFetches.unsubscribe()
    for (const o of observers) o.unsubscribe()
  })
})
