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
//     refusal belongs to one agent and never follows the form to another.

import { useCallback, useRef, useState } from 'react'
import { isApiError } from '@/lib/api'

interface Refusal {
  agentId: string | null
  value: number | null
  message: string
}

type LimitDraft = { max_tool_iterations?: number | null }

export function isToolIterationLimitRefusal(err: unknown): boolean {
  if (!isApiError(err) || err.status !== 400) return false
  return err.field === 'max_tool_iterations' || /\bmax_tool_iterations\b/.test(err.userMessage)
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

export function useToolIterationLimitRefusal(agentId: string | null): ToolIterationLimitRefusal {
  const [state, setRefusal] = useState<Refusal | null>(null)
  const [refusalMessages, setRefusalMessages] = useState<ReadonlySet<string>>(() => new Set())
  const ref = useRef<Refusal | null>(null)
  const agentRef = useRef(agentId)
  agentRef.current = agentId
  const current = useCallback(
    () => (ref.current && ref.current.agentId === agentRef.current ? ref.current : null),
    [],
  )
  const refusal = state && state.agentId === agentId ? state : null

  const record = useCallback((next: Refusal | null) => {
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
    const withoutLimit = { ...draft, max_tool_iterations: undefined }
    const data = withoutRefused(draft)
    try {
      await send(data)
    } catch (err) {
      if (data.max_tool_iterations === undefined || !isToolIterationLimitRefusal(err)) throw err
      record({ agentId: agentRef.current, value: data.max_tool_iterations, message: isApiError(err) ? err.userMessage : String(err) })
      if (!(await send(withoutLimit))) throw err
    }
  }, [record, withoutRefused])

  const messageFor = (draft: number | null | undefined) =>
    refusal && draft !== undefined && draft === refusal.value ? refusal.message : null
  const holdsRefused = (draft: number | null | undefined) => {
    const refused = current()
    return refused !== null && draft !== undefined && draft === refused.value
  }

  const isRefusalMessage = (message: string | undefined) => message !== undefined && refusalMessages.has(message)

  return { messageFor, isRefusalMessage, withoutRefused, holdsRefused, clear, save }
}
