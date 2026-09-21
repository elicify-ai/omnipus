// InheritToggle — "inherit from caller" switch for native-subagent wizard
// fields (UAT agent-form fix 4a).
//
// A native (in-process) subagent is a delegation-only worker whose Model /
// Tools / Skills default to being inherited from the caller. This
// toggle exposes that choice in the creation wizard: ON = inherit (editor
// hidden, field omitted from the create request); OFF = override (editor
// revealed, explicit value sent).

import { ArrowsLeftRight } from '@phosphor-icons/react'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'

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
    <Label className="flex items-center justify-between gap-[var(--space-2-5)] rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2-5)] py-[var(--space-2)] cursor-pointer">
      <span className="flex items-center gap-[var(--space-2)] min-w-0">
        <ArrowsLeftRight size={14} className="shrink-0 text-[var(--color-muted)]" aria-hidden="true" />
        <span className="text-[length:var(--type-body-compact-size)] font-medium text-[var(--color-secondary)]">{label}</span>
        <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
          {inherit ? 'Inherited from caller' : 'Overridden'}
        </span>
      </span>
      {/* Was a native <input type="checkbox" role="switch">: role="switch" on
          a plain checkbox is a masquerade (controls/checkbox-as-switch) —
          the accent-color checkbox never actually rendered switch-shaped,
          it only claimed the switch role. Switch is the real primitive:
          same controlled boolean (checked/onCheckedChange ↔
          inherit/onChange), genuine role="switch" + aria-checked from
          Radix, and Radix's Label + Switch pairing already forwards a
          label click to the switch (label-click behavior is unchanged). */}
      <Switch
        checked={inherit}
        onCheckedChange={onChange}
        aria-label={`Inherit ${label} from caller`}
        data-testid={testId}
      />
    </Label>
  )
}

export default InheritToggle
