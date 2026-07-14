import { useMutation } from '@tanstack/react-query'
import { CheckCircle, WarningCircle, XCircle, Spinner, Info } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { testAgentRunner } from '@/lib/api'
import type { ExecutorConfig, RunnerTestResponse } from '@/lib/api'
import { getErrorMessage } from '@/lib/api'

// Spec-4 FR-4.1/FR-4.2 — Executor selector + external-CLI runner connection test.
//
// A sub-agent's executor controls which runtime runs its tasks:
//   - native      → run inside the Omnipus agent loop (default)
//   - external-cli → delegate to an external CLI agent (claude-code / codex / opencode);
//                    fully wired below (CLI picker + Test Connection button with 6
//                    distinct outcome states) but experimental in v0.1.0
//   - remote-a2a  → RESERVED; not resolvable in v0.1.0 — dispatch fails the sub-turn with an error
//
// When kind=external-cli the operator can run a connection test that validates the
// CLI binary is present, runs, and is authenticated WITHOUT spending any tokens.

// NonNullable: ExecutorConfig is Partial<…>, so ['kind'] otherwise includes
// undefined. The Radix <Select> value/SelectItem value props require a real
// string; narrowing here mirrors the existing ExecutorCLI treatment below.
type ExecutorKind = NonNullable<ExecutorConfig['kind']>
type ExecutorCLI = NonNullable<ExecutorConfig['cli']>

const KIND_OPTIONS: ReadonlyArray<{ value: ExecutorKind; label: string }> = [
  { value: 'native', label: 'Native (Omnipus agent loop)' },
  { value: 'external-cli', label: 'External CLI' },
  { value: 'remote-a2a', label: 'Remote (A2A)' },
]

const CLI_OPTIONS: ReadonlyArray<{ value: ExecutorCLI; label: string }> = [
  { value: 'claude-code', label: 'Claude Code' },
  { value: 'codex', label: 'Codex' },
  { value: 'opencode', label: 'opencode' },
]

// effectiveKind normalises an absent executor to its default ("native").
function effectiveKind(value: ExecutorConfig | undefined): ExecutorKind {
  return value?.kind ?? 'native'
}

interface ExecutorSelectorProps {
  /** Current executor config (undefined = native default). */
  value: ExecutorConfig | undefined
  /** Fires with the next executor config whenever kind/cli changes. */
  onChange: (next: ExecutorConfig) => void
  /**
   * Agent id — required for the connection test. When absent (e.g. the create
   * modal where the agent does not exist yet) the Test Connection button is
   * hidden, because there is no persisted runner to probe.
   */
  agentId?: string
  /** Read-only mode (locked core agents). */
  disabled?: boolean
  /**
   * WCAG 3.3.1 / 4.1.3 wiring for the validation error rendered below
   * the selector (worker executor required). When `errorId` is supplied
   * the selector's `aria-describedby` points at the `<FormError>` region
   * and `aria-invalid` flips to `true` whenever `hasError` is true.
   */
  errorId?: string
  hasError?: boolean
  /**
   * When true, this selector is rendering for a locked CORE agent (Mia, Jim,
   * Ray, Ava). Core agents run native/in-process only — `external-cli` is a
   * worker-only affordance and the backend rejects it with 400 for core
   * agents (G9: "core agents run native only" gate). When set, the
   * `external-cli` option is rendered with `disabled` + a tooltip explaining
   * the restriction, so the user cannot even reach the wire. The default
   * (false) preserves the existing behaviour for workers + custom base agents.
   */
  isCoreAgent?: boolean
}

export function ExecutorSelector({ value, onChange, agentId, disabled = false, errorId, hasError, isCoreAgent = false }: ExecutorSelectorProps) {
  const kind = effectiveKind(value)
  const cli = value?.cli

  const handleKindChange = (nextKind: ExecutorKind) => {
    // Defence-in-depth: even if a consumer forgets to disable the option, we
    // must never emit an `external-cli` payload for a locked core agent — the
    // backend rejects it with 400 and the user sees a confusing server error.
    // The dropdown is also disabled + tooltip'd, but if the value is mutated
    // programmatically (test harness, future programmatic form reset), this
    // clamp keeps the wire safe.
    if (isCoreAgent && nextKind === 'external-cli') {
      onChange({ kind: 'native' })
      return
    }
    if (nextKind === 'external-cli') {
      // Default the CLI to claude-code when switching into external-cli so the
      // payload is immediately valid and the test button has a target.
      onChange({ kind: 'external-cli', cli: cli ?? 'claude-code' })
    } else {
      // native / remote-a2a carry no cli.
      onChange({ kind: nextKind })
    }
  }

  const handleCliChange = (nextCli: ExecutorCLI) => {
    onChange({ kind: 'external-cli', cli: nextCli })
  }

  return (
    <div className="space-y-3" data-testid="executor-selector">
      <div className="space-y-1.5">
        <label htmlFor="executor-kind" className="text-xs text-[var(--color-muted)]">
          Runtime
        </label>
        <Select
          value={kind}
          onValueChange={(v) => handleKindChange(v as ExecutorKind)}
          disabled={disabled}
        >
          <SelectTrigger
            id="executor-kind"
            data-testid="executor-kind-select"
            aria-describedby={errorId}
            aria-invalid={hasError || undefined}
            title={isCoreAgent ? 'Core agents run native only. External CLI is for workers only.' : undefined}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {KIND_OPTIONS.map((o) => {
              // G9: core agents cannot pick external-cli — the backend rejects
              // the wire payload with 400. Render the option but disable it with
              // a tooltip that explains why; the testid lets tests verify the
              // disabled flag without scraping the DOM.
              const disableForCore = isCoreAgent && o.value === 'external-cli'
              return (
                <SelectItem
                  key={o.value}
                  value={o.value}
                  disabled={disableForCore || undefined}
                  title={disableForCore ? 'Core agents run native only. External CLI is for workers only.' : undefined}
                  data-testid={`executor-kind-option-${o.value}`}
                >
                  {disableForCore ? `${o.label} (workers only)` : o.label}
                </SelectItem>
              )
            })}
          </SelectContent>
        </Select>
        <p className="text-[11px] text-[var(--color-muted)] leading-snug">
          {kind === 'native'
            ? 'Runs the sub-agent inside the Omnipus agent loop. The default and only fully-wired runtime.'
            : kind === 'external-cli'
              ? 'Delegates the sub-agent’s work to an external CLI agent process. Experimental in v0.1.0.'
              : 'Reserved for the A2A protocol. Not resolvable in v0.1.0.'}
        </p>
      </div>

      {kind === 'external-cli' && (
        <div className="space-y-1.5" data-testid="executor-cli-block">
          <label htmlFor="executor-cli" className="text-xs text-[var(--color-muted)]">
            CLI tool
          </label>
          <Select
            value={cli ?? 'claude-code'}
            onValueChange={(v) => handleCliChange(v as ExecutorCLI)}
            disabled={disabled}
          >
            <SelectTrigger id="executor-cli" data-testid="executor-cli-select">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {CLI_OPTIONS.map((o) => (
                <SelectItem key={o.value} value={o.value}>
                  {o.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {agentId && !disabled && (
            <RunnerTestButton agentId={agentId} />
          )}
        </div>
      )}

      {kind === 'remote-a2a' && (
        <div
          data-testid="executor-remote-a2a-note"
          className="flex items-start gap-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2.5"
        >
          <Info size={14} className="text-[var(--color-muted)] shrink-0 mt-0.5" weight="fill" />
          <p className="text-[11px] text-[var(--color-muted)] leading-snug">
            Remote (A2A) executors are <strong className="text-[var(--color-secondary)]">reserved — not available in v0.1.0</strong>.
            Selecting this will cause delegated sub-turns to fail with an error until A2A resolution ships.
          </p>
        </div>
      )}
    </div>
  )
}

// ── RunnerTestButton ────────────────────────────────────────────────────────────
//
// Spec-4 FR-4.2: runs the external-CLI connection test and renders the distinct
// result states. Each failure reason has its own remedy text, never collapsed.

const STATUS_BY_REASON: Record<
  RunnerTestResponse['reason'],
  { tone: 'ok' | 'warn' | 'error'; title: string }
> = {
  '': { tone: 'ok', title: 'CLI found · authenticated' },
  'missing-binary': { tone: 'error', title: 'CLI not installed' },
  'unauthenticated': { tone: 'warn', title: 'CLI found but not logged in' },
  'handshake-failed': { tone: 'error', title: 'CLI did not respond' },
  'unknown-cli': { tone: 'error', title: 'Unknown CLI' },
  'not-external-cli': { tone: 'error', title: 'No external runner to test' },
}

function RunnerTestButton({ agentId }: { agentId: string }) {
  const { mutate, data, error, isPending, reset } = useMutation<RunnerTestResponse, Error>({
    mutationFn: () => testAgentRunner(agentId),
  })

  return (
    <div className="space-y-2 pt-1">
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={isPending}
        onClick={() => {
          reset()
          mutate()
        }}
        data-testid="runner-test-button"
        className="gap-1.5"
      >
        {isPending && <Spinner size={13} className="animate-spin" />}
        {isPending ? 'Testing…' : 'Test Connection'}
      </Button>

      {error && (
        <div
          data-testid="runner-test-request-error"
          className="flex items-start gap-2 rounded-md border border-[var(--color-error)]/40 bg-[var(--color-error)]/10 px-3 py-2"
        >
          <XCircle size={14} className="text-[var(--color-error)] shrink-0 mt-0.5" weight="fill" />
          <p className="text-[11px] text-[var(--color-error)] leading-snug">
            Test request failed: {getErrorMessage(error, 'Test request failed')}
          </p>
        </div>
      )}

      {data && <RunnerTestResult result={data} />}
    </div>
  )
}

function RunnerTestResult({ result }: { result: RunnerTestResponse }) {
  const status = STATUS_BY_REASON[result.reason] ?? STATUS_BY_REASON['handshake-failed']
  const Icon = status.tone === 'ok' ? CheckCircle : status.tone === 'warn' ? WarningCircle : XCircle
  const toneClass =
    status.tone === 'ok'
      ? 'border-emerald-500/40 bg-emerald-500/10 text-emerald-400'
      : status.tone === 'warn'
        ? 'border-amber-500/40 bg-amber-500/10 text-amber-400'
        : 'border-[var(--color-error)]/40 bg-[var(--color-error)]/10 text-[var(--color-error)]'

  return (
    <div
      data-testid="runner-test-result"
      data-reason={result.reason || 'ok'}
      className={`flex items-start gap-2 rounded-md border px-3 py-2.5 ${toneClass}`}
    >
      <Icon size={15} className="shrink-0 mt-0.5" weight="fill" />
      <div className="min-w-0 space-y-0.5">
        <p className="text-xs font-medium leading-tight">{status.title}</p>
        <p className="text-[11px] opacity-90 leading-snug break-words">{result.message}</p>
        {result.cli_version && (
          <p className="text-[10px] font-mono opacity-75">
            {result.cli}
            {result.cli_version ? ` · v${result.cli_version}` : ''}
          </p>
        )}
      </div>
    </div>
  )
}
