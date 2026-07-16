import { useMemo, useRef, useState } from 'react'
import { ArrowBendUpRight } from '@phosphor-icons/react'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { validateConnection, type TeamEditState, type TeamNodeModel } from './teamGraphModel'

export interface AgentDelegatePickerProps {
  /** The agent this button delegates FROM (the node it is rendered on). */
  source: TeamNodeModel
  /** Every node currently on the canvas — filtered down to valid targets. */
  nodes: readonly TeamNodeModel[]
  editState: TeamEditState
  workerIds: ReadonlySet<string>
  /** Create the from→to edge. Callers pass the SAME handler wired to React
   *  Flow's `onConnect`, so a keyboard-created edge runs through identical
   *  validation + mutation as a drag-created one. */
  onDelegate: (from: string, to: string) => void
}

/**
 * Keyboard-operable equivalent of "drag the gold Handle onto another node"
 * (WCAG 2.1.1 Keyboard / 2.5.7 dragging-movements — the canvas has no
 * keyboard path to CREATE a delegation edge, only to select/delete an
 * existing one via the edge chip). A small "Delegate…" button opens a menu of
 * every OTHER agent on the workspace that `validateConnection` currently
 * allows as a target for this source — the EXACT SAME predicate
 * `isValidConnection`/`handleConnect` use for the drag gesture, so the
 * keyboard path can never accept an edge the drag path would reject (or vice
 * versa). Selecting a target calls `onDelegate`, which the caller wires to
 * the identical `onConnect` handler the canvas drag uses — no duplicated
 * mutation logic.
 */
export function AgentDelegatePicker({
  source,
  nodes,
  editState,
  workerIds,
  onDelegate,
}: AgentDelegatePickerProps) {
  const [open, setOpen] = useState(false)
  const triggerRef = useRef<HTMLButtonElement>(null)

  const candidates = useMemo(
    () =>
      nodes.filter(
        (n) =>
          n.id !== source.id &&
          validateConnection(source.id, n.id, editState, workerIds) === null,
      ),
    [nodes, source.id, editState, workerIds],
  )

  return (
    <DropdownMenu open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger asChild>
        <button
          ref={triggerRef}
          type="button"
          data-node-action="delegate"
          data-testid={`team-node-delegate-${source.id}`}
          aria-label={`Delegate from ${source.name} to another agent`}
          title="Delegate to another agent — keyboard equivalent of dragging the gold connection dot"
          className="nodrag shrink-0 rounded p-1 text-[var(--color-muted)] hover:bg-[var(--color-accent)]/15 hover:text-[var(--color-accent)]"
          onClick={(e) => e.stopPropagation()}
        >
          <ArrowBendUpRight size={12} weight="bold" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="end"
        className="nodrag nopan w-56"
        onCloseAutoFocus={(e) => {
          // Suppress Radix's default restore-to-last-focused-element behavior
          // (which can land on `<body>` when the trigger has unmounted or
          // React Flow's own focus tracking interferes), then explicitly put
          // focus back on the trigger button so a keyboard user keeps their
          // place on the node they were delegating from instead of losing
          // focus entirely.
          e.preventDefault()
          triggerRef.current?.focus()
        }}
      >
        <DropdownMenuLabel>Delegate to…</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {candidates.length === 0 ? (
          <p className="px-2 py-1.5 text-xs text-[var(--color-muted)]">
            No eligible agents — every other team member already has an edge from{' '}
            {source.name}, or the team has only this one agent.
          </p>
        ) : (
          candidates.map((n) => (
            <DropdownMenuItem
              key={n.id}
              data-testid={`team-node-delegate-target-${source.id}-${n.id}`}
              onSelect={() => onDelegate(source.id, n.id)}
              className="cursor-pointer"
            >
              {n.name}
              <span className="ml-auto text-[10px] text-[var(--color-muted)]">{n.role}</span>
            </DropdownMenuItem>
          ))
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
