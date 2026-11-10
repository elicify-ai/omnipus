// useToolIterationLimitRefusal — keeps a refused own tool-iteration limit
// (#904 D10 "above the global" / the 1–1000 bound, both a 400 naming
// max_tool_iterations) from hijacking AgentProfile's autosave:
//
//   - only THAT refusal is pinned on the limit field; any other failed save
//     (a 5xx, a revision 409 caused by another field) stays on the normal
//     autosave indicator;
//   - the refused value is dropped from later payloads (and the refused PUT is
//     retried once without it), so unrelated fields keep saving while the
//     field explains why its own value did not;
//   - editing the limit again clears the refusal, so the next value — even
//     the same number after the global was raised — is sent normally; a
//     refusal (and the set of refusal texts the indicator suppresses) belongs
//     to one agent and never follows the form to another;
//   - a refusal that answers a save after the admin already switched to
//     another agent is not dropped silently: an error toast names the agent
//     it belongs to (its name at save time, else its id) and the refused
//     value, since no field of that agent is on screen to explain it.
//
// A refusal is recognised ONLY by the server's structured `field:
// "max_tool_iterations"` on a 400 (ErrorResponse.field, set on every per-agent
// limit refusal — create and update, bound and D10). There is no message-text
// fallback: a 400 without the field (an older backend) is not pinned on the
// limit field and stays on the ordinary autosave indicator, where it is still
// shown in full — never hidden.

import { useCallback, useRef, useState } from 'react'
import { isApiError } from '@/lib/api'
import { useUiStore } from '@/store/ui'

interface Refusal {
  agentId: string | null
  value: number | null
  message: string
}

type LimitDraft = { max_tool_iterations?: number | null }

const EMPTY: ReadonlySet<string> = new Set()

export function isToolIterationLimitRefusal(err: unknown): boolean {
  if (!isApiError(err) || err.status !== 400) return false
  return err.field === 'max_tool_iterations'
}

export interface ToolIterationLimitRefusal {
  /** The refusal to pin on the field while the draft still holds the refused value. */
  messageFor: (draft: number | null | undefined) => string | null
  /**
   * True when a save error is a limit refusal (current or already cleared by
   * a re-edit): the field owns that explanation, so the autosave indicator
   * shows its generic text instead of repeating or resurrecting it.
   */
  isRefusalMessage: (message: string | undefined) => boolean
  /** The draft minus a still-refused limit — also used by the pagehide flush. */
  withoutRefused: <T extends LimitDraft>(draft: T) => T
  /** True while the draft still holds a refused value (the form stays dirty). */
  holdsRefused: (draft: number | null | undefined) => boolean
  clear: () => void
  /**
   * Runs one save. `send` returns false when the draft has nothing to PUT.
   * A limit refusal is recorded and the save retried once without the limit;
   * when nothing else was left to send, the refusal is re-thrown.
   */
  save: <T extends LimitDraft>(draft: T, send: (data: T) => Promise<boolean>) => Promise<void>
}

// Long enough to read the agent, the value and the server's reason.
const OFF_SCREEN_REFUSAL_TOAST_MS = 10_000

function offScreenRefusalText(agentId: string | null, agentName: string | undefined, value: number | null, message: string): string {
  const who = agentName?.trim() ? agentName.trim() : `Agent ${agentId ?? 'unknown'}`
  return `${who}: own tool-call limit ${value ?? 'none'} was not saved — ${message}`
}

export function useToolIterationLimitRefusal(agentId: string | null, agentName?: string): ToolIterationLimitRefusal {
  const addToast = useUiStore((s) => s.addToast)
  const [state, setRefusal] = useState<Refusal | null>(null)
  // The refusal texts seen for the CURRENT agent only (the indicator hides
  // them because the field explains them).
  const [refusalMessages, setRefusalMessages] = useState<ReadonlySet<string>>(EMPTY)
  const ref = useRef<Refusal | null>(null)
  const agentRef = useRef(agentId)
  // A different agent drops the refusal and the text set outright — neither
  // follows the form to another agent, nor comes back on returning to this
  // one (state adjusted during render, React's documented pattern).
  const [shownFor, setShownFor] = useState(agentId)
  if (shownFor !== agentId) {
    setShownFor(agentId)
    setRefusal(null)
    setRefusalMessages(EMPTY)
  }
  if (agentRef.current !== agentId) {
    agentRef.current = agentId
    ref.current = null
  }
  const nameRef = useRef(agentName)
  nameRef.current = agentName
  const current = useCallback(
    () => (ref.current && ref.current.agentId === agentRef.current ? ref.current : null),
    [],
  )
  const refusal = state && state.agentId === agentId ? state : null

  const record = useCallback((next: Refusal | null) => {
    // A refusal answering a save started for another agent is stale.
    if (next && next.agentId !== agentRef.current) return
    ref.current = next
    setRefusal(next)
    if (next) setRefusalMessages((prev) => (prev.has(next.message) ? prev : new Set(prev).add(next.message)))
  }, [])

  const clear = useCallback(() => {
    if (ref.current !== null) record(null)
  }, [record])

  const withoutRefused = useCallback(<T extends LimitDraft>(draft: T): T => {
    const refused = current()
    return refused && draft.max_tool_iterations === refused.value ? { ...draft, max_tool_iterations: undefined } : draft
  }, [current])

  const save = useCallback(async <T extends LimitDraft>(draft: T, send: (data: T) => Promise<boolean>) => {
    const owner = agentRef.current
    const ownerName = nameRef.current
    const withoutLimit = { ...draft, max_tool_iterations: undefined }
    const data = withoutRefused(draft)
    try {
      await send(data)
    } catch (err) {
      if (data.max_tool_iterations === undefined || !isToolIterationLimitRefusal(err)) throw err
      const refused = { agentId: owner, value: data.max_tool_iterations, message: isApiError(err) ? err.userMessage : String(err) }
      if (owner === agentRef.current) record(refused)
      else addToast({ variant: 'error', message: offScreenRefusalText(owner, ownerName, refused.value, refused.message), duration: OFF_SCREEN_REFUSAL_TOAST_MS })
      if (!(await send(withoutLimit))) throw err
    }
  }, [addToast, record, withoutRefused])

  const messageFor = (draft: number | null | undefined) =>
    refusal && draft !== undefined && draft === refusal.value ? refusal.message : null
  const holdsRefused = (draft: number | null | undefined) => {
    const refused = current()
    return refused !== null && draft !== undefined && draft === refused.value
  }

  const isRefusalMessage = (message: string | undefined) => message !== undefined && refusalMessages.has(message)

  return { messageFor, isRefusalMessage, withoutRefused, holdsRefused, clear, save }
}
