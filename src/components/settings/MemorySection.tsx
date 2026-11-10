import { useState, useEffect, useRef } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Brain, ArrowUp, ArrowDown, X, Warning } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ModelSelector } from '@/components/ui/model-selector'
import { Switch } from '@/components/ui/switch'
import { fetchMemorySettings, updateMemorySettings, fetchProviders, getErrorMessage } from '@/lib/api'
import type { MemorySettings, FallbackModel } from '@/lib/api'
import { useModelToProvider, resolveUniqueModelProvider } from '@/lib/agents/modelToProvider'
import { isProviderUsable } from '@/lib/providerStatus'
import { useUiStore } from '@/store/ui'
import type { EffortPair } from '@/lib/effort/useEffortSwitch'
import { providersCatalogQueryOptions } from '@/lib/providersCatalogQuery'
import { SaveStatus, useSaveStatus } from './SaveStatus'
import { RecapModelSlot } from './RecapModelSlot'
import { RecapLimitsArea } from './RecapLimitsArea'
import {
  CHATGPT_SIGN_IN_PROVIDER,
  RECAP_LIMIT_FIELDS,
  defaultRecapLimitDrafts,
  recapLimitError,
  type RecapLimitDrafts,
} from './recapLimits'

// ── Skeleton ──────────────────────────────────────────────────────────────────

function Skeleton() {
  return (
    <Card className="p-[var(--space-3)] space-y-[var(--space-2-5)] animate-pulse">
      <div className="h-4 w-40 rounded bg-[var(--color-border)]" />
      <div className="h-3 w-full rounded bg-[var(--color-border)]" />
      <div className="h-3 w-2/3 rounded bg-[var(--color-border)]" />
      <div className="h-3 w-3/4 rounded bg-[var(--color-border)]" />
    </Card>
  )
}

// ── Toggle row ────────────────────────────────────────────────────────────────

interface ToggleRowProps {
  id: string
  label: string
  description?: string
  checked: boolean
  onChange: (val: boolean) => void
}

function ToggleRow({ id, label, description, checked, onChange }: ToggleRowProps) {
  return (
    <div className="flex items-start justify-between gap-[var(--space-3)]">
      <div className="flex-1 min-w-0">
        <Label htmlFor={id} className="cursor-pointer">
          {label}
        </Label>
        {description && (
          <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] mt-[var(--space-0-5)] leading-relaxed">{description}</p>
        )}
      </div>
      <Switch
        id={id}
        checked={checked}
        onCheckedChange={onChange}
        aria-label={label}
      />
    </div>
  )
}

// ── Number field row ──────────────────────────────────────────────────────────

interface NumberRowProps {
  id: string
  label: string
  description?: string
  value: number
  min?: number
  step?: number
  unit?: string
  onChange: (val: number) => void
}

function NumberRow({ id, label, description, value, min = 0, step = 1, unit, onChange }: NumberRowProps) {
  return (
    <div className="space-y-[var(--space-2)]">
      <Label htmlFor={id}>
        {label}
      </Label>
      {description && (
        <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] leading-relaxed">{description}</p>
      )}
      <div className="flex items-center gap-[var(--space-2)]">
        <Input
          id={id}
          type="number"
          min={min}
          step={step}
          value={value}
          onChange={(e) => {
            // fix-3 (NaN drift): when the input is cleared or partially typed,
            // parseFloat returns NaN. Coerce to the field's min so the controlled
            // input and form state never diverge — Save always persists the
            // displayed value, not a stale value from before the clear.
            const v = parseFloat(e.target.value)
            onChange(isNaN(v) ? min : v)
          }}
          className="w-28 rounded border border-[var(--color-border)] bg-[var(--color-surface-2)] px-[var(--space-2)] py-[var(--space-1)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)] focus:outline-none"
        />
        {unit && <span className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">{unit}</span>}
      </div>
    </div>
  )
}

// ── Form state type (all fields required so setField is type-safe) ─────────────

type FormState = {
  auto_recap_enabled: boolean
  idle_timeout_minutes: number
  bootstrap_recap_enabled: boolean
  bootstrap_recap_max_per_minute: number
  recap_model: string
  /** The provider the recap model is picked under ('' = unknown, old data, #1291). */
  recap_provider: string
  /** Recap effort: '' rides the recap default, Off (FD-19). */
  recap_reasoning_effort: string
  recap_fallback_models: FallbackModel[]
  session_days: number
  memory_retros_days: number
  /** The ten "Recap limits" as typed drafts (S-82). */
  limits: RecapLimitDrafts
}

type ScalarKey = Exclude<keyof FormState, 'limits'>

// ── Defaults ──────────────────────────────────────────────────────────────────

const DEFAULT_SETTINGS: FormState = {
  auto_recap_enabled: false,
  idle_timeout_minutes: 30,
  bootstrap_recap_enabled: false,
  bootstrap_recap_max_per_minute: 5,
  recap_model: '',
  recap_fallback_models: [],
  session_days: 90,
  memory_retros_days: 180,
  recap_provider: '',
  recap_reasoning_effort: '',
  limits: defaultRecapLimitDrafts(),
}

const RECAP_RECOMMENDATION = "We recommend Off for the recap: it keeps recaps cheap, and a recap doesn't need reasoning."

/** The form values one acknowledged server document stands for. */
function formFromSettings(data: MemorySettings): FormState {
  const limits = defaultRecapLimitDrafts()
  for (const field of RECAP_LIMIT_FIELDS) {
    const stored = data[field.key]
    if (typeof stored === 'number') limits[field.key] = String(stored)
  }
  return {
    auto_recap_enabled: data.auto_recap_enabled ?? DEFAULT_SETTINGS.auto_recap_enabled,
    idle_timeout_minutes: data.idle_timeout_minutes ?? DEFAULT_SETTINGS.idle_timeout_minutes,
    bootstrap_recap_enabled: data.bootstrap_recap_enabled ?? DEFAULT_SETTINGS.bootstrap_recap_enabled,
    bootstrap_recap_max_per_minute: data.bootstrap_recap_max_per_minute ?? DEFAULT_SETTINGS.bootstrap_recap_max_per_minute,
    recap_model: data.recap_model ?? DEFAULT_SETTINGS.recap_model,
    recap_provider: data.recap_provider ?? '',
    recap_reasoning_effort: data.recap_reasoning_effort ?? '',
    recap_fallback_models: data.recap_fallback_models ?? DEFAULT_SETTINGS.recap_fallback_models,
    session_days: data.session_days ?? DEFAULT_SETTINGS.session_days,
    memory_retros_days: data.memory_retros_days ?? DEFAULT_SETTINGS.memory_retros_days,
    limits,
  }
}

function sameFallbacks(a: FallbackModel[], b: FallbackModel[]): boolean {
  const norm = (list: FallbackModel[]) =>
    JSON.stringify(list.map((f) => [f.model, f.provider ?? '', f.reasoning_effort ?? '']))
  return norm(a) === norm(b)
}

function sameValue(key: ScalarKey, a: FormState, b: FormState): boolean {
  if (key === 'recap_fallback_models') return sameFallbacks(a.recap_fallback_models, b.recap_fallback_models)
  return a[key] === b[key]
}

const SCALAR_KEYS: ScalarKey[] = [
  'auto_recap_enabled',
  'idle_timeout_minutes',
  'bootstrap_recap_enabled',
  'bootstrap_recap_max_per_minute',
  'recap_model',
  'recap_provider',
  'recap_reasoning_effort',
  'recap_fallback_models',
  'session_days',
  'memory_retros_days',
]

/** The dirty/save state: the drafts compared with the latest acknowledged baseline (FR-054). */
function isDirty(form: FormState, baseline: FormState): boolean {
  if (SCALAR_KEYS.some((key) => !sameValue(key, form, baseline))) return true
  return RECAP_LIMIT_FIELDS.some((field) => form.limits[field.key].trim() !== baseline.limits[field.key])
}

/**
 * Explicit-Save reconciliation (FR-054, r7): a field takes the response value
 * only where its draft still equals what was submitted; a field edited while
 * the request was pending keeps its draft.
 */
function reconcileAfterSave(current: FormState, submitted: FormState, acknowledged: FormState): FormState {
  const next: FormState = { ...current, limits: { ...current.limits } }
  for (const key of SCALAR_KEYS) {
    if (sameValue(key, current, submitted)) (next as Record<ScalarKey, unknown>)[key] = acknowledged[key]
  }
  for (const field of RECAP_LIMIT_FIELDS) {
    if (current.limits[field.key] === submitted.limits[field.key]) next.limits[field.key] = acknowledged.limits[field.key]
  }
  return next
}

/** One recap fallback entry for a committed slot: effort '' rides the Off default and is not stored. */
function fallbackEntry(pair: EffortPair, effort: string): FallbackModel {
  return {
    model: pair.model,
    provider: pair.provider || undefined,
    ...(effort !== '' ? { reasoning_effort: effort } : {}),
  }
}

function replaceFallback(list: FallbackModel[], model: string, entry: FallbackModel): FallbackModel[] {
  return list.map((f) => (f.model === model ? entry : f))
}

// ── Component ─────────────────────────────────────────────────────────────────

export function MemorySection(): React.ReactElement {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['memory-settings'],
    queryFn: fetchMemorySettings,
  })

  // The served catalog drives every recap effort chip: start reading it with
  // the settings so the chips usually render with their real presentation.
  useQuery(providersCatalogQueryOptions())

  const { data: providers = [], isError: providersError } = useQuery({
    queryKey: ['providers'],
    queryFn: fetchProviders,
  })

  // Usability, not key-connection (see providerStatus.ts): a subscription
  // provider is signed_in, never connected, and its models must list too.
  const usableProviders = providers.filter((p) => isProviderUsable(p.status))
  const availableModels = usableProviders.flatMap((p) => p.models ?? [])
  const providerGroups = usableProviders
    .filter((p) => (p.models ?? []).length > 0)
    .map((p) => ({ providerName: p.display_name ?? p.name ?? p.id, providerId: p.id, models: p.models ?? [] }))

  const { lookup: modelToProvider } = useModelToProvider(usableProviders)

  const [form, setForm] = useState<FormState>(DEFAULT_SETTINGS)
  // The latest acknowledged server document (FR-054): hydrated once from the
  // first read, then replaced by every completed write's response. Every
  // fallback replacement array is built from it, never from an older snapshot.
  const [baseline, setBaseline] = useState<MemorySettings | null>(null)
  const baselineRef = useRef<MemorySettings | null>(null)
  // One writer (FR-054, r6): at most one PUT in flight from this screen.
  const [writing, setWriting] = useState(false)
  const writingRef = useRef(false)
  // The mapping note of each recap slot's last switch (primary, and per fallback model).
  const [primaryNote, setPrimaryNote] = useState<string | null>(null)
  const [fallbackNotes, setFallbackNotes] = useState<Record<string, string>>({})
  const primaryChipRef = useRef<HTMLButtonElement | null>(null)
  const fallbackChipRefs = useRef<Map<string, HTMLButtonElement | null>>(new Map())
  // The chip that takes focus once a failed slot autosave has been restored (S-57).
  const [focusAfterFailure, setFocusAfterFailure] = useState<(() => HTMLElement | null) | null>(null)

  const { state: saveState, setState: setSaveState, errorMessage, setErrorMessage } = useSaveStatus()

  useEffect(() => {
    if (!data || baselineRef.current !== null) return
    baselineRef.current = data
    setBaseline(data)
    setForm(formFromSettings(data))
  }, [data])

  useEffect(() => {
    if (writing || focusAfterFailure === null) return
    // Runs after the restored chip is enabled again; the next tick lets any
    // closing popover finish its own focus return first.
    const target = focusAfterFailure
    setFocusAfterFailure(null)
    const timer = setTimeout(() => target()?.focus(), 0)
    return () => clearTimeout(timer)
  }, [writing, focusAfterFailure])

  /**
   * Sends one write. `apply` takes the acknowledged response into the form;
   * `rollback` restores what the failed write changed. The baseline is
   * updated in the same render that re-enables the disabled controls.
   */
  async function runWrite(body: MemorySettings, apply: (resp: MemorySettings) => void, rollback: () => void) {
    writingRef.current = true
    setWriting(true)
    setSaveState('saving')
    try {
      const resp = await updateMemorySettings(body)
      baselineRef.current = resp
      setBaseline(resp)
      queryClient.setQueryData(['memory-settings'], resp)
      apply(resp)
      setSaveState('saved')
    } catch (err) {
      rollback()
      setSaveState('error')
      const msg = getErrorMessage(err, 'Save failed')
      setErrorMessage(msg)
      addToast({ message: msg, variant: 'error' })
    } finally {
      writingRef.current = false
      setWriting(false)
    }
  }

  const invalidLimit = RECAP_LIMIT_FIELDS.some((field) => recapLimitError(field, form.limits[field.key]) !== null)
  const baselineForm = baseline ? formFromSettings(baseline) : null
  const dirty = baselineForm !== null && isDirty(form, baselineForm)
  // SaveStatus shows the save state against the baseline; unsaved drafts are
  // never presented as saved (FR-054 "Two independent comparisons").
  const shownSaveState = dirty && (saveState === 'idle' || saveState === 'saved') ? 'unsaved' : saveState

  function handleSave() {
    if (writingRef.current || invalidLimit) return
    const submitted = form
    const limitValues: Partial<MemorySettings> = {}
    for (const field of RECAP_LIMIT_FIELDS) limitValues[field.key] = Number(submitted.limits[field.key].trim())
    const payload: MemorySettings = {
      auto_recap_enabled: submitted.auto_recap_enabled,
      idle_timeout_minutes: submitted.idle_timeout_minutes,
      bootstrap_recap_enabled: submitted.bootstrap_recap_enabled,
      bootstrap_recap_max_per_minute: submitted.bootstrap_recap_max_per_minute,
      session_days: submitted.session_days,
      memory_retros_days: submitted.memory_retros_days,
      // Only include recap_model when non-empty (server-side "" = "use default")
      recap_model: submitted.recap_model !== '' ? submitted.recap_model : undefined,
      recap_provider: submitted.recap_model !== '' && submitted.recap_provider !== '' ? submitted.recap_provider : undefined,
      recap_reasoning_effort: submitted.recap_reasoning_effort !== '' ? submitted.recap_reasoning_effort : undefined,
      // Only include fallbacks when non-empty
      recap_fallback_models: submitted.recap_fallback_models.length > 0 ? submitted.recap_fallback_models : undefined,
      ...limitValues,
    }
    void runWrite(
      payload,
      (resp) => setForm((current) => reconcileAfterSave(current, submitted, formFromSettings(resp))),
      // A failed Save restores nothing: every draft stays (FR-054).
      () => {},
    )
  }

  /** The recap model slot changed (model and effort together): autosave that slot only (FR-054). */
  function commitPrimarySlot(pair: EffortPair, effort: string, note: string | null) {
    if (writingRef.current) return
    const acknowledged = baselineRef.current ? formFromSettings(baselineRef.current) : DEFAULT_SETTINGS
    setForm((current) => ({ ...current, recap_model: pair.model, recap_provider: pair.provider, recap_reasoning_effort: effort }))
    setPrimaryNote(note)
    void runWrite(
      { recap_model: pair.model, recap_provider: pair.provider, recap_reasoning_effort: effort },
      (resp) => {
        const saved = formFromSettings(resp)
        setForm((current) => ({
          ...current,
          recap_model: saved.recap_model,
          recap_provider: saved.recap_provider,
          recap_reasoning_effort: saved.recap_reasoning_effort,
        }))
      },
      () => {
        setForm((current) => ({
          ...current,
          recap_model: acknowledged.recap_model,
          recap_provider: acknowledged.recap_provider,
          recap_reasoning_effort: acknowledged.recap_reasoning_effort,
        }))
        setPrimaryNote(null)
        setFocusAfterFailure(() => () => primaryChipRef.current)
      },
    )
  }

  /**
   * A recap fallback slot changed. A saved row autosaves the last successful
   * baseline array with only that entry replaced (FR-054, r6); a row that
   * exists only as an unsaved draft just updates the draft.
   */
  function commitFallbackSlot(previousModel: string, pair: EffortPair, effort: string, note: string | null) {
    if (writingRef.current) return
    if (pair.model !== previousModel && form.recap_fallback_models.some((f) => f.model === pair.model)) {
      addToast({ message: `"${pair.model}" is already in the fallback list.`, variant: 'warning' })
      return
    }
    const entry = fallbackEntry(pair, effort)
    setForm((current) => ({ ...current, recap_fallback_models: replaceFallback(current.recap_fallback_models, previousModel, entry) }))
    setFallbackNotes((current) => {
      const next = { ...current }
      delete next[previousModel]
      if (note) next[pair.model] = note
      return next
    })
    const savedList = baselineRef.current?.recap_fallback_models ?? []
    const savedIndex = savedList.findIndex((f) => f.model === previousModel)
    if (savedIndex < 0) return
    const savedEntry = savedList[savedIndex]
    void runWrite(
      { recap_fallback_models: savedList.map((f, i) => (i === savedIndex ? entry : f)) },
      (resp) => {
        const acknowledgedEntry = (resp.recap_fallback_models ?? []).find((f) => f.model === pair.model) ?? entry
        setForm((current) => ({ ...current, recap_fallback_models: replaceFallback(current.recap_fallback_models, pair.model, acknowledgedEntry) }))
      },
      () => {
        setForm((current) => ({ ...current, recap_fallback_models: replaceFallback(current.recap_fallback_models, pair.model, savedEntry) }))
        setFallbackNotes((current) => {
          const next = { ...current }
          delete next[pair.model]
          return next
        })
        setFocusAfterFailure(() => () => fallbackChipRefs.current.get(savedEntry.model) ?? null)
      },
    )
  }

  function setField<K extends keyof FormState>(key: K, value: FormState[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  // Fallback chain helpers — mirrors AgentProfile's pattern
  // H6 (provider hotfix 2026-10-13): providerOverride carries the provider
  // row the user actually picked under (onPairChange fires after onChange
  // for the same pick) — the first-wins modelToProvider lookup cannot tell
  // two providers serving the same slug apart. First call seeds via the
  // lookup; the second patches to the exact pick.
  function addFallbackFromSelector(modelSlug: string, providerOverride?: string) {
    const trimmed = modelSlug.trim()
    if (!trimmed) return
    const knownProvider = providerOverride || modelToProvider(trimmed)
    if (!knownProvider) {
      addToast({
        message: `"${trimmed}" isn't listed by any connected provider — saving anyway, but the fallback may not work.`,
        variant: 'warning',
      })
    }
    setField('recap_fallback_models', (() => {
      const existing = form.recap_fallback_models.find((f) => f.model === trimmed)
      if (existing) {
        if (!providerOverride || existing.provider === providerOverride) return form.recap_fallback_models
        return form.recap_fallback_models.map((f) =>
          f.model === trimmed ? { ...f, provider: providerOverride } : f,
        )
      }
      return [...form.recap_fallback_models, { model: trimmed, provider: knownProvider || undefined }]
    })())
  }

  function removeFallback(modelSlug: string) {
    setField('recap_fallback_models', form.recap_fallback_models.filter((f) => f.model !== modelSlug))
  }

  function moveFallback(modelSlug: string, direction: -1 | 1) {
    const prev = form.recap_fallback_models
    const idx = prev.findIndex((f) => f.model === modelSlug)
    if (idx < 0) return
    const target = idx + direction
    if (target < 0 || target >= prev.length) return
    const next = prev.slice()
    const [item] = next.splice(idx, 1)
    next.splice(target, 0, item)
    setField('recap_fallback_models', next)
  }

  // ── Focus management for the fallback list (up/down/remove) ────────────────
  // Removing a row unmounts its buttons; moving a row to a list boundary
  // disables the button that was just clicked (idx===0 disables "up",
  // idx===length-1 disables "down"). Either case drops focus to <body> unless
  // we explicitly move it to a still-focusable control after the state
  // update commits. `pendingFallbackFocusRef` records what the user just did
  // (before the mutation), and the effect below — keyed on the resulting
  // list — resolves it once refs reflect the new render.
  type FallbackControlType = 'up' | 'down' | 'remove'
  const fallbackControlRefs = useRef<Map<string, Partial<Record<FallbackControlType, HTMLButtonElement | null>>>>(new Map())
  const fallbackAddContainerRef = useRef<HTMLDivElement | null>(null)
  const pendingFallbackFocusRef = useRef<{ type: FallbackControlType; index: number; direction?: -1 | 1 } | null>(null)

  function setFallbackControlRef(model: string, type: FallbackControlType, el: HTMLButtonElement | null) {
    const entry = fallbackControlRefs.current.get(model) ?? {}
    entry[type] = el
    fallbackControlRefs.current.set(model, entry)
  }

  function focusFallbackAddTrigger() {
    const trigger = fallbackAddContainerRef.current?.querySelector<HTMLElement>(
      '[data-testid="recap-fallback-add-trigger"]',
    )
    trigger?.focus()
  }

  function focusFallbackRowControl(model: string, preferred: FallbackControlType[]): boolean {
    const entry = fallbackControlRefs.current.get(model)
    if (!entry) return false
    for (const type of preferred) {
      const btn = entry[type]
      if (btn && !btn.disabled) {
        btn.focus()
        return true
      }
    }
    return false
  }

  useEffect(() => {
    const pending = pendingFallbackFocusRef.current
    if (!pending) return
    pendingFallbackFocusRef.current = null
    const { type, index, direction } = pending
    const models = form.recap_fallback_models

    if (type === 'remove') {
      if (models.length === 0) {
        focusFallbackAddTrigger()
        return
      }
      const targetIdx = Math.min(index, models.length - 1)
      const targetModel = models[targetIdx].model
      if (!focusFallbackRowControl(targetModel, ['remove', 'up', 'down'])) {
        focusFallbackAddTrigger()
      }
      return
    }

    // Reorder (up/down): the row count is unchanged — focus the control at
    // its NEW position, preferring the same control type the user just used.
    const newIdx = index + (direction ?? 0)
    if (newIdx < 0 || newIdx >= models.length) return
    const targetModel = models[newIdx].model
    const preferredOrder: FallbackControlType[] = type === 'up' ? ['up', 'down', 'remove'] : ['down', 'up', 'remove']
    focusFallbackRowControl(targetModel, preferredOrder)
  }, [form.recap_fallback_models])

  function handleMoveFallback(modelSlug: string, direction: -1 | 1) {
    const idx = form.recap_fallback_models.findIndex((f) => f.model === modelSlug)
    if (idx >= 0) {
      pendingFallbackFocusRef.current = { type: direction === -1 ? 'up' : 'down', index: idx, direction }
    }
    moveFallback(modelSlug, direction)
  }

  function handleRemoveFallback(modelSlug: string) {
    const idx = form.recap_fallback_models.findIndex((f) => f.model === modelSlug)
    if (idx >= 0) {
      pendingFallbackFocusRef.current = { type: 'remove', index: idx }
    }
    removeFallback(modelSlug)
  }

  if (isLoading) return <Skeleton />

  if (isError) {
    return (
      <p className="text-[length:var(--type-body-compact-size)]" style={{ color: 'var(--color-error)' }}>
        Failed to load memory settings:{' '}
        {error instanceof Error ? error.message : 'Unknown error'}
      </p>
    )
  }

  return (
    <section className="space-y-[var(--space-2-5)]">
      <div className="flex items-center justify-between">
        <h3 className="text-[length:var(--type-body-compact-size)] font-medium text-[var(--color-secondary)] flex items-center gap-[var(--space-1)]">
          <Brain size={14} className="text-[var(--color-muted)]" />
          Memory &amp; Recap
        </h3>
        <SaveStatus state={shownSaveState} errorMessage={errorMessage} />
      </div>

      <Card className="p-[var(--space-3)] space-y-[var(--space-3)]">
        <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] leading-relaxed">
          Global settings for automatic session recap (context compaction) and memory retention.
          These settings apply across all workspaces and agents.
        </p>

        {/* Auto recap */}
        <ToggleRow
          id="auto-recap-enabled"
          label="Auto recap"
          description="Automatically summarise sessions when they become idle, keeping the context window compact."
          checked={form.auto_recap_enabled}
          onChange={(v) => setField('auto_recap_enabled', v)}
        />

        {form.auto_recap_enabled && (
          <NumberRow
            id="idle-timeout-minutes"
            label="Idle timeout"
            description="Minutes of inactivity before a recap is triggered. Restart catch-up uses the same wait."
            value={form.idle_timeout_minutes}
            min={1}
            unit="minutes"
            onChange={(v) => setField('idle_timeout_minutes', Math.max(1, Math.round(v)))}
          />
        )}

        <div className="border-t border-[var(--color-border)] pt-[var(--space-3)] space-y-[var(--space-3)]">
          {/* Bootstrap recap */}
          <ToggleRow
            id="bootstrap-recap-enabled"
            label="Bootstrap recap"
            description="Summarise all sessions when the gateway starts. Useful for keeping memory fresh after a restart."
            checked={form.bootstrap_recap_enabled}
            onChange={(v) => setField('bootstrap_recap_enabled', v)}
          />

          {form.bootstrap_recap_enabled && (
            <NumberRow
              id="bootstrap-recap-max-per-minute"
              label="Bootstrap recap rate limit"
              description="Maximum number of bootstrap recap operations per minute."
              value={form.bootstrap_recap_max_per_minute}
              min={1}
              unit="per minute"
              onChange={(v) => setField('bootstrap_recap_max_per_minute', Math.max(1, Math.round(v)))}
            />
          )}
        </div>

        {/* Summarization model */}
        <div className="border-t border-[var(--color-border)] pt-[var(--space-3)] space-y-[var(--space-3)]">
          <div className="space-y-[var(--space-1)]">
            <p className="text-[length:var(--type-body-compact-size)] font-medium text-[var(--color-secondary)]">Summarization model</p>
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] leading-relaxed">
              Recap runs a background summarization call — a fast, cheap model is recommended.
              Leave empty to use the default model. The provider shown next to the model is
              display-only for this setting: recap runs are routed by the model name.
            </p>
          </div>

          {providersError && (
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-warning)]">
              Could not load providers. You can still enter a model slug manually.
            </p>
          )}

          {/* Primary model and its effort chip */}
          <div className="space-y-[var(--space-1)]">
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Primary model</p>
            <RecapModelSlot
              scope="recap-model"
              value={{ provider: form.recap_provider, model: form.recap_model, effort: form.recap_reasoning_effort }}
              contextName={`Effort for the recap model, ${form.recap_model}`}
              disabled={writing}
              note={primaryNote}
              chipRef={primaryChipRef}
              modelTrigger={() => document.querySelector<HTMLElement>('[data-testid="recap-model-trigger"]')}
              onCommit={commitPrimarySlot}
            >
              {({ handlers, chip }) => (
                <>
                  <ModelSelector
                    models={availableModels}
                    value={form.recap_model}
                    // The stored provider wins (recap_provider); a value saved
                    // without one resolves a row only when exactly one provider
                    // serves it (#1291) — an ambiguous slug shows no chosen row.
                    valueProvider={form.recap_provider || resolveUniqueModelProvider(usableProviders, form.recap_model)}
                    onChange={handlers.onChange}
                    onPairChange={handlers.onPairChange}
                    placeholder="Default model"
                    providerGroups={providerGroups}
                    triggerTestId="recap-model-trigger"
                    disabled={writing}
                    allowFreeTextWhenEmpty={!!providersError}
                    emptyCatalogHint="Connect a provider in Settings to pick a model"
                  />
                  {chip}
                </>
              )}
            </RecapModelSlot>
          </div>

          {/* Fallback chain */}
          <div className="space-y-[var(--space-2)]">
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">Fallback models</p>
            <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] leading-relaxed">
              Tried in order if the primary model fails.
            </p>

            {form.recap_fallback_models.length > 0 && (
              <div className="space-y-[var(--space-1)]" data-testid="recap-fallback-list">
                {form.recap_fallback_models.map((entry, idx) => {
                  const providerMissing = !entry.provider
                  const providerLabel = providerMissing
                    ? '—'
                    : (usableProviders.find((p) => p.id === entry.provider)?.display_name
                        ?? usableProviders.find((p) => p.id === entry.provider)?.name
                        ?? entry.provider)

                  return (
                    <div
                      key={entry.model}
                      data-testid={`recap-fallback-row-${entry.model}`}
                      className="flex items-center gap-[var(--space-2)] rounded border border-[var(--color-border)] bg-[var(--color-surface-2)] px-[var(--space-2)] py-[var(--space-1)]"
                    >
                      <span className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] w-4 shrink-0 text-right">{idx + 1}.</span>
                      <span
                        data-testid={`recap-fallback-provider-${entry.model}`}
                        className="inline-flex items-center px-[var(--space-1)] rounded text-[length:var(--type-caption-size)] font-semibold shrink-0"
                        style={{
                          backgroundColor: 'color-mix(in srgb, var(--color-accent) 15%, transparent)',
                          color: 'var(--color-accent)',
                          border: '1px solid color-mix(in srgb, var(--color-accent) 30%, transparent)',
                        }}
                      >
                        {providerLabel}
                      </span>
                      <RecapModelSlot
                        scope="recap-fallback"
                        value={{ provider: entry.provider ?? '', model: entry.model, effort: entry.reasoning_effort ?? '' }}
                        contextName={`Effort for recap fallback ${idx + 1}, ${entry.model}`}
                        disabled={writing}
                        note={fallbackNotes[entry.model] ?? null}
                        chipRef={(el) => {
                          fallbackChipRefs.current.set(entry.model, el)
                        }}
                        modelTrigger={() =>
                          Array.from(document.querySelectorAll<HTMLElement>('[data-testid^="recap-fallback-model-"]')).find(
                            (el) => el.dataset.testid === `recap-fallback-model-${entry.model}`,
                          ) ?? null
                        }
                        onCommit={(pair, effort, note) => commitFallbackSlot(entry.model, pair, effort, note)}
                      >
                        {({ handlers, chip }) => (
                          <div className="flex flex-1 min-w-0 flex-wrap items-center gap-[var(--space-2)]">
                            <div className="flex-1 min-w-0">
                              <ModelSelector
                                models={availableModels}
                                value={entry.model}
                                valueProvider={entry.provider ?? ''}
                                onChange={handlers.onChange}
                                onPairChange={handlers.onPairChange}
                                placeholder="Fallback model"
                                providerGroups={providerGroups}
                                triggerTestId={`recap-fallback-model-${entry.model}`}
                                variant="ghost"
                                constrainToCatalog
                                disabled={writing}
                                allowFreeTextWhenEmpty={!!providersError}
                                emptyCatalogHint="Connect a provider to change fallbacks"
                              />
                            </div>
                            {chip}
                          </div>
                        )}
                      </RecapModelSlot>
                      {providerMissing && (
                        <span
                          data-testid={`recap-fallback-warning-${entry.model}`}
                          role="img"
                          aria-label="Provider not connected — fallback may not work at runtime"
                          title="Provider not connected — fallback may not work at runtime"
                          className="inline-flex items-center text-[var(--color-warning)] shrink-0"
                        >
                          <Warning size={12} weight="fill" />
                        </span>
                      )}
                      <IconButton
                        variant="ghost"
                        ref={(el) => setFallbackControlRef(entry.model, 'up', el)}
                        type="button"
                        data-testid={`recap-fallback-up-${entry.model}`}
                        aria-label={`Move fallback ${entry.model} up`}
                        disabled={writing || idx === 0}
                        onClick={() => handleMoveFallback(entry.model, -1)}
                        className="h-auto w-auto p-0 text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)]"
                      >
                        <ArrowUp size={12} />
                      </IconButton>
                      <IconButton
                        variant="ghost"
                        ref={(el) => setFallbackControlRef(entry.model, 'down', el)}
                        type="button"
                        data-testid={`recap-fallback-down-${entry.model}`}
                        aria-label={`Move fallback ${entry.model} down`}
                        disabled={writing || idx === form.recap_fallback_models.length - 1}
                        onClick={() => handleMoveFallback(entry.model, 1)}
                        className="h-auto w-auto p-0 text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)]"
                      >
                        <ArrowDown size={12} />
                      </IconButton>
                      <IconButton
                        variant="ghost"
                        ref={(el) => setFallbackControlRef(entry.model, 'remove', el)}
                        type="button"
                        data-testid={`recap-fallback-remove-${entry.model}`}
                        aria-label={`Remove fallback ${entry.model}`}
                        disabled={writing}
                        onClick={() => handleRemoveFallback(entry.model)}
                        className="h-auto w-auto p-0 text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-error)]"
                      >
                        <X size={12} />
                      </IconButton>
                    </div>
                  )
                })}
              </div>
            )}

            <div data-testid="recap-fallback-add" ref={fallbackAddContainerRef}>
              <ModelSelector
                models={availableModels}
                value=""
                onChange={(v) => addFallbackFromSelector(v)}
                // H6: the grouped pick's exact provider overrides the
                // first-wins lookup — see addFallbackFromSelector.
                onPairChange={({ model: m, provider: p }) => addFallbackFromSelector(m, p)}
                placeholder="Add fallback model…"
                providerGroups={providerGroups}
                triggerTestId="recap-fallback-add-trigger"
                disabled={writing}
                constrainToCatalog
                allowFreeTextWhenEmpty={!!providersError}
                emptyCatalogHint="Connect a provider to add fallbacks"
              />
            </div>
          </div>

          <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] leading-relaxed">
            {RECAP_RECOMMENDATION}
          </p>

          <RecapLimitsArea
            drafts={form.limits}
            onChange={(key, draft) => setForm((prev) => ({ ...prev, limits: { ...prev.limits, [key]: draft } }))}
            onReset={() => setForm((prev) => ({ ...prev, limits: defaultRecapLimitDrafts() }))}
            chatGptRoute={
              form.recap_provider === CHATGPT_SIGN_IN_PROVIDER ||
              form.recap_fallback_models.some((f) => f.provider === CHATGPT_SIGN_IN_PROVIDER)
            }
          />
        </div>

        <div className="border-t border-[var(--color-border)] pt-[var(--space-3)] space-y-[var(--space-3)]">
          <p className="text-[length:var(--type-utility-xs-size)] font-semibold text-[var(--color-secondary)]">Retention</p>

          <NumberRow
            id="session-days"
            label="Session retention"
            description="Number of days to retain session JSONL files before the retention sweep removes them. Set 0 to use the system default (90 days)."
            value={form.session_days}
            min={0}
            unit="days"
            onChange={(v) => setField('session_days', Math.max(0, Math.round(v)))}
          />

          <NumberRow
            id="memory-retros-days"
            label="Memory retrospective retention"
            description="Number of days to retain memory retrospective files."
            value={form.memory_retros_days}
            min={0}
            unit="days"
            onChange={(v) => setField('memory_retros_days', Math.max(0, Math.round(v)))}
          />
        </div>

        <div className="border-t border-[var(--color-border)] pt-[var(--space-3)] flex justify-end">
          <Button
            size="sm"
            onClick={handleSave}
            disabled={writing || invalidLimit}
            aria-label="Save memory settings"
          >
            {writing ? 'Saving...' : 'Save'}
          </Button>
        </div>
      </Card>
    </section>
  )
}
