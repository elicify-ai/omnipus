// InheritToggle — "inherit from caller" switch for native-subagent wizard
// fields (UAT agent-form fix 4a).
//
// A native (in-process) subagent is a delegation-only worker whose Model /
// Tools / Skills default to being inherited from the caller. This
// toggle exposes that choice in the creation wizard: ON = inherit (editor
// hidden, field omitted from the create request); OFF = override (editor
// revealed, explicit value sent).

import { ArrowsLeftRight } from '@phosphor-icons/react'

export interface InheritToggleProps {
  /** Field label shown next to the switch (e.g. "Model", "Tools"). */
  label: string
  /** Current inherit state — true = inherit from caller. */
  inherit: boolean
  onChange: (inherit: boolean) => void
  /** Stable test id for the switch input. */
  testId: string
}

export function InheritToggle({ label, inherit, onChange, testId }: InheritToggleProps) {
  return (
    <label className="flex items-center justify-between gap-3 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-2 cursor-pointer">
      <span className="flex items-center gap-2 min-w-0">
        <ArrowsLeftRight size={14} className="shrink-0 text-[var(--color-muted)]" aria-hidden="true" />
        <span className="text-sm font-medium text-[var(--color-secondary)]">{label}</span>
        <span className="text-[11px] text-[var(--color-muted)]">
          {inherit ? 'Inherited from caller' : 'Overridden'}
        </span>
      </span>
      <input tabIndex={0}
        type="checkbox"
        role="switch"
        checked={inherit}
        onChange={(e) => onChange(e.target.checked)}
        aria-label={`Inherit ${label} from caller`}
        data-testid={testId}
        className="shrink-0 accent-[var(--color-accent)]"
      />
    </label>
  )
}

export default InheritToggle
