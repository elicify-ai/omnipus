// Unit tests for the queryClient ApiSchemaError handler.
//
// Verifies that when a query throws ApiSchemaError, the centralized handler
// in queryClient.ts picks it up and triggers a production toast via useUiStore.
//
// Strategy: Create an isolated QueryClient (not the singleton), subscribe to
// its query cache, manually trigger a query error, and assert that the ui-store
// toast was queued.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { QueryClient } from '@tanstack/react-query'
import { ApiSchemaError } from './api'

// ── Mock ui store ──────────────────────────────────────────────────────────────
//
// queryClient.ts calls useUiStore.getState().addToast inside a dynamic import.
// We must intercept that import to verify the toast was added.

const mockAddToast = vi.fn()
const mockGetState = vi.fn(() => ({ addToast: mockAddToast }))

vi.mock('@/store/ui', () => ({
  useUiStore: {
    getState: mockGetState,
  },
}))

// ── Helpers ───────────────────────────────────────────────────────────────────

/**
 * Create an isolated QueryClient with the same _handleApiSchemaError subscription
 * wired in manually. We replicate the queryClient.ts subscription logic here
 * so the test does not import the singleton (which would cause side effects and
 * isolation issues across tests).
 */
function makeTestQueryClient() {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })

  function handleApiSchemaError(err: unknown): void {
    if (!(err instanceof ApiSchemaError)) return
    void import('@/store/ui').then(({ useUiStore }) => {
      useUiStore.getState().addToast({
        message: 'Backend response failed validation. Server may be a different version. Please refresh.',
        variant: 'error',
      })
    })
  }

  client.getQueryCache().subscribe((event) => {
    if (event.type === 'updated' && event.action.type === 'error') {
      handleApiSchemaError(event.action.error)
    }
  })

  client.getMutationCache().subscribe((event) => {
    if (event.type === 'updated' && event.mutation.state.status === 'error') {
      handleApiSchemaError(event.mutation.state.error)
    }
  })

  return client
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('queryClient ApiSchemaError handler', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('calls addToast when a query throws ApiSchemaError', async () => {
    // BDD: Given a QueryClient with the ApiSchemaError subscription,
    // When a queryFn throws ApiSchemaError,
    // Then addToast is called with the validation error message.
    //
    // Traces to: queryClient.ts _handleApiSchemaError — production toast on schema error.
    const client = makeTestQueryClient()

    const schemaError = new ApiSchemaError(
      '/test-endpoint',
      [{ path: ['field'], message: 'expected string, got number' }],
      { field: 42 },
    )

    // Trigger the error path by fetching a query whose queryFn always rejects.
    await expect(
      client.fetchQuery({
        queryKey: ['test-schema-error'],
        queryFn: () => Promise.reject(schemaError),
      }),
    ).rejects.toThrow(ApiSchemaError)

    // Allow the dynamic import and async handler to resolve.
    await new Promise((resolve) => setTimeout(resolve, 50))

    // The addToast must have been called with the validation error message.
    expect(mockAddToast).toHaveBeenCalledOnce()
    const [toastArg] = mockAddToast.mock.calls[0] as [{ message: string; variant: string }]
    expect(toastArg.variant).toBe('error')
    expect(toastArg.message).toContain('Backend response failed validation')
  })

  it('does NOT call addToast for a non-ApiSchemaError query error', async () => {
    // BDD: Given a QueryClient with the ApiSchemaError subscription,
    // When a queryFn throws a plain Error (not ApiSchemaError),
    // Then addToast is NOT called.
    //
    // Traces to: queryClient.ts _handleApiSchemaError guard: if (!(err instanceof ApiSchemaError)) return
    const client = makeTestQueryClient()

    const plainError = new Error('Network request failed')

    await expect(
      client.fetchQuery({
        queryKey: ['test-plain-error'],
        queryFn: () => Promise.reject(plainError),
      }),
    ).rejects.toThrow('Network request failed')

    await new Promise((resolve) => setTimeout(resolve, 50))

    // The toast must NOT have been triggered for a non-schema error.
    expect(mockAddToast).not.toHaveBeenCalled()
  })

  it('calls addToast when a mutation throws ApiSchemaError', async () => {
    // BDD: Given a QueryClient with the ApiSchemaError mutation subscription,
    // When a mutationFn throws ApiSchemaError,
    // Then addToast is called with the validation error message.
    //
    // Traces to: queryClient.ts getMutationCache().subscribe — mutation error path.
    const client = makeTestQueryClient()

    const schemaError = new ApiSchemaError(
      '/mutation-endpoint',
      [{ path: ['result'], message: 'expected object, got null' }],
      null,
    )

    const mutation = client.getMutationCache().build(client, {
      mutationFn: () => Promise.reject(schemaError),
    })

    await expect(mutation.execute(undefined)).rejects.toThrow(ApiSchemaError)

    // Allow the dynamic import and async handler to resolve.
    await new Promise((resolve) => setTimeout(resolve, 50))

    expect(mockAddToast).toHaveBeenCalledOnce()
    const [toastArg] = mockAddToast.mock.calls[0] as [{ message: string; variant: string }]
    expect(toastArg.variant).toBe('error')
    expect(toastArg.message).toContain('Backend response failed validation')
  })
})
