// packages.api — spec order 19 (issue #1967 B1). The transport test.
//
// Oracles: spec "Counting requests" (one package request per deliberate
// action; zero on client csrf_missing; the recovery read GET /api/v1/state is
// counted separately), "Apply outcome classes" (the classifier table),
// "Integration Boundaries → Gateway package routes" (raw upload bytes, blob +
// Content-Disposition name on export, apply body {mode, workspace_id},
// DELETE cancel), the no-automatic-resend rule across ALL THREE replay
// layers — transport switches AND the production QueryClient mutation
// defaults (retry:false per call; CHECK F04) — and "Local execution" (the
// real request()/requestOnce() run over a mocked global fetch — the
// transport itself is never mocked).
//
// Successful fixtures are validated against the REAL generated schemas
// (Schemas.PackageImportPreview / PackageImportResult, CHECK F02); wrong
// 200-bodies stay in dedicated negative rows.
//
// RED: every packages.ts call is a throwing stub, so each behaviour row fails
// with "not implemented"; the classifier rows fail on their throwing stubs
// too. The positive control rows (generic-caller CSRF re-send, planted-toast,
// and the production mutation retryer) pin today's behaviour and are expected
// to pass in RED — they prove the instruments can see a re-send, a toast and
// a mutation retry, so the "never resend" rows cannot pass vacuously.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { MutationObserver, QueryClient } from '@tanstack/react-query'
import { ApiError } from '@/lib/api-error'
import { ApiSchemaError, request } from './http'
import { shouldRetryMutation, mutationRetryDelay } from '@/lib/queryClient'
import {
  applyPackageImport,
  cancelPackageImport,
  classifyApplyOutcome,
  classifyUploadOutcome,
  exportPackage,
  PackageImportPreviewSchema,
  PackageImportResultSchema,
  uploadPackageImport,
  type PackageImportPreview,
  type PackageImportResult,
} from './packages'
import { useUiStore } from '@/store/ui'

beforeEach(() => {
  document.cookie = 'csrf=packages-api-test; path=/'
  // A fresh toast spy per test: http.ts reads the store through
  // getState().addToast, so replacing the action observes the raise.
  useUiStore.setState({ addToast: addToastSpy as unknown as () => void })
  // The transport's planted-toast dedup is module state: advance the clock
  // past the 5 s window between tests so no row depends on order (W02).
  fakeNow += 60_000
  dateNowSpy = vi.spyOn(Date, 'now').mockImplementation(() => fakeNow)
})

afterEach(() => {
  dateNowSpy.mockRestore()
  vi.restoreAllMocks()
  document.cookie = 'csrf=; max-age=0; path=/'
  document.cookie = '__Host-csrf=; max-age=0; path=/'
})

const addToastSpy = vi.fn()
let dateNowSpy = { mockRestore: () => {} } as ReturnType<typeof vi.spyOn>
let fakeNow = 1_700_000_000_000

// A production-defaults QueryClient: the REAL exported predicates, not a
// hand-copied replica (CHECK F04). Package mutations under test are built
// through this client's defaults, with retry:false on the package call rows.
function makeProductionQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      mutations: { retry: shouldRetryMutation, retryDelay: mutationRetryDelay },
    },
  })
}

function mockFetch(
  impl: (url: string, init?: RequestInit) => Response | Promise<Response>,
): ReturnType<typeof vi.spyOn> {
  return vi.spyOn(globalThis, 'fetch').mockImplementation(impl as never)
}

function jsonResponse(status: number, body: unknown, headers?: Record<string, string>): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json', ...headers },
  })
}

function errorBody(status: number, body: Record<string, string>): Response {
  return jsonResponse(status, body)
}

// A bundle-file stand-in with recognizable bytes.
function bundleFile(size = 8): File {
  const bytes = Uint8Array.from({ length: size }, (_, i) => (i * 7 + 0x4f) % 256)
  return new File([bytes], 'bundle.omnpkg', { type: 'application/octet-stream' })
}

async function fileBytes(body: unknown): Promise<Uint8Array> {
  if (body instanceof Blob) return new Uint8Array(await body.arrayBuffer())
  if (body instanceof ArrayBuffer) return new Uint8Array(body)
  if (body instanceof Uint8Array) return body
  throw new Error(`upload body is not binary: ${String(body)}`)
}

// Package requests counted per URL and method (spec "Counting requests");
// the recovery read GET /api/v1/state is counted separately. The spy MUST be
// installed before counting — a count of zero over an uninstalled spy is
// meaningless (CHECK F05).
function packageCalls(method: string): Array<Array<unknown>> {
  return (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.filter(([url, init]) => {
    const m = String((init as RequestInit | undefined)?.method ?? 'GET').toUpperCase()
    return String(url).includes('/api/v1/packages/') && m === method
  })
}

function totalPackageCalls(): Array<Array<unknown>> {
  return (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.filter(([url]) =>
    String(url).includes('/api/v1/packages/'),
  )
}

function stateReads(): Array<Array<unknown>> {
  return (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.filter(
    ([url, init]) =>
      String(url).endsWith('/api/v1/state') &&
      String((init as RequestInit | undefined)?.method ?? 'GET').toUpperCase() === 'GET',
  )
}

// ── Fixtures, validated against the real generated schemas (CHECK F02) ──────

function validPreview(): PackageImportPreview {
  const preview: PackageImportPreview = {
    staging_id: 'S1',
    expires_at: new Date(Date.now() + 30 * 60_000).toISOString(),
    manifest: {
      format: 'omnpkg',
      format_version: 1,
      scope: 'custom',
      elements: [{ type: 'agent', schema: 1, source_id: 'a-writer', name: 'Writer', files: ['agent.json', 'SOUL.md'] }],
      counts: {},
      files: {},
      source_build: 'v0.1.1',
      created_at: new Date(Date.now() - 60_000).toISOString(),
      includes_sessions: false,
      needs_secrets: [],
    },
    will_add: [
      {
        type: 'agent',
        source_id: 'a-writer',
        source_name: 'Writer',
        proposed_name: 'Writer',
        renamed: false,
      },
    ],
    skipped_elements: [],
    warnings: [],
    needs_secrets: [],
  }
  // A "valid" fixture that fails its own schema is a harness bug, not RED.
  PackageImportPreviewSchema.parse(preview)
  return preview
}

function validResult(activation: 'active' | 'pending', workspaceId = 'ws-1'): PackageImportResult {
  const result: PackageImportResult = {
    mode: 'new',
    created: [
      {
        type: 'agent',
        source_id: 'a-writer',
        new_id: 'a-new',
        name: 'Writer (imported)',
        workspace_id: workspaceId,
      },
    ],
    warnings: [],
    needs_secrets: [],
    activation,
    ...(activation === 'pending' ? { activation_message: 'The agent starts on its next heartbeat.' } : {}),
  }
  PackageImportResultSchema.parse(result)
  return result
}

// ── Per-call one-request rows (Counting requests) ────────────────────────────
//
// For each of apply, upload, cancel and export: a server CSRF 403, a planted-
// cookie 403, a network failure at dispatch and an uncoded 5xx each produce
// EXACTLY one package request and never an automatic second (and no recovery
// read — the calls run with the replay switches off).

const CSRF_BODY = { error: 'csrf token mismatch' }
const PLANTED_BODY = {
  code: 'planted_cookie_cleared',
  error: 'Omnipus cleared cookies set by a preview — please retry',
}

async function expectOneRequestOnly(action: () => Promise<unknown>, method: string, expected: object) {
  await expect(action).rejects.toMatchObject(expected)
  expect(packageCalls(method)).toHaveLength(1)
  expect(totalPackageCalls()).toHaveLength(1)
  expect(stateReads()).toHaveLength(0)
}

describe('package calls never resend (real transport over a mocked fetch)', () => {
  describe('apply POST /packages/import/{id}/apply', () => {
    it('a server CSRF 403 sends exactly one apply request', async () => {
      mockFetch(() => errorBody(403, CSRF_BODY))
      await expectOneRequestOnly(() => applyPackageImport('S', { mode: 'new' }), 'POST', { status: 403 })
    })

    it('a planted_cookie_cleared 403 sends one request and raises no toast', async () => {
      mockFetch(() => errorBody(403, PLANTED_BODY))
      await expectOneRequestOnly(
        () => applyPackageImport('S', { mode: 'new' }),
        'POST',
        { status: 403, code: 'planted_cookie_cleared' },
      )
      expect(addToastSpy, 'the package calls own their recovery — no generic toast').not.toHaveBeenCalled()
    })

    it('a network failure at dispatch sends exactly one apply request', async () => {
      mockFetch(() => {
        throw new TypeError('network down')
      })
      await expectOneRequestOnly(() => applyPackageImport('S', { mode: 'new' }), 'POST', { status: 0 })
    })

    it('an uncoded 502 sends exactly one apply request', async () => {
      mockFetch(() => new Response('<html>bad gateway</html>', { status: 502 }))
      await expectOneRequestOnly(() => applyPackageImport('S', { mode: 'new' }), 'POST', { status: 502 })
    })
  })

  describe('upload POST /packages/import', () => {
    it('a server CSRF 403 sends exactly one upload request', async () => {
      mockFetch(() => errorBody(403, CSRF_BODY))
      await expectOneRequestOnly(() => uploadPackageImport(bundleFile()), 'POST', { status: 403 })
    })

    it('a planted_cookie_cleared 403 sends one request and raises no toast', async () => {
      mockFetch(() => errorBody(403, PLANTED_BODY))
      await expectOneRequestOnly(() => uploadPackageImport(bundleFile()), 'POST', {
        status: 403,
        code: 'planted_cookie_cleared',
      })
      expect(addToastSpy).not.toHaveBeenCalled()
    })

    it('a network failure at dispatch sends exactly one upload request', async () => {
      mockFetch(() => {
        throw new TypeError('network down')
      })
      await expectOneRequestOnly(() => uploadPackageImport(bundleFile()), 'POST', { status: 0 })
    })

    it('an uncoded 503 sends exactly one upload request', async () => {
      mockFetch(() => new Response('service unavailable', { status: 503 }))
      await expectOneRequestOnly(() => uploadPackageImport(bundleFile()), 'POST', { status: 503 })
    })
  })

  describe('cancel DELETE /packages/import/{id}', () => {
    it('a server CSRF 403 sends exactly one cancel request', async () => {
      mockFetch(() => errorBody(403, CSRF_BODY))
      await expectOneRequestOnly(() => cancelPackageImport('S'), 'DELETE', { status: 403 })
    })

    it('a planted_cookie_cleared 403 sends one request and raises no toast', async () => {
      mockFetch(() => errorBody(403, PLANTED_BODY))
      await expectOneRequestOnly(() => cancelPackageImport('S'), 'DELETE', {
        status: 403,
        code: 'planted_cookie_cleared',
      })
      expect(addToastSpy).not.toHaveBeenCalled()
    })

    it('a network failure at dispatch sends exactly one cancel request', async () => {
      mockFetch(() => {
        throw new TypeError('network down')
      })
      await expectOneRequestOnly(() => cancelPackageImport('S'), 'DELETE', { status: 0 })
    })

    it('an uncoded 504 sends exactly one cancel request', async () => {
      mockFetch(() => new Response('gateway timeout', { status: 504 }))
      await expectOneRequestOnly(() => cancelPackageImport('S'), 'DELETE', { status: 504 })
    })
  })

  describe('export POST /packages/export', () => {
    it('a server CSRF 403 sends exactly one export request', async () => {
      mockFetch(() => errorBody(403, CSRF_BODY))
      await expectOneRequestOnly(() => exportPackage(['a-1']), 'POST', { status: 403 })
    })

    it('a planted_cookie_cleared 403 sends one request and raises no toast', async () => {
      mockFetch(() => errorBody(403, PLANTED_BODY))
      await expectOneRequestOnly(() => exportPackage(['a-1']), 'POST', {
        status: 403,
        code: 'planted_cookie_cleared',
      })
      expect(addToastSpy).not.toHaveBeenCalled()
    })

    it('a network failure at dispatch sends exactly one export request', async () => {
      mockFetch(() => {
        throw new TypeError('network down')
      })
      await expectOneRequestOnly(() => exportPackage(['a-1']), 'POST', { status: 0 })
    })

    it('an uncoded 500 sends exactly one export request', async () => {
      mockFetch(() => new Response('server error', { status: 500 }))
      await expectOneRequestOnly(() => exportPackage(['a-1']), 'POST', { status: 500 })
    })
  })

  it('a missing CSRF cookie refuses every package call before any request', async () => {
    document.cookie = 'csrf=; max-age=0; path=/'
    document.cookie = '__Host-csrf=; max-age=0; path=/'
    // The spy is installed BEFORE the calls: a zero-count is only evidence
    // over an installed instrument (CHECK F05).
    mockFetch(() => {
      throw new Error('no request may reach fetch on client csrf_missing')
    })
    await expect(applyPackageImport('S', { mode: 'new' })).rejects.toMatchObject({ code: 'csrf_missing' })
    await expect(uploadPackageImport(bundleFile())).rejects.toMatchObject({ code: 'csrf_missing' })
    await expect(cancelPackageImport('S')).rejects.toMatchObject({ code: 'csrf_missing' })
    await expect(exportPackage(['a-1'])).rejects.toMatchObject({ code: 'csrf_missing' })
    expect(totalPackageCalls(), 'zero package requests (the real cookie gate)').toHaveLength(0)
    expect(stateReads()).toHaveLength(0)
  })

  it('after a csrf_missing refusal the next deliberate action sends the first request', async () => {
    document.cookie = 'csrf=; max-age=0; path=/'
    // Installed first, so the zero-count assertion observes the real gate.
    const fetchSpy = mockFetch(
      () =>
        jsonResponse(200, {
          mode: 'new',
          created: [{ type: 'agent', source_id: 'a-writer', new_id: 'a-new', name: 'Writer (imported)', workspace_id: 'ws-1' }],
          warnings: [],
          needs_secrets: [],
          activation: 'active',
        }),
    )
    await expect(applyPackageImport('S', { mode: 'new' })).rejects.toMatchObject({ code: 'csrf_missing' })
    expect(totalPackageCalls()).toHaveLength(0)
    // Recovery: the cookie exists again; the person's next click sends the FIRST apply.
    document.cookie = 'csrf=packages-api-test; path=/'
    await expect(applyPackageImport('S', { mode: 'new' })).resolves.toMatchObject({
      created: [{ new_id: 'a-new' }],
      activation: 'active',
    })
    const sent = fetchSpy.mock.calls.filter(([, init]: unknown[]) => String((init as RequestInit | undefined)?.method).toUpperCase() === 'POST')
    expect(sent, 'this click sends the first apply, once').toHaveLength(1)
  })
})

// ── The production mutation path never resends (CHECK F04: the third
//    replay layer). Each package mutation is built through the production
//    QueryClient defaults with retry:false per call; the control row proves
//    the SAME client's retryer is live by omitting the override.

describe('package mutations never retry through the production query defaults', () => {
  function runMutation(
    client: QueryClient,
    mutationFn: () => Promise<unknown>,
    opts?: { retry?: boolean },
  ): Promise<unknown> {
    return new Promise((resolve, reject) => {
      const observer = new MutationObserver(client, {
        mutationFn,
        ...(opts?.retry === false ? { retry: false } : {}),
        onError: (err) => reject(err),
        onSuccess: (data) => resolve(data),
      })
      observer.mutate()
    })
  }

  const networkDown = () => {
    throw new TypeError('network down')
  }

  it.each([
    ['apply', () => applyPackageImport('S', { mode: 'new' })],
    ['upload', () => uploadPackageImport(bundleFile())],
    ['cancel', () => cancelPackageImport('S')],
    ['export', () => exportPackage(['a-1'])],
  ])('%s with retry:false dispatches exactly once through the production defaults', async (_name, fn) => {
    mockFetch(networkDown)
    await expect(runMutation(makeProductionQueryClient(), fn as () => Promise<unknown>, { retry: false })).rejects.toMatchObject({ status: 0 })
    expect(totalPackageCalls(), 'the mutation layer adds no second dispatch').toHaveLength(1)
  })

  it('control: the production default retryer IS live for a caller without retry:false', async () => {
    vi.useFakeTimers()
    try {
      mockFetch(networkDown)
      const client = makeProductionQueryClient()
      const pending = runMutation(client, () => applyPackageImport('S', { mode: 'new' }))
      pending.catch(() => {})
      // Drain the production backoff (1s + 2s + 4s) under controlled time.
      await vi.advanceTimersByTimeAsync(9_000)
      await expect(pending).rejects.toMatchObject({ status: 0 })
      expect(totalPackageCalls(), 'initial + three production retries').toHaveLength(4)
    } finally {
      vi.useRealTimers()
    }
  })
})

// ── Wire shapes (Integration Boundaries → Gateway package routes) ────────────

describe('package call wire shapes', () => {
  it('upload sends the raw bundle bytes, not JSON, and parses the preview against the generated schema', async () => {
    const file = bundleFile(8)
    const preview = validPreview()
    mockFetch(() => jsonResponse(200, preview))
    const parsed = await uploadPackageImport(file)
    expect(parsed.staging_id).toBe('S1')
    expect(parsed.will_add?.[0]?.proposed_name).toBe('Writer')
    expect(parsed.will_add?.[0]?.renamed).toBe(false)

    const [url, init] = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(String(url).endsWith('/api/v1/packages/import')).toBe(true)
    expect((init as RequestInit).method).toBe('POST')
    const sent = await fileBytes((init as RequestInit).body)
    const original = new Uint8Array(await file.arrayBuffer())
    expect(Array.from(sent)).toEqual(Array.from(original))
    const asText = new TextDecoder().decode(sent)
    expect(() => JSON.parse(asText), 'the upload body is binary, not a JSON document').toThrow()
  })

  it('export downloads the bundle with the Content-Disposition name and the blob', async () => {
    const bundleBytes = Uint8Array.from([0x4f, 0x4d, 0x4e, 0x50, 0x4b, 0x47, 0x01, 0x02])
    mockFetch(
      () =>
        new Response(bundleBytes, {
          status: 200,
          headers: {
            'Content-Type': 'application/octet-stream',
            'Content-Disposition': 'attachment; filename="Writer.omnpkg"',
            'Content-Length': String(bundleBytes.length),
          },
        }),
    )
    const download = await exportPackage(['a-1'])
    expect(download.fileName).toBe('Writer.omnpkg')
    expect(Array.from(new Uint8Array(await download.blob.arrayBuffer()))).toEqual(Array.from(bundleBytes))

    const [url, init] = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(String(url).endsWith('/api/v1/packages/export')).toBe(true)
    expect((init as RequestInit).method).toBe('POST')
    expect(JSON.parse(String((init as RequestInit).body))).toEqual({ agent_ids: ['a-1'] })
  })

  it('apply sends the exact {mode, workspace_id} body and parses the result against the generated schema', async () => {
    mockFetch(() => jsonResponse(200, validResult('active', 'ws-1')))
    const result = await applyPackageImport('S', { mode: 'new', workspace_id: 'ws-1' })
    expect(result.created?.[0]?.new_id).toBe('a-new')
    expect(result.activation).toBe('active')

    const [url, init] = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(String(url).endsWith('/api/v1/packages/import/S/apply')).toBe(true)
    expect((init as RequestInit).method).toBe('POST')
    expect(JSON.parse(String((init as RequestInit).body))).toEqual({ mode: 'new', workspace_id: 'ws-1' })
  })

  it('cancel sends DELETE to the staging URL and resolves on 204', async () => {
    mockFetch(() => new Response(null, { status: 204 }))
    await expect(cancelPackageImport('S')).resolves.toBeUndefined()
    const [url, init] = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    expect(String(url).endsWith('/api/v1/packages/import/S')).toBe(true)
    expect((init as RequestInit).method).toBe('DELETE')
    expect(totalPackageCalls()).toHaveLength(1)
  })

  it('a 200 that fails schema parsing rejects, sends one request and classifies as unknown', async () => {
    mockFetch(() => jsonResponse(200, { unexpected: true }))
    let caught: unknown
    await applyPackageImport('S', { mode: 'new' }).catch((e: unknown) => {
      caught = e
    })
    expect(packageCalls('POST'), 'never resent').toHaveLength(1)
    expect(classifyApplyOutcome(caught)).toBe('unknown')
  })
})

// ── The outcome classifiers (Apply outcome classes / D4) ─────────────────────

describe('classifyApplyOutcome — every answer sorts into exactly one class', () => {
  it('maps every known refusal to known_refusal', () => {
    const known: Array<[number, string | undefined, string | undefined]> = [
      [403, 'csrf_missing', undefined], // client csrf_missing (the gate's own error)
      [400, undefined, undefined],
      [403, undefined, 'csrf token mismatch'], // CSRF failure body
      [403, 'planted_cookie_cleared', undefined],
      [404, 'staging_not_found', undefined],
      [404, 'workspace_not_found', undefined],
      [409, 'import_in_progress', undefined],
      [409, 'name_suffix_exhausted', undefined],
      [409, 'default_workspace_missing', undefined],
      [413, undefined, undefined],
      [422, 'agent_file_unreadable', undefined],
      [500, 'import_rolled_back', undefined],
    ]
    for (const [status, code, body] of known) {
      const err = new ApiError(status, 'server said no', { code, body })
      expect(classifyApplyOutcome(err), `${status} ${code ?? ''}`).toBe('known_refusal')
    }
  })

  it('maps import_rollback_incomplete to cleanup_pending', () => {
    expect(classifyApplyOutcome(new ApiError(500, 'incomplete', { code: 'import_rollback_incomplete' }))).toBe(
      'cleanup_pending',
    )
  })

  it('maps everything unlisted to unknown', () => {
    const unknowns: unknown[] = [
      new ApiError(500, 'boom'), // uncoded 5xx
      new ApiError(502, 'proxy'),
      new ApiError(503, 'proxy'),
      new ApiError(504, 'proxy'),
      new ApiError(0, 'Network unavailable. Check your connection.'), // network failure
      new ApiError(429, 'rate limited'), // any other status
      new ApiError(401, 'unauthorized'),
      new ApiSchemaError('POST /api/v1/packages/import/S/apply', [{ path: [], message: 'bad' }], {}),
    ]
    for (const err of unknowns) {
      expect(classifyApplyOutcome(err)).toBe('unknown')
    }
  })
})

describe('classifyUploadOutcome — the same sort for an upload that never confirmed', () => {
  it('maps coded upload refusals to known_refusal', () => {
    const known: Array<[number, string | undefined]> = [
      [403, 'csrf_missing'],
      [400, undefined],
      [403, 'planted_cookie_cleared'],
      [413, undefined],
      [422, 'not_a_package'],
      [409, 'too_many_staged_imports'],
    ]
    for (const [status, code] of known) {
      expect(classifyUploadOutcome(new ApiError(status, 'no', { code })), `${status} ${code ?? ''}`).toBe(
        'known_refusal',
      )
    }
  })

  it('maps network failures and unlisted statuses to unknown', () => {
    expect(classifyUploadOutcome(new ApiError(0, 'network'))).toBe('unknown')
    expect(classifyUploadOutcome(new ApiError(502, 'proxy'))).toBe('unknown')
    expect(classifyUploadOutcome(new ApiError(401, 'unauthorized'))).toBe('unknown')
  })
})

// ── Controls: today's generic-caller recovery still exists (pins) ────────────
//
// These two rows exercise request() the way every OTHER caller does — no
// switches. They pass in RED by design: they prove the instrument can see a
// re-send and a toast, so the "never resend / no toast" rows above cannot
// pass vacuously.

describe('controls — the generic recovery other callers keep (positive controls)', () => {
  it('a CSRF 403 on a plain request still recovers once and resends', async () => {
    let nth = 0
    const fetchSpy = mockFetch(() => (nth++ === 0 ? errorBody(403, CSRF_BODY) : jsonResponse(200, { ok: true })))
    await expect(request('/control-path', { method: 'POST' })).resolves.toEqual({ ok: true })
    const sent = fetchSpy.mock.calls.filter(([url]: unknown[]) => String(url).endsWith('/api/v1/control-path'))
    expect(sent, 'today: one recovery read and one automatic re-send').toHaveLength(2)
    expect(stateReads()).toHaveLength(1)
  })

  it('a planted-cookie 403 on a plain request still raises the one Retry toast', async () => {
    mockFetch(() => errorBody(403, PLANTED_BODY))
    await expect(request('/control-path', { method: 'POST' })).rejects.toMatchObject({ status: 403 })
    expect(addToastSpy, 'today: the generic planted-cookie toast with a Retry action').toHaveBeenCalledTimes(1)
    const toast = addToastSpy.mock.calls[0]?.[0] as { variant?: string; action?: { label?: string } }
    expect(toast?.variant).toBe('error')
    expect(toast?.action?.label).toBe('Retry')
  })
})
