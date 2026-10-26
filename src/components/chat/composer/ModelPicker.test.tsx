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
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useChatStore, type ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useConnectionStore } from '@/store/connection'
import { fetchAgents } from '@/lib/api'

// ── API mocks ─────────────────────────────────────────────────────────────────

// Per-test provider fixtures. The DEFAULT list is byte-identical to the fixed
// list this file always mocked (every pre-existing test below depends on it);
// the subscription-provider tests at the bottom swap it via
// setMockProviders() and their beforeEach restores the default.
const providerFixtures = vi.hoisted(() => ({
  list: [
    {
      id: 'openrouter',
      name: 'openrouter',
      display_name: 'OpenRouter',
      status: 'connected',
      models: ['z-ai/glm-5.2', 'z-ai/glm-5-turbo', 'openai/gpt-4o'],
    },
  ] as Array<Record<string, unknown>>,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([
      {
        figure: 'Omnipus', role: 'general',
        id: 'mia',
        name: 'Mia',
        type: 'core',
        status: 'active',
        model: 'z-ai/glm-5.2',
        description: 'Assistant',
      },
      {
        figure: 'Omnipus', role: 'general',
        id: 'jim',
        name: 'Jim',
        type: 'core',
        status: 'idle',
        description: 'Orchestrator', // no `model` — exercises the null activeAgentModel path
      },
    ]),
    fetchProviders: vi.fn(() => Promise.resolve(providerFixtures.list)),
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
  // Reused session ids represent independent tests, not a returning browser.
  // Do not inherit another test's explicit per-session model choice.
  localStorage.removeItem('omnipus.sessionModelChoice.v1')
  vi.clearAllMocks()
  act(() => {
    useSessionStore.setState({ activeAgentId: 'mia', activeSessionId: 'sess_1' })
    // H6: reset the picked-provider twin too, or a dual-provider test's
    // pick leaks into the next test's display state.
    useChatStore.setState({ nextModel: null, nextModelProvider: null, messages: [] })
    useUiStore.setState({ modelSelectorOpen: false })
  })
})

// H6 R2: the agent-default seed test swaps the fetchAgents fixture (an
// implementation override SURVIVES vi.clearAllMocks). Restore the factory
// list byte-identically so no other describe inherits it.
afterEach(() => {
  vi.mocked(fetchAgents).mockResolvedValue([
    {
      figure: 'Omnipus', role: 'general',
      id: 'mia',
      name: 'Mia',
      type: 'core',
      status: 'active',
      model: 'z-ai/glm-5.2',
      description: 'Assistant',
    },
    {
      figure: 'Omnipus', role: 'general',
      id: 'jim',
      name: 'Jim',
      type: 'core',
      status: 'idle',
      description: 'Orchestrator',
    },
  ] as never)
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

// ── Subscription providers (ADR-068 FR-034) ──────────────────────────────────
//
// A sign-in provider (openai-chatgpt et al.) is `signed_in`, never
// `connected` — the composer picker filtered on `status === 'connected'` and
// silently dropped every subscription provider's models: the founder's live
// instance showed 21 GPT models on a genuinely authenticated openai-chatgpt
// row that the chat model picker refused to list. The picker must go through
// providerStatus.isProviderUsable (connected OR signed_in), and `expired`
// must stay EXCLUDED — a lapsed session's models cannot run, and offering
// them would trade one dead end for another.

describe('ModelPicker — subscription (signed_in) providers are usable', () => {
  beforeEach(() => {
    providerFixtures.list = [
      {
        id: 'openrouter',
        name: 'openrouter',
        display_name: 'OpenRouter',
        status: 'connected',
        models: ['z-ai/glm-5.2', 'z-ai/glm-5-turbo', 'openai/gpt-4o'],
      },
    ]
  })

  it('a signed_in provider with models appears in the picker and its model is selectable', async () => {
    providerFixtures.list = [
      {
        id: 'openai-chatgpt',
        name: 'openai-chatgpt',
        display_name: 'ChatGPT',
        status: 'signed_in',
        models: ['gpt-5.6', 'gpt-5.6-sol'],
      },
    ]
    renderPicker()
    // RED pre-fix: with the signed_in provider filtered out the catalogue is
    // empty, so ModelSelector renders its disabled empty-catalogue placeholder
    // ("Connect a provider to pick a model") and never becomes a combobox —
    // the exact dead end the founder hit.
    await waitForLoadedTrigger()

    act(() => { useUiStore.getState().setModelSelectorOpen(true) })
    const item = await vi.waitFor(() => screen.getByText('gpt-5.6-sol'))
    fireEvent.click(item)
    await vi.waitFor(() => {
      expect(useChatStore.getState().nextModel).toBe('gpt-5.6-sol')
    })
  })

  it('an expired provider does NOT appear — a lapsed session cannot serve a model', async () => {
    providerFixtures.list = [
      {
        id: 'openai-chatgpt',
        name: 'openai-chatgpt',
        display_name: 'ChatGPT',
        status: 'expired',
        models: ['gpt-5.6'],
      },
    ]
    renderPicker()
    // Boundary pin: the usable rule is {connected, signed_in}, not "anything
    // but disconnected". The expired row is filtered out, so the picker falls
    // to the same disabled empty-catalogue placeholder — and the model text
    // is nowhere in the DOM.
    await vi.waitFor(() => {
      expect(screen.getByText('Connect a provider to pick a model')).toBeInTheDocument()
    })
    expect(screen.queryByText('gpt-5.6')).not.toBeInTheDocument()
  })
})

// ── H6 (provider hotfix 2026-10-13) — (provider, model) selection identity ─────
//
// The founder's live instance listed the SAME model id (gpt-6.1-sol) under
// TWO providers — the custom local proxy `grok-local` (a local proxy that
// serves both Grok and ChatGPT models; the dual listing itself is fine) and
// the subscription row `openai-chatgpt` — and picking it under ONE showed it
// SELECTED UNDER BOTH, because selection was keyed by model id only. The
// picker now records WHICH provider row the pick was made under and shows
// the check on that row alone.

const DUAL_PROVIDER_LIST = [
  {
    id: 'grok-local',
    name: 'grok-local',
    display_name: 'grok-local',
    status: 'connected',
    models: ['gpt-6.1-sol', 'grok-4'],
  },
  {
    id: 'openai-chatgpt',
    name: 'openai-chatgpt',
    display_name: 'ChatGPT (subscription)',
    status: 'signed_in',
    models: ['gpt-6.1-sol', 'gpt-5.6'],
  },
] as Array<Record<string, unknown>>

/** Both grouped rows for one model slug, in provider-fixture order.
 *  Options only — the trigger button also carries the value text once a
 *  value is set, and it is not a row. */
const dualProviderRows = (model: string) =>
  screen.getAllByRole('option').filter((el) => (el.textContent ?? '').includes(model))

describe('ModelPicker — H6 same model under two providers: only the picked provider is selected', () => {
  beforeEach(() => {
    providerFixtures.list = DUAL_PROVIDER_LIST
  })

  it('picking the shared slug under openai-chatgpt records THAT provider and marks only its row', async () => {
    renderPicker()
    await waitForLoadedTrigger()

    act(() => { useUiStore.getState().setModelSelectorOpen(true) })
    await vi.waitFor(() => {
      expect(
        screen.getAllByRole('option').filter((el) => (el.textContent ?? '').includes('gpt-6.1-sol')),
      ).toHaveLength(2)
    })
    const pickRows = screen.getAllByRole('option').filter((el) => (el.textContent ?? '').includes('gpt-6.1-sol'))
    // Click the SECOND row — openai-chatgpt's (fixture order).
    fireEvent.click(pickRows[1])

    // The pick carries the provider into the store — the (provider, model)
    // pair, not just the slug. (The wire frame still carries model_name
    // only today — MessageFrame.metadata has no provider field; see the
    // FR-010 contract tests in ChatScreen.test.tsx.)
    await vi.waitFor(() => {
      expect(useChatStore.getState().nextModel).toBe('gpt-6.1-sol')
      expect(useChatStore.getState().nextModelProvider).toBe('openai-chatgpt')
    })

    // Reopen: exactly the openai-chatgpt row shows the check.
    act(() => { useUiStore.getState().setModelSelectorOpen(true) })
    await vi.waitFor(() => screen.getAllByText('gpt-6.1-sol'))
    const rows = dualProviderRows('gpt-6.1-sol')
    expect(rows).toHaveLength(2)
    expect(rows[0]).not.toHaveAttribute('data-chosen') // grok-local's row
    expect(rows[1]).toHaveAttribute('data-chosen') // openai-chatgpt's row
  })

  it('picking the shared slug under grok-local marks only grok-local\'s row (the pair decides, not the slug)', async () => {
    renderPicker()
    await waitForLoadedTrigger()

    act(() => { useUiStore.getState().setModelSelectorOpen(true) })
    const pickRows = await vi.waitFor(() =>
      screen.getAllByRole('option').filter((el) => (el.textContent ?? '').includes('gpt-6.1-sol')),
    )
    expect(pickRows).toHaveLength(2)
    fireEvent.click(pickRows[0]) // grok-local's row
    await vi.waitFor(() => {
      expect(useChatStore.getState().nextModelProvider).toBe('grok-local')
    })

    act(() => { useUiStore.getState().setModelSelectorOpen(true) })
    await vi.waitFor(() => screen.getAllByText('gpt-6.1-sol'))
    const rows = dualProviderRows('gpt-6.1-sol')
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveAttribute('data-chosen') // grok-local's row
    expect(rows[1]).not.toHaveAttribute('data-chosen') // openai-chatgpt's row
  })

  it('a SEEDED value offered by TWO providers shows NO chosen row — list order is not a recorded choice', async () => {
    act(() => {
      useChatStore.setState({ messages: [assistantMsg('m1', 'gpt-6.1-sol')] })
    })
    renderPicker()
    await waitForLoadedTrigger()
    await vi.waitFor(() => {
      expect(useChatStore.getState().nextModel).toBe('gpt-6.1-sol')
      expect(useChatStore.getState().nextModelProvider).toBeNull()
    })

    act(() => { useUiStore.getState().setModelSelectorOpen(true) })
    await vi.waitFor(() => screen.getAllByText('gpt-6.1-sol'))
    const rows = dualProviderRows('gpt-6.1-sol')
    expect(rows).toHaveLength(2)
    // H6 R2 (review round 1 — F3): a provider-less seed resolves a row only
    // when EXACTLY ONE provider serves the slug. Two providers → ambiguous →
    // no row chosen. The old first-wins lookup presented list order (grok-
    // local first) as if it were the provider that served the last turn.
    expect(rows[0]).not.toHaveAttribute('data-chosen')
    expect(rows[1]).not.toHaveAttribute('data-chosen')
  })

  it('a SEEDED value offered by exactly ONE provider marks that provider\'s row', async () => {
    act(() => {
      useChatStore.setState({ messages: [assistantMsg('m1', 'grok-4')] })
    })
    renderPicker()
    await waitForLoadedTrigger()
    await vi.waitFor(() => {
      expect(useChatStore.getState().nextModel).toBe('grok-4')
    })

    act(() => { useUiStore.getState().setModelSelectorOpen(true) })
    await vi.waitFor(() => screen.getAllByText('grok-4'))
    const rows = dualProviderRows('grok-4')
    expect(rows).toHaveLength(1)
    expect(rows[0]).toHaveAttribute('data-chosen')
  })

  it('an AGENT-DEFAULT seed carries the agent\'s own provider — the check lands on the agent\'s provider row, not the first listing', async () => {
    // The agent response has an explicit `provider` field; the seed must
    // pass it through (H6 R2 F3). Mirror the reviewed scenario: agent data
    // already loaded, then navigate to an EMPTY conversation — the seed
    // comes from the agent default, transcript-less.
    vi.mocked(fetchAgents).mockResolvedValue([
      {
        figure: 'Omnipus', role: 'general',
        id: 'mia',
        name: 'Mia',
        type: 'core',
        status: 'active',
        model: 'gpt-6.1-sol',
        provider: 'openai-chatgpt',
        description: 'Assistant',
      },
    ] as never)
    renderPicker()
    await waitForLoadedTrigger() // agents + providers queries settled

    act(() => {
      useSessionStore.setState({ activeSessionId: 'sess_empty', activeAgentId: 'mia' })
    })
    await vi.waitFor(() => {
      expect(useChatStore.getState().nextModel).toBe('gpt-6.1-sol')
      expect(useChatStore.getState().nextModelProvider).toBe('openai-chatgpt')
    })

    act(() => { useUiStore.getState().setModelSelectorOpen(true) })
    await vi.waitFor(() => screen.getAllByText('gpt-6.1-sol'))
    const rows = dualProviderRows('gpt-6.1-sol')
    expect(rows).toHaveLength(2)
    expect(rows[0]).not.toHaveAttribute('data-chosen') // grok-local — first listing, NOT the agent's provider
    expect(rows[1]).toHaveAttribute('data-chosen') // openai-chatgpt — the agent's own provider
  })

  it('navigating to a different session re-seeds and releases the picked provider', async () => {
    renderPicker()
    await waitForLoadedTrigger()

    act(() => { useUiStore.getState().setModelSelectorOpen(true) })
    const pickRows = await vi.waitFor(() =>
      screen.getAllByRole('option').filter((el) => (el.textContent ?? '').includes('gpt-6.1-sol')),
    )
    fireEvent.click(pickRows[1])
    await vi.waitFor(() => {
      expect(useChatStore.getState().nextModelProvider).toBe('openai-chatgpt')
    })

    // Genuine navigation: a different session whose transcript seeds a
    // different model — the pick (and its provider) was scoped to the
    // conversation it was made in.
    act(() => {
      useSessionStore.setState({ activeSessionId: 'sess_2', activeAgentId: 'mia' })
      useChatStore.setState({ messages: [assistantMsg('m2', 'grok-4')] })
    })
    await vi.waitFor(() => {
      expect(useChatStore.getState().nextModel).toBe('grok-4')
      expect(useChatStore.getState().nextModelProvider).toBeNull()
    })
  })
})
