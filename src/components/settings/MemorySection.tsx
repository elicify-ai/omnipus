import { useState, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Brain, ArrowUp, ArrowDown, X, Warning } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { ModelSelector } from '@/components/ui/model-selector'
import { fetchMemorySettings, updateMemorySettings, fetchProviders, getErrorMessage } from '@/lib/api'
import type { MemorySettings, FallbackModel } from '@/lib/api'
import { useModelToProvider } from '@/lib/agents/modelToProvider'
import { useUiStore } from '@/store/ui'
import { SaveStatus, useSaveStatus } from './SaveStatus'

// ── Skeleton ──────────────────────────────────────────────────────────────────

function Skeleton() {
  return (
    <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-3 animate-pulse">
      <div className="h-4 w-40 rounded bg-[var(--color-border)]" />
      <div className="h-3 w-full rounded bg-[var(--color-border)]" />
      <div className="h-3 w-2/3 rounded bg-[var(--color-border)]" />
      <div className="h-3 w-3/4 rounded bg-[var(--color-border)]" />
    </div>
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
    <div className="flex items-start justify-between gap-4">
      <div className="flex-1 min-w-0">
        <label htmlFor={id} className="text-sm font-medium text-[var(--color-secondary)] cursor-pointer">
          {label}
        </label>
        {description && (
          <p className="text-xs text-[var(--color-muted)] mt-0.5 leading-relaxed">{description}</p>
        )}
      </div>
      <button
        id={id}
        type="button"
        role="switch"
        aria-checked={checked}
        onClick={() => onChange(!checked)}
        className={[
          'relative inline-flex h-5 w-9 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)] focus:ring-offset-2 focus:ring-offset-[var(--color-primary)]',
          checked ? 'bg-[var(--color-accent)]' : 'bg-[var(--color-border)]',
        ].join(' ')}
        aria-label={label}
      >
        <span
          className={[
            'pointer-events-none inline-block h-4 w-4 rounded-full bg-white shadow transform transition-transform',
            checked ? 'translate-x-4' : 'translate-x-0',
          ].join(' ')}
          aria-hidden="true"
        />
      </button>
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
    <div className="space-y-1">
      <label htmlFor={id} className="text-sm font-medium text-[var(--color-secondary)]">
        {label}
      </label>
      {description && (
        <p className="text-xs text-[var(--color-muted)] leading-relaxed">{description}</p>
      )}
      <div className="flex items-center gap-2">
        <input
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
          className="w-28 rounded border border-[var(--color-border)] bg-[var(--color-surface-2)] px-2 py-1 text-sm text-[var(--color-secondary)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]"
        />
        {unit && <span className="text-xs text-[var(--color-muted)]">{unit}</span>}
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
  recap_fallback_models: FallbackModel[]
  session_days: number
  memory_retros_days: number
}

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
}

// ── Component ─────────────────────────────────────────────────────────────────

export function MemorySection(): React.ReactElement {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['memory-settings'],
    queryFn: fetchMemorySettings,
  })

  const { data: providers = [], isError: providersError } = useQuery({
    queryKey: ['providers'],
    queryFn: fetchProviders,
  })

  const connectedProviders = providers.filter((p) => p.status === 'connected')
  const availableModels = connectedProviders.flatMap((p) => p.models ?? [])
  const providerGroups = connectedProviders
    .filter((p) => (p.models ?? []).length > 0)
    .map((p) => ({ providerName: p.display_name ?? p.name ?? p.id, providerId: p.id, models: p.models ?? [] }))

  const { lookup: modelToProvider } = useModelToProvider(connectedProviders)

  const [form, setForm] = useState<FormState>(DEFAULT_SETTINGS)

  const { state: saveState, setState: setSaveState, errorMessage, setErrorMessage } = useSaveStatus()

  useEffect(() => {
    if (!data) return
    setForm({
      auto_recap_enabled: data.auto_recap_enabled ?? DEFAULT_SETTINGS.auto_recap_enabled,
      idle_timeout_minutes: data.idle_timeout_minutes ?? DEFAULT_SETTINGS.idle_timeout_minutes,
      bootstrap_recap_enabled: data.bootstrap_recap_enabled ?? DEFAULT_SETTINGS.bootstrap_recap_enabled,
      bootstrap_recap_max_per_minute: data.bootstrap_recap_max_per_minute ?? DEFAULT_SETTINGS.bootstrap_recap_max_per_minute,
      recap_model: data.recap_model ?? DEFAULT_SETTINGS.recap_model,
      recap_fallback_models: data.recap_fallback_models ?? DEFAULT_SETTINGS.recap_fallback_models,
      session_days: data.session_days ?? DEFAULT_SETTINGS.session_days,
      memory_retros_days: data.memory_retros_days ?? DEFAULT_SETTINGS.memory_retros_days,
    })
  }, [data])

  const { mutate: save, isPending: isSaving } = useMutation({
    mutationFn: (body: MemorySettings) => updateMemorySettings(body),
    onMutate: () => setSaveState('saving'),
    onSuccess: (resp) => {
      setSaveState('saved')
      queryClient.setQueryData(['memory-settings'], resp)
    },
    onError: (err: Error) => {
      setSaveState('error')
      const msg = getErrorMessage(err, 'Save failed')
      setErrorMessage(msg)
      addToast({ message: msg, variant: 'error' })
    },
  })

  function handleSave() {
    const payload: MemorySettings = {
      auto_recap_enabled: form.auto_recap_enabled,
      idle_timeout_minutes: form.idle_timeout_minutes,
      bootstrap_recap_enabled: form.bootstrap_recap_enabled,
      bootstrap_recap_max_per_minute: form.bootstrap_recap_max_per_minute,
      session_days: form.session_days,
      memory_retros_days: form.memory_retros_days,
      // Only include recap_model when non-empty (server-side "" = "use default")
      recap_model: form.recap_model !== '' ? form.recap_model : undefined,
      // Only include fallbacks when non-empty
      recap_fallback_models: form.recap_fallback_models.length > 0 ? form.recap_fallback_models : undefined,
    }
    save(payload)
  }

  function setField<K extends keyof FormState>(key: K, value: FormState[K]) {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  // Fallback chain helpers — mirrors AgentProfile's pattern
  function addFallbackFromSelector(modelSlug: string) {
    const trimmed = modelSlug.trim()
    if (!trimmed) return
    const knownProvider = modelToProvider(trimmed)
    if (!knownProvider) {
      addToast({
        message: `"${trimmed}" isn't listed by any connected provider — saving anyway, but the fallback may not work.`,
        variant: 'warning',
      })
    }
    setField('recap_fallback_models', (() => {
      if (form.recap_fallback_models.some((f) => f.model === trimmed)) return form.recap_fallback_models
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

  if (isLoading) return <Skeleton />

  if (isError) {
    return (
      <p className="text-sm" style={{ color: 'var(--color-error)' }}>
        Failed to load memory settings:{' '}
        {error instanceof Error ? error.message : 'Unknown error'}
      </p>
    )
  }

  return (
    <section className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-[var(--color-secondary)] flex items-center gap-1.5">
          <Brain size={14} className="text-[var(--color-muted)]" />
          Memory &amp; Recap
        </h3>
        <SaveStatus state={saveState} errorMessage={errorMessage} />
      </div>

      <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-5">
        <p className="text-xs text-[var(--color-muted)] leading-relaxed">
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
            description="Minutes of inactivity before a recap is triggered."
            value={form.idle_timeout_minutes}
            min={1}
            unit="minutes"
            onChange={(v) => setField('idle_timeout_minutes', Math.max(1, Math.round(v)))}
          />
        )}

        <div className="border-t border-[var(--color-border)] pt-4 space-y-4">
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
        <div className="border-t border-[var(--color-border)] pt-4 space-y-4">
          <div className="space-y-1">
            <p className="text-sm font-medium text-[var(--color-secondary)]">Summarization model</p>
            <p className="text-xs text-[var(--color-muted)] leading-relaxed">
              Recap runs a background summarization call — a fast, cheap model is recommended.
              Leave empty to use the default model.
            </p>
          </div>

          {providersError && (
            <p className="text-xs text-[var(--color-warning)]">
              Could not load providers. You can still enter a model slug manually.
            </p>
          )}

          {/* Primary model */}
          <div className="space-y-1">
            <p className="text-xs text-[var(--color-muted)]">Primary model</p>
            <ModelSelector
              models={availableModels}
              value={form.recap_model}
              onChange={(v) => setField('recap_model', v)}
              onPairChange={({ model }) => setField('recap_model', model)}
              placeholder="Default model"
              providerGroups={providerGroups}
              triggerTestId="recap-model-trigger"
              allowFreeTextWhenEmpty={!!providersError}
              emptyCatalogHint="Connect a provider in Settings to pick a model"
            />
          </div>

          {/* Fallback chain */}
          <div className="space-y-2">
            <p className="text-xs text-[var(--color-muted)]">Fallback models</p>
            <p className="text-xs text-[var(--color-muted)] leading-relaxed">
              Tried in order if the primary model fails.
            </p>

            {form.recap_fallback_models.length > 0 && (
              <div className="space-y-1" data-testid="recap-fallback-list">
                {form.recap_fallback_models.map((entry, idx) => {
                  const providerMissing = !entry.provider
                  const providerLabel = providerMissing
                    ? '—'
                    : (connectedProviders.find((p) => p.id === entry.provider)?.display_name
                        ?? connectedProviders.find((p) => p.id === entry.provider)?.name
                        ?? entry.provider)

                  return (
                    <div
                      key={entry.model}
                      data-testid={`recap-fallback-row-${entry.model}`}
                      className="flex items-center gap-2 rounded border border-[var(--color-border)] bg-[var(--color-surface-2)] px-2 py-1.5"
                    >
                      <span className="text-xs text-[var(--color-muted)] w-4 shrink-0 text-right">{idx + 1}.</span>
                      <span
                        data-testid={`recap-fallback-provider-${entry.model}`}
                        className="inline-flex items-center px-1.5 rounded text-[10px] font-semibold shrink-0"
                        style={{
                          backgroundColor: 'color-mix(in srgb, var(--color-accent) 15%, transparent)',
                          color: 'var(--color-accent)',
                          border: '1px solid color-mix(in srgb, var(--color-accent) 30%, transparent)',
                        }}
                      >
                        {providerLabel}
                      </span>
                      <span
                        data-testid={`recap-fallback-model-${entry.model}`}
                        className="flex-1 text-xs font-mono text-[var(--color-secondary)] truncate"
                      >
                        {entry.model}
                      </span>
                      {providerMissing && (
                        <span
                          data-testid={`recap-fallback-warning-${entry.model}`}
                          role="img"
                          aria-label="Provider not connected — fallback may not work at runtime"
                          title="Provider not connected — fallback may not work at runtime"
                          className="inline-flex items-center text-amber-400 shrink-0"
                        >
                          <Warning size={12} weight="fill" />
                        </span>
                      )}
                      <button
                        type="button"
                        data-testid={`recap-fallback-up-${entry.model}`}
                        aria-label={`Move fallback ${entry.model} up`}
                        disabled={idx === 0}
                        onClick={() => moveFallback(entry.model, -1)}
                        className="text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors disabled:opacity-30 disabled:cursor-not-allowed focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)] rounded"
                      >
                        <ArrowUp size={12} />
                      </button>
                      <button
                        type="button"
                        data-testid={`recap-fallback-down-${entry.model}`}
                        aria-label={`Move fallback ${entry.model} down`}
                        disabled={idx === form.recap_fallback_models.length - 1}
                        onClick={() => moveFallback(entry.model, 1)}
                        className="text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors disabled:opacity-30 disabled:cursor-not-allowed focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)] rounded"
                      >
                        <ArrowDown size={12} />
                      </button>
                      <button
                        type="button"
                        data-testid={`recap-fallback-remove-${entry.model}`}
                        aria-label={`Remove fallback ${entry.model}`}
                        onClick={() => removeFallback(entry.model)}
                        className="text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)] rounded"
                      >
                        <X size={12} />
                      </button>
                    </div>
                  )
                })}
              </div>
            )}

            <div data-testid="recap-fallback-add">
              <ModelSelector
                models={availableModels}
                value=""
                onChange={(v) => addFallbackFromSelector(v)}
                placeholder="Add fallback model…"
                providerGroups={providerGroups}
                triggerTestId="recap-fallback-add-trigger"
                constrainToCatalog
                allowFreeTextWhenEmpty={!!providersError}
                emptyCatalogHint="Connect a provider to add fallbacks"
              />
            </div>
          </div>
        </div>

        <div className="border-t border-[var(--color-border)] pt-4 space-y-4">
          <p className="text-xs font-semibold text-[var(--color-secondary)]">Retention</p>

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

        <div className="border-t border-[var(--color-border)] pt-4 flex justify-end">
          <Button
            size="sm"
            onClick={handleSave}
            disabled={isSaving}
            aria-label="Save memory settings"
          >
            {isSaving ? 'Saving...' : 'Save'}
          </Button>
        </div>
      </div>
    </section>
  )
}
