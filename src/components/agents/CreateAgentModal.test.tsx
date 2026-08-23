// CreateAgentModal — drives the 3-step CreateAgentWizard via the prop API.
//
// W4 of agent-form-requirements: the legacy 2-tab modal was rewritten as a
// 3-step + Advanced wizard. This file covers the wizard's prop-driven
// surface (open / onClose / onCreate / initialType) — the spec's BDD
// scenarios live in the spec doc and the CreateAgentWizard.test.tsx
// sub-component tests.
//
// Notes on what's covered vs. what's deferred:
//   - Skills picker (US-E6) and Executor editor (Spec-4) live in the
//     "Advanced" step which is deferred per the wizard file's comment.
//     Tests for those features are marked .skip with a tracked reason
//     and will be re-enabled when the Advanced step lands.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import * as React from 'react'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { CreateAgentModal } from './CreateAgentModal'
import { useUiStore } from '@/store/ui'
import { createAgent, fetchProviders, fetchRegistryTools, fetchSkills, ApiError } from '@/lib/api'
import type { RegistryTool, Skill } from '@/lib/api'
import type { Provider } from '@/lib/api/generated/openapi-types'
import { applyRolePreset } from '@/lib/toolPolicyPresets'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchProviders: vi.fn().mockResolvedValue([]),
    fetchRegistryTools: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
    createAgent: vi.fn(),
  }
})

// The ModelSelector is now a CONSTRAINED dropdown (UAT model-catalog fix):
// with the mocked empty provider list it renders a disabled "no models"
// state (no <input>). These modal tests fill the model via the trigger test
// id to exercise the wire-payload plumbing, so stub the selector with a plain
// forwarding <input>. Picker mechanics are covered by model-selector.test.tsx.
// The primary element STAYS the plain forwarding <input> the pre-existing
// tests already drive via `fireEvent.change(screen.getByTestId('wizard-model'), ...)`
// — only new, additive attributes/siblings are appended so this file can
// also assert CreateAgentModal's `providersQuery` states actually reach the
// picker (CreateAgentModal → CreateAgentWizard → Step1Identity →
// ModelSelector) end to end, without re-testing the picker's own render
// logic (covered by model-selector.test.tsx and Step1Identity.test.tsx).
vi.mock('@/components/ui/model-selector', () => ({
  ModelSelector: ({
    value,
    onChange,
    triggerTestId,
    catalogStatus,
    catalogErrorMessage,
    emptyCatalogHint,
    onRetryCatalog,
  }: {
    value: string
    onChange: (v: string) => void
    triggerTestId?: string
    catalogStatus?: 'loading' | 'error' | 'ready'
    catalogErrorMessage?: string
    emptyCatalogHint?: string
    onRetryCatalog?: () => void
  }) => {
    const testId = triggerTestId ?? 'model-selector'
    return (
      <>
        <input
          data-testid={testId}
          data-catalog-status={catalogStatus ?? 'ready'}
          value={value}
          onChange={(e) => onChange(e.target.value)}
        />
        {catalogStatus === 'error' && (
          <span data-testid={`${testId}-error-message`}>{catalogErrorMessage}</span>
        )}
        {catalogStatus !== 'error' && catalogStatus !== 'loading' && (
          <span data-testid={`${testId}-empty-hint`}>{emptyCatalogHint}</span>
        )}
        {onRetryCatalog && (
          <button type="button" data-testid={`${testId}-retry`} onClick={onRetryCatalog}>
            Retry
          </button>
        )}
      </>
    )
  },
}))

// Advanced.tsx renders a TanStack Router <Link> to the delegation-trust
// page. Real Link needs a RouterProvider; stub it to a plain anchor so the
// wizard can render in isolation.
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    Link: ({ children, to, ...rest }: { children?: React.ReactNode; to?: string } & Record<string, unknown>) => (
      <a href={typeof to === 'string' ? to : '#'} {...(rest as Record<string, unknown>)}>
        {children}
      </a>
    ),
  }
})

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderModal(props: Parameters<typeof CreateAgentModal>[0] = {}) {
  const client = makeClient()
  const utils = render(
    <QueryClientProvider client={client}>
      <CreateAgentModal {...props} />
    </QueryClientProvider>,
  )
  // Expose the QueryClient alongside the render utils so tests can inspect
  // the mutation cache directly (e.g. to pin a mutation's *resolved value*,
  // not just its externally-visible side effects like toasts).
  return { ...utils, client }
}

/** Drive the wizard through steps ① → ② → ③ with valid minimal inputs
 *  for a Main agent. Returns a promise that resolves when step 3 is
 *  visible. The caller can then click "Create agent". */
async function fillAndAdvanceToStep3(opts?: { initialType?: 'Main' | 'Subagent' | 'subagent_3p'; cliPath?: string; cliArgs?: string }) {
  // Step ① — Identity. Name + model required; description also required
  // for workers (Subagent / subagent_3p). External agents also require a
  // non-empty CLI path before the step is considered valid.
  fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'Research Bot' } })
  const type = opts?.initialType ?? 'Main'
  // UAT 4a: a native Subagent defaults to inheriting its model — turn the
  // toggle OFF so the model picker appears and we can set an explicit model
  // (these tests exercise the explicit-model path).
  if (type === 'Subagent' && screen.queryByTestId('wizard-inherit-model')) {
    const t = screen.getByTestId('wizard-inherit-model') as HTMLInputElement
    if (t.checked) fireEvent.click(t)
  }
  fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'claude-sonnet-4-6' } })
  if (type !== 'Main') {
    fireEvent.change(screen.getByTestId('wizard-description'), {
      target: { value: 'A research sub-agent' },
    })
  }
  if (type === 'subagent_3p') {
    if (screen.queryByTestId('wizard-cli-chooser')) {
      fireEvent.click(screen.getByTestId('wizard-cli-claude-code'))
    }
    fireEvent.change(screen.getByTestId('wizard-cli-path'), {
      target: { value: opts?.cliPath ?? '/usr/local/bin/claude-code' },
    })
    if (opts?.cliArgs) {
      fireEvent.change(screen.getByTestId('wizard-cli-args'), {
        target: { value: opts.cliArgs },
      })
    }
  }
  await waitFor(() => {
    expect(screen.getByTestId('wizard-next-1')).not.toBeDisabled()
  })
  fireEvent.click(screen.getByTestId('wizard-next-1'))

  // Step ② — Personality. Soul required for all three types.
  fireEvent.change(screen.getByTestId('wizard-soul'), {
    target: { value: 'You are a focused research assistant.' },
  })

  // subagent_3p is a TWO-step wizard (2026-07-03): tools/skills/fallback
  // never apply to an external runner and its old step ③ only duplicated
  // step ①'s runner config — Create lives on step ② for that type.
  if (type === 'subagent_3p') {
    expect(await screen.findByTestId('wizard-create')).toBeInTheDocument()
    return
  }

  await waitFor(() => {
    expect(screen.getByTestId('wizard-next-2')).not.toBeDisabled()
  })
  fireEvent.click(screen.getByTestId('wizard-next-2'))

  // Step ③ — Tools. UAT 4a: a native Subagent defaults to inheriting Tools —
  // turn the toggle OFF so the per-tool editor (wizard-tools-cfg) is shown.
  if (type === 'Subagent' && screen.queryByTestId('wizard-inherit-tools')) {
    const t = screen.getByTestId('wizard-inherit-tools') as HTMLInputElement
    if (t.checked) fireEvent.click(t)
  }
  // All fields are wire-optional for Main / Subagent. Just confirm the step renders.
  expect(await screen.findByTestId('wizard-create')).toBeInTheDocument()
}

beforeEach(() => {
  act(() => {
    useUiStore.setState({
      createAgentModalOpen: false,
      toasts: [],
    })
  })
  vi.mocked(createAgent).mockReset()
  // S8: reset fetchSkills between tests — individual tests override it with
  // mockResolvedValue([...]), and without a reset the prior resolved value
  // leaks into the next test (the "default none" AC1 test would see the
  // previous test's skills list).
  vi.mocked(fetchSkills).mockReset().mockResolvedValue([])
  // Same leak risk for fetchRegistryTools — the tools_cfg-default tests below
  // override it with a real tool catalog; reset back to [] so unrelated
  // tests (which assume no tools) are not affected.
  vi.mocked(fetchRegistryTools).mockReset().mockResolvedValue([])
  // Same leak risk for fetchProviders — the four-state provider-catalog
  // tests below override it with pending/rejected/warned-provider results;
  // reset back to the "no provider connected" default so unrelated tests
  // are unaffected.
  vi.mocked(fetchProviders).mockReset().mockResolvedValue([])
})

describe('CreateAgentModal — prop-driven opening', () => {
  it('renders nothing when closed', () => {
    renderModal({ open: false, onClose: vi.fn() })
    expect(screen.queryByTestId('wizard-stepper')).toBeNull()
  })

  it('renders the wizard when open via prop', () => {
    renderModal({ open: true, onClose: vi.fn() })
    // The type chip is the wizard's mount signal — present for every type.
    expect(screen.getByTestId('type-chip')).toBeInTheDocument()
    expect(screen.getByTestId('wizard-stepper')).toBeInTheDocument()
  })

  it('renders the wizard when createAgentModalOpen is true in store', () => {
    act(() => {
      useUiStore.setState({ createAgentModalOpen: true })
    })
    renderModal()
    expect(screen.getByTestId('type-chip')).toBeInTheDocument()
  })

  it('uses initialType=Main by default (covers both prop-only and store paths)', () => {
    renderModal({ open: true, onClose: vi.fn() })
    expect(screen.getByTestId('type-chip')).toHaveTextContent(/^Main$/)
  })
})

describe('CreateAgentModal — type chip behavior (spec §11 #3, §9.3)', () => {
  it('initialType=Subagent renders the Subagent type chip', () => {
    renderModal({ open: true, onClose: vi.fn(), initialType: 'Subagent' })
    expect(screen.getByTestId('type-chip')).toHaveTextContent(/^Subagent$/)
  })

  it('initialType=subagent_3p renders the External type chip', () => {
    renderModal({ open: true, onClose: vi.fn(), initialType: 'subagent_3p' })
    expect(screen.getByTestId('type-chip')).toHaveTextContent(/Subagent \(External\)/)
  })

  it('initialType=subagent_3p with initialCli renders the locked CLI chip', () => {
    renderModal({
      open: true,
      onClose: vi.fn(),
      initialType: 'subagent_3p',
      initialCli: 'claude-code',
    })
    expect(screen.getByTestId('cli-chip')).toHaveTextContent(/claude-code/)
  })

  it('initialType=Main does NOT render a CLI chip (CLI is external-only)', () => {
    renderModal({ open: true, onClose: vi.fn(), initialType: 'Main' })
    expect(screen.queryByTestId('cli-chip')).toBeNull()
  })

  it('the [×] on the type chip closes the wizard (no confirmation, per spec §11 #3)', () => {
    const onClose = vi.fn()
    renderModal({ open: true, onClose })
    fireEvent.click(screen.getByRole('button', { name: /cancel wizard/i }))
    expect(onClose).toHaveBeenCalledOnce()
  })
})

describe('CreateAgentModal — backward-compat: legacy custom/worker literals', () => {
  it('maps initialType=custom to Main wire enum', () => {
    renderModal({ open: true, onClose: vi.fn(), initialType: 'custom' })
    expect(screen.getByTestId('type-chip')).toHaveTextContent(/^Main$/)
  })

  it('maps initialType=worker to Subagent wire enum', () => {
    renderModal({ open: true, onClose: vi.fn(), initialType: 'worker' })
    expect(screen.getByTestId('type-chip')).toHaveTextContent(/^Subagent$/)
  })

  it('store type=worker (legacy) maps to Subagent when no prop is given', () => {
    act(() => {
      useUiStore.setState({ createAgentModalOpen: true, createAgentModalType: 'worker' as unknown as 'Subagent' })
    })
    renderModal()
    expect(screen.getByTestId('type-chip')).toHaveTextContent(/^Subagent$/)
  })
})

describe('CreateAgentModal — step ① Identity (spec §5.3-§5.5)', () => {
  it('renders the wizard-name, wizard-description, wizard-model inputs', () => {
    renderModal({ open: true, onClose: vi.fn() })
    expect(screen.getByTestId('wizard-name')).toBeInTheDocument()
    expect(screen.getByTestId('wizard-description')).toBeInTheDocument()
    expect(screen.getByTestId('wizard-model')).toBeInTheDocument()
  })

  it('disables Next until name + model are filled (Main type)', async () => {
    renderModal({ open: true, onClose: vi.fn() })
    const next = screen.getByTestId('wizard-next-1')
    expect(next).toBeDisabled()
    // Name alone is not enough — model is also required.
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'Foo' } })
    expect(next).toBeDisabled()
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    await waitFor(() => expect(next).not.toBeDisabled())
    // Clearing the name re-disables the button.
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: '' } })
    await waitFor(() => expect(next).toBeDisabled())
  })

  it('also requires description for Subagent before Next is enabled', async () => {
    // Inherit toggles default OFF (UAT e2e fix), so a native Subagent's step 1
    // is gated on name + model + description.
    renderModal({ open: true, onClose: vi.fn(), initialType: 'Subagent' })
    const next = screen.getByTestId('wizard-next-1')
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'Sub' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    // Description still empty → Next disabled.
    expect(next).toBeDisabled()
    fireEvent.change(screen.getByTestId('wizard-description'), { target: { value: 'd' } })
    await waitFor(() => expect(next).not.toBeDisabled())
  })
})

describe('CreateAgentModal — step ② Personality (spec §5.3-§5.5)', () => {
  it('renders wizard-soul for all three types', async () => {
    renderModal({ open: true, onClose: vi.fn() })
    // Step 1 gating: fill name + model first so Next is enabled.
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'X' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    expect(await screen.findByTestId('wizard-soul')).toBeInTheDocument()
  })

  it('disables Next on step 2 until soul is non-empty', async () => {
    renderModal({ open: true, onClose: vi.fn() })
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'A' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    const next = await screen.findByTestId('wizard-next-2')
    expect(next).toBeDisabled()
    fireEvent.change(screen.getByTestId('wizard-soul'), { target: { value: 'hi' } })
    await waitFor(() => expect(next).not.toBeDisabled())
  })

  it('Subagent wizard does NOT show Heartbeat or Voice (Main-only per spec §3.1 rows 10-13)', async () => {
    renderModal({ open: true, onClose: vi.fn(), initialType: 'Subagent' })
    // Inherit toggles default OFF — fill name + model + description to reach step 2.
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'W' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    fireEvent.change(screen.getByTestId('wizard-description'), { target: { value: 'd' } })
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    expect(await screen.findByTestId('wizard-soul')).toBeInTheDocument()
    // FR-017: heartbeat is now workspace-scoped — removed from the create wizard.
    expect(screen.queryByTestId('wizard-heartbeat')).toBeNull()
    expect(screen.queryByTestId('wizard-voice')).toBeNull()
  })

  it('Main wizard shows Voice but NOT Heartbeat (heartbeat is workspace-scoped per FR-017 / US-5.AC3)', async () => {
    // FR-017: heartbeat has been removed from the create wizard (workspace-scoped now).
    // Voice is still Main-only. Soul upload control is present (FR-026 / US-10).
    renderModal({ open: true, onClose: vi.fn(), initialType: 'Main' })
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'A' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    expect(await screen.findByTestId('wizard-soul')).toBeInTheDocument()
    // Heartbeat MUST be absent — it's configured per workspace, not at create time.
    expect(screen.queryByTestId('wizard-heartbeat')).toBeNull()
    // Voice is still present for Main agents.
    expect(screen.getByTestId('wizard-voice')).toBeInTheDocument()
    // FR-026 / US-10: soul upload button present.
    expect(screen.getByTestId('wizard-soul-upload')).toBeInTheDocument()
  })
})

describe('CreateAgentModal — step ③ Tools (spec §5.3-§5.5)', () => {
  it('Main wizard shows wizard-tools-cfg', async () => {
    renderModal({ open: true, onClose: vi.fn(), initialType: 'Main' })
    await fillAndAdvanceToStep3({ initialType: 'Main' })
    expect(screen.getByTestId('wizard-tools-cfg')).toBeInTheDocument()
  })

  it('Subagent wizard shows wizard-tools-cfg', async () => {
    renderModal({ open: true, onClose: vi.fn(), initialType: 'Subagent' })
    await fillAndAdvanceToStep3({ initialType: 'Subagent' })
    expect(screen.getByTestId('wizard-tools-cfg')).toBeInTheDocument()
  })

  it('subagent_3p wizard does NOT show wizard-tools-cfg (spec §3.1 row 14)', async () => {
    renderModal({ open: true, onClose: vi.fn(), initialType: 'subagent_3p' })
    await fillAndAdvanceToStep3({ initialType: 'subagent_3p' })
    expect(screen.queryByTestId('wizard-tools-cfg')).toBeNull()
  })
})

describe('CreateAgentModal — submit success path', () => {
  it('calls onCreate with the wire payload (name, type, model, soul)', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    const onClose = vi.fn()
    renderModal({ open: true, onClose, onCreate })
    await fillAndAdvanceToStep3()
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => {
      expect(onCreate).toHaveBeenCalledWith(
        expect.objectContaining({
          type: 'Main',
          name: 'Research Bot',
          model: 'claude-sonnet-4-6',
          soul: 'You are a focused research assistant.',
        }),
      )
    })
  })

  // Regression: mutationFn used to cast the *request* payload to the
  // response `Agent` type (`data as unknown as Agent`) when onCreateProp
  // resolves to void, so onSuccess could silently read server-computed
  // fields (id, timestamps, resolved defaults) off a value that never had
  // them. mutationFn now resolves `null` for this path and onSuccess falls
  // back to the submitted request's `name` — this pins that the success
  // side effects (toast + close) still fire correctly with no synthesized
  // entity in play.
  it('legacy onCreate-resolves-to-void path: shows the success toast from the request name (not a synthesized Agent) and closes the modal', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    const onClose = vi.fn()
    const { client } = renderModal({ open: true, onClose, onCreate })
    await fillAndAdvanceToStep3()
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
    const toasts = useUiStore.getState().toasts
    expect(toasts).toHaveLength(1)
    expect(toasts[0]).toMatchObject({
      message: 'Created agent "Research Bot"',
      variant: 'success',
    })

    // The toast-text assertion above is byte-identical whether the fix is
    // applied or not: AgentCreateRequestMain.name is required, and the OLD
    // buggy `data as unknown as Agent` cast made `agent.name` resolve to
    // `data.name` — the exact same value the NEW code's `variables.name`
    // fallback produces. It does NOT pin the actual defect (reading a
    // server-computed field off a request-shaped object).
    //
    // What DOES distinguish the fix from the bug: the mutation's *resolved
    // value* itself. Post-fix, mutationFn resolves `null` for this legacy
    // onCreateProp path (see CreateAgentModal.tsx's createAgentMutation
    // comment). Pre-fix, it would have resolved the request payload cast to
    // Agent — a truthy, `Research Bot`-shaped object, not null. Inspect the
    // settled mutation directly via the QueryClient's mutation cache rather
    // than only observing the onSuccess side effects.
    const mutations = client.getMutationCache().getAll()
    expect(mutations).toHaveLength(1)
    expect(mutations[0].state.data).toBeNull()
  })

  it('Subagent submit includes description in the wire payload', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate, initialType: 'Subagent' })
    await fillAndAdvanceToStep3({ initialType: 'Subagent' })
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => {
      expect(onCreate).toHaveBeenCalledWith(
        expect.objectContaining({
          type: 'Subagent',
          name: 'Research Bot',
          description: 'A research sub-agent',
        }),
      )
    })
  })

  it('trims name / model / soul before submission (matches step gating)', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate })
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: '  Padded  ' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: '  claude-sonnet-4-6  ' } })
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    fireEvent.change(screen.getByTestId('wizard-soul'), { target: { value: '  You are concise.  ' } })
    fireEvent.click(screen.getByTestId('wizard-next-2'))
    fireEvent.click(await screen.findByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call.name).toBe('Padded')
    expect(call.model).toBe('claude-sonnet-4-6')
    expect(call.soul).toBe('You are concise.')
  })

  it('sends a hex color (NOT a semantic name) to the wire — wire requires /^#[0-9A-Fa-f]{6}$/', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate })
    await fillAndAdvanceToStep3()
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call.color).toMatch(/^#[0-9A-Fa-f]{6}$/)
  })

  it('Subagent submit sends type=Subagent (the wire enum, not the legacy "worker")', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate, initialType: 'Subagent' })
    await fillAndAdvanceToStep3({ initialType: 'Subagent' })
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call.type).toBe('Subagent')
  })

  it('subagent_3p submit omits description when empty and sends type=subagent_3p', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({
      open: true,
      onClose: vi.fn(),
      onCreate,
      initialType: 'subagent_3p',
      initialCli: 'claude-code',
    })
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'External' } })
    fireEvent.change(screen.getByTestId('wizard-description'), { target: { value: 'Runs in claude-code' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'claude-sonnet-4-6' } })
    fireEvent.change(screen.getByTestId('wizard-cli-path'), { target: { value: '/usr/local/bin/claude-code' } })
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    fireEvent.change(screen.getByTestId('wizard-soul'), { target: { value: 'hi' } })
    // Two-step wizard for external runners (2026-07-03): Create is on step 2.
    fireEvent.click(await screen.findByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call.type).toBe('subagent_3p')
    expect(call.description).toBe('Runs in claude-code')
  })
})

// ── tools_cfg commit-on-submit default (untouched Tools step) ───────────────
//
// Regression coverage for the bug where Step3Tools.tsx's toolPolicyValue()
// DISPLAYS a Balanced-preset default (applyRolePreset('balanced', tools))
// before the operator interacts with ToolPolicyEditor, but
// payloadToCreateRequest only forwarded payload.tools_cfg when it was
// defined — i.e. only after a genuine ToolPolicyEditor onChange. Submitting
// with the Tools step untouched therefore omitted tools_cfg entirely, and
// the backend's NewCustomAgentToolsCfg() seed (deny-everything except a
// narrow read-only allow-list) applied instead of the Balanced policy the
// wizard had shown. The fix commits the SAME displayed default into the
// request at submit time when payload.tools_cfg is still undefined.
describe('CreateAgentModal — tools_cfg commit-on-submit default (untouched Tools step)', () => {
  // Includes browser_navigate / browser_evaluate — a real gap in prior
  // coverage: the Balanced preset's browser-tool overrides were keyed with
  // wrong (dotted, non-matching) names until 2026-07-11, and this catalog
  // previously had NO browser tools at all, so the commit-on-submit test
  // suite would have stayed green while shipping that defect (it silently
  // committed 'allow' for browser tools instead of the displayed ask/deny).
  const CATALOG: RegistryTool[] = [
    { name: 'bash', description: 'bash', scope: 'general', category: 'core', source: 'builtin' },
    { name: 'read_file', description: 'read_file', scope: 'general', category: 'core', source: 'builtin' },
    { name: 'write_file', description: 'write_file', scope: 'general', category: 'core', source: 'builtin' },
    { name: 'web_search', description: 'web_search', scope: 'general', category: 'core', source: 'builtin' },
    { name: 'browser_navigate', description: 'browser_navigate', scope: 'general', category: 'browser', source: 'builtin' },
    { name: 'browser_evaluate', description: 'browser_evaluate', scope: 'general', category: 'browser', source: 'builtin' },
  ]

  it('Main: submitting without touching Step 3 commits the Balanced-preset tools_cfg (not an omitted field)', async () => {
    vi.mocked(fetchRegistryTools).mockResolvedValue(CATALOG)
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate, initialType: 'Main' })
    await fillAndAdvanceToStep3({ initialType: 'Main' })
    // Deliberately do NOT touch the ToolPolicyEditor (no preset click, no
    // per-tool toggle) — go straight to Create.
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call.tools_cfg).toBeDefined()
    expect(call.tools_cfg).toEqual({ builtin: applyRolePreset('balanced', CATALOG) })
    // Spot-check the exact policy values the wizard displayed (§2.1 Balanced
    // overrides): bash=ask, write_file=ask, browser_navigate=ask,
    // browser_evaluate=deny, everything else allow. The browser-tool
    // assertions are the direct regression check for the wrong-override-key
    // bug — before the fix these silently committed 'allow'.
    expect(call.tools_cfg.builtin.policies.bash).toBe('ask')
    expect(call.tools_cfg.builtin.policies.write_file).toBe('ask')
    expect(call.tools_cfg.builtin.policies.read_file).toBe('allow')
    expect(call.tools_cfg.builtin.policies.web_search).toBe('allow')
    expect(call.tools_cfg.builtin.policies.browser_navigate).toBe('ask')
    expect(call.tools_cfg.builtin.policies.browser_evaluate).toBe('deny')
  })

  it('Subagent (not inheriting tools): submitting without touching Step 3 commits the Balanced-preset tools_cfg', async () => {
    vi.mocked(fetchRegistryTools).mockResolvedValue(CATALOG)
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate, initialType: 'Subagent' })
    await fillAndAdvanceToStep3({ initialType: 'Subagent' })
    // fillAndAdvanceToStep3 already flips wizard-inherit-tools OFF for
    // Subagent so the per-tool editor renders; still never touched.
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call.tools_cfg).toEqual({ builtin: applyRolePreset('balanced', CATALOG) })
  })

  it('Main: an explicitly-touched Tools step still forwards the user-chosen policy verbatim (no override)', async () => {
    vi.mocked(fetchRegistryTools).mockResolvedValue(CATALOG)
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate, initialType: 'Main' })
    await fillAndAdvanceToStep3({ initialType: 'Main' })
    // Genuinely interact with the editor: pick the Cautious preset.
    fireEvent.click(screen.getByTestId('preset-cautious'))
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call.tools_cfg).toEqual({ builtin: applyRolePreset('cautious', CATALOG) })
  })

  it('subagent_3p never carries tools_cfg — the variant schema forbids it (additionalProperties: false)', async () => {
    vi.mocked(fetchRegistryTools).mockResolvedValue(CATALOG)
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({
      open: true,
      onClose: vi.fn(),
      onCreate,
      initialType: 'subagent_3p',
      initialCli: 'claude-code',
    })
    await fillAndAdvanceToStep3({ initialType: 'subagent_3p', cliPath: '/usr/local/bin/claude-code' })
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call).not.toHaveProperty('tools_cfg')
  })

  // Regression coverage for the empty/unresolved-registry race (5 independent
  // reviewers flagged this on the first pass of this fix): registryTools =
  // toolsQuery.data ?? [] has no loading/error gate, so submitting while the
  // /tools query is still empty (unresolved, or — degenerately — a genuinely
  // tool-less catalog) must NOT commit an explicit-but-empty
  // `{ builtin: { policies: {} } }`, which would trip the backend's
  // full-coverage validation and surface a confusing 400. It must fall back
  // to omitting tools_cfg — the same as the pre-existing (pre-default-commit)
  // behavior for this one degenerate case. resolveToolsCfg (@/lib/
  // toolPolicyPresets) encodes this guard.
  it('Main: an empty/unresolved tool registry omits tools_cfg rather than committing an empty policies map', async () => {
    // fetchRegistryTools resolves to [] here (the beforeEach default) —
    // simulating the query never having populated a catalog by submit time.
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate, initialType: 'Main' })
    await fillAndAdvanceToStep3({ initialType: 'Main' })
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call).not.toHaveProperty('tools_cfg')
  })

  it('Subagent (not inheriting tools): an empty/unresolved tool registry omits tools_cfg rather than committing an empty policies map', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate, initialType: 'Subagent' })
    await fillAndAdvanceToStep3({ initialType: 'Subagent' })
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call).not.toHaveProperty('tools_cfg')
  })
})

describe('CreateAgentModal — submit error path', () => {
  it('surfaces the error inline (wizard-submit-error) and keeps the modal open when onCreate rejects', async () => {
    const onCreate = vi.fn().mockRejectedValue(new Error('Server rejected agent create'))
    renderModal({ open: true, onClose: vi.fn(), onCreate })
    await fillAndAdvanceToStep3()
    fireEvent.click(screen.getByTestId('wizard-create'))
    const err = await screen.findByTestId('wizard-submit-error')
    expect(err).toHaveTextContent(/server rejected/i)
    expect(screen.getByTestId('wizard-stepper')).toBeInTheDocument()
  })

  it('clears the error banner when the user goes back and re-edits a field', async () => {
    const onCreate = vi.fn().mockRejectedValue(new Error('Boom'))
    renderModal({ open: true, onClose: vi.fn(), onCreate })
    await fillAndAdvanceToStep3()
    fireEvent.click(screen.getByTestId('wizard-create'))
    await screen.findByTestId('wizard-submit-error')
    fireEvent.click(screen.getByTestId('wizard-back'))
    fireEvent.change(screen.getByTestId('wizard-soul'), { target: { value: 'rewritten' } })
    // Banner should be gone after the field edit.
    expect(screen.queryByTestId('wizard-submit-error')).toBeNull()
  })
})

// Traces to: wave2 findings-fix cycle — the top-level createAgentMutation's
// onError (CreateAgentModal.tsx ~L229-236) was dedup'd from a raw
// isApiError ternary into getErrorMessage() but had no test exercising it.
// The existing "submit error path" tests above only cover the wizard's own
// inline wizard-submit-error banner via the onCreate PROP override — they
// never drive the real wire-level createAgent() call this handler guards,
// and never assert on the toast this handler fires.
describe('CreateAgentModal — top-level create-mutation error toast (getErrorMessage migration)', () => {
  it('fires an error toast with the ApiError userMessage when the wire-level createAgent() call rejects (no onCreate override)', async () => {
    vi.mocked(createAgent).mockRejectedValue(
      new ApiError(409, 'An agent with this name already exists.', {
        body: '{"error":"agent name conflict: Research Bot"}',
      }),
    )
    // No onCreate prop — this drives the real createAgent() mutationFn
    // branch, not the legacy onCreateProp path the other error tests use.
    renderModal({ open: true, onClose: vi.fn() })
    await fillAndAdvanceToStep3()
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(createAgent).toHaveBeenCalled())

    await waitFor(() => {
      const toasts = useUiStore.getState().toasts
      expect(toasts).toContainEqual(
        expect.objectContaining({
          message: 'An agent with this name already exists.',
          variant: 'error',
        }),
      )
    })

    // The raw server body is debug-only (ApiError.body) and must never leak
    // into the user-facing toast — only userMessage may appear.
    const toasts = useUiStore.getState().toasts
    expect(toasts.some((t) => /agent name conflict/i.test(t.message))).toBe(false)
  })
})

describe('CreateAgentModal — Cancel button', () => {
  it('closes the modal when Cancel is clicked', () => {
    const onClose = vi.fn()
    renderModal({ open: true, onClose })
    fireEvent.click(screen.getByRole('button', { name: /^cancel$/i }))
    expect(onClose).toHaveBeenCalledOnce()
  })
})

// ── Advanced step (Skills picker, Executor editor, runtime knobs) ───────────

describe('CreateAgentModal — Skills picker (US-E6)', () => {
  it('new agent submits without skills when none are selected (AC1 — default none)', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate })
    await fillAndAdvanceToStep3()
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call.skills).toBeUndefined()
  })

  it('shows skills on Step 3 when installed skills are present', async () => {
    vi.mocked(fetchSkills).mockResolvedValue([
      { id: 'web-research', name: 'Web Research', version: '1.0.0', verified: false, status: 'active' } as Skill,
    ])
    renderModal({ open: true, onClose: vi.fn() })
    await fillAndAdvanceToStep3()
    expect(screen.getByTestId('wizard-skills')).toBeInTheDocument()
    expect(screen.getByTestId('skill-checkbox-web-research')).toBeInTheDocument()
  })

  it('includes selected skills in the onCreate payload', async () => {
    vi.mocked(fetchSkills).mockResolvedValue([
      { id: 'web-research', name: 'Web Research', version: '1.0.0', verified: false, status: 'active' } as Skill,
      { id: 'code-review', name: 'Code Review', version: '1.0.0', verified: false, status: 'active' } as Skill,
    ])
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate })
    await fillAndAdvanceToStep3()
    fireEvent.click(screen.getByTestId('skill-checkbox-web-research'))
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call.skills).toEqual(['web-research'])
  })

  it('does not include skills in payload when none are selected (AC2)', async () => {
    vi.mocked(fetchSkills).mockResolvedValue([
      { id: 'web-research', name: 'Web Research', version: '1.0.0', verified: false, status: 'active' } as Skill,
    ])
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate })
    await fillAndAdvanceToStep3()
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call.skills).toBeUndefined()
  })
})

describe('CreateAgentModal — Executor (Spec-4)', () => {
  it('omits executor when left on the native default', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate })
    await fillAndAdvanceToStep3()
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call.executor).toBeUndefined()
  })

  it('includes an external-cli executor in the create payload', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({
      open: true,
      onClose: vi.fn(),
      onCreate,
      initialType: 'subagent_3p',
      initialCli: 'claude-code',
    })
    await fillAndAdvanceToStep3({ initialType: 'subagent_3p', cliPath: '/opt/bin/claude-code', cliArgs: '--verbose' })
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call.executor).toEqual({
      kind: 'external-cli',
      cli: 'claude-code',
      cli_path: '/opt/bin/claude-code',
      cli_args: '--verbose',
    })
  })

  it('does not render the connection test button in the create flow (no agentId)', () => {
    renderModal({ open: true, onClose: vi.fn(), initialType: 'subagent_3p', initialCli: 'claude-code' })
    expect(screen.queryByRole('button', { name: /test connection/i })).toBeNull()
  })
})

describe('CreateAgentModal — Advanced step fields', () => {
  it('forwards default timeout_seconds and max_tool_iterations (steering_mode is retired — the wire field no longer exists)', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate })
    await fillAndAdvanceToStep3()
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call.timeout_seconds).toBe(300)
    expect(call.max_tool_iterations).toBe(200)
    expect('steering_mode' in call).toBe(false)
  })

  it('forwards model_params and the new shell_policy shape from Advanced', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate })
    await fillAndAdvanceToStep3()

    fireEvent.click(screen.getByTestId('advanced-disclosure-trigger'))
    fireEvent.change(screen.getByLabelText('Temperature'), {
      target: { value: '0.5' },
    })
    fireEvent.change(screen.getByTestId('shell-deny-patterns-textarea'), {
      target: { value: 'rm -rf /\ncurl.*169\\.254' },
    })

    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call.model_params).toEqual({ temperature: 0.5 })
    expect(call.shell_policy).toEqual({
      enable_deny_patterns: true,
      custom_deny_patterns: ['rm -rf /', 'curl.*169\\.254'],
    })
  })

  // W2b field-matrix gating (docs/internal/architecture/agent-types-field-matrix.md):
  // steering_mode is a Main-surface concept — workers are forced
  // one-at-a-time server-side — and `executor` never applies to a native
  // (in-process) Subagent (the server derives `native`).
  it('Subagent create has NO steering_mode and NO executor key at all', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate, initialType: 'Subagent' })
    await fillAndAdvanceToStep3({ initialType: 'Subagent' })
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call.steering_mode).toBeUndefined()
    expect('executor' in call).toBe(false)
    // A native Subagent legitimately carries max_tool_iterations (O, default
    // 200/turn per the field matrix) — only steering_mode/executor are gated.
    expect(call.max_tool_iterations).toBe(200)
  })

  // subagent_3p never carries max_tool_iterations (the external CLI runs its
  // own loop — agent-types-field-matrix.md, Decisions #1 (resolved
  // 2026-07-03): excluded) but DOES carry timeout_seconds (process-level
  // kill for a hung CLI) and always sends the external-cli executor block.
  it('subagent_3p create has NO max_tool_iterations, HAS timeout_seconds, and executor.kind=external-cli', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({
      open: true,
      onClose: vi.fn(),
      onCreate,
      initialType: 'subagent_3p',
      initialCli: 'claude-code',
    })
    await fillAndAdvanceToStep3({ initialType: 'subagent_3p', cliPath: '/usr/local/bin/claude-code' })
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call.max_tool_iterations).toBeUndefined()
    expect(call.timeout_seconds).toBe(300)
    expect(call.executor).toMatchObject({ kind: 'external-cli' })
    expect(call.steering_mode).toBeUndefined()
    expect(call.model_params).toBeUndefined()
  })
})

describe('CreateAgentModal — create/edit parity fixes (P3, 2026-07-03)', () => {
  it('step 2 shows the SOUL.md minLength hint and places the upload button BELOW the textarea', async () => {
    renderModal({ open: true, onClose: vi.fn() })
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'A' } })
    fireEvent.change(screen.getByTestId('wizard-model'), { target: { value: 'm' } })
    fireEvent.click(screen.getByTestId('wizard-next-1'))

    const soul = await screen.findByTestId('wizard-soul')
    // minLength hint (AgentCreateRequest.soul is minLength: 1).
    expect(screen.getByTestId('soul-minlength-hint')).toBeInTheDocument()
    // The upload affordance sits BELOW the textarea (edit-dialog parity —
    // the wizard used to place its own drifted copy above the box).
    const upload = screen.getByTestId('wizard-soul-upload')
    const position = soul.compareDocumentPosition(upload)
    expect(position & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('subagent_3p wizard is TWO steps — no Tools step, no skills mapping, Create on step 2', async () => {
    renderModal({ open: true, onClose: vi.fn(), initialType: 'subagent_3p' })
    await fillAndAdvanceToStep3({ initialType: 'subagent_3p' })
    // Create is reachable on step 2; the Tools step (and its skills/tools
    // blocks) does not exist for external runners.
    expect(screen.getByTestId('wizard-create')).toBeInTheDocument()
    expect(screen.queryByTestId('wizard-next-2')).toBeNull()
    expect(screen.queryByTestId('wizard-skills')).toBeNull()
    expect(screen.queryByTestId('wizard-tools-cfg')).toBeNull()
    // The stepper shows exactly two steps.
    const stepper = screen.getByTestId('wizard-stepper')
    expect(stepper.textContent).toContain('Identity')
    expect(stepper.textContent).toContain('Personality')
    expect(stepper.textContent).not.toContain('Tools')
  })

  it('native Subagent step 3 still offers the skills mapping', async () => {
    renderModal({ open: true, onClose: vi.fn(), initialType: 'Subagent' })
    await fillAndAdvanceToStep3({ initialType: 'Subagent' })
    expect(screen.queryByTestId('wizard-skills')).not.toBeNull()
  })
})

// ── Subagent inherit-flag wire-omission contract (UAT 4a) ───────────────────
//
// A native Subagent's Model / Tools / Skills may each be inherited
// from the delegating caller instead of set explicitly (InheritToggle,
// `wizard-inherit-model` / `-tools` / `-skills`). When a toggle
// is ON, `payloadToCreateRequest` (CreateAgentModal.tsx) MUST omit the
// corresponding field(s) from the wire request so the server keeps the
// inherited rail — and the UI-only `inherit_*` flags themselves must NEVER
// appear in the request (they aren't in the AgentCreateRequestSubagent
// schema; sending one would be a 400).
describe('CreateAgentModal — Subagent inherit-flag wire-omission contract (UAT 4a)', () => {
  it('inherit_model=true omits model/provider/fallback_models and the flag itself never rides the wire', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate, initialType: 'Subagent' })
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'Inherits Model' } })
    fireEvent.change(screen.getByTestId('wizard-description'), { target: { value: 'inherits its model from the caller' } })
    // Toggle ON (default OFF) — the model picker hides and step 1 no longer
    // gates on a model value.
    fireEvent.click(screen.getByTestId('wizard-inherit-model'))
    await waitFor(() => expect(screen.getByTestId('wizard-next-1')).not.toBeDisabled())
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    fireEvent.change(await screen.findByTestId('wizard-soul'), { target: { value: 'soul' } })
    fireEvent.click(screen.getByTestId('wizard-next-2'))
    fireEvent.click(await screen.findByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call).not.toHaveProperty('model')
    expect(call).not.toHaveProperty('provider')
    expect(call).not.toHaveProperty('fallback_models')
    expect(call).not.toHaveProperty('inherit_model')
  })

  it('inherit_tools=true omits tools_cfg even after the per-tool editor was touched, and the flag itself never rides the wire', async () => {
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate, initialType: 'Subagent' })
    await fillAndAdvanceToStep3({ initialType: 'Subagent' })
    // Set an explicit override BEFORE flipping inherit back ON — proves the
    // gate (not merely an untouched default) is what omits the field.
    fireEvent.click(screen.getByTestId('preset-cautious'))
    fireEvent.click(screen.getByTestId('wizard-inherit-tools'))
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call).not.toHaveProperty('tools_cfg')
    expect(call).not.toHaveProperty('inherit_tools')
  })

  it('inherit_skills=true omits skills even after a skill was granted, and the flag itself never rides the wire', async () => {
    vi.mocked(fetchSkills).mockResolvedValue([
      { id: 'web-research', name: 'Web Research', version: '1.0.0', verified: false, status: 'active' } as Skill,
    ])
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate, initialType: 'Subagent' })
    await fillAndAdvanceToStep3({ initialType: 'Subagent' })
    // Grant a skill BEFORE flipping inherit back ON — proves the gate omits
    // an explicitly-set value, not just an empty default.
    fireEvent.click(screen.getByTestId('skill-checkbox-web-research'))
    fireEvent.click(screen.getByTestId('wizard-inherit-skills'))
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    expect(call).not.toHaveProperty('skills')
    expect(call).not.toHaveProperty('inherit_skills')
  })

  it('with all three inherit toggles ON, none of the three gated fields nor the three inherit_* flags reach the wire', async () => {
    vi.mocked(fetchSkills).mockResolvedValue([
      { id: 'web-research', name: 'Web Research', version: '1.0.0', verified: false, status: 'active' } as Skill,
    ])
    const onCreate = vi.fn().mockResolvedValue(undefined)
    renderModal({ open: true, onClose: vi.fn(), onCreate, initialType: 'Subagent' })
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'All Inherited' } })
    fireEvent.change(screen.getByTestId('wizard-description'), { target: { value: 'inherits everything' } })
    fireEvent.click(screen.getByTestId('wizard-inherit-model'))
    await waitFor(() => expect(screen.getByTestId('wizard-next-1')).not.toBeDisabled())
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    fireEvent.change(await screen.findByTestId('wizard-soul'), { target: { value: 'soul' } })
    fireEvent.click(screen.getByTestId('wizard-next-2'))
    await screen.findByTestId('wizard-create')
    fireEvent.click(screen.getByTestId('wizard-inherit-tools'))
    fireEvent.click(screen.getByTestId('wizard-inherit-skills'))
    fireEvent.click(screen.getByTestId('wizard-create'))
    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const call = onCreate.mock.calls.at(-1)![0]
    for (const key of [
      'model', 'provider', 'fallback_models', 'tools_cfg', 'skills',
      'inherit_model', 'inherit_tools', 'inherit_skills',
    ]) {
      expect(call).not.toHaveProperty(key)
    }
    // The create still went through with the fields that DO apply.
    expect(call.type).toBe('Subagent')
    expect(call.name).toBe('All Inherited')
  })
})

// =====================================================================
// Providers-query four-state plumbing (bug fix)
//
// The model picker used to collapse LOADING / FAILED / connected-but-
// empty-catalog / no-provider-connected into one "connect a provider"
// message, true for only the last state. Motivating incident: CI logged
// the gateway's upstream fetch to openrouter.ai failing 9 times with
// `context canceled` (zero successes) while /providers itself was healthy
// (curl from the same worker: 200 in 0.46s) — and the picker still told
// the user no provider was connected. These tests exercise the REAL
// `providers` useQuery in CreateAgentModal end to end (ModelSelector is
// stubbed above, but only to expose the props it receives — not to fake
// the query states themselves).
// =====================================================================

function connectedProvider(overrides: Partial<Provider> = {}): Provider {
  return {
    id: 'openrouter',
    name: 'openrouter',
    display_name: 'OpenRouter',
    status: 'connected',
    auth_method: 'api_key',
    dependents: [],
    backs_default: false,
    models: [],
    has_models_endpoint: true,
    has_api_key: true,
    ...overrides,
  }
}

describe('CreateAgentModal — providers-query four-state plumbing (bug fix)', () => {
  it('state 1 (loading): the picker sees catalogStatus="loading" while fetchProviders is still in flight', async () => {
    let resolveProviders: (v: Provider[]) => void = () => {}
    vi.mocked(fetchProviders).mockReset().mockReturnValue(
      new Promise<Provider[]>((resolve) => {
        resolveProviders = resolve
      }),
    )
    renderModal({ open: true, onClose: vi.fn() })
    expect(screen.getByTestId('wizard-model')).toHaveAttribute('data-catalog-status', 'loading')
    expect(screen.queryByTestId('wizard-model-empty-hint')).not.toBeInTheDocument()
    // Drain the pending promise so the test doesn't leak a dangling timer.
    resolveProviders([])
    await waitFor(() => expect(screen.getByTestId('wizard-model')).toHaveAttribute('data-catalog-status', 'ready'))
  })

  it('state 2 (error): the picker sees catalogStatus="error" with the real message and a working Retry', async () => {
    vi.mocked(fetchProviders).mockReset().mockRejectedValue(
      new Error('Get "https://openrouter.ai/api/v1/models": context canceled'),
    )
    renderModal({ open: true, onClose: vi.fn() })
    await waitFor(() =>
      expect(screen.getByTestId('wizard-model')).toHaveAttribute('data-catalog-status', 'error'),
    )
    expect(screen.getByTestId('wizard-model-error-message')).toHaveTextContent(/context canceled/)
    // Retry re-invokes the same query function (CreateAgentModal wraps
    // providersQuery.refetch, not a fresh fetch — the wire-up is what
    // this test proves).
    vi.mocked(fetchProviders).mockClear()
    fireEvent.click(screen.getByTestId('wizard-model-retry'))
    await waitFor(() => expect(fetchProviders).toHaveBeenCalledTimes(1))
  })

  it('state 3 (connected, empty catalog with backend warning): the picker sees the provider\'s warning text, not the generic "connect a provider" hint', async () => {
    vi.mocked(fetchProviders)
      .mockReset()
      .mockResolvedValue([
        connectedProvider({ models: [], warning: 'could not fetch upstream model list: status 429' }),
      ])
    renderModal({ open: true, onClose: vi.fn() })
    await waitFor(() =>
      expect(screen.getByTestId('wizard-model')).toHaveAttribute('data-catalog-status', 'ready'),
    )
    expect(screen.getByTestId('wizard-model-empty-hint')).toHaveTextContent(
      /model list unavailable: could not fetch upstream model list: status 429/i,
    )
  })

  it('state 4 (no provider connected at all): the picker keeps today\'s exact "connect a provider" hint — the one true case', async () => {
    vi.mocked(fetchProviders).mockReset().mockResolvedValue([])
    renderModal({ open: true, onClose: vi.fn() })
    await waitFor(() =>
      expect(screen.getByTestId('wizard-model')).toHaveAttribute('data-catalog-status', 'ready'),
    )
    expect(screen.getByTestId('wizard-model-empty-hint')).toHaveTextContent(
      /connect a provider in settings to pick a model/i,
    )
  })
})
