// Oracle: connected-programs revision 8 FR-051, S54/S140/S141, message table.
// Only HTTP is fake. The generated card, real store/component/query client and
// rendered result are real. Backend publication/generation is tested in Go.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { SessionStateSecretRequest } from '@/lib/api/generated/asyncapi-types'
import type { ProgramConnection, ProgramList } from '@/lib/api/generated/openapi-types'
import { useSecretRequestStore } from '@/store/secretRequests'
import { useSessionStore } from '@/store/session'
import { SecretRequestThreadTail } from './SecretRequestCard'

const card: SessionStateSecretRequest = {
  request_id: 'req-github', session_id: 's-dev', state: 'submitted',
  acting_agent_id: 'dev', acting_agent_name: 'Dev', purpose: 'Connect GitHub',
  recipient: { kind: 'program', program_id: 'github', lines: ['GitHub (gh, git)'] },
  fields: [{ key: 'token', label: 'Token' }], program_generation: 1,
  reference_names: ['program.github'], updated_at: '2026-11-22T00:00:00Z',
}

function openRestoredCard(connection: ProgramConnection) {
  const list: ProgramList = {
    // Contract ConfigurationRevision: exactly 64 lower-case hex characters.
    revision: 'a'.repeat(64), web_chat_allowed: true,
    entries: [{ id: 'github', kind: 'preset', display_name: 'GitHub',
      program_words: ['gh', 'git'], token_variables: ['GH_TOKEN', 'GITHUB_TOKEN'],
      network_sites: ['github.com'], enabled_agent_ids: [], revision: 1, connection }],
  }
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(input instanceof Request ? input.url : String(input), 'http://localhost')
    return new Response(JSON.stringify(url.pathname === '/api/v1/programs' ? list : { error: 'not found' }), {
      status: url.pathname === '/api/v1/programs' ? 200 : 404,
      headers: { 'Content-Type': 'application/json' },
    })
  }))
  act(() => {
    useSessionStore.setState({ activeSessionId: card.session_id })
    useSecretRequestStore.getState().reconcileSession(card.session_id, [card])
  })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}><SecretRequestThreadTail /></QueryClientProvider>)
}

beforeEach(() => { useSecretRequestStore.getState().reset() })
afterEach(() => { cleanup(); vi.unstubAllGlobals() })

it('S140 restores the submitted card as checking while the same generation is held', async () => {
  openRestoredCard({ state: 'checking', generation: 1 })
  expect(await screen.findByText('Stored — checking the account…')).toBeVisible()
  expect(screen.queryByText(/Connected as/)).not.toBeInTheDocument()
})

it('S140 shows the failed-check message for the card generation after reload', async () => {
  const message = 'The account check failed. GitHub said: "HTTP 401: Bad credentials".'
  openRestoredCard({ state: 'check_failed', generation: 1, message })
  expect(await screen.findByText(message)).toBeVisible()
  expect(screen.queryByText(/Connected as/)).not.toBeInTheDocument()
})

it('S54 shows Connected as octocat only for the submitted card generation', async () => {
  openRestoredCard({ state: 'connected', generation: 1, account_label: 'octocat' })
  expect(await screen.findByText('Connected as octocat')).toBeVisible()
})

it('S141 shows replaced rather than a newer generations account after reload', async () => {
  openRestoredCard({ state: 'connected', generation: 2, account_label: 'newer-user' })
  expect(await screen.findByText('Stored — replaced by a newer token')).toBeVisible()
  expect(screen.queryByText(/newer-user/)).not.toBeInTheDocument()
  expect(useSecretRequestStore.getState().byId[card.request_id].request.program_generation).toBe(1)
})
