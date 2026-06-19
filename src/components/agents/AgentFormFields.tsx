import { Scroll, NotePencil, Microphone } from '@phosphor-icons/react'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Separator } from '@/components/ui/separator'
import { SmartSelect } from '@/components/ui/smart-select'
import { ExecutorSelector } from './ExecutorSelector'
import { FormError } from '@/components/ui/FormError'
import { AVATAR_COLORS, AVATAR_COLORS_BY_NAME } from '@/lib/constants'
import { ICON_OPTIONS, getIconComponent, type IconName } from '@/lib/agentIcons'
import type { ExecutorConfig } from '@/lib/api'

// ── AgentFormFields ──────────────────────────────────────────────────────────
//
// Shared worker-vs-base form split for the create modal AND the agent profile.
// The locked concept (`.preview-doc/agents.html`) defines workers as
// delegation-only labour agents — never a chat target, no heartbeat, never
// the default, optional soul ("task prompt"), and they carry an executor.
// The behavioural split (soul/instructions/heartbeat + execution params) is
// rendered by the same components in both the modal (tab layout) and the
// profile (accordion layout); only the surrounding chrome differs.
//
// Two consumers, two layouts — a single shared `BehaviorFields` component
// keeps the split in ONE place so the modal and profile cannot drift.

export interface BehaviorFieldsProps {
  /** True when rendering the worker (delegation-only labour) form shape. */
  isWorker: boolean
  /** The current SOUL.md content. For workers this is the task prompt. */
  soul: string
  /** Soul setter. Either `onSoulChange` or `setSoul` may be supplied. */
  onSoulChange?: (next: string) => void
  /** Soul setter alias — accepts the conventional `setSoul` name from
   *  consumers that already use that name in their state hooks. */
  setSoul?: (next: string) => void
  /** The current AGENT.md instructions content. Optional in both tiers. */
  instructions: string
  /** Instructions setter. Either `onInstructionsChange` or `setInstructions` may be supplied. */
  onInstructionsChange?: (next: string) => void
  /** Instructions setter alias — accepts the conventional `setInstructions` name. */
  setInstructions?: (next: string) => void
  /**
   * Per-agent persona voice identifier (e.g. TTS voice name or voice model ID).
   * Schema-pinned; not active until v0.2.0 TTS. Optional in both tiers — empty
   * string means "no voice configured" (the wire field is omitted).
   * W6-B4 / G1: this field has been on the wire for a while but had no UI.
   */
  voice: string
  /** Voice setter. Either `onVoiceChange` or `setVoice` may be supplied. */
  onVoiceChange?: (next: string) => void
  /** Voice setter alias — accepts the conventional `setVoice` name. */
  setVoice?: (next: string) => void
  /**
   * Optional upload button — the profile renders one, the modal does not
   * (the modal has no file upload affordance for soul/instructions).
   */
  renderUploadButton?: (target: 'soul' | 'instructions', onUpload: (content: string) => void) => React.ReactNode
}

/**
 * Renders the "Behavior" form section: SOUL.md (with worker-relabelled
 * "Task prompt" copy when `isWorker`) + Additional Instructions. Heartbeat
 * is intentionally NOT included — the modal never shows it, and the profile
 * renders heartbeat only for base agents. Keeping the field set tight lets
 * the modal and profile share this exact block.
 */
export function BehaviorFields({
  isWorker,
  soul,
  onSoulChange,
  setSoul,
  instructions,
  onInstructionsChange,
  setInstructions,
  voice,
  onVoiceChange,
  setVoice,
  renderUploadButton,
}: BehaviorFieldsProps) {
  const handleSoul = onSoulChange ?? setSoul
  const handleInstructions = onInstructionsChange ?? setInstructions
  const handleVoice = onVoiceChange ?? setVoice
  return (
    <div className="space-y-5">
      {/* SOUL.md / Task prompt — relabelled for workers, optional in both tiers.
          Workers: empty is valid (per the locked concept, soul is optional).
          Base: empty at create time is also valid (the agent starts in "draft"). */}
      <div className="space-y-2">
        <div className="flex items-center gap-2">
          <Scroll size={13} className="text-[var(--color-accent)]" />
          <p className="text-xs font-medium text-[var(--color-secondary)]">
            {isWorker ? (
              <>
                Task prompt <span className="text-[var(--color-muted)] font-normal">(optional)</span>
              </>
            ) : (
              'Personality & instructions'
            )}
          </p>
        </div>
        <p className="text-xs text-[var(--color-muted)]">
          {isWorker ? (
            <>
              Optional system prompt for the worker&apos;s runner. Composed with
              any caller-supplied task prompt at run time. Stored as{' '}
              <span className="font-mono text-[11px]">SOUL.md</span>. Leave empty
              to use the executor&apos;s default behaviour.
            </>
          ) : (
            <>
              Defines this agent&apos;s character, expertise, and behavioural
              guidelines. Stored as <span className="font-mono text-[11px]">SOUL.md</span>{' '}
              in the agent workspace.
            </>
          )}
        </p>
        <Textarea
          data-testid={isWorker ? 'worker-task-prompt' : 'agent-soul'}
          value={soul}
          onChange={(e) => handleSoul?.(e.target.value)}
          placeholder={
            isWorker
              ? "# Task prompt (optional)\n\nDefine how this worker should approach its delegated task..."
              : "# Soul\n\nDefine this agent's personality, expertise, and behavioural guidelines..."
          }
          rows={6}
          className="text-xs font-mono resize-none"
          // Workers: explicitly NOT required. Empty is valid.
          required={false}
          aria-required={false}
        />
        {renderUploadButton?.('soul', (v) => handleSoul?.(v))}
      </div>

      <Separator />

      {/* Additional Instructions — same in both tiers, optional. */}
      <div className="space-y-2">
        <div className="flex items-center gap-2">
          <NotePencil size={13} className="text-[var(--color-accent)]" />
          <p className="text-xs font-medium text-[var(--color-secondary)]">
            Additional Instructions
          </p>
        </div>
        <p className="text-xs text-[var(--color-muted)]">
          Extra instructions appended to the {isWorker ? "worker's" : "agent's"} context.
        </p>
        <Textarea
          value={instructions}
          onChange={(e) => handleInstructions?.(e.target.value)}
          placeholder="Add specific instructions, constraints, or domain knowledge..."
          rows={4}
          className="text-xs font-mono resize-none"
        />
        {renderUploadButton?.('instructions', (v) => handleInstructions?.(v))}
      </div>

      <Separator />

      {/* W6-B4 / G1: Voice — per-agent persona voice identifier (TTS voice name
          or voice model ID). Schema-pinned on `Agent.voice` / `AgentUpdateRequest.voice`;
          not active until v0.2.0 TTS, but exposing the field now means operators
          can pre-configure the persona voice for when the feature ships.
          Optional in both tiers — empty string omits the field on the wire. */}
      <div className="space-y-2">
        <div className="flex items-center gap-2">
          <Microphone size={13} className="text-[var(--color-accent)]" />
          <p className="text-xs font-medium text-[var(--color-secondary)]">
            Voice <span className="text-[var(--color-muted)] font-normal">(optional)</span>
          </p>
        </div>
        <p className="text-xs text-[var(--color-muted)]">
          Per-agent persona voice identifier (e.g. <span className="font-mono text-[11px]">alloy</span>).
          Used by v0.2.0 TTS to pick a voice when this agent speaks. Leave empty for the engine default.
        </p>
        <Input
          data-testid={isWorker ? 'worker-voice' : 'agent-voice'}
          value={voice}
          onChange={(e) => handleVoice?.(e.target.value)}
          placeholder="e.g. alloy"
          className="text-xs h-8 font-mono"
          // Voice is optional in both tiers — never required.
          required={false}
          aria-required={false}
        />
      </div>
    </div>
  )
}

// ── Executor runtime selector ────────────────────────────────────────────────

export interface ExecutorSectionProps {
  /** True when rendering the worker form (executor is required). */
  isWorker: boolean
  /** The current executor value (or `undefined` for the default). */
  value: ExecutorConfig | undefined
  onChange: (next: ExecutorConfig | undefined) => void
  /** Validation error to render below the selector (worker: required). */
  error?: string
  /**
   * True when the parent form is for a locked CORE agent (Mia, Jim, Ray, Ava).
   * Wave 6 G9: "core agents run native only" — external-cli is a worker-only
   * affordance, so when this is true the dropdown disables external-cli and
   * the selector clamps programmatic changes back to native. Default false.
   */
  isCoreAgent?: boolean
}

/**
 * Renders the executor/runtime selector with the tier-branched label:
 *   - Worker: "Executor *" + the "Required for workers" helper, with a
 *     validation error slot.
 *   - Base: "Executor" (no helper, no error).
 * Placed here so the create modal AND any future form reusing this
 * component get the same chrome — and so the ExecutorSelector import
 * stays in one chunk (the modal's chunk imports from this module, and
 * the tree-shaker keeps the dependency).
 */
export function ExecutorSection({
  isWorker,
  value,
  onChange,
  error,
  isCoreAgent = false,
}: ExecutorSectionProps) {
  // Wave 6 / A-fix: WCAG 3.3.1/4.1.3 wiring. The `<select>` in
  // ExecutorSelector carries `aria-describedby="executor-error"` and
  // `aria-invalid={!!error}` whenever this section renders an error,
  // so screen readers announce the validation message via the
  // `<FormError role="alert">` rendered below.
  const errorId = 'executor-error'
  return (
    <div className="space-y-1.5 pt-1 border-t border-[var(--color-border)]">
      <p className="text-xs font-medium text-[var(--color-secondary)] pt-1">
        Executor {isWorker && <span className="text-[var(--color-error)]" aria-hidden="true">*</span>}
      </p>
      {isWorker && (
        <p className="text-[11px] text-[var(--color-muted)]">
          Required for workers — pick the runtime that will execute delegated tasks.
        </p>
      )}
      <ExecutorSelector
        value={value}
        onChange={(next) => {
          onChange(next)
          if (next && error) onChange(next)
        }}
        errorId={errorId}
        hasError={!!error}
        isCoreAgent={isCoreAgent}
      />
      <FormError id={errorId} error={error} />
    </div>
  )
}

// ── Modal title + description copy (tier-branched) ───────────────────────────

export interface AgentFormCopy {
  title: string
  description: string
  /** Test id for the modal title heading (used by tier-preset tests). */
  testId: string
  /** Submit-button label shown while the mutation is pending + the final label. */
  submitLabel: string
}

/**
 * Returns the title/description/submit-label copy for the create modal,
 * branched by tier. Single source of truth — both the modal header and any
 * test that asserts on the title go through this helper.
 */
export function getCreateAgentFormCopy(type: 'custom' | 'worker'): AgentFormCopy {
  if (type === 'worker') {
    return {
      title: 'New sub-agent worker',
      description:
        'Configure a delegation-only labour agent. Workers are invoked by other agents — they are not chat targets and never run on a schedule.',
      testId: 'create-worker-modal-title',
      submitLabel: 'Create worker',
    }
  }
  return {
    title: 'New custom agent',
    description: 'Configure a new custom agent with a persona, model, and tools.',
    testId: 'create-custom-modal-title',
    submitLabel: 'Create agent',
  }
}

// ── Avatar color picker (lifted from AgentProfile.tsx:843-866) ─────────────

export interface AvatarColorPickerProps {
  /** Currently selected color (hex). */
  value: string
  /** Called with the chosen color (hex) on click. */
  onChange: (color: string) => void
  /** Optional testid prefix; the full id is `${testidPrefix}-${semanticName}`. */
  testIdPrefix?: string
  /** Optional className for the wrapper. */
  className?: string
}

/**
 * 8-swatch avatar color picker. Uses the brand palette from
 * `src/lib/constants.ts`. Each button's `aria-label` and `title` resolve the
 * semantic name (e.g. "Forge Gold") via `avatarColorName()` so screen
 * readers announce the brand name instead of the raw hex. The selected
 * swatch is highlighted with a double-ring (primary + colour) so the
 * choice is visible against any background.
 */
export function AvatarColorPicker({
  value,
  onChange,
  testIdPrefix = 'avatar-color',
  className,
}: AvatarColorPickerProps) {
  return (
    <div className={className ?? 'flex gap-2'}>
      {AVATAR_COLORS.map((color) => {
        const name = AVATAR_COLORS_BY_NAME[color] ?? color
        const isSelected = value === color
        return (
          <button
            key={color}
            type="button"
            data-testid={`${testIdPrefix}-${name}`}
            onClick={() => onChange(color)}
            className="w-7 h-7 rounded-full transition-transform hover:scale-110 focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)] focus:ring-offset-1 focus:ring-offset-[var(--color-primary)]"
            style={{
              backgroundColor: color,
              boxShadow: isSelected ? `0 0 0 2px var(--color-primary), 0 0 0 4px ${color}` : undefined,
            }}
            aria-label={name}
            aria-pressed={isSelected}
            title={name}
          />
        )
      })}
    </div>
  )
}

// ── Avatar icon picker (lifted from AgentProfile.tsx:869-878) ──────────────

export interface IconPickerProps {
  /** Currently selected icon name. */
  value: IconName
  /** Called with the chosen icon name on change. */
  onChange: (icon: IconName) => void
  /** Optional testid applied to a wrapping div (SmartSelect trigger inherits). */
  triggerTestId?: string
  /** Optional className override. */
  triggerClassName?: string
}

/**
 * Avatar icon picker — wraps the `SmartSelect` primitive with the
 * `ICON_OPTIONS` vocabulary from `src/lib/agentIcons.ts`. The select value
 * is the wire-shape `IconName` (e.g. `"lightbulb"`, `"robot"`).
 */
export function IconPicker({
  value,
  onChange,
  triggerTestId,
  triggerClassName = 'w-48',
}: IconPickerProps) {
  return (
    <div data-testid={triggerTestId}>
      <SmartSelect
        value={value}
        onValueChange={(v) => onChange(v as IconName)}
        triggerClassName={triggerClassName}
        items={ICON_OPTIONS.map(({ name: iconName }) => ({ value: iconName, label: iconName }))}
      />
    </div>
  )
}

// ── Avatar header circle (lifted from AgentProfile.tsx:625-630) ───────────

export interface AvatarHeaderProps {
  /** The hex color for the circle background. */
  color: string | null | undefined
  /** Optional className override. */
  className?: string
}

/**
 * The 12-px circle with the agent's chosen icon, used in slide-over
 * headers. Background is the agent's color; the icon foreground is the
 * primary (deep black) for contrast. Lifts the inline JSX that was
 * duplicated in `AgentProfile.tsx:625-630`.
 */
export function AvatarHeader({ color, className }: AvatarHeaderProps) {
  // The static Robot icon matches the wizard's Step 1 default (icon: 'Robot'
  // per CreateAgentWizard.tsx:initialPayload). When the profile wires this
  // to a dynamic icon, this can become a prop.
  const Icon = getIconComponent('Robot')
  return (
    <div
      className={className ?? 'w-12 h-12 rounded-full flex items-center justify-center shrink-0'}
      style={{ backgroundColor: color ?? 'var(--color-surface-3)' }}
    >
      <Icon size={22} className="text-[var(--color-primary)]" />
    </div>
  )
}
