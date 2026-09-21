import { forwardRef, useEffect, useRef, useState } from 'react'
import { Stack, Trash, X } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { cn } from '@/lib/utils'
import { ALL_MODES, type DelegationMode, type TeamEdgeModel } from './teamGraphModel'

// Mode-chip accents (Sovereign Deep). Shared by the editor + the collapsed label.
export const MODE_CHIP_CLASS: Record<DelegationMode, string> = {
  direct:
    'border-[var(--color-accent)]/40 bg-[var(--color-accent)]/10 text-[var(--color-accent)]',
  task:
    'border-[var(--color-success)]/40 bg-[var(--color-success)]/10 text-[var(--color-success)]',
}

// Hover companion for MODE_CHIP_CLASS's "on" colors: the mode toggle below is
// now a catalogued `Button` (ghost variant), whose own hover treatment
// (surface tint + secondary text) would repaint an ENABLED chip's hover state
// — it previously had none. These pin the chip's own colors back in for
// `:hover` so an enabled chip's appearance is unchanged, matching MODE_CHIP_CLASS 1:1.
const MODE_CHIP_HOVER_CLASS: Record<DelegationMode, string> = {
  direct: 'hover:bg-[var(--color-accent)]/10 hover:text-[var(--color-accent)]',
  task: 'hover:bg-[var(--color-success)]/10 hover:text-[var(--color-success)]',
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

  // Opening the editor (the chip <-> editor swap in WorkspaceTeamGraph's
  // DelegationEdge) moves focus INTO it — onto the first mode chip, the
  // primary control a user opened this editor to reach. Closing restores
  // focus to the chip (see WorkspaceTeamGraph.tsx's DelegationEdge).
  const firstModeRef = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    firstModeRef.current?.focus()
  }, [])

  return (
    <div
      data-testid={`team-edge-editor-${model.from}-${model.to}`}
      className="w-56 rounded-lg border border-[var(--color-accent)]/60 bg-[var(--color-surface-1)] p-[var(--space-2)] shadow-lg"
    >
      <div className="mb-[var(--space-2)] flex items-center justify-between">
        <span className="text-[length:var(--type-caption-size)] font-medium uppercase tracking-wide text-[var(--color-muted)]">
          Delegation modes
        </span>
        <IconButton
          aria-label="Close edge editor"
          className="h-auto w-auto inline-flex items-center justify-center p-0 text-[var(--color-muted)] hover:bg-transparent hover:text-[var(--color-secondary)] pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]"
          onClick={onClose}
        >
          <X size={12} weight="bold" />
        </IconButton>
      </div>
      <div className="flex flex-wrap gap-[var(--space-1)]">
        {ALL_MODES.map((m, i) => {
          const on = model.modes.includes(m)
          const isLastOn = on && model.modes.length === 1
          return (
            <Button
              key={m}
              ref={i === 0 ? firstModeRef : undefined}
              variant="ghost"
              data-testid={`team-edge-mode-${m}`}
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
              onClick={() => {
                // aria-disabled (not the native `disabled` attribute) — a
                // truly `disabled` button drops out of the tab order
                // entirely, making its explanatory title/tooltip ("at least
                // one mode is required") unreachable for keyboard users. It
                // stays focusable; the click itself is a no-op instead.
                if (isLastOn) return
                onToggleMode(model.from, model.to, m)
              }}
              className={cn(
                // h-auto/font-normal/aria-disabled:opacity-100 override
                // Button's own default height, font-medium and its
                // aria-disabled:opacity-50 rule (Button's own automatic
                // disabled-look) — this chip stays full-opacity even
                // aria-disabled (the title tooltip is the only isLastOn cue).
                'h-auto rounded border px-[var(--space-1)] py-[var(--space-0-5)] font-mono text-[length:var(--type-caption-size)] lowercase font-[var(--font-weight-regular)] transition-opacity aria-disabled:opacity-100',
                on
                  ? cn(MODE_CHIP_CLASS[m], MODE_CHIP_HOVER_CLASS[m])
                  : 'border-[var(--color-border)] bg-[var(--color-surface-2)] text-[var(--color-muted)] opacity-60 hover:bg-[var(--color-surface-2)] hover:text-[var(--color-muted)] hover:opacity-100',
              )}
            >
              {MODE_LABEL[m]}
            </Button>
          )
        })}
      </div>
      <div className="mt-[var(--space-2)] flex items-center gap-[var(--space-1)]">
        <Stack size={12} weight="bold" className="text-[var(--color-muted)]" />
        <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">depth</span>
        <input tabIndex={0}
          type="number"
          min={0}
          inputMode="numeric"
          data-testid="team-edge-depth"
          value={depthDraft}
          aria-label="Delegation depth — maximum hops for this edge"
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
          className="h-6 w-14 rounded border border-[var(--color-border)] bg-[var(--color-surface-2)] px-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-secondary)] focus:border-[var(--color-accent)] focus:outline-none"
        />
        <Button
          size="sm"
          variant="ghost"
          data-testid={`team-edge-delete-${model.from}-${model.to}`}
          className="ml-auto h-6 gap-[var(--space-1)] px-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-error)] hover:bg-[var(--color-error)]/10"
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

/**
 * The collapsed edge label: mode chips (+ depth) that opens the editor.
 * Forwards its ref so WorkspaceTeamGraph's DelegationEdge can refocus this
 * exact chip when the inline editor it opens is closed (focus would
 * otherwise drop to <body> once the editor's own DOM node unmounts).
 */
export const EdgeLabelChip = forwardRef<HTMLButtonElement, EdgeLabelChipProps>(function EdgeLabelChip(
  { model, defaultDepth, onClick },
  ref,
) {
  return (
    <Button
      ref={ref}
      variant="ghost"
      aria-label={`Edit delegation ${model.from} to ${model.to}`}
      onClick={onClick}
      className="flex h-auto w-auto cursor-pointer items-center gap-[var(--space-1)] rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-1)] py-[var(--space-0-5)] font-[var(--font-weight-regular)] shadow-sm hover:bg-[var(--color-surface-1)] hover:border-[var(--color-accent)]/50 pointer-coarse:min-h-[44px] pointer-coarse:min-w-[44px]"
    >
      {model.modes.length === 0 ? (
        <span className="text-[length:var(--type-caption-size)] italic text-[var(--color-muted)]">all modes</span>
      ) : (
        model.modes.map((m) => (
          <span
            key={m}
            className={cn(
              'rounded border px-[var(--space-1)] py-0 font-mono text-[length:var(--type-caption-size)] lowercase',
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
      <span className="ml-[var(--space-0-5)] inline-flex items-center gap-[var(--space-0-5)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
        <Stack size={9} weight="bold" />
        {model.depth ?? defaultDepth}
      </span>
    </Button>
  )
})
