// Step1Identity — wizard step ① (Identity).
//
// Per spec §5.3-§5.5: color, icon, name, description, model, fallback
// models (item 6 reorg — moved from Step3Tools so the editor sits directly
// adjacent to the primary model field), plus the subagent_3p executor block
// (cli_path / env_overrides / cli_args). The color + icon editors are
// lifted from `AgentFormFields.tsx` (`<AvatarColorPicker>`, `<IconPicker>`),
// the model picker is `<ModelSelector>` (Main + Subagent), and the
// subagent_3p executor inputs are free-text per the spec wireframe.
//
// W4 + W5 testids emitted per the plan's UI table:
//   wizard-name, wizard-description, wizard-color, wizard-icon, wizard-model,
//   wizard-add-fallback, wizard-fallback-N (item 6),
//   wizard-cli-chip (locked, only when initialCli is set),
//   wizard-cli-path, wizard-env-overrides, wizard-cli-args

import { useEffect, useRef, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  AvatarColorPicker,
  IconPicker,
} from '../AgentFormFields'
import { ModelSelector, type ModelGroup } from '@/components/ui/model-selector'
import { InheritToggle } from './InheritToggle'
import { CliPathValidationHint } from '../CliPathValidationHint'
import { CommandPreview } from '../CommandPreview'
import { useCliPathValidation } from '@/hooks/useCliPathValidation'
import { useCliDetect } from '@/hooks/useCliDetect'
import { buildExecutorPreviewRequest } from '@/hooks/useCommandPreview'
import { detectEntryFor, resolveCliDetectHint, SUPPORTED_CLIS } from '@/lib/cliDetect'
import { useModelToProvider } from '@/lib/agents/modelToProvider'
import type { IconName } from '@/lib/agentIcons'
import type { ExecutorCommandPreviewRequest, FallbackModel } from '@/lib/api'
import type { Provider } from '@/lib/api/generated/openapi-types'
import { X } from '@phosphor-icons/react'
import type { StepProps, WizardCli } from './types'

const MAX_FALLBACKS = 2

const CLI_LABEL: Record<WizardCli, string> = {
  'claude-code': 'claude-code',
  codex: 'codex',
  opencode: 'opencode',
}

export function Step1Identity({
  payload,
  setField,
  initialType,
  initialCli,
  connectedProviders = [],
  providersLoading = false,
  providersError,
  onRetryProviders,
}: StepProps) {
  const isWorker = initialType !== 'Main'
  const isExternal = initialType === 'subagent_3p'
  // Native subagents can inherit their model from the caller (UAT 4a).
  const isNativeSubagent = initialType === 'Subagent'
  const inheritModel = isNativeSubagent && payload.inherit_model === true

  // `connectedProviders` is provided by the parent (CreateAgentModal) so
  // the Step 1 / Step 3 sub-components stay query-client-free and the
  // CreateAgentWizard unit tests can render the wizard without a
  // QueryClientProvider wrapper. The `providerGroups` shape is the same
  // the ModelSelector consumes elsewhere.
  const providerGroups: ModelGroup[] = connectedProviders.map((p) => ({
    providerName: p.name ?? p.id ?? 'Unknown',
    // O3 two-field: pass the provider routing key so onPairChange can emit it.
    providerId: p.id,
    models: p.models ?? [],
  }))

  // Bug fix: the picker used to collapse "still loading", "fetch failed",
  // "provider connected but its model catalogue came back empty", and
  // "genuinely no provider connected" into one message, true only for the
  // last case. Motivating incident: CI logged the gateway's upstream fetch
  // to openrouter.ai failing 9 times with `context canceled` (zero
  // successes) while the healthy /providers endpoint itself returned 200
  // in 0.46s from the same worker — the picker still told the user no
  // provider was connected, which was false.
  const catalogStatus: 'loading' | 'error' | 'ready' = providersLoading
    ? 'loading'
    : providersError
      ? 'error'
      : 'ready'
  const flatModels = connectedProviders.flatMap((p) => p.models ?? [])
  // Ready + empty is itself two different truths: a provider IS connected
  // but its model catalogue came back empty (surface the backend's own
  // `Provider.warning` — e.g. "could not fetch upstream model list: status
  // 429" — rather than blaming the user for not connecting one), vs.
  // genuinely no connected provider at all (today's message, the one case
  // where it was already honest).
  const providerWithWarning = connectedProviders.find((p) => p.warning)
  const emptyCatalogHint =
    connectedProviders.length > 0 && flatModels.length === 0
      ? providerWithWarning
        ? `Model list unavailable: ${providerWithWarning.warning}`
        : 'Connected provider has no models available'
      : 'Connect a provider in Settings to pick a model'

  return (
    <>
      <div className="space-y-2">
        <label htmlFor="wizard-name" className="text-sm font-medium">
          Name <span className="text-[var(--color-error)]" aria-label="required">*</span>
        </label>
        <Input
          id="wizard-name"
          data-testid="wizard-name"
          value={payload.name}
          onChange={(e) => setField('name', e.target.value)}
          placeholder="Research Assistant"
          aria-required="true"
        />
      </div>

      <div className="space-y-2">
        <label htmlFor="wizard-description" className="text-sm font-medium">
          Description
          {isWorker && (
            <span className="text-[var(--color-error)] ml-1" aria-label="required">*</span>
          )}
        </label>
        <Textarea
          id="wizard-description"
          data-testid="wizard-description"
          value={payload.description}
          onChange={(e) => setField('description', e.target.value)}
          placeholder={
            isWorker
              ? 'What this worker handles — required for routing'
              : 'A short subtitle (optional for Main)'
          }
          aria-required={isWorker}
          rows={2}
          className="resize-none"
        />
      </div>

      <div className="space-y-2">
        <label className="text-sm font-medium">Avatar color</label>
        <AvatarColorPicker
          value={payload.color}
          onChange={(c) => setField('color', c)}
          testIdPrefix="wizard-color"
        />
      </div>

      <div className="space-y-2">
        <label className="text-sm font-medium">Icon</label>
        <IconPicker
          value={payload.icon as IconName}
          onChange={(icon) => setField('icon', icon)}
          triggerTestId="wizard-icon"
        />
      </div>

      {/* Native-subagent inherit toggle (UAT 4a). ON = model inherited from
          the caller; the picker is hidden and no model is sent. */}
      {isNativeSubagent && (
        <InheritToggle
          label="Model"
          inherit={payload.inherit_model === true}
          onChange={(v) => setField('inherit_model', v)}
          testId="wizard-inherit-model"
        />
      )}

      {!inheritModel && (
      <div className="space-y-2">
        <label htmlFor="wizard-model" className="text-sm font-medium">
          Model <span className="text-[var(--color-error)]" aria-label="required">*</span>
        </label>
        {/* subagent_3p: free-text slug only — passed verbatim to the CLI
            invocation (`claude --model <slug>`). The picker would lie about
            which providers are reachable from an external CLI runner. */}
        {isExternal ? (
          <>
            {initialCli && (
              <div
                data-testid="wizard-cli-chip"
                className="flex items-center gap-2 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2 text-xs font-mono"
              >
                <span className="text-[var(--color-muted)]">CLI:</span>
                <span className="font-medium text-[var(--color-secondary)]">
                  {CLI_LABEL[initialCli]}
                </span>
                <span className="text-[var(--color-muted)]">(locked)</span>
              </div>
            )}
            <Input
              id="wizard-model"
              data-testid="wizard-model"
              value={payload.model}
              onChange={(e) => setField('model', e.target.value)}
              placeholder="claude-sonnet-4-6"
              aria-required="true"
            />
          </>
        ) : (
          /* Main + Subagent: CONSTRAINED picker fed the connected-provider
             catalogue (UAT model-catalog fix). Selection is limited to real
             models, so a non-catalogue slug cannot be saved and the
             "unresolved" chip can never fire. When no provider is connected
             the picker shows a disabled "connect a provider" state. */
          <ModelSelector
            models={[...flatModels]}
            providerGroups={providerGroups}
            value={payload.model}
            onChange={(m) => setField('model', m)}
            onPairChange={({ provider: p }) => setField('provider', p)}
            placeholder="Pick a connected model"
            triggerTestId="wizard-model"
            constrainToCatalog
            emptyCatalogHint={emptyCatalogHint}
            catalogStatus={catalogStatus}
            catalogErrorMessage={providersError}
            onRetryCatalog={onRetryProviders}
          />
        )}
      </div>
      )}

      {/* Fallback models — item 6 reorg: relocated from Step3Tools so the
          editor sits directly adjacent to the primary model field above.
          Hidden for subagent_3p (the external runner manages its own
          retries — matrix row 16); NOT gated on inheritModel, matching the
          original Step3Tools behaviour (a native subagent that inherits its
          primary model may still configure its own fallback chain). */}
      {!isExternal && (
        <FallbackEditor
          payload={payload}
          setField={setField}
          providers={connectedProviders as Provider[]}
        />
      )}

      {/* subagent_3p executor block — rendered in Step 1 so the wireframe
          stays linear (CLI chooser → path → env → args). Re-rendered
          inside `<Advanced />` so it stays editable from the disclosure
          even after the user advances past step 1. */}
      {isExternal && (
        <ExecutorInputs
          payload={payload}
          setField={setField}
          lockedCli={initialCli}
        />
      )}
    </>
  )
}

// ── Fallback editor (item 6 reorg — moved from Step3Tools.tsx) ─────────────
// Wire shape: `[{model, provider}]`, tried in order, max 2 entries (server
// rejects more with 400). Mirrors `AgentProfile.tsx`'s edit-form fallback
// editor so the create wizard and edit flow cannot drift on the wire shape.

interface FallbackEditorProps {
  payload: StepProps['payload']
  setField: StepProps['setField']
  providers: Provider[]
}

function FallbackEditor({ payload, setField, providers }: FallbackEditorProps) {
  const fallbacks = payload.fallback_models ?? []
  const { lookup: modelToProvider } = useModelToProvider(providers)
  const providerNameById = new Map(providers.map((p) => [p.id, p.name ?? p.id ?? 'Unknown']))

  function setFallbacks(next: FallbackModel[]) {
    setField('fallback_models', next.length > 0 ? next : undefined)
  }

  function addFallback() {
    if (fallbacks.length >= MAX_FALLBACKS) return
    setFallbacks([
      ...fallbacks,
      { model: '', provider: modelToProvider('') },
    ])
  }

  function updateFallback(idx: number, patch: Partial<FallbackModel>) {
    setFallbacks(
      fallbacks.map((f, i) => (i === idx ? { ...f, ...patch } : f)),
    )
  }

  function removeFallback(idx: number) {
    setFallbacks(fallbacks.filter((_, i) => i !== idx))
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <label className="text-sm font-medium">Fallback models (max {MAX_FALLBACKS})</label>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={addFallback}
          disabled={fallbacks.length >= MAX_FALLBACKS}
          title={
            fallbacks.length >= MAX_FALLBACKS
              ? `Maximum ${MAX_FALLBACKS} fallback models`
              : undefined
          }
          data-testid="wizard-add-fallback"
          className="h-7 text-xs"
        >
          + Add fallback
        </Button>
      </div>
      <p className="text-xs text-[var(--color-muted)]">
        Each fallback is a {`{model, provider}`} pair. Server rejects more
        than {MAX_FALLBACKS} with 400.
      </p>
      {fallbacks.length === 0 && (
        <p className="text-xs text-[var(--color-muted)]">No fallbacks configured.</p>
      )}
      <div className="space-y-1.5">
        {fallbacks.map((entry, idx) => (
          <div
            key={idx}
            className="flex items-center gap-1.5 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-2 py-1.5"
            data-testid={`wizard-fallback-${idx}`}
          >
            <Input
              value={entry.model}
              onChange={(e) => {
                const model = e.target.value
                updateFallback(idx, {
                  model,
                  // Auto-fill the provider from the slug → provider map
                  // so the user does not have to retype it. Operator can
                  // override via the explicit picker below.
                  provider: modelToProvider(model) || entry.provider,
                })
              }}
              placeholder="model slug"
              className="font-mono text-xs h-7 flex-1"
              data-testid={`wizard-fallback-model-${idx}`}
            />
            <Select
              value={entry.provider || '__auto__'}
              onValueChange={(v) => updateFallback(idx, { provider: v === '__auto__' ? '' : v })}
            >
              <SelectTrigger
                className="h-7 px-2 text-xs"
                data-testid={`wizard-fallback-provider-${idx}`}
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="__auto__">auto</SelectItem>
                {providers.map((p) => (
                  <SelectItem key={p.id} value={p.id}>
                    {providerNameById.get(p.id) ?? p.id}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <button tabIndex={0}
              type="button"
              onClick={() => removeFallback(idx)}
              className="inline-flex items-center justify-center text-[var(--color-muted)] hover:text-[var(--color-error)] p-1 pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]"
              aria-label={`Remove fallback ${idx + 1}`}
              data-testid={`wizard-fallback-remove-${idx}`}
            >
              <X size={12} />
            </button>
          </div>
        ))}
      </div>
    </div>
  )
}

// ── Executor inputs ─────────────────────────────────────────────────────────

export interface ExecutorInputsProps {
  payload: StepProps['payload']
  setField: StepProps['setField']
  /** When set, the CLI chooser radios are hidden — CLI is locked from
   *  the roster. */
  lockedCli?: WizardCli
}

// ── Env override row helpers ────────────────────────────────────────────────
// The wire shape (`executor_env_overrides`) is a flat `Record<string, string>`,
// but keying editor rows off the KEY itself (`key={k}`) meant every keystroke
// in the KEY field changed the row's React key — remounting the input and
// dropping focus mid-word, and silently MERGING two rows the instant their
// keys collided (no error, just one row vanishing). Rows below carry a
// synthetic `id` that never changes for the row's lifetime; the KEY/VALUE
// text is local editor state, serialized back to the record shape only when
// it's safe to do so (see `commitIfValid`).
interface EnvRow {
  id: string
  key: string
  value: string
}

function recordToRows(record: Record<string, string>): EnvRow[] {
  return Object.entries(record).map(([key, value], i) => ({ id: `env-init-${i}`, key, value }))
}

function rowsToRecord(rows: EnvRow[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const row of rows) out[row.key] = row.value
  return out
}

/** Non-empty keys that appear on more than one row. Empty keys (freshly
 *  "+ Add"-ed, not yet typed into) are excluded — they aren't a meaningful
 *  collision to flag until the operator starts naming them. */
function findDuplicateKeys(rows: EnvRow[]): Set<string> {
  const seen = new Set<string>()
  const dupes = new Set<string>()
  for (const row of rows) {
    if (row.key === '') continue
    if (seen.has(row.key)) dupes.add(row.key)
    seen.add(row.key)
  }
  return dupes
}

/**
 * The subagent_3p executor inputs — CLI chooser (when not locked),
 * cli_path, env_overrides (KEY=VALUE key/value editor), and cli_args.
 * Exported so `<Advanced />` can render the same block when the user
 * opens the disclosure after advancing past step 1.
 */
export function ExecutorInputs({ payload, setField, lockedCli }: ExecutorInputsProps) {
  // Stable-id row state (FW-4 focus-loss fix) — seeded ONCE from the payload
  // at mount. `executor_env_overrides` is only ever written by this
  // component's own commits (see `commitIfValid`), so there is no external
  // writer to reconcile against after mount.
  const [envRows, setEnvRows] = useState<EnvRow[]>(() => recordToRows(payload.executor_env_overrides ?? {}))
  const envRowIdCounter = useRef(envRows.length)
  const envDuplicateKeys = findDuplicateKeys(envRows)

  /** Pushes `rows` to the payload UNLESS a duplicate key exists anywhere in
   *  it — a real collision would silently overwrite one entry the instant
   *  it's serialized to the `Record<string,string>` wire shape, so a
   *  pending duplicate simply blocks the commit (leaving the last known-good
   *  payload in place) instead of merging. */
  function commitIfValid(rows: EnvRow[]) {
    if (findDuplicateKeys(rows).size > 0) return
    setField('executor_env_overrides', rowsToRecord(rows))
  }

  function addEnvRow() {
    envRowIdCounter.current += 1
    const next = [...envRows, { id: `env-${envRowIdCounter.current}`, key: '', value: '' }]
    setEnvRows(next)
    commitIfValid(next)
  }

  /** KEY edits are local-only while typing (no commit) — this is what stops
   *  a transient duplicate from ever reaching the payload mid-rename. */
  function updateEnvKeyDraft(id: string, newKey: string) {
    setEnvRows(envRows.map((row) => (row.id === id ? { ...row, key: newKey } : row)))
  }

  /** Commits the row set on blur — the "rename" is only final once the
   *  operator leaves the field, and only if it didn't produce a duplicate. */
  function commitEnvKey() {
    commitIfValid(envRows)
  }

  function updateEnvValue(id: string, value: string) {
    const next = envRows.map((row) => (row.id === id ? { ...row, value } : row))
    setEnvRows(next)
    commitIfValid(next)
  }

  function removeEnvRow(id: string) {
    const next = envRows.filter((row) => row.id !== id)
    setEnvRows(next)
    commitIfValid(next)
  }

  // Live command-line preview (executor-command-preview) — built from the
  // flat wizard payload (no persisted agent id exists yet, hence the
  // stateless/body-driven endpoint). `max_tool_iterations` is deliberately
  // omitted: `initialPayload` in `CreateAgentWizard.tsx` only seeds it for
  // non-subagent_3p types (agent-types-field-matrix.md Decisions #1 — the
  // external CLI runs its own tool loop), so `payload.max_tool_iterations`
  // is always undefined here anyway; omitting it lets the preview fall back
  // to the same server-side default (50) real dispatch uses for this tier.
  const commandPreviewRequest: ExecutorCommandPreviewRequest | undefined = payload.cli
    ? buildExecutorPreviewRequest(payload.cli, payload.model, payload.executor_cli_path, payload.executor_cli_args)
    : undefined

  // external-executor-cli-path-detection spec (ADR-030) — US-1/US-2: probe
  // the host once per mount (no query-client dependency, matching the
  // `AgentListScreen` cli-detect pattern) so the path field can be prefilled
  // per selected CLI. Detection failure is non-fatal — the field just stays
  // manual (US-1 AC-3's "not found" hint degrades to "unknown"). Shared with
  // `AgentProfile`'s Runtime tab via `useCliDetect` (Simplify F1).
  const cliDetect = useCliDetect()

  // US-1 AC-1/AC-2: prefill the detected absolute path when the field is
  // EMPTY, once per CLI (re)selection. Deliberately does NOT depend on
  // `payload.executor_cli_path` — re-reading it (not re-triggering on every
  // keystroke) is what lets an operator's non-empty value stay authoritative
  // without the effect fighting their edits (US-4).
  useEffect(() => {
    if (!payload.cli || !cliDetect) return
    if ((payload.executor_cli_path ?? '').trim().length > 0) return
    const entry = detectEntryFor(cliDetect, payload.cli)
    if (entry?.installed && entry.path) {
      setField('executor_cli_path', entry.path)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [payload.cli, cliDetect])

  // US-3/US-5: validate-on-blur, debounced + cancel-in-flight (see the hook).
  const cliValidation = useCliPathValidation()
  const detectHint = resolveCliDetectHint(cliDetect, payload.cli)

  // Lift the classification up onto the payload (UI-only field, never sent
  // to the wire — see WizardSubmitPayload.executor_cli_validation_reason)
  // so CreateAgentWizard can gate the Create button on it (FR-008/FR-018).
  //
  // IMPORTANT: this only WRITES on a definitive "result" — it deliberately
  // does NOT clear the field on idle/pending/error. `<Step3Tools>` renders a
  // SECOND, independent `<ExecutorInputs>` instance (so the executor block
  // stays editable from step 3 without a 4th stepper step) with its own
  // fresh `cliValidation` (starts at idle). If this effect cleared on every
  // idle mount, mounting that second instance on step 3 would silently wipe
  // a block set on step 1 the moment the operator reaches the Create button.
  // Clearing instead happens explicitly at the two real invalidation points —
  // editing the path and switching CLI (both below) — so a stale block never
  // survives an actual edit (FR-019), but merely re-mounting the same
  // unedited value does not resurrect an already-cleared verdict either.
  useEffect(() => {
    if (cliValidation.status.kind !== 'result') return
    setField('executor_cli_validation_reason', cliValidation.status.result.reason)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cliValidation.status])

  return (
    <div className="space-y-4 pt-4 border-t border-[var(--color-border)]">
      {/* CLI chooser — only when the roster did NOT pre-lock one. */}
      {!lockedCli && (
        <div className="space-y-2">
          <label className="text-sm font-medium">CLI runtime</label>
          <div className="flex gap-2 flex-wrap" data-testid="wizard-cli-chooser">
            {(SUPPORTED_CLIS as readonly WizardCli[]).map((cli) => {
              const selected = payload.cli === cli
              return (
                <button tabIndex={0}
                  key={cli}
                  type="button"
                  onClick={() => {
                    // Switching CLI invalidates any verdict validated
                    // against the previous CLI at this path. Clear both the
                    // local hook (for THIS mount's hint) and the payload
                    // field directly (the result-only sync effect above
                    // deliberately does not clear on idle — see its
                    // comment — so this explicit clear is what actually
                    // lifts a stale block, FR-019).
                    cliValidation.reset()
                    setField('executor_cli_validation_reason', undefined)
                    setField('cli', cli)
                  }}
                  className={
                    selected
                      ? 'px-3 py-1.5 rounded-md text-xs font-medium border bg-[var(--color-accent)]/20 text-[var(--color-accent)] border-[var(--color-accent)]/40'
                      : 'px-3 py-1.5 rounded-md text-xs font-medium border border-[var(--color-border)] text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors'
                  }
                  data-testid={`wizard-cli-${cli}`}
                  aria-pressed={selected}
                >
                  {CLI_LABEL[cli]}
                </button>
              )
            })}
          </div>
          <p className="text-[11px] text-[var(--color-muted)]">
            Selects the external runner that will execute this agent.
          </p>
        </div>
      )}

      <div className="space-y-2">
        <label htmlFor="wizard-cli-path" className="text-sm font-medium">
          CLI path
        </label>
        <Input
          id="wizard-cli-path"
          data-testid="wizard-cli-path"
          value={payload.executor_cli_path ?? ''}
          onChange={(e) => {
            // Editing invalidates any prior validate() verdict. Clear both
            // the local hook (for THIS mount's hint) and the payload field
            // directly — the result-only sync effect above deliberately
            // does not clear on idle (see its comment), so this explicit
            // clear is what actually lifts a stale block (FR-019).
            cliValidation.reset()
            setField('executor_cli_validation_reason', undefined)
            setField('executor_cli_path', e.target.value)
          }}
          onBlur={(e) => {
            if (!payload.cli) return
            cliValidation.validate(payload.cli, e.target.value)
          }}
          placeholder="/usr/local/bin/claude-code"
          className="font-mono text-xs"
        />
        <CliPathValidationHint
          validation={cliValidation.status}
          detectHint={detectHint}
          testId="wizard-cli-path-status"
        />
        <p className="text-[11px] text-[var(--color-muted)]">
          Path to the CLI executable on the host.
        </p>
      </div>

      <div className="space-y-2" data-testid="wizard-env-overrides">
        <div className="flex items-center justify-between">
          <label className="text-sm font-medium">Environment overrides</label>
          <button tabIndex={0}
            type="button"
            onClick={addEnvRow}
            className="text-[11px] text-[var(--color-accent)] hover:underline"
            data-testid="wizard-env-overrides-add"
          >
            + Add env var
          </button>
        </div>
        {envRows.length === 0 && (
          <p className="text-[11px] text-[var(--color-muted)]">
            No env overrides. Add KEY=VALUE pairs passed to the CLI process.
          </p>
        )}
        <div className="space-y-1.5">
          {envRows.map((row) => {
            const isDuplicate = row.key !== '' && envDuplicateKeys.has(row.key)
            return (
              <div key={row.id} className="space-y-1">
                <div className="flex items-center gap-1.5">
                  <Input
                    value={row.key}
                    onChange={(e) => updateEnvKeyDraft(row.id, e.target.value)}
                    onBlur={commitEnvKey}
                    placeholder="KEY"
                    className="font-mono text-xs flex-1"
                    aria-label="Environment variable name"
                    aria-invalid={isDuplicate || undefined}
                    data-testid={`wizard-env-key-${row.id}`}
                  />
                  <Input
                    value={row.value}
                    onChange={(e) => updateEnvValue(row.id, e.target.value)}
                    placeholder="VALUE"
                    className="font-mono text-xs flex-1"
                    aria-label="Environment variable value"
                    data-testid={`wizard-env-value-${row.id}`}
                  />
                  <button tabIndex={0}
                    type="button"
                    onClick={() => removeEnvRow(row.id)}
                    className="inline-flex items-center justify-center text-[var(--color-muted)] hover:text-[var(--color-error)] text-xs px-2 pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]"
                    aria-label={`Remove ${row.key || 'env var'}`}
                  >
                    ×
                  </button>
                </div>
                {isDuplicate && (
                  <p
                    className="text-[10px] text-[var(--color-error)]"
                    data-testid={`wizard-env-duplicate-${row.id}`}
                  >
                    Duplicate key "{row.key}" — rename it before this change is saved.
                  </p>
                )}
              </div>
            )
          })}
        </div>
      </div>

      <div className="space-y-2">
        <label htmlFor="wizard-cli-args" className="text-sm font-medium">
          Additional CLI arguments
        </label>
        <Input
          id="wizard-cli-args"
          data-testid="wizard-cli-args"
          value={payload.executor_cli_args ?? ''}
          onChange={(e) => setField('executor_cli_args', e.target.value)}
          placeholder="e.g. --add-dir /extra/path"
          className="font-mono text-xs"
        />
        <p className="text-[11px] text-[var(--color-muted)]">
          Space-separated args passed before the user prompt, in addition to the flags Omnipus applies automatically when this agent runs — see the live command preview below. Any argument that would be silently ignored is called out there before you create the agent.
        </p>
      </div>

      <CommandPreview req={commandPreviewRequest} testId="wizard-command-preview" />
    </div>
  )
}

export default Step1Identity