import { create } from 'zustand'

import type { JudgeVerdictFrame } from '@/lib/api/generated/asyncapi-types'

// ADR-049 D2/D4/US-13 — dedicated GLOBAL store for live judge-verdict pushes.
//
// `JudgeVerdictFrame` deliberately carries no `session_id` (it is correlated
// by task_id/plan_id, not any specific chat thread — see chat.ts's
// `SESSION_SCOPED_FRAME_TYPES` comment), so it cannot be routed through the
// per-session chat-store buckets the way rate_limit/goal_status/loop_status
// are. It is fed here instead, mirroring the #283/#264
// whatsapp_pairing/notification pattern: `chat.ts`'s `handleFrame` calls
// `useJudgeActivityStore.getState().apply(frame)` directly (no hook
// subscription), keeping the chat store decoupled from this one.
//
// `useRunningActivity` (the ActivityBar/ActivityPanel data hook) reads
// `verdicts` to build `JudgeActivityItem`s — always placed in
// `recentlyFinished` (a judge call has no "running" phase on the wire: only
// the completed verdict is ever pushed), sharing `RECENTLY_FINISHED_CAP=8`
// with agent/bash items (SD-C11). This store's own cap keeps unbounded
// memory growth off the table even before that hook-level cap applies.

export const JUDGE_VERDICT_CAP = 8

interface JudgeActivityStore {
  /** Most-recently-arrived last, capped at JUDGE_VERDICT_CAP. */
  verdicts: JudgeVerdictFrame[]
  apply: (frame: JudgeVerdictFrame) => void
  /** Test-only reset hook. */
  reset: () => void
}

export const useJudgeActivityStore = create<JudgeActivityStore>((set) => ({
  verdicts: [],
  apply: (frame) =>
    set((s) => ({
      // De-dupe by verdict id (a WS reconnect could theoretically re-deliver
      // the same push) before appending and re-capping.
      verdicts: [...s.verdicts.filter((v) => v.id !== frame.id), frame].slice(-JUDGE_VERDICT_CAP),
    })),
  reset: () => set({ verdicts: [] }),
}))
