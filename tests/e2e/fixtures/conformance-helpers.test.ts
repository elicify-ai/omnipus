// conformance-helpers.test.ts — vitest coverage for apiFetch's transport-reset
// retry boundaries and recordTransportReplay's sanitized, loud reporting path.
//
// This is the permanent regression the 2026-11-07 CHECK demanded (findings 1,
// 3 and 4 of ciflake-check2-report.md): the audit's scratch probes proved the
// boundaries and the reporting path, but probes outside repository CI are not
// a suite. Every expectation here traces to that CHECK-verified contract for
// commit d14beac9c — GET/PUT replay only, exactly 2 attempts, one 1,000ms
// delay; POST/PATCH/DELETE, HTTP error responses and non-reset failures never
// replay; repeated resets fail loudly; the record carries a whitelisted error
// CLASS token only (never raw error text — finding 4); a reporting failure is
// never swallowed (finding 1). Boundary values are the spec, not observed
// implementation output.
//
// SCOPE RULE (same as selectors.test.ts): only pure logic and process edges
// are unit-tested here. Faked edges: `page.request.fetch` (network), the CSRF
// cookie read, `page.waitForTimeout` (clock) and the Playwright TestInfo sink
// (runner boundary). apiFetch, recordTransportReplay, buildReplayRecord,
// classifyTransportReset and isTestInfoUnavailableError run for REAL. The
// full gateway/browser lifecycle and the reporter's JSON serialization of
// annotations/attachments stay covered by the Playwright suites and the
// runner source (playwright/lib/runner/index.js, fix2 report), not here.
//
// Deliberately NOT covered here (known gaps): a REAL Playwright TestInfo
// (needs a runner), the live gateway replay behaviour itself, and the JSON
// report upload path (CI-level).

// @vitest-environment node
import { afterEach, describe, expect, it, vi } from 'vitest'
import { test, type Page } from '@playwright/test'
import {
  apiFetch,
  BASE_URL,
  buildReplayRecord,
  classifyTransportReset,
  isTestInfoUnavailableError,
  recordTransportReplay,
  TEST_INFO_UNAVAILABLE_MESSAGE,
  type ReplayEvidenceSink,
} from './conformance-helpers'

// ── Fake process edges ───────────────────────────────────────────────────────

interface RecordedCall {
  method: string
  url: string
  csrfHeader?: string
}

type FetchResult = { ok: boolean; status: number; text: string }
type FetchBehavior = (callIndex: number, method: string, url: string) => FetchResult | Promise<FetchResult>

/**
 * A Page fake exposing exactly the surface apiFetch touches: waitForTimeout
 * (clock edge — records every awaited delay), context().cookies() (auth edge
 * — a seeded __Host-csrf like global-setup.ts leaves behind) and
 * request.fetch (network edge — driven by the per-test `behavior`).
 */
function makeFakePage(behavior: FetchBehavior): { page: Page; calls: RecordedCall[]; waits: number[] } {
  const calls: RecordedCall[] = []
  const waits: number[] = []
  const page = {
    waitForTimeout: async (ms: number) => {
      waits.push(ms)
    },
    context: () => ({
      cookies: async () => [{ name: '__Host-csrf', value: 'test-csrf-token' }],
    }),
    request: {
      fetch: async (url: string, opts: { method: string; headers: Record<string, string> }) => {
        calls.push({ method: opts.method, url, csrfHeader: opts.headers['X-Csrf-Token'] })
        const r = await behavior(calls.length, opts.method, url)
        return { ok: () => r.ok, status: () => r.status, text: async () => r.text }
      },
    },
  } as unknown as Page
  return { page, calls, waits }
}

const okJson = (body: unknown) => ({ ok: true, status: 200, text: JSON.stringify(body) })
const resetError = () => {
  throw new Error('socket hang up')
}

/** A TestInfo-like sink capturing annotations and attach calls — the same
 * Pick<TestInfo, 'annotations' | 'attach'> surface recordTransportReplay's
 * injectable parameter takes. attach is a vi.fn, also exposed as attachMock
 * with its mock surface, so call-order assertions can compare against the
 * stderr warn. */
function makeSink(): ReplayEvidenceSink & {
  attached: Array<{ name: string; opts: { body: string; contentType: string } }>
  attachMock: { mock: { invocationCallOrder: number[] } }
} {
  const attached: Array<{ name: string; opts: { body: string; contentType: string } }> = []
  const attachMock = vi.fn(async (name: string, opts: { body: string; contentType: string }) => {
    attached.push({ name, opts })
  })
  return { annotations: [], attached, attach: attachMock, attachMock }
}

/** Collects every evidence channel's text so redaction assertions can sweep
 * annotation + attachment + stderr together. */
function evidenceText(sink: ReturnType<typeof makeSink>, warns: string[]): string {
  return [JSON.stringify(sink.annotations), JSON.stringify(sink.attached), warns.join('\n')].join('\n')
}

// console.warn is the surviving record channel; every test that triggers a
// recording silences and captures it so test output stays clean.
let warnSpy: ReturnType<typeof vi.spyOn>
afterEach(() => {
  vi.restoreAllMocks()
})

/** The warn spy's captured lines, stringified for evidence-sweep assertions. */
const warnLines = (): string[] => warnSpy.mock.calls.map((c: unknown[]) => c.map(String).join(' '))

describe('apiFetch transport-reset retry boundaries', () => {
  it('replays a GET exactly once, after exactly one 1,000ms delay, and returns the recovered response', async () => {
    warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const { page, calls, waits } = makeFakePage((i) => {
      if (i === 1) return resetError()
      return okJson({ id: 'a1', recovered: true })
    })
    const res = await apiFetch<{ id: string; recovered: boolean }>(page, 'GET', '/api/v1/example')
    expect(calls, 'exactly two attempts for a replayed GET').toHaveLength(2)
    expect(waits, 'exactly one inter-attempt delay of the specified 1,000ms').toEqual([1_000])
    expect(res.ok).toBe(true)
    expect(res.status).toBe(200)
    expect(res.body).toEqual({ id: 'a1', recovered: true })
    expect(res.raw).toBe(JSON.stringify({ id: 'a1', recovered: true }))
    expect(calls[0].url).toBe(`${BASE_URL}/api/v1/example`)
    expect(calls[1].url).toBe(`${BASE_URL}/api/v1/example`)
    // The stderr record wired from apiFetch into the recorder: one sanitized
    // line naming method, path and the CLASS token of the input error (this
    // test feeds 'socket hang up', whose whitelisted token is
    // socket-hang-up; under vitest there is no running Playwright test, so
    // the annotation sink is correctly null).
    expect(warnSpy).toHaveBeenCalledTimes(1)
    expect(String(warnSpy.mock.calls[0][0])).toContain('GET /api/v1/example')
    expect(String(warnSpy.mock.calls[0][0])).toContain('class socket-hang-up')
  })

  it('replays a PUT exactly once, carrying the CSRF token on each attempt', async () => {
    warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const { page, calls, waits } = makeFakePage((i) => {
      if (i === 1) return resetError()
      return okJson({ warning: '' })
    })
    const res = await apiFetch(page, 'PUT', '/api/v1/agents/a1/tools', { revision: 'r1' })
    expect(calls, 'exactly two attempts for a replayed PUT').toHaveLength(2)
    expect(waits).toEqual([1_000])
    expect(res.ok).toBe(true)
    for (const call of calls) {
      expect(call.method).toBe('PUT')
      expect(call.csrfHeader, 'a non-GET attempt reads the seeded CSRF cookie').toBe('test-csrf-token')
    }
  })

  it.each(['POST', 'PATCH', 'DELETE'] as const)(
    'never replays %s even when the transport resets: exactly one attempt, the reset error propagates',
    async (method: 'POST' | 'PATCH' | 'DELETE') => {
      warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
      const { page, calls, waits } = makeFakePage(() => resetError())
      await expect(apiFetch(page, method, '/api/v1/example', {})).rejects.toThrow(/socket hang up/)
      expect(calls, `a reset ${method} must not be replayed — replaying a create can duplicate an entity`).toHaveLength(1)
      expect(waits, 'no retry delay is ever awaited without a replay').toEqual([])
      expect(warnSpy, 'no replay was decided on, so nothing is recorded').not.toHaveBeenCalled()
    },
  )

  it('does not replay an HTTP error response — a delivered status is not a transport reset', async () => {
    const { page, calls, waits } = makeFakePage(() => ({ ok: false, status: 500, text: JSON.stringify({ error: 'boom' }) }))
    const res = await apiFetch<{ error: string }>(page, 'GET', '/api/v1/example')
    expect(calls, 'the server answered, so there is nothing to replay').toHaveLength(1)
    expect(waits).toEqual([])
    expect(res.ok).toBe(false)
    expect(res.status).toBe(500)
    expect(res.body).toEqual({ error: 'boom' })
  })

  it('does not replay a non-reset transport failure and propagates its specific message', async () => {
    warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const { page, calls, waits } = makeFakePage(() => {
      throw new Error('fetch timed out')
    })
    await expect(apiFetch(page, 'GET', '/api/v1/example')).rejects.toThrow('fetch timed out')
    expect(calls).toHaveLength(1)
    expect(waits).toEqual([])
    expect(warnSpy).not.toHaveBeenCalled()
  })

  it('fails loudly after exactly two attempts when every attempt resets', async () => {
    warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const { page, calls, waits } = makeFakePage(() => resetError())
    await expect(apiFetch(page, 'GET', '/api/v1/example')).rejects.toThrow(/socket hang up/)
    expect(calls, 'two attempts are the whole budget — no third try').toHaveLength(2)
    expect(waits, 'exactly one inter-attempt delay, then the reset is rethrown').toEqual([1_000])
    expect(warnSpy, 'a replay that did NOT recover still leaves its record').toHaveBeenCalledTimes(1)
  })

  it('never waits before the first attempt', async () => {
    const { page, waits } = makeFakePage(() => okJson({ fine: true }))
    const res = await apiFetch(page, 'GET', '/api/v1/example')
    expect(res.ok).toBe(true)
    expect(waits, 'the delay belongs between attempts only').toEqual([])
  })
})

describe('recordTransportReplay reporting path', () => {
  const PUT_PATH = '/api/v1/agents/a1/tools'

  it('records one annotation and one sanitized attachment on the sink', async () => {
    warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const sink = makeSink()
    await recordTransportReplay('PUT', PUT_PATH, 'ECONNRESET', 1, sink)
    expect(sink.annotations).toHaveLength(1)
    expect(sink.annotations[0].type).toBe('transport-reset-replayed')
    expect(sink.annotations[0].description).toMatch(new RegExp(`PUT ${PUT_PATH}: attempt 1 .+class econnreset.+attempt 2 after 1000ms`))
    expect(sink.attached).toHaveLength(1)
    expect(sink.attached[0].name).toMatch(/^transport-reset-replay-put-\d+\.json$/)
    expect(sink.attached[0].opts.contentType).toBe('application/json')
    const recorded = JSON.parse(sink.attached[0].opts.body)
    // Exact record shape — safe metadata only, per the CHECK-verified record
    // contract with the raw error field replaced by its whitelisted class.
    expect(recorded).toEqual({
      kind: 'transport-reset-replayed',
      method: 'PUT',
      path: PUT_PATH,
      error_class: 'econnreset',
      failed_attempt: 1,
      retried_attempt: 2,
      delay_ms: 1_000,
    })
    expect(warnSpy).toHaveBeenCalledTimes(1)
  })

  it('never records raw error text — only the whitelisted class token (redaction regression, CHECK finding 4)', async () => {
    warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const sink = makeSink()
    // A synthetic error whose first 200 characters carry a sentinel that must
    // behave like a leaked header/cookie value: the old error.slice(0, 200)
    // record copied it through verbatim (CHECK's logging-negative probe).
    await recordTransportReplay('GET', '/api/v1/example', 'AUDIT-COOKIE-NOT-A-CREDENTIAL: sekrit-header-value; ECONNRESET midbody', 1, sink)
    const recorded = JSON.parse(sink.attached[0].opts.body)
    expect(recorded).toEqual({
      kind: 'transport-reset-replayed',
      method: 'GET',
      path: '/api/v1/example',
      error_class: 'econnreset',
      failed_attempt: 1,
      retried_attempt: 2,
      delay_ms: 1_000,
    })
    const everything = evidenceText(sink, warnLines())
    expect(everything).not.toContain('AUDIT-COOKIE-NOT-A-CREDENTIAL')
    expect(everything).not.toContain('sekrit-header-value')
    expect(everything).toContain('class econnreset')
  })

  it('keeps only the stderr record when there is no running test (null sink)', async () => {
    warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    // The exact worker-level-setup shape: no Playwright test result exists, so
    // the retry must still apply and the stderr line is the only record.
    await expect(recordTransportReplay('GET', '/api/v1/example', 'socket hang up', 1, null)).resolves.toBeUndefined()
    expect(warnSpy).toHaveBeenCalledTimes(1)
    expect(String(warnSpy.mock.calls[0][0])).toContain('[apiFetch] transport reset replayed')
  })

  it('propagates an annotation failure instead of swallowing it (CHECK finding 1)', async () => {
    warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const sink = makeSink()
    sink.annotations.push = () => {
      throw new Error('annotation store exploded')
    }
    await expect(recordTransportReplay('PUT', PUT_PATH, 'ECONNRESET', 1, sink)).rejects.toThrow(
      'annotation store exploded',
    )
    expect(warnSpy, 'the stderr record is written before the failing channel').toHaveBeenCalledTimes(1)
  })

  it('propagates an attachment failure instead of swallowing it (CHECK finding 1)', async () => {
    warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const sink = makeSink()
    sink.attach = async () => {
      throw new Error('attachment write failed')
    }
    await expect(recordTransportReplay('PUT', PUT_PATH, 'ECONNRESET', 1, sink)).rejects.toThrow(
      'attachment write failed',
    )
    expect(warnSpy, 'the stderr record survives the failing attachment').toHaveBeenCalledTimes(1)
  })

  it('writes the stderr record BEFORE the attachment so a lost report upload still leaves the log line', async () => {
    warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const sink = makeSink()
    await recordTransportReplay('GET', '/api/v1/example', 'ECONNRESET', 1, sink)
    expect(warnSpy.mock.invocationCallOrder[0]).toBeLessThan(sink.attachMock.mock.invocationCallOrder[0])
  })
})

describe('buildReplayRecord', () => {
  it('builds the attachment name with the lowercase method', () => {
    const record = buildReplayRecord('DELETE', '/x', 'ECONNRESET', 1)
    expect(record.attachmentName).toMatch(/^transport-reset-replay-delete-\d+\.json$/)
  })

  it('classifies the reset class into the description and body identically', () => {
    const record = buildReplayRecord('PUT', '/p', 'socket hang up', 1)
    expect(record.description).toContain('class socket-hang-up')
    expect(JSON.parse(record.attachmentBody).error_class).toBe('socket-hang-up')
  })
})

describe('classifyTransportReset', () => {
  it.each([
    ['socket hang up', 'socket-hang-up'],
    ['ECONNRESET', 'econnreset'],
    ['econnreset (lowercase variant)', 'econnreset'],
    ['ECONNABORTED while writing', 'econnaborted'],
    ['EPIPE', 'epipe'],
    ['fetch timed out', 'unclassified-reset'],
    ['HTTP 500 Internal Server Error', 'unclassified-reset'],
  ])('maps %s to the whitelisted token %s', (errorText: string, expected: string) => {
    expect(classifyTransportReset(errorText)).toBe(expected)
  })
})

describe('isTestInfoUnavailableError', () => {
  it('matches ONLY the pinned Playwright unavailable-test error', () => {
    expect(isTestInfoUnavailableError(new Error(TEST_INFO_UNAVAILABLE_MESSAGE))).toBe(true)
  })

  it.each([
    ['a different Error', new Error('reporter exploded')],
    ['a plain string with the same text', TEST_INFO_UNAVAILABLE_MESSAGE],
    ['null', null],
    ['undefined', undefined],
  ])('rejects %s', (_label: string, candidate: unknown) => {
    expect(isTestInfoUnavailableError(candidate)).toBe(false)
  })
})

// The REAL default path: recordTransportReplay with NO injected sink resolves
// its sink through currentTestInfo() -> test.info(). The runner boundary
// (test.info) is the one faked edge; currentTestInfo's narrowed catch and the
// recorder's default-argument lookup run for REAL. This is what kills a
// blanket "return null on any test.info() error" edit (CHECK 3 finding F2),
// which the injected-sink and standalone-discriminator cases above cannot see.
describe('recordTransportReplay default test.info() lookup (no injected sink)', () => {
  it('rejects with the unexpected test.info() error instead of suppressing it (CHECK 3 F2)', async () => {
    warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.spyOn(test, 'info').mockImplementation(() => {
      throw new Error('reporter exploded')
    })
    await expect(recordTransportReplay('GET', '/api/v1/example', 'ECONNRESET', 1)).rejects.toThrow(
      'reporter exploded',
    )
  })

  it('rejects with a thrown non-Error value instead of suppressing it (CHECK 3 F2)', async () => {
    warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.spyOn(test, 'info').mockImplementation(() => {
      throw TEST_INFO_UNAVAILABLE_MESSAGE // same text, but a string, not an Error
    })
    await expect(recordTransportReplay('GET', '/api/v1/example', 'ECONNRESET', 1)).rejects.toBe(
      TEST_INFO_UNAVAILABLE_MESSAGE,
    )
  })

  it('falls back to the stderr-only record for the exact unavailable-test error', async () => {
    warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.spyOn(test, 'info').mockImplementation(() => {
      throw new Error(TEST_INFO_UNAVAILABLE_MESSAGE)
    })
    await expect(recordTransportReplay('GET', '/api/v1/example', 'ECONNRESET', 1)).resolves.toBeUndefined()
    expect(warnSpy).toHaveBeenCalledTimes(1)
    expect(String(warnSpy.mock.calls[0][0])).toContain('[apiFetch] transport reset replayed')
  })

  it('writes to the sink test.info() returns when a test is running', async () => {
    warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const sink = makeSink()
    vi.spyOn(test, 'info').mockImplementation(() => sink as unknown as ReturnType<typeof test.info>)
    await recordTransportReplay('PUT', '/api/v1/example', 'ECONNRESET', 1)
    expect(sink.annotations).toHaveLength(1)
    expect(sink.attached).toHaveLength(1)
  })
})
