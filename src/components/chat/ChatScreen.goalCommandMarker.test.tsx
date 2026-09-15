/**
 * ChatScreen.goalCommandMarker.test.tsx
 *
 * UAT defect B, rendering half. A goal whose criteria never compiled left no
 * goal-shaped element in the thread after a reload — a probe found
 * `allGoalTestids: []`.
 *
 * The cause is a SERVER-side persistence asymmetry, established by reading
 * the chain rather than guessed: `pkg/agent/goal_loop.go::activateInstantGoal`
 * activates the goal and emits a live `goal_status` frame but never calls
 * `anchorGoalRecordInTranscript`, so the card / pill / ack line have no
 * transcript entry to replay from. `src/store/chat.ts`'s own comment above
 * `GOAL_ACK_LINE_TEXT` documents that tradeoff and names the backend fix.
 * Nothing here pretends to supply it.
 *
 * What IS persisted is the user's own message: `pkg/gateway/websocket.go`
 * appends the raw `/goal …` text before the agent loop rewrites it, and
 * `pkg/gateway/replay.go::streamReplay` applies no slash-command filter. It
 * replayed as an anonymous bubble, which is why a goal-shaped probe found
 * nothing. These tests pin that the replayed message renders as recognisably
 * a goal — from the persisted text alone, claiming nothing about goal STATE.
 *
 * `VirtualUserMessageRow` is the historical/reload path (see
 * ChatScreen.skill-chip.test.tsx, which uses it as the same seam).
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { VirtualUserMessageRow } from './ChatScreen'
import type { ChatMessage } from '@/store/chat'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

function makeUserMessage(content: string): ChatMessage {
  return {
    id: `msg-${Math.random().toString(36).slice(2)}`,
    role: 'user',
    content,
    timestamp: new Date().toISOString(),
    status: 'done',
  } as ChatMessage
}

function renderReplayedMessage(content: string) {
  return render(
    <VirtualUserMessageRow message={makeUserMessage(content)} skills={[]} commandLabels={['/goal']} />,
  )
}

describe('VirtualUserMessageRow — a replayed /goal message is recognisable as a goal', () => {
  it('marks a replayed goal-setting message, so a reload leaves a goal-shaped trace', () => {
    renderReplayedMessage('/goal ship the release with a written changelog')

    expect(screen.getByTestId('goal-command-marker')).toBeInTheDocument()
    expect(screen.getByTestId('user-message')).toHaveAttribute('data-goal-command', 'true')
  })

  it('still shows the intent the user typed', () => {
    const { container } = renderReplayedMessage('/goal ship the release')
    expect(container.textContent).toContain('ship the release')
  })

  it('claims nothing about goal STATE — the marker is not a record card or a pill', () => {
    renderReplayedMessage('/goal ship the release')

    // Only what the transcript supports: "this message set a goal". The
    // record card and pill are live-frame-driven and must not be faked here.
    expect(screen.queryByTestId('goal-echo-card')).not.toBeInTheDocument()
    expect(screen.queryByTestId('goal-pill-tray')).not.toBeInTheDocument()
    expect(screen.queryByTestId('goal-ack-line')).not.toBeInTheDocument()
  })

  it('leaves an ordinary chat message unmarked', () => {
    renderReplayedMessage('ship the release')

    expect(screen.queryByTestId('goal-command-marker')).not.toBeInTheDocument()
    expect(screen.getByTestId('user-message')).not.toHaveAttribute('data-goal-command')
  })

  it('leaves a goal CLEAR command unmarked — it ended a goal, it did not set one', () => {
    renderReplayedMessage('/goal clear')

    expect(screen.queryByTestId('goal-command-marker')).not.toBeInTheDocument()
  })

  it('leaves a bare /goal status query unmarked', () => {
    renderReplayedMessage('/goal')

    expect(screen.queryByTestId('goal-command-marker')).not.toBeInTheDocument()
  })
})
