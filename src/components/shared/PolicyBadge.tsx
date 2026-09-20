import { ShieldCheck, ShieldWarning, Prohibit } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

export type ToolPolicy = 'allow' | 'ask' | 'deny'

const POLICY_CONFIGS: Record<ToolPolicy, { icon: typeof ShieldCheck; label: string; color: string; activeColor: string }> = {
  allow: { icon: ShieldCheck, label: 'Allow', color: 'text-[var(--color-muted)]', activeColor: 'bg-[var(--color-status-done)]/20 text-[var(--color-status-done)] border-[var(--color-status-done)]/40' },
  ask: { icon: ShieldWarning, label: 'Ask', color: 'text-[var(--color-muted)]', activeColor: 'bg-amber-500/20 text-amber-400 border-amber-500/40' },
  deny: { icon: Prohibit, label: 'Deny', color: 'text-[var(--color-muted)]', activeColor: 'bg-[var(--color-status-failed)]/20 text-[var(--color-status-failed)] border-[var(--color-status-failed)]/40' },
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
    <Button
      type="button"
      variant="ghost"
      onClick={onClick}
      disabled={disabled}
      title={title}
      aria-pressed={active}
      className={cn(
        'h-auto items-center gap-[var(--space-1)] rounded px-[var(--space-2)] py-[var(--space-0-5)] text-[length:var(--type-caption-size)] font-medium border hover:bg-[var(--color-surface-2)]',
        active ? cfg.activeColor : `border-transparent ${cfg.color}`,
      )}
    >
      <Icon size={11} weight="bold" />
      {cfg.label}
    </Button>
  )
}
