import { useState, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { FloppyDisk, WarningCircle } from '@phosphor-icons/react'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { AdvancedDisclosure } from '@/components/shared/AdvancedDisclosure'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { SmartSelect } from '@/components/ui/smart-select'
import {
  fetchAgents,
  createSchedule,
  updateSchedule,
  isApiError,
} from '@/lib/api'
import type { Schedule, ScheduleCreate, ScheduleUpdate, ScheduleTrigger } from '@/lib/api/generated/openapi-types'
import { useUiStore } from '@/store/ui'
import { triggerSummary } from './SchedulesList'
import {
  UNIT_MS,
  buildRepeatCron,
  reverseParseCron,
  reverseParseEvery,
  isStructurallyValidCron,
} from './cronUtils'
import type { FriendlyMode, RepeatShape, EveryUnit } from './cronUtils'

// ── Types ──────────────────────────────────────────────────────────────────────

interface ScheduleFormSheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  // When set, the form edits an existing schedule; otherwise it creates a new one.
  schedule?: Schedule | null
  // Pre-fill the owner (used by the agent profile "New schedule" button).
  defaultOwnerAgentId?: string
}

// session_mode values the UI may emit (NOT 'main' — D18).
type AllowedSessionMode = 'isolated' | 'continue'

interface FormState {
  ownerAgentId: string
  name: string
  // Friendly trigger mode for the UI selector.
  friendlyMode: FriendlyMode
  // 'once' controls
  atLocal: string // datetime-local string
  // 'repeat' controls
  repeatShape: RepeatShape
  everyValue: string
  everyUnit: EveryUnit
  repeatTime: string   // HH:MM for daily-at/weekly-at/monthly-at
  weekday: string      // 0-6 (Sunday=0) for weekly-at
  monthDay: string     // 1-31 for monthly-at
  // 'custom' controls
  cronExpr: string
  // payload fields
  message: string
  // session mode — only 'isolated' or 'continue' in UI; 'main' is legacy read-only.
  sessionMode: AllowedSessionMode
  deliver: boolean
  timeoutSeconds: string
  enabled: boolean
}

// ── Constants ──────────────────────────────────────────────────────────────────

const WEEKDAY_LABELS = [
  { value: '0', label: 'Sunday' },
  { value: '1', label: 'Monday' },
  { value: '2', label: 'Tuesday' },
  { value: '3', label: 'Wednesday' },
  { value: '4', label: 'Thursday' },
  { value: '5', label: 'Friday' },
  { value: '6', label: 'Saturday' },
]

// ── Initial state builder ──────────────────────────────────────────────────

function toLocalInput(ms: number): string {
  const d = new Date(ms)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function emptyState(defaultOwner?: string): FormState {
  return {
    ownerAgentId: defaultOwner ?? '',
    name: '',
    // AC4 (US-A2): brand-new form defaults trigger to Once.
    friendlyMode: 'once',
    atLocal: '',
    repeatShape: 'interval',
    everyValue: '60',
    everyUnit: 'minutes',
    repeatTime: '09:00',
    weekday: '1',
    monthDay: '1',
    cronExpr: '',
    message: '',
    // AC2 (US-A3 D18): default session mode is isolated.
    sessionMode: 'isolated',
    deliver: false,
    timeoutSeconds: '',
    enabled: true,
  }
}

function initialState(schedule: Schedule | null | undefined, defaultOwner?: string): FormState {
  if (!schedule) return emptyState(defaultOwner)
  const t = schedule.trigger

  // session_mode: only map known UI values; 'main' is legacy read-only (handled in the UI).
  // We map 'main' → 'isolated' in the form state so the save always emits a valid mode.
  // The read-only legacy notice prevents the user from being misled (AC, US-A3).
  const sessionMode: AllowedSessionMode =
    schedule.session_mode === 'continue' ? 'continue' : 'isolated'

  const base: FormState = {
    ownerAgentId: schedule.owner_agent_id,
    name: schedule.name,
    friendlyMode: 'once',
    atLocal: '',
    repeatShape: 'interval',
    everyValue: '60',
    everyUnit: 'minutes',
    repeatTime: '09:00',
    weekday: '1',
    monthDay: '1',
    cronExpr: '',
    message: schedule.message,
    sessionMode,
    deliver: schedule.deliver,
    timeoutSeconds: schedule.timeout_seconds ? String(schedule.timeout_seconds) : '',
    enabled: schedule.enabled,
  }

  if (t.kind === 'at') {
    return { ...base, friendlyMode: 'once', atLocal: t.at_ms ? toLocalInput(t.at_ms) : '' }
  }

  if (t.kind === 'every' && t.every_ms) {
    const parsed = reverseParseEvery(t.every_ms)
    if (parsed) {
      return {
        ...base,
        friendlyMode: parsed.friendlyMode ?? 'repeat',
        repeatShape: parsed.repeatShape ?? 'interval',
        everyValue: parsed.everyValue ?? '60',
        everyUnit: parsed.everyUnit ?? 'minutes',
      }
    }
    // Non-round interval: cannot represent in friendly UI — show in custom view.
    return { ...base, friendlyMode: 'custom', cronExpr: String(t.every_ms) }
  }

  if (t.kind === 'cron' && t.cron_expr) {
    const parsed = reverseParseCron(t.cron_expr)
    if (parsed) {
      return {
        ...base,
        friendlyMode: parsed.friendlyMode ?? 'repeat',
        repeatShape: parsed.repeatShape ?? 'interval',
        repeatTime: parsed.repeatTime ?? '09:00',
        weekday: parsed.weekday ?? '1',
        monthDay: parsed.monthDay ?? '1',
      }
    }
    // Complex cron: open verbatim in Custom (AC5, M-1 — never silently rewrite).
    return { ...base, friendlyMode: 'custom', cronExpr: t.cron_expr }
  }

  return base
}

// ── Build the wire trigger from form state ──────────────────────────────────

function buildTrigger(form: FormState): ScheduleTrigger {
  switch (form.friendlyMode) {
    case 'once':
      return {
        kind: 'at',
        at_ms: form.atLocal ? new Date(form.atLocal).getTime() : undefined,
      }
    case 'repeat': {
      if (form.repeatShape === 'interval') {
        return {
          kind: 'every',
          every_ms: form.everyValue
            ? Math.max(1, Number(form.everyValue)) * UNIT_MS[form.everyUnit]
            : undefined,
        }
      }
      // Daily/weekly/monthly → cron
      const expr = buildRepeatCron(form.repeatShape, form.repeatTime, form.weekday, form.monthDay)
      return { kind: 'cron', cron_expr: expr ?? undefined }
    }
    case 'custom':
      return { kind: 'cron', cron_expr: form.cronExpr || undefined }
  }
}

// ── Human-readable trigger description (for NextRunPreview) ────────────────

/**
 * Build a human-readable description of the trigger derived from form state,
 * used by NextRunPreview. Returns null when inputs are incomplete/invalid.
 */
/** Pluralize a unit label based on count. "1 hour", "2 hours", etc. */
function pluralizeUnit(n: number, unit: EveryUnit): string {
  const singular: Record<EveryUnit, string> = { minutes: 'minute', hours: 'hour', days: 'day' }
  return n === 1 ? singular[unit] : unit
}

function formTriggerDescription(form: FormState): string | null {
  switch (form.friendlyMode) {
    case 'once':
      if (!form.atLocal) return null
      return `Once · ${new Date(form.atLocal).toLocaleString()}`
    case 'repeat': {
      if (form.repeatShape === 'interval') {
        const n = parseInt(form.everyValue, 10)
        if (isNaN(n) || n < 1) return null
        return `Every ${n} ${pluralizeUnit(n, form.everyUnit)}`
      }
      const cron = buildRepeatCron(form.repeatShape, form.repeatTime, form.weekday, form.monthDay)
      if (!cron) return null
      // Delegate to triggerSummary for human-readable output.
      return triggerSummary({ kind: 'cron', cron_expr: cron })
    }
    case 'custom': {
      if (!form.cronExpr.trim()) return null
      if (!isStructurallyValidCron(form.cronExpr)) return null
      return triggerSummary({ kind: 'cron', cron_expr: form.cronExpr.trim() })
    }
  }
}

// ── NextRunPreview ─────────────────────────────────────────────────────────

interface NextRunPreviewProps {
  form: FormState
  serverError?: string | null
}

/**
 * LOCAL component — single consumer (ScheduleFormSheet). See spec §2, M-11.
 * Reuses triggerSummary() from SchedulesList.tsx.
 * Shows human description + "server time" for valid inputs.
 * Shows server error inline when the server returns a 4xx for the trigger (AC3b).
 */
function NextRunPreview({ form, serverError }: NextRunPreviewProps) {
  if (serverError) {
    return (
      <p className="text-[11px] text-[var(--color-error)] mt-1" data-testid="next-run-preview">
        {serverError}
      </p>
    )
  }

  const desc = formTriggerDescription(form)
  if (!desc) return null

  return (
    <p className="text-[11px] text-[var(--color-muted)] mt-1" data-testid="next-run-preview">
      {desc} · <span className="text-[var(--color-muted)]">server time</span>
    </p>
  )
}

// ── Session mode helpers ───────────────────────────────────────────────────

/**
 * Human label for the session mode, used on schedule cards (not the raw enum).
 * AC5 (US-A3): card must NOT render the raw session_mode enum string.
 */
export function sessionModeLabel(mode: string): string {
  switch (mode) {
    case 'isolated': return 'Fresh session per run'
    case 'continue': return 'Keeps full thread'
    case 'main': return 'Main chat (legacy)'
    default: return mode
  }
}

// ── Main component ─────────────────────────────────────────────────────────

export function ScheduleFormSheet({
  open,
  onOpenChange,
  schedule,
  defaultOwnerAgentId,
}: ScheduleFormSheetProps) {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const isEdit = Boolean(schedule)

  const [form, setForm] = useState<FormState>(() => initialState(schedule, defaultOwnerAgentId))
  // Inline server validation error for the trigger (AC3b).
  const [triggerServerError, setTriggerServerError] = useState<string | null>(null)

  // Re-initialise form when the sheet opens with a different schedule.
  useEffect(() => {
    if (open) {
      setForm(initialState(schedule, defaultOwnerAgentId))
      setTriggerServerError(null)
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, schedule?.id])

  function setValue<K extends keyof FormState>(key: K, value: FormState[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
    // Clear server error when the user edits the trigger.
    if (['friendlyMode', 'atLocal', 'repeatShape', 'everyValue', 'everyUnit',
         'repeatTime', 'weekday', 'monthDay', 'cronExpr'].includes(key)) {
      setTriggerServerError(null)
    }
  }

  const { data: agents = [], isError: agentsLoadFailed, error: agentsError } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    enabled: open,
  })

  // Default owner: agent with default:true, or first agent.
  // Applied only when the form is new (no schedule) and no defaultOwnerAgentId provided.
  useEffect(() => {
    if (!open || isEdit || defaultOwnerAgentId || agents.length === 0) return
    if (form.ownerAgentId !== '') return
    const defaultAgent = agents.find((a) => a.default) ?? agents[0]
    if (defaultAgent) setValue('ownerAgentId', defaultAgent.id)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agents, open])

  // Is a legacy 'main' schedule being edited? (AC, US-A3: load in read-only/"legacy" form)
  // Declared before useMutation so it can be closed over by mutationFn.
  const isLegacyMain = isEdit && schedule?.session_mode === 'main'

  const { mutate: doSave, isPending: saving } = useMutation({
    mutationFn: () => {
      const trigger = buildTrigger(form)
      const timeout = form.timeoutSeconds === '' ? undefined : Number(form.timeoutSeconds)
      // Fix 5: clear any stale server error on each save attempt so a retry
      // doesn't show a stale inline error while the new request is in flight.
      setTriggerServerError(null)
      if (isEdit && schedule) {
        // Fix 1 (BLOCKER 1): legacy 'main' schedules must NOT have session_mode
        // rewritten. The spec (§0/§3 US-A3 edge case) requires the stored 'main'
        // value to be PRESERVED — OMIT session_mode from the update body entirely
        // so the server keeps whatever is stored. The read-only notice in the UI
        // prevents the user from selecting a new mode.
        const body: ScheduleUpdate = {
          name: form.name,
          owner_agent_id: form.ownerAgentId,
          trigger,
          message: form.message,
          deliver: form.deliver,
          ...(isLegacyMain ? {} : { session_mode: form.sessionMode }),
          timeout_seconds: timeout,
          enabled: form.enabled,
        }
        return updateSchedule(schedule.id, body)
      }
      const body: ScheduleCreate = {
        name: form.name,
        owner_agent_id: form.ownerAgentId,
        trigger,
        message: form.message,
        deliver: form.deliver,
        session_mode: form.sessionMode,
        timeout_seconds: timeout,
        enabled: form.enabled,
      }
      return createSchedule(body)
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['schedules'] })
      addToast({ message: isEdit ? 'Schedule updated' : 'Schedule created', variant: 'success' })
      onOpenChange(false)
    },
    onError: (err: unknown) => {
      const msg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Save failed'
      // Map trigger-related 4xx errors inline (AC3b). The structured server error
      // code/field would make this routing precise; a backend follow-up is needed
      // to add that — for now we use a keyword heuristic on the message text.
      if (isApiError(err) && err.status && err.status >= 400 && err.status < 500) {
        const lc = msg.toLowerCase()
        if (lc.includes('cron') || lc.includes('trigger') || lc.includes('schedule') || lc.includes('interval')) {
          setTriggerServerError(msg)
          return
        }
      }
      addToast({ message: msg, variant: 'error' })
    },
  })

  // Fix 4: ensure the trigger is fully populated before allowing Save.
  // buildTrigger can return {kind:'cron', cron_expr:undefined} when the user
  // has chosen a repeat shape that requires a time/weekday/day but hasn't filled it in yet.
  function isTriggerPopulated(): boolean {
    const t = buildTrigger(form)
    switch (t.kind) {
      case 'at':
        return true // at_ms is optional (server defaults to now); once without a time is valid
      case 'every':
        return t.every_ms != null && t.every_ms > 0
      case 'cron':
        return t.cron_expr != null && t.cron_expr.trim() !== ''
    }
  }

  // Is the custom cron structurally valid enough to allow Save?
  const customCronOk =
    form.friendlyMode !== 'custom' || isStructurallyValidCron(form.cronExpr)

  const canSave =
    form.ownerAgentId !== '' &&
    form.name.trim() !== '' &&
    form.message.trim() !== '' &&
    customCronOk &&
    isTriggerPopulated() &&
    !saving

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="sm:w-[480px] bg-[var(--color-surface-1)] border-[var(--color-border)] overflow-y-auto"
      >
        <SheetHeader className="pb-4 border-b border-[var(--color-border)]">
          <SheetTitle className="font-headline text-base font-semibold text-[var(--color-secondary)]">
            {isEdit ? 'Edit schedule' : 'New schedule'}
          </SheetTitle>
        </SheetHeader>

        <div className="pt-5 space-y-5">
          {/* ── Primary fields (US-A1: simple-first; novice sees only these) ─── */}

          {/* Which agent (owner) */}
          <Field label="Which agent" htmlFor="schedule-owner" required>
            <SmartSelect
              value={form.ownerAgentId}
              onValueChange={(v) => setValue('ownerAgentId', v)}
              placeholder={agentsLoadFailed ? 'Could not load agents' : 'Select an agent'}
              items={agents.map((a) => ({ value: a.id, label: a.name }))}
            />
            {agentsLoadFailed && (
              <div
                role="alert"
                className="mt-1.5 flex items-start gap-1.5 text-[11px] text-destructive"
              >
                <WarningCircle size={13} weight="fill" className="mt-px shrink-0" />
                <span>
                  Couldn&apos;t load agents
                  {(() => {
                    const detail = isApiError(agentsError)
                      ? agentsError.userMessage
                      : agentsError instanceof Error
                        ? agentsError.message
                        : ''
                    return detail ? `: ${detail}` : '.'
                  })()}{' '}
                  The owner dropdown is empty and Save is disabled until agents load. Close and reopen to retry.
                </span>
              </div>
            )}
          </Field>

          {/* Name */}
          <Field label="Name" htmlFor="schedule-name" required>
            <Input
              id="schedule-name"
              value={form.name}
              onChange={(e) => setValue('name', e.target.value)}
              placeholder="Daily PR summary"
              className="text-xs"
            />
          </Field>

          {/* Message */}
          <Field label="Message" htmlFor="schedule-message" required>
            <Textarea
              id="schedule-message"
              value={form.message}
              onChange={(e) => setValue('message', e.target.value)}
              placeholder="Summarize today's merged PRs in one line."
              className="text-xs resize-none h-24"
            />
          </Field>

          {/* ── When? (friendly trigger) ────────────────────────────────────── */}
          <WhenSection form={form} setValue={setValue} triggerServerError={triggerServerError} />

          {/* Reassurance line (D18) — each run is isolated, memory is shared */}
          <p className="text-[11px] text-[var(--color-muted)]">
            Each run starts fresh. Your agent keeps its memory between runs.
          </p>

          {/* ── Advanced options ─────────────────────────────────────────────── */}
          <AdvancedDisclosure title="Advanced options" summary="Delivery, timeout, session mode">
            <div className="space-y-4">
              {/* Delivery */}
              <div className="flex items-center justify-between">
                <div>
                  <Label className="text-xs font-medium text-[var(--color-secondary)]">
                    Send result directly to channel
                  </Label>
                  <p className="text-[10px] text-[var(--color-muted)] mt-0.5">
                    Off — your agent processes the message. On — sends the message straight to the channel.
                  </p>
                </div>
                <Switch
                  checked={form.deliver}
                  onCheckedChange={(checked) => setValue('deliver', checked)}
                  aria-label="Send directly to channel"
                />
              </div>

              {/* Session mode — D18 (US-A3) */}
              {isLegacyMain ? (
                /* AC (US-A3): legacy 'main' schedule → read-only notice; mode not re-selectable */
                <div className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2">
                  <p className="text-xs text-[var(--color-muted)]">
                    Session mode:{' '}
                    <span className="text-[var(--color-secondary)]">Main chat (legacy — read-only)</span>
                  </p>
                  <p className="text-[10px] text-[var(--color-muted)] mt-1">
                    This schedule uses a legacy session mode. Edit other fields freely; the session mode cannot be changed here.
                  </p>
                </div>
              ) : (
                /* AC3 (US-A3): "keep the full thread" opt-in → session_mode:'continue' */
                <div className="flex items-center justify-between">
                  <div>
                    <Label
                      htmlFor="schedule-keep-thread"
                      className="text-xs font-medium text-[var(--color-secondary)]"
                    >
                      Also keep the full conversation thread across runs
                    </Label>
                    <p className="text-[10px] text-[var(--color-muted)] mt-0.5">
                      Off — each run is a fresh conversation (recommended). On — the agent continues from where it left off.
                    </p>
                  </div>
                  <Switch
                    id="schedule-keep-thread"
                    checked={form.sessionMode === 'continue'}
                    onCheckedChange={(checked) =>
                      setValue('sessionMode', checked ? 'continue' : 'isolated')
                    }
                    aria-label="Keep the full conversation thread across runs"
                  />
                </div>
              )}

              {/* Timeout */}
              <Field label="Run timeout (seconds)" htmlFor="schedule-timeout">
                <Input
                  id="schedule-timeout"
                  type="number"
                  min={0}
                  value={form.timeoutSeconds}
                  onChange={(e) => setValue('timeoutSeconds', e.target.value)}
                  placeholder="Leave blank to use the global default"
                  className="text-xs"
                />
              </Field>

              {/* Active toggle */}
              <div className="flex items-center justify-between">
                <Label className="text-xs font-medium text-[var(--color-secondary)]">Active</Label>
                <Switch
                  checked={form.enabled}
                  onCheckedChange={(checked) => setValue('enabled', checked)}
                  aria-label="Active"
                />
              </div>
            </div>
          </AdvancedDisclosure>

          {/* Actions */}
          <div className="pt-2 border-t border-[var(--color-border)]">
            <Button
              className="w-full gap-1.5"
              onClick={() => doSave()}
              disabled={!canSave}
            >
              <FloppyDisk size={13} />
              {saving ? 'Saving…' : isEdit ? 'Save changes' : 'Create schedule'}
            </Button>
          </div>
        </div>
      </SheetContent>
    </Sheet>
  )
}

// ── WhenSection ────────────────────────────────────────────────────────────

interface WhenSectionProps {
  form: FormState
  setValue: <K extends keyof FormState>(key: K, value: FormState[K]) => void
  triggerServerError: string | null
}

/**
 * Friendly "When?" trigger section: Once / Repeat / Custom.
 * Switching modes clears the inputs that belong to the departed mode so there
 * is no silent carry-over (M-1).
 */
function WhenSection({ form, setValue, triggerServerError }: WhenSectionProps) {
  function setFriendlyMode(next: FriendlyMode) {
    // Fix 6: clear the inputs that belong to the mode being left (M-1).
    // Switching TO custom  → clear cronExpr so the field starts blank.
    // Switching TO repeat  → clear cronExpr (no carry-over from custom).
    // Switching AWAY from repeat → reset repeat-specific fields to defaults so
    //   a later switch back to repeat starts from a clean state.
    if (next === 'custom') {
      setValue('cronExpr', '')
    } else if (next === 'repeat') {
      // Coming from custom: clear the raw cron so it doesn't linger in state.
      setValue('cronExpr', '')
    }
    // When leaving repeat for once or custom, reset repeat fields to defaults
    // so they don't silently carry over if the user returns to repeat.
    if (form.friendlyMode === 'repeat' && next !== 'repeat') {
      setValue('repeatShape', 'interval')
      setValue('repeatTime', '09:00')
      setValue('weekday', '1')
      setValue('monthDay', '1')
    }
    setValue('friendlyMode', next)
  }

  const showCustomError =
    form.friendlyMode === 'custom' &&
    form.cronExpr.trim() !== '' &&
    !isStructurallyValidCron(form.cronExpr)

  return (
    <div className="space-y-2">
      <Field label="When?" htmlFor="schedule-friendly-mode">
        <SmartSelect
          value={form.friendlyMode}
          onValueChange={(v) => setFriendlyMode(v as FriendlyMode)}
          items={[
            { value: 'once', label: 'Once' },
            { value: 'repeat', label: 'Repeat' },
            { value: 'custom', label: 'Custom (advanced)' },
          ]}
        />
      </Field>

      {/* Once */}
      {form.friendlyMode === 'once' && (
        <Field label="Run at" htmlFor="schedule-at">
          <Input
            id="schedule-at"
            type="datetime-local"
            value={form.atLocal}
            onChange={(e) => setValue('atLocal', e.target.value)}
            className="text-xs"
          />
        </Field>
      )}

      {/* Repeat */}
      {form.friendlyMode === 'repeat' && (
        <RepeatSection form={form} setValue={setValue} />
      )}

      {/* Custom (raw cron) */}
      {form.friendlyMode === 'custom' && (
        <div className="space-y-1">
          <Field label="Cron expression" htmlFor="schedule-cron">
            <Input
              id="schedule-cron"
              value={form.cronExpr}
              onChange={(e) => setValue('cronExpr', e.target.value)}
              placeholder="0 18 * * *"
              className="font-mono text-xs"
            />
          </Field>
          <p className="text-[10px] text-[var(--color-muted)]">
            Examples:{' '}
            <code className="font-mono">0 9 * * 1-5</code> (weekdays 9am) ·{' '}
            <code className="font-mono">30 18 1 * *</code> (1st of month 6:30pm)
          </p>
          {showCustomError && (
            <p className="text-[11px] text-[var(--color-error)]" data-testid="cron-error">
              Could not read that schedule — check the format (5 fields: minute hour day month weekday).
            </p>
          )}
        </div>
      )}

      {/* Live NextRunPreview — hidden when there is a structural error (AC3) */}
      {!showCustomError && (
        <NextRunPreview form={form} serverError={triggerServerError} />
      )}
    </div>
  )
}

// ── RepeatSection ──────────────────────────────────────────────────────────

interface RepeatSectionProps {
  form: FormState
  setValue: <K extends keyof FormState>(key: K, value: FormState[K]) => void
}

function RepeatSection({ form, setValue }: RepeatSectionProps) {
  // Time-of-day picker is shown only when the repeat shape will emit a cron (not interval).
  const needsTime = form.repeatShape !== 'interval'

  return (
    <div className="space-y-2">
      <Field label="Repeat" htmlFor="schedule-repeat-shape">
        <SmartSelect
          value={form.repeatShape}
          onValueChange={(v) => setValue('repeatShape', v as RepeatShape)}
          items={[
            { value: 'interval', label: 'Every N minutes / hours / days' },
            { value: 'daily-at', label: 'Every day at a time' },
            { value: 'weekly-at', label: 'Every week on a day' },
            { value: 'monthly-at', label: 'Every month on a day' },
          ]}
        />
      </Field>

      {form.repeatShape === 'interval' && (
        <div className="flex gap-2">
          <Input
            id="schedule-every-value"
            type="number"
            min={1}
            value={form.everyValue}
            onChange={(e) => setValue('everyValue', e.target.value)}
            className="text-xs flex-1"
            aria-label="Interval amount"
          />
          <div className="w-32">
            <SmartSelect
              value={form.everyUnit}
              onValueChange={(v) => setValue('everyUnit', v as EveryUnit)}
              items={[
                { value: 'minutes', label: 'minutes' },
                { value: 'hours', label: 'hours' },
                { value: 'days', label: 'days' },
              ]}
            />
          </div>
        </div>
      )}

      {/* Weekly: show day-of-week picker */}
      {form.repeatShape === 'weekly-at' && (
        <Field label="Day of week" htmlFor="schedule-weekday">
          <SmartSelect
            value={form.weekday}
            onValueChange={(v) => setValue('weekday', v)}
            items={WEEKDAY_LABELS}
          />
        </Field>
      )}

      {/* Monthly: show day-of-month picker */}
      {form.repeatShape === 'monthly-at' && (
        <Field label="Day of month" htmlFor="schedule-month-day">
          <Input
            id="schedule-month-day"
            type="number"
            min={1}
            max={31}
            value={form.monthDay}
            onChange={(e) => setValue('monthDay', e.target.value)}
            className="text-xs"
            placeholder="1–31"
          />
        </Field>
      )}

      {/* Time-of-day: only for daily/weekly/monthly (not interval) */}
      {needsTime && (
        <Field label="At time (server time)" htmlFor="schedule-repeat-time">
          <Input
            id="schedule-repeat-time"
            type="time"
            value={form.repeatTime}
            onChange={(e) => setValue('repeatTime', e.target.value)}
            className="text-xs"
          />
        </Field>
      )}
    </div>
  )
}

// ── Field helper ───────────────────────────────────────────────────────────

function Field({
  label,
  htmlFor,
  required,
  children,
}: {
  label: string
  htmlFor: string
  required?: boolean
  children: React.ReactNode
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={htmlFor} className="text-xs font-medium text-[var(--color-secondary)]">
        {label}
        {required && <span className="text-[var(--color-error)] ml-0.5">*</span>}
      </Label>
      {children}
    </div>
  )
}
