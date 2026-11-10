import { ArrowRight, CaretDown, CaretRight, Folder } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import type { Agent } from '@/lib/api'
import { cn } from '@/lib/utils'
import { AgentMark } from '@/components/agents/AgentMark'

const COARSE_TARGET =
  'pointer-coarse:min-h-[var(--target-touch-minimum)] pointer-coarse:min-w-[var(--target-touch-minimum)]'

export function WorkspaceHeader({ name, isCollapsed, onToggle, panelId, onSwitch, isHighlighted }: {
  name: string
  isCollapsed: boolean
  onToggle: () => void
  panelId: string
  /** Omitted for Unfiled — there is no workspace to switch to. Sibling of the toggle, never nested. */
  onSwitch?: () => void
  isHighlighted?: boolean
}) {
  return (
    <div className={cn('flex items-center w-full rounded-md', isHighlighted && 'bg-[var(--color-surface-2)]')}>
      <Button
        variant="ghost"
        onClick={onToggle}
        className={cn('h-auto min-w-0 flex-1 justify-start gap-[var(--space-1)] px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] uppercase tracking-wider hover:bg-transparent hover:text-[var(--color-secondary)]', COARSE_TARGET)}
        aria-expanded={!isCollapsed}
        aria-controls={panelId}
      >
        {isCollapsed ? <CaretRight size={10} className="shrink-0" /> : <CaretDown size={10} className="shrink-0" />}
        <Folder size={14} className="shrink-0" />
        <span className="flex-1 text-left truncate">{name}</span>
        {isHighlighted && (
          <span className="shrink-0 rounded border border-[var(--color-border)] px-[var(--space-1)] text-[length:var(--type-caption-size)] font-[var(--font-weight-regular)] normal-case tracking-[var(--font-letter-spacing-normal)] text-[var(--color-muted)]" aria-hidden="true">↵</span>
        )}
      </Button>
      {onSwitch && (
        <IconButton
          onClick={onSwitch}
          data-testid="workspace-switch-arrow"
          aria-label={`Switch to workspace ${name}`}
          title={`Switch to workspace ${name}`}
          className={cn('h-auto w-auto shrink-0 mr-[var(--space-2)] rounded p-[var(--space-1)] text-[var(--color-accent)] opacity-80 hover:opacity-100 hover:bg-transparent', COARSE_TARGET)}
        >
          <ArrowRight size={13} />
        </IconButton>
      )}
    </div>
  )
}

export function AgentHeader({ agent, name, isCollapsed, onToggle, panelId }: {
  agent: Agent | undefined
  name: string
  isCollapsed: boolean
  onToggle: () => void
  panelId: string
}) {
  return (
    <Button
      variant="ghost"
      onClick={onToggle}
      className={cn('h-auto w-full justify-start gap-[var(--space-1)] px-[var(--space-2)] py-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)]', COARSE_TARGET)}
      aria-expanded={!isCollapsed}
      aria-controls={panelId}
    >
      {isCollapsed ? <CaretRight size={9} className="shrink-0" /> : <CaretDown size={9} className="shrink-0" />}
      <AgentMark agent={agent} name={name} size={18} />
      <span className="flex-1 text-left truncate">{name}</span>
    </Button>
  )
}
