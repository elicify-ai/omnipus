import { useState, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Wallet, ArrowsClockwise, Info, Power, Gauge } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Progress } from '@/components/ui/progress'
import { Badge } from '@/components/ui/badge'
import {
  fetchTokenBudgetStatus,
  updateTokenBudget,
  tokenBudgetQueryKeys,
  getErrorMessage,
  type TokenBudgetStatus,
} from '@/lib/api'
import { formatTokens } from '@/lib/formatTokens'
import { useUiStore } from '@/store/ui'
import { SaveStatus, useSaveStatus } from '@/components/settings/SaveStatus'

// ADR-053 D12 / R§8.3 / FE-6 — app-level OVERALL token budget for the Usage
// screen. ONE shared pool across all workloads (owner/member/verifier/Judge);
// no per-plan cap, no money/USD cap, no IsPrivilegedAgent exemption. The
// ceiling is restart-gated (R§8.3e); the live lever for runaway spend is the
// existing Stop/cancel cascade, not a live token cut. cap = 0 is the unbounded
// sentinel (R§8.3a) — shows a persistent "unbounded — set a budget" advisory.

// ── Skeleton ──────────────────────────────────────────────────────────────────

function Skeleton() {
  return (
    <div
      className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-3 animate-pulse"
      data-testid="token-budget-skeleton"
      aria-busy="true"
      aria-label="Loading token budget"
    >
      <div className="h-4 w-40 rounded bg-[var(--color-border)]" />
      <div className="h-3 w-full rounded bg-[var(--color-border)]" />
      <div className="h-3 w-2/3 rounded bg-[var(--color-border)]" />
    </div>
  )
}

// ── Scope breakdown ───────────────────────────────────────────────────────────

interface ScopeRow {
  key: keyof TokenBudgetStatus['by_scope']
  label: string
  tokens: number
}

const SCOPE_LABELS: Record<keyof TokenBudgetStatus['by_scope'], string> = {
  owner: 'Owner loop',
  member: 'Members',
  verifier: 'Verifiers',
  judge: 'Judge',
}

function ScopeBreakdown({ byScope, maxScope }: {
  byScope: TokenBudgetStatus['by_scope']
  maxScope: number
}) {
  const rows: ScopeRow[] = (Object.keys(SCOPE_LABELS) as Array<keyof TokenBudgetStatus['by_scope']>)
    .map((key) => ({ key, label: SCOPE_LABELS[key], tokens: byScope[key] ?? 0 }))
    .filter((r) => r.tokens > 0)
    .sort((a, b) => b.tokens - a.tokens)

  if (rows.length === 0) {
    return (
      <p className="text-xs text-[var(--color-muted)] py-2" data-testid="token-budget-scopes-empty">
        No per-workload spend recorded yet.
      </p>
    )
  }

  return (
    <div className="space-y-2" role="list" data-testid="token-budget-scopes">
      {rows.map((row) => {
        const pct = maxScope > 0 ? Math.round((row.tokens / maxScope) * 100) : 0
        return (
          <div key={row.key} className="flex items-center gap-3" role="listitem">
            <div className="w-24 shrink-0 truncate text-xs text-[var(--color-secondary)]">{row.label}</div>
            <div className="flex-1 min-w-0">
              <Progress
                value={pct}
                aria-label={`${row.label}: ${row.tokens} tokens`}
                className="h-1.5"
              />
            </div>
            <div
              className="w-14 text-right text-xs font-mono tabular-nums text-[var(--color-muted)] shrink-0"
              aria-label={`${row.tokens} tokens`}
            >
              {formatTokens(row.tokens)}
            </div>
          </div>
        )
      })}
    </div>
  )
}

// ── Component ─────────────────────────────────────────────────────────────────

export function TokenBudgetSection(): React.ReactElement | null {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const { state: saveState, setState: setSaveState, errorMessage, setErrorMessage } = useSaveStatus()

  const { data, isLoading, isError, error } = useQuery({
    queryKey: tokenBudgetQueryKeys.status,
    queryFn: fetchTokenBudgetStatus,
    staleTime: 15_000,
  })

  // The operator-set ceiling. `0` is the unbounded sentinel (R§8.3a). The
  // input is a free-form number field; we keep an empty-string UI state so
  // the user can clear it to mean "unbounded" (persisted as 0).
  const [budgetInput, setBudgetInput] = useState<string>('')
  const [loaded, setLoaded] = useState(false)

  useEffect(() => {
    if (!data || loaded) return
    setBudgetInput(data.budget > 0 ? String(data.budget) : '')
    setLoaded(true)
  }, [data, loaded])

  const { mutate: save, isPending: isSaving } = useMutation({
    mutationFn: (budget: number) => updateTokenBudget(budget),
    onMutate: () => setSaveState('saving'),
    onSuccess: (resp) => {
      setSaveState('saved')
      queryClient.setQueryData(tokenBudgetQueryKeys.status, resp)
      setBudgetInput(resp.budget > 0 ? String(resp.budget) : '')
      // R§8.3e: the ceiling is restart-gated. Surface the restart requirement
      // inline so the operator knows the new cap hasn't taken effect live.
      addToast({
        message: 'Token budget saved — restart the gateway to apply the new ceiling.',
        variant: 'warning',
      })
    },
    onError: (err: Error) => {
      setSaveState('error')
      const msg = getErrorMessage(err, 'Save failed')
      setErrorMessage(msg)
      addToast({ message: msg, variant: 'error' })
    },
  })

  function handleSave() {
    const parsed = parseInt(budgetInput, 10)
    // Empty / non-numeric / negative → unbounded sentinel (0). The schema
    // requires an integer >= 0; clamp negatives to 0 rather than rejecting
    // (an operator clearing the field means "no cap").
    const next = isNaN(parsed) || parsed < 0 ? 0 : Math.floor(parsed)
    save(next)
  }

  // Has the operator typed a value that differs from the persisted ceiling?
  // Drives the Save button's enabled state (mirrors MemorySection's dirty-check
  // intent without a full form diff).
  const persistedBudget = data?.budget ?? 0
  const parsedInput = (() => {
    const p = parseInt(budgetInput, 10)
    return isNaN(p) || p < 0 ? 0 : Math.floor(p)
  })()
  const isDirty = parsedInput !== persistedBudget

  if (isLoading) {
    return (
      <section className="space-y-3" data-testid="token-budget-section">
        <Skeleton />
      </section>
    )
  }

  // A failed fetch must NOT block the rest of the Usage screen — degrade
  // gracefully to a small inline note. The spend accounting is display-only;
  // the operator can still use Stop/cancel (the live lever) without it.
  if (isError || !data) {
    return (
      <section className="space-y-3" data-testid="token-budget-section">
        <div
          className="rounded-lg border border-[var(--color-error)]/30 bg-[var(--color-error)]/10 px-4 py-3 text-sm text-[var(--color-error)]"
          role="alert"
        >
          Could not load token budget: {error instanceof Error ? error.message : 'Unknown error'}
        </div>
      </section>
    )
  }

  const isUnbounded = data.budget === 0
  // INV-8: consumed may overshoot budget by up to the sum of in-flight turn
  // costs (post-turn provider-reported debit). Clamp only the visual bar.
  const rawPct = isUnbounded ? 0 : data.budget > 0 ? (data.consumed / data.budget) * 100 : 0
  const barPct = Math.min(100, Math.max(0, Math.round(rawPct)))
  const isOver = !isUnbounded && data.consumed >= data.budget
  const maxScope = Math.max(data.by_scope.owner, data.by_scope.member, data.by_scope.verifier, data.by_scope.judge, 1)

  return (
    <section className="space-y-3" data-testid="token-budget-section">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-[var(--color-secondary)] flex items-center gap-1.5">
          <Wallet size={14} className="text-[var(--color-muted)]" />
          Token budget
        </h3>
        <SaveStatus state={saveState} errorMessage={errorMessage} />
      </div>

      <div className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-1)] p-4 space-y-4">
        <p className="text-xs text-[var(--color-muted)] leading-relaxed">
          One app-level overall token budget covers all workloads — owner loop, members, verifiers, and the Judge.
          No per-plan cap, no money cap. Every turn debits the same shared pool regardless of agent privilege.
        </p>

        {/* Persistent unbounded advisory (R§8.3a) */}
        {isUnbounded && (
          <div
            className="rounded-md border border-[var(--color-accent)]/40 bg-[var(--color-accent)]/10 px-3 py-2 text-xs text-[var(--color-secondary)] flex items-start gap-2"
            data-testid="token-budget-advisory"
            role="status"
          >
            <Info size={14} weight="fill" className="text-[var(--color-accent)] shrink-0 mt-0.5" aria-hidden="true" />
            <span>
              <span className="font-medium">Unbounded — set a budget.</span> No overall token cap is configured, so
              spend runs without a ceiling. Set one below to bound it.
            </span>
          </div>
        )}

        {/* Exhausted banner (INV-8) */}
        {data.exhausted && (
          <div
            className="rounded-md border border-[var(--color-error)]/40 bg-[var(--color-error)]/10 px-3 py-2 text-xs text-[var(--color-error)] flex items-start gap-2"
            data-testid="token-budget-exhausted"
            role="alert"
          >
            <Power size={14} weight="fill" className="shrink-0 mt-0.5" aria-hidden="true" />
            <span>
              <span className="font-medium">Budget exhausted.</span> Running scopes brake to
              <span className="font-mono"> failed(budget_exhausted)</span> at their next boundary. Raise the ceiling
              (restart to apply) or cancel scopes to stop spend.
            </span>
          </div>
        )}

        {/* Budget input + Save */}
        <div className="space-y-1.5">
          <label htmlFor="token-budget-input" className="text-sm font-medium text-[var(--color-secondary)]">
            Overall token ceiling
          </label>
          <p className="text-xs text-[var(--color-muted)] leading-relaxed">
            Leave empty for unbounded. The ceiling counts tokens, not dollars.
          </p>
          <div className="flex items-center gap-2 flex-wrap">
            <Input
              id="token-budget-input"
              type="number"
              min={0}
              step={1000}
              value={budgetInput}
              placeholder="unbounded"
              onChange={(e) => setBudgetInput(e.target.value)}
              data-testid="token-budget-input"
              aria-label="Overall token ceiling in tokens"
              className="w-40 rounded border border-[var(--color-border)] bg-[var(--color-surface-2)] px-2 py-1 text-sm text-[var(--color-secondary)] focus:outline-none"
            />
            <span className="text-xs text-[var(--color-muted)]">tokens</span>
            <Button
              size="sm"
              onClick={handleSave}
              disabled={isSaving || !isDirty}
              aria-label="Save token budget"
              data-testid="token-budget-save"
            >
              {isSaving ? 'Saving...' : 'Save'}
            </Button>
          </div>

          {/* Restart-gated notice (R§8.3e) */}
          <p
            className="text-xs text-[var(--color-muted)] leading-relaxed flex items-start gap-1.5 mt-1"
            data-testid="token-budget-restart-notice"
          >
            <ArrowsClockwise size={12} className="shrink-0 mt-0.5" aria-hidden="true" />
            <span>
              <span className="font-medium">Restart required.</span> A new ceiling takes effect after a gateway
              restart — it never cuts live spend mid-flight. For an immediate cut, use Stop or Cancel on the running
              scope.
            </span>
          </p>
        </div>

        {/* Spend accounting */}
        <div className="border-t border-[var(--color-border)] pt-4 space-y-3" data-testid="token-budget-spend">
          <div className="flex items-center justify-between">
            <p className="text-xs font-semibold text-[var(--color-secondary)] flex items-center gap-1.5">
              <Gauge size={12} className="text-[var(--color-muted)]" aria-hidden="true" />
              Spend accounting
            </p>
            {data.exhausted && (
              <Badge variant="muted" className="text-[10px] uppercase tracking-wider">Exhausted</Badge>
            )}
          </div>

          {isUnbounded ? (
            <div className="space-y-1.5" data-testid="token-budget-spend-unbounded">
              <div className="flex items-baseline justify-between gap-3">
                <span className="text-xs text-[var(--color-muted)]">Consumed</span>
                <span className="font-mono tabular-nums text-sm text-[var(--color-secondary)]">
                  {formatTokens(data.consumed)}
                </span>
              </div>
              <div className="flex items-baseline justify-between gap-3">
                <span className="text-xs text-[var(--color-muted)]">Ceiling</span>
                <span className="text-sm text-[var(--color-accent)]">Unbounded (advisory)</span>
              </div>
            </div>
          ) : (
            <div className="space-y-1.5">
              <div className="flex items-baseline justify-between gap-3" data-testid="token-budget-spend-bound">
                <span className="text-xs text-[var(--color-muted)]">Consumed / ceiling</span>
                <span className={`font-mono tabular-nums text-sm${isOver ? ' text-[var(--color-error)]' : ' text-[var(--color-secondary)]'}`}>
                  {formatTokens(data.consumed)} / {formatTokens(data.budget)}
                  <span className={`ml-1.5 text-xs${isOver ? ' text-[var(--color-error)]' : ' text-[var(--color-muted)]'}`}>
                    ({Math.round(rawPct)}%)
                  </span>
                </span>
              </div>
              <Progress
                value={barPct}
                aria-label={`${data.consumed} of ${data.budget} tokens consumed`}
                className={`h-2${isOver ? ' [&>div]:bg-[var(--color-error)]' : ''}`}
              />
              <div className="flex items-baseline justify-between gap-3">
                <span className="text-xs text-[var(--color-muted)]">Remaining</span>
                <span className="font-mono tabular-nums text-xs text-[var(--color-muted)]">
                  {data.remaining > 0 ? formatTokens(data.remaining) : '0'}
                </span>
              </div>
            </div>
          )}

          {/* Per-workload breakdown (display-only — same shared pool, D12) */}
          <div className="space-y-2 pt-1">
            <p className="text-xs text-[var(--color-muted)]">By workload</p>
            <ScopeBreakdown byScope={data.by_scope} maxScope={maxScope} />
          </div>
        </div>

        {/* token≠dollar operator note (R§8.3b) */}
        <p
          className="border-t border-[var(--color-border)] pt-3 text-xs text-[var(--color-muted)] leading-relaxed flex items-start gap-1.5"
          data-testid="token-budget-dollar-note"
        >
          <Info size={12} className="shrink-0 mt-0.5" aria-hidden="true" />
          <span>
            A token cap does not bound dollar spend uniformly — token price varies across providers and models. Use
            this as a volume guard, not a cost cap.
          </span>
        </p>
      </div>
    </section>
  )
}
