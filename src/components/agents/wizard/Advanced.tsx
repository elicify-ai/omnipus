// Advanced — collapsible disclosure rendered ABOVE the footer (NOT a 4th
// step in the stepper). Houses the wire-format fields that the operator
// rarely touches on first-create: model_params, sandbox_profile,
// shell_policy, rate_limits, delegation_policy, timeout_seconds,
// max_tool_iterations, steering_mode, and the subagent_3p executor block.
//
// All fields are type-branched:
//   Main + Subagent: model_params, sandbox_profile, shell_policy,
//                     rate_limits, delegation_policy (Main only),
//                     timeout_seconds, max_tool_iterations,
//                     steering_mode (Main only)
//   subagent_3p:     executor block (cli_path, env_overrides, cli_args)
//                     — External agents are CLI-driven, so model_params,
//                     sandbox_profile, shell_policy, rate_limits,
//                     timeout_seconds, max_tool_iterations are all
//                     rejected 400 on the wire (per
//                     `ExecutorConfig` schema note: the CLI manages its
//                     own isolation, auth, retries).
//
// Implementation lifts from `AgentProfile.tsx` where possible (rate-limit
// inputs, sandbox profile radio) and reuses the executor inputs from
// `<Step1Identity>` so the disclosure stays in sync with the Step 1 editor.

import { Link } from '@tanstack/react-router'
import { SmartSelect } from '@/components/ui/smart-select'
import { Input } from '@/components/ui/input'
import { AdvancedDisclosure } from '@/components/shared/AdvancedDisclosure'
import { SandboxProfileSelector } from '../SandboxProfileSelector'
import { ShellDenyPatternsEditor } from '../ShellDenyPatternsEditor'
import { ExecutorInputs } from './Step1Identity'
import type { SandboxProfile } from '@/lib/api'
import type { AdvancedProps } from './types'

export function Advanced({
  payload,
  setField,
  initialType,
  initialCli,
}: AdvancedProps) {
  const isExternal = initialType === 'subagent_3p'
  const isMain = initialType === 'Main'

  return (
    <AdvancedDisclosure
      title="Advanced"
      summary="Model parameters, sandbox, rate limits, and runtime knobs"
    >
      <div className="space-y-5">
        {!isExternal && (
          <MainAdvancedFields
            payload={payload}
            setField={setField}
            isMain={isMain}
          />
        )}
        {isExternal && (
          <div className="space-y-2">
            <p className="text-xs text-[var(--color-muted)]">
              External CLI runner manages its own isolation, auth, and retries.
              Configure the runner below.
            </p>
            <ExecutorInputs
              payload={payload}
              setField={setField}
              lockedCli={initialCli}
            />
          </div>
        )}
      </div>
    </AdvancedDisclosure>
  )
}

// ── Main + Subagent fields ───────────────────────────────────────────────────

interface MainAdvancedFieldsProps {
  payload: AdvancedProps['payload']
  setField: AdvancedProps['setField']
  isMain: boolean
}

function MainAdvancedFields({ payload, setField, isMain }: MainAdvancedFieldsProps) {
  const modelParams = payload.model_params ?? {}
  const rateLimits = payload.rate_limits ?? {}

  function setModelParam<K extends 'temperature' | 'max_tokens' | 'top_p'>(
    key: K,
    value: number | undefined,
  ) {
    const next = { ...modelParams }
    if (value === undefined || Number.isNaN(value)) {
      delete (next as Record<string, unknown>)[key]
    } else {
      next[key] = value
    }
    setField('model_params', next)
  }

  function setRateLimit<K extends keyof NonNullable<AdvancedProps['payload']['rate_limits']>>(
    key: K,
    value: NonNullable<AdvancedProps['payload']['rate_limits']>[K] | undefined,
  ) {
    const next = { ...rateLimits }
    if (value === undefined) {
      delete (next as Record<string, unknown>)[key]
    } else {
      (next as Record<string, unknown>)[key] = value
    }
    setField('rate_limits', next)
  }

  return (
    <>
      {/* Model parameters */}
      <div className="space-y-2">
        <p className="text-xs font-medium text-[var(--color-secondary)]">Model parameters</p>
        <div className="space-y-2">
          <RangeRow
            label="Temperature"
            caption="Sampling temperature (0.0 – 2.0). Lower = more deterministic. Default 0.7."
            value={modelParams.temperature}
            min={0}
            max={2}
            step={0.1}
            onChange={(v) => setModelParam('temperature', v)}
          />
          <NumberRow
            label="Max tokens"
            caption="Maximum tokens to generate per turn. Default 2048."
            value={modelParams.max_tokens}
            min={1}
            step={1}
            onChange={(v) => setModelParam('max_tokens', v)}
          />
          <RangeRow
            label="Top P"
            caption="Nucleus sampling probability mass. 1.0 disables it. Default 1.0."
            value={modelParams.top_p}
            min={0}
            max={1}
            step={0.05}
            onChange={(v) => setModelParam('top_p', v)}
          />
        </div>
      </div>

      {/* Sandbox profile */}
      <div className="space-y-2">
        <p className="text-xs font-medium text-[var(--color-secondary)]">Sandbox profile</p>
        <p className="text-[11px] text-[var(--color-muted)]">
          Kernel-enforced file + network boundary. &quot;Use global default&quot; inherits the global Security config.
        </p>
        <SandboxProfileSelector
          value={payload.sandbox_profile as unknown as SandboxProfile | undefined}
          agentName={payload.name || 'this agent'}
          godModeAvailable
          godModeOptedIn
          onChange={(profile) =>
            // SandboxProfile includes 'none' which is UI-only — strip it
            // before writing back to the wire-shaped payload (which
            // excludes 'none').
            setField(
              'sandbox_profile',
              profile === 'none' ? undefined : profile,
            )
          }
        />
      </div>

      {/* Shell deny patterns */}
      <div className="space-y-2">
        <p className="text-xs font-medium text-[var(--color-secondary)]">Shell deny patterns</p>
        <ShellDenyPatternsEditor
          value={payload.shell_policy?.deny ?? []}
          onChange={(deny) => setField('shell_policy', deny.length > 0 ? { deny } : undefined)}
        />
      </div>

      {/* Rate limits */}
      <div className="space-y-2">
        <p className="text-xs font-medium text-[var(--color-secondary)]">Rate limits</p>
        <label className="flex items-center gap-2 text-xs text-[var(--color-secondary)]">
          <input
            type="checkbox"
            checked={rateLimits.use_global_defaults ?? true}
            onChange={(e) =>
              setRateLimit('use_global_defaults', e.target.checked ? true : undefined)
            }
            className="accent-[var(--color-accent)]"
          />
          <span>Use global defaults</span>
        </label>
        {!rateLimits.use_global_defaults && (
          <div className="space-y-1.5">
            <NumberRow
              label="LLM calls / hour"
              caption="Maximum LLM API calls per hour for this agent. Empty = no cap."
              value={rateLimits.max_llm_calls_per_hour}
              min={0}
              onChange={(v) => setRateLimit('max_llm_calls_per_hour', v)}
            />
            <NumberRow
              label="Tool calls / minute"
              caption="Maximum tool calls per minute for this agent. Empty = no cap."
              value={rateLimits.max_tool_calls_per_minute}
              min={0}
              onChange={(v) => setRateLimit('max_tool_calls_per_minute', v)}
            />
            <NumberRow
              label="Max cost / day ($)"
              caption="Maximum USD cost per day. Empty = no cap."
              value={rateLimits.max_cost_per_day}
              min={0}
              step={0.01}
              onChange={(v) => setRateLimit('max_cost_per_day', v)}
            />
          </div>
        )}
      </div>

      {/* Runtime knobs */}
      <div className="space-y-2">
        <p className="text-xs font-medium text-[var(--color-secondary)]">Runtime</p>
        <div className="space-y-1.5">
          <NumberRow
            label="Timeout (seconds)"
            caption="Maximum seconds a single agent turn may run. Default 300."
            value={payload.timeout_seconds}
            min={1}
            onChange={(v) => setField('timeout_seconds', v)}
          />
          <NumberRow
            label="Max tool iterations"
            caption="Maximum number of tool calls allowed per turn. Default 50."
            value={payload.max_tool_iterations}
            min={1}
            onChange={(v) => setField('max_tool_iterations', v)}
          />
          {isMain && (
            <div className="space-y-1">
              <p className="text-[11px] text-[var(--color-muted)]">Steering mode</p>
              <SmartSelect
                value={payload.steering_mode ?? 'one-at-a-time'}
                onValueChange={(v) =>
                  setField(
                    'steering_mode',
                    v as 'one-at-a-time' | 'queue-and-process',
                  )
                }
                items={[
                  { value: 'one-at-a-time', label: 'One at a time' },
                  { value: 'queue-and-process', label: 'Queue and process' },
                ]}
              />
            </div>
          )}
          {isMain && (
            <div className="space-y-1 pt-2 border-t border-[var(--color-border)]">
              <p className="text-[11px] text-[var(--color-muted)]">Delegation policy</p>
              <Link
                to="/agents/trust"
                className="text-xs text-[var(--color-accent)] hover:underline"
              >
                Configure delegation policy →
              </Link>
            </div>
          )}
        </div>
      </div>
    </>
  )
}

// ── Helpers ──────────────────────────────────────────────────────────────────

interface RangeRowProps {
  label: string
  caption: string
  value: number | undefined
  min: number
  max: number
  step: number
  onChange: (next: number | undefined) => void
}

function RangeRow({ label, caption, value, min, max, step, onChange }: RangeRowProps) {
  const display = value ?? defaultFor(label)
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between">
        <label className="text-xs text-[var(--color-secondary)]">{label}</label>
        <span className="text-[11px] font-mono text-[var(--color-muted)]">{display}</span>
      </div>
      <input
        type="range"
        min={min}
        max={max}
        step={step}
        value={display}
        onChange={(e) => onChange(Number(e.target.value))}
        className="w-full accent-[var(--color-accent)]"
        aria-label={label}
        title={caption}
      />
      <p className="text-[10px] text-[var(--color-muted)]">{caption}</p>
    </div>
  )
}

interface NumberRowProps {
  label: string
  caption: string
  value: number | undefined
  min?: number
  step?: number
  onChange: (next: number | undefined) => void
}

function NumberRow({ label, caption, value, min, step, onChange }: NumberRowProps) {
  return (
    <div className="space-y-1">
      <label className="text-[11px] text-[var(--color-secondary)]">{label}</label>
      <Input
        type="number"
        min={min}
        step={step}
        value={value ?? ''}
        onChange={(e) => {
          const raw = e.target.value
          if (raw === '') {
            onChange(undefined)
            return
          }
          const n = Number(raw)
          if (!Number.isFinite(n)) return
          onChange(n)
        }}
        placeholder="—"
        className="h-7 text-xs"
      />
      <p className="text-[10px] text-[var(--color-muted)]">{caption}</p>
    </div>
  )
}

// Sensible slider defaults — matches the spec §3.1 column "default" for
// each field. The wizard only sends a value to the wire when the user
// explicitly moves the slider; this default is for the slider's idle
// position, never for the payload.
function defaultFor(label: string): number {
  switch (label) {
    case 'Temperature':
      return 0.7
    case 'Top P':
      return 1
    default:
      return 0
  }
}

export default Advanced