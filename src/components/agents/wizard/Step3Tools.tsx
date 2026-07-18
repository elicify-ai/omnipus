// Step3Tools — wizard step ③ (Tools).
//
// Per spec §5.3-§5.5: tools_cfg (per-tool allow/ask/deny editor — default
// deny; HIDDEN for subagent_3p per matrix row 14), skills[] (checkbox list
// of installed skills — shown for all three types). fallback_models[]
// moved to Step1Identity.tsx (item 6 reorg — adjacent to the model field);
// see that file for the FallbackEditor implementation.
//
// The ToolPolicyEditor + SkillPicker patterns are lifted from
// `AgentProfile.tsx` (the slide-over profile editor) so the create wizard
// and edit flow cannot drift on the wire shape.
//
// W4 + W5 testids emitted per the plan's UI table:
//   wizard-tools-cfg (Main/Subagent only — HIDDEN for External per BDD),
//   wizard-skills.

import type { RegistryTool } from '@/lib/api'
import { resolveToolsCfg } from '@/lib/toolPolicyPresets'
import { ToolPolicyEditor, type ToolPolicyValue } from '@/components/shared/ToolPolicyEditor'
import { InheritToggle } from './InheritToggle'
import type { StepProps } from './types'

export function Step3Tools({
  payload,
  setField,
  initialType,
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
      {/* subagent_3p never reaches this step anymore — the wizard is two
          steps for external runners (2026-07-03): tools/skills/fallback
          policy don't apply, and the runner config lives on step ① only
          (this step used to duplicate it). The isExternal gates below stay
          as defense-in-depth. */}

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
            Starts from the Balanced preset. Per-tool allow / ask / deny editor —
            every tool has an explicit policy, no hidden default.
          </p>
          <ToolPolicyEditor
            tools={tools}
            value={toolPolicyValue(payload.tools_cfg, tools)}
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
                  className="flex items-start gap-3 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2.5 cursor-pointer hover:bg-[var(--color-surface-3)] transition-colors"
                >
                  <input tabIndex={0}
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
    </>
  )
}

// ── helpers ──────────────────────────────────────────────────────────────────

/**
 * Convert the wire-shape `AgentToolsCfg.builtin` (which is optional) into
 * the required shape `<ToolPolicyEditor>` consumes. Defaults to the
 * 'balanced' role preset — expanded to a complete map over the full known
 * tool catalog — when nothing is set yet so the editor has something
 * concrete to render before the user picks anything.
 *
 * Delegates to `resolveToolsCfg` (`@/lib/toolPolicyPresets`) — the SAME
 * helper `CreateAgentModal.tsx` uses to compute the commit-on-submit
 * default, so this render and that submit can never independently drift on
 * what counts as "already set." When `resolveToolsCfg` returns `undefined`
 * (registry not yet loaded — see its doc comment), fall back to an empty
 * policies map so the editor still has something to render; there is
 * nothing to default over yet regardless.
 */
function toolPolicyValue(cfg: StepProps['payload']['tools_cfg'], tools: RegistryTool[]): ToolPolicyValue {
  return resolveToolsCfg(cfg, tools)?.builtin ?? { policies: {} }
}

export default Step3Tools