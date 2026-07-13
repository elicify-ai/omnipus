import { useEffect, useState } from 'react'
import { Stack, Trash, X } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { ALL_MODES, type DelegationMode, type TeamEdgeModel } from './teamGraphModel'

// Mode-chip accents (Sovereign Deep). Shared by the editor + the collapsed label.
export const MODE_CHIP_CLASS: Record<DelegationMode, string> = {
  direct:
    'border-[var(--color-accent)]/40 bg-[var(--color-accent)]/10 text-[var(--color-accent)]',
  task:
    'border-[var(--color-success)]/40 bg-[var(--color-success)]/10 text-[var(--color-success)]',
}

/** Human-readable label for each delegation mode (chip title/tooltip + a11y label). */
export const MODE_LABEL: Record<DelegationMode, string> = {
  direct: 'Direct Delegation',
  task: 'Task Delegation',
}

export interface EdgeModeEditorProps {
  model: TeamEdgeModel
  /** The workspace's currently-resolved depth ceiling — pre-fills/redisplays
   *  a concrete depth whenever the edge's own `model.depth` is unset. */
  defaultDepth: number
  onToggleMode: (from: string, to: string, mode: DelegationMode) => void
  onSetDepth: (from: string, to: string, depth: number | undefined) => void
  onDelete: (from: string, to: string) => void
  onClose: () => void
}

/**
 * The inline edge editor popover: multi-select delegation MODE chips (Direct
 * Delegation / Task Delegation) + a DEPTH stepper + delete. Extracted from the
 * React Flow edge so it is unit-testable without a measured canvas (React Flow
 * only paints edge labels after node measurement, which jsdom doesn't do). The
 * last remaining mode can't be removed (an empty modes array reads as "all
 * allowed" on the backend). Depth always shows a concrete number — there is no
 * "∞"/inherit-blank state.
 */
export function EdgeModeEditor({
  model,
  defaultDepth,
  onToggleMode,
  onSetDepth,
  onDelete,
  onClose,
}: EdgeModeEditorProps) {
  // Controlled depth draft so a partial value (empty field) doesn't fight the
  // committed model value. Always concrete: falls back to defaultDepth when
  // the edge's own depth is unset (e.g. a legacy edge never touched by this UI).
  const [depthDraft, setDepthDraft] = useState<string>(String(model.depth ?? defaultDepth))
  useEffect(() => {
    setDepthDraft(String(model.depth ?? defaultDepth))
  }, [model.depth, defaultDepth])

  return (
    <div
      data-testid={`team-edge-editor-${model.from}-${model.to}`}
      className="w-56 rounded-lg border border-[var(--color-accent)]/60 bg-[var(--color-surface-1)] p-2.5 shadow-lg"
    >
      <div className="mb-2 flex items-center justify-between">
        <span className="text-[10px] font-medium uppercase tracking-wide text-[var(--color-muted)]">
          Delegation modes
        </span>
        <button
          type="button"
          aria-label="Close edge editor"
          className="text-[var(--color-muted)] hover:text-[var(--color-secondary)]"
          onClick={onClose}
        >
          <X size={12} weight="bold" />
        </button>
      </div>
      <div className="flex flex-wrap gap-1">
        {ALL_MODES.map((m) => {
          const on = model.modes.includes(m)
          const isLastOn = on && model.modes.length === 1
          return (
            <button
              key={m}
              type="button"
              data-testid={`team-edge-mode-${m}`}
              disabled={isLastOn}
              aria-pressed={on}
              aria-disabled={isLastOn}
              aria-label={MODE_LABEL[m]}
              title={
                isLastOn
                  ? 'At least one mode is required — an edge with no modes would allow ALL modes.'
                  : on
                    ? `Disable ${MODE_LABEL[m]}`
                    : `Enable ${MODE_LABEL[m]}`
              }
              onClick={() => onToggleMode(model.from, model.to, m)}
              className={cn(
                'rounded border px-1.5 py-0.5 font-mono text-[10px] lowercase transition-opacity',
                on
                  ? MODE_CHIP_CLASS[m]
                  : 'border-[var(--color-border)] bg-[var(--color-surface-2)] text-[var(--color-muted)] opacity-60 hover:opacity-100',
                isLastOn && 'cursor-not-allowed',
              )}
            >
              {MODE_LABEL[m]}
            </button>
          )
        })}
      </div>
      <div className="mt-2 flex items-center gap-1.5">
        <Stack size={12} weight="bold" className="text-[var(--color-muted)]" />
        <span className="text-[10px] text-[var(--color-muted)]">depth</span>
        <input
          type="number"
          min={0}
          inputMode="numeric"
          data-testid="team-edge-depth"
          value={depthDraft}
          title="Max delegation hops for this edge. Edges you haven't changed keep tracking the workspace/global default automatically."
          onChange={(e) => {
            const v = e.target.value
            setDepthDraft(v)
            if (v === '') {
              onSetDepth(model.from, model.to, undefined)
            } else {
              const n = Number(v)
              if (Number.isFinite(n) && n >= 0) {
                onSetDepth(model.from, model.to, Math.floor(n))
              }
            }
          }}
          className="h-6 w-14 rounded border border-[var(--color-border)] bg-[var(--color-surface-2)] px-1.5 text-[11px] text-[var(--color-secondary)] focus:border-[var(--color-accent)] focus:outline-none"
        />
        <Button
          size="sm"
          variant="ghost"
          data-testid={`team-edge-delete-${model.from}-${model.to}`}
          className="ml-auto h-6 gap-1 px-1.5 text-[10px] text-[var(--color-error)] hover:bg-[var(--color-error)]/10"
          onClick={() => onDelete(model.from, model.to)}
        >
          <Trash size={11} weight="bold" /> delete
        </Button>
      </div>
    </div>
  )
}

export interface EdgeLabelChipProps {
  model: TeamEdgeModel
  /** The workspace's currently-resolved depth ceiling — displayed whenever
   *  the edge's own `model.depth` is unset, so the badge always shows a
   *  concrete number. */
  defaultDepth: number
  onClick: () => void
}

/** The collapsed edge label: mode chips (+ depth) that opens the editor. */
export function EdgeLabelChip({ model, defaultDepth, onClick }: EdgeLabelChipProps) {
  return (
    <button
      type="button"
      aria-label={`Edit delegation ${model.from} to ${model.to}`}
      onClick={onClick}
      className="flex cursor-pointer items-center gap-1 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-1.5 py-0.5 shadow-sm hover:border-[var(--color-accent)]/50"
    >
      {model.modes.length === 0 ? (
        <span className="text-[9px] italic text-[var(--color-muted)]">all modes</span>
      ) : (
        model.modes.map((m) => (
          <span
            key={m}
            className={cn(
              'rounded border px-1 py-0 font-mono text-[9px] lowercase',
              MODE_CHIP_CLASS[m],
            )}
          >
            {m}
          </span>
        ))
      )}
      {/* Depth is always a concrete number now — no more conditional "only
          when set" rendering; an edge whose own depth is unset shows the
          workspace's resolved default instead of hiding the badge. */}
      <span className="ml-0.5 inline-flex items-center gap-0.5 text-[9px] text-[var(--color-muted)]">
        <Stack size={9} weight="bold" />
        {model.depth ?? defaultDepth}
      </span>
    </button>
  )
}
