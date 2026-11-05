import { createContext, useEffect, useState } from 'react'
import type { SmoothOptions } from '@assistant-ui/react'
import { useChatStore } from '@/store/chat'
import { logDiagnostic } from '@/lib/telemetry'

/**
 * Whether the opened conversation has been revealed. Renderers outside a
 * covered list (embedded threads, stories) are always revealed.
 */
export const ThreadRevealContext = createContext(true)

/**
 * Text-reveal pacing for a live reply part. AssistantUI's typewriter starts a
 * RUNNING part at an empty string and re-types everything it already holds over
 * its drain window (useSmooth's initial state). For a reply that was already in
 * progress when the chat opened, that re-type is the open flicker: the
 * text-so-far blanks and types itself in again while the rows around it are
 * still being measured. While the conversation is covered, a part catches up
 * within one animation frame; text that arrives after the reveal keeps the
 * normal pacing. Options only retune the running animator, so switching them
 * never restarts the reveal.
 */
export const CATCH_UP_SMOOTH: SmoothOptions = { drainMs: 1 }

/** Upper bound for one opening; reaching it is logged, never silent. */
export const OPEN_REVEAL_LIMIT_MS = 10_000

type OpenState = 'pending' | 'ready' | 'failed' // not-wire-format: local reveal decision

function openState(sid: string | null, openFailed: boolean): (state: ReturnType<typeof useChatStore.getState>) => OpenState {
  return (state) => {
    const bucket = sid ? state.sessionsById[sid] : undefined
    if (openFailed) return 'failed'
    if (!bucket) return 'pending'
    const { history } = bucket
    // A bucket with no paging state has no page to wait for: reveal rather
    // than hide content (store buckets always carry it; seeded ones may not).
    if (!history) return 'ready'
    if (history.serverState?.status === 'unavailable' || history.requests.latest.status === 'error' || history.requests.latest.error) return 'failed'
    if (bucket.isReplaying || bucket.awaitingCatchUp) return 'pending'
    return history.historyLoaded || history.viewMode === 'historical' ? 'ready' : 'pending'
  }
}

/**
 * One reveal per opened conversation (the list is keyed per session, so it
 * mounts once per open). The thread stays covered until the latest page is
 * rendered, the live attach has caught up and two animation frames have let
 * the mounted rows report their measured heights; then it is revealed in one
 * step at the position the viewport already pinned. A failed read or catch-up
 * reveals at once, so the existing visible error and Retry take over. A brand
 * new chat (its first message is still being sent) is never covered: its own
 * message must stay visible.
 */
export function useOpenReveal(sid: string | null, openFailed: boolean): boolean {
  const state = useChatStore(openState(sid, openFailed))
  const [covered, setCovered] = useState(() => {
    if (!sid || sid === '__pending') return false
    const pending = useChatStore.getState().pendingFirstSend
    return !(pending && (pending.sessionId === null || pending.sessionId === sid))
  })
  useEffect(() => {
    if (!covered) return
    if (state === 'failed') { setCovered(false); return }
    if (state !== 'ready') return
    let frame = requestAnimationFrame(() => { frame = requestAnimationFrame(() => setCovered(false)) })
    return () => cancelAnimationFrame(frame)
  }, [covered, state])
  useEffect(() => {
    if (!covered) return
    const timer = setTimeout(() => {
      logDiagnostic('chatOpenRevealTimeout', { limitMs: OPEN_REVEAL_LIMIT_MS })
      setCovered(false)
    }, OPEN_REVEAL_LIMIT_MS)
    return () => clearTimeout(timer)
  }, [covered])
  return covered
}
