// knowledgeRecordConflict.test.ts — the 409 → KnowledgeRecordConflictError
// translation `writeVaultRecord` performs (ADR-083 EMB-086, CW-7), exercised
// from a REAL API error.
//
// WHY A SEPARATE FILE, AND WHY IT MOCKS `fetch` RATHER THAN `writeVaultRecord`.
// The existing coverage for this behaviour lives in RecordFieldEditor's own
// suite, which mocks `writeVaultRecord` outright and rejects with a
// hand-constructed `KnowledgeRecordConflictError`. That proves the editor
// renders a conflict it is HANDED; it cannot prove anything about the code
// that PRODUCES one, because that code never runs. The demonstration: replace
// `knowledgeRecordConflictFromApiError`'s whole body with `return err` and
// that suite still passes. In production the same mutation turns a stale
// write into a generic "Could not save this field" instead of the conflict
// banner with a Retry — a silent downgrade of the one control that stops an
// edit nobody saw from being overwritten.
//
// So this file mocks exactly one thing: the network. `writeVaultRecord`,
// `request`, `ApiError.fromResponse`, the raw-body re-parse and the
// `KnowledgeConflictError` Zod envelope are all the real implementations. The
// unit under test is the translation itself.
//
// Mocking `fetch` also restores the OTHER thing that mock was hiding: the
// client-side `RecordWriteRequestSchema.parse` of every outgoing write. With
// `writeVaultRecord` stubbed, a malformed request body reaches no validator
// at all; here it reaches the real one, and the last test below proves the
// request never leaves the browser.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  writeVaultRecord,
  isKnowledgeRecordConflict,
  isApiError,
  KnowledgeRecordConflictError,
  ApiError,
} from './api'
import type { components } from './api/generated/openapi-types'

type RecordWriteRequest = components['schemas']['RecordWriteRequest']

// A VALID update body: `id` present means update, so the contract requires a
// `version_token`, and the token must match the minted shape
// (`^v1:(absent|[0-9a-f]{32})$`) or the client-side schema rejects it before
// any request is made.
const STALE_TOKEN = 'v1:0123456789abcdef0123456789abcdef'
const FRESH_TOKEN = 'v1:fedcba9876543210fedcba9876543210'

function updateBody(overrides: Partial<RecordWriteRequest> = {}): RecordWriteRequest {
  return {
    type: 'company',
    id: 'CO-0142',
    version_token: STALE_TOKEN,
    properties: [{ property: 'status', values: [{ type: 'text', text: 'Active' }] }],
    ...overrides,
  }
}

/** The gateway's real 409 envelope for a refused compare-and-swap. */
function conflictBody() {
  return {
    error: 'the file changed since it was read',
    code: 'knowledge_version_conflict',
    path: 'CRM/Companies/Acme Ltd.md',
    expected_version: STALE_TOKEN,
    actual_version: FRESH_TOKEN,
  }
}

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

let fetchMock: ReturnType<typeof vi.fn>

beforeEach(() => {
  // `request()` refuses any state-changing call with no CSRF cookie before it
  // ever reaches fetch, so the POST under test needs one. The plain-HTTP name
  // is the one jsdom will actually store — a `__Host-` cookie requires Secure.
  document.cookie = 'csrf=test-csrf-token'
  fetchMock = vi.fn()
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  vi.unstubAllGlobals()
  document.cookie = 'csrf=; expires=Thu, 01 Jan 1970 00:00:00 GMT'
})

describe('writeVaultRecord translates a real 409 into KnowledgeRecordConflictError (EMB-086)', () => {
  it('produces the typed conflict — with the FRESH token a retry must send — from the response alone', async () => {
    fetchMock.mockResolvedValue(jsonResponse(409, conflictBody()))

    const err = await writeVaultRecord('ws-1', updateBody()).then(
      () => {
        throw new Error('writeVaultRecord resolved; it must reject on a 409')
      },
      (e: unknown) => e,
    )

    // THE assertion the `return err` mutation kills: a bare ApiError(409)
    // fails this, and the editor falls back to its generic save-failed text.
    expect(isKnowledgeRecordConflict(err)).toBe(true)
    const conflict = err as KnowledgeRecordConflictError

    expect(conflict.path).toBe('CRM/Companies/Acme Ltd.md')
    // `actualVersion` is the whole point of the type: the token a Retry must
    // send. Asserting it is NOT the stale one the refused attempt sent is what
    // separates a real translation from a type that merely exists.
    expect(conflict.actualVersion).toBe(FRESH_TOKEN)
    expect(conflict.expectedVersion).toBe(STALE_TOKEN)
    expect(conflict.actualVersion).not.toBe(conflict.expectedVersion)

    // Still a 409 ApiError, so the class doc's compatibility claim holds.
    expect(isApiError(err)).toBe(true)
    expect(conflict.status).toBe(409)
    expect(conflict.code).toBe('knowledge_version_conflict')
  })

  it('leaves a 409 whose body is NOT the conflict envelope as a plain ApiError', async () => {
    // A proxy error page, or any other 409 this client did not mint. The
    // paired negative: without it, "every 409 is a conflict" would pass the
    // test above, and the reader would be told a colleague edited the file
    // when the real answer is that something upstream refused the request.
    fetchMock.mockResolvedValue(
      new Response('<html><body>409 Conflict</body></html>', {
        status: 409,
        headers: { 'content-type': 'text/html' },
      }),
    )

    const err = await writeVaultRecord('ws-1', updateBody()).then(
      () => {
        throw new Error('writeVaultRecord resolved; it must reject on a 409')
      },
      (e: unknown) => e,
    )

    expect(isApiError(err)).toBe(true)
    expect((err as ApiError).status).toBe(409)
    expect(isKnowledgeRecordConflict(err)).toBe(false)
  })

  it('leaves a 409 carrying a DIFFERENT typed error code as a plain ApiError', async () => {
    // Same shape, wrong `code` — the envelope's discriminator is a literal, so
    // this must not be read as a version conflict either.
    fetchMock.mockResolvedValue(
      jsonResponse(409, { ...conflictBody(), code: 'some_other_conflict' }),
    )

    const err = await writeVaultRecord('ws-1', updateBody()).then(
      () => {
        throw new Error('writeVaultRecord resolved; it must reject on a 409')
      },
      (e: unknown) => e,
    )

    expect(isKnowledgeRecordConflict(err)).toBe(false)
    expect(isApiError(err)).toBe(true)
  })

  it('does not mistake a 400 carrying a conflict-shaped body for a conflict', async () => {
    // Guards the status half of the condition: only a 409 is a conflict.
    fetchMock.mockResolvedValue(jsonResponse(400, conflictBody()))

    const err = await writeVaultRecord('ws-1', updateBody()).then(
      () => {
        throw new Error('writeVaultRecord resolved; it must reject on a 400')
      },
      (e: unknown) => e,
    )

    expect(isKnowledgeRecordConflict(err)).toBe(false)
    expect((err as ApiError).status).toBe(400)
  })
})

describe('writeVaultRecord validates the OUTGOING body client-side before any request', () => {
  it('refuses a malformed version_token without calling fetch', async () => {
    await expect(
      writeVaultRecord('ws-1', updateBody({ version_token: 'not-a-minted-token' })),
    ).rejects.toThrow()

    // The point: rejected at the client edge, so a request the server would
    // have 400'd never leaves the browser.
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('refuses a write with no properties without calling fetch', async () => {
    await expect(writeVaultRecord('ws-1', updateBody({ properties: [] }))).rejects.toThrow()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('sends a valid body through to the network as JSON on the records path', async () => {
    // Paired positive for the two refusals above — without it, a validator
    // that rejected EVERYTHING would pass them both.
    fetchMock.mockResolvedValue(jsonResponse(409, conflictBody()))

    await writeVaultRecord('ws-1', updateBody()).catch(() => undefined)

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/api/v1/library/ws-1/knowledge/records')
    expect(init.method).toBe('POST')
    expect(JSON.parse(String(init.body))).toMatchObject({
      type: 'company',
      id: 'CO-0142',
      version_token: STALE_TOKEN,
    })
  })
})
