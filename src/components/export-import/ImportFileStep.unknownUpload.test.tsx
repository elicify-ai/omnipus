// Import File step — unknown upload answer, spec order 26a (issue #1967 B1).
// Oracle: spec scenario "Unknown upload answer is not resent": the upload's
// connection is cut after the server accepted it → exactly ONE upload request
// and the did-not-confirm text; the step stays. Real transport over a mocked
// fetch (the packages module stays real; this is a lost-answer row, not a
// replay row, but the real pipeline costs nothing and pins the counting).
// The wizard rides the production QueryClient mutation defaults (CHECK F04
// round 2): this row is the upload call site's never-resend oracle.
import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { shouldRetryMutation, mutationRetryDelay } from '@/lib/queryClient'

let mockSearch: Record<string, string | undefined> = {}

const { navigateSpy, mockAgents, mockWorkspaces } = vi.hoisted(() => ({
  navigateSpy: vi.fn(),
  mockAgents: vi.fn(),
  mockWorkspaces: vi.fn(),
}))

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    createFileRoute: () => (opts: Record<string, unknown>) => ({ ...opts, useSearch: () => mockSearch }),
    useNavigate: () => navigateSpy,
  }
})

vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  fetchAgents: mockAgents,
  fetchWorkspaces: mockWorkspaces,
}))

import { Route } from '@/routes/_app/export-import'
import { rememberIdentityMode } from '@/lib/api/identityMode'
import { useUiStore } from '@/store/ui'

const DID_NOT_CONFIRM =
  'Omnipus did not confirm the upload. Choose the file again. If the upload did arrive, it can no longer be used after 30 minutes, and Omnipus will try to remove its temporary files at a later upload.'

beforeEach(() => {
  mockSearch = { wizard: 'import' }
  navigateSpy.mockReset()
  mockAgents.mockResolvedValue([])
  mockWorkspaces.mockResolvedValue([])
  rememberIdentityMode('local')
  useUiStore.setState({ addToast: vi.fn() as unknown as () => void })
  document.cookie = 'csrf=import-unknown-test; path=/'
})

afterEach(() => {
  vi.restoreAllMocks()
  document.cookie = 'csrf=; max-age=0; path=/'
  sessionStorage.clear()
})

describe('Unknown upload answer is not resent (US-5, D2-14)', () => {
  // The wizard mounts under the PRODUCTION mutation defaults (CHECK F04
  // round 2): this row's single-dispatch assertion is the upload call site's
  // never-resend oracle, so a GREEN wizard that omits retry:false on the
  // upload mutation fails it — the api-level control proves those same
  // defaults resend a retryable failure four times.
  function renderWizard() {
    const client = new QueryClient({
      defaultOptions: { mutations: { retry: shouldRetryMutation, retryDelay: mutationRetryDelay } },
    })
    const Component = (Route as unknown as { component: React.ComponentType }).component
    return render(
      <QueryClientProvider client={client}>
        <Component />
      </QueryClientProvider>,
    )
  }

  it('a cut upload connection: exactly one upload request and the did-not-confirm text', async () => {
    const uploadCalls: Array<unknown> = []
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (url) => {
      const u = String(url)
      if (u.endsWith('/api/v1/packages/import')) {
        uploadCalls.push(u)
        throw new TypeError('connection cut')
      }
      throw new Error(`unexpected fetch in test: ${u}`)
    })
    renderWizard()
    // 26a right-reason RED (CHECK): the input's availability is asserted
    // BEFORE dispatch, so this row can never fail as "Unable to fire a change
    // event" — its only sanctioned failure is the missing behaviour itself.
    const input = document.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(input, 'the File step renders a bundle-file input before the change event fires').not.toBeNull()
    const file = new File([new Uint8Array(64)], 'bundle.omnpkg', { type: 'application/octet-stream' })
    await act(async () => {
      fireEvent.change(input!, { target: { files: [file] } })
    })
    expect(await screen.findByText(DID_NOT_CONFIRM)).toBeInTheDocument()
    expect(uploadCalls, 'exactly one upload request, never resent').toHaveLength(1)
    // Still on the File step.
    expect(screen.getByRole('button', { name: 'Choose a bundle file' })).toBeInTheDocument()
    expect(screen.queryByText('Will be added')).toBeNull()
  })
})
