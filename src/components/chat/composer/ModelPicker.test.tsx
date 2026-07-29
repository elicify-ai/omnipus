/**
 * ModelPicker tests.
 *
 * ModelPicker (extracted verbatim from ChatControls per the composer-redesign
 * plan — see the design assets in .preview-doc/composer-redesign/) wraps the
 * per-message <ModelSelector> plus its data wiring:
 *   - `data-testid="composer-model-selector"` on the trigger
 *   - the `nextModel`/`setNextModel` chat-store slice
 *   - the connected-providers query (`availableModels`/`providerGroups`)
 *   - the `activeAgentModel` placeholder fallback
 *   - the `transcriptLastModel`/`lastSeedKey` navigation re-seed effect
 *     (moved from ChatControls)
 *   - the `modelSelectorOpen`/`setModelSelectorOpen` ui-store wiring (used by
 *     the /model slash command to open the picker programmatically)
 *
 * Scaffolding (mocks, ResizeObserver/scrollIntoView stubs, QueryClient
 * wrapper) ported from src/components/chat/ChatControls.test.tsx
 * (describe('ChatControls — Model selector (interactive)')) and
 * src/components/chat/ChatControls.agent-selector-open.test.tsx (since
 * removed — controlled open-state pattern). The FR-010 sendMessage/metadata.model_name wire
 * contract lives in ChatScreen.test.tsx
 * (describe('OmnipusComposer — model selector store contracts (FR-010)')) —
 * out of scope here since ModelPicker does not call sendMessage itself.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useChatStore, type ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useConnectionStore } from '@/store/connection'

// ── API mocks ─────────────────────────────────────────────────────────────────

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([
      {
        id: 'mia',
        name: 'Mia',
        type: 'core',
        status: 'active',
        model: 'z-ai/glm-5.2',
        description: 'Assistant',
      },
      {
        id: 'jim',
        name: 'Jim',
        type: 'core',
        status: 'idle',
        description: 'Orchestrator', // no `model` — exercises the null activeAgentModel path
      },
    ]),
    fetchProviders: vi.fn().mockResolvedValue([
      {
        id: 'openrouter',
        name: 'openrouter',
        display_name: 'OpenRouter',
        status: 'connected',
        models: ['z-ai/glm-5.2', 'z-ai/glm-5-turbo', 'openai/gpt-4o'],
      },
    ]),
  }
})

// ── ResizeObserver / scrollIntoView stubs (required by cmdk inside the popover) ─

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

// Static import after mocks are in place.
import { ModelPicker } from './ModelPicker'

// ── Render helper ─────────────────────────────────────────────────────────────

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderPicker() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <ModelPicker />
    </QueryClientProvider>,
  )
}

function assistantMsg(id: string, model: string): ChatMessage {
  return {
    id,
    role: 'assistant',
    content: 'hi',
    timestamp: '2026-07-13T00:00:00Z',
    status: 'done',
    model,
  }
}

// ── Store reset ───────────────────────────────────────────────────────────────

beforeEach(() => {
  vi.clearAllMocks()
  act(() => {
    useSessionStore.setState({ activeAgentId: 'mia', activeSessionId: 'sess_1' })
    useChatStore.setState({ nextModel: null, messages: [] })
    useUiStore.setState({ modelSelectorOpen: false })
  })
})

// ── Render + testid ───────────────────────────────────────────────────────────

describe('ModelPicker — renders the model selector', () => {
  it('renders the ModelSelector trigger with data-testid="composer-model-selector"', async () => {
    renderPicker()
    // Before the providers query resolves, ModelSelector's catalogEmpty
    // branch renders a disabled placeholder <div> (no role) at this same
    // testid — wait for the query to settle and the real interactive
    // <button role="combobox"> to replace it before asserting on it.
    await vi.waitFor(() => {
      expect(screen.getByTestId('composer-model-selector').getAttribute('role')).toBe('combobox')
    })
    expect(screen.getByTestId('composer-model-selector')).toBeInTheDocument()
  })

  // Composer tab ring, position 4 — the value itself is ChatScreen's
  // contract (see src/components/chat/ChatScreen.tab-ring.test.tsx), but the
  // plumbing that the `tabIndex` prop actually reaches the interactive
  // trigger element (not just an ignored prop) is this component's own
  // contract to keep.
  it('forwards the tabIndex prop onto the ModelSelector trigger', async () => {
    render(
      <QueryClientProvider client={makeClient()}>
        <ModelPicker tabIndex={4} />
      </QueryClientProvider>,
    )
    await vi.waitFor(() => {
      expect(screen.getByTestId('composer-model-selector').getAttribute('role')).toBe('combobox')
    })
    expect(screen.getByTestId('composer-model-selector')).toHaveAttribute('tabindex', '4')
  })
})

// ── nextModel / setNextModel store contract ─────────────────────────────────────

describe('ModelPicker — nextModel/setNextModel store contract', () => {
  it('picking a model from the dropdown calls setNextModel with the picked model', async () => {
    renderPicker()
    await vi.waitFor(() => screen.getByTestId('composer-model-selector'))

    // Drive the picker open via the store (the same mechanism the /model
    // slash command uses — see the modelSelectorOpen tests below) rather
    // than simulating the trigger's internal pointer-toggle behavior.
    act(() => {
      useUiStore.getState().setModelSelectorOpen(true)
    })

    const item = await vi.waitFor(() => screen.getByText('openai/gpt-4o'))
    fireEvent.click(item)

    await vi.waitFor(() => {
      expect(useChatStore.getState().nextModel).toBe('openai/gpt-4o')
    })
  })

  it('picking a second, DIFFERENT model produces a DIFFERENT store value (not a hardcoded pick)', async () => {
    renderPicker()
    await vi.waitFor(() => screen.getByTestId('composer-model-selector'))

    act(() => { useUiStore.getState().setModelSelectorOpen(true) })
    const firstItem = await vi.waitFor(() => screen.getByText('openai/gpt-4o'))
    fireEvent.click(firstItem)
    await vi.waitFor(() => expect(useChatStore.getState().nextModel).toBe('openai/gpt-4o'))

    act(() => { useUiStore.getState().setModelSelectorOpen(true) })
    const secondItem = await vi.waitFor(() => screen.getByText('z-ai/glm-5-turbo'))
    fireEvent.click(secondItem)
    await vi.waitFor(() => expect(useChatStore.getState().nextModel).toBe('z-ai/glm-5-turbo'))
  })

})

// ── transcriptLastModel / lastSeedKey re-seed effect ────────────────────────────

describe('ModelPicker — transcriptLastModel/lastSeedKey re-seed effect', () => {
  it('re-seeds nextModel from the transcript\'s last assistant model on mount', async () => {
    act(() => {
      useChatStore.setState({ messages: [assistantMsg('m1', 'z-ai/glm-5-turbo')] })
    })
    renderPicker()
    await vi.waitFor(() => {
      expect(useChatStore.getState().nextModel).toBe('z-ai/glm-5-turbo')
    })
  })

  it('re-seeds to a DIFFERENT value when navigating to a session with a DIFFERENT transcript last model', async () => {
    act(() => {
      useChatStore.setState({ messages: [assistantMsg('m1', 'z-ai/glm-5-turbo')] })
    })
    renderPicker()
    await vi.waitFor(() => {
      expect(useChatStore.getState().nextModel).toBe('z-ai/glm-5-turbo')
    })

    // Navigate: new session id + new agent id + a different transcript.
    // Differentiation: session A seeds 'z-ai/glm-5-turbo', session B seeds
    // 'openai/gpt-4o' — two different seed keys must produce two different
    // re-seeded values, proving the effect is not a no-op / hardcoded seed.
    act(() => {
      useSessionStore.setState({ activeSessionId: 'sess_2', activeAgentId: 'mia' })
      useChatStore.setState({ messages: [assistantMsg('m2', 'openai/gpt-4o')] })
    })
    await vi.waitFor(() => {
      expect(useChatStore.getState().nextModel).toBe('openai/gpt-4o')
    })
  })

  it('does NOT clobber an explicit user selection for the SAME seed key', async () => {
    renderPicker()
    await vi.waitFor(() => screen.getByTestId('composer-model-selector'))

    // User explicitly picks a model.
    act(() => {
      useChatStore.getState().setNextModel('openai/gpt-4o')
    })
    expect(useChatStore.getState().nextModel).toBe('openai/gpt-4o')

    // A new assistant message with a DIFFERENT model lands on the SAME
    // session/agent (e.g. a delayed replay frame) — seedKey is unchanged,
    // so the effect must not overwrite the explicit pick.
    act(() => {
      useChatStore.setState({
        messages: [
          ...useChatStore.getState().messages,
          assistantMsg('m3', 'z-ai/glm-5-turbo'),
        ],
      })
    })
    // Flush any pending effects/microtasks.
    await vi.waitFor(() => {
      expect(useChatStore.getState().messages).toHaveLength(1)
    })
    expect(useChatStore.getState().nextModel).toBe('openai/gpt-4o')

    // Also verify agent/provider queries settling afterward doesn't clobber it either.
    await vi.waitFor(() => screen.getByTestId('composer-model-selector'))
    expect(useChatStore.getState().nextModel).toBe('openai/gpt-4o')
  })

  it('does not re-seed while the session id is transitioning through the "__pending" bucket', async () => {
    renderPicker()
    await vi.waitFor(() => screen.getByTestId('composer-model-selector'))
    act(() => {
      useChatStore.getState().setNextModel('openai/gpt-4o')
    })

    // A fresh chat's first send transiently sets activeSessionId to '__pending'.
    act(() => {
      useSessionStore.setState({ activeSessionId: '__pending', activeAgentId: 'mia' })
    })
    expect(useChatStore.getState().nextModel).toBe('openai/gpt-4o')

    // '__pending' materializes into the real session id — same conversation continuing.
    act(() => {
      useSessionStore.setState({ activeSessionId: 'sess_materialized', activeAgentId: 'mia' })
    })
    await vi.waitFor(() => {
      expect(useSessionStore.getState().activeSessionId).toBe('sess_materialized')
    })
    // The user's model pick must survive the '' → '__pending' → realId transition.
    expect(useChatStore.getState().nextModel).toBe('openai/gpt-4o')
  })
})

// ── modelSelectionSource — activeAgentId-only change (same session) ─────────────
//
// The twin of the AGENT PRECEDENCE RULE fixed for agents in 5157e378
// ("session attach clobbering the picked agent"). Here the bug is on the
// MODEL side: activeSessionId stays the SAME (no navigation to a different
// conversation) but activeAgentId changes — e.g. the user picks a different
// agent from AgentPicker or the "@" mention menu mid-conversation. Before the
// fix, the re-seed effect treated this identically to a genuine navigation
// and silently reset nextModel to the new agent's default, discarding
// whatever model the user had just explicitly picked — and THAT overwritten
// value is what went out on the wire (metadata.model_name).
//
// Both halves of the contract are asserted, and the wire frame — not just
// component state — is checked in each: an explicit pick must survive, and a
// user who never picked must still get the new agent's default (a frozen
// picker would be just as wrong as a clobbered one).
describe('ModelPicker — activeAgentId-only change (same session, mid-conversation agent switch)', () => {
  function fakeConnection(sentFrames: unknown[]) {
    return {
      send: (frame: unknown) => {
        sentFrames.push(frame)
        return true
      },
      disconnect: vi.fn(),
      connect: vi.fn(),
      isConnected: true,
    }
  }

  it('an explicit user pick SURVIVES an activeAgentId change within the same session, and the WIRE frame carries the picked model', async () => {
    renderPicker()
    await waitForLoadedTrigger()

    // Real user pick, through the actual dropdown (exercises the production
    // onPickerChange handler, not a mocked one).
    act(() => { useUiStore.getState().setModelSelectorOpen(true) })
    const item = await vi.waitFor(() => screen.getByText('z-ai/glm-5-turbo'))
    fireEvent.click(item)
    await vi.waitFor(() => expect(useChatStore.getState().nextModel).toBe('z-ai/glm-5-turbo'))

    // Agent switches mid-conversation (AgentPicker/"@" mention) — SAME
    // session, to 'jim' whose mock has NO configured model. Without the fix
    // this re-seeds nextModel to null (jim's absent default).
    act(() => {
      useSessionStore.setState({ activeSessionId: 'sess_1', activeAgentId: 'jim' })
    })

    // Component state: the explicit pick must survive.
    expect(useChatStore.getState().nextModel).toBe('z-ai/glm-5-turbo')
    // And the rendered trigger must show it too (not just the store).
    expect(screen.getByTestId('composer-model-selector').getAttribute('aria-label')).toBe(
      'Model selector, currently z-ai/glm-5-turbo',
    )

    // WIRE PROOF: what the picker shows is what actually gets sent.
    const sentFrames: unknown[] = []
    act(() => {
      useConnectionStore.setState({ connection: fakeConnection(sentFrames) as never, isConnected: true })
    })
    act(() => {
      const nm = useChatStore.getState().nextModel
      useChatStore.getState().sendMessage('hello', nm ? { model_name: nm } : undefined)
    })
    const last = sentFrames.at(-1) as { type?: string; metadata?: { model_name?: string } }
    expect(last?.type).toBe('message')
    expect(last?.metadata?.model_name).toBe('z-ai/glm-5-turbo')
  })

  it('with NO explicit pick, an activeAgentId change within the same session DOES re-seed to the new agent\'s default — the clobber must not be "fixed" by freezing the picker', async () => {
    renderPicker()
    await waitForLoadedTrigger()

    // Simulate whatever value the auto-seed had previously produced, WITHOUT
    // going through the picker's onChange (no explicit user pick was made —
    // this mirrors the store-level seeding the effect itself performs).
    act(() => {
      useChatStore.getState().setNextModel('z-ai/glm-5.2')
    })

    // Agent switches mid-conversation, same session, no explicit pick in force.
    act(() => {
      useSessionStore.setState({ activeSessionId: 'sess_1', activeAgentId: 'jim' })
    })

    // jim has no configured model and there is no transcript — nextModel
    // must follow the new agent's (absent) default, not stay pinned.
    await vi.waitFor(() => expect(useChatStore.getState().nextModel).toBeNull())
    expect(screen.getByTestId('composer-model-selector').textContent).toMatch(/select a model/i)

    // WIRE PROOF: no override reaches the wire either — the server falls
    // back to jim's own configured model, matching what the UI shows.
    const sentFrames: unknown[] = []
    act(() => {
      useConnectionStore.setState({ connection: fakeConnection(sentFrames) as never, isConnected: true })
    })
    act(() => {
      const nm = useChatStore.getState().nextModel
      useChatStore.getState().sendMessage('hello', nm ? { model_name: nm } : undefined)
    })
    const last = sentFrames.at(-1) as { type?: string; metadata?: unknown }
    expect(last?.type).toBe('message')
    expect(last?.metadata).toBeUndefined()
  })

  it('an explicit pick made for one agent, after a no-pick agent switch, still re-arms and survives the NEXT agent switch', async () => {
    renderPicker()
    await waitForLoadedTrigger()

    // No explicit pick yet; agent switches once (auto re-seed, per the test above).
    act(() => {
      useSessionStore.setState({ activeSessionId: 'sess_1', activeAgentId: 'jim' })
    })
    await vi.waitFor(() => expect(useChatStore.getState().nextModel).toBeNull())

    // NOW the user explicitly picks a model for jim's turn.
    act(() => { useUiStore.getState().setModelSelectorOpen(true) })
    const item = await vi.waitFor(() => screen.getByText('openai/gpt-4o'))
    fireEvent.click(item)
    await vi.waitFor(() => expect(useChatStore.getState().nextModel).toBe('openai/gpt-4o'))

    // Switch agents again, same session — the fresh pick must survive too.
    act(() => {
      useSessionStore.setState({ activeSessionId: 'sess_1', activeAgentId: 'mia' })
    })
    expect(useChatStore.getState().nextModel).toBe('openai/gpt-4o')
  })
})

// ── modelSelectorOpen / setModelSelectorOpen ui-store wiring ────────────────────

describe('ModelPicker — modelSelectorOpen/setModelSelectorOpen ui-store wiring', () => {
  it('does NOT render the popover content when modelSelectorOpen is false (default)', async () => {
    renderPicker()
    await vi.waitFor(() => screen.getByTestId('composer-model-selector'))
    expect(useUiStore.getState().modelSelectorOpen).toBe(false)
    expect(screen.queryByPlaceholderText(/search models/i)).not.toBeInTheDocument()
  })

  it('renders the popover content when modelSelectorOpen is already true at mount (store-driven open, e.g. /model)', async () => {
    act(() => {
      useUiStore.setState({ modelSelectorOpen: true })
    })
    renderPicker()
    await vi.waitFor(() => {
      expect(screen.getByPlaceholderText(/search models/i)).toBeInTheDocument()
    })
  })

  it('setting modelSelectorOpen=true after mount opens the popover', async () => {
    renderPicker()
    await vi.waitFor(() => screen.getByTestId('composer-model-selector'))
    expect(screen.queryByPlaceholderText(/search models/i)).not.toBeInTheDocument()

    act(() => {
      useUiStore.getState().setModelSelectorOpen(true)
    })

    await vi.waitFor(() => {
      expect(screen.getByPlaceholderText(/search models/i)).toBeInTheDocument()
    })
  })

  it('picking a model closes the popover by writing modelSelectorOpen=false back to the store', async () => {
    renderPicker()
    await vi.waitFor(() => screen.getByTestId('composer-model-selector'))
    act(() => { useUiStore.getState().setModelSelectorOpen(true) })
    const item = await vi.waitFor(() => screen.getByText('z-ai/glm-5-turbo'))
    fireEvent.click(item)

    await vi.waitFor(() => {
      expect(useUiStore.getState().modelSelectorOpen).toBe(false)
    })
    expect(screen.queryByPlaceholderText(/search models/i)).not.toBeInTheDocument()
  })
})

// ── activeAgentModel placeholder fallback ────────────────────────────────────────

// Waits until ModelSelector has left its transient "catalogEmpty" loading
// state — the providers query starts empty on first render, so the trigger
// briefly renders as a disabled placeholder <div> (no role, "Connect a
// provider…" text) at the SAME testid before switching to the real
// interactive <button role="combobox"> once fetchProviders resolves. That
// switch is a different DOM element (div → button), so any reference
// captured before this settles is stale — callers must re-query
// screen.getByTestId(...) fresh after this resolves, not reuse an earlier
// handle.
async function waitForLoadedTrigger(): Promise<HTMLElement> {
  await vi.waitFor(() => {
    expect(screen.getByTestId('composer-model-selector').getAttribute('role')).toBe('combobox')
  })
  return screen.getByTestId('composer-model-selector')
}

describe('ModelPicker — activeAgentModel fallback', () => {
  it('shows the active agent\'s model as the placeholder when nextModel is null', async () => {
    renderPicker()
    await waitForLoadedTrigger()

    // Force nextModel back to null without changing the session/agent seed
    // key (so the re-seed effect's guard does not refire and overwrite this
    // — see the "does NOT clobber" test above for the same mechanism).
    act(() => {
      useChatStore.getState().setNextModel(null)
    })
    await vi.waitFor(() => {
      expect(useChatStore.getState().nextModel).toBeNull()
    })

    const trigger = screen.getByTestId('composer-model-selector')
    expect(trigger.textContent).toContain('z-ai/glm-5.2')
    // The aria-label omits "currently" specifically when the ModelSelector's
    // `value` prop is empty (see model-selector.tsx) — this is how we tell
    // "rendered via the placeholder fallback" apart from "rendered as an
    // explicit value", even though the displayed text is identical.
    expect(trigger.getAttribute('aria-label')).toBe('Model selector, z-ai/glm-5.2')
    expect(trigger.getAttribute('aria-label')).not.toContain('currently')
  })

  it('DIFFERENTIATION: the same displayed text renders as an explicit "currently" value once the user has picked it', async () => {
    renderPicker()
    await waitForLoadedTrigger()

    act(() => {
      useChatStore.getState().setNextModel('z-ai/glm-5.2')
    })

    await vi.waitFor(() => {
      expect(screen.getByTestId('composer-model-selector').getAttribute('aria-label')).toContain('currently')
    })
    expect(screen.getByTestId('composer-model-selector').getAttribute('aria-label')).toBe('Model selector, currently z-ai/glm-5.2')
  })

  it('shows the generic "Select a model…" placeholder when the active agent has no configured model', async () => {
    act(() => {
      useSessionStore.setState({ activeAgentId: 'jim', activeSessionId: 'sess_1' })
    })
    renderPicker()
    const trigger = await waitForLoadedTrigger()
    act(() => {
      useChatStore.getState().setNextModel(null)
    })
    await vi.waitFor(() => {
      expect(useChatStore.getState().nextModel).toBeNull()
    })
    expect(trigger.textContent).toMatch(/select a model/i)
  })
})

// ── disabled (read-only) mode ────────────────────────────────────────────────

describe('ModelPicker — disabled prop (read-only session)', () => {
  it('disables the ModelSelector trigger when disabled=true', async () => {
    render(
      <QueryClientProvider client={makeClient()}>
        <ModelPicker disabled />
      </QueryClientProvider>,
    )
    await vi.waitFor(() => {
      expect(screen.getByTestId('composer-model-selector')).toBeDisabled()
    })
  })

  it('does NOT disable the trigger when disabled is omitted (default false)', async () => {
    renderPicker()
    await waitForLoadedTrigger()
    expect(screen.getByTestId('composer-model-selector')).not.toBeDisabled()
  })
})
