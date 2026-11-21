// ContextSection — Settings → Limits → Context and tool results, and Model
// overrides (ADR-066 D9, FR-036 / FR-037; Settings redesign #1519: on the
// Save bar, FR-010). Formerly the Models tab.
//
// Global context-budget controls: the three per-surface tool-result caps
// (D4), the relative tool-result share limit (D6), the ingest bound (D10), the
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
// The form lives in useContextForm so the Limits section can put it on the
// same Save bar as Agents and goals (FR-019); ContextSection renders it alone.
//
// X-08 (ADR-068): `?provider=&model=` on the Settings route pre-fills a new
// override row (or focuses the existing one) so the "No context length"
// pointer lands on the right row.
import { ReadErrorCallout } from './ReadErrorCallout'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus, X } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { getContextSettings, putContextSettings } from '@/lib/api'
import type { ContextSettings, ContextSettingsUpdate, ContextModelOverride, ContextWindowSource } from '@/lib/api'
import { FormRow, SaveBarPanel, SaveOutcomeLine } from './SaveBar'
import { checkFormPart, toWriteResponse, type PartCheck, type WriteResponse } from './saveOutcome'
import { useFormSaveBar, type FormSavePart } from './useFormSaveBar'

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
  | 'tool_result_share_fraction'
  | 'ingest_bound_bytes'

type OverrideRow = { provider: string; model: string; context_window: string }

type FormState = Record<NumericKey, string> & {
  default_context_window: string
  model_overrides: OverrideRow[]
}

type FieldErrors = Record<string, string>

// Shift the decimal point without rounding fractional percentages or adding
// binary multiplication artifacts (for example, 0.29 must display as 29%).
function fractionToPercent(fraction: number): string {
  const [coefficient, exponent = '0'] = String(fraction).split('e')
  const [whole, decimal = ''] = coefficient.split('.')
  const digits = whole + decimal
  const point = whole.length + Number(exponent) + 2
  const percent = point <= 0
    ? `0.${'0'.repeat(-point)}${digits}`
    : point >= digits.length
      ? digits + '0'.repeat(point - digits.length)
      : `${digits.slice(0, point)}.${digits.slice(point)}`
  return percent.replace(/^0+(?=\d)/, '')
}

function toForm(s: ContextSettings): FormState {
  return {
    mcp_result_cap: String(s.mcp_result_cap),
    builtin_success_cap: String(s.builtin_success_cap),
    builtin_failure_cap: String(s.builtin_failure_cap),
    tool_result_share_fraction: fractionToPercent(s.tool_result_share_fraction),
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
    key: 'tool_result_share_fraction',
    label: 'Tool-result share limit',
    description: 'Maximum share of the model’s effective context window (W) used by tool results, not the remaining request budget. Default: 50%.',
    unit: '%',
    max: 100,
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
    if (f.key === 'tool_result_share_fraction') {
      const raw = form[f.key].trim()
      const percent = Number(raw)
      const [coefficient, exponent = '0'] = raw.toLowerCase().split('e')
      const fraction = Number(`${coefficient}e${Number(exponent) - 2}`)
      if (!Number.isFinite(percent) || !Number.isFinite(fraction) || fraction <= 0 || percent > 100) {
        errors[f.key] = `${f.label} must be a number greater than 0% and at most 100%.`
      } else {
        parsed[f.key] = fraction
      }
      continue
    }
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



export interface ContextSectionProps { // not-wire-format: React props, never serialized
  /** X-08: pre-fill (or focus) an override row for this (provider, model). */
  prefillOverride?: { provider: string; model: string }
}

export const CONTEXT_QUERY_KEY = ['context-settings'] as const

function sameForm(a: FormState, b: FormState, key: NumericKey | 'default_context_window'): boolean {
  return a[key].trim() === b[key].trim()
}

function sameOverrides(a: OverrideRow[], b: OverrideRow[]): boolean {
  return (
    a.length === b.length &&
    a.every((r, i) => r.provider.trim() === b[i].provider.trim() && r.model.trim() === b[i].model.trim() && r.context_window.trim() === b[i].context_window.trim())
  )
}

/** Context and tool results plus Model overrides as one Save-bar part. */
export function useContextForm({ prefillOverride }: ContextSectionProps = {}) {
  const queryClient = useQueryClient()
  const { data, isLoading, isError, error, refetch, isFetching } = useQuery({
    queryKey: CONTEXT_QUERY_KEY,
    queryFn: getContextSettings,
  })
  // The saved values known to the screen: the last successful answer, else the read.
  const [savedSettings, setSavedSettings] = useState<ContextSettings | null>(null)
  const base = savedSettings ?? data ?? null
  const baseline = useMemo(() => (base ? toForm(base) : null), [base])
  const [form, setForm] = useState<FormState | null>(null)
  const [serverErrors, setServerErrors] = useState<FieldErrors>({})
  const sentRef = useRef<{ form: FormState; body: ContextSettingsUpdate; base: ContextSettings } | null>(null)

  // Hydrate from GET; apply the X-08 pre-fill once on top of the fetched rows:
  // one new unsaved row (FR-004).
  useEffect(() => {
    if (!data || form) return
    const next = toForm(data)
    if (prefillOverride) {
      const exists = next.model_overrides.some((r) => r.provider === prefillOverride.provider && r.model === prefillOverride.model)
      if (!exists) next.model_overrides.push({ provider: prefillOverride.provider, model: prefillOverride.model, context_window: '' })
    }
    setForm(next)
  }, [data, form, prefillOverride])

  const { errors: localErrors } = useMemo(() => (form ? validate(form) : { errors: {} as FieldErrors }), [form])
  const errors = { ...serverErrors, ...localErrors }

  const changedFields = useMemo(() => {
    if (!form || !baseline) return [] as string[]
    const out: string[] = []
    for (const f of NUMERIC_FIELDS) if (!sameForm(form, baseline, f.key)) out.push(f.key)
    if (!sameForm(form, baseline, 'default_context_window')) out.push('default_context_window')
    if (!sameOverrides(form.model_overrides, baseline.model_overrides)) out.push('model_overrides')
    return out
  }, [form, baseline])

  const clearServerError = (prefix: string) =>
    setServerErrors((e) => {
      const rest: FieldErrors = {}
      for (const k of Object.keys(e)) if (!k.startsWith(prefix)) rest[k] = e[k]
      return rest
    })

  const setNumeric = (key: NumericKey | 'default_context_window', value: string) => {
    setForm((f) => (f ? { ...f, [key]: value } : f))
    clearServerError(key)
  }
  const setRow = (i: number, patch: Partial<OverrideRow>) => {
    setForm((f) => {
      if (!f) return f
      const rows = f.model_overrides.slice()
      rows[i] = { ...rows[i], ...patch }
      return { ...f, model_overrides: rows }
    })
    clearServerError('model_overrides')
  }
  const addRow = () => setForm((f) => (f ? { ...f, model_overrides: [...f.model_overrides, { provider: '', model: '', context_window: '' }] } : f))
  const removeRow = (i: number) => {
    setForm((f) => (f ? { ...f, model_overrides: f.model_overrides.filter((_, j) => j !== i) } : f))
    clearServerError('model_overrides')
  }

  const write = async (): Promise<WriteResponse> => {
    if (!form || !base) return { ok: false, status: 400, json: async () => ({ error: 'The context settings are not loaded.' }) }
    const body = buildPartial(validate(form).parsed, base)
    sentRef.current = { form, body, base }
    return toWriteResponse(() => putContextSettings(body))
  }

  const part: FormSavePart = {
    id: 'context',
    count: changedFields.length,
    invalidCount: form ? Object.keys(localErrors).length : 0,
    write,
    onOutcome: (outcome) => {
      const sent = sentRef.current
      if (outcome.kind === 'saved' || outcome.kind === 'saved-not-in-use') {
        const resp = outcome.body as ContextSettings | undefined
        const nextBase = resp && Array.isArray(resp.model_overrides) ? resp : null
        if (nextBase) {
          setSavedSettings(nextBase)
          queryClient.setQueryData(CONTEXT_QUERY_KEY, nextBase)
        } else {
          // No settings in the answer (saved, not in use; or the saved-state
          // check): the sent values are the saved values known to the screen,
          // never a running read that may still show the old ones (FR-012).
          if (sent) setSavedSettings({ ...sent.base, ...sent.body } as ContextSettings)
          void queryClient.invalidateQueries({ queryKey: CONTEXT_QUERY_KEY })
        }
        if (sent && nextBase) {
          // Show what is stored now; an edit made while saving is kept.
          setForm((f) => (f === sent.form ? toForm(nextBase) : f))
        }
        setServerErrors({})
      } else if (outcome.kind === 'not-saved') {
        const field = outcome.field ?? [...NUMERIC_FIELDS.map((f) => f.key), 'default_context_window', 'model_overrides'].find((k) => outcome.message.includes(k))
        if (field) setServerErrors({ [field]: outcome.message })
      }
    },
    // C-6 saved-state check (FR-020 outcome 4): GET /config/pending-restart
    // names a saved-but-not-applied value as context.<field>; any other
    // value is read from GET /settings/context.
    check: async (): Promise<PartCheck> => {
      const sent = sentRef.current
      if (!sent) return { kind: 'unknown' }
      const before = sent.base as unknown as Record<string, unknown>
      let fresh: Record<string, unknown> | null = null
      const result = await checkFormPart(
        Object.entries(sent.body).map(([name, v]) => ({
          key: `context.${name}`,
          label: name === 'model_overrides' ? 'Model overrides' : (NUMERIC_FIELDS.find((f) => f.key === name)?.label ?? 'Default context window'),
          draft: v ?? null,
          before: before[name] ?? null,
        })),
        async (configKey) => {
          fresh ??= (await getContextSettings()) as unknown as Record<string, unknown>
          return fresh[configKey.slice('context.'.length)] ?? null
        },
      )
      if (result.kind === 'changed-elsewhere') {
        // The read values become the saved values; the drafts stay.
        const next: Record<string, unknown> = { ...before }
        for (const [configKey, stored] of Object.entries(result.saved)) next[configKey.slice('context.'.length)] = stored
        setSavedSettings(next as unknown as ContextSettings)
      }
      return result
    },
    focusInvalid: () => {
      const first = Object.keys(localErrors)[0]
      if (first) document.querySelector<HTMLElement>(`[data-settings-field="settings-limits-${first.replace(/\./g, '-')}"]`)?.focus()
    },
    discard: () => {
      if (baseline) setForm({ ...baseline, model_overrides: baseline.model_overrides.map((r) => ({ ...r })) })
      setServerErrors({})
    },
  }

  const effectiveSource: ContextWindowSource | null =
    base && base.default_context_window !== undefined && base.default_context_window !== null ? 'operator' : null

  // ── Views ──────────────────────────────────────────────────────────────
  let status: React.ReactNode = null
  if (isLoading || (!form && !isError)) {
    status = (
      <div role="status" data-testid="context-loading" className="flex items-center gap-[var(--space-2)] text-[var(--color-muted)]">
        <Spinner decorative />
        Loading Context and tool results…
      </div>
    )
  } else if (isError || !form) {
    status = (
      <ReadErrorCallout
        title="Context and tool results could not be loaded"
        error={error}
        onRetry={() => void refetch()}
        retrying={isFetching}
        data-testid="context-load-error"
      />
    )
  }

  const fieldsView = status ?? (
    <Card className="px-[var(--space-3)]" data-testid="context-section">
      {NUMERIC_FIELDS.map((f) => {
        const domId = `settings-limits-${f.key}`
        return (
          <FormRow key={f.key} label={f.label} description={f.description} error={errors[f.key]} fieldId={domId}>
            {({ id, describedBy, invalid, fieldId }) => (
              <div className="flex items-center gap-[var(--space-2)]">
                <Input
                  id={id}
                  data-settings-field={fieldId}
                  data-testid={`context-${f.key.replace(/_/g, '-')}`}
                  type="text"
                  inputMode={f.key === 'tool_result_share_fraction' ? 'decimal' : 'numeric'}
                  value={form![f.key]}
                  onChange={(e) => setNumeric(f.key, e.target.value)}
                  aria-describedby={describedBy}
                  aria-invalid={invalid || undefined}
                  className="max-w-[10rem]"
                />
                <span className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">{f.unit}</span>
              </div>
            )}
          </FormRow>
        )
      })}
      <FormRow
        label="Default context window"
        description="Empty means not set: each model uses its catalog, live or minimum value."
        error={errors.default_context_window}
        fieldId="settings-limits-default_context_window"
      >
        {({ id, describedBy, invalid, fieldId }) => (
          <div className="flex items-center gap-[var(--space-2)]">
            <Input
              id={id}
              data-settings-field={fieldId}
              data-testid="context-default-window"
              type="text"
              inputMode="numeric"
              placeholder="Not set"
              value={form!.default_context_window}
              onChange={(e) => setNumeric('default_context_window', e.target.value)}
              aria-describedby={describedBy}
              aria-invalid={invalid || undefined}
              className="max-w-[10rem]"
            />
            <span className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">tokens</span>
            <span data-testid="context-default-window-source" className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
              Source: {effectiveSource ? CONTEXT_WINDOW_SOURCE_LABEL[effectiveSource] : 'not set (catalog, live or floor per model)'}
            </span>
          </div>
        )}
      </FormRow>
    </Card>
  )

  const overridesView = status ?? (
    <Card className="p-[var(--space-3)] space-y-[var(--space-2)]" id="model-overrides">
      <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
        The context length for one provider and model, for endpoints that do not report one or to keep a model below its advertised limit. An override can only lower the window.
      </p>
      {form!.model_overrides.length === 0 ? (
        <p data-testid="context-overrides-empty" className="text-[length:var(--type-body-compact-size)] text-[var(--color-muted)]">
          No overrides. Every model uses its catalog or live context length.
        </p>
      ) : (
        <ul className="space-y-[var(--space-2)]">
          {form!.model_overrides.map((row, i) => {
            const keys = (['provider', 'model', 'context_window'] as const).map((k) => `model_overrides.${i}.${k}`)
            const rowErrors = keys.map((k) => errors[k]).filter(Boolean)
            return (
              <li key={i} data-testid="context-override-row" className="space-y-[var(--space-1)]">
                <div className="flex flex-wrap items-center gap-[var(--space-2)]">
                  <Input
                    aria-label={`Override ${i + 1} provider`}
                    data-settings-field={`settings-limits-model_overrides-${i}-provider`}
                    data-testid="context-override-provider"
                    placeholder="Provider"
                    value={row.provider}
                    aria-invalid={errors[keys[0]] ? true : undefined}
                    onChange={(e) => setRow(i, { provider: e.target.value })}
                    className="max-w-[10rem]"
                  />
                  <Input
                    aria-label={`Override ${i + 1} model`}
                    data-settings-field={`settings-limits-model_overrides-${i}-model`}
                    data-testid="context-override-model"
                    placeholder="Model"
                    value={row.model}
                    aria-invalid={errors[keys[1]] ? true : undefined}
                    onChange={(e) => setRow(i, { model: e.target.value })}
                    className="max-w-[14rem]"
                  />
                  <Input
                    aria-label={`Override ${i + 1} context length, tokens`}
                    data-settings-field={`settings-limits-model_overrides-${i}-context_window`}
                    data-testid="context-override-window"
                    type="text"
                    inputMode="numeric"
                    placeholder="Tokens"
                    value={row.context_window}
                    aria-invalid={errors[keys[2]] ? true : undefined}
                    onChange={(e) => setRow(i, { context_window: e.target.value })}
                    className="max-w-[8rem]"
                  />
                  <Button type="button" variant="ghost" size="sm" aria-label={`Remove override ${i + 1}`} data-testid="context-override-remove" onClick={() => removeRow(i)}>
                    <X size={14} aria-hidden="true" />
                  </Button>
                </div>
                {rowErrors.map((m) => (
                  <p key={m} role="alert" className="text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">
                    {m}
                  </p>
                ))}
              </li>
            )
          })}
        </ul>
      )}
      {errors.model_overrides && (
        <p role="alert" className="text-[length:var(--type-body-compact-size)] text-[var(--color-error)]">
          {errors.model_overrides}
        </p>
      )}
      <Button type="button" variant="outline" size="sm" data-testid="context-override-add" onClick={addRow}>
        <Plus size={14} aria-hidden="true" /> Add override
      </Button>
    </Card>
  )

  const firstInvalid = Object.keys(localErrors)[0]
  const invalidGroup: 'context' | 'overrides' | null = !firstInvalid ? null : firstInvalid.startsWith('model_overrides') ? 'overrides' : 'context'

  return { part, fieldsView, overridesView, invalidGroup, ready: !!form && !isError, loadFailed: isError, overrideCount: form?.model_overrides.length ?? 0 }
}

/** Context and tool results plus Model overrides on their own, with their own Save bar. */
export function ContextSection({ prefillOverride }: ContextSectionProps): React.ReactElement {
  const form = useContextForm({ prefillOverride })
  const { bar, message } = useFormSaveBar([form.part])
  return (
    <div className="space-y-[var(--space-3)]">
      {form.fieldsView}
      {form.ready && form.overridesView}
      <SaveOutcomeLine message={message('context')} />
      <SaveBarPanel {...bar} />
    </div>
  )
}
