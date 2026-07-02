import { describe, it, expect, vi, beforeEach, beforeAll } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ScheduleFormSheet } from './ScheduleFormSheet'
import type { Agent } from '@/lib/api'
import type { Schedule } from '@/lib/api/generated/openapi-types'

// #264 US6 AS3 — Create via the Sheet form: fill fields + submit calls
// createSchedule + shows a success toast.
// #320 — Friendly When? selector + live NextRunPreview + cron round-trip (US-A2)
// #321 — Plain Advanced wording + Hermes isolated-only session model (US-A3/D18)
//
// Note on Radix Select interaction in JSDOM:
// Radix <Select> uses pointer events (hasPointerCapture) which are not implemented
// in JSDOM. SmartSelect interaction tests that involve dropdown opening use the
// `schedule` prop (edit mode, pre-initialised state) rather than simulating dropdown
// clicks. The friendly→cron logic is tested exhaustively in cronRoundTrip.test.ts
// (pure unit tests, no DOM required).

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn(),
    createSchedule: vi.fn().mockResolvedValue({ id: 's-new' }),
    updateSchedule: vi.fn().mockResolvedValue({ id: 's1' }),
    isApiError: vi.fn().mockReturnValue(false),
  }
})

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: ReturnType<typeof vi.fn> }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

import { fetchAgents, createSchedule, updateSchedule } from '@/lib/api'

const mockAgents: Agent[] = [
  { id: 'mia', name: 'Mia', type: 'core', default: true },
  { id: 'max', name: 'Max', type: 'custom', default: false },
] as unknown as Agent[]

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderForm(props?: Partial<React.ComponentProps<typeof ScheduleFormSheet>>) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <ScheduleFormSheet open={true} onOpenChange={() => {}} {...props} />
    </QueryClientProvider>,
  )
}

/** Click the "Advanced options" toggle button. */
async function openAdvanced() {
  const advBtn = screen.getByRole('button', { name: /Advanced options/i })
  fireEvent.click(advBtn)
  await waitFor(() => expect(screen.getByText('Run timeout (seconds)')).toBeInTheDocument())
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(fetchAgents).mockResolvedValue(mockAgents)
})

// ── Original tests (kept and updated for AC4 default=once) ────────────────

describe('ScheduleFormSheet — create (#264 US6 AS3)', () => {
  it('renders the create form with no table', async () => {
    const { container } = renderForm({ defaultOwnerAgentId: 'mia' })
    await screen.findByText('New schedule')
    expect(container.querySelector('table')).toBeNull()
  })

  it('fills fields and submitting calls createSchedule + toasts', async () => {
    renderForm({ defaultOwnerAgentId: 'mia' })
    await screen.findByText('New schedule')

    // Name + message are required; owner pre-filled from defaultOwnerAgentId.
    fireEvent.change(screen.getByPlaceholderText(/Daily PR summary/i), {
      target: { value: 'My schedule' },
    })
    fireEvent.change(screen.getByPlaceholderText(/Summarize today/i), {
      target: { value: 'Do the thing' },
    })

    fireEvent.click(screen.getByRole('button', { name: /create schedule/i }))

    await waitFor(() => expect(createSchedule).toHaveBeenCalledTimes(1))
    const body = vi.mocked(createSchedule).mock.calls[0][0]
    expect(body.name).toBe('My schedule')
    expect(body.message).toBe('Do the thing')
    expect(body.owner_agent_id).toBe('mia')
    // default trigger is "once" (AC4 — US-A2)
    expect(body.trigger.kind).toBe('at')

    await waitFor(() =>
      expect(mockAddToast).toHaveBeenCalledWith(
        expect.objectContaining({ variant: 'success' }),
      ),
    )
  })

  it('disables submit until required fields are filled', async () => {
    renderForm({ defaultOwnerAgentId: 'mia' })
    await screen.findByText('New schedule')
    // owner is pre-filled but name + message empty → disabled
    expect(screen.getByRole('button', { name: /create schedule/i })).toBeDisabled()
  })
})

// ── #320 US-A2 — Friendly When? + NextRunPreview ──────────────────────────

describe('ScheduleFormSheet — #320 US-A2: friendly trigger + preview', () => {
  it('AC4: brand-new form shows "Once" as the default When? mode', async () => {
    renderForm({ defaultOwnerAgentId: 'mia' })
    await screen.findByText('New schedule')
    // The When? label is present.
    expect(screen.getByText('When?')).toBeInTheDocument()
    // "Once" appears in the When? combobox trigger.
    // The When? trigger is the second combobox (after the agent picker).
    const comboboxes = document.body.querySelectorAll('[role="combobox"]')
    const whenTrigger = comboboxes[1]
    expect(whenTrigger?.textContent).toContain('Once')
  })

  it('US-A1 AC1: primary fields visible; advanced fields hidden until expanded', async () => {
    renderForm({ defaultOwnerAgentId: 'mia' })
    await screen.findByText('New schedule')
    // Primary fields: agent, name, message, When?
    expect(screen.getByText('Which agent')).toBeInTheDocument()
    expect(screen.getByText('Name')).toBeInTheDocument()
    expect(screen.getByText('Message')).toBeInTheDocument()
    expect(screen.getByText('When?')).toBeInTheDocument()
    // Advanced fields NOT visible (collapsed) — Run timeout not in DOM until expanded.
    expect(screen.queryByText('Run timeout (seconds)')).not.toBeInTheDocument()
    expect(screen.queryByText('Active')).not.toBeInTheDocument()
    // Advanced options button is present.
    expect(screen.getByRole('button', { name: /Advanced options/i })).toBeInTheDocument()
  })

  it('US-A1 AC2: advanced section reveals extra fields on expand', async () => {
    renderForm({ defaultOwnerAgentId: 'mia' })
    await screen.findByText('New schedule')
    await openAdvanced()
    expect(screen.getByText('Run timeout (seconds)')).toBeInTheDocument()
    expect(screen.getByText('Active')).toBeInTheDocument()
  })

  it('AC4: default trigger kind is "once" (submitted as kind:at)', async () => {
    renderForm({ defaultOwnerAgentId: 'mia' })
    await screen.findByText('New schedule')
    fireEvent.change(screen.getByPlaceholderText(/Daily PR summary/i), { target: { value: 'T' } })
    fireEvent.change(screen.getByPlaceholderText(/Summarize today/i), { target: { value: 'M' } })
    fireEvent.click(screen.getByRole('button', { name: /create schedule/i }))
    await waitFor(() => expect(createSchedule).toHaveBeenCalledTimes(1))
    const body = vi.mocked(createSchedule).mock.calls[0][0]
    expect(body.trigger.kind).toBe('at')
  })

  it('AC5 (edit): cron "0 18 * * *" re-hydrates to daily-at + preview renders', async () => {
    const dailySchedule: Schedule = {
      id: 's-daily',
      name: 'Daily',
      enabled: true,
      owner_agent_id: 'mia',
      trigger: { kind: 'cron', cron_expr: '0 18 * * *' },
      message: 'Daily msg',
      deliver: false,
      session_mode: 'isolated',
      timeout_seconds: 0,
      state: {},
      runs: [],
      created_at_ms: 1000,
      updated_at_ms: 2000,
    }
    renderForm({ schedule: dailySchedule })
    await screen.findByText('Edit schedule')
    // The When? should show "Repeat" (reverse-parsed from daily cron).
    // The form should have the repeat shape visible.
    expect(screen.getByText('When?')).toBeInTheDocument()
    // Preview renders with "server time" label for a valid cron.
    await waitFor(() => expect(screen.getByTestId('next-run-preview')).toBeInTheDocument())
    expect(screen.getByTestId('next-run-preview')).toHaveTextContent('server time')
  })

  it('AC5 (edit): complex cron "*/5 9-17 * * 1-5" opens Custom view verbatim', async () => {
    const complexSchedule: Schedule = {
      id: 's-complex',
      name: 'Complex',
      enabled: true,
      owner_agent_id: 'mia',
      trigger: { kind: 'cron', cron_expr: '*/5 9-17 * * 1-5' },
      message: 'Msg',
      deliver: false,
      session_mode: 'isolated',
      timeout_seconds: 0,
      state: {},
      runs: [],
      created_at_ms: 1000,
      updated_at_ms: 2000,
    }
    renderForm({ schedule: complexSchedule })
    await screen.findByText('Edit schedule')
    // The cron expression should be shown verbatim in the custom input.
    await waitFor(() => expect(screen.getByPlaceholderText('0 18 * * *')).toBeInTheDocument())
    const cronInput = screen.getByPlaceholderText('0 18 * * *') as HTMLInputElement
    expect(cronInput.value).toBe('*/5 9-17 * * 1-5')
  })

  it('AC5 (edit): complex cron re-saved byte-identical (trigger unchanged)', async () => {
    const complexSchedule: Schedule = {
      id: 's-complex',
      name: 'Complex',
      enabled: true,
      owner_agent_id: 'mia',
      trigger: { kind: 'cron', cron_expr: '*/5 9-17 * * 1-5' },
      message: 'Msg',
      deliver: false,
      session_mode: 'isolated',
      timeout_seconds: 0,
      state: {},
      runs: [],
      created_at_ms: 1000,
      updated_at_ms: 2000,
    }
    renderForm({ schedule: complexSchedule })
    await screen.findByText('Edit schedule')
    // Save without changing anything.
    await waitFor(() => expect(screen.getByPlaceholderText('0 18 * * *')).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(updateSchedule).toHaveBeenCalledTimes(1))
    const body = vi.mocked(updateSchedule).mock.calls[0][1]
    // Trigger should be byte-identical to the original.
    expect(body.trigger).toEqual({ kind: 'cron', cron_expr: '*/5 9-17 * * 1-5' })
  })

  it('AC3 custom: malformed cron (structural error) gates Save + hides preview', async () => {
    // Load with a complex cron so Custom view is open.
    const complexSchedule: Schedule = {
      id: 's-complex',
      name: 'Complex',
      enabled: true,
      owner_agent_id: 'mia',
      trigger: { kind: 'cron', cron_expr: '*/5 9-17 * * 1-5' },
      message: 'Msg',
      deliver: false,
      session_mode: 'isolated',
      timeout_seconds: 0,
      state: {},
      runs: [],
      created_at_ms: 1000,
      updated_at_ms: 2000,
    }
    renderForm({ schedule: complexSchedule })
    await screen.findByText('Edit schedule')
    await waitFor(() => screen.getByPlaceholderText('0 18 * * *'))

    // Type a malformed cron.
    fireEvent.change(screen.getByPlaceholderText('0 18 * * *'), {
      target: { value: 'not a cron' },
    })

    // Inline error shows.
    await waitFor(() => expect(screen.getByTestId('cron-error')).toBeInTheDocument())
    // Save button should be disabled.
    expect(screen.getByRole('button', { name: /save changes/i })).toBeDisabled()
    // Preview hidden (structural error present).
    expect(screen.queryByTestId('next-run-preview')).not.toBeInTheDocument()
  })

  it('AC3 custom: structurally valid cron shows preview (AC3b: 99 99 * * * is structurally valid)', async () => {
    const complexSchedule: Schedule = {
      id: 's-complex',
      name: 'Complex',
      enabled: true,
      owner_agent_id: 'mia',
      trigger: { kind: 'cron', cron_expr: '*/5 9-17 * * 1-5' },
      message: 'Msg',
      deliver: false,
      session_mode: 'isolated',
      timeout_seconds: 0,
      state: {},
      runs: [],
      created_at_ms: 1000,
      updated_at_ms: 2000,
    }
    renderForm({ schedule: complexSchedule })
    await screen.findByText('Edit schedule')
    await waitFor(() => screen.getByPlaceholderText('0 18 * * *'))

    // Type a structurally valid but semantically bad cron.
    fireEvent.change(screen.getByPlaceholderText('0 18 * * *'), {
      target: { value: '99 99 * * *' },
    })

    // Structural check passes → no cron-error, preview shows.
    await waitFor(() => expect(screen.queryByTestId('cron-error')).not.toBeInTheDocument())
    expect(screen.getByTestId('next-run-preview')).toBeInTheDocument()
    // Save enabled.
    expect(screen.getByRole('button', { name: /save changes/i })).not.toBeDisabled()
  })

  it('Custom: examples text is shown', async () => {
    const complexSchedule: Schedule = {
      id: 's-c',
      name: 'C',
      enabled: true,
      owner_agent_id: 'mia',
      trigger: { kind: 'cron', cron_expr: '*/5 * * * *' },
      message: 'M',
      deliver: false,
      session_mode: 'isolated',
      timeout_seconds: 0,
      state: {},
      runs: [],
      created_at_ms: 1000,
      updated_at_ms: 2000,
    }
    renderForm({ schedule: complexSchedule })
    await screen.findByText('Edit schedule')
    await waitFor(() => screen.getByText(/Examples:/))
    expect(screen.getByText(/Examples:/)).toBeInTheDocument()
  })

  it('BLOCKER 2: zero-padded cron "0 09 * * *" opens Custom verbatim (no silent data-loss)', async () => {
    // Without the BLOCKER 2 fix, "09" would be accepted and round-tripped as "9",
    // changing the stored expression. With the fix, it falls through to Custom.
    const zeroPaddedSchedule: Schedule = {
      id: 's-padded',
      name: 'Padded',
      enabled: true,
      owner_agent_id: 'mia',
      trigger: { kind: 'cron', cron_expr: '0 09 * * *' },
      message: 'Msg',
      deliver: false,
      session_mode: 'isolated',
      timeout_seconds: 0,
      state: {},
      runs: [],
      created_at_ms: 1000,
      updated_at_ms: 2000,
    }
    renderForm({ schedule: zeroPaddedSchedule })
    await screen.findByText('Edit schedule')
    // Must open in Custom view with the expression verbatim.
    await waitFor(() => expect(screen.getByPlaceholderText('0 18 * * *')).toBeInTheDocument())
    const cronInput = screen.getByPlaceholderText('0 18 * * *') as HTMLInputElement
    expect(cronInput.value).toBe('0 09 * * *')
  })

  it('BLOCKER 2: step cron "*/15 9 * * *" opens Custom verbatim', async () => {
    const stepSchedule: Schedule = {
      id: 's-step',
      name: 'Step',
      enabled: true,
      owner_agent_id: 'mia',
      trigger: { kind: 'cron', cron_expr: '*/15 9 * * *' },
      message: 'Msg',
      deliver: false,
      session_mode: 'isolated',
      timeout_seconds: 0,
      state: {},
      runs: [],
      created_at_ms: 1000,
      updated_at_ms: 2000,
    }
    renderForm({ schedule: stepSchedule })
    await screen.findByText('Edit schedule')
    await waitFor(() => expect(screen.getByPlaceholderText('0 18 * * *')).toBeInTheDocument())
    const cronInput = screen.getByPlaceholderText('0 18 * * *') as HTMLInputElement
    expect(cronInput.value).toBe('*/15 9 * * *')
  })

  it('every 30min schedule re-hydrates to Repeat interval', async () => {
    const intervalSchedule: Schedule = {
      id: 's-interval',
      name: 'Interval',
      enabled: true,
      owner_agent_id: 'mia',
      trigger: { kind: 'every', every_ms: 1_800_000 },
      message: 'Msg',
      deliver: false,
      session_mode: 'isolated',
      timeout_seconds: 0,
      state: {},
      runs: [],
      created_at_ms: 1000,
      updated_at_ms: 2000,
    }
    renderForm({ schedule: intervalSchedule })
    await screen.findByText('Edit schedule')
    // The interval input should show 30.
    await waitFor(() => {
      const numberInputs = document.body.querySelectorAll('input[type="number"]')
      const intervalInput = Array.from(numberInputs).find(
        (inp) => (inp as HTMLInputElement).value === '30',
      )
      expect(intervalInput).toBeDefined()
    })
  })
})

// ── #321 US-A3 — Session model (D18) ──────────────────────────────────────

describe('ScheduleFormSheet — #321 US-A3: session model (D18)', () => {
  it('AC1: no "Use my main chat" option + no session_mode select with main option', async () => {
    renderForm({ defaultOwnerAgentId: 'mia' })
    await screen.findByText('New schedule')
    // Expand advanced to see all fields.
    await openAdvanced()
    // No "main" text or "main chat" text.
    expect(screen.queryByText(/main chat/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/Use my main chat/i)).not.toBeInTheDocument()
    // No select has a 'main' option.
    const allSelects = document.body.querySelectorAll('select')
    const hasMainOption = Array.from(allSelects).some((s) =>
      Array.from(s.options).some((o) => o.value === 'main'),
    )
    expect(hasMainOption).toBe(false)
    // No combobox shows 'main' as current value.
    const allComboboxes = document.body.querySelectorAll('[role="combobox"]')
    const hasMainCombobox = Array.from(allComboboxes).some((c) =>
      c.textContent?.toLowerCase().includes('main chat') &&
      !c.textContent?.toLowerCase().includes('legacy'),
    )
    expect(hasMainCombobox).toBe(false)
  })

  it('AC2: default submit produces session_mode:isolated', async () => {
    renderForm({ defaultOwnerAgentId: 'mia' })
    await screen.findByText('New schedule')
    fireEvent.change(screen.getByPlaceholderText(/Daily PR summary/i), { target: { value: 'S' } })
    fireEvent.change(screen.getByPlaceholderText(/Summarize today/i), { target: { value: 'M' } })
    fireEvent.click(screen.getByRole('button', { name: /create schedule/i }))
    await waitFor(() => expect(createSchedule).toHaveBeenCalledTimes(1))
    const body = vi.mocked(createSchedule).mock.calls[0][0]
    expect(body.session_mode).toBe('isolated')
  })

  it('AC3: "keep the full thread" toggle → session_mode:continue', async () => {
    renderForm({ defaultOwnerAgentId: 'mia' })
    await screen.findByText('New schedule')
    await openAdvanced()

    // Toggle "keep the full thread" on.
    const keepThreadSwitch = screen.getByRole('switch', { name: /keep the full conversation thread/i })
    fireEvent.click(keepThreadSwitch)

    // Fill required fields.
    fireEvent.change(screen.getByPlaceholderText(/Daily PR summary/i), { target: { value: 'S' } })
    fireEvent.change(screen.getByPlaceholderText(/Summarize today/i), { target: { value: 'M' } })
    fireEvent.click(screen.getByRole('button', { name: /create schedule/i }))

    await waitFor(() => expect(createSchedule).toHaveBeenCalledTimes(1))
    const body = vi.mocked(createSchedule).mock.calls[0][0]
    expect(body.session_mode).toBe('continue')
  })

  it('AC4: owner defaults to the default:true agent (Mia) when no defaultOwnerAgentId', async () => {
    render(
      <QueryClientProvider client={makeClient()}>
        <ScheduleFormSheet open={true} onOpenChange={() => {}} />
      </QueryClientProvider>,
    )
    await screen.findByText('New schedule')
    fireEvent.change(screen.getByPlaceholderText(/Daily PR summary/i), { target: { value: 'S' } })
    fireEvent.change(screen.getByPlaceholderText(/Summarize today/i), { target: { value: 'M' } })
    fireEvent.click(screen.getByRole('button', { name: /create schedule/i }))
    await waitFor(() => expect(createSchedule).toHaveBeenCalledTimes(1))
    const body = vi.mocked(createSchedule).mock.calls[0][0]
    // Mia has default:true.
    expect(body.owner_agent_id).toBe('mia')
  })

  it('AC (US-A3): legacy "main" schedule loads with read-only notice (mode not re-selectable)', async () => {
    const mainSchedule: Schedule = {
      id: 's-main',
      name: 'Legacy Main',
      enabled: true,
      owner_agent_id: 'mia',
      trigger: { kind: 'every', every_ms: 3_600_000 },
      message: 'Old message',
      deliver: false,
      session_mode: 'main',
      timeout_seconds: 0,
      state: {},
      runs: [],
      created_at_ms: 1000,
      updated_at_ms: 2000,
    }
    renderForm({ schedule: mainSchedule })
    await screen.findByText('Edit schedule')
    await openAdvanced()
    // Should show read-only legacy notice.
    expect(screen.getByText(/legacy.*read-only/i)).toBeInTheDocument()
    // Should NOT show the "keep the full thread" toggle.
    expect(screen.queryByRole('switch', { name: /keep the full conversation thread/i })).not.toBeInTheDocument()
  })

  it('BLOCKER 1 (US-A3): legacy "main" schedule — save OMITS session_mode (not rewritten to isolated)', async () => {
    // Spec §0 line 320 / §3 US-A3 edge case: a pre-existing 'main' schedule must not
    // have its session_mode silently rewritten. OMIT the field from the update body
    // so the server preserves whatever is stored.
    const mainSchedule: Schedule = {
      id: 's-main',
      name: 'Legacy Main',
      enabled: true,
      owner_agent_id: 'mia',
      trigger: { kind: 'every', every_ms: 3_600_000 },
      message: 'Old message',
      deliver: false,
      session_mode: 'main',
      timeout_seconds: 0,
      state: {},
      runs: [],
      created_at_ms: 1000,
      updated_at_ms: 2000,
    }
    renderForm({ schedule: mainSchedule })
    await screen.findByText('Edit schedule')
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(updateSchedule).toHaveBeenCalledTimes(1))
    const body = vi.mocked(updateSchedule).mock.calls[0][1]
    // session_mode must be ABSENT from the update body — the server preserves 'main'.
    // (If it were present and set to 'isolated', the server would overwrite the stored mode.)
    expect(Object.prototype.hasOwnProperty.call(body, 'session_mode')).toBe(false)
  })

  it('reassurance line is visible on the primary form', async () => {
    renderForm({ defaultOwnerAgentId: 'mia' })
    await screen.findByText('New schedule')
    expect(screen.getByText(/each run starts fresh/i)).toBeInTheDocument()
  })
})

// Workers (type:'worker') have no heartbeat and the backend rejects a scheduled
// run owned by a worker, so the owner picker must exclude them — neither offered
// as an option nor auto-selected as the default owner.
describe('ScheduleFormSheet — workers excluded from the owner picker', () => {
  const agentsWithWorker: Agent[] = [
    { id: 'mia', name: 'Mia', type: 'core', default: false },
    { id: 'builder', name: 'Builder Worker', type: 'worker', default: false },
  ] as unknown as Agent[]

  it('does not list a worker as an owner option; lists base agents', async () => {
    vi.mocked(fetchAgents).mockResolvedValue(agentsWithWorker)
    // jsdom lacks scrollIntoView; Radix Select calls it when opening the listbox.
    Element.prototype.scrollIntoView = vi.fn()

    renderForm()
    await screen.findByText('New schedule')

    // The owner picker is the first combobox.
    const ownerTrigger = document.body.querySelectorAll('[role="combobox"]')[0] as HTMLElement
    fireEvent.click(ownerTrigger)

    await waitFor(() => {
      const options = Array.from(document.querySelectorAll('[role="option"]')).map(
        (el) => el.textContent ?? '',
      )
      expect(options.some((t) => t.includes('Mia'))).toBe(true)
      expect(options.some((t) => t.includes('Builder Worker'))).toBe(false)
    })

    delete (Element.prototype as { scrollIntoView?: () => void }).scrollIntoView
  })

  it('auto-default owner skips a worker even when the worker is default:true', async () => {
    // A worker marked default:true must NOT become the schedule owner — the
    // auto-selection only considers eligible (non-worker) agents.
    vi.mocked(fetchAgents).mockResolvedValue([
      { id: 'builder', name: 'Builder Worker', type: 'worker', default: true },
      { id: 'mia', name: 'Mia', type: 'core', default: false },
    ] as unknown as Agent[])

    render(
      <QueryClientProvider client={makeClient()}>
        <ScheduleFormSheet open={true} onOpenChange={() => {}} />
      </QueryClientProvider>,
    )
    await screen.findByText('New schedule')
    fireEvent.change(screen.getByPlaceholderText(/Daily PR summary/i), { target: { value: 'S' } })
    fireEvent.change(screen.getByPlaceholderText(/Summarize today/i), { target: { value: 'M' } })
    fireEvent.click(screen.getByRole('button', { name: /create schedule/i }))

    await waitFor(() => expect(createSchedule).toHaveBeenCalledTimes(1))
    const body = vi.mocked(createSchedule).mock.calls[0][0]
    // The first non-worker agent (Mia) is chosen, NOT the default:true worker.
    expect(body.owner_agent_id).toBe('mia')
  })
})

// ── DateTimePicker round-trip for "Run at" (kind:'at') ─────────────────────
//
// The "Once" trigger's date/time is picked via the shadcn DateTimePicker
// (Popover + Calendar + Hour/Minute <Select>s), not a native
// `<input type="datetime-local">`. This exercises the full pick → submit →
// payload path end to end (complementing the pure-function coverage in
// taskFormFields.test.ts and cronRoundTrip.test.ts).

describe('ScheduleFormSheet — DateTimePicker round-trip for "Run at" (kind:\'at\')', () => {
  beforeAll(() => {
    if (!Element.prototype.hasPointerCapture) {
      Element.prototype.hasPointerCapture = () => false
    }
    if (!Element.prototype.scrollIntoView) {
      Element.prototype.scrollIntoView = () => {}
    }
    if (typeof window !== 'undefined' && !window.ResizeObserver) {
      window.ResizeObserver = class {
        observe() {}
        unobserve() {}
        disconnect() {}
      } as unknown as typeof ResizeObserver
    }
  })

  function runAtTrigger(): HTMLElement {
    const el = document.getElementById('schedule-at')
    if (!el) throw new Error('Run at trigger not found')
    return el
  }

  function clickDay(isoDate: string) {
    const btn = document.querySelector(`[data-day="${isoDate}"] button`)
    if (!btn) throw new Error(`no day button for ${isoDate}`)
    fireEvent.click(btn)
  }

  function selectOption(comboboxName: string, optionName: string) {
    fireEvent.click(screen.getByRole('combobox', { name: comboboxName }))
    const option = screen.getByRole('option', { name: optionName })
    fireEvent.pointerDown(option, { pointerId: 1, button: 0 })
    fireEvent.click(option)
  }

  // The calendar opens on real "today" (react-day-picker's own default) when
  // no value is set yet — navigate to the target month regardless of when
  // the suite happens to run (same pattern as date-time-picker.test.tsx).
  function navigateToMonth(isoDate: string) {
    const [y, m] = isoDate.split('-').map(Number)
    const target = new Date(y, m - 1, 1)
    const now = new Date()
    const diff = (target.getFullYear() - now.getFullYear()) * 12 + (target.getMonth() - now.getMonth())
    const label = diff >= 0 ? /go to the next month/i : /go to the previous month/i
    for (let i = 0; i < Math.abs(diff); i++) {
      fireEvent.click(screen.getByRole('button', { name: label }))
    }
  }

  it('picking a date + time in the "Run at" picker submits the matching at_ms', async () => {
    renderForm({ defaultOwnerAgentId: 'mia' })
    await screen.findByText('New schedule')

    fireEvent.change(screen.getByPlaceholderText(/Daily PR summary/i), { target: { value: 'T' } })
    fireEvent.change(screen.getByPlaceholderText(/Summarize today/i), { target: { value: 'M' } })

    // Default trigger mode is already "Once" (AC4) — open the "Run at"
    // DateTimePicker directly and pick a specific date + time.
    fireEvent.click(runAtTrigger())
    navigateToMonth('2026-09-10')
    clickDay('2026-09-10')
    selectOption('Hour', '12')
    selectOption('Minute', '30')

    fireEvent.click(screen.getByRole('button', { name: /create schedule/i }))

    await waitFor(() => expect(createSchedule).toHaveBeenCalledTimes(1))
    const body = vi.mocked(createSchedule).mock.calls[0][0]
    expect(body.trigger.kind).toBe('at')
    expect(body.trigger.at_ms).toBe(new Date(2026, 8, 10, 12, 30).getTime())
  })
})
