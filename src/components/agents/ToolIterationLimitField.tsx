// ToolIterationLimitField — the per-agent "Max tool calls per turn" control
// (issue #904, docs/internal/specs/tool-iteration-limit-spec.md, FR-016).
//
// One global limit (Settings → Performance) caps every agent; an agent's own
// value may only LOWER it. The rule is computed by the server alone: this
// component renders the server's effective value, its source and the
// override-ignored flag verbatim and never derives them itself (FR-003).
// It also never prints a literal default — the global comes from
// GET /performance, and without it the copy simply omits the number (FR-004).
//
// Used by the agent profile's Advanced tab (every agent type, subagent_3p
// included — D14) and by the create wizard's Advanced step (native and
// external variants). The caller owns persistence: `onChange(number)` sets an
// own value, `onChange(null)` means "no own value" (the profile sends a raw
// JSON null — D9; the wizard omits the key).

import { useRef, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Field } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  MAX_TOOL_ITERATIONS_MAX,
  MAX_TOOL_ITERATIONS_MIN,
  parseMaxToolIterations,
} from '@/components/settings/MaxToolIterationsCard'
import type { Agent } from '@/lib/api'

// Contract bound for every writable limit field (AgentUpdateRequest /
// AgentCreateRequest* `max_tool_iterations`: minimum 1, maximum 1000).
const BOUND_MESSAGE = `Enter a whole number from ${MAX_TOOL_ITERATIONS_MIN} to ${MAX_TOOL_ITERATIONS_MAX}.`

/** Server-computed state of an existing agent's limit (absent in the create wizard). */
export interface ToolIterationLimitServerState {
  effective: Agent['max_tool_iterations']
  source: Agent['max_tool_iterations_source']
  overrideIgnored: Agent['max_tool_iterations_override_ignored']
  storedOverride?: Agent['max_tool_iterations_override']
}

export interface ToolIterationLimitFieldProps {
  /** The own value currently shown: a number, or null when the agent has none. */
  value: number | null
  /** Commit a new own value (null = no own value / use the global limit). */
  onChange: (next: number | null) => void
  /** Called on every keystroke, before any commit (lets an autosaving form mark itself dirty). */
  onEdit?: () => void
  /** The global limit in force (GET /performance); undefined while unknown. */
  globalLimit?: number
  /** Server-computed state; omitted when there is no agent yet (create wizard). */
  server?: ToolIterationLimitServerState
  /** Server refusal to show inline (D10 message, bound message). */
  serverError?: string | null
  /**
   * What an emptied input means. 'restore' (autosaving profile): nothing is
   * committed while typing and blur restores the last value — clearing an
   * own value is the explicit "Use global limit" action. 'clear' (wizard):
   * empty commits null immediately.
   */
  emptyBehavior?: 'restore' | 'clear'
  disabled?: boolean
  testId?: string
}

function sourceLine(server: ToolIterationLimitServerState | undefined, globalLimit: number | undefined): string | null {
  if (!server) {
    return globalLimit !== undefined
      ? `Empty uses the global limit (${globalLimit})`
      : 'Empty uses the global limit'
  }
  if (server.overrideIgnored) {
    // source is "global" here, so the server's effective value IS the global.
    return `Own value ${server.storedOverride} is above the global limit (${server.effective}) and has no effect`
  }
  if (server.source === 'agent') {
    return globalLimit !== undefined
      ? `Lowered for this agent: ${server.effective} (global limit ${globalLimit})`
      : `Lowered for this agent: ${server.effective}`
  }
  return `Using the global limit (${server.effective})`
}

function placeholderFor(server: ToolIterationLimitServerState | undefined, globalLimit: number | undefined): string {
  if (globalLimit !== undefined) return `Global limit (${globalLimit})`
  // With source "global" the server's effective value is the global limit.
  if (server && server.source === 'global') return `Global limit (${server.effective})`
  return 'Global limit'
}

export function ToolIterationLimitField({
  value,
  onChange,
  onEdit,
  globalLimit,
  server,
  serverError,
  emptyBehavior = 'restore',
  disabled = false,
  testId = 'agent-max-tool-calls-input',
}: ToolIterationLimitFieldProps) {
  const inputRef = useRef<HTMLInputElement>(null)
  const committed = value === null ? '' : String(value)
  // Draft string absorbs in-progress typing so an emptied or invalid input
  // never commits (the zero-clobber P0 of 2026-07-03). Re-synced mid-render
  // when the committed value changes from outside (hydration, reset).
  const [draft, setDraft] = useState(committed)
  const [prevCommitted, setPrevCommitted] = useState(committed)
  if (committed !== prevCommitted) {
    setPrevCommitted(committed)
    setDraft(committed)
  }
  const [boundError, setBoundError] = useState<string | null>(null)

  function handleChange(raw: string) {
    onEdit?.()
    setDraft(raw)
    if (raw.trim() === '') {
      setBoundError(null)
      if (emptyBehavior === 'clear') onChange(null)
      return
    }
    const parsed = parseMaxToolIterations(raw)
    if (parsed === null) {
      setBoundError(BOUND_MESSAGE)
      return
    }
    setBoundError(null)
    if (parsed !== value) onChange(parsed)
  }

  function handleBlur() {
    if (draft.trim() === '' && emptyBehavior === 'restore') setDraft(committed)
  }

  function handleReset() {
    setBoundError(null)
    setDraft('')
    onChange(null)
    inputRef.current?.focus()
  }

  const line = sourceLine(server, globalLimit)
  const error = boundError ?? serverError ?? null

  return (
    <Field
      label="Max tool calls per turn"
      description={
        <>
          <span className="block">
            Per single turn (one message, task, or heartbeat run) — the turn pauses at the limit and can be
            continued. An agent's own value can only lower the global limit.
          </span>
          {line && (
            <span className="block text-[var(--color-secondary)]" data-testid="tool-iteration-limit-source">
              {line}
            </span>
          )}
        </>
      }
      error={error}
    >
      {(controlProps) => (
        <div className="flex items-center gap-[var(--space-2)]">
          <Input
            {...controlProps}
            ref={inputRef}
            type="number"
            inputMode="numeric"
            min={MAX_TOOL_ITERATIONS_MIN}
            max={MAX_TOOL_ITERATIONS_MAX}
            step={1}
            data-testid={testId}
            value={draft}
            placeholder={placeholderFor(server, globalLimit)}
            onChange={(e) => handleChange(e.target.value)}
            onBlur={handleBlur}
            disabled={disabled}
            className="h-8 w-40 text-[length:var(--type-utility-xs-size)]"
          />
          {value !== null && !disabled && (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              data-testid="tool-iteration-limit-reset"
              onClick={handleReset}
            >
              Use global limit
            </Button>
          )}
        </div>
      )}
    </Field>
  )
}

export default ToolIterationLimitField
