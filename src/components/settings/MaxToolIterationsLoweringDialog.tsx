/**
 * MaxToolIterationsLoweringDialog — the D11/D16 confirm step shown before a
 * change to the global "Max tool calls per turn" limit lowers any agent's own
 * limit (docs/internal/specs/tool-iteration-limit-spec.md, User Story 6).
 *
 * It lists every affected agent as "old → new" and asks for an explicit
 * Confirm before anything is written (and before the step-up password prompt).
 * When the server refused a confirmed list because it changed in between
 * (409 drift, D16), `listChanged` shows a "list changed" notice announced via
 * role="alert" and moves focus to the first list entry so the fresh list is
 * read out; a fresh Confirm is then required.
 *
 * Built on the catalogued ConfirmDialog. Its description renders inside a <p>
 * (Radix Dialog.Description), so the list is spans carrying list/listitem
 * roles rather than <ul>/<li>, which may not nest inside a paragraph.
 */

import { useEffect, useRef } from 'react'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import type { MaxToolIterationAgentChange } from '@/lib/api/generated/openapi-types'

export interface MaxToolIterationsLoweringDialogProps {
  open: boolean
  /** The new global limit being saved. */
  value: number
  /** The agents whose own limit would be lowered to `value`. */
  agents: MaxToolIterationAgentChange[]
  /** True after a 409 drift: the list was reloaded from the server's answer. */
  listChanged: boolean
  onConfirm: () => void
  onCancel: () => void
}

export function MaxToolIterationsLoweringDialog({
  open,
  value,
  agents,
  listChanged,
  onConfirm,
  onCancel,
}: MaxToolIterationsLoweringDialogProps): React.ReactElement {
  const firstEntryRef = useRef<HTMLSpanElement | null>(null)

  // After a drift reload, move focus to the first entry of the fresh list.
  // Deferred one tick: the dialog's own open-autofocus (Cancel, the safe
  // default) runs in a parent effect after this one and would otherwise win.
  useEffect(() => {
    if (!open || !listChanged) return
    const timer = window.setTimeout(() => firstEntryRef.current?.focus(), 0)
    return () => window.clearTimeout(timer)
  }, [open, listChanged, agents])

  const count = agents.length
  const title =
    count === 0
      ? `Set the limit to ${value}?`
      : `Lower the limit for ${count} ${count === 1 ? 'agent' : 'agents'}?`

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onCancel()
      }}
      title={title}
      description={
        <>
          {listChanged && (
            <span
              role="alert"
              data-testid="max-tool-iterations-list-changed"
              className="block mb-[var(--space-2)] font-medium text-[var(--color-warning)]"
            >
              The list of affected agents changed — review and confirm again.
            </span>
          )}
          <span className="block">
            {count === 0
              ? `Setting the global limit to ${value} no longer lowers any agent's own limit.`
              : `Setting the global limit to ${value} also lowers these agents' own limits. Raising the global limit again later does not restore them.`}
          </span>
          <span
            role="list"
            aria-label="Agents whose limit will be lowered"
            data-testid="max-tool-iterations-lowering-list"
            className="block mt-[var(--space-2)] space-y-[var(--space-1)]"
          >
            {count === 0 ? (
              <span role="listitem" ref={firstEntryRef} tabIndex={-1} className="block">
                No agents will be lowered.
              </span>
            ) : (
              agents.map((agent, index) => (
                <span
                  key={agent.agent_id}
                  role="listitem"
                  ref={index === 0 ? firstEntryRef : undefined}
                  tabIndex={index === 0 ? -1 : undefined}
                  data-testid="max-tool-iterations-lowering-entry"
                  className="block text-[var(--color-secondary)]"
                >
                  {agent.agent_name}: <span className="font-mono">{agent.old_value}</span>{' '}
                  <span aria-hidden="true">→</span>
                  <span className="sr-only">to</span>{' '}
                  <span className="font-mono">{agent.new_value}</span>
                </span>
              ))
            )}
          </span>
        </>
      }
      confirmLabel={count === 0 ? 'Set limit' : 'Lower limits'}
      onConfirm={onConfirm}
    />
  )
}
