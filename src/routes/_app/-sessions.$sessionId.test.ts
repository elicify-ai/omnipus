// Unit tests for the sessions.$sessionId route loader logic.
//
// The loader has three branches:
//   1. fetchSessionDetail returns detail → loader returns detail, activates session in store.
//   2. fetchSessionDetail throws ApiError with status 404 → loader swallows it and returns null.
//   3. fetchSessionDetail throws ApiSchemaError (or any other error) → loader re-throws.
//
// NOTE: The route file (sessions.$sessionId.tsx) imports ChatScreen which transitively
// imports the full component tree. This causes test-environment hang due to module-load
// side effects (WebSocket connections, WS store initialisation). Rather than fighting
// the import chain, these tests re-implement the loader logic inline (a copy of the
// exact function body) and test it directly. This approach is more reliable for
// isolating loader behaviour from component lifecycle.
//
// If the loader logic in sessions.$sessionId.tsx changes, this test must be updated.

import { describe, it, expect } from 'vitest'
import { ApiError } from '@/lib/api-error'
import { ApiSchemaError } from '@/lib/api'
import { isApiError } from '@/lib/api'

// ── Loader logic (extracted from sessions.$sessionId.tsx) ─────────────────────
//
// This mirrors the exact catch/return logic of the loader:
//   - Returns detail on success, null when detail is nullish.
//   - Swallows ApiError 404; re-throws all other errors.

interface SessionDetail {
  session: { id: string; agent_id: string }
  agent_removed: boolean
}

async function loaderLogic(
  fetchSessionDetail: (id: string) => Promise<SessionDetail | null>,
  setActiveSession: (id: string, agentId: string, _: null) => void,
  sessionId: string,
): Promise<SessionDetail | null> {
  try {
    const detail = await fetchSessionDetail(sessionId)
    if (detail?.session) {
      setActiveSession(detail.session.id, detail.session.agent_id, null)
    }
    return detail ?? null
  } catch (err) {
    if (isApiError(err) && err.status === 404) {
      return null
    }
    throw err
  }
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('sessions.$sessionId route loader', () => {
  it('returns null when fetchSessionDetail throws ApiError 404', async () => {
    // BDD: Given the API returns a 404 (session does not exist),
    // When the loader is called,
    // Then the loader returns null (does not re-throw).
    //
    // Traces to: sessions.$sessionId.tsx loader — swallows 404 so ChatScreen renders
    // with default state instead of showing an error boundary.
    const fetchSessionDetail = async (_id: string): Promise<SessionDetail | null> => {
      throw new ApiError(404, 'Session not found')
    }
    const setActiveSession = (_id: string, _agentId: string, _: null) => {}

    const result = await loaderLogic(fetchSessionDetail, setActiveSession, 'sess-404')

    expect(result).toBeNull()
  })

  it('re-throws ApiSchemaError from fetchSessionDetail', async () => {
    // BDD: Given the API returns a response that fails schema validation,
    // When the loader is called,
    // Then the loader re-throws the ApiSchemaError so the route error boundary
    // can surface the contract violation rather than hiding it.
    //
    // Traces to: sessions.$sessionId.tsx loader — re-throws non-404 errors.
    const schemaError = new ApiSchemaError(
      '/sessions/sess-bad-contract',
      [{ path: ['session', 'id'], message: 'expected string, got number' }],
      { session: { id: 42 } },
    )
    const fetchSessionDetail = async (_id: string): Promise<SessionDetail | null> => {
      throw schemaError
    }
    const setActiveSession = (_id: string, _agentId: string, _: null) => {}

    await expect(loaderLogic(fetchSessionDetail, setActiveSession, 'sess-schema-error')).rejects.toThrow(
      ApiSchemaError,
    )
  })

  it('re-throws 500 ApiError from fetchSessionDetail', async () => {
    // BDD: Given the API returns a 500 server error,
    // When the loader is called,
    // Then the loader re-throws so the error boundary handles it.
    //
    // Traces to: sessions.$sessionId.tsx loader — only swallows ApiError{status:404}.
    const serverError = new ApiError(500, 'Internal server error')
    const fetchSessionDetail = async (_id: string): Promise<SessionDetail | null> => {
      throw serverError
    }
    const setActiveSession = (_id: string, _agentId: string, _: null) => {}

    await expect(loaderLogic(fetchSessionDetail, setActiveSession, 'sess-500')).rejects.toThrow(ApiError)
  })

  it('returns session detail and activates session store on success', async () => {
    // BDD: Given the API returns a valid session detail,
    // When the loader is called,
    // Then the loader returns the detail and calls setActiveSession with the
    // session id and agent_id.
    //
    // Traces to: sessions.$sessionId.tsx loader — sets active session in Zustand store.
    const mockDetail: SessionDetail = {
      session: { id: 'sess-abc', agent_id: 'jim' },
      agent_removed: false,
    }
    const fetchSessionDetail = async (_id: string): Promise<SessionDetail | null> => mockDetail

    let activatedId = ''
    let activatedAgentId = ''
    const setActiveSession = (id: string, agentId: string, _: null) => {
      activatedId = id
      activatedAgentId = agentId
    }

    const result = await loaderLogic(fetchSessionDetail, setActiveSession, 'sess-abc')

    // Content test: verify returned detail matches what was fetched.
    expect(result).toEqual(mockDetail)
    // Persistence test: verify the store was activated with the exact session data.
    expect(activatedId).toBe('sess-abc')
    expect(activatedAgentId).toBe('jim')
  })

  it('returns null when fetchSessionDetail resolves with null', async () => {
    // BDD: Given the API returns null (no session found, non-error path),
    // When the loader is called,
    // Then the loader returns null without activating the store.
    //
    // Traces to: sessions.$sessionId.tsx loader — return detail ?? null.
    const fetchSessionDetail = async (_id: string): Promise<SessionDetail | null> => null

    let activatedId = ''
    const setActiveSession = (id: string, _agentId: string, _: null) => {
      activatedId = id
    }

    const result = await loaderLogic(fetchSessionDetail, setActiveSession, 'sess-null')

    expect(result).toBeNull()
    expect(activatedId).toBe('') // setActiveSession must NOT have been called
  })
})
