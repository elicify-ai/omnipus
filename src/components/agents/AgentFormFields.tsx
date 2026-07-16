import { Scroll, Microphone, UploadSimple } from '@phosphor-icons/react'
import { Textarea } from '@/components/ui/textarea'
import { Separator } from '@/components/ui/separator'
import { SmartSelect } from '@/components/ui/smart-select'
import { VoiceProviderSub } from './voice-provider-sub'
import { useUiStore } from '@/store/ui'
import { AVATAR_COLORS, AVATAR_COLORS_BY_NAME } from '@/lib/constants'
import { ICON_OPTIONS, getIconComponent, type IconName } from '@/lib/agentIcons'

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
  /**
   * Per-agent persona voice identifier (e.g. TTS voice name or voice model ID).
   * The picker itself (`VoiceProviderSub`, rendered below) is live today — it
   * queries the configured voice provider and lets the operator select or type
   * a voice value, which is persisted on the agent. Only downstream TTS
   * *playback* (the agent actually speaking with this voice) is deferred to
   * v0.2.0. Optional in both tiers — empty string means "no voice configured"
   * (the wire field is omitted).
   * W6-B4 / G1: this field has been on the wire for a while but had no UI.
   */
  voice: string
  /** Voice setter. Either `onVoiceChange` or `setVoice` may be supplied. */
  onVoiceChange?: (next: string) => void
  /** Voice setter alias — accepts the conventional `setVoice` name. */
  setVoice?: (next: string) => void
  /**
   * Optional upload button — rendered BELOW the textarea (create-dialog
   * parity bug: the wizard used to place its own copy above the box).
   */
  renderUploadButton?: (target: 'soul', onUpload: (content: string) => void) => React.ReactNode
  /**
   * Marks the soul as required-to-submit and renders the minLength hint.
   * Defaults to `isWorker` (the profile's historical behavior: base agents
   * may be saved empty as drafts). The create wizard passes `true` for all
   * types — AgentCreateRequest.soul is `minLength: 1` and the server rejects
   * whitespace-only.
   */
  soulRequired?: boolean
  /** Overrides the soul textarea's data-testid (wizard: "wizard-soul"). */
  soulTestId?: string
  /** Optional data-testid for the voice block wrapper (wizard: "wizard-voice"). */
  voiceWrapperTestId?: string
}

/**
 * UploadMdButton — the shared "Upload .md" affordance for SOUL.md content.
 * One implementation for the profile AND the wizard (the wizard used to carry
 * a drifted duplicate). Reads .md/.markdown/.txt up to 1MB and hands the text
 * to `onUpload`.
 */
export function UploadMdButton({
  onUpload,
  testId,
}: {
  onUpload: (content: string) => void
  testId?: string
}) {
  const addToast = useUiStore((s) => s.addToast)
  return (
    <button
      type="button"
      data-testid={testId}
      onClick={() => {
        const input = document.createElement('input')
        input.type = 'file'
        input.accept = '.md,.markdown,.txt'
        input.onchange = (e) => {
          const file = (e.target as HTMLInputElement).files?.[0]
          if (!file) return
          if (file.size > 1_000_000) {
            addToast({
              message: `File too large (${(file.size / 1_000_000).toFixed(1)}MB). Max 1MB for markdown files.`,
              variant: 'error',
            })
            return
          }
          const reader = new FileReader()
          reader.onload = () => {
            onUpload(reader.result as string)
          }
          reader.onerror = () => {
            addToast({
              message: `Failed to read ${file.name}: ${reader.error?.message ?? 'unknown error'}`,
              variant: 'error',
            })
          }
          reader.readAsText(file)
        }
        input.click()
      }}
      className="h-7 px-2 text-xs rounded border border-[var(--color-border)] text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)] transition-colors flex items-center gap-1"
    >
      <UploadSimple size={12} />
      Upload .md
    </button>
  )
}

/**
 * Renders the "Behavior" form section: SOUL.md (with worker-relabelled
 * "Task prompt" copy when `isWorker`) + Voice. Heartbeat is intentionally
 * NOT included — the modal never shows it, and the profile renders heartbeat
 * only for base agents. Keeping the field set tight lets the modal and
 * profile share this exact block.
 */
export function BehaviorFields({
  isWorker,
  soul,
  onSoulChange,
  setSoul,
  voice,
  onVoiceChange,
  setVoice,
  renderUploadButton,
  soulRequired,
  soulTestId,
  voiceWrapperTestId,
}: BehaviorFieldsProps) {
  const handleSoul = onSoulChange ?? setSoul
  const handleVoice = onVoiceChange ?? setVoice
  const required = soulRequired ?? isWorker
  return (
    <div className="space-y-5">
      {/* SOUL.md / Task prompt — relabelled for workers.
          Workers: now a required task prompt (per the worker form spec).
          Base: empty at create time is also valid (the agent starts in "draft"). */}
      <div className="space-y-2">
        <div className="flex items-center gap-2">
          <Scroll size={13} className="text-[var(--color-accent)]" />
          <p className="text-xs font-medium text-[var(--color-secondary)]">
            {isWorker ? 'Task prompt' : 'Personality & instructions'}
            {required && (
              <span className="text-[var(--color-error)] ml-0.5" aria-label="required">*</span>
            )}
          </p>
        </div>
        <p className="text-xs text-[var(--color-muted)]">
          {isWorker ? (
            <>
              System prompt for the worker&apos;s runner. Composed with any
              caller-supplied task prompt at run time. Stored as{' '}
              <span className="font-mono text-[11px]">SOUL.md</span>.
            </>
          ) : (
            <>
              Defines this agent&apos;s character, expertise, and behavioural
              guidelines. Stored as <span className="font-mono text-[11px]">SOUL.md</span>{' '}
              in the agent workspace.
            </>
          )}
        </p>
        {required && (
          <p className="text-[11px] text-[var(--color-muted)]" data-testid="soul-minlength-hint">
            Required — minimum 1 character; whitespace-only is rejected.
          </p>
        )}
        <Textarea
          data-testid={soulTestId ?? (isWorker ? 'worker-task-prompt' : 'agent-soul')}
          value={soul}
          onChange={(e) => handleSoul?.(e.target.value)}
          placeholder={
            isWorker
              ? "# Task prompt\n\nDefine how this worker should approach its delegated task..."
              : "# Soul\n\nDefine this agent's personality, expertise, and behavioural guidelines..."
          }
          rows={6}
          className="text-xs font-mono resize-none"
          required={required}
          aria-required={required ? 'true' : 'false'}
        />
        {/* Upload sits BELOW the textarea — create/edit parity (the wizard
            used to place its own duplicate above the box). */}
        {renderUploadButton?.('soul', (v) => handleSoul?.(v))}
      </div>

      {/* W6-B4 / G1: Voice — per-agent persona voice identifier (TTS voice name
          or voice model ID). Main agents get the provider-aware widget; workers
          do not have a chat surface and never use TTS, so the field is hidden. */}
      {!isWorker && (
        <>
          <Separator />
          <div className="space-y-2" data-testid={voiceWrapperTestId}>
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
            <VoiceProviderSub
              value={voice ?? ''}
              onChange={(v) => handleVoice?.(v)}
            />
          </div>
        </>
      )}
    </div>
  )
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
            className="w-7 h-7 rounded-full transition-transform hover:scale-110 focus:outline-none"
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
