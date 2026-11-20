/**
 * PerformanceSection — Settings → Limits → Agents and goals (Settings
 * redesign #1519; formerly the Performance tab).
 *
 * Backed by GET/PUT /api/v1/performance. Every value waits for Save changes
 * (FR-010: the 600 ms auto-save and its confirmation are gone). All six
 * Agents-and-goals rows carry the Password badge (FR-025): their one write is
 * password protected, so a Save asks for the password once (FR-026) and sends
 * the changed values in exactly one PUT (FR-027). The Tool loading switch
 * stays where it is until per-tool loading levels replace it (S-Q3); it rides
 * the same protected write.
 *
 * #904 "Max tool calls per turn": a LOWERING is first previewed
 * (GET /performance/max-tool-iterations/preview); agents it would lower are
 * listed in MaxToolIterationsLoweringDialog as a pre-save confirm (FR-016),
 * before the password prompt, and the PUT carries that exact list as
 * confirmed_lowering. A 409 drift stops the Save, re-opens the dialog with
 * the server's fresh list and, once confirmed, saves again — asking for the
 * password again (FR-031).
 *
 * A save the server stored but could not apply (500 performance_reload_failed,
 * or a 200 carrying pending_apply) is "Saved, but not in use yet" (FR-020).
 *
 * The form lives in usePerformanceForm so the Limits section can put it on
 * the same Save bar as Context and tool results (FR-019). PerformanceSection
 * renders it alone with its own Save bar.
 */

import { ReadErrorCallout } from './ReadErrorCallout'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Warning } from '@phosphor-icons/react'
import { Callout, CalloutDescription, CalloutTitle } from '@/components/ui/callout'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
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
import type { MaxToolIterationAgentChange, PerformancePendingApply, PerformanceSettings } from '@/lib/api/generated/openapi-types'
import { useUiStore } from '@/store/ui'
import { savedStateWarning, MAX_TOOL_ITERATIONS_MIN, MAX_TOOL_ITERATIONS_MAX } from './MaxToolIterationsCard'
import { MaxToolIterationsLoweringDialog } from './MaxToolIterationsLoweringDialog'
import { FormRow, SaveBarPanel, SaveOutcomeLine } from './SaveBar'
import { toWriteResponse, type WriteResponse } from './saveOutcome'
import { useFormSaveBar, type FormSavePart } from './useFormSaveBar'

export const PERFORMANCE_QUERY_KEY = ['performance-settings'] as const

const PHYSICAL_THREAD_CEILING = 2000
const LOWERED_TOAST_DURATION_MS = 10_000

type Key =
  | 'max_parallel_agents'
  | 'goal_max_rounds'
  | 'goal_reminder_quiet_seconds'
  | 'goal_stalled_checkin_enabled'
  | 'goal_stalled_checkin_minutes'
  | 'max_tool_iterations'
  | 'tools_on_demand'

type Draft = Partial<Record<Key, string | boolean>>
type Values = Record<Key, string | boolean>

const LABELS: Record<Key, string> = {
  max_parallel_agents: 'Max parallel agents',
  goal_max_rounds: 'Tries per goal',
  goal_reminder_quiet_seconds: 'Idle reminder quiet window',
  goal_stalled_checkin_enabled: 'Stalled-work check-ins',
  goal_stalled_checkin_minutes: 'Check-in interval',
  max_tool_iterations: 'Max tool calls per turn',
  tools_on_demand: 'Tool loading',
}

const ORDER: Key[] = [
  'max_parallel_agents',
  'goal_max_rounds',
  'goal_reminder_quiet_seconds',
  'goal_stalled_checkin_enabled',
  'goal_stalled_checkin_minutes',
  'max_tool_iterations',
  'tools_on_demand',
]

function wholeNumber(raw: string): number | null {
  const t = raw.trim()
  return /^\d+$/.test(t) ? Number(t) : null
}

/** Dataset D-10 ranges and messages. */
function validate(key: Key, v: string | boolean): string | null {
  if (typeof v === 'boolean') return null
  const n = wholeNumber(v)
  switch (key) {
    case 'max_parallel_agents':
      if (v.trim() === '') return null
      return n !== null && n >= 1 ? null : 'Enter a whole number of 1 or more, or leave it empty for no fixed limit.'
    case 'goal_max_rounds':
      return n !== null && n >= 1 ? null : 'Enter a whole number of 1 or more.'
    case 'goal_reminder_quiet_seconds':
      return n !== null && n >= 30 && n <= 3600 ? null : 'Enter a whole number from 30 to 3,600.'
    case 'goal_stalled_checkin_minutes':
      return n !== null && n >= 5 && n <= 1440 ? null : 'Enter a whole number from 5 to 1,440.'
    case 'max_tool_iterations':
      return n !== null && n >= MAX_TOOL_ITERATIONS_MIN && n <= MAX_TOOL_ITERATIONS_MAX
        ? null
        : 'Enter a whole number from 1 to 1,000.'
    default:
      return null
  }
}

function valuesOf(d: PerformanceSettings): Values {
  return {
    max_parallel_agents: d.max_parallel_agents_configured === false || !d.max_parallel_agents ? '' : String(d.max_parallel_agents),
    goal_max_rounds: String(d.goal_max_rounds ?? 20),
    goal_reminder_quiet_seconds: String(d.goal_reminder_quiet_seconds ?? 60),
    goal_stalled_checkin_enabled: d.goal_stalled_checkin_enabled ?? true,
    goal_stalled_checkin_minutes: String(d.goal_stalled_checkin_minutes ?? 30),
    max_tool_iterations: d.max_tool_iterations === undefined ? '' : String(d.max_tool_iterations),
    tools_on_demand: d.tools_on_demand ?? true,
  }
}

function same(a: string | boolean, b: string | boolean): boolean {
  if (typeof a === 'string' && typeof b === 'string') return a.trim() === b.trim()
  return a === b
}

function bodyOf(changed: Key[], v: Values): PerformanceSettingsUpdate {
  const body: PerformanceSettingsUpdate = {}
  for (const k of changed) {
    const raw = v[k]
    if (typeof raw === 'boolean') (body as Record<string, unknown>)[k] = raw
    else (body as Record<string, unknown>)[k] = raw.trim() === '' ? 0 : Number(raw.trim())
  }
  return body
}

function loweredSummaryText(agents: MaxToolIterationAgentChange[]): string {
  const list = agents.map((a) => `${a.agent_name} ${a.old_value} → ${a.new_value}`).join(', ')
  return `Lowered ${agents.length} ${agents.length === 1 ? 'agent' : 'agents'}: ${list}`
}

/**
 * What the fields show while a save is not applied yet (contract
 * PerformancePendingApply.stage). At stage reload the running configuration
 * already holds the new values. At stage refresh the read still returns the
 * OLD values, so the fields show the saved ones only where this page knows
 * them from its own save.
 */
function pendingFieldsLine(p: PerformancePendingApply, known: Partial<Values>): string {
  if (p.stage === 'reload') return 'The fields show the saved values.'
  const allKnown = p.changed_fields.length > 0 && p.changed_fields.every((f) => f in known)
  return allKnown
    ? 'The fields below show the saved values. Until the restart or reload, Omnipus keeps running on the previous ones.'
    : 'The fields below still show the values Omnipus is running on; the saved ones take effect after the restart or reload.'
}

interface LoweringState { // not-wire-format: screen-only dialog state
  value: number
  agents: MaxToolIterationAgentChange[]
  listChanged: boolean
  resolve: (ok: boolean) => void
}

export function usePerformanceForm(options: { onReconfirmed?: () => void } = {}) {
  const { onReconfirmed } = options
  const queryClient = useQueryClient()
  const { addToast } = useUiStore()
  const { data, isLoading, error, refetch, isFetching } = useQuery({
    queryKey: PERFORMANCE_QUERY_KEY,
    queryFn: fetchPerformanceSettings,
  })
  const [drafts, setDrafts] = useState<Draft>({})
  // The saved values known to the screen (FR-012): a write's answer wins over
  // a running-settings read that may still show the old values.
  const [known, setKnown] = useState<Partial<Values>>({})
  const [lowering, setLowering] = useState<LoweringState | null>(null)
  const [loweredSummary, setLoweredSummary] = useState<string | null>(null)
  const confirmedRef = useRef<{ value: number; agents: MaxToolIterationAgentChange[] } | null>(null)
  const sentRef = useRef<{ keys: Key[]; values: Values } | null>(null)

  const server = useMemo(() => (data ? valuesOf(data) : null), [data])
  const saved = useCallback((k: Key): string | boolean => (k in known ? known[k]! : server ? server[k] : ''), [known, server])
  const value = useCallback((k: Key): string | boolean => (k in drafts ? drafts[k]! : saved(k)), [drafts, saved])

  const changed = useMemo(() => ORDER.filter((k) => k in drafts && !same(drafts[k]!, saved(k))), [drafts, saved])
  const errors = useMemo(() => {
    const out: Partial<Record<Key, string>> = {}
    if (!server) return out
    for (const k of ORDER) {
      if (!(k in drafts)) continue
      const msg = validate(k, value(k))
      if (msg) out[k] = msg
    }
    return out
  }, [drafts, server, value])

  const setDraft = (k: Key, v: string | boolean) => {
    setDrafts((prev) => {
      const next = { ...prev }
      if (same(v, saved(k))) delete next[k]
      else next[k] = v
      return next
    })
  }

  const askLowering = (value: number, agents: MaxToolIterationAgentChange[], listChanged: boolean) =>
    new Promise<boolean>((resolve) => setLowering({ value, agents, listChanged, resolve }))

  const preSave = async (): Promise<boolean> => {
    if (!changed.includes('max_tool_iterations')) return true
    const next = Number(String(value('max_tool_iterations')).trim())
    const inForce = data?.max_tool_iterations
    if (inForce !== undefined && next >= inForce) {
      confirmedRef.current = null // a raise never rewrites an agent (D20)
      return true
    }
    if (confirmedRef.current?.value === next) return true
    let agents: MaxToolIterationAgentChange[]
    try {
      agents = (await fetchMaxToolIterationsLoweringPreview(next)).agents
    } catch (err) {
      addToast({ variant: 'error', message: getErrorMessage(err, 'The agents this lowers could not be checked. Try again.') })
      return false
    }
    if (agents.length === 0) {
      confirmedRef.current = { value: next, agents: [] }
      return true
    }
    const ok = await askLowering(next, agents, false)
    if (ok) confirmedRef.current = { value: next, agents }
    return ok
  }

  const write = async ({ reauthToken }: { reauthToken?: string }): Promise<WriteResponse> => {
    const current = Object.fromEntries(ORDER.map((k) => [k, value(k)])) as Values
    const body = bodyOf(changed, current)
    if (body.max_tool_iterations !== undefined && confirmedRef.current?.value === body.max_tool_iterations) {
      body.confirmed_lowering = confirmedRef.current.agents.map((a) => ({ agent_id: a.agent_id, old_value: a.old_value }))
    }
    sentRef.current = { keys: [...changed], values: current }
    try {
      const resp = await updatePerformanceSettings(body, reauthToken)
      return { ok: true, status: 200, json: async () => resp }
    } catch (err) {
      if (isMaxToolIterationsLoweringConflict(err)) {
        // FR-031: nothing was written; show the fresh list for a new confirm.
        confirmedRef.current = null
        const fresh = err.preview
        void askLowering(fresh.value, fresh.agents, true).then((ok) => {
          if (!ok) return
          confirmedRef.current = { value: fresh.value, agents: fresh.agents }
          onReconfirmed?.()
        })
        return {
          ok: false,
          status: 409,
          json: async () => ({ error: 'The agents this lowers changed since you confirmed. Check the new list, then save again.' }),
        }
      }
      if (isPerformanceReloadFailed(err)) {
        // Stored in config.json but not in force yet: saved, not in use.
        if (err.loweredAgents.length) reportLowered(err.loweredAgents)
        return { ok: true, status: 200, json: async () => ({ pending_apply: { stage: err.stage ?? 'refresh' } }) }
      }
      return toWriteResponse(() => Promise.reject(err))
    }
  }

  const reportLowered = (agents: MaxToolIterationAgentChange[] | undefined) => {
    if (!agents?.length) return
    const text = loweredSummaryText(agents)
    addToast({ variant: 'success', message: text, duration: LOWERED_TOAST_DURATION_MS })
    setLoweredSummary(text)
  }

  const onOutcome: FormSavePart['onOutcome'] = (outcome) => {
    const sent = sentRef.current
    if (!sent) return
    if (outcome.kind === 'saved' || outcome.kind === 'saved-not-in-use') {
      const next: Partial<Values> = {}
      for (const k of sent.keys) next[k] = sent.values[k]
      setKnown((prev) => ({ ...prev, ...next }))
      setDrafts((prev) => {
        const d = { ...prev }
        for (const k of sent.keys) if (same(d[k] ?? '', sent.values[k])) delete d[k]
        return d
      })
      const body = outcome.body as Partial<PerformanceSettings> | undefined
      reportLowered(body?.max_tool_iterations_lowered_agents)
      confirmedRef.current = null
      void queryClient.invalidateQueries({ queryKey: PERFORMANCE_QUERY_KEY })
    }
  }

  const check = async (): Promise<'saved' | 'not-saved'> => {
    const sent = sentRef.current
    const fresh = valuesOf(await fetchPerformanceSettings())
    if (!sent) return 'not-saved'
    return sent.keys.every((k) => same(fresh[k], sent.values[k])) ? 'saved' : 'not-saved'
  }

  const discard = () => setDrafts({})

  const part: FormSavePart = {
    id: 'performance',
    count: changed.length,
    invalidCount: Object.keys(errors).length,
    passwordLabels: changed.map((k) => LABELS[k]),
    preSave,
    write,
    stopsSave: (o) => o.kind === 'not-saved' && o.status === 409,
    check,
    onOutcome,
    focusInvalid: () => {
      const first = ORDER.find((k) => errors[k])
      if (first) document.querySelector<HTMLElement>(`[data-settings-field="settings-limits-${first}"]`)?.focus()
    },
    discard,
  }

  // ── View ───────────────────────────────────────────────────────────────
  const numberRow = (k: Key, description: React.ReactNode, opts: { unit?: string; placeholder?: string; testId: string; disabled?: boolean; allowsEmpty?: boolean }) => (
    <FormRow key={k} label={LABELS[k]} description={description} error={errors[k]} password fieldId={`settings-limits-${k}`}>
      {({ id, describedBy, invalid, fieldId }) => (
        <div className="flex items-center gap-[var(--space-2)]">
          <Input
            id={id}
            data-settings-field={fieldId}
            data-testid={opts.testId}
            // A field where empty means "no limit" stays text; the others are numbers.
            type={opts.allowsEmpty ? 'text' : 'number'}
            inputMode="numeric"
            value={String(value(k))}
            placeholder={opts.placeholder}
            disabled={opts.disabled}
            onChange={(e) => setDraft(k, e.target.value)}
            aria-describedby={describedBy}
            aria-invalid={invalid || undefined}
            className="max-w-[10rem]"
          />
          {opts.unit && <span className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">{opts.unit}</span>}
        </div>
      )}
    </FormRow>
  )

  const switchRow = (k: Key, description: React.ReactNode, testId: string) => (
    <FormRow key={k} label={LABELS[k]} description={description} password fieldId={`settings-limits-${k}`}>
      {({ id, describedBy, fieldId }) => (
        <Switch
          id={id}
          data-settings-field={fieldId}
          data-testid={testId}
          checked={value(k) === true}
          onCheckedChange={(c) => setDraft(k, c)}
          aria-describedby={describedBy}
        />
      )}
    </FormRow>
  )

  let view: React.ReactNode
  if (isLoading) {
    view = (
      <div role="status" data-testid="performance-loading" className="flex items-center gap-[var(--space-2)] text-[var(--color-muted)]">
        <Spinner decorative />
        Loading Agents and goals…
      </div>
    )
  } else if (error || !data) {
    view = (
      <ReadErrorCallout
        title="Agents and goals could not be loaded"
        error={error}
        onRetry={() => void refetch()}
        retrying={isFetching}
        data-testid="performance-load-error"
        retryTestId="performance-retry-btn"
      />
    )
  } else {
    const parallel = wholeNumber(String(value('max_parallel_agents')))
    const savedWarning = savedStateWarning(data.max_tool_iterations_saved_state, data.max_tool_iterations_saved_raw, data.max_tool_iterations)
    view = (
      <div className="space-y-[var(--space-2)]">
        {data.pending_apply && (
          <Callout variant="info" data-testid="performance-unapplied-notice">
            <CalloutTitle>Some saved values are not in use yet</CalloutTitle>
            <CalloutDescription>{performancePendingApplyMessage(data.pending_apply)} {pendingFieldsLine(data.pending_apply, known)}</CalloutDescription>
          </Callout>
        )}
        <Card className="p-[var(--space-3)] text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
          Currently in use: {data.effective_max_parallel_agents ?? 'unknown'} parallel agents
          {data.max_parallel_agents_configured === false ? ' · without a limit, the number is bounded by available memory.' : '.'}
        </Card>
        <Card className="px-[var(--space-3)]">
          {numberRow('max_parallel_agents', 'Empty means no fixed limit. 1 or more. Above 2,000 the server may run out of threads.', {
            placeholder: 'No limit',
            testId: 'performance-max-agents-input',
            allowsEmpty: true,
          })}
          {parallel !== null && parallel > PHYSICAL_THREAD_CEILING && (
            <p data-testid="performance-high-value-warning" className="flex items-start gap-[var(--space-1)] pb-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-warning)]">
              <Warning size={14} aria-hidden="true" className="mt-[var(--space-0-5)] shrink-0" />
              {parallel} is used exactly as set, but above {PHYSICAL_THREAD_CEILING.toLocaleString('en-US')} the server may run out of threads and stop.
            </p>
          )}
          {numberRow('goal_max_rounds', 'How many rounds an agent gets to finish one goal. 1 or more. Default 20.', { unit: 'rounds', testId: 'performance-goal-max-rounds-input' })}
          {numberRow('goal_reminder_quiet_seconds', '30 to 3,600 seconds. Default 60.', { unit: 's', testId: 'performance-goal-quiet-seconds-input' })}
          {switchRow('goal_stalled_checkin_enabled', 'Ask a stuck agent how it is doing.', 'performance-goal-stalled-checkin-switch')}
          {numberRow('goal_stalled_checkin_minutes', '5 to 1,440 minutes. Default 30.', {
            unit: 'minutes',
            testId: 'performance-goal-stalled-minutes-input',
            disabled: value('goal_stalled_checkin_enabled') !== true,
          })}
          {numberRow('max_tool_iterations', '1 to 1,000. Lowering it first shows which agents are affected.', { testId: 'performance-max-tool-iterations-input' })}
          {savedWarning && (
            <p role="status" className="pb-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-warning)]">
              {savedWarning}
            </p>
          )}
          {loweredSummary && (
            <p role="status" data-testid="performance-lowered-summary" className="pb-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
              {loweredSummary}
            </p>
          )}
          {switchRow('tools_on_demand', 'Load tools only when an agent needs them, so each message stays small.', 'performance-tools-on-demand-switch')}
        </Card>
      </div>
    )
  }

  // Rendered outside the collapsible group so a pre-save confirm always shows.
  const dialogs = lowering ? (
    <MaxToolIterationsLoweringDialog
      open
      value={lowering.value}
      agents={lowering.agents}
      listChanged={lowering.listChanged}
      onConfirm={() => {
        lowering.resolve(true)
        setLowering(null)
      }}
      onCancel={() => {
        lowering.resolve(false)
        setLowering(null)
      }}
    />
  ) : null

  return { part, view, dialogs, ready: !!data && !error, loadFailed: !!error && !isLoading }
}

/** Agents and goals on its own, with its own Save bar. */
export function PerformanceSection(): React.ReactElement {
  const saveRef = useRef<() => Promise<void>>(async () => {})
  const form = usePerformanceForm({ onReconfirmed: () => void saveRef.current() })
  const { bar, message, save } = useFormSaveBar([form.part])
  useEffect(() => {
    saveRef.current = save
  }, [save])
  return (
    <div className="space-y-[var(--space-2)]">
      {form.view}
      {form.dialogs}
      <SaveOutcomeLine message={message('performance')} />
      <SaveBarPanel {...bar} />
    </div>
  )
}
