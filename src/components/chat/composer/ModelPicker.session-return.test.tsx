// Oracle: #1436 Expected + RED dispatch: a session keeps its latest explicit
// model on return/reload; the agent default remains visibly identifiable.
// REAL: picker, selector, query functions/validation, stores, browser storage.
// Substitute only HTTP and missing jsdom facilities. No application mocks.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  agent, DEFAULT_MODEL, FIRST_CHOICE, LATEST_CHOICE, SESSION_A, SESSION_B, WORKSPACE_ID,
  installSessionReturnBrowser, installSessionReturnNetwork,
} from '../../../../tests/session-return/fixtures'

async function loadPageModules() {
  const { useChatStore } = await import('@/store/chat')
  const { useSessionStore } = await import('@/store/session')
  const { useWorkspacesStore } = await import('@/store/workspacesStore')
  const { ModelPicker } = await import('./ModelPicker')
  const { fetchAgents, fetchProviders } = await import('@/lib/api')
  return { useChatStore, useSessionStore, useWorkspacesStore, ModelPicker, fetchAgents, fetchProviders }
}

let page: Awaited<ReturnType<typeof loadPageModules>>
let unexpectedRequests: string[]
let restoreBrowser: () => void
const clients: QueryClient[] = []

beforeEach(async () => {
  localStorage.clear()
  sessionStorage.clear()
  vi.resetModules()
  restoreBrowser = installSessionReturnBrowser()
  unexpectedRequests = installSessionReturnNetwork()
  page = await loadPageModules()
  act(() => {
    page.useWorkspacesStore.getState().setActiveWorkspaceId(WORKSPACE_ID)
    page.useSessionStore.getState().setActiveSession(SESSION_A, agent.id)
  })
})

afterEach(() => {
  cleanup()
  for (const client of clients.splice(0)) client.clear()
  restoreBrowser()
  vi.unstubAllGlobals()
  expect(unexpectedRequests, 'all HTTP calls must use a declared contract fixture').toEqual([])
  localStorage.clear()
  sessionStorage.clear()
})

async function renderPicker() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } })
  clients.push(client)
  // Resolve the real validated queries before mounting, so the regression is
  // not confused with a separate async agent-default loading race.
  await client.fetchQuery({ queryKey: ['agents'], queryFn: page.fetchAgents })
  await client.fetchQuery({ queryKey: ['providers'], queryFn: page.fetchProviders })
  const { ModelPicker } = page
  return render(<QueryClientProvider client={client}><ModelPicker /></QueryClientProvider>)
}

async function openPicker() {
  fireEvent.click(screen.getByTestId('composer-model-selector'))
  await screen.findByRole('listbox')
}

function modelOption(model: string, providerName = 'OpenRouter') {
  const group = screen.getByRole('group', { name: providerName })
  return within(group).getByRole('option', { name: (name) => name.includes(model) })
}

async function choose(model: string) {
  await openPicker()
  fireEvent.click(modelOption(model))
  await expectChoice(model)
}

async function expectChoice(model: string) {
  await waitFor(() => {
    const state = page.useChatStore.getState()
    expect({ model: state.nextModel, provider: state.nextModelProvider }, 'outgoing model pair must match the last user choice').toEqual({ model, provider: 'openrouter' })
    expect(screen.getByTestId('composer-model-selector'), 'visible selector must agree with outgoing choice').toHaveTextContent(model)
  })
}

async function visit(sessionId: string) {
  act(() => page.useSessionStore.getState().setActiveSession(sessionId, agent.id))
  await waitFor(() => expect(page.useSessionStore.getState().activeSessionId).toBe(sessionId))
}

function seedOlderAssistant(model: string) {
  act(() => page.useChatStore.getState().setMessages([{
    id: 'older-assistant', role: 'assistant', content: 'Earlier response',
    timestamp: '2026-10-23T12:00:00Z', status: 'done', model,
  }]))
}

describe('#1436 — latest model belongs to the session', () => {
  it('restores A\'s choice after B with the same agent chose a different model', async () => {
    await renderPicker()
    await choose(FIRST_CHOICE)
    await visit(SESSION_B)
    await choose(LATEST_CHOICE)
    await visit(SESSION_A)
    await expectChoice(FIRST_CHOICE)
  })

  it('restores the latest replacement choice, not the first choice or default', async () => {
    await renderPicker()
    await choose(FIRST_CHOICE)
    await choose(LATEST_CHOICE)
    await visit(SESSION_B)
    await visit(SESSION_A)
    await expectChoice(LATEST_CHOICE)
  })

  it('does not replace an explicit choice with an older assistant\'s model on return', async () => {
    seedOlderAssistant(DEFAULT_MODEL)
    await renderPicker()
    await choose(FIRST_CHOICE)
    await visit(SESSION_B)
    await visit(SESSION_A)
    await expectChoice(FIRST_CHOICE)
  })

  it('keeps a choice made before sending across a reload with fresh page modules', async () => {
    const mounted = await renderPicker()
    await choose(FIRST_CHOICE)
    expect(page.useChatStore.getState().messages, 'no assistant response may be required to save a selector choice').toEqual([])
    mounted.unmount()
    for (const client of clients.splice(0)) client.clear()

    // Real module re-import, not a partial setState that leaves a hidden
    // in-memory per-session map alive. Only browser storage survives.
    const oldChatStore = page.useChatStore
    vi.resetModules()
    page = await loadPageModules()
    expect(page.useChatStore, 'reload must instantiate a different store').not.toBe(oldChatStore)
    act(() => {
      page.useWorkspacesStore.getState().setActiveWorkspaceId(WORKSPACE_ID)
      page.useSessionStore.getState().setActiveSession(SESSION_A, agent.id)
    })
    await renderPicker()
    await expectChoice(FIRST_CHOICE)
  })

  it('keeps a deliberate switch back to the default instead of resurrecting the transcript model', async () => {
    seedOlderAssistant(FIRST_CHOICE)
    await renderPicker()
    await choose(DEFAULT_MODEL)
    await visit(SESSION_B)
    await visit(SESSION_A)
    await expectChoice(DEFAULT_MODEL)
  })
})

describe('#1436 — visible agent default is independent of current selection', () => {
  it('marks only the agent\'s default provider/model row while another model is chosen', async () => {
    await renderPicker()
    await choose(FIRST_CHOICE)
    await openPicker()
    const defaultRow = modelOption(DEFAULT_MODEL)
    const sameSlugOtherProvider = modelOption(DEFAULT_MODEL, 'Backup')
    const chosenRow = modelOption(FIRST_CHOICE)
    expect(chosenRow).toHaveAttribute('data-chosen', 'true')
    expect(defaultRow).not.toHaveAttribute('data-chosen', 'true')
    expect(sameSlugOtherProvider).not.toHaveAttribute('data-chosen', 'true')
    // #1436 requires a user-visible default mark, not the selection check or
    // cmdk keyboard cursor. Copy is flexible; it must visibly identify Default.
    expect(within(defaultRow).getByText(/\bdefault\b/i)).toBeVisible()
    expect(within(sameSlugOtherProvider).queryByText(/\bdefault\b/i)).not.toBeInTheDocument()
    expect(within(chosenRow).queryByText(/\bdefault\b/i)).not.toBeInTheDocument()
  })
})
