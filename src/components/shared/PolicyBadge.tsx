import { ShieldCheck, ShieldWarning, Prohibit } from '@phosphor-icons/react'

export type ToolPolicy = 'allow' | 'ask' | 'deny'

const POLICY_CONFIGS: Record<ToolPolicy, { icon: typeof ShieldCheck; label: string; color: string; activeColor: string }> = {
  allow: { icon: ShieldCheck, label: 'Allow', color: 'text-[var(--color-muted)]', activeColor: 'bg-emerald-500/20 text-emerald-400 border-emerald-500/40' },
  ask: { icon: ShieldWarning, label: 'Ask', color: 'text-[var(--color-muted)]', activeColor: 'bg-amber-500/20 text-amber-400 border-amber-500/40' },
  deny: { icon: Prohibit, label: 'Deny', color: 'text-[var(--color-muted)]', activeColor: 'bg-red-500/20 text-red-400 border-red-500/40' },
}

interface PolicyBadgeProps {
  policy: ToolPolicy
  onClick: () => void
  active: boolean
  disabled?: boolean
  /** Native hover tooltip — e.g. why a control is locked by a global override. */
  title?: string
}

export function PolicyBadge({ policy, onClick, active, disabled, title }: PolicyBadgeProps) {
  const cfg = POLICY_CONFIGS[policy]
  const Icon = cfg.icon
  return (
    <button tabIndex={0}
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      aria-pressed={active}
      className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-[10px] font-medium border transition-colors disabled:opacity-40 disabled:cursor-not-allowed ${
        active ? cfg.activeColor : `border-transparent ${cfg.color} hover:bg-[var(--color-surface-2)]`
      }`}
    >
      <Icon size={11} weight="bold" />
      {cfg.label}
    </button>
  )
}
