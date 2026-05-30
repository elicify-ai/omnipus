import { useState, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Gauge } from '@phosphor-icons/react'
import { fetchRateLimits, updateRateLimits, isApiError } from '@/lib/api'
import type { RateLimitsUpdateRequest } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { useAuthStore } from '@/store/auth'
import { SaveStatus, useSaveStatus } from './SaveStatus'

// ── Helpers ───────────────────────────────────────────────────────────────────

function isNonNegativeDecimal(val: string): boolean {
  if (val === '') return true
  const n = Number(val)
  return !isNaN(n) && isFinite(n) && n >= 0
}

function isNonNegativeInteger(val: string): boolean {
  if (val === '') return true
  const n = Number(val)
  return !isNaN(n) && isFinite(n) && n >= 0 && Number.isInteger(n)
}

// ── Field component ───────────────────────────────────────────────────────────

interface FieldProps {
  label: string
  hint: string
  value: string
  error: string
  disabled: boolean
  onChange: (v: string) => void
  onBlur: (v: string) => void
}

function Field({ label, hint, value, error, disabled, onChange, onBlur }: FieldProps) {
  return (
    <div className="space-y-1">
      <label className="text-xs font-medium text-[var(--color-secondary)]">{label}</label>
      <input
        type="number"
        min="0"
        step="any"
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
        onBlur={(e) => onBlur(e.target.value)}
        className={[
          'w-full rounded-md border px-3 py-1.5 text-sm bg-[var(--color-surface-2)] text-[var(--color-secondary)] outline-none transition-colors disabled:opacity-60 disabled:cursor-not-allowed',
          error
            ? 'border-[var(--color-error)]'
            : 'border-[var(--color-border)] focus:border-[var(--color-accent)]/60',
        ].join(' ')}
        placeholder="0 = unlimited"
      />
      {error ? (
        <p className="text-[11px]" style={{ color: 'var(--color-error)' }}>{error}</p>
      ) : (
        <p className="text-[11px] text-[var(--color-muted)]">{hint}</p>
      )}
    </div>
  )
}

// ── Skeleton ──────────────────────────────────────────────────────────────────

function Skeleton() {
  return (
    <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-4 animate-pulse">
      {[0, 1, 2].map((i) => (
        <div key={i} className="space-y-1.5">
          <div className="h-3 w-32 rounded bg-[var(--color-border)]" />
          <div className="h-8 rounded bg-[var(--color-border)]" />
        </div>
      ))}
    </div>
  )
}

// ── Component ─────────────────────────────────────────────────────────────────

export function RateLimitsSection(): React.ReactElement {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const role = useAuthStore((s) => s.role)
  const isAdmin = role === 'admin'

  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['rate-limits-k'],
    queryFn: fetchRateLimits,
  })

  // String state for controlled inputs — allows intermediate values during typing
  const [costCap, setCostCap] = useState('')
  const [llmPerHour, setLlmPerHour] = useState('')
  const [toolPerMin, setToolPerMin] = useState('')

  // Inline validation errors
  const [costCapErr, setCostCapErr] = useState('')
  const [llmPerHourErr, setLlmPerHourErr] = useState('')
  const [toolPerMinErr, setToolPerMinErr] = useState('')

  // Track server values to build partial update body
  const [serverCostCap, setServerCostCap] = useState<number | undefined>()
  const [serverLlmPerHour, setServerLlmPerHour] = useState<number | undefined>()
  const [serverToolPerMin, setServerToolPerMin] = useState<number | undefined>()

  const { state: saveState, setState: setSaveState, errorMessage, setErrorMessage } = useSaveStatus()

  useEffect(() => {
    if (!data) return
    // GET /security/rate-limits returns daily_cost_cap (no _usd suffix).
    // The cap fields are 0 when unlimited rather than undefined.
    const cc = data.daily_cost_cap
    const lph = data.max_agent_llm_calls_per_hour
    const tpm = data.max_agent_tool_calls_per_minute
    setCostCap(cc !== undefined ? String(cc) : '')
    setLlmPerHour(lph !== undefined ? String(lph) : '')
    setToolPerMin(tpm !== undefined ? String(tpm) : '')
    setServerCostCap(cc)
    setServerLlmPerHour(lph)
    setServerToolPerMin(tpm)
  }, [data])

  function validateCostCap(val: string): boolean {
    if (!isNonNegativeDecimal(val)) {
      setCostCapErr('Must be a non-negative number (e.g. 25.50). 0 = unlimited.')
      return false
    }
    setCostCapErr('')
    return true
  }

  function validateLlmPerHour(val: string): boolean {
    if (!isNonNegativeInteger(val)) {
      setLlmPerHourErr('Must be a non-negative integer. 0 = unlimited.')
      return false
    }
    setLlmPerHourErr('')
    return true
  }

  function validateToolPerMin(val: string): boolean {
    if (!isNonNegativeInteger(val)) {
      setToolPerMinErr('Must be a non-negative integer. 0 = unlimited.')
      return false
    }
    setToolPerMinErr('')
    return true
  }

  // Build partial update body — only include changed fields
  function buildUpdateBody(cc: string, lph: string, tpm: string): RateLimitsUpdateRequest {
    const body: RateLimitsUpdateRequest = {}
    if (cc !== '' && Number(cc) !== serverCostCap) {
      body.daily_cost_cap_usd = Number(cc)
    } else if (cc !== '' && serverCostCap === undefined) {
      body.daily_cost_cap_usd = Number(cc)
    }
    if (lph !== '' && Number(lph) !== serverLlmPerHour) {
      body.max_agent_llm_calls_per_hour = Number(lph)
    } else if (lph !== '' && serverLlmPerHour === undefined) {
      body.max_agent_llm_calls_per_hour = Number(lph)
    }
    if (tpm !== '' && Number(tpm) !== serverToolPerMin) {
      body.max_agent_tool_calls_per_minute = Number(tpm)
    } else if (tpm !== '' && serverToolPerMin === undefined) {
      body.max_agent_tool_calls_per_minute = Number(tpm)
    }
    return body
  }

  const { mutate: save } = useMutation({
    mutationFn: (body: RateLimitsUpdateRequest) => updateRateLimits(body),
    onMutate: () => setSaveState('saving'),
    onSuccess: (_resp) => {
      setSaveState('saved')
      // Invalidate the GET query so the form reloads the applied values.
      void queryClient.invalidateQueries({ queryKey: ['rate-limits-k'] })
    },
    onError: (err: unknown) => {
      const msg = isApiError(err) ? err.userMessage : err instanceof Error ? err.message : 'Save failed'
      setSaveState('error')
      setErrorMessage(msg)
      addToast({ message: msg, variant: 'error' })
    },
  })

  function handleCostCapBlur(val: string) {
    if (!validateCostCap(val)) return
    const body = buildUpdateBody(val, llmPerHour, toolPerMin)
    if (Object.keys(body).length > 0) save(body)
  }

  function handleLlmPerHourBlur(val: string) {
    if (!validateLlmPerHour(val)) return
    const body = buildUpdateBody(costCap, val, toolPerMin)
    if (Object.keys(body).length > 0) save(body)
  }

  function handleToolPerMinBlur(val: string) {
    if (!validateToolPerMin(val)) return
    const body = buildUpdateBody(costCap, llmPerHour, val)
    if (Object.keys(body).length > 0) save(body)
  }

  if (isLoading) return <Skeleton />

  if (isError) {
    return (
      <p className="text-sm" style={{ color: 'var(--color-error)' }}>
        Failed to load rate limits: {error instanceof Error ? error.message : 'Unknown error'}
      </p>
    )
  }

  return (
    <section className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-[var(--color-secondary)] flex items-center gap-1.5">
          <Gauge size={14} className="text-[var(--color-muted)]" />
          Rate Limits
        </h3>
        <SaveStatus state={saveState} errorMessage={errorMessage} />
      </div>

      <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-4">
        <p className="text-xs text-[var(--color-muted)] leading-relaxed">
          Set spending and throughput caps. Use 0 to mean unlimited. Changes apply when you leave each field.
        </p>

        <Field
          label="Daily cost cap (USD)"
          hint="Maximum spend per day across all LLM calls. 0 = unlimited."
          value={costCap}
          error={costCapErr}
          disabled={!isAdmin}
          onChange={(v) => { setCostCap(v); setCostCapErr('') }}
          onBlur={handleCostCapBlur}
        />

        <Field
          label="Max LLM calls per hour (per agent)"
          hint="Maximum LLM invocations per agent per hour. 0 = unlimited."
          value={llmPerHour}
          error={llmPerHourErr}
          disabled={!isAdmin}
          onChange={(v) => { setLlmPerHour(v); setLlmPerHourErr('') }}
          onBlur={handleLlmPerHourBlur}
        />

        <Field
          label="Max tool calls per minute (per agent)"
          hint="Maximum tool invocations per agent per minute. 0 = unlimited."
          value={toolPerMin}
          error={toolPerMinErr}
          disabled={!isAdmin}
          onChange={(v) => { setToolPerMin(v); setToolPerMinErr('') }}
          onBlur={handleToolPerMinBlur}
        />
      </div>
    </section>
  )
}
