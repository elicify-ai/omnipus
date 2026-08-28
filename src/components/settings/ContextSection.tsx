// ContextSection — Settings → Models (ADR-066 D9, FR-036 / FR-037).
//
// Global context-budget controls: the three per-surface tool-result caps
// (D4), the absolute mid-turn trigger (D6), the ingest bound (D10), the
// global default context window (D2 rung 3) and the per-(provider, model)
// window overrides (D2 rung 2). Everything renders from the generated
// ContextSettings / ContextSettingsUpdate types only (Constraint #8).
//
// Write semantics (FR-036): PUT is a PARTIAL update — Save sends only the
// fields that differ from the last GET (`model_overrides` is compared as a
// whole list and sent whole, since the wire replaces the list). Limits are
// validated client-side with the same field + limit copy the gateway uses
// for its 400 (B-14), and a server 400 that names a field renders on that
// field. Every successful write reloads the registry on the gateway — there
// is no restart gate, so the UI shows plain Saved.
//
// X-08 (ADR-068): `?provider=&model=` on the Settings route pre-fills a new
// override row (or focuses the existing one) so the "No context length"
// pointer lands on the right row.
import { useState, useEffect, useMemo } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Sliders, Plus, X, Warning } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { getContextSettings, putContextSettings, getErrorMessage, isApiError } from '@/lib/api'
import type { ContextSettings, ContextSettingsUpdate, ContextModelOverride, ContextWindowSource } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { SaveStatus, useSaveStatus } from './SaveStatus'

// ── Limits (ADR-066 §5 / FR-036; mirrored by the gateway's 400s) ──────────────

export const CAP_CEILING = 150_000
export const INGEST_BOUND_EXCLUSIVE_MAX = 8_388_608 // must be strictly below

const fmt = (n: number) => n.toLocaleString('en-US')

// Shared source-label copy for ContextWindowSource (T068-30 reuses it).
export const CONTEXT_WINDOW_SOURCE_LABEL: Record<ContextWindowSource, string> = {
  operator: 'Operator override',
  live: 'Provider (live)',
  catalog: 'Catalog',
  floor: 'Conservative floor',
}

// ── Form state ────────────────────────────────────────────────────────────────
// Numeric fields are kept as strings so a cleared input stays cleared (and is
// reported as an error on Save) instead of silently snapping to a minimum.

type NumericKey =
  | 'mcp_result_cap'
  | 'builtin_success_cap'
  | 'builtin_failure_cap'
  | 'absolute_trigger_chars'
  | 'ingest_bound_bytes'

type OverrideRow = { provider: string; model: string; context_window: string }

type FormState = Record<NumericKey, string> & {
  default_context_window: string
  model_overrides: OverrideRow[]
}

type FieldErrors = Record<string, string>

function toForm(s: ContextSettings): FormState {
  return {
    mcp_result_cap: String(s.mcp_result_cap),
    builtin_success_cap: String(s.builtin_success_cap),
    builtin_failure_cap: String(s.builtin_failure_cap),
    absolute_trigger_chars: String(s.absolute_trigger_chars),
    ingest_bound_bytes: String(s.ingest_bound_bytes),
    default_context_window:
      s.default_context_window === undefined || s.default_context_window === null
        ? ''
        : String(s.default_context_window),
    model_overrides: s.model_overrides.map((o) => ({
      provider: o.provider,
      model: o.model,
      context_window: String(o.context_window),
    })),
  }
}

function parseInt_(raw: string): number | null {
  if (raw.trim() === '') return null
  const n = Number(raw)
  return Number.isInteger(n) ? n : Number.NaN
}

const NUMERIC_FIELDS: Array<{
  key: NumericKey
  label: string
  description: string
  unit: string
  max?: number
  maxExclusive?: number
}> = [
  {
    key: 'mcp_result_cap',
    label: 'MCP tool result cap',
    description: 'Largest successful MCP tool result kept in the conversation window. Longer results are cut to this size; the full text stays in the session archive.',
    unit: 'characters',
    max: CAP_CEILING,
  },
  {
    key: 'builtin_success_cap',
    label: 'Built-in tool result cap',
    description: 'Largest successful built-in tool result, attachment, recalled page or delegate report kept in the window.',
    unit: 'characters',
    max: CAP_CEILING,
  },
  {
    key: 'builtin_failure_cap',
    label: 'Failed tool result cap',
    description: 'Largest failed, denied or skipped tool result kept in the window — built-in or MCP.',
    unit: 'characters',
    max: CAP_CEILING,
  },
  {
    key: 'absolute_trigger_chars',
    label: 'Mid-turn check trigger',
    description: 'When tool results in a single turn reach this many characters, the window is re-checked before the next model call.',
    unit: 'characters',
  },
  {
    key: 'ingest_bound_bytes',
    label: 'Ingest bound',
    description: 'Most bytes read from any network or subprocess source in one go. Must stay below 8,388,608.',
    unit: 'bytes',
    maxExclusive: INGEST_BOUND_EXCLUSIVE_MAX,
  },
]

// ── Validation (B-14) — same field + limit naming as the gateway 400 ──────────

function validate(form: FormState): { errors: FieldErrors; parsed: Partial<ContextSettings> } {
  const errors: FieldErrors = {}
  const parsed: Partial<ContextSettings> = {}

  for (const f of NUMERIC_FIELDS) {
    const n = parseInt_(form[f.key])
    if (n === null || Number.isNaN(n)) {
      errors[f.key] = `${f.label} must be a whole number.`
      continue
    }
    if (n < 1) {
      errors[f.key] = `${f.label} must be at least 1.`
      continue
    }
    if (f.max !== undefined && n > f.max) {
      errors[f.key] = `${f.label} must be at most ${fmt(f.max)}.`
      continue
    }
    if (f.maxExclusive !== undefined && n >= f.maxExclusive) {
      errors[f.key] = `${f.label} must be below ${fmt(f.maxExclusive)}.`
      continue
    }
    parsed[f.key] = n
  }

  const dw = parseInt_(form.default_context_window)
  if (dw === null) {
    parsed.default_context_window = null
  } else if (Number.isNaN(dw) || dw < 1) {
    errors.default_context_window = 'Default context window must be a whole number of at least 1, or empty to unset.'
  } else {
    parsed.default_context_window = dw
  }

  const overrides: ContextModelOverride[] = []
  form.model_overrides.forEach((row, i) => {
    const provider = row.provider.trim()
    const model = row.model.trim()
    const cw = parseInt_(row.context_window)
    let ok = true
    if (provider === '') {
      errors[`model_overrides.${i}.provider`] = 'Provider is required.'
      ok = false
    }
    if (model === '') {
      errors[`model_overrides.${i}.model`] = 'Model is required.'
      ok = false
    }
    if (cw === null || Number.isNaN(cw) || cw < 1) {
      errors[`model_overrides.${i}.context_window`] = 'Context length must be a whole number of at least 1.'
      ok = false
    }
    if (ok) overrides.push({ provider, model, context_window: cw as number })
  })
  parsed.model_overrides = overrides

  return { errors, parsed }
}

// Diff against the last GET → the partial PUT body (FR-036).
function buildPartial(parsed: Partial<ContextSettings>, base: ContextSettings): ContextSettingsUpdate {
  const body: ContextSettingsUpdate = {}
  for (const f of NUMERIC_FIELDS) {
    const v = parsed[f.key]
    if (v !== undefined && v !== base[f.key]) body[f.key] = v
  }
  const baseDw = base.default_context_window ?? null
  if (parsed.default_context_window !== undefined && parsed.default_context_window !== baseDw) {
    body.default_context_window = parsed.default_context_window
  }
  const ov = parsed.model_overrides ?? []
  const same =
    ov.length === base.model_overrides.length &&
    ov.every(
      (o, i) =>
        o.provider === base.model_overrides[i].provider &&
        o.model === base.model_overrides[i].model &&
        o.context_window === base.model_overrides[i].context_window,
    )
  if (!same) body.model_overrides = ov
  return body
}

// A gateway 400 names the field and the limit (FR-036). Map it onto the field
// when the body carries `details.field`, else by the field name appearing in
// the message; otherwise it lands on the banner.
function fieldFromError(err: unknown): { field?: string; message: string } {
  const message = getErrorMessage(err, 'Save failed')
  if (!isApiError(err) || err.status !== 400) return { message }
  let field: string | undefined
  if (err.body) {
    try {
      const parsed = JSON.parse(err.body) as { details?: { field?: unknown } }
      if (typeof parsed.details?.field === 'string') field = parsed.details.field
    } catch {
      /* not JSON — fall through to the name scan */
    }
  }
  if (!field) {
    const known = [...NUMERIC_FIELDS.map((f) => f.key), 'default_context_window', 'model_overrides']
    field = known.find((k) => err.userMessage.includes(k))
  }
  return { field, message: err.userMessage }
}

// ── Skeleton ──────────────────────────────────────────────────────────────────

function Skeleton() {
  return (
    <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-3 animate-pulse">
      <div className="h-4 w-40 rounded bg-[var(--color-border)]" />
      <div className="h-3 w-full rounded bg-[var(--color-border)]" />
      <div className="h-3 w-2/3 rounded bg-[var(--color-border)]" />
    </div>
  )
}

function FieldError({ id, message }: { id: string; message?: string }) {
  if (!message) return null
  return (
    <p id={id} data-testid={`context-error-${id.replace(/^context-error-/, '')}`} role="alert" className="text-xs mt-1" style={{ color: 'var(--color-error)' }}>
      {message}
    </p>
  )
}

// ── Component ─────────────────────────────────────────────────────────────────

export interface ContextSectionProps { // not-wire-format: React props, never serialized
  /** X-08: pre-fill (or focus) an override row for this (provider, model). */
  prefillOverride?: { provider: string; model: string }
}

export function ContextSection({ prefillOverride }: ContextSectionProps): React.ReactElement {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['context-settings'],
    queryFn: getContextSettings,
  })

  const [form, setForm] = useState<FormState | null>(null)
  const [errors, setErrors] = useState<FieldErrors>({})
  const [bannerError, setBannerError] = useState<string | null>(null)
  const { state: saveState, setState: setSaveState, errorMessage, setErrorMessage } = useSaveStatus()

  // Hydrate from GET; apply the X-08 pre-fill once on top of the fetched rows.
  useEffect(() => {
    if (!data) return
    const next = toForm(data)
    if (prefillOverride) {
      const exists = next.model_overrides.some(
        (r) => r.provider === prefillOverride.provider && r.model === prefillOverride.model,
      )
      if (!exists) {
        next.model_overrides.push({ provider: prefillOverride.provider, model: prefillOverride.model, context_window: '' })
      }
    }
    setForm(next)
    setErrors({})
  }, [data, prefillOverride])

  const { mutate: save, isPending: isSaving } = useMutation({
    mutationFn: (body: ContextSettingsUpdate) => putContextSettings(body),
    onMutate: () => {
      setSaveState('saving')
      setBannerError(null)
    },
    onSuccess: (resp) => {
      setSaveState('saved')
      queryClient.setQueryData(['context-settings'], resp)
    },
    onError: (err: Error) => {
      setSaveState('error')
      const { field, message } = fieldFromError(err)
      setErrorMessage(message)
      if (field) setErrors((e) => ({ ...e, [field]: message }))
      else setBannerError(message)
      addToast({ message, variant: 'error' })
    },
  })

  const effectiveSource = useMemo<ContextWindowSource | null>(() => {
    if (!data) return null
    return data.default_context_window === undefined || data.default_context_window === null ? null : 'operator'
  }, [data])

  if (isLoading || !form) return <Skeleton />
  if (isError) {
    return (
      <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 text-sm" style={{ color: 'var(--color-error)' }} role="alert">
        <Warning size={14} className="inline mr-1" weight="fill" />
        Could not load context settings: {getErrorMessage(error, 'unknown error')}
      </div>
    )
  }

  const setNumeric = (key: NumericKey | 'default_context_window', value: string) => {
    setForm((f) => (f ? { ...f, [key]: value } : f))
    setErrors((e) => {
      if (!(key in e)) return e
      const rest = { ...e }
      delete rest[key]
      return rest
    })
  }

  const setRow = (i: number, patch: Partial<OverrideRow>) => {
    setForm((f) => {
      if (!f) return f
      const rows = f.model_overrides.slice()
      rows[i] = { ...rows[i], ...patch }
      return { ...f, model_overrides: rows }
    })
    setErrors((e) => {
      const rest: FieldErrors = {}
      for (const k of Object.keys(e)) if (!k.startsWith(`model_overrides.${i}.`)) rest[k] = e[k]
      return rest
    })
  }

  const addRow = () =>
    setForm((f) => (f ? { ...f, model_overrides: [...f.model_overrides, { provider: '', model: '', context_window: '' }] } : f))

  const removeRow = (i: number) => {
    setForm((f) => (f ? { ...f, model_overrides: f.model_overrides.filter((_, j) => j !== i) } : f))
    setErrors((e) => {
      const rest: FieldErrors = {}
      for (const k of Object.keys(e)) if (!k.startsWith('model_overrides.')) rest[k] = e[k]
      return rest
    })
  }

  function handleSave() {
    if (!data) return
    const { errors: v, parsed } = validate(form as FormState)
    setErrors(v)
    if (Object.keys(v).length > 0) {
      setSaveState('error')
      setErrorMessage('Fix the highlighted fields.')
      return
    }
    const body = buildPartial(parsed, data)
    if (Object.keys(body).length === 0) {
      setSaveState('saved')
      return
    }
    save(body)
  }

  const inputCls =
    'w-40 rounded border bg-[var(--color-surface-2)] px-2 py-1 text-sm text-[var(--color-secondary)] focus:outline-none'
  const borderFor = (key: string) =>
    errors[key] ? 'border-[var(--color-error)]' : 'border-[var(--color-border)]'

  return (
    <div className="space-y-6" data-testid="context-section">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="font-headline text-lg font-semibold text-[var(--color-secondary)] flex items-center gap-2">
            <Sliders size={18} /> Models
          </h2>
          <p className="text-xs text-[var(--color-muted)] mt-0.5 leading-relaxed">
            Context-window budget: how much of each tool result stays in the conversation, when the window is
            re-checked mid-turn, and which context length each model is assumed to have. Changes apply on the next
            turn — no restart.
          </p>
        </div>
      </div>

      {bannerError && (
        <div role="alert" data-testid="context-error-banner" className="rounded border border-[var(--color-error)] px-3 py-2 text-xs" style={{ color: 'var(--color-error)' }}>
          {bannerError}
        </div>
      )}

      {/* Caps, trigger, ingest bound */}
      <section className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-4">
        <h3 className="text-sm font-semibold text-[var(--color-secondary)]">Tool results and limits</h3>
        {NUMERIC_FIELDS.map((f) => {
          const id = `context-${f.key.replace(/_/g, '-')}`
          const err = errors[f.key]
          return (
            <div key={f.key} className="space-y-1">
              <label htmlFor={id} className="text-sm font-medium text-[var(--color-secondary)]">
                {f.label}
              </label>
              <p className="text-xs text-[var(--color-muted)] leading-relaxed">{f.description}</p>
              <div className="flex items-center gap-2">
                <Input
                  id={id}
                  data-testid={id}
                  type="number"
                  inputMode="numeric"
                  min={1}
                  max={f.max ?? (f.maxExclusive !== undefined ? f.maxExclusive - 1 : undefined)}
                  step={1}
                  value={form[f.key]}
                  aria-invalid={err ? 'true' : undefined}
                  aria-describedby={err ? `context-error-${f.key}` : undefined}
                  onChange={(e) => setNumeric(f.key, e.target.value)}
                  className={`${inputCls} ${borderFor(f.key)}`}
                />
                <span className="text-xs text-[var(--color-muted)]">{f.unit}</span>
              </div>
              <FieldError id={`context-error-${f.key}`} message={err} />
            </div>
          )
        })}
      </section>

      {/* Global default window */}
      <section className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-2">
        <h3 className="text-sm font-semibold text-[var(--color-secondary)]">Default context window</h3>
        <p className="text-xs text-[var(--color-muted)] leading-relaxed">
          Used when neither the agent nor a model override sets a context length. It is clamped to what the model
          actually supports. Leave empty to let the provider catalog decide.
        </p>
        <div className="flex items-center gap-2">
          <Input
            id="context-default-window"
            data-testid="context-default-window"
            type="number"
            inputMode="numeric"
            min={1}
            step={1}
            placeholder="Not set"
            value={form.default_context_window}
            aria-invalid={errors.default_context_window ? 'true' : undefined}
            aria-describedby={errors.default_context_window ? 'context-error-default_context_window' : undefined}
            onChange={(e) => setNumeric('default_context_window', e.target.value)}
            className={`${inputCls} ${borderFor('default_context_window')}`}
          />
          <span className="text-xs text-[var(--color-muted)]">tokens</span>
          <span data-testid="context-default-window-source" className="text-xs text-[var(--color-muted)]">
            Source: {effectiveSource ? CONTEXT_WINDOW_SOURCE_LABEL[effectiveSource] : 'not set (catalog, live or floor per model)'}
          </span>
        </div>
        <FieldError id="context-error-default_context_window" message={errors.default_context_window} />
      </section>

      {/* Model overrides */}
      <section className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-3" id="model-overrides">
        <div className="flex items-start justify-between gap-4">
          <div>
            <h3 className="text-sm font-semibold text-[var(--color-secondary)]">Model overrides</h3>
            <p className="text-xs text-[var(--color-muted)] leading-relaxed">
              Set the context length for a specific provider and model — for endpoints that do not report one, or to
              keep a model below its advertised limit. An override can only lower the effective window, never raise it.
            </p>
          </div>
          <Button type="button" variant="outline" size="sm" data-testid="context-override-add" onClick={addRow}>
            <Plus size={14} className="mr-1" /> Add override
          </Button>
        </div>

        {form.model_overrides.length === 0 ? (
          <p data-testid="context-overrides-empty" className="text-xs text-[var(--color-muted)]">
            No overrides. Every model uses its catalog or live context length.
          </p>
        ) : (
          <ul className="space-y-2">
            {form.model_overrides.map((row, i) => {
              const pk = `model_overrides.${i}.provider`
              const mk = `model_overrides.${i}.model`
              const wk = `model_overrides.${i}.context_window`
              return (
                <li key={i} data-testid="context-override-row" className="rounded border border-[var(--color-border)] p-2 space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <Input
                      aria-label="Provider"
                      data-testid="context-override-provider"
                      placeholder="provider id"
                      value={row.provider}
                      aria-invalid={errors[pk] ? 'true' : undefined}
                      onChange={(e) => setRow(i, { provider: e.target.value })}
                      className={`${inputCls} ${borderFor(pk)}`}
                    />
                    <Input
                      aria-label="Model"
                      data-testid="context-override-model"
                      placeholder="model id"
                      value={row.model}
                      aria-invalid={errors[mk] ? 'true' : undefined}
                      onChange={(e) => setRow(i, { model: e.target.value })}
                      className={`${inputCls} w-56 ${borderFor(mk)}`}
                    />
                    <Input
                      aria-label="Context length"
                      data-testid="context-override-window"
                      type="number"
                      inputMode="numeric"
                      min={1}
                      step={1}
                      placeholder="tokens"
                      value={row.context_window}
                      aria-invalid={errors[wk] ? 'true' : undefined}
                      onChange={(e) => setRow(i, { context_window: e.target.value })}
                      className={`${inputCls} w-32 ${borderFor(wk)}`}
                    />
                    <Button type="button" variant="ghost" size="sm" aria-label="Remove override" data-testid="context-override-remove" onClick={() => removeRow(i)}>
                      <X size={14} />
                    </Button>
                  </div>
                  <FieldError id={`context-error-${pk}`} message={errors[pk]} />
                  <FieldError id={`context-error-${mk}`} message={errors[mk]} />
                  <FieldError id={`context-error-${wk}`} message={errors[wk]} />
                </li>
              )
            })}
          </ul>
        )}
        <FieldError id="context-error-model_overrides" message={errors.model_overrides} />
      </section>

      <div className="flex items-center justify-end gap-3">
        <SaveStatus state={saveState} errorMessage={errorMessage} />
        <Button type="button" data-testid="context-save" onClick={handleSave} disabled={isSaving}>
          Save
        </Button>
      </div>
    </div>
  )
}
