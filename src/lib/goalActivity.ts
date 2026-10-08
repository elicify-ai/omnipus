import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'

/**
 * True while a session's goal is still running or will run again on its own
 * (the keeper resumes it between turns). In the idle gap between two goal turns
 * `isStreaming` is false, yet Stop must still reach the gateway so the keeper is
 * paused. `judge_unavailable` counts: pkg/agent/goal_triggers.go::reportJudgeUnavailable
 * clears the idle marker "so the next quiet-window re-fires" once the Judge recovers.
 * `waiting_on_user` and `blocked` do not: they park until the user speaks. Read from the bucket's latest `goal_status` frame, which is fresher
 * than the polled sessions list.
 */
export function isGoalRunning(goalStatus: GoalStatusFrame | null | undefined): boolean {
  const state = goalStatus?.state
  return state === 'active' || state === 're-planning' || state === 'judging' || state === 'judge_unavailable'
}

/** The slice of a session bucket that decides whether its goal is still running. */
export interface GoalRunningBucket {
  goalStatus?: GoalStatusFrame | null
  /** When a cancel frame last left for this session (client clock). */
  goalStopSentAt?: number | null
  lastUserMessageAt?: number | null
}

/**
 * The goal is running unless the person stopped it and has not written since.
 * pkg/agent/goal_triggers.go::pauseGoalKeeperForStop pauses the keeper after any
 * explicit Stop until a user message arrives ("the goal stays active"), and no frame
 * announces that pause, so the client mirrors the gateway rule: a Stop sent after the
 * last user message means paused. Not persisted: a reload forgets it, and Stop is then
 * offered again until the next message (the gateway still honours a second Stop).
 */
export function isBucketGoalRunning(bucket: GoalRunningBucket | null | undefined): boolean {
  if (!bucket || !isGoalRunning(bucket.goalStatus)) return false
  const stoppedAt = bucket.goalStopSentAt ?? 0
  return !(stoppedAt > 0 && stoppedAt > (bucket.lastUserMessageAt ?? 0))
}
