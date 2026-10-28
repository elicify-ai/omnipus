/**
 * PerformanceSection — Settings → Performance tab.
 *
 * Spec-3 max-parallel fan-out gate: lets an admin configure
 * max_parallel_agents (the global dispatch semaphore capacity) and
 * tools_on_demand (the tool-loading mode).
 * Admin-only; backed by GET/PUT /api/v1/performance.
 *
 * Autosave: changes are applied automatically after a short debounce.
 * Performance settings are one of ADR-0008 ruling 6's six controls, so a
 * confirmation naming the change opens once the debounced value settles on a
 * valid input — the Save button is gone, and the password prompt that used to
 * sit here is gone with it.
 *
 * Both max_parallel_agents and tools_on_demand are sent together on every
 * PUT so neither field silently reverts when only one is changed.
 *
 * #904 adds the global "Max tool calls per turn" limit (MaxToolIterationsCard,
 * tool-iteration-limit-spec.md US-1/US-6): a LOWERING is first previewed
 * (GET /performance/max-tool-iterations/preview); agents it would lower are
 * listed in MaxToolIterationsLoweringDialog before the step-up gate, and the
 * PUT carries that exact list as confirmed_lowering. A 409 drift re-opens the
 * dialog with the server's fresh list. A raise never rewrites an agent (D20)
 * and saves directly behind the step-up gate.
 *
 * C1 adds the two independent goal timers beside the goal completion budget:
 * the idle reminder quiet window (goal_reminder_quiet_seconds, 30–3600, no
 * off value) and the stalled-work check-in switch plus interval
 * (goal_stalled_checkin_enabled / _minutes, 5–1440; OFF disables the minutes
 * input visually but keeps its value). GET always resolves all three; PUT is
 * the same partial update — a timer save sends only its own key, and the
 * saved-but-unapplied machinery (UnappliedValues) carries them like every
 * other Performance field.
 */

import { useState, useEffect, useRef, useCallback } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Cpu, Info, Warning, Target, Clock } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  fetchPerformanceSettings,
  updatePerformanceSettings,
  fetchMaxToolIterationsLoweringPreview,
  isMaxToolIterationsLoweringConflict,
  isPerformanceReloadFailed,
  performancePendingApplyMessage,
  getErrorMessage,
  type PerformanceSettingsUpdate,
} from '@/lib/api'
import type { MaxToolIterationAgentChange, PerformanceSettings } from '@/lib/api/generated/openapi-types'
import { useUiStore } from '@/store/ui'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'
import type { AutoSaveStatus } from '@/hooks/useAutoSave'
import { isReAuthCancelled } from './useReAuthGate'
import { useStepUp } from './useStepUp'
import {
  MaxToolIterationsCard,
  MAX_TOOL_ITERATIONS_MIN,
  MAX_TOOL_ITERATIONS_MAX,
  parseMaxToolIterations,
} from './MaxToolIterationsCard'
import { MaxToolIterationsLoweringDialog } from './MaxToolIterationsLoweringDialog'

// ── Skeleton ──────────────────────────────────────────────────────────────────

// The skeleton MUST carry text. Its bars are decorative divs with no text
// content, so a purely-visual skeleton makes the whole panel's innerText the
// empty string — indistinguishable, to a reader or a screen reader, from
// "there is nothing to configure here". That is exactly how this tab read
// during the retry window of a failing load (GET /api/v1/performance is
// retried three times with 1s/2s/4s backoff, so the panel sat textless for
// ~7 seconds before any error surfaced).
function Skeleton() {
  return (
    <Card
      role="status"
      aria-live="polite"
      data-testid="performance-loading"
      className="p-[var(--space-3)] space-y-[var(--space-2-5)]"
    >
      <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Loading performance settings…</p>
      <div className="space-y-[var(--space-2-5)] animate-pulse" aria-hidden="true">
        <div className="h-4 w-48 rounded bg-[var(--color-border)]" />
        <div className="h-3 w-full rounded bg-[var(--color-border)]" />
        <div className="h-3 w-2/3 rounded bg-[var(--color-border)]" />
      </div>
    </Card>
  )
}

// ── Component ─────────────────────────────────────────────────────────────────

// Autosave debounce: wait 600 ms of inactivity before opening the confirmation.
const AUTOSAVE_DEBOUNCE_MS = 600

// Validation message shared by every path that rejects an invalid
// max_parallel_agents input (triggerSave, the debounced autosave settle, and
// the tools_on_demand toggle guard). Bounds mirror the backend contract
// (contracts/components/schemas/PerformanceSettingsUpdate.yaml): minimum 0,
// no ceiling — 0 (or a blank field) clears the explicit cap, any
// positive integer is honored exactly as configured.
const INVALID_MAX_PARALLEL_MESSAGE =
  'max_parallel_agents must be zero or a positive whole number (0 or blank means no explicit cap — concurrency is then bounded by available memory).'

// PHYSICAL_THREAD_CEILING mirrors pkg/config/config.go's
// physicalConcurrencySafetyCeiling — the point above which the backend
// itself logs a WARN (Go's runtime hard-aborts the process past 10,000 OS
// threads; this leaves a 5x margin). It is NOT enforced as a limit — the
// backend honors any explicit value in full — so the frontend must only ever
// caution here, never claim the value will be lowered.
//
// It is ALSO the value effective_max_parallel_agents carries when nothing is
// configured, which is precisely why that case must never be rendered as a
// recommendation: the backstop answers "what would abort the Go runtime",
// not "what can this machine run". See max_parallel_agents_configured.
const PHYSICAL_THREAD_CEILING = 2000

// DEFAULT_GOAL_MAX_ROUNDS mirrors PerformanceSettings.yaml's documented
// default (20). goal_max_rounds is documented as "always present" on the
// wire, but this fallback exists for the same forward-compatibility reason
// max_parallel_agents_configured is tested with `!== false` above: an older
// backend that omits the field must not render a blank/undefined control.
const DEFAULT_GOAL_MAX_ROUNDS = 20

// Validation message for the single global goal-round budget
// (GOAL-FR-024/FR-045, D-D/D-E). Mirrors the backend contract
// (PerformanceSettingsUpdate.yaml): minimum 1, no ceiling other than the
// independent hard-divergence brake enforced server-side. There is NO
// per-goal override anywhere (GOAL-FR-046/US-8 retired) — this is the one
// and only budget control in the product, governing task goals and chat
// goals identically.
const INVALID_GOAL_MAX_ROUNDS_MESSAGE =
  'Tries per goal must be a whole number of at least 1.'

// C1 goal timers — bounds mirror PerformanceSettingsUpdate.yaml exactly:
// goal_reminder_quiet_seconds is an inclusive 30–3600 integer with NO off
// value (0 is invalid, not "off"), and goal_stalled_checkin_minutes is an
// inclusive 5–1440 integer that stays validated even while check-ins are
// switched off (there is no 0 sentinel there either). Both reject blanks,
// fractions and out-of-range values visibly; nothing is clamped or defaulted.
const DEFAULT_GOAL_QUIET_SECONDS = 60
const DEFAULT_STALLED_CHECKIN_MINUTES = 30

const INVALID_GOAL_QUIET_SECONDS_MESSAGE =
  'Idle reminder quiet window must be a whole number of seconds between 30 and 3600.'

const INVALID_STALLED_CHECKIN_MINUTES_MESSAGE =
  'Stalled-work check-in interval must be a whole number of minutes between 5 and 1440.'

// #904 tool-iteration limit (tool-iteration-limit-spec.md US-1 AS-3): the
// refusal names the bound, word for word as the server's 400 does.
const INVALID_MAX_TOOL_ITERATIONS_MESSAGE =
  `Max tool calls per turn must be between ${MAX_TOOL_ITERATIONS_MIN} and ${MAX_TOOL_ITERATIONS_MAX}.`

// The F4 result toast stays up longer than the 4 s default so the list of
// lowered agents can be read; the same text also stays inline under the field.
const LOWERED_TOAST_DURATION_MS = 10_000

function loweredSummaryText(agents: MaxToolIterationAgentChange[]): string {
  const list = agents.map((a) => `${a.agent_name} ${a.old_value} \u2192 ${a.new_value}`).join(', ')
  return `Lowered ${agents.length} ${agents.length === 1 ? 'agent' : 'agents'}: ${list}`
}

// Values a PUT saved to config.json whose in-memory refresh then failed
// (performance_reload_failed with stage `refresh`, or a body that did not say
// the stage): GET /performance keeps answering the OLD values until the
// gateway reloads or restarts, so the inputs show these instead and the
// tool-call limit is not compared against the stale in-memory global.
// C1: the three goal-timer keys ride the same machinery — a timer save
// reported as not applied keeps its input showing the saved value (and the
// retained minutes/switch state) until GET reports it.
type UnappliedValues = Pick<
  PerformanceSettingsUpdate,
  | 'max_parallel_agents'
  | 'tools_on_demand'
  | 'goal_max_rounds'
  | 'goal_reminder_quiet_seconds'
  | 'goal_stalled_checkin_enabled'
  | 'goal_stalled_checkin_minutes'
  | 'max_tool_iterations'
>

function savedValuesOf(body: PerformanceSettingsUpdate): UnappliedValues {
  const out: UnappliedValues = {}
  if (body.max_parallel_agents !== undefined) out.max_parallel_agents = body.max_parallel_agents
  if (body.tools_on_demand !== undefined) out.tools_on_demand = body.tools_on_demand
  if (body.goal_max_rounds !== undefined) out.goal_max_rounds = body.goal_max_rounds
  if (body.goal_reminder_quiet_seconds !== undefined) out.goal_reminder_quiet_seconds = body.goal_reminder_quiet_seconds
  if (body.goal_stalled_checkin_enabled !== undefined) out.goal_stalled_checkin_enabled = body.goal_stalled_checkin_enabled
  if (body.goal_stalled_checkin_minutes !== undefined) out.goal_stalled_checkin_minutes = body.goal_stalled_checkin_minutes
  if (body.max_tool_iterations !== undefined) out.max_tool_iterations = body.max_tool_iterations
  return out
}

// configuredMaxParallel is the configured cap GET reports (0 = none set) —
// see the input sync effect for why max_parallel_agents alone is not it.
function configuredMaxParallel(data: PerformanceSettings): number {
  return data.max_parallel_agents_configured === false ? 0 : (data.max_parallel_agents ?? 0)
}

// True once GET /performance reports every saved-but-unapplied value, i.e.
// the gateway has reloaded its configuration since. C1: GET always resolves
// the three timer keys, so a plain comparison is exact for them too.
function serverCaughtUp(u: UnappliedValues, data: PerformanceSettings): boolean {
  return (
    (u.max_parallel_agents === undefined || configuredMaxParallel(data) === u.max_parallel_agents) &&
    (u.tools_on_demand === undefined || (data.tools_on_demand ?? true) === u.tools_on_demand) &&
    (u.goal_max_rounds === undefined || data.goal_max_rounds === u.goal_max_rounds) &&
    (u.goal_reminder_quiet_seconds === undefined ||
      data.goal_reminder_quiet_seconds === u.goal_reminder_quiet_seconds) &&
    (u.goal_stalled_checkin_enabled === undefined ||
      data.goal_stalled_checkin_enabled === u.goal_stalled_checkin_enabled) &&
    (u.goal_stalled_checkin_minutes === undefined ||
      data.goal_stalled_checkin_minutes === u.goal_stalled_checkin_minutes) &&
    (u.max_tool_iterations === undefined || data.max_tool_iterations === u.max_tool_iterations)
  )
}

const UNKNOWN_LOWERED_TEXT =
  "Some agents' own tool-call limits may have been lowered, but the server's list could not be read — reload the page to see which."

// The D11 confirm dialog's state. `retryBody` carries the other fields of a
// PUT the server refused with a D16 drift 409, so confirming the fresh list
// resends them too instead of silently dropping them.
interface LoweringDialogState {
  value: number
  agents: MaxToolIterationAgentChange[]
  listChanged: boolean
  retryBody?: PerformanceSettingsUpdate
}

export function PerformanceSection(): React.ReactElement {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const stepUp = useStepUp()
  const [saveStatus, setSaveStatus] = useState<AutoSaveStatus>('idle')
  const [inputValue, setInputValue] = useState<string>('')
  // toolsOnDemand mirrors the tools_on_demand field. true = load on demand (default).
  const [toolsOnDemand, setToolsOnDemand] = useState<boolean>(true)
  // dirty tracks whether the user has changed any field since the last save.
  const [dirty, setDirty] = useState(false)

  // goalMaxRoundsInput mirrors the single global goal-tries setting
  // (GOAL-FR-024/FR-045, D-D/D-E) — the ONE budget control in the product,
  // governing task goals and chat goals identically. It is an independent
  // field from max_parallel_agents/tools_on_demand above: a different card,
  // saved via its own partial PUT body ({ goal_max_rounds }) so editing one
  // control never touches the other two. goalDirty is its own dirty flag for
  // the same reason.
  const [goalMaxRoundsInput, setGoalMaxRoundsInput] = useState<string>('')
  const [goalDirty, setGoalDirty] = useState(false)

  // C1 goal timers — three independent fields beside the goal budget, each
  // saved via its own partial PUT body so editing one never touches another:
  //   - the idle reminder quiet window (numeric, debounced autosave);
  //   - the stalled-work check-in switch (immediate, like tools_on_demand —
  //     it is the ONLY off switch, and there is deliberately no quiet-off);
  //   - the stalled-work check-in interval (numeric, debounced autosave,
  //     disabled while check-ins are off but keeping its value — bounds still
  //     apply while disabled, so editing is blocked rather than validation).
  const [quietSecondsInput, setQuietSecondsInput] = useState<string>('')
  const [quietDirty, setQuietDirty] = useState(false)
  const [stalledEnabled, setStalledEnabled] = useState<boolean>(true)
  const [stalledDirty, setStalledDirty] = useState(false)
  const [stalledMinutesInput, setStalledMinutesInput] = useState<string>('')
  const [stalledMinutesDirty, setStalledMinutesDirty] = useState(false)

  // #904 global tool-iteration limit — its own input, dirty flag and partial
  // PUT body ({ max_tool_iterations, confirmed_lowering }), like the goal
  // budget above. A change first asks the read-only preview endpoint which
  // agents it would lower (D11); if any, the confirm dialog lists them before
  // the step-up gate opens.
  const [toolIterInput, setToolIterInput] = useState<string>('')
  const [toolIterDirty, setToolIterDirty] = useState(false)
  const [toolIterError, setToolIterError] = useState<string | null>(null)
  const [lowering, setLowering] = useState<LoweringDialogState | null>(null)
  const [loweredSummary, setLoweredSummary] = useState<string | null>(null)
  // Saved-but-not-in-force values (see UnappliedValues; cleared by the next
  // fully applied save or once GET /performance reports them) and this page's
  // own notice text. The server's pending-apply state (GET /performance
  // pending_apply, #904) owns whether the notice shows. noticeSince is when
  // the last not-applied save answered: until GET has answered after it, the
  // page's own message stands in for the server's state.
  const [unapplied, setUnapplied] = useState<UnappliedValues | null>(null)
  const [unappliedNotice, setUnappliedNotice] = useState<string | null>(null)
  const [noticeSince, setNoticeSince] = useState(0)
  // Bumped on every edit so a preview answer for a superseded value is dropped.
  const previewSeqRef = useRef(0)
  const toolIterDebounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // The change waiting on the operator's confirmation, and whether the
  // confirmation is open (ADR-0008 ruling 6).
  // Shared across all three controls on this screen — only one save can be
  // in flight (and one dialog open) at a time.
  //
  // Review finding 15 (silent data loss): this is ONE slot but there are TWO
  // independent 600 ms debounces feeding it (max-parallel/tools-on-demand and
  // the goal round budget). Overwriting it dropped the earlier control's edit
  // *silently* — the PUT carried only the later body, `onSuccess` cleared BOTH
  // dirty flags, the sync effects then snapped the discarded field back to the
  // server value, and the indicator said "saved". A pending write must
  // therefore ACCUMULATE (`enqueuePending`), never replace: the wire type is a
  // genuine partial update ("Omitted = unchanged"), so a merged body saves
  // both intents in one round trip. The slot is a ref, not state: nothing
  // renders from it, and the debounce timers and mutation callbacks that read
  // it all run outside the render they were created in, where a state
  // snapshot would be stale.
  const pendingRef = useRef<PerformanceSettingsUpdate | null>(null)
  // True from gate-open until the gate settles; see openStepUp.
  const gateInFlightRef = useRef(false)

  // Draft generations (CG-CR-1 / CG-CR-2).
  //
  // Every user edit bumps the generation of the field it edited. The queued
  // body records the generation each field was written at, and the body handed
  // to the mutation carries those generations along. Two rules follow, and
  // both are needed for this save coordinator to stay truthful:
  //
  //   - a COMMIT may mark a control clean only while that control still shows
  //     the edit that was saved. While a PUT is in flight the operator can
  //     type a NEWER value into the same input; that edit is still on screen
  //     and must survive the older save, so its generation has moved on and
  //     the control keeps its dirty flag (the server-sync effect then leaves
  //     the newer value alone) — CG-CR-2.
  //   - a CANCELLED confirmation must leave the queue: its patch is dropped,
  //     so a later, unrelated save cannot apply the change the operator
  //     declined. An edit made after that confirmation opened is a NEWER
  //     draft, not the cancelled one: it stays visible and stays queued —
  //     CG-CR-1.
  const draftGenRef = useRef<Record<string, number>>({})
  const pendingGenRef = useRef<Record<string, number>>({})
  // What the open confirmation proposes to save, and the generation of each
  // field it carries. The cancel path needs both to tell the cancelled
  // change apart from a newer one.
  const gateProposalRef = useRef<{
    body: PerformanceSettingsUpdate
    gen: Record<string, number>
  } | null>(null)

  // Single writer for the pending slot.
  const setPendingPatch = useCallback((next: PerformanceSettingsUpdate | null) => {
    pendingRef.current = next
  }, [])

  // Merge a control's body into whatever is already queued instead of
  // replacing it (see the slot's comment above).
  const enqueuePending = useCallback((patch: PerformanceSettingsUpdate) => {
    const prev = pendingRef.current
    setPendingPatch(prev ? { ...prev, ...patch } : patch)
    // Record the generation each queued field was written at, so a commit or
    // a cancellation can tell "the edit that was saved" from "a newer edit
    // the operator has made since".
    const gens = pendingGenRef.current
    for (const key of Object.keys(patch)) {
      gens[key] = draftGenRef.current[key] ?? 0
    }
  }, [setPendingPatch])

  // Bump the draft generation of the field a user edit just changed.
  const bumpDraftGen = useCallback((key: string) => {
    draftGenRef.current[key] = (draftGenRef.current[key] ?? 0) + 1
  }, [])

  // Debounce timers for autosave — one per independently-saved control.
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const goalDebounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const quietDebounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const stalledMinutesDebounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const { data, dataUpdatedAt, isLoading, error, refetch, isFetching } = useQuery({
    queryKey: ['performance-settings'],
    queryFn: fetchPerformanceSettings,
    staleTime: 30_000,
  })

  // Sync inputs with fetched values on first load.
  useEffect(() => {
    if (data && !dirty) {
      // A saved-but-unapplied value wins over GET's stale in-memory one.
      // max_parallel_agents is NOT the configured value when nothing is
      // configured. The backend substitutes the resolved effective value
      // there, because 0 is an internal sentinel the schema forbids on the
      // wire (minimum: 1). So on an unconfigured install this field carries
      // the physical OS-thread backstop, and prefilling the input with it
      // would let an operator who saves without touching anything silently
      // turn a memory-bounded install into one with an explicit cap of 2000.
      // max_parallel_agents_configured is what distinguishes the two.
      const configured = unapplied?.max_parallel_agents ?? configuredMaxParallel(data)
      setInputValue(configured === 0 ? '' : String(configured))
      // tools_on_demand defaults to true when absent from the response.
      setToolsOnDemand(unapplied?.tools_on_demand ?? data.tools_on_demand ?? true)
    }
  }, [data, dirty, unapplied])

  // Sync the goal-round-budget input with the fetched value on first load —
  // a separate effect from the one above because it tracks its own dirty
  // flag (goalDirty), independent from max_parallel_agents/tools_on_demand.
  useEffect(() => {
    if (data && !goalDirty) {
      setGoalMaxRoundsInput(String(unapplied?.goal_max_rounds ?? data.goal_max_rounds ?? DEFAULT_GOAL_MAX_ROUNDS))
    }
  }, [data, goalDirty, unapplied])

  // C1: the three goal-timer controls sync the same way — one effect per
  // independently-saved field, gated on its own dirty flag. A saved-but-
  // unapplied timer value (UnappliedValues) wins over GET's stale in-memory
  // one; the numeric fallbacks mirror DEFAULT_GOAL_MAX_ROUNDS (GET always
  // resolves these on a current backend, but an older one that omits the
  // fields must not render a blank/undefined control).
  useEffect(() => {
    if (data && !quietDirty) {
      setQuietSecondsInput(
        String(unapplied?.goal_reminder_quiet_seconds ?? data.goal_reminder_quiet_seconds ?? DEFAULT_GOAL_QUIET_SECONDS),
      )
    }
  }, [data, quietDirty, unapplied])

  useEffect(() => {
    if (data && !stalledDirty) {
      setStalledEnabled(unapplied?.goal_stalled_checkin_enabled ?? data.goal_stalled_checkin_enabled ?? true)
    }
  }, [data, stalledDirty, unapplied])

  useEffect(() => {
    if (data && !stalledMinutesDirty) {
      setStalledMinutesInput(
        String(unapplied?.goal_stalled_checkin_minutes ?? data.goal_stalled_checkin_minutes ?? DEFAULT_STALLED_CHECKIN_MINUTES),
      )
    }
  }, [data, stalledMinutesDirty, unapplied])

  // Sync the tool-iteration input with the server's in-force global. No
  // literal fallback: an older backend that omits the field shows an empty
  // input rather than a number the runtime may not be using (spec FR-004).
  useEffect(() => {
    if (data && !toolIterDirty) {
      const shown = unapplied?.max_tool_iterations ?? data.max_tool_iterations
      setToolIterInput(shown === undefined ? '' : String(shown))
    }
  }, [data, toolIterDirty, unapplied])

  // GET now reports every saved value: the inputs stop overriding it.
  useEffect(() => {
    if (data && unapplied && serverCaughtUp(unapplied, data)) setUnapplied(null)
  }, [data, unapplied])

  // Once GET has answered since the last not-applied save, the server's
  // pending-apply state decides the notice: absent means everything saved is
  // in force, so the page's own text goes too.
  useEffect(() => {
    if (data && dataUpdatedAt >= noticeSince && !data.pending_apply) setUnappliedNotice(null)
  }, [data, dataUpdatedAt, noticeSince])

  // Clear a dirty flag ONLY for a control a committed PUT actually carried,
  // and only while the user has not re-edited that control since the body was
  // handed to the mutation. Clearing a dirty flag re-arms the sync effect
  // above, which overwrites the input with the server's value — doing that
  // for a field the PUT never sent is precisely the silent revert finding
  // 15 describes.
  //
  // CG-CR-2: "has not re-edited" is decided by the field's draft generation,
  // not by the queued patch alone. While a PUT is in flight the operator can
  // type a NEWER value into the same input (the numeric fields stay editable
  // during a save — that is existing behaviour and stays). That newer edit is
  // still on screen and must survive the older save: the generation captured
  // with the body is compared with the current one, and when it has moved on
  // the control keeps its dirty flag, so the server-sync effect does not
  // overwrite the newer value with the older saved one.
  const clearCommittedDirty = useCallback((saved: PerformanceSettingsUpdate, capturedGen?: Record<string, number>) => {
    const queued = pendingRef.current
    const gens = capturedGen ?? {}
    // True when the edit this body carried is still the one on screen. A
    // field the operator never edited has no generation of its own: the
    // queue records the baseline 0 for it, so both sides compare the same
    // baseline.
    const settled = (key: string) => {
      const atCapture = gens[key]
      if (atCapture === undefined) return true
      return atCapture === (draftGenRef.current[key] ?? 0)
    }
    const parallelSaved = 'max_parallel_agents' in saved || 'tools_on_demand' in saved
    const parallelRequeued =
      queued !== null && ('max_parallel_agents' in queued || 'tools_on_demand' in queued)
    if (parallelSaved && !parallelRequeued && settled('max_parallel_agents') && settled('tools_on_demand')) {
      setDirty(false)
    }
    if ('goal_max_rounds' in saved && !(queued !== null && 'goal_max_rounds' in queued) && settled('goal_max_rounds')) {
      setGoalDirty(false)
    }
    if ('goal_reminder_quiet_seconds' in saved && !(queued !== null && 'goal_reminder_quiet_seconds' in queued) && settled('goal_reminder_quiet_seconds')) {
      setQuietDirty(false)
    }
    if ('goal_stalled_checkin_enabled' in saved && !(queued !== null && 'goal_stalled_checkin_enabled' in queued) && settled('goal_stalled_checkin_enabled')) {
      setStalledDirty(false)
    }
    if ('goal_stalled_checkin_minutes' in saved && !(queued !== null && 'goal_stalled_checkin_minutes' in queued) && settled('goal_stalled_checkin_minutes')) {
      setStalledMinutesDirty(false)
    }
    if ('max_tool_iterations' in saved && !(queued !== null && 'max_tool_iterations' in queued) && settled('max_tool_iterations')) {
      setToolIterDirty(false)
    }
  }, [])

  // discardCancelledProposal removes a cancelled confirmation's patch from the
  // queue (CG-CR-1) and re-marks the affected controls clean, so the
  // cancelled edit is neither shown nor applied by a later, unrelated save.
  //
  // Two things are deliberately NOT discarded:
  //   - a field the operator edited AGAIN while the confirmation was open
  //     holds a NEWER value in the queue: that newer edit is not the
  //     cancelled change, so it stays queued and stays visible, and its own
  //     confirmation saves it;
  //   - the autosave timer of such a newer edit keeps running, so the newer
  //     value is still proposed on its own.
  // A timer that is still pending for a control the operator has NOT edited
  // since the confirmation opened belongs to the cancelled edit itself (any
  // later edit bumps that control's generation), so it is discarded — it
  // would otherwise re-propose the very value the operator just declined.
  const discardCancelledProposal = useCallback(() => {
    const proposal = gateProposalRef.current
    gateProposalRef.current = null
    if (!proposal) return
    const slot = pendingRef.current
    if (slot) {
      const next: PerformanceSettingsUpdate = { ...slot }
      let dropped = false
      for (const key of Object.keys(proposal.body) as (keyof PerformanceSettingsUpdate)[]) {
        // Drop only the value this confirmation proposed: when the queue
        // already holds a newer edit for that field, the newer one stays.
        if (next[key] === proposal.body[key]) {
          delete next[key]
          delete pendingGenRef.current[key]
          dropped = true
        }
      }
      if (dropped) setPendingPatch(Object.keys(next).length > 0 ? next : null)
    }
    const gens = proposal.gen
    // True when the operator has not edited that control since the
    // confirmation opened. A control the operator never edited has no
    // generation of its own: the queue recorded the baseline 0 for it, so
    // both sides compare the same baseline.
    const untouched = (key: string) => {
      const atOpen = gens[key]
      if (atOpen === undefined) return true
      return atOpen === (draftGenRef.current[key] ?? 0)
    }
    // The autosave timer that could still re-propose a cancelled value.
    const timerFor = (key: string): { current: ReturnType<typeof setTimeout> | null } | null => {
      switch (key) {
        case 'max_parallel_agents': return debounceRef
        case 'goal_max_rounds': return goalDebounceRef
        case 'goal_reminder_quiet_seconds': return quietDebounceRef
        case 'goal_stalled_checkin_minutes': return stalledMinutesDebounceRef
        case 'max_tool_iterations': return toolIterDebounceRef
        default: return null
      }
    }
    const discardTimer = (key: string) => {
      const timer = timerFor(key)
      if (timer?.current) {
        clearTimeout(timer.current)
        timer.current = null
      }
    }
    if ('max_parallel_agents' in proposal.body || 'tools_on_demand' in proposal.body) {
      if (untouched('max_parallel_agents') && untouched('tools_on_demand')) {
        setDirty(false)
        discardTimer('max_parallel_agents')
      }
    }
    if ('goal_max_rounds' in proposal.body && untouched('goal_max_rounds')) {
      setGoalDirty(false)
      discardTimer('goal_max_rounds')
    }
    if ('goal_reminder_quiet_seconds' in proposal.body && untouched('goal_reminder_quiet_seconds')) {
      setQuietDirty(false)
      discardTimer('goal_reminder_quiet_seconds')
    }
    if ('goal_stalled_checkin_enabled' in proposal.body && untouched('goal_stalled_checkin_enabled')) {
      setStalledDirty(false)
    }
    if ('goal_stalled_checkin_minutes' in proposal.body && untouched('goal_stalled_checkin_minutes')) {
      setStalledMinutesDirty(false)
      discardTimer('goal_stalled_checkin_minutes')
    }
    if ('max_tool_iterations' in proposal.body && untouched('max_tool_iterations')) {
      setToolIterDirty(false)
      discardTimer('max_tool_iterations')
    }
  }, [setPendingPatch])

  // F4: name the agents a save actually lowered — a 10 s status toast plus
  // the same text kept inline under the field.
  const reportLowered = useCallback((lowered: MaxToolIterationAgentChange[] | undefined) => {
    if (!lowered || lowered.length === 0) return
    const text = loweredSummaryText(lowered)
    addToast({ variant: 'success', message: text, duration: LOWERED_TOAST_DURATION_MS })
    setLoweredSummary(text)
  }, [addToast])

  const mutation = useMutation({
    // `gen` carries the draft generation of every field the body holds, so
    // the commit knows which edit it actually saved (see clearCommittedDirty).
    mutationFn: (variables: { body: PerformanceSettingsUpdate; token?: string; gen?: Record<string, number> }) =>
      updatePerformanceSettings(variables.body, variables.token),
    onSuccess: (result, variables) => {
      setSaveStatus('saved')
      // A successful PUT answers with the server's pending-apply state: only
      // when it is absent is whatever an earlier failed apply left pending in
      // force now. An unrelated save never clears the notice on its own.
      if (!result?.pending_apply) {
        setUnapplied(null)
        setUnappliedNotice(null)
      }
      clearCommittedDirty(variables.body, variables.gen)
      reportLowered(result?.max_tool_iterations_lowered_agents)
      // The slot was emptied when the body was handed over (onConfirmed),
      // so anything sitting in it now is a NEWER edit — leave it queued.
      void queryClient.invalidateQueries({ queryKey: ['performance-settings'] })
      // Reset to 'idle' after showing 'saved' briefly.
      setTimeout(() => setSaveStatus('idle'), 2000)
    },
    onError: (err, variables) => {
      // D16 drift: nothing was written because the set of agents to lower
      // changed since the preview. Re-open the dialog with the server's fresh
      // list; a new Confirm is required. Not an error toast — the dialog
      // itself announces the change.
      if (isMaxToolIterationsLoweringConflict(err)) {
        setSaveStatus('idle')
        // Keep the refused PUT's other fields; the value and confirmed list
        // come from the fresh preview when the admin confirms again.
        const rest: PerformanceSettingsUpdate = { ...variables.body }
        delete rest.max_tool_iterations
        delete rest.confirmed_lowering
        setLowering({
          value: err.preview.value,
          agents: err.preview.agents,
          listChanged: true,
          retryBody: rest,
        })
        return
      }
      // The write committed but is not in force yet: not a failed save. Say
      // so plainly and name the agents it lowered (from the error body — GET
      // /performance does not carry them). Stage `reload`: GET already shows
      // the new values, so re-read it. Stage `refresh` (or unknown): GET still
      // shows the OLD values, so the saved ones stay on show (UnappliedValues)
      // with a lasting notice, instead of the inputs snapping back to stale
      // numbers that look current.
      if (isPerformanceReloadFailed(err)) {
        setSaveStatus('idle')
        if (!err.inMemoryUpdated) {
          const saved = savedValuesOf(variables.body)
          setUnapplied((prev) => ({ ...prev, ...saved }))
        }
        setUnappliedNotice(err.userMessage)
        setNoticeSince(Date.now())
        clearCommittedDirty(variables.body, variables.gen)
        addToast({ variant: 'warning', message: err.userMessage, duration: LOWERED_TOAST_DURATION_MS })
        reportLowered(err.loweredAgents)
        if (err.loweredUnknown) setLoweredSummary(UNKNOWN_LOWERED_TEXT)
        void queryClient.invalidateQueries({ queryKey: ['performance-settings'] })
        return
      }
      if ('max_tool_iterations' in variables.body) {
        setToolIterError(getErrorMessage(err, 'Failed to save the tool-call limit.'))
      }
      setSaveStatus('error')
      // Deliberately does NOT clear the slot or the dirty flags: the slot was
      // already emptied at hand-over, the inputs still hold the unsaved edit,
      // and the sr-only "Save changes" escape hatch stays available to retry.
      const msg = getErrorMessage(err, 'Failed to save performance settings.')
      addToast({ variant: 'error', message: msg })
    },
  })

  // buildBody constructs the full update payload using the latest local state.
  // Both fields are always sent together so neither reverts when only one changes.
  const buildBody = useCallback((
    rawInput: string,
    onDemand: boolean,
  ): PerformanceSettingsUpdate | null => {
    const raw = rawInput.trim()
    const parsed = raw === '' ? 0 : parseInt(raw, 10)
    // Backend bound (PerformanceSettingsUpdate.yaml): minimum 0, no ceiling.
    // 0 (or blank, which we treat as 0 above) means "no explicit cap";
    // any positive integer is honored exactly as configured — there is no
    // upper limit to validate against here.
    if (raw !== '' && (isNaN(parsed) || parsed < 0)) return null
    return { max_parallel_agents: parsed, tools_on_demand: onDemand }
  }, [])

  // buildGoalBody constructs the partial-update payload for the single
  // global goal-round budget alone (GOAL-FR-024/FR-045, D-D/D-E). Unlike
  // buildBody above, this sends ONLY goal_max_rounds — PerformanceSettingsUpdate
  // is a genuine partial update ("Omitted = unchanged") and this control has
  // no relationship to max_parallel_agents/tools_on_demand, so there is no
  // "revert on save" risk in sending it alone. Minimum is 1, no 0-sentinel
  // (unlike max_parallel_agents, there is no "clear to automatic" meaning
  // here — the backend always resolves a value, default 20).
  const buildGoalBody = useCallback((rawInput: string): PerformanceSettingsUpdate | null => {
    const raw = rawInput.trim()
    if (raw === '') return null
    const parsed = parseInt(raw, 10)
    if (isNaN(parsed) || parsed < 1) return null
    return { goal_max_rounds: parsed }
  }, [])

  // C1 builders, one per timer control, in buildGoalBody's shape: a
  // single-key partial body, or null when the input is out of range — which
  // the caller surfaces visibly (never clamped, never defaulted). Number()
  // + Number.isInteger (not parseInt) so a fraction like "30.5" is REJECTED
  // rather than silently truncated to 30 — the backend rejects fractions and
  // the UI must never round one into a save. Blank means "no edit" here, not
  // 0: both keys have no off value.
  const buildQuietSecondsBody = useCallback((rawInput: string): PerformanceSettingsUpdate | null => {
    const raw = rawInput.trim()
    if (raw === '') return null
    const parsed = Number(raw)
    if (!Number.isInteger(parsed) || parsed < 30 || parsed > 3600) return null
    return { goal_reminder_quiet_seconds: parsed }
  }, [])

  const buildStalledMinutesBody = useCallback((rawInput: string): PerformanceSettingsUpdate | null => {
    const raw = rawInput.trim()
    if (raw === '') return null
    const parsed = Number(raw)
    if (!Number.isInteger(parsed) || parsed < 5 || parsed > 1440) return null
    return { goal_stalled_checkin_minutes: parsed }
  }, [])

  // openStepUp hands the queued patch (pendingRef, accumulated by
  // enqueuePending above) to the step-up gate (ADR-0010 WP3): ReAuthDialog +
  // a replayed consent token in local mode, ConfirmDialog with no token in
  // platform mode. The `stepUp.open` guard mirrors the old `!confirmOpen`
  // check the escape-hatch buttons below still use: while a dialog is
  // already visible, a further debounced edit merges into pendingRef (via
  // enqueuePending) instead of opening a second, overlapping prompt — the
  // one dialog, when confirmed, reads whatever is in the slot AT THAT TIME.
  const openStepUp = useCallback(() => {
    // Two guards, one per timing: `stepUp.open` is state and lags a render,
    // so two debounces landing in the same tick would both see it false —
    // gateInFlightRef is set synchronously and closes that window. A second
    // edit while a gate is in flight has already merged into pendingRef via
    // enqueuePending; the one open dialog reads the slot at confirm time.
    if (stepUp.open || gateInFlightRef.current) return
    gateInFlightRef.current = true
    // What this confirmation proposes to save (CG-CR-1): the queue as it
    // stands now, with the generation of each field. If the operator
    // cancels, this patch must leave the queue — otherwise the next
    // confirmed control merges it and the cancelled change is applied
    // without the operator ever seeing it.
    gateProposalRef.current = {
      body: pendingRef.current ? { ...pendingRef.current } : {},
      gen: { ...pendingGenRef.current },
    }
    // capturedBody is read from the pending slot lazily, on run()'s FIRST
    // invocation — never here, synchronously, at gate-open time. Two
    // reasons this has to be lazy, one per mode:
    //   - confirm mode: run() fires once, at the operator's actual confirm
    //     click, which may be well after a SECOND debounced edit has already
    //     merged into pendingRef (review finding 15 — one shared slot, two
    //     independent 600ms debounces). Reading eagerly here would miss it.
    //   - password mode: useStepUp opens ReAuthDialog first and calls run()
    //     exactly once, with the minted token, at the operator's confirm —
    //     the same late moment as confirm mode.
    // "Read once, on first invocation, reuse after" keeps a retry (should a
    // gate ever call run() twice) submitting the SAME body.
    let captured = false
    let capturedBody: PerformanceSettingsUpdate | null = null
    let capturedGen: Record<string, number> = {}
    const runOnce = (token?: string) => {
      if (!captured) {
        captured = true
        capturedBody = pendingRef.current
        // The generation each field was written at, so the commit knows
        // whether the edit it saved is still the one on screen (CG-CR-2).
        capturedGen = { ...pendingGenRef.current }
        // Empty the slot as the body is handed over, so an edit made while
        // this gate/PUT cycle is in flight accumulates on its own and is
        // not cleared by this PUT's onSuccess.
        setPendingPatch(null)
        pendingGenRef.current = {}
      }
      if (!capturedBody) return Promise.resolve(undefined)
      return mutation.mutateAsync({ body: capturedBody, token, gen: capturedGen })
    }
    void stepUp
      .gate(
        runOnce,
        {
          title: 'Change the performance settings?',
          body: 'This changes how many agents Omnipus runs at once, whether tools are loaded on demand, how many tries a goal gets, the goal reminder and check-in timers, and how many tool calls an agent may make per turn. It takes effect without a restart; the timers apply on the next sweep.',
          confirmLabel: 'Change performance settings',
        },
      )
      .catch((err: unknown) => {
        if (isReAuthCancelled(err)) {
          // Cancelling means the pending change was never sent. The
          // cancelled patch leaves the queue (CG-CR-1) and the controls it
          // carried are marked clean, so the sync effect above
          // (`data && !dirty`) re-applies the last-known-good server values
          // — otherwise the switch/input would keep showing the unsaved edit
          // indefinitely, until the user happened to change it again. A
          // control the operator edited again while the confirmation was
          // open keeps its newer value: that edit is not the cancelled one.
          setSaveStatus('idle')
          discardCancelledProposal()
        }
        // A real save failure already surfaced its toast via the mutation's
        // onError above.
      })
      .finally(() => {
        gateInFlightRef.current = false
      })
  }, [stepUp, mutation, setPendingPatch, discardCancelledProposal])

  // triggerSave validates the current input and opens the step-up gate.
  const triggerSave = useCallback(() => {
    const body = buildBody(inputValue, toolsOnDemand)
    if (!body) {
      addToast({ variant: 'error', message: INVALID_MAX_PARALLEL_MESSAGE })
      return
    }
    enqueuePending(body)
    openStepUp()
  }, [inputValue, toolsOnDemand, buildBody, addToast, enqueuePending, openStepUp])

  // triggerGoalSave mirrors triggerSave for the independent goal-round-budget
  // control — the keyboard-accessible escape hatch for its own sr-only button.
  const triggerGoalSave = useCallback(() => {
    const body = buildGoalBody(goalMaxRoundsInput)
    if (!body) {
      addToast({ variant: 'error', message: INVALID_GOAL_MAX_ROUNDS_MESSAGE })
      return
    }
    enqueuePending(body)
    openStepUp()
  }, [goalMaxRoundsInput, buildGoalBody, addToast, enqueuePending, openStepUp])

  // Autosave: debounce on input change then open the step-up gate.
  function handleInputChange(value: string) {
    setInputValue(value)
    setDirty(true)
    bumpDraftGen('max_parallel_agents')
    setSaveStatus('idle')
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => {
      const body = buildBody(value, toolsOnDemand)
      if (body) {
        setSaveStatus('saving')
        enqueuePending(body)
        openStepUp()
      } else {
        // max_parallel_agents settled out of range — this path never goes
        // through triggerSave, so without this branch the debounce would
        // silently no-op: no toast, no indication the value wasn't saved.
        // Mirror the same guidance triggerSave and the toggle path show.
        setSaveStatus('idle')
        addToast({ variant: 'error', message: INVALID_MAX_PARALLEL_MESSAGE })
      }
    }, AUTOSAVE_DEBOUNCE_MS)
  }

  // Autosave for the independent goal-round-budget control: debounce, then
  // save ONLY { goal_max_rounds } — see buildGoalBody's comment for why this
  // never touches the other two fields' saved state.
  function handleGoalInputChange(value: string) {
    setGoalMaxRoundsInput(value)
    setGoalDirty(true)
    bumpDraftGen('goal_max_rounds')
    setSaveStatus('idle')
    if (goalDebounceRef.current) clearTimeout(goalDebounceRef.current)
    goalDebounceRef.current = setTimeout(() => {
      const body = buildGoalBody(value)
      if (body) {
        setSaveStatus('saving')
        enqueuePending(body)
        openStepUp()
      } else {
        setSaveStatus('idle')
        addToast({ variant: 'error', message: INVALID_GOAL_MAX_ROUNDS_MESSAGE })
      }
    }, AUTOSAVE_DEBOUNCE_MS)
  }

  // C1 timer controls, in handleGoalInputChange's shape: the two numeric
  // fields debounce into their own single-key PUT (an out-of-range settle is
  // named on an error toast and saves nothing), and the stalled switch fires
  // immediately like tools_on_demand — a toggle is an unambiguous action with
  // nothing to settle. The switch body carries ONLY the boolean, so turning
  // check-ins off never rewrites the retained minutes, and the minutes input
  // is simply disabled while off (bounds still apply server-side even then).
  function handleQuietSecondsChange(value: string) {
    setQuietSecondsInput(value)
    setQuietDirty(true)
    bumpDraftGen('goal_reminder_quiet_seconds')
    setSaveStatus('idle')
    if (quietDebounceRef.current) clearTimeout(quietDebounceRef.current)
    quietDebounceRef.current = setTimeout(() => {
      const body = buildQuietSecondsBody(value)
      if (body) {
        setSaveStatus('saving')
        enqueuePending(body)
        openStepUp()
      } else {
        setSaveStatus('idle')
        addToast({ variant: 'error', message: INVALID_GOAL_QUIET_SECONDS_MESSAGE })
      }
    }, AUTOSAVE_DEBOUNCE_MS)
  }

  function handleStalledMinutesChange(value: string) {
    setStalledMinutesInput(value)
    setStalledMinutesDirty(true)
    bumpDraftGen('goal_stalled_checkin_minutes')
    setSaveStatus('idle')
    if (stalledMinutesDebounceRef.current) clearTimeout(stalledMinutesDebounceRef.current)
    stalledMinutesDebounceRef.current = setTimeout(() => {
      const body = buildStalledMinutesBody(value)
      if (body) {
        setSaveStatus('saving')
        enqueuePending(body)
        openStepUp()
      } else {
        setSaveStatus('idle')
        addToast({ variant: 'error', message: INVALID_STALLED_CHECKIN_MINUTES_MESSAGE })
      }
    }, AUTOSAVE_DEBOUNCE_MS)
  }

  function handleStalledEnabledChange(checked: boolean) {
    setStalledEnabled(checked)
    setStalledDirty(true)
    bumpDraftGen('goal_stalled_checkin_enabled')
    setSaveStatus('saving')
    enqueuePending({ goal_stalled_checkin_enabled: checked })
    openStepUp()
  }

  // Escape hatches for the two C1 numeric controls, mirroring
  // triggerGoalSave: validate now and open the step-up gate.
  const triggerQuietSecondsSave = useCallback(() => {
    const body = buildQuietSecondsBody(quietSecondsInput)
    if (!body) {
      addToast({ variant: 'error', message: INVALID_GOAL_QUIET_SECONDS_MESSAGE })
      return
    }
    enqueuePending(body)
    openStepUp()
  }, [quietSecondsInput, buildQuietSecondsBody, addToast, enqueuePending, openStepUp])

  const triggerStalledMinutesSave = useCallback(() => {
    const body = buildStalledMinutesBody(stalledMinutesInput)
    if (!body) {
      addToast({ variant: 'error', message: INVALID_STALLED_CHECKIN_MINUTES_MESSAGE })
      return
    }
    enqueuePending(body)
    openStepUp()
  }, [stalledMinutesInput, buildStalledMinutesBody, addToast, enqueuePending, openStepUp])

  // settleToolIter runs once the tool-iteration input settles (debounce or
  // the sr-only Save button): validate the 1–1000 bound, then:
  //   - a raise (or the same value, repairing the file, D13) goes straight to
  //     the step-up gate with no preview and no confirmed_lowering — a raise
  //     never rewrites any agent (D20): an agent whose own value is above the
  //     new global keeps it and stays capped and flagged;
  //   - a lowering asks the preview endpoint which agents it would lower,
  //     then opens the D11 confirm dialog (some agents) or the gate (none).
  // With no in-force value to compare against (an older backend omitting
  // it), a raise cannot be told from a lowering, so the preview is asked.
  const settleToolIter = useCallback((raw: string) => {
    const parsed = parseMaxToolIterations(raw)
    if (parsed === null) {
      setSaveStatus('idle')
      setToolIterError(INVALID_MAX_TOOL_ITERATIONS_MESSAGE)
      return
    }
    // Unchanged from the value in force — nothing to save, unless the value
    // saved in config.json is missing or out of range (D13): then saving the
    // in-force number is exactly how the admin repairs the file.
    // After a failed refresh the saved value is the baseline; the in-memory
    // global GET reports is stale, so a raise cannot be told from a lowering
    // against it — the preview (the server's own answer) is asked instead.
    const unappliedLimit = unapplied?.max_tool_iterations
    const savedOk = unappliedLimit !== undefined || (data?.max_tool_iterations_saved_state ?? 'ok') === 'ok'
    if (parsed === (unappliedLimit ?? data?.max_tool_iterations) && savedOk) {
      setSaveStatus('idle')
      setToolIterDirty(false)
      return
    }
    const seq = ++previewSeqRef.current
    setSaveStatus('saving')
    const inForce = unappliedLimit === undefined ? data?.max_tool_iterations : undefined
    if (inForce !== undefined && parsed >= inForce) {
      enqueuePending({ max_tool_iterations: parsed })
      openStepUp()
      return
    }
    fetchMaxToolIterationsLoweringPreview(parsed).then(
      (preview) => {
        if (seq !== previewSeqRef.current) return
        if (preview.agents.length > 0) {
          setSaveStatus('idle')
          setLowering({ value: parsed, agents: preview.agents, listChanged: false })
          return
        }
        enqueuePending({ max_tool_iterations: parsed, confirmed_lowering: [] })
        openStepUp()
      },
      (err: unknown) => {
        if (seq !== previewSeqRef.current) return
        // Nothing is saved when the affected agents cannot be listed.
        setSaveStatus('error')
        setToolIterError(
          `Could not check which agents this would affect, so nothing was saved: ${getErrorMessage(err, 'unknown error')}`,
        )
      },
    )
  }, [data?.max_tool_iterations, data?.max_tool_iterations_saved_state, unapplied?.max_tool_iterations, enqueuePending, openStepUp])

  function handleToolIterChange(value: string) {
    setToolIterInput(value)
    setToolIterDirty(true)
    bumpDraftGen('max_tool_iterations')
    setToolIterError(null)
    setLoweredSummary(null)
    setLowering(null)
    setSaveStatus('idle')
    previewSeqRef.current += 1
    if (toolIterDebounceRef.current) clearTimeout(toolIterDebounceRef.current)
    toolIterDebounceRef.current = setTimeout(() => settleToolIter(value), AUTOSAVE_DEBOUNCE_MS)
  }

  function triggerToolIterSave() {
    if (toolIterDebounceRef.current) clearTimeout(toolIterDebounceRef.current)
    settleToolIter(toolIterInput)
  }

  // Confirm in the D11 dialog: queue the exact snapshot the admin saw (D16 —
  // the server compares it as a set) and open the step-up gate.
  function confirmLowering() {
    if (!lowering) return
    const { value, agents, retryBody } = lowering
    setLowering(null)
    setSaveStatus('saving')
    enqueuePending({
      ...retryBody,
      max_tool_iterations: value,
      confirmed_lowering: agents.map((a) => ({ agent_id: a.agent_id, old_value: a.old_value })),
    })
    openStepUp()
  }

  // Cancel writes nothing for the limit (US-6 AS-2); the field returns to the
  // saved value. After a D16 drift the dialog also holds the refused PUT's
  // OTHER fields (retryBody) — their inputs are still dirty, so they are
  // re-sent on their own (behind the step-up gate) rather than dropped with
  // a dirty input nothing will ever save. Cancelling that gate resets them.
  function cancelLowering() {
    const retryBody = lowering?.retryBody
    setLowering(null)
    setSaveStatus('idle')
    setToolIterDirty(false)
    if (retryBody && Object.keys(retryBody).length > 0) {
      setSaveStatus('saving')
      enqueuePending(retryBody)
      openStepUp()
    }
  }

  // handleToolsOnDemandChange fires immediately (no debounce) — a toggle is an
  // unambiguous user action that doesn't need a settling delay.
  function handleToolsOnDemandChange(checked: boolean) {
    const previousToolsOnDemand = toolsOnDemand
    setToolsOnDemand(checked)
    setDirty(true)
    bumpDraftGen('tools_on_demand')
    if (debounceRef.current) clearTimeout(debounceRef.current)
    const body = buildBody(inputValue, checked)
    if (body) {
      setSaveStatus('saving')
      enqueuePending(body)
      openStepUp()
    } else {
      // max_parallel_agents is out of range — the toggle can't proceed until
      // the numeric field is fixed. Revert the switch (it was optimistically
      // flipped above but nothing will be saved), clear any stale 'saving'
      // spinner so it doesn't stick forever, and tell the user why.
      setToolsOnDemand(previousToolsOnDemand)
      setSaveStatus('idle')
      addToast({ variant: 'error', message: INVALID_MAX_PARALLEL_MESSAGE })
    }
  }

  // Cleanup debounce timer on unmount.
  useEffect(() => {
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current)
      if (goalDebounceRef.current) clearTimeout(goalDebounceRef.current)
      if (quietDebounceRef.current) clearTimeout(quietDebounceRef.current)
      if (stalledMinutesDebounceRef.current) clearTimeout(stalledMinutesDebounceRef.current)
      if (toolIterDebounceRef.current) clearTimeout(toolIterDebounceRef.current)
    }
  }, [])

  if (isLoading) return <Skeleton />
  if (error) {
    // State the underlying cause, and give the reader a way out. This mirrors
    // the sibling tabs on this same screen — Security's SkillTrustSection
    // renders "Failed to load skill trust settings: <cause>", and Gateway
    // banners the reason — rather than a bare sentence that tells an operator
    // nothing about whether to wait, re-authenticate, or check the backend.
    return (
      <div
        role="alert"
        data-testid="performance-load-error"
        className="rounded-lg border border-[var(--color-error)]/40 bg-[var(--color-error)]/10 p-[var(--space-3)] space-y-[var(--space-2)]"
      >
        <div className="flex items-start gap-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">
          <Warning size={16} className="mt-[var(--space-0-5)] shrink-0" />
          <span>
            Failed to load performance settings: {getErrorMessage(error, 'Unknown error')}
          </span>
        </div>
        <Button
          variant="link"
          type="button"
          data-testid="performance-retry-btn"
          onClick={() => void refetch()}
          disabled={isFetching}
          className="text-[length:var(--type-utility-xs-size)] font-medium text-[var(--color-secondary)] underline hover:text-[var(--color-secondary)]"
        >
          {isFetching ? 'Retrying…' : 'Retry'}
        </Button>
      </div>
    )
  }

  const effective = data?.effective_max_parallel_agents ?? '?'

  // Is anything actually configured?
  //
  // This is the whole point of the max_parallel_agents_configured field.
  // There is no longer a computed default: when nothing is configured, the
  // backend bounds concurrency by LIVE available memory at the moment each
  // agent turn is admitted, and effective_max_parallel_agents carries a
  // PHYSICAL OS-thread-safety backstop (2000) rather than a capacity
  // estimate. Rendering that integer under the words "Recommended" told
  // every operator on an unconfigured install that the system recommends
  // 2000 parallel agents — a number nothing in the process was claiming.
  //
  // Note the `!== false` rather than a truthy check: the field is optional on
  // the wire, and an older backend that omits it should keep the previous
  // (integer) rendering rather than silently switching every install to the
  // automatic text.
  const isConfigured = data?.max_parallel_agents_configured !== false
  const recommended =
    isConfigured && typeof effective === 'number' ? effective : undefined

  // High-value caution: when the typed value exceeds the physical OS-thread
  // safety ceiling the backend itself warns about (physicalConcurrencySafetyCeiling,
  // pkg/config/config.go), surface an inline caution. This does NOT mean the
  // value will be lowered — explicit values have no ceiling and are always
  // honored exactly as configured — it only flags the real thread-exhaustion
  // risk the backend's own WARN log calls out at the same threshold.
  const inputValueNum = inputValue.trim() === '' ? null : parseInt(inputValue, 10)
  const exceedsPhysicalCeiling =
    inputValueNum !== null &&
    !isNaN(inputValueNum) &&
    inputValueNum > PHYSICAL_THREAD_CEILING

  // The not-applied notice (#904): shown while the server reports pending
  // settings, or — right after a not-applied save, before GET has answered
  // again — on this page's own failure message. Its text prefers that
  // message (it carries the server's reason); otherwise, e.g. after a page
  // reload, it names the settings the server reports as pending.
  const pendingApply = data?.pending_apply ?? null
  const awaitingServer = unappliedNotice !== null && dataUpdatedAt < noticeSince
  const noticeText = unappliedNotice ?? (pendingApply ? performancePendingApplyMessage(pendingApply) : null)
  const notice = (awaitingServer || pendingApply) && noticeText
    ? { text: noticeText, fieldsShowSaved: pendingApply?.stage !== 'refresh' || unapplied !== null }
    : null

  const recommendationText =
    typeof recommended === 'number'
      ? `Currently in use: ${recommended} parallel agents`
      : 'automatic \u2014 bounded by available memory'

  return (
    <div className="space-y-[var(--space-3)]">
      {/* Header */}
      <div className="flex items-center justify-between gap-[var(--space-2)]">
        <div className="flex items-center gap-[var(--space-2)]">
          <Cpu size={18} className="text-[var(--color-secondary)]" />
          <h2 className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">Agent Concurrency</h2>
        </div>
        <AutoSaveIndicator status={saveStatus} />
      </div>

      {notice && (
        <Card
          variant="inset"
          role="status"
          data-testid="performance-unapplied-notice"
          className="p-[var(--space-2-5)] flex items-start gap-[var(--space-2)]"
        >
          <Warning size={14} className="text-[var(--color-warning)] mt-[var(--space-0-5)] shrink-0" aria-hidden />
          <div className="flex-1 min-w-0">
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-secondary)] leading-relaxed">
              {notice.text}
            </p>
            <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] mt-[var(--space-0-5)]">
              {notice.fieldsShowSaved
                ? 'The fields below show the saved values. Until the restart or reload, Omnipus keeps running on the previous ones.'
                : 'The fields below still show the values Omnipus is running on; the saved ones take effect after the restart or reload.'}{' '}
              A new tool-call limit set here is checked against the saved limit; an agent{'\u2019'}s own limit, set on
              its profile, is still checked against the running one.
            </p>
          </div>
        </Card>
      )}

      {/* Live concurrency card — shown above the input */}
      <Card variant="inset" className="p-[var(--space-2-5)] flex items-start gap-[var(--space-2)]">
        <Info size={14} className="text-[var(--color-accent)] mt-[var(--space-0-5)] shrink-0" />
        <div className="flex-1 min-w-0">
          <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-secondary)] leading-relaxed">
            {recommendationText}
          </p>
          <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] mt-[var(--space-0-5)]">
            {isConfigured
              ? 'An explicit value has no ceiling — it is always honored exactly as set. Agent turns are still admitted only while the host has memory to spare.'
              : 'Nothing is configured, so concurrency is bounded by this host\u2019s available memory at the moment each agent turn starts. Set a value below to cap it explicitly instead.'}
          </p>
        </div>
      </Card>

      {/* Concurrency card */}
      <Card className="p-[var(--space-3)] space-y-[var(--space-3)]">
        <div className="space-y-[var(--space-1)]">
          <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] leading-relaxed">
            Controls how many tasks and subagents may run concurrently across all agents.
            Leave blank for no explicit cap — concurrency is then bounded by available memory. Changes apply once you confirm them.
          </p>
          <div className="flex items-center gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
            <Info size={12} />
            <span>
              Effective value in use:{' '}
              <span className="font-mono font-medium text-[var(--color-secondary)]">
                {isConfigured ? effective : 'automatic'}
              </span>
            </span>
          </div>
        </div>

        <div className="flex items-center gap-[var(--space-2-5)]">
          <Label
            htmlFor="performance-max-agents"
            className="text-[length:var(--type-utility-xs-size)] font-medium leading-[var(--font-line-height-body)] text-[var(--color-secondary)] w-44 shrink-0"
          >
            Max parallel agents
          </Label>
          <Input
            id="performance-max-agents"
            type="number"
            min={0}
            placeholder="auto"
            value={inputValue}
            onChange={(e) => handleInputChange(e.target.value)}
            className="w-24 h-7 text-[length:var(--type-body-compact-size)]"
            aria-label="Max parallel agents"
            data-testid="performance-max-agents-input"
          />
        </div>

        {/* High-value caution — yellow inline notice. Unlike the old
            over-limit warning this replaces, it does NOT claim the value
            will be lowered: explicit values have no ceiling and are always
            honored exactly as configured. It only surfaces the same
            thread-exhaustion risk the backend's own WARN log calls out
            above physicalConcurrencySafetyCeiling. */}
        {exceedsPhysicalCeiling && (
          <div
            data-testid="performance-high-value-warning"
            className="flex items-start gap-[var(--space-2)] p-[var(--space-2)] rounded-md border border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 text-[length:var(--type-utility-xs-size)] text-[var(--color-warning)]"
          >
            <Warning size={14} className="mt-[var(--space-0-5)] shrink-0" />
            <span>
              {inputValueNum} will be honored exactly as set — there is no ceiling — but values
              this high risk Go runtime thread exhaustion, which aborts the process. The
              backend logs a warning above {PHYSICAL_THREAD_CEILING}; consider whether you
              really need this many concurrent agents.
            </span>
          </div>
        )}
      </Card>

      {/* Tool loading card */}
      <Card className="p-[var(--space-3)] space-y-[var(--space-2-5)]">
        {/* Section heading */}
        <div className="flex items-center gap-[var(--space-2)]">
          <h3 className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">Tool loading</h3>
        </div>

        {/* Toggle row */}
        <div className="flex items-start justify-between gap-[var(--space-3)]">
          <div className="flex-1 min-w-0">
            {toolsOnDemand ? (
              <>
                <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">Load tools on demand</p>
                <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] mt-[var(--space-0-5)]">
                  Smaller messages, lower token use. Recommended.
                </p>
              </>
            ) : (
              <>
                <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">Keep all tools loaded</p>
                <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] mt-[var(--space-0-5)]">
                  Every tool is always available — no loading step, but larger messages.
                </p>
              </>
            )}
          </div>
          <Switch
            checked={toolsOnDemand}
            onCheckedChange={handleToolsOnDemandChange}
            disabled={mutation.isPending}
            aria-label="Tool loading"
            data-testid="performance-tools-on-demand-switch"
          />
        </div>

        {/* Helper text */}
        <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] leading-relaxed">
          Applies to all agents. Takes effect on the next message — no restart required.
          Changes apply once you confirm them.
        </p>
      </Card>

      {/* Goal completion budget card (GOAL-FR-024/FR-045, D-D/D-E) — the ONE
          global goal-tries setting in the product. There is no per-goal
          override anywhere: not on the task detail panel, not in chat, not
          on the wire, not in the store. This single control governs task
          goals and chat goals identically. */}
      <Card className="p-[var(--space-3)] space-y-[var(--space-2-5)]">
        <div className="flex items-center gap-[var(--space-2)]">
          <Target size={16} className="text-[var(--color-secondary)]" />
          <h3 className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">Goal completion budget</h3>
        </div>

        <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] leading-relaxed">
          How many times an agent may try to finish a goal before it stops and reports the
          goal as not met. This applies to goals set in chat and to goals on tasks.
        </p>

        <div className="flex items-center gap-[var(--space-2-5)]">
          <Label
            htmlFor="performance-goal-max-rounds"
            className="text-[length:var(--type-utility-xs-size)] font-medium leading-[var(--font-line-height-body)] text-[var(--color-secondary)] w-44 shrink-0"
          >
            Tries per goal
          </Label>
          <Input
            id="performance-goal-max-rounds"
            type="number"
            min={1}
            value={goalMaxRoundsInput}
            onChange={(e) => handleGoalInputChange(e.target.value)}
            className="w-24 h-7 text-[length:var(--type-body-compact-size)]"
            aria-label="Tries per goal"
            data-testid="performance-goal-max-rounds-input"
          />
        </div>

        <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] leading-relaxed">
          Goals already running keep the limit they started with. Saving asks you to
          confirm the change.
        </p>
      </Card>

      {/* C1 goal timers — beside the goal completion budget (docs/settings.md
          "Goal timers"). Two independent timers over the same fixed 30-second
          keeper sweep, live without a restart: the idle reminder quiet window
          (no off value — 0 is invalid, not "off") and the stalled-work
          check-in switch with its retained interval. Turning check-ins off
          disables the minutes input visually but keeps its value; the bounds
          still apply even while off, so the input is blocked rather than
          re-validated into a different number. */}
      <Card className="p-[var(--space-3)] space-y-[var(--space-2-5)]">
        <div className="flex items-center gap-[var(--space-2)]">
          <Clock size={16} className="text-[var(--color-secondary)]" />
          <h3 className="text-[length:var(--type-body-compact-size)] font-semibold text-[var(--color-secondary)]">Goal timers</h3>
        </div>

        <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] leading-relaxed">
          How long a goal waits before another automatic reminder, and how long busy background
          work is observed before one stalled-work check-in. Both timers are independent, run on
          the same fixed 30-second sweep, and take effect without a restart.
        </p>

        <div className="flex items-center gap-[var(--space-2-5)]">
          <Label
            htmlFor="performance-goal-quiet-seconds"
            className="text-[length:var(--type-utility-xs-size)] font-medium leading-[var(--font-line-height-body)] text-[var(--color-secondary)] w-44 shrink-0"
          >
            Idle reminder quiet window (seconds)
          </Label>
          <Input
            id="performance-goal-quiet-seconds"
            type="number"
            min={30}
            max={3600}
            step={1}
            value={quietSecondsInput}
            onChange={(e) => handleQuietSecondsChange(e.target.value)}
            className="w-24 h-7 text-[length:var(--type-body-compact-size)]"
            aria-label="Idle reminder quiet window (seconds)"
            data-testid="performance-goal-quiet-seconds-input"
          />
        </div>

        <div className="flex items-start justify-between gap-[var(--space-3)]">
          <div className="flex-1 min-w-0">
            <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">Stalled-work check-ins</p>
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] mt-[var(--space-0-5)]">
              After the interval below without new progress, one check-in on the goal&rsquo;s stalled
              background work. Turning this off keeps the interval saved.
            </p>
          </div>
          <Switch
            checked={stalledEnabled}
            onCheckedChange={handleStalledEnabledChange}
            disabled={mutation.isPending}
            aria-label="Stalled-work check-ins"
            data-testid="performance-goal-stalled-checkin-switch"
          />
        </div>

        <div className="flex items-center gap-[var(--space-2-5)]">
          <Label
            htmlFor="performance-goal-stalled-minutes"
            className="text-[length:var(--type-utility-xs-size)] font-medium leading-[var(--font-line-height-body)] text-[var(--color-secondary)] w-44 shrink-0"
          >
            Stalled-work check-in interval (minutes)
          </Label>
          <Input
            id="performance-goal-stalled-minutes"
            type="number"
            min={5}
            max={1440}
            step={1}
            value={stalledMinutesInput}
            onChange={(e) => handleStalledMinutesChange(e.target.value)}
            disabled={!stalledEnabled}
            className="w-24 h-7 text-[length:var(--type-body-compact-size)]"
            aria-label="Stalled-work check-in interval (minutes)"
            data-testid="performance-goal-stalled-minutes-input"
          />
        </div>

        <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] leading-relaxed">
          A due reminder or check-in can be noticed up to 30 seconds late. The quiet window has
          no off value; the switch is the only way to stop stalled-work check-ins. Saving asks you
          to confirm the change.
        </p>
      </Card>

      <MaxToolIterationsCard
        value={toolIterInput}
        onChange={handleToolIterChange}
        error={toolIterError}
        inForce={data?.max_tool_iterations}
        savedState={data?.max_tool_iterations_saved_state}
        savedRaw={data?.max_tool_iterations_saved_raw}
        loweredSummary={loweredSummary}
      />

      {lowering && (
        <MaxToolIterationsLoweringDialog
          open
          value={lowering.value}
          agents={lowering.agents}
          listChanged={lowering.listChanged}
          onConfirm={confirmLowering}
          onCancel={cancelLowering}
        />
      )}

      {stepUp.dialogs}

      {/* Escape hatch: manual trigger exposed for keyboard users / edge cases */}
      {dirty && !stepUp.open && (
        <Button
          type="button"
          data-testid="performance-save-btn"
          onClick={triggerSave}
          className="sr-only focus:not-sr-only focus:absolute focus:z-50"
        >
          Save changes
        </Button>
      )}
      {goalDirty && !stepUp.open && (
        <Button
          type="button"
          data-testid="performance-goal-save-btn"
          onClick={triggerGoalSave}
          className="sr-only focus:not-sr-only focus:absolute focus:z-50"
        >
          Save changes
        </Button>
      )}
      {quietDirty && !stepUp.open && (
        <Button
          type="button"
          data-testid="performance-quiet-seconds-save-btn"
          onClick={triggerQuietSecondsSave}
          className="sr-only focus:not-sr-only focus:absolute focus:z-50"
        >
          Save changes
        </Button>
      )}
      {stalledMinutesDirty && !stepUp.open && (
        <Button
          type="button"
          data-testid="performance-stalled-minutes-save-btn"
          onClick={triggerStalledMinutesSave}
          className="sr-only focus:not-sr-only focus:absolute focus:z-50"
        >
          Save changes
        </Button>
      )}
      {toolIterDirty && !stepUp.open && !lowering && (
        <Button
          type="button"
          data-testid="performance-max-tool-iterations-save-btn"
          onClick={triggerToolIterSave}
          className="sr-only focus:not-sr-only focus:absolute focus:z-50"
        >
          Save changes
        </Button>
      )}
    </div>
  )
}
