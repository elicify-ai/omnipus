import { useState, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Brain, Plus, Trash } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { fetchMemorySettings, updateMemorySettings, isApiError } from '@/lib/api'
import type { MemorySettings } from '@/lib/api'
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

// ── Model allow-list editor ───────────────────────────────────────────────────

interface ModelAllowListProps {
  value: string[]
  onChange: (val: string[]) => void
}

function ModelAllowListEditor({ value, onChange }: ModelAllowListProps) {
  const [newEntry, setNewEntry] = useState('')
  const [addError, setAddError] = useState<string | null>(null)

  function handleAdd() {
    const trimmed = newEntry.trim()
    if (!trimmed) {
      setAddError('Model slug cannot be empty.')
      return
    }
    if (value.includes(trimmed)) {
      setAddError('Already in the list.')
      return
    }
    setAddError(null)
    onChange([...value, trimmed])
    setNewEntry('')
  }

  function handleDelete(slug: string) {
    onChange(value.filter((s) => s !== slug))
  }

  return (
    <div className="space-y-2">
      <p className="text-sm font-medium text-[var(--color-secondary)]">Recap model allow-list</p>
      <p className="text-xs text-[var(--color-muted)] leading-relaxed">
        Model slugs permitted for recap/compaction LLM calls. Leave empty to allow any configured model.
      </p>

      {value.length === 0 ? (
        <p className="text-xs text-[var(--color-muted)] italic">Empty — any configured model is allowed.</p>
      ) : (
        <div className="space-y-1">
          {value.map((slug) => (
            <div
              key={slug}
              className="flex items-center gap-2 rounded border border-[var(--color-border)] bg-[var(--color-surface-2)] px-2 py-1.5"
            >
              <span className="flex-1 text-xs font-mono text-[var(--color-secondary)] break-all">{slug}</span>
              <button
                type="button"
                aria-label={`Remove model ${slug}`}
                onClick={() => handleDelete(slug)}
                className="text-[var(--color-muted)] hover:text-[var(--color-error)] transition-colors focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)] rounded"
              >
                <Trash size={12} />
              </button>
            </div>
          ))}
        </div>
      )}

      <div className="space-y-1">
        <div className="flex items-center gap-2">
          <input
            type="text"
            value={newEntry}
            onChange={(e) => { setNewEntry(e.target.value); setAddError(null) }}
            placeholder="google/gemini-2.5-flash"
            aria-label="New model slug"
            className="flex-1 h-7 rounded border border-[var(--color-border)] bg-[var(--color-surface-2)] px-2 text-xs font-mono text-[var(--color-secondary)] focus:outline-none focus:ring-1 focus:ring-[var(--color-accent)]"
            onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); handleAdd() } }}
          />
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="h-7 px-2 gap-1 text-xs shrink-0"
            onClick={handleAdd}
            aria-label="Add model"
          >
            <Plus size={11} />
            Add
          </Button>
        </div>
        {addError && <p className="text-[10px] text-[var(--color-error)]">{addError}</p>}
      </div>
    </div>
  )
}

// ── Defaults ──────────────────────────────────────────────────────────────────

const DEFAULT_SETTINGS: Required<MemorySettings> = {
  auto_recap_enabled: false,
  idle_timeout_minutes: 30,
  bootstrap_recap_enabled: false,
  bootstrap_recap_max_per_minute: 5,
  bootstrap_recap_daily_budget_usd: 0.5,
  recap_model_allow_list: [],
  session_days: 90,
  memory_retros_days: 365,
}

// ── Component ─────────────────────────────────────────────────────────────────

export function MemorySection(): React.ReactElement {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()

  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['memory-settings'],
    queryFn: fetchMemorySettings,
  })

  const [form, setForm] = useState<Required<MemorySettings>>(DEFAULT_SETTINGS)

  const { state: saveState, setState: setSaveState, errorMessage, setErrorMessage } = useSaveStatus()

  useEffect(() => {
    if (!data) return
    setForm({
      auto_recap_enabled: data.auto_recap_enabled ?? DEFAULT_SETTINGS.auto_recap_enabled,
      idle_timeout_minutes: data.idle_timeout_minutes ?? DEFAULT_SETTINGS.idle_timeout_minutes,
      bootstrap_recap_enabled: data.bootstrap_recap_enabled ?? DEFAULT_SETTINGS.bootstrap_recap_enabled,
      bootstrap_recap_max_per_minute: data.bootstrap_recap_max_per_minute ?? DEFAULT_SETTINGS.bootstrap_recap_max_per_minute,
      bootstrap_recap_daily_budget_usd: data.bootstrap_recap_daily_budget_usd ?? DEFAULT_SETTINGS.bootstrap_recap_daily_budget_usd,
      recap_model_allow_list: data.recap_model_allow_list ?? DEFAULT_SETTINGS.recap_model_allow_list,
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
      const msg = isApiError(err) ? err.userMessage : err.message
      setErrorMessage(msg)
      addToast({ message: msg, variant: 'error' })
    },
  })

  function handleSave() {
    save(form)
  }

  function setField<K extends keyof Required<MemorySettings>>(
    key: K,
    value: Required<MemorySettings>[K],
  ) {
    setForm((prev) => ({ ...prev, [key]: value }))
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
            <>
              <NumberRow
                id="bootstrap-recap-max-per-minute"
                label="Bootstrap recap rate limit"
                description="Maximum number of bootstrap recap operations per minute."
                value={form.bootstrap_recap_max_per_minute}
                min={1}
                unit="per minute"
                onChange={(v) => setField('bootstrap_recap_max_per_minute', Math.max(1, Math.round(v)))}
              />

              <NumberRow
                id="bootstrap-recap-daily-budget"
                label="Bootstrap recap daily budget"
                description="Maximum USD spend allowed for bootstrap recaps per calendar day."
                value={form.bootstrap_recap_daily_budget_usd}
                min={0}
                step={0.01}
                unit="USD / day"
                onChange={(v) => setField('bootstrap_recap_daily_budget_usd', Math.max(0, v))}
              />
            </>
          )}
        </div>

        <div className="border-t border-[var(--color-border)] pt-4">
          <ModelAllowListEditor
            value={form.recap_model_allow_list}
            onChange={(v) => setField('recap_model_allow_list', v)}
          />
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
