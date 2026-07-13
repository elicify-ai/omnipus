// CreateAgentWizard — 3-step + Advanced wizard for creating user-creatable
// agents. W4 of agent-form-requirements delivery.
//
// Split into sub-components per the plan's file-ownership matrix:
//   ./wizard/Step1Identity.tsx   — color, icon, name, description, model
//   ./wizard/Step2Personality.tsx — soul, heartbeat, voice
//   ./wizard/Step3Tools.tsx      — tools_cfg, skills, fallback_models
//   ./wizard/Advanced.tsx       — model_params, shell, etc. (deferred)
// This file owns the Sheet shell, the type/CLI chips, the stepper, and the
// footer with submit handling. Each step is given the current payload + a
// `setField` callback so the wizard is a controlled form.
//
// Per spec §11 #3 the `[×]` on the Type chip closes the wizard (no
// confirmation even if dirty — by design). On submit error the wizard
// surfaces the error inline + keeps the modal open (silent-drop fixed
// per silent-failure-hunter review).

import * as React from 'react'
import { useReducer, useRef } from 'react'

import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetDescription, SheetFooter } from '@/components/ui/sheet'
import { FormError } from '@/components/ui/FormError'
import { X } from '@phosphor-icons/react'

import { useUiStore } from '@/store/ui'
import { isApiError, type AgentToolsCfg, type FallbackModel, type RegistryTool } from '@/lib/api'
import { AVATAR_COLORS_BY_NAME } from '@/lib/constants'
import { useFocusRestore } from '@/hooks/useFocusRestore'
import { isBlockingCliReason } from '@/hooks/useCliPathValidation'
import type { Provider, Skill, CliValidateResponse } from '@/lib/api/generated/openapi-types'

import { Step1Identity } from './wizard/Step1Identity'
import { Step2Personality } from './wizard/Step2Personality'
import { Step3Tools } from './wizard/Step3Tools'
import { Advanced } from './wizard/Advanced'

export type WizardType = 'Main' | 'Subagent' | 'subagent_3p'
export type WizardCli = 'claude-code' | 'codex' | 'opencode'

export interface WizardSubmitPayload {
  type: WizardType
  cli?: WizardCli
  name: string
  description: string
  color: string
  icon: string
  model: string
  /** O3 two-field: explicit provider routing key paired with model.
   *  Empty string / absent = resolve via default provider. */
  provider?: string
  soul: string
  heartbeat?: string
  heartbeat_enabled?: boolean
  heartbeat_interval?: number
  voice?: string
  // Wire-format fields — typed against the generated `AgentCreateRequest`
  // shape so `payloadToCreateRequest` (in CreateAgentModal.tsx) can
  // forward them verbatim without `as` casts.
  tools_cfg?: AgentToolsCfg
  skills?: string[]
  fallback_models?: FallbackModel[]
  executor_cli_path?: string
  executor_env_overrides?: Record<string, string>
  executor_cli_args?: string
  /**
   * external-executor-cli-path-detection spec (ADR-030) — UI-only, never
   * forwarded to the wire (payloadToCreateRequest in CreateAgentModal.tsx
   * never reads it). Set by `ExecutorInputs`' validate-on-blur whenever a
   * `POST /system/cli-validate` call resolves to a definitive result;
   * cleared (undefined) while unchecked/pending/errored so an in-flight or
   * failed re-check never leaves a stale block in place (FR-019). Consumed
   * below to gate the Create button on missing-binary/handshake-failed
   * (FR-008/FR-018) — never on a raw `ok` boolean.
   */
  executor_cli_validation_reason?: CliValidateResponse['reason']
  // Advanced disclosure fields — W5. Each field matches the
  // `AgentCreateRequest` shape so the modal's payloadToCreateRequest
  // can forward them as-is. All optional.
  model_params?: {
    temperature?: number
    max_tokens?: number
    top_p?: number
  }
  shell_policy?: {
    enable_deny_patterns?: boolean
    custom_deny_patterns?: string[]
  }
  rate_limits?: {
    use_global_defaults?: boolean
    max_llm_calls_per_hour?: number
    max_tool_calls_per_minute?: number
    max_cost_per_day?: number
  }
  timeout_seconds?: number
  max_tool_iterations?: number
  steering_mode?: 'one-at-a-time' | 'queue-and-process'
  // ── Inherit-from-caller toggles (UAT agent-form fix 4a) ──────────────
  // UI-only flags (NOT wire fields). A native (in-process) subagent is a
  // delegation-only worker: by default its Model / Tools / Skills
  // are inherited from the caller (the agent that delegates to it). These
  // toggles make that inheritance EXPLICIT and editable in the creation
  // wizard. When a toggle is ON (inherit), the corresponding editor is
  // hidden and the field is OMITTED from the create request so the server
  // keeps the inherited rail. When OFF, the editor is revealed and the
  // explicit value is sent. They default ON for native Subagents and have
  // no effect for Main agents (which never inherit) or external subagents
  // (whose external runner honours their own config).
  inherit_model?: boolean
  inherit_tools?: boolean
  inherit_skills?: boolean
}

interface WizardProps {
  initialType: WizardType
  initialCli?: WizardCli
  onSubmit: (payload: WizardSubmitPayload) => Promise<void>
  /**
   * Called when the user dismisses the wizard (Cancel button, type chip
   * `[×]`, or Sheet onOpenChange → false). Defaults to the store's
   * `closeCreateAgentModal`. Wired by the prop-driven test path so a
   * test's `onClose` mock fires.
   */
  onClose?: () => void
  /**
   * Connected providers (status === 'connected') for the Step 1 model
   * picker and the Step 3 fallback editor. Lifted from CreateAgentModal
   * so the wizard itself stays query-client-free (and unit-testable
   * without a QueryClientProvider wrapper).
   */
  connectedProviders?: ReadonlyArray<Provider>
  /** Registry tools for the Step 3 ToolPolicyEditor. */
  registryTools?: ReadonlyArray<RegistryTool>
  /** Installed skills for the Step 3 skills picker. */
  skills?: ReadonlyArray<Skill>
  /** Global (Settings → Security) tool policy — locks contradicting Step 3 controls. */
  globalPolicies?: import('@/components/shared/ToolPolicyEditor').ToolPolicyValue
}

const TYPE_CHIP_LABEL: Record<WizardType, string> = {
  Main: 'Main',
  Subagent: 'Subagent',
  'subagent_3p': 'Subagent (External)',
}

const CLI_CHIP_LABEL: Record<WizardCli, string> = {
  'claude-code': 'claude-code',
  codex: 'codex',
  opencode: 'opencode',
}

const STEP_NAMES = ['Identity', 'Personality', 'Tools'] as const
// subagent_3p (external CLI runner): tools/skills/fallback policy never apply
// to an external runner, and its step ③ only duplicated step ①'s runner
// config (P3 bug, 2026-07-03) — the wizard is TWO steps for that type.
const STEP_NAMES_EXTERNAL = ['Identity', 'Personality'] as const

// Action is a discriminated union of per-field setters. The mapped type
// keeps the value type coupled to the field key so a bad assignment
// fails at compile time (no `unknown` hole, no `as` cast in the reducer).
type SetAction<K extends keyof WizardSubmitPayload = keyof WizardSubmitPayload> = {
  type: 'set'
  field: K
  value: WizardSubmitPayload[K]
}
type Action = SetAction

function reducer(state: WizardSubmitPayload, action: Action): WizardSubmitPayload {
  return { ...state, [action.field]: action.value }
}

function initialPayload(initialType: WizardType, initialCli?: WizardCli): WizardSubmitPayload {
  // Per spec §4.4 the default color is the first entry of the palette map.
  // `avatarColorName()` resolves its semantic label; the wire format stores
  // the hex value, so we keep the hex.
  const defaultColorHex = Object.keys(AVATAR_COLORS_BY_NAME)[0] as string
  return {
    type: initialType,
    cli: initialCli,
    name: '',
    description: '',
    color: defaultColorHex,
    icon: 'Robot',
    model: '',
    provider: '',
    soul: '',
    heartbeat_enabled: false,
    heartbeat_interval: 1800,
    // Per the field matrix (docs/internal/architecture/agent-types-field-matrix.md)
    // timeout_seconds is O for every user-creatable type, so it always seeds.
    // max_tool_iterations is excluded for subagent_3p (the external CLI runs
    // its own loop — agent-types-field-matrix.md, Decisions #1 (resolved
    // 2026-07-03): excluded).
    // steering_mode is a Main-surface concept only (workers are forced
    // one-at-a-time server-side) — seeding it for a worker payload would
    // carry a field its variant can't have even though payloadToCreateRequest
    // already filters it; the Advanced UI also reads this default.
    timeout_seconds: 300,
    ...(initialType !== 'subagent_3p' ? { max_tool_iterations: 200 } : {}),
    ...(initialType === 'Main' ? { steering_mode: 'one-at-a-time' as const } : {}),
    // Inherit-from-caller toggles default OFF so the corresponding editors
    // (model picker, tools, skills) render by default and the
    // operator makes an explicit choice. Inheritance stays an opt-in via the
    // toggles (available only for native Subagents). Main / external types
    // ignore these flags entirely.
    inherit_model: false,
    inherit_tools: false,
    inherit_skills: false,
  }
}

export function CreateAgentWizard({
  initialType,
  initialCli,
  onSubmit,
  onClose,
  connectedProviders = [],
  registryTools = [],
  skills = [],
  globalPolicies,
}: WizardProps) {
  const closeCreateAgentModal = useUiStore((s) => s.closeCreateAgentModal)
  // Resolve close handler: tests pass `onClose`; production falls back to
  // the store action. Either way the wizard owns the dismiss path so the
  // prop-driven test path can spy on close without touching the store.
  const close = onClose ?? closeCreateAgentModal
  // NOTE: we deliberately do NOT call addToast here on submit error.
  // The parent CreateAgentModal owns the post-mutation lifecycle (success
  // + error toasts, modal close) via useMutation.onSuccess / onError.
  // Firing a toast here would produce a duplicate for the user.

  const [payload, dispatch] = useReducer(reducer, undefined, () => initialPayload(initialType, initialCli))
  const [step, setStep] = React.useState<1 | 2 | 3>(1)
  const [submitting, setSubmitting] = React.useState(false)
  const [submitError, setSubmitError] = React.useState<string | null>(null)
  const errorRef = useRef<HTMLDivElement | null>(null)

  // useFocusRestore captures the trigger element on open so the +Add
  // button that opened the wizard regains focus on close. The Sheet's
  // onOpenAutoFocus fires BEFORE Radix shifts focus, which is when the
  // hook captures document.activeElement.
  const { onOpenAutoFocus } = useFocusRestore(true)

  const setField = React.useCallback(
    <L extends keyof WizardSubmitPayload>(field: L, value: WizardSubmitPayload[L]) => {
      dispatch({ type: 'set', field, value })
      // Editing a field implies the prior error (if any) is stale.
      if (submitError) setSubmitError(null)
    },
    [submitError],
  )

  // Navigate between steps; clears any stale submit error so the user
  // doesn't stare at a banner from a prior attempt while editing.
  const goToStep = React.useCallback(
    (next: 1 | 2 | 3) => {
      setStep(next)
      setSubmitError(null)
    },
    [],
  )

  const isWorker = initialType !== 'Main'
  const isExternal = initialType === 'subagent_3p'

  // Step gating. Step 1 requires name + model + (description if worker).
  // External (subagent_3p) agents also need a selected CLI and a non-empty
  // CLI path before they can leave the Identity step.
  // A native subagent that inherits its model from the caller does not need a
  // model selected (the field is omitted from the create request).
  const modelInherited = initialType === 'Subagent' && payload.inherit_model === true
  const stepNames = isExternal ? STEP_NAMES_EXTERNAL : STEP_NAMES
  const totalSteps = stepNames.length

  const step1Valid = payload.name.trim().length > 0 &&
    (modelInherited || payload.model.trim().length > 0) &&
    (!isWorker || payload.description.trim().length > 0) &&
    (!isExternal || (!!payload.cli && (payload.executor_cli_path?.trim().length ?? 0) > 0))
  // Step 2 requires soul (whitespace-trimmed non-empty).
  const step2Valid = payload.soul.trim().length > 0
  const canAdvance = step === 1 ? step1Valid : step === 2 ? step2Valid : true

  // external-executor-cli-path-detection spec FR-008/FR-018: block ONLY the
  // final Create action on a definitive missing-binary/handshake-failed
  // validate-on-blur result. Gated on `reason` via the shared
  // `isBlockingCliReason` predicate (never a raw `ok` boolean) — the same
  // predicate `AgentProfile`'s edit-form autosave gate uses, so the two
  // surfaces can't drift on which reasons block. `unauthenticated` is a
  // non-blocking warning and MUST NOT block. Any other state (unchecked/
  // pending/errored) is FR-019's "no trap" default: allowed. Does not
  // affect step-1→2 Next gating (only the Create button). Named
  // `cliPathBlocked` (not `cliValidationBlocked`) to avoid colliding with
  // the identically-named export from `useCliPathValidation`.
  const cliPathBlocked =
    isExternal && isBlockingCliReason(payload.executor_cli_validation_reason)

  // B3: when Next is disabled, name the missing required field(s) so the user
  // isn't left staring at a greyed button with no explanation. Mirrors the
  // gating logic above (does NOT change it).
  function missingFieldLabels(): string[] {
    const missing: string[] = []
    if (step === 1) {
      if (payload.name.trim().length === 0) missing.push('Name')
      if (!modelInherited && payload.model.trim().length === 0) missing.push('Model')
      if (isWorker && payload.description.trim().length === 0) missing.push('Description')
      if (isExternal && !payload.cli) missing.push('CLI')
      if (isExternal && (payload.executor_cli_path?.trim().length ?? 0) === 0) missing.push('CLI path')
    } else if (step === 2) {
      if (payload.soul.trim().length === 0) missing.push('Soul')
    }
    return missing
  }
  const missingLabels = canAdvance ? [] : missingFieldLabels()

  async function handleSubmit() {
    // External (subagent_3p) agents need a non-empty CLI path. The step-1
    // gate already prevents reaching the submit button with an empty path,
    // but this check protects the API call if anything bypasses the gate.
    if (isExternal && !payload.executor_cli_path?.trim()) {
      setSubmitError('CLI path is required for an external subagent')
      setSubmitting(false)
      return
    }
    // FR-008/FR-018: the Create button is already disabled while
    // cliPathBlocked is true, but this defends the API call if
    // anything bypasses the gate (e.g. a stale disabled prop during a fast
    // re-render race).
    if (cliPathBlocked) {
      setSubmitError(`CLI path failed validation (${payload.executor_cli_validation_reason}) — fix the path before creating.`)
      setSubmitting(false)
      return
    }

    setSubmitting(true)
    setSubmitError(null)
    try {
      // Parent owns close + success toast via useMutation.onSuccess.
      // We only throw the error so the inline error path can render.
      await onSubmit(payload)
    } catch (err) {
      // Surface the error inline + announce via role=alert (no extra
      // toast here — the parent's onError fires its own toast).
      const message = isApiError(err)
        ? err.userMessage
        : err instanceof Error
          ? err.message
          : 'Failed to create agent'
      setSubmitError(message)
      // Move focus to the error message so screen readers announce it.
      errorRef.current?.focus()
    } finally {
      setSubmitting(false)
    }
  }

  const stepProps = {
    payload,
    setField,
    initialType,
    initialCli,
    connectedProviders,
    registryTools,
    skills,
    globalPolicies,
  }

  return (
    <Sheet open onOpenChange={(open) => !open && close()}>
      <SheetContent
        side="right"
        // Wizard is full-width on phone, capped at 3xl on sm+ (per §13.3).
        className="w-full sm:max-w-3xl flex flex-col gap-0 p-0"
        // useFocusRestore captures the trigger element before Radix
        // shifts focus into the dialog, then restores focus on close.
        onOpenAutoFocus={onOpenAutoFocus}
      >
        <SheetHeader className="px-4 sm:px-8">
          <SheetTitle>+ New agent</SheetTitle>
        </SheetHeader>
        <div className="px-4 sm:px-8 pt-3 pb-4 shrink-0">
          <SheetDescription>
            Configure the agent's identity, personality, and tools.
          </SheetDescription>
          <div className="flex flex-col gap-2 mt-4">
            {/* Type chip (locked). The [x] cancels the wizard per §11 #3. */}
            <div className="flex items-center justify-between gap-2 rounded-md border border-[var(--color-border)] px-3 py-2">
              <div className="flex items-center gap-2 text-sm">
                <span className="text-[var(--color-muted)]">Type:</span>
                <span data-testid="type-chip" className="font-medium">
                  {TYPE_CHIP_LABEL[initialType]}
                </span>
                <span className="text-xs text-[var(--color-muted)]">(locked)</span>
              </div>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                onClick={close}
                aria-label="Cancel wizard"
                className="h-11 w-11"
              >
                <X size={18} />
              </Button>
            </div>
            {isExternal && initialCli && (
              <div className="flex items-center gap-2 rounded-md border border-[var(--color-border)] px-3 py-2 text-sm">
                <span className="text-[var(--color-muted)]">CLI:</span>
                <span data-testid="cli-chip" className="font-medium">
                  {CLI_CHIP_LABEL[initialCli]}
                </span>
                <span className="text-xs text-[var(--color-muted)]">(locked)</span>
              </div>
            )}
          </div>
          {/* Stepper. Per WAI-ARIA APG, a stepper is a list of steps with
              `aria-current="step"` on the active item — not a progressbar
              (which is reserved for fill-style progress toward a single
              completion value). The visible glyph is responsive: phone
              shows the dot (●/○), sm+ shows the numbered label (①/②/③).
              Both render once per step — the original code had two spans
              emitting the same character, which stacked. */}
          <ol
            className="flex items-center gap-2 mt-4 text-sm"
            aria-label={`Wizard progress: step ${step} of ${totalSteps}`}
            data-testid="wizard-stepper"
          >
            {stepNames.map((name, idx) => {
              const n = (idx + 1) as 1 | 2 | 3
              const isActive = step === n
              return (
                <React.Fragment key={name}>
                  <li
                    className={isActive ? 'font-semibold' : 'text-[var(--color-muted)]'}
                    aria-current={isActive ? 'step' : undefined}
                  >
                    <span className="sm:hidden" aria-hidden="true">
                      {isActive ? '●' : '○'}
                    </span>
                    <span className="hidden sm:inline" aria-hidden="true">
                      {isActive ? `● ${name}` : `○ ${name}`}
                    </span>
                    <span className="sr-only">Step {n}: {name}</span>
                  </li>
                  {idx < stepNames.length - 1 && (
                    <li className="text-[var(--color-muted)]" aria-hidden="true">—</li>
                  )}
                </React.Fragment>
              )
            })}
          </ol>
        </div>

        <div
          className="flex-1 overflow-auto px-4 sm:px-8 py-6 space-y-4"
        >
          {submitError && (
            <div
              ref={errorRef}
              tabIndex={-1}
              data-testid="wizard-submit-error"
              className="rounded-md border border-[var(--color-error)]/40 bg-[var(--color-error)]/10 px-3 py-2 text-sm text-[var(--color-error)] focus:outline-none focus:ring-2 focus:ring-[var(--color-error)]"
            >
              <FormError id="wizard-submit-error-text" error={submitError} className="mt-0" />
            </div>
          )}
          {step === 1 && <Step1Identity {...stepProps} />}
          {step === 2 && (
            <>
              <Step2Personality {...stepProps} />
              {/* External (subagent_3p) is a 2-step wizard — there is no
                  step 3 to host the Advanced disclosure, so it mounts at
                  the end of step 2 instead. `<Advanced>` renders its own
                  slim variant for subagent_3p (timeout + rate limits only —
                  see wizard/Advanced.tsx), so this is safe to always mount
                  when isExternal without duplicating the full-agent knobs. */}
              {isExternal && <Advanced {...stepProps} />}
            </>
          )}
          {step === 3 && (
            <>
              <Step3Tools {...stepProps} />
              {/* Advanced disclosure mounts on Step 3 only (so the stepper
                  stays 3 steps), but the field edits apply to the same
                  payload — independent of which step is visible. */}
              <Advanced {...stepProps} />
            </>
          )}
        </div>

        {/* Footer. flex-col-reverse on phone (primary CTA on top), sm+ side-by-side. */}
        <SheetFooter className="px-4 sm:px-8 py-4 border-t border-[var(--color-border)] flex flex-col-reverse sm:flex-row sm:justify-end sm:space-x-2">
          {missingLabels.length > 0 && (
            <p
              role="alert"
              data-testid="wizard-advance-hint"
              className="text-xs text-[var(--color-muted)] self-center sm:mr-auto sm:order-first"
            >
              Missing: {missingLabels.join(', ')}
            </p>
          )}
          {missingLabels.length === 0 && cliPathBlocked && (
            <p
              role="alert"
              data-testid="wizard-cli-validation-blocked"
              className="text-xs text-[var(--color-error)] self-center sm:mr-auto sm:order-first"
            >
              CLI path failed validation ({payload.executor_cli_validation_reason}) — fix the path on step ① before creating.
            </p>
          )}
          <Button
            type="button"
            variant="ghost"
            onClick={close}
            disabled={submitting}
            className="h-11"
          >
            Cancel
          </Button>
          {step > 1 && (
            <Button
              type="button"
              variant="outline"
              onClick={() => goToStep((step - 1) as 1 | 2 | 3)}
              disabled={submitting}
              data-testid="wizard-back"
              className="h-11"
            >
              ← Back
            </Button>
          )}
          {step < totalSteps ? (
            <Button
              type="button"
              onClick={() => goToStep((step + 1) as 1 | 2 | 3)}
              disabled={!canAdvance}
              data-testid={`wizard-next-${step}`}
              className="h-11"
            >
              Next →
            </Button>
          ) : (
            <Button
              type="button"
              onClick={handleSubmit}
              disabled={!canAdvance || submitting || cliPathBlocked}
              data-testid="wizard-create"
              className="h-11"
            >
              {submitting ? 'Creating…' : 'Create agent'}
            </Button>
          )}
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

export default CreateAgentWizard
