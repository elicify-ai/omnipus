// CreateAgentWizard — 3-step + Advanced wizard for creating user-creatable
// agents. W4 of agent-form-requirements delivery.
//
// Split into sub-components per the plan's file-ownership matrix:
//   ./wizard/Step1Identity.tsx   — color, icon, name, description, model
//   ./wizard/Step2Personality.tsx — soul, instructions, heartbeat, voice
//   ./wizard/Step3Tools.tsx      — tools_cfg, skills, fallback_models
//   ./wizard/Advanced.tsx       — model_params, sandbox, shell, etc. (deferred)
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
import type { Provider, Skill } from '@/lib/api/generated/openapi-types'

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
  instructions: string
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
  // Advanced disclosure fields — W5. Each field matches the
  // `AgentCreateRequest` shape so the modal's payloadToCreateRequest
  // can forward them as-is. All optional.
  model_params?: {
    temperature?: number
    max_tokens?: number
    top_p?: number
  }
  // O13: 'off' removed from the per-agent picker — "no sandbox" is the global
  // god-mode switch only.
  sandbox_profile?: 'workspace' | 'workspace+net' | 'host'
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
  delegation_policy?: string
  timeout_seconds?: number
  max_tool_iterations?: number
  steering_mode?: 'one-at-a-time' | 'queue-and-process'
  // ── Inherit-from-caller toggles (UAT agent-form fix 4a) ──────────────
  // UI-only flags (NOT wire fields). A native (in-process) subagent is a
  // delegation-only worker: by default its Model / Tools / Skills / Sandbox
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
  inherit_sandbox?: boolean
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
    instructions: '',
    heartbeat_enabled: false,
    heartbeat_interval: 1800,
    timeout_seconds: 300,
    max_tool_iterations: 50,
    steering_mode: 'one-at-a-time',
    // Inherit-from-caller toggles default OFF so the corresponding editors
    // (model picker, tools, skills, sandbox) render by default and the
    // operator makes an explicit choice. Inheritance stays an opt-in via the
    // toggles (available only for native Subagents). Main / external types
    // ignore these flags entirely.
    inherit_model: false,
    inherit_tools: false,
    inherit_skills: false,
    inherit_sandbox: false,
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
  const step1Valid = payload.name.trim().length > 0 &&
    (modelInherited || payload.model.trim().length > 0) &&
    (!isWorker || payload.description.trim().length > 0) &&
    (!isExternal || (!!payload.cli && (payload.executor_cli_path?.trim().length ?? 0) > 0))
  // Step 2 requires soul (whitespace-trimmed non-empty).
  const step2Valid = payload.soul.trim().length > 0
  const canAdvance = step === 1 ? step1Valid : step === 2 ? step2Valid : true

  async function handleSubmit() {
    // External (subagent_3p) agents need a non-empty CLI path. The step-1
    // gate already prevents reaching the submit button with an empty path,
    // but this check protects the API call if anything bypasses the gate.
    if (isExternal && !payload.executor_cli_path?.trim()) {
      setSubmitError('CLI path is required for an external subagent')
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
        <SheetHeader className="px-4 sm:px-8 pt-5 sm:pt-7 pb-4 sm:pb-5 border-b border-[var(--color-border)] shrink-0">
          <SheetTitle>+ New agent</SheetTitle>
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
            aria-label={`Wizard progress: step ${step} of 3`}
            data-testid="wizard-stepper"
          >
            {STEP_NAMES.map((name, idx) => {
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
                  {idx < STEP_NAMES.length - 1 && (
                    <li className="text-[var(--color-muted)]" aria-hidden="true">—</li>
                  )}
                </React.Fragment>
              )
            })}
          </ol>
        </SheetHeader>

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
          {step === 2 && <Step2Personality {...stepProps} />}
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
          {step < 3 ? (
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
              disabled={!canAdvance || submitting}
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
