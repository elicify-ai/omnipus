// Step3Tools — wizard step ③ (Tools).
//
// Per spec §5.3-§5.5: tools_cfg (per-tool allow/ask/deny editor — default
// deny; HIDDEN for subagent_3p per matrix row 14), skills[] (checkbox list
// of installed skills — shown for all three types), fallback_models[]
// (chip + per-entry provider picker, max 2 — HIDDEN for subagent_3p per
// matrix row 16).
//
// The ToolPolicyEditor + SkillPicker + FallbackEditor patterns are lifted
// from `AgentProfile.tsx` (the slide-over profile editor) so the create
// wizard and edit flow cannot drift on the wire shape.
//
// W4 + W5 testids emitted per the plan's UI table:
//   wizard-tools-cfg (Main/Subagent only — HIDDEN for External per BDD),
//   wizard-skills,
//   wizard-add-fallback.

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import type {
  FallbackModel,
  RegistryTool,
} from '@/lib/api'
import { useModelToProvider } from '@/lib/agents/modelToProvider'
import { applyRolePreset } from '@/lib/toolPolicyPresets'
import { ToolPolicyEditor, type ToolPolicyValue } from '@/components/shared/ToolPolicyEditor'
import { InheritToggle } from './InheritToggle'
import { ExecutorInputs } from './Step1Identity'
import type { Provider } from '@/lib/api/generated/openapi-types'
import { X } from '@phosphor-icons/react'
import type { StepProps } from './types'

const MAX_FALLBACKS = 2

export function Step3Tools({
  payload,
  setField,
  initialType,
  initialCli,
  connectedProviders = [],
  registryTools = [],
  skills = [],
  globalPolicies,
}: StepProps) {
  const isExternal = initialType === 'subagent_3p'
  // Native subagents can inherit Tools / Skills from the caller (UAT 4a).
  const isNativeSubagent = initialType === 'Subagent'
  const inheritTools = isNativeSubagent && payload.inherit_tools === true
  const inheritSkills = isNativeSubagent && payload.inherit_skills === true
  const tools: RegistryTool[] = [...registryTools]

  // Global (Settings → Security) tool policy — used to lock per-agent controls
  // that would contradict a global deny/ask (no contradicting configs). Provided
  // by the parent (CreateAgentModal) via props so this step stays presentational.
  const globalPolicyValue: ToolPolicyValue | undefined = globalPolicies

  return (
    <>
      {/* subagent_3p: the external CLI runner config is the substance of the
          final step (tools / fallback policy don't apply to an external
          runner). Rendered inline (not in the collapsed Advanced disclosure)
          so the operator can finalize cli_path / env / args before creating.
          This is the same editor surfaced on Step 1 — both write the same
          payload, so edits stay in sync. */}
      {isExternal && (
        <div className="space-y-2">
          <label className="text-sm font-medium">External CLI runner</label>
          <p className="text-xs text-[var(--color-muted)]">
            The external runner manages its own isolation, auth, and retries.
          </p>
          <ExecutorInputs
            payload={payload}
            setField={setField}
            lockedCli={initialCli}
          />
        </div>
      )}

      {!isExternal && isNativeSubagent && (
        <InheritToggle
          label="Tools"
          inherit={payload.inherit_tools === true}
          onChange={(v) => setField('inherit_tools', v)}
          testId="wizard-inherit-tools"
        />
      )}
      {!isExternal && !inheritTools && (
        <div className="space-y-2" data-testid="wizard-tools-cfg">
          <label className="text-sm font-medium">Tools policy</label>
          <p className="text-xs text-[var(--color-muted)]">
            Default: deny. Per-tool allow / ask / deny editor.
          </p>
          <ToolPolicyEditor
            tools={tools}
            value={toolPolicyValue(payload.tools_cfg)}
            globalPolicies={globalPolicyValue}
            onChange={(next) =>
              setField(
                'tools_cfg',
                { ...(payload.tools_cfg ?? {}), builtin: next } as unknown as StepProps['payload']['tools_cfg'],
              )
            }
          />
        </div>
      )}

      {isNativeSubagent && (
        <InheritToggle
          label="Skills"
          inherit={payload.inherit_skills === true}
          onChange={(v) => setField('inherit_skills', v)}
          testId="wizard-inherit-skills"
        />
      )}
      {/* Skills — HIDDEN for subagent_3p (matrix row 14): an external CLI
          runner (claude-code / codex / opencode) can never load Omnipus
          skills, so the mapping was meaningless for 3p agents (P3 bug,
          2026-07-03 — the gate below was missing !isExternal). */}
      {!isExternal && !inheritSkills && (
      <div className="space-y-2" data-testid="wizard-skills">
        <label className="text-sm font-medium">Skills</label>
        <p className="text-xs text-[var(--color-muted)]">
          Multi-select chips of installed skills. Empty = no skills granted.
        </p>
        {skills.length === 0 ? (
          <p className="text-xs text-[var(--color-muted)]">No skills installed.</p>
        ) : (
          <div className="space-y-1.5">
            {skills.map((skill) => {
              const granted = (payload.skills ?? []).includes(skill.id)
              return (
                <label
                  key={skill.id}
                  className="flex items-start gap-3 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2.5 cursor-pointer hover:bg-[var(--color-surface-2)] transition-colors"
                >
                  <input
                    type="checkbox"
                    checked={granted}
                    onChange={(e) => {
                      const current = payload.skills ?? []
                      setField(
                        'skills',
                        e.target.checked
                          ? [...current, skill.id]
                          : current.filter((s) => s !== skill.id),
                      )
                    }}
                    className="mt-0.5 shrink-0 accent-[var(--color-accent)]"
                    data-testid={`skill-checkbox-${skill.id}`}
                  />
                  <div className="min-w-0">
                    <p className="text-sm font-medium text-[var(--color-secondary)] leading-tight">
                      {skill.name}
                    </p>
                    {skill.description && (
                      <p className="text-[11px] text-[var(--color-muted)] mt-0.5 leading-snug">
                        {skill.description}
                      </p>
                    )}
                    <span className="text-[10px] font-mono text-[var(--color-muted)]/70">
                      {skill.id}
                    </span>
                  </div>
                </label>
              )
            })}
          </div>
        )}
      </div>
      )}

      {!isExternal && (
        <FallbackEditor
          payload={payload}
          setField={setField}
          providers={connectedProviders as Provider[]}
        />
      )}
    </>
  )
}

// ── Fallback editor ──────────────────────────────────────────────────────────

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
            <select
              value={entry.provider ?? ''}
              onChange={(e) => updateFallback(idx, { provider: e.target.value })}
              className="h-7 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-2 text-xs text-[var(--color-secondary)]"
              data-testid={`wizard-fallback-provider-${idx}`}
            >
              <option value="">auto</option>
              {providers.map((p) => (
                <option key={p.id} value={p.id}>
                  {providerNameById.get(p.id) ?? p.id}
                </option>
              ))}
            </select>
            <button
              type="button"
              onClick={() => removeFallback(idx)}
              className="text-[var(--color-muted)] hover:text-[var(--color-error)] p-1"
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

// ── helpers ──────────────────────────────────────────────────────────────────

/**
 * Convert the wire-shape `AgentToolsCfg.builtin` (which is optional) into
 * the required shape `<ToolPolicyEditor>` consumes. Defaults to the
 * 'balanced' role preset when nothing is set yet so the editor has
 * something to render before the user picks anything.
 */
function toolPolicyValue(cfg: StepProps['payload']['tools_cfg']): ToolPolicyValue {
  const builtin = cfg?.builtin
  if (builtin && typeof builtin.default_policy === 'string' && typeof builtin.policies === 'object') {
    return {
      default_policy: builtin.default_policy,
      policies: { ...builtin.policies },
    }
  }
  return applyRolePreset('balanced')
}

export default Step3Tools