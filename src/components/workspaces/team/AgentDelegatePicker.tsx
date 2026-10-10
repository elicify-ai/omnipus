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
import { IconButton } from '@/components/ui/icon-button'
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
      // NO identity filter here (F6, reverify 60e299a86): a member's ordinary
      // self-edge IS a valid target, so the source must be offered back to
      // itself whenever the SHARED model accepts that self-edge — this is the
      // keyboard path to restore a self-edge that was deleted. Hard-coding
      // `n.id !== source.id` (or any identity rule) here would duplicate the
      // model's rules and drift from them; the single source of truth is
      // `validateConnection`, which already exempts a self-edge from the cycle
      // check and bounds it by membership/duplicate/system-target like any
      // other edge. See teamGraphModel.ts::validateConnection's doc comment.
      nodes.filter(
        (n) =>
          // SD-C17 defense-in-depth: a System agent is never a valid
          // delegation target, even though it cannot appear as a team
          // member (and therefore as a node here) through the supported
          // flow — see teamGraphModel.ts's validateConnection doc comment.
          validateConnection(source.id, n.id, editState, workerIds, n.type === 'system') === null,
      ),
    [nodes, source.id, editState, workerIds],
  )

  return (
    <DropdownMenu open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger asChild>
        <IconButton
          ref={triggerRef}
          data-node-action="delegate"
          data-testid={`team-node-delegate-${source.id}`}
          aria-label={`Delegate from ${source.name}`}
          title="Delegate — keyboard equivalent of dragging the gold connection dot onto a target (an agent, or this same agent to add a self-line)"
          className="nodrag h-auto w-auto shrink-0 rounded p-[var(--space-1)] text-[var(--color-muted)] hover:bg-[var(--color-accent)]/15 hover:text-[var(--color-accent)]"
          onClick={(e) => e.stopPropagation()}
        >
          <ArrowBendUpRight size={12} weight="bold" />
        </IconButton>
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
          <p className="px-[var(--space-2)] py-[var(--space-1)] text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
            No eligible agents — every possible target already has a delegation
            edge from {source.name}, or is not a valid target.
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
              <span className="ml-auto text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{n.role}</span>
            </DropdownMenuItem>
          ))
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
