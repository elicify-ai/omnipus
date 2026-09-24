import { Profiler } from 'react'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  AssistantConnectionStatus,
  AssistantMessageConnectionStatus,
  ChatConnectionNotice,
  ChatConnectionStatusLine,
  UserMessageDeliveryStatus,
  deriveConnectionDisplay,
} from './ConnectionStatus'
import { useConnectionStore } from '@/store/connection'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'

afterEach(() => {
  vi.useRealTimers()
  useConnectionStore.setState({
    isConnected: true,
    reconnectPhase: null,
    reconnectAttempt: 0,
    disconnectedAt: null,
    reconnectedAt: null,
    lastDisconnectDurationMs: null,
    lastDisconnectWasTerminal: false,
    disconnectedAssistantMessageId: null,
    liteMode: false,
  })
})

describe('UserMessageDeliveryStatus', () => {
  it.each([
    ['queued', 'Not sent yet, will be sent automatically', "Not sent yet — it will be sent automatically as soon as you're back online."],
    ['received', 'Received', 'Received by Omnipus. Mia will pick it up after finishing the current step.'],
    ['working', 'Mia is working on it', 'Mia is working on this.'],
  ] as const)('renders the %s icon with its exact accessible label and tooltip', (state, label, tooltip) => {
    render(<UserMessageDeliveryStatus state={state} agentName="Mia" latest />)

    const icon = screen.getByRole('button', { name: label })
    fireEvent.focus(icon)
    expect(screen.getByRole('tooltip')).toHaveTextContent(tooltip)
  })

  it('keeps failed text and exposes the exact retry action without error colours', () => {
    const onRetry = vi.fn()
    const { container } = render(
      <UserMessageDeliveryStatus state="failed" agentName="Mia" latest onRetry={onRetry} />,
    )

    expect(screen.getByText("Couldn't be sent")).toBeVisible()
    // Review finding 15: the trailing ", button" was removed from the label
    // — the element is already a real <button>, so a screen reader already
    // announces its role; keeping the literal suffix double-announced it
    // ("…, button, button"). Provenance: this assertion previously expected
    // the (buggy) doubled-announcement label text verbatim.
    const retry = screen.getByRole('button', { name: "Couldn't be sent. Try again" })
    fireEvent.focus(retry)
    expect(screen.getByRole('tooltip')).toHaveTextContent(
      "This message couldn't be delivered. Your text is kept — click to try again.",
    )
    fireEvent.click(retry)
    expect(onRetry).toHaveBeenCalledOnce()
    expect(container.innerHTML).not.toMatch(/color-(?:error|warning)|\bred-|\borange-|\bamber-/)
  })
})

describe('AssistantConnectionStatus', () => {
  it('shows the exact quiet continuation state after a long drop', () => {
    render(<AssistantConnectionStatus state="paused" agentName="Mia" />)

    expect(screen.getByTestId('assistant-connection-status')).toHaveTextContent(
      'Mia is still working on this — the rest appears when you\'re connected again.',
    )
    const icon = screen.getByRole('button', {
      name: 'Mia is still working. The rest appears when the connection is back.',
    })
    fireEvent.focus(icon)
    expect(screen.getByRole('tooltip')).toHaveTextContent(
      'Your connection dropped. Mia keeps working in the background; nothing is lost.',
    )
  })

  it('shows Generate again when the answer can no longer resume', () => {
    const onGenerateAgain = vi.fn()
    render(
      <AssistantConnectionStatus
        state="unfinished"
        agentName="Mia"
        onGenerateAgain={onGenerateAgain}
      />,
    )

    expect(screen.getByText("This answer couldn't be finished")).toBeVisible()
    // Review finding 15: trailing ", button" removed — see the provenance
    // note on the equivalent UserMessageDeliveryStatus assertion above.
    const generate = screen.getByRole('button', { name: "Answer couldn't be finished. Generate again" })
    fireEvent.focus(generate)
    expect(screen.getByRole('tooltip')).toHaveTextContent(
      'The connection was gone for too long to continue this answer. Generate it again to get a complete reply.',
    )
    fireEvent.click(generate)
    expect(onGenerateAgain).toHaveBeenCalledOnce()
  })
})

describe('ChatConnectionStatusLine', () => {
  it.each([
    [
      'offline',
      'No internet connection. Your agents keep working.',
      'No internet connection. Your agents keep working.',
      "Your device isn't connected to the internet. Omnipus reconnects on its own when it's back — your messages and your agents' work are kept.",
    ],
    [
      'unreachable',
      "Can't reach Omnipus right now. Your agents keep working.",
      "Can't reach Omnipus right now. Your agents keep working.",
      "Omnipus isn't responding at the moment. It will keep trying on its own — your messages and your agents' work are kept.",
    ],
  ] as const)('renders the exact %s copy, screen-reader label, and tooltip', (state, copy, label, tooltip) => {
    render(<ChatConnectionStatusLine state={state} />)
    expect(screen.getByTestId('connection-status-line')).toHaveTextContent(copy)
    const icon = screen.getByRole('button', { name: label })
    fireEvent.focus(icon)
    expect(screen.getByRole('tooltip')).toHaveTextContent(tooltip)
  })

  it('renders the exact recovery confirmation', () => {
    render(<ChatConnectionStatusLine state="back" />)
    expect(screen.getByRole('status')).toHaveTextContent('Up to date')
  })
})

describe('deriveConnectionDisplay', () => {
  const base = {
    isConnected: false,
    reconnectPhase: 'reconnecting' as const,
    disconnectedAt: 1_000,
    reconnectedAt: null,
    lastDisconnectDurationMs: null,
    lastDisconnectWasTerminal: false,
    hasInterruptedAnswer: true,
    deviceOnline: true,
    // Review finding 14 / ADR-082: whether the server's own session_state
    // has confirmed a turn is still running for this session. Most cases
    // below are exercised offline, where this client cannot know either
    // way — default to false (the neutral "nothing confirmed running"
    // starting point most of these scenarios use).
    sessionHasActiveTurn: false,
    // #823 catch-up redesign (BE-DESIGN.md §6.5) — true while this
    // session's reconnect is still mid catch-up (between session_snapshot/
    // the incremental tail and the matching catch_up_complete). Default
    // false: most cases below represent an attach that has already fully
    // resolved one way or the other.
    awaitingCatchUp: false,
  }

  it('shows nothing for a drop shorter than 15 seconds', () => {
    expect(deriveConnectionDisplay({ ...base, now: 15_999 })).toEqual({ answer: 'hidden', chat: 'hidden' })
  })

  it('shows only the answer continuation state at 15 seconds', () => {
    expect(deriveConnectionDisplay({ ...base, now: 16_000 })).toEqual({ answer: 'paused', chat: 'hidden' })
  })

  it('distinguishes device offline from an unreachable gateway at two minutes', () => {
    expect(deriveConnectionDisplay({ ...base, now: 121_000, deviceOnline: false }).chat).toBe('offline')
    expect(deriveConnectionDisplay({ ...base, now: 121_000, deviceOnline: true }).chat).toBe('unreachable')
  })

  // Review finding 14 / ADR-082: CORRECTED — this test used to assert
  // `answer: 'unfinished'` (the literal buggy outcome the review flags: "the
  // turn still runs server-side" while the SPA shows a hard failure). A
  // turn never ends just because the browser gave up retrying (ADR-082 P1)
  // — while still disconnected there is no server signal to confirm
  // anything by, so the only honest state is the same quiet "still working"
  // continuation as any other drop, escalated only in the CHAT line (which
  // has always been about reachability, not turn completion).
  it('never claims the answer is unfinished while still offline, even after giving up retries (ADR-082 P1)', () => {
    expect(deriveConnectionDisplay({ ...base, now: 2_000, reconnectPhase: 'gave_up' })).toEqual({
      answer: 'paused',
      chat: 'unreachable',
    })
  })

  it('does not show "couldn\'t be finished" once reconnected while the server confirms the turn is still running', () => {
    expect(
      deriveConnectionDisplay({
        ...base,
        isConnected: true,
        reconnectPhase: null,
        disconnectedAt: null,
        reconnectedAt: 1_000,
        now: 1_000,
        sessionHasActiveTurn: true,
      }).answer,
    ).toBe('hidden')
  })

  it('shows "couldn\'t be finished" once reconnected and the server confirms the turn is NOT running', () => {
    expect(
      deriveConnectionDisplay({
        ...base,
        isConnected: true,
        reconnectPhase: null,
        disconnectedAt: null,
        reconnectedAt: 1_000,
        now: 1_000,
        sessionHasActiveTurn: false,
      }).answer,
    ).toBe('unfinished')
  })

  // #823 catch-up redesign (BE-DESIGN.md §6.5) — the ONLY input change this
  // lane makes to deriveConnectionDisplay itself (per the design's own DoD:
  // "phase-1 components untouched apart from the unfinished input"). The
  // gateway's session_state frame (which sessionHasActiveTurn is read from)
  // arrives BEFORE catch_up_complete in the real attach sequence (§4.1 A6),
  // so — even though it is already present locally — treating it as
  // authoritative before the matching catch_up_complete has actually landed
  // risks flashing "couldn't be finished" mid catch-up on a reconnect whose
  // incremental tail (or snapshot replay) hasn't finished being applied yet.
  it('never shows "couldn\'t be finished" while still mid catch-up, even if the connection is already reconnected and the server says no active turn', () => {
    expect(
      deriveConnectionDisplay({
        ...base,
        isConnected: true,
        reconnectPhase: null,
        disconnectedAt: null,
        reconnectedAt: 1_000,
        now: 1_000,
        sessionHasActiveTurn: false,
        awaitingCatchUp: true,
      }).answer,
    ).toBe('hidden')
  })

  it('shows Up to date only after a state that was visible, and only for two seconds', () => {
    const recovered = {
      ...base,
      isConnected: true,
      reconnectPhase: null,
      disconnectedAt: null,
      reconnectedAt: 50_000,
      lastDisconnectDurationMs: 15_000,
      // This scenario is about the CHAT recovery line, not an interrupted
      // answer — no assistant bubble is under test here.
      hasInterruptedAnswer: false,
    }
    expect(deriveConnectionDisplay({ ...recovered, now: 51_999 })).toEqual({ answer: 'hidden', chat: 'back' })
    expect(deriveConnectionDisplay({ ...recovered, now: 52_000 })).toEqual({ answer: 'hidden', chat: 'hidden' })
    expect(deriveConnectionDisplay({ ...recovered, now: 51_000, lastDisconnectDurationMs: 14_999 })).toEqual({
      answer: 'hidden',
      chat: 'hidden',
    })
  })
})

describe('ChatConnectionNotice timing', () => {
  it('waits two minutes, then shows the calm line and a two-second recovery confirmation', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-22T00:00:00Z'))
    const disconnectedAt = Date.now()
    useConnectionStore.setState({
      isConnected: false,
      reconnectPhase: 'reconnecting',
      disconnectedAt,
      disconnectedAssistantMessageId: 'assistant-1',
    })

    render(<ChatConnectionNotice />)
    expect(screen.queryByTestId('connection-status-line')).not.toBeInTheDocument()

    act(() => vi.advanceTimersByTime(120_000))
    expect(screen.getByTestId('connection-status-line')).toHaveTextContent(
      "Can't reach Omnipus right now. Your agents keep working. · Try again",
    )

    act(() => {
      useConnectionStore.setState({
        isConnected: true,
        reconnectPhase: null,
        disconnectedAt: null,
        reconnectedAt: Date.now(),
        lastDisconnectDurationMs: 120_000,
      })
    })
    expect(screen.getByTestId('connection-status-line')).toHaveTextContent('Up to date')

    act(() => vi.advanceTimersByTime(2_000))
    expect(screen.queryByTestId('connection-status-line')).not.toBeInTheDocument()
  })
})

// Review finding 13: "Status components keep re-rendering every second after
// the first reconnect." reconnectedAt was never cleared after the "Up to
// date" note, so `active` (`!isConnected || reconnectedAt !== null`) stayed
// true forever and the 1-second ticking timer never stopped — on EVERY
// assistant-message row, since AssistantMessageConnectionStatus started its
// timer before checking whether this row was even the disconnected one.
describe('finding 13 — permanent 1s timers after reconnect', () => {
  it('clears reconnectedAt ~2s after recovery, so the ticking timer stops for good', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-22T00:00:00Z'))
    // Drive the real setConnected(...) action (the only production path
    // that ever writes reconnectedAt — OmnipusRuntimeProvider.tsx), rather
    // than poking the store directly, so this exercises the actual fix
    // (the auto-clear timer lives inside that action).
    act(() => useConnectionStore.getState().setConnected(false))
    useConnectionStore.setState({ disconnectedAssistantMessageId: 'assistant-1' })
    // Advance past QUIET_DROP_MS (15s) so the drop was long enough to have
    // been shown — deriveConnectionDisplay only offers a "back"/recovery
    // state at all when the drop itself was visible.
    act(() => vi.advanceTimersByTime(16_000))
    act(() => useConnectionStore.getState().setConnected(true))

    render(<ChatConnectionNotice />)
    // "Up to date" is visible immediately after recovery.
    expect(screen.getByTestId('connection-status-line')).toHaveTextContent('Up to date')

    // BUG REGRESSION: before the fix, reconnectedAt was never cleared, so
    // `active` stayed true and the component (and every assistant-message
    // row's own status component) kept a 1-second setInterval running
    // forever.
    act(() => vi.advanceTimersByTime(2_500))
    expect(useConnectionStore.getState().reconnectedAt).toBeNull()
  })

  it('never starts a timer for an assistant row that is not the disconnected one', () => {
    const setIntervalSpy = vi.spyOn(window, 'setInterval')
    useConnectionStore.setState({
      isConnected: false,
      reconnectPhase: 'reconnecting',
      disconnectedAt: Date.now(),
      disconnectedAssistantMessageId: 'assistant-DISCONNECTED',
    })

    // This row is a DIFFERENT message than the one that was disconnected —
    // before the fix, useConnectionNow's setInterval ran regardless, for
    // EVERY assistant row in the thread, before the messageId check ever
    // short-circuited the render.
    render(<AssistantMessageConnectionStatus messageId="assistant-UNRELATED" agentName="Mia" />)

    expect(setIntervalSpy).not.toHaveBeenCalled()
    setIntervalSpy.mockRestore()
  })

  it('narrows its store subscription: an unrelated connection-store field change does not re-render the row', () => {
    useConnectionStore.setState({
      isConnected: false,
      reconnectPhase: 'reconnecting',
      disconnectedAt: Date.now(),
      disconnectedAssistantMessageId: 'assistant-1',
    })

    // React.Profiler's onRender fires on every commit of the wrapped
    // subtree, including a child re-rendering with no prop change — unlike
    // counting an outer wrapper's own render, which only proves the WRAPPER
    // didn't re-render, not the subscribed child.
    let commitCount = 0
    render(
      <Profiler id="probe" onRender={() => { commitCount += 1 }}>
        <AssistantMessageConnectionStatus messageId="assistant-1" agentName="Mia" />
      </Profiler>,
    )
    const commitsAfterMount = commitCount

    // liteMode is unrelated to anything deriveConnectionDisplay reads.
    // BUG REGRESSION: before the fix, the component subscribed to
    // `(state) => state` (the WHOLE store), so this unrelated field
    // flipping re-rendered it anyway.
    act(() => {
      useConnectionStore.setState({ liteMode: true })
    })

    expect(commitCount).toBe(commitsAfterMount)
  })
})

// Review finding 15: a11y gaps in ConnectionStatus.tsx.
describe('finding 15 — accessibility gaps', () => {
  it('never fades the failed-send Try again button, even when not the latest message', () => {
    const { container } = render(
      <UserMessageDeliveryStatus state="failed" agentName="Mia" latest={false} onRetry={vi.fn()} />,
    )
    // BUG REGRESSION: the whole point of "failed" is that it needs action —
    // fading was meant only for the passive ticks (queued/received/working),
    // not this actionable, WCAG 1.4.13-relevant control.
    expect(container.innerHTML).not.toMatch(/opacity-40/)
  })

  it('the tooltip can be dismissed with Escape (WCAG 1.4.13 dismissable)', () => {
    render(<UserMessageDeliveryStatus state="received" agentName="Mia" latest onRetry={vi.fn()} />)
    const icon = screen.getByRole('button', { name: 'Received' })
    fireEvent.focus(icon)
    expect(screen.getByRole('tooltip')).toBeInTheDocument()

    fireEvent.keyDown(icon, { key: 'Escape' })
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
  })

  it('the tooltip is hoverable — the pointer can rest on it (WCAG 1.4.13 hoverable)', () => {
    render(<UserMessageDeliveryStatus state="received" agentName="Mia" latest onRetry={vi.fn()} />)
    const icon = screen.getByRole('button', { name: 'Received' })
    fireEvent.focus(icon)
    const tooltip = screen.getByRole('tooltip')
    // BUG REGRESSION: `pointer-events-none` made it impossible for a mouse
    // user to move the pointer FROM the trigger ONTO the tooltip itself —
    // any such movement crossed dead space and the tooltip vanished.
    expect(tooltip.className).not.toMatch(/pointer-events-none/)
  })

  it('does not double-announce ", button" — the label is not appended with it when the element is already a real <button>', () => {
    render(<UserMessageDeliveryStatus state="failed" agentName="Mia" latest onRetry={vi.fn()} />)
    // A real <button> already announces its role as "button" to a screen
    // reader; appending ", button" to the label text makes that
    // "…, button, button". accessible name must not contain the literal
    // string.
    const retry = screen.getByRole('button', { name: /Couldn't be sent/ })
    expect(retry.getAttribute('aria-label')).not.toMatch(/,\s*button\s*$/i)
  })
})

// Review finding 14 / ADR-082: the component-level wiring for the two pure
// deriveConnectionDisplay assertions above.
describe('finding 14 — AssistantMessageConnectionStatus wiring', () => {
  const TEST_SID = 'finding-14-session'

  afterEach(() => {
    useChatStore.setState({ sessionsById: {} })
    useSessionStore.setState({ activeSessionId: null })
  })

  it('does not render Generate again while the server confirms the session still has an active turn', () => {
    useConnectionStore.setState({
      isConnected: true,
      reconnectPhase: null,
      disconnectedAt: null,
      reconnectedAt: null,
      lastDisconnectDurationMs: 20_000,
      lastDisconnectWasTerminal: true,
      disconnectedAssistantMessageId: 'assistant-1',
    })
    useSessionStore.setState({ activeSessionId: TEST_SID })
    // activeTurnId lives on the per-session bucket, not the flat foreground
    // state — see ConnectionStatus.tsx's own read for why.
    useChatStore.setState({
      sessionsById: { [TEST_SID]: { activeTurnId: 'turn-still-running' } as never },
    })

    render(<AssistantMessageConnectionStatus messageId="assistant-1" agentName="Mia" />)
    // BUG REGRESSION: before the fix this rendered "Generate again" purely
    // because the client had given up retrying — even though ADR-082 says
    // the turn keeps running regardless, and the server (via session_state)
    // has just confirmed exactly that.
    expect(screen.queryByTestId('assistant-connection-status')).not.toBeInTheDocument()
  })

  it('BE-DESIGN.md §6.5 / Opus review round 2 item 5: renders once reconnected even though disconnectedAssistantMessageId was already cleared, when the bucket itself shows the turn ended with no done()', () => {
    // Regression: connection.ts::setConnected(true) clears
    // disconnectedAssistantMessageId the INSTANT the socket reconnects — so
    // gating this component purely on `disconnectedHere` made the
    // awaitingCatchUp check downstream in deriveConnectionDisplay
    // unreachable dead code: by the time catch-up could possibly have
    // resolved, this component had already returned null. §6.5's real rule
    // doesn't need the connection store's transient flag at all — it is
    // fully derivable from the bucket: no done(T) applied (the bubble is
    // still open/unfinished) and session_state.active_turn is not T.
    useConnectionStore.setState({
      isConnected: true,
      reconnectPhase: null,
      disconnectedAt: null,
      reconnectedAt: null,
      lastDisconnectDurationMs: 20_000,
      lastDisconnectWasTerminal: true,
      disconnectedAssistantMessageId: null, // already cleared by setConnected(true)
    })
    useSessionStore.setState({ activeSessionId: TEST_SID })
    useChatStore.setState({
      sessionsById: {
        [TEST_SID]: {
          activeTurnId: null, // session_state confirms no turn running
          awaitingCatchUp: false, // catch_up_complete has already resolved
          isReplaying: false,
          messageOrder: ['assistant-1'],
          messagesById: {
            'assistant-1': {
              id: 'assistant-1',
              role: 'assistant',
              content: 'partial answer',
              timestamp: '2026-09-24T00:00:00Z',
              status: 'streaming',
              isStreaming: true,
              turnId: 'turn-gone-before-restart', // != activeTurnId (null) — the turn ended without a done()
            },
          },
        } as never,
      },
    })

    render(<AssistantMessageConnectionStatus messageId="assistant-1" agentName="Mia" />)
    expect(screen.getByTestId('assistant-connection-status')).toBeInTheDocument()
  })

  it('resends by message id (including any attachments) via resendMessage, not a fresh sendMessage call', () => {
    useConnectionStore.setState({
      isConnected: true,
      reconnectPhase: null,
      disconnectedAt: null,
      reconnectedAt: null,
      lastDisconnectDurationMs: 20_000,
      lastDisconnectWasTerminal: true,
      disconnectedAssistantMessageId: 'assistant-1',
    })
    const resendMessage = vi.fn()
    useSessionStore.setState({ activeSessionId: TEST_SID })
    useChatStore.setState({
      sessionsById: { [TEST_SID]: { activeTurnId: null } as never },
      messages: [
        { id: 'user-msg-1', role: 'user', content: 'hi', timestamp: '2026-09-22T00:00:00Z', status: 'done' },
      ] as never,
      resendMessage,
    })

    render(<AssistantMessageConnectionStatus messageId="assistant-1" agentName="Mia" />)
    const generate = screen.getByRole('button', { name: /Generate again/ })
    fireEvent.click(generate)
    // BUG REGRESSION: before the fix this called `sendMessage(lastUser.content)`
    // — a NEW message id, dropping any attachments the original carried.
    expect(resendMessage).toHaveBeenCalledWith('user-msg-1')
  })
})
