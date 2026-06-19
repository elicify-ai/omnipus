// Step1Identity — wizard step ① (Identity).
//
// Per spec §5.3-§5.5: color, icon, name, description, model, plus the
// subagent_3p executor block (cli_path / env_overrides / cli_args). The
// color + icon editors are lifted from `AgentFormFields.tsx`
// (`<AvatarColorPicker>`, `<IconPicker>`), the model picker is
// `<ModelSelector>` (Main + Subagent), and the subagent_3p executor
// inputs are free-text per the spec wireframe.
//
// W4 + W5 testids emitted per the plan's UI table:
//   wizard-name, wizard-description, wizard-color, wizard-icon, wizard-model,
//   wizard-cli-chip (locked, only when initialCli is set),
//   wizard-cli-path, wizard-env-overrides, wizard-cli-args

import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import {
  AvatarColorPicker,
  IconPicker,
} from '../AgentFormFields'
import { ModelSelector, type ModelGroup } from '@/components/ui/model-selector'
import type { IconName } from '@/lib/agentIcons'
import type { StepProps, WizardCli } from './types'

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
}: StepProps) {
  const isWorker = initialType !== 'Main'
  const isExternal = initialType === 'subagent_3p'

  // `connectedProviders` is provided by the parent (CreateAgentModal) so
  // the Step 1 / Step 3 sub-components stay query-client-free and the
  // CreateAgentWizard unit tests can render the wizard without a
  // QueryClientProvider wrapper. The `providerGroups` shape is the same
  // the ModelSelector consumes elsewhere.
  const providerGroups: ModelGroup[] = connectedProviders.map((p) => ({
    providerName: p.name ?? p.id ?? 'Unknown',
    models: p.models ?? [],
  }))

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
          /* Main + Subagent: searchable picker filtered by connected providers.
             showUnresolvedIndicator surfaces an "Unresolved" chip if the saved
             slug isn't in the provider catalogue — same UX as the model picker
             on the profile edit slide-over (W6-C4 / G12). */
          <ModelSelector
            models={[...connectedProviders.flatMap((p) => p.models ?? [])]}
            providerGroups={providerGroups}
            value={payload.model}
            onChange={(m) => setField('model', m)}
            placeholder="Pick a connected model"
            triggerTestId="wizard-model"
            showUnresolvedIndicator
          />
        )}
      </div>

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

// ── Executor inputs ─────────────────────────────────────────────────────────

export interface ExecutorInputsProps {
  payload: StepProps['payload']
  setField: StepProps['setField']
  /** When set, the CLI chooser radios are hidden — CLI is locked from
   *  the roster. */
  lockedCli?: WizardCli
}

/**
 * The subagent_3p executor inputs — CLI chooser (when not locked),
 * cli_path, env_overrides (KEY=VALUE key/value editor), and cli_args.
 * Exported so `<Advanced />` can render the same block when the user
 * opens the disclosure after advancing past step 1.
 */
export function ExecutorInputs({ payload, setField, lockedCli }: ExecutorInputsProps) {
  const envOverrides = payload.executor_env_overrides ?? {}

  function addEnvRow() {
    setField('executor_env_overrides', { ...envOverrides, '': '' })
  }

  function updateEnvKey(oldKey: string, newKey: string) {
    if (newKey === oldKey) return
    const next: Record<string, string> = {}
    for (const [k, v] of Object.entries(envOverrides)) {
      next[k === oldKey ? newKey : k] = v
    }
    setField('executor_env_overrides', next)
  }

  function updateEnvValue(key: string, value: string) {
    setField('executor_env_overrides', { ...envOverrides, [key]: value })
  }

  function removeEnvRow(key: string) {
    const next = { ...envOverrides }
    delete next[key]
    setField('executor_env_overrides', next)
  }

  return (
    <div className="space-y-4 pt-4 border-t border-[var(--color-border)]">
      {/* CLI chooser — only when the roster did NOT pre-lock one. */}
      {!lockedCli && (
        <div className="space-y-2">
          <label className="text-sm font-medium">CLI runtime</label>
          <div className="flex gap-2 flex-wrap" data-testid="wizard-cli-chooser">
            {(['claude-code', 'codex', 'opencode'] as WizardCli[]).map((cli) => {
              const selected = payload.cli === cli
              return (
                <button
                  key={cli}
                  type="button"
                  onClick={() => setField('cli', cli)}
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
          onChange={(e) => setField('executor_cli_path', e.target.value)}
          placeholder="/usr/local/bin/claude-code"
          className="font-mono text-xs"
        />
        <p className="text-[11px] text-[var(--color-muted)]">
          Path to the CLI executable on the host.
        </p>
      </div>

      <div className="space-y-2" data-testid="wizard-env-overrides">
        <div className="flex items-center justify-between">
          <label className="text-sm font-medium">Environment overrides</label>
          <button
            type="button"
            onClick={addEnvRow}
            className="text-[11px] text-[var(--color-accent)] hover:underline"
            data-testid="wizard-env-overrides-add"
          >
            + Add env var
          </button>
        </div>
        {Object.keys(envOverrides).length === 0 && (
          <p className="text-[11px] text-[var(--color-muted)]">
            No env overrides. Add KEY=VALUE pairs passed to the CLI process.
          </p>
        )}
        <div className="space-y-1.5">
          {Object.entries(envOverrides).map(([k, v]) => (
            <div key={k} className="flex items-center gap-1.5">
              <Input
                value={k}
                onChange={(e) => updateEnvKey(k, e.target.value)}
                placeholder="KEY"
                className="font-mono text-xs flex-1"
                aria-label="Environment variable name"
              />
              <Input
                value={v}
                onChange={(e) => updateEnvValue(k, e.target.value)}
                placeholder="VALUE"
                className="font-mono text-xs flex-1"
                aria-label="Environment variable value"
              />
              <button
                type="button"
                onClick={() => removeEnvRow(k)}
                className="text-[var(--color-muted)] hover:text-[var(--color-error)] text-xs px-2"
                aria-label={`Remove ${k || 'env var'}`}
              >
                ×
              </button>
            </div>
          ))}
        </div>
      </div>

      <div className="space-y-2">
        <label htmlFor="wizard-cli-args" className="text-sm font-medium">
          CLI arguments
        </label>
        <Input
          id="wizard-cli-args"
          data-testid="wizard-cli-args"
          value={payload.executor_cli_args ?? ''}
          onChange={(e) => setField('executor_cli_args', e.target.value)}
          placeholder="--verbose --output json"
          className="font-mono text-xs"
        />
        <p className="text-[11px] text-[var(--color-muted)]">
          Space-separated args passed before the user prompt.
        </p>
      </div>
    </div>
  )
}

export default Step1Identity