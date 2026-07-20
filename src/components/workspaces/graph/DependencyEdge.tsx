import { memo } from 'react'
import {
  BaseEdge,
  EdgeLabelRenderer,
  getSmoothStepPath,
  type EdgeProps,
} from '@xyflow/react'
import { X } from '@phosphor-icons/react'

/** Shape of the per-edge `data` GraphView injects (see `edgesWithData`). */
export interface DependencyEdgeData {
  /** Remove this dependency (blocker → blocked). Absent on a read-only canvas. */
  onRemove?: (blockerId: string, blockedId: string) => void
  [key: string]: unknown
}

/**
 * A dependency edge (blocker → blocked) with an inline × delete button at its
 * midpoint. The button is the discoverable, reliable way to remove a
 * dependency: selecting the thin edge + Backspace also works, but only while
 * the canvas holds keyboard focus — not obvious, and the #1 reason "I can't
 * delete a dependency" was reported.
 *
 * All visual styling (status colour, width, opacity, arrow marker, animated
 * flow) is passed straight through from buildTaskGraph via `style`/`markerEnd`,
 * so this renders identically to the built-in `smoothstep` edge it replaces —
 * it only adds the button. GraphView swaps edges to this `type: 'dependency'`
 * only when editing is enabled (an `onRemove` callback is present).
 */
function DependencyEdgeComponent({
  id,
  source,
  target,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  markerEnd,
  style,
  data,
}: EdgeProps) {
  const [edgePath, labelX, labelY] = getSmoothStepPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  })
  const onRemove = (data as DependencyEdgeData | undefined)?.onRemove

  return (
    <>
      <BaseEdge id={id} path={edgePath} markerEnd={markerEnd} style={style} />
      {onRemove && (
        <EdgeLabelRenderer>
          <button
            type="button"
            tabIndex={0}
            // `pointer-events-auto` re-enables clicks (the EdgeLabelRenderer
            // container is pointer-events:none); `nodrag nopan` keeps the click
            // from starting a canvas pan/drag.
            className="nodrag nopan pointer-events-auto absolute flex h-4 w-4 items-center justify-center rounded-full border border-[var(--color-border)] bg-[var(--color-surface-1)] text-[var(--color-muted)] opacity-70 shadow-[0_1px_4px_rgba(0,0,0,0.45)] transition-all hover:scale-110 hover:border-[var(--color-error)] hover:text-[var(--color-error)] hover:opacity-100 focus-visible:border-[var(--color-error)] focus-visible:text-[var(--color-error)] focus-visible:opacity-100"
            style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)` }}
            onClick={(e) => {
              e.stopPropagation()
              onRemove(source, target)
            }}
            aria-label="Remove dependency"
            title="Remove dependency"
          >
            <X size={9} weight="bold" />
          </button>
        </EdgeLabelRenderer>
      )}
    </>
  )
}

export const DependencyEdge = memo(DependencyEdgeComponent)
