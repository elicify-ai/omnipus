/**
 * #904 gate round 2, item 3 — a per-agent limit refusal is recognised only by
 * the server's structured `field: "max_tool_iterations"` (ErrorResponse.field),
 * never by the message text; and the set of refusal texts the autosave
 * indicator suppresses belongs to one agent and is dropped when the agent
 * changes.
 */

import { describe, it, expect, beforeEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { ApiError } from '@/lib/api-error'
import { isToolIterationLimitRefusal, useToolIterationLimitRefusal } from './useToolIterationLimitRefusal'
import { useUiStore } from '@/store/ui'

const TEXT = 'max_tool_iterations 300 is above the global limit (200); lower it, or raise the global limit in Settings → Performance'

describe('isToolIterationLimitRefusal', () => {
  it('a 400 carrying field max_tool_iterations is a refusal', () => {
    expect(isToolIterationLimitRefusal(new ApiError(400, TEXT, { field: 'max_tool_iterations' }))).toBe(true)
  })

  it('a 400 whose text names max_tool_iterations but carries no field is NOT a refusal (no message parsing)', () => {
    expect(isToolIterationLimitRefusal(new ApiError(400, TEXT))).toBe(false)
  })

  it('a 400 attributed to another field is not a refusal, even when the text names the limit', () => {
    expect(isToolIterationLimitRefusal(new ApiError(400, TEXT, { field: 'model' }))).toBe(false)
  })

  it('a non-400 with the field is not a refusal', () => {
    expect(isToolIterationLimitRefusal(new ApiError(500, TEXT, { field: 'max_tool_iterations' }))).toBe(false)
  })
})

describe('useToolIterationLimitRefusal — refusal texts are per agent', () => {
  it('drops the suppressed refusal texts when the agent changes', async () => {
    const { result, rerender } = renderHook(({ id }) => useToolIterationLimitRefusal(id), {
      initialProps: { id: 'agent-a' as string | null },
    })
    const refusal = new ApiError(400, TEXT, { field: 'max_tool_iterations' })
    let sends = 0
    await act(async () => {
      await result.current.save({ max_tool_iterations: 300, name: 'x' }, async (data) => {
        sends += 1
        if (data.max_tool_iterations !== undefined) throw refusal
        return true
      })
    })
    expect(sends).toBe(2)
    expect(result.current.isRefusalMessage(TEXT)).toBe(true)
    expect(result.current.messageFor(300)).toBe(TEXT)

    rerender({ id: 'agent-b' })
    expect(result.current.isRefusalMessage(TEXT)).toBe(false)
    expect(result.current.messageFor(300)).toBeNull()

    // Back on the first agent the old set is not resurrected either.
    rerender({ id: 'agent-a' })
    expect(result.current.isRefusalMessage(TEXT)).toBe(false)
  })

  it('a text-only 400 is re-thrown to the caller (it is not swallowed as a refusal)', async () => {
    const { result } = renderHook(() => useToolIterationLimitRefusal('agent-a'))
    const plain = new ApiError(400, TEXT)
    await expect(
      act(async () => {
        await result.current.save({ max_tool_iterations: 300, name: 'x' }, async (data) => {
          if (data.max_tool_iterations !== undefined) throw plain
          return true
        })
      }),
    ).rejects.toBe(plain)
    expect(result.current.isRefusalMessage(TEXT)).toBe(false)
  })

  it('a refusal answering a save started for another agent is not recorded on the new one', async () => {
    const { result, rerender } = renderHook(({ id }) => useToolIterationLimitRefusal(id), {
      initialProps: { id: 'agent-a' as string | null },
    })
    const refusal = new ApiError(400, TEXT, { field: 'max_tool_iterations' })
    let reject: (e: unknown) => void = () => {}
    let pending: Promise<void> = Promise.resolve()
    act(() => {
      pending = result.current.save({ max_tool_iterations: 300, name: 'x' }, (data) =>
        data.max_tool_iterations !== undefined
          ? new Promise<boolean>((_, rej) => { reject = rej })
          : Promise.resolve(true),
      )
    })
    rerender({ id: 'agent-b' })
    await act(async () => {
      reject(refusal)
      await pending
    })
    expect(result.current.isRefusalMessage(TEXT)).toBe(false)
    expect(result.current.messageFor(300)).toBeNull()
  })

  // #904 gate round 3, silent-failure #4: the admin switched agent before the
  // server refused the first agent's own limit. The refusal must not follow
  // the form (above) — but it must not vanish either: a toast names the agent
  // and the refused value.
  describe('a refusal that lands after the admin switched agent', () => {
    beforeEach(() => {
      useUiStore.setState({ toasts: [] })
    })

    async function refuseAfterSwitch(nameAtSave: string | undefined) {
      const { result, rerender } = renderHook(({ id, name }) => useToolIterationLimitRefusal(id, name), {
        initialProps: { id: 'agent-a' as string | null, name: nameAtSave },
      })
      const refusal = new ApiError(400, TEXT, { field: 'max_tool_iterations' })
      let reject: (e: unknown) => void = () => {}
      let pending: Promise<void> = Promise.resolve()
      const sent: Array<number | null | undefined> = []
      act(() => {
        pending = result.current.save({ max_tool_iterations: 300, name: 'x' }, (data) => {
          sent.push(data.max_tool_iterations)
          return data.max_tool_iterations !== undefined
            ? new Promise<boolean>((_, rej) => { reject = rej })
            : Promise.resolve(true)
        })
      })
      rerender({ id: 'agent-b', name: 'Beta' })
      await act(async () => {
        reject(refusal)
        await pending
      })
      return { result, sent }
    }

    it('shows one error toast naming the first agent, the refused value and the server reason', async () => {
      const { result, sent } = await refuseAfterSwitch('Alpha')
      const toasts = useUiStore.getState().toasts
      expect(toasts).toHaveLength(1)
      expect(toasts[0].variant).toBe('error')
      expect(toasts[0].message).toBe(`Alpha: own tool-call limit 300 was not saved — ${TEXT}`)
      // The other fields were still saved for the first agent.
      expect(sent).toEqual([300, undefined])
      // Still not pinned on the agent now on screen.
      expect(result.current.messageFor(300)).toBeNull()
    })

    it('without a known name, the toast still names the agent by id', async () => {
      await refuseAfterSwitch(undefined)
      const toasts = useUiStore.getState().toasts
      expect(toasts).toHaveLength(1)
      expect(toasts[0].message).toBe(`Agent agent-a: own tool-call limit 300 was not saved — ${TEXT}`)
    })

    it('a refusal for the agent still on screen is pinned on the field, not toasted', async () => {
      const { result } = renderHook(() => useToolIterationLimitRefusal('agent-a', 'Alpha'))
      const refusal = new ApiError(400, TEXT, { field: 'max_tool_iterations' })
      await act(async () => {
        await result.current.save({ max_tool_iterations: 300, name: 'x' }, async (data) => {
          if (data.max_tool_iterations !== undefined) throw refusal
          return true
        })
      })
      expect(result.current.messageFor(300)).toBe(TEXT)
      expect(useUiStore.getState().toasts).toHaveLength(0)
    })
  })
})
