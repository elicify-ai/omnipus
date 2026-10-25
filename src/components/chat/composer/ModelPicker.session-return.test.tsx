// Oracle: #1436 Expected + RED dispatch: a session keeps its latest explicit
// model on return/reload; the agent default remains visibly identifiable.
// REAL: picker, selector, query functions/validation, stores, browser storage.
// Substitute only HTTP and missing jsdom facilities. No application mocks.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, renderHook, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ClientFrame, WsConnection } from '@/lib/ws'
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

describe('#1436 — unfiled chats keep their latest model without a workspace', () => {
  // Chief ruling 2026-10-24: persistence applies to ALL chats. Keep the two
  // restore paths independent so navigation failure cannot mask reload.
  it.each(['navigation', 'reload'] as const)('restores the unfiled session choice after %s', async (returnPath) => {
    act(() => page.useWorkspacesStore.getState().setActiveWorkspaceId(null))
    const mounted = await renderPicker()
    await choose(FIRST_CHOICE)
    expect(page.useWorkspacesStore.getState().activeWorkspaceId, 'the chosen chat is unfiled').toBeNull()
    expect(page.useChatStore.getState().messages, 'saving a choice does not require sending or a reply').toEqual([])

    if (returnPath === 'navigation') {
      await visit(SESSION_B)
      await choose(LATEST_CHOICE)
      await visit(SESSION_A)
    } else {
      mounted.unmount()
      for (const client of clients.splice(0)) client.clear()
      const oldChatStore = page.useChatStore
      vi.resetModules()
      page = await loadPageModules()
      expect(page.useChatStore, 'reload must discard the previous in-memory store').not.toBe(oldChatStore)
      act(() => {
        page.useWorkspacesStore.getState().setActiveWorkspaceId(null)
        page.useSessionStore.getState().setActiveSession(SESSION_A, agent.id)
      })
      await renderPicker()
    }

    expect(page.useWorkspacesStore.getState().activeWorkspaceId, 'restoration must not silently bind a workspace').toBeNull()
    expect(page.useSessionStore.getState().activeSessionId).toBe(SESSION_A)
    await expectChoice(FIRST_CHOICE)
  })
})

describe('#1436 review gaps — draft choices and storage failure', () => {
  it('R1 keeps the draft model through the real first-send acknowledgement and reload before an answer', async () => {
    const { useConnectionStore } = await import('@/store/connection')
    const { useOmnipusRuntime } = await import('@/lib/omnipus-runtime')
    const frames: ClientFrame[] = []
    const transport = { send: (frame: ClientFrame) => { frames.push(frame); return true }, close: vi.fn(), isConnected: true }
    useConnectionStore.setState({ connection: transport as unknown as WsConnection, isConnected: true })
    act(() => page.useSessionStore.getState().setActiveSession(null, agent.id))
    const mounted = await renderPicker()
    const runtime = renderHook(() => useOmnipusRuntime())
    await choose(FIRST_CHOICE)
    expect(page.useSessionStore.getState().activeSessionId, 'the choice starts before a server session exists').toBeNull()
    await act(async () => {
      runtime.result.current.thread.composer.setText('First message with my chosen model')
      await runtime.result.current.thread.composer.send()
    })
    expect(page.useSessionStore.getState().activeSessionId).toBe('__pending')
    const sent = frames.find((frame) => frame.type === 'message')
    expect(sent).toMatchObject({ type: 'message', metadata: { model_name: FIRST_CHOICE } })
    if (!sent || sent.type !== 'message') throw new Error('The real composer did not send the first message')
    act(() => page.useChatStore.getState().handleFrame({
      type: 'session_started', session_id: SESSION_A, agent_id: agent.id, client_message_id: sent.client_message_id,
    }))
    expect(page.useSessionStore.getState().activeSessionId).toBe(SESSION_A)
    expect(page.useChatStore.getState().messages.filter((message) => message.role === 'assistant' && (message.content.trim() || message.model)), 'no assistant answer/model has arrived').toEqual([])
    mounted.unmount()
    runtime.unmount()
    for (const client of clients.splice(0)) client.clear()
    const oldStore = page.useChatStore
    vi.resetModules()
    page = await loadPageModules()
    expect(page.useChatStore).not.toBe(oldStore)
    const reloadedConnection = await import('@/store/connection')
    reloadedConnection.useConnectionStore.setState({ connection: transport as unknown as WsConnection, isConnected: true })
    act(() => {
      page.useWorkspacesStore.getState().setActiveWorkspaceId(WORKSPACE_ID)
      page.useSessionStore.getState().setActiveSession(SESSION_A, agent.id)
    })
    await renderPicker()
    await expectChoice(FIRST_CHOICE)
    await openPicker()
    expect(modelOption(FIRST_CHOICE, 'OpenRouter')).toHaveAttribute('data-chosen', 'true')
    fireEvent.click(screen.getByTestId('composer-model-selector'))
    const reloadedRuntimeModule = await import('@/lib/omnipus-runtime')
    const nextRuntime = renderHook(() => reloadedRuntimeModule.useOmnipusRuntime())
    await act(async () => {
      nextRuntime.result.current.thread.composer.setText('Next message after reload')
      await nextRuntime.result.current.thread.composer.send()
    })
    expect(frames.filter((frame) => frame.type === 'message').at(-1)).toMatchObject({
      type: 'message', session_id: SESSION_A, metadata: { model_name: FIRST_CHOICE },
    })
  })

  it.each(['QuotaExceededError', 'SecurityError'])('R4 warns and retains in-page choices when storage throws %s', async (errorName) => {
    const { ToastContainer } = await import('@/components/ui/toast-container')
    render(<ToastContainer />)
    await renderPicker()
    const nativeSetItem = Storage.prototype.setItem
    const storage = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, key, value) {
      if (key === 'omnipus.sessionModelChoice.v1') throw new DOMException('Model choice write rejected', errorName)
      nativeSetItem.call(this, key, value)
    })
    try {
      await choose(FIRST_CHOICE)
      const warnings = [...screen.queryAllByRole('status'), ...screen.queryAllByRole('alert')]
        .filter((element) => /model|choice|preference|storage/i.test(element.textContent ?? '') && /sav|remember|persist|storage|permission/i.test(element.textContent ?? ''))
      expect.soft(warnings.length, 'a rejected model-choice write must produce a visible user warning').toBeGreaterThan(0)
      for (const warning of warnings) expect.soft(warning).toBeVisible()
      await visit(SESSION_B)
      await choose(LATEST_CHOICE)
      await visit(SESSION_A)
      await expectChoice(FIRST_CHOICE)
    } finally {
      storage.mockRestore()
    }
  })

  it('R4 warns on malformed preference JSON and keeps the current page choice on return', async () => {
    const { ToastContainer } = await import('@/components/ui/toast-container')
    render(<ToastContainer />)
    await renderPicker()
    await choose(FIRST_CHOICE)
    expect(screen.queryAllByRole('alert')).toEqual([])
    localStorage.setItem('omnipus.sessionModelChoice.v1', '{malformed preference JSON')
    await visit(SESSION_B)
    const warnings = [...screen.queryAllByRole('status'), ...screen.queryAllByRole('alert')]
      .filter((element) => /model|choice|preference|storage/i.test(element.textContent ?? '') && /read|load|corrupt|invalid|storage|sav|remember/i.test(element.textContent ?? ''))
    expect.soft(warnings.length, 'malformed JSON must be visibly distinguishable from an absent preference').toBeGreaterThan(0)
    for (const warning of warnings) expect.soft(warning).toBeVisible()
    await choose(LATEST_CHOICE)
    await visit(SESSION_A)
    await expectChoice(FIRST_CHOICE)
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
