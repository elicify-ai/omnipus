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
