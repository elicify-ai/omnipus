// frame-cases.mail-panel-observer.test.ts: the client frame router must
// RECOGNISE the two server -> client Mail panel presence frames,
// mail_panel_observer_ack and mail_panel_observer_error, instead of sending
// them down the unknown-frame default branch of
// slices/frames.ts::createFrameSlice.handleFrame.
//
// Oracle sources (never the implementation under test):
//   - contracts/asyncapi.yaml, components.messages.MailPanelObserverAckFrame /
//     MailPanelObserverErrorFrame and components.schemas of the same names:
//     both are declared server -> client frames, connection-scoped, "no
//     session_id" — they are part of the contract the router is expected to
//     understand, so they are NOT unknown frames.
//   - the sender side: components/workspaces/mail/mailPanelPresence.ts::
//     createMailPanelPresence emits mail_panel_observer (open/close) on
//     every Mail panel mount, and gateway pkg/gateway/mail_presence.go::
//     handleMailPanelObserverFrame answers each with the ack (or error).
//   - the generated Zod schemas in src/lib/api/generated/ws-schemas.ts, used
//     below to prove the fixtures are contract-valid frames (the real
//     transport edge would have accepted them and forwarded them to
//     handleFrame).
//
// The unit is the REAL useChatStore.handleFrame. Mocked: the telemetry sink
// only (a process edge; logDiagnostic is a no-op in test mode, so a spy is
// the only way to observe whether the unknown-frame signal was sent), plus
// console.warn as the observation point for the warning.
//
// SCOPE LIMIT (design question reported to team-lead, not pinned here):
// the contract says the error frame makes "the panel fall back to
// conservative request-scoped connections" and carries a human-readable
// `error` "safe to display", but it names NO client-visible surface (no
// toast, banner, store field or adapter callback), and
// mailPanelPresence.ts deliberately has no failure channel (presence is
// "best-effort ... never an authorization or correctness dependency"). So
// the "surfaced visibly, never silently swallowed" requirement cannot be
// derived from the contract; it is not asserted below. Likewise the ack has
// no named client-visible effect (the adapter has no acknowledged state), so
// only recognition is asserted for it.

import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import { logDiagnostic } from '@/lib/telemetry'
import { MailPanelObserverAckFrame, MailPanelObserverErrorFrame } from '@/lib/api/generated/ws-schemas'
import type { WsReceiveFrame } from '@/lib/ws'

vi.mock('@/lib/telemetry', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/telemetry')>()
  return { ...actual, logDiagnostic: vi.fn() }
})

const UNKNOWN_TYPE = 'mail_panel_observer_nonexistent_for_control'
const OBSERVER_ID = 'obs-11111111-2222-3333-4444-555555555555'
const WORKSPACE_ID = 'ws-mail-1'

// Contract-shaped fixtures (asyncapi.yaml schemas: required keys, no session_id).
const ackOpen = {
  type: 'mail_panel_observer_ack',
  action: 'open',
  observer_id: OBSERVER_ID,
  workspace_id: WORKSPACE_ID,
}
const ackClose = { ...ackOpen, action: 'close' }
const errUnauthorized = {
  type: 'mail_panel_observer_error',
  observer_id: OBSERVER_ID,
  workspace_id: WORKSPACE_ID,
  code: 'unauthorized_workspace',
  error: 'This connection is not authorized for that workspace.',
}
const errMalformed = { ...errUnauthorized, code: 'malformed_frame', error: 'The presence frame was malformed.' }

let warnSpy: ReturnType<typeof vi.spyOn>

beforeEach(() => {
  vi.mocked(logDiagnostic).mockClear()
  warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
  useSessionStore.setState({ activeSessionId: 'session-mail-observer' })
  useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {} } as never)
})

afterEach(() => {
  warnSpy.mockRestore()
})

function send(frame: object): void {
  useChatStore.getState().handleFrame(frame as unknown as WsReceiveFrame)
}

function unknownFrameWarnings(): unknown[][] {
  return warnSpy.mock.calls.filter((c: unknown[]) => c[0] === '[chat] Unknown frame type')
}

function unknownFrameTelemetry(): unknown[][] {
  return vi.mocked(logDiagnostic).mock.calls.filter((c: unknown[]) => c[0] === 'chatUnknownFrameType')
}

describe('fixtures are contract-valid (oracle check for the frames below)', () => {
  it.each([
    ['ack open', ackOpen, MailPanelObserverAckFrame],
    ['ack close', ackClose, MailPanelObserverAckFrame],
    ['error unauthorized_workspace', errUnauthorized, MailPanelObserverErrorFrame],
    ['error malformed_frame', errMalformed, MailPanelObserverErrorFrame],
  ])('the generated schema accepts the %s fixture', (_name, fixture, schema) => {
    expect(schema.safeParse(fixture).success).toBe(true)
  })
})

describe('mail_panel_observer_ack is a recognised frame (asyncapi.yaml MailPanelObserverAckFrame)', () => {
  it.each([
    ['open', ackOpen],
    ['close', ackClose],
  ])('an ack for action=%s emits no unknown-frame warning', (_action, frame) => {
    send(frame)
    expect(unknownFrameWarnings()).toEqual([])
  })

  it.each([
    ['open', ackOpen],
    ['close', ackClose],
  ])('an ack for action=%s emits no chatUnknownFrameType telemetry', (_action, frame) => {
    send(frame)
    expect(unknownFrameTelemetry()).toEqual([])
  })

  it('an ack between two unknown frames is itself recognised: only the two unknown frames warn', () => {
    // runtime.unknownFrameCount resets on every frame (frames.ts HIGH-2
    // comment: "reset on every known-good frame"); the unknown branch then
    // increments it. Interleaving a recognised frame must therefore leave
    // each unknown frame at count 1 and still warn for both.
    send({ type: UNKNOWN_TYPE })
    send(ackOpen)
    send({ type: UNKNOWN_TYPE })
    expect(unknownFrameWarnings()).toEqual([
      ['[chat] Unknown frame type', { type: UNKNOWN_TYPE, count: 1 }],
      ['[chat] Unknown frame type', { type: UNKNOWN_TYPE, count: 1 }],
    ])
  })
})

describe('mail_panel_observer_error is a recognised frame (asyncapi.yaml MailPanelObserverErrorFrame)', () => {
  it.each([
    ['unauthorized_workspace', errUnauthorized],
    ['malformed_frame', errMalformed],
  ])('an error with code=%s emits no unknown-frame warning', (_code, frame) => {
    send(frame)
    expect(unknownFrameWarnings()).toEqual([])
  })

  it.each([
    ['unauthorized_workspace', errUnauthorized],
    ['malformed_frame', errMalformed],
  ])('an error with code=%s emits no chatUnknownFrameType telemetry', (_code, frame) => {
    send(frame)
    expect(unknownFrameTelemetry()).toEqual([])
  })

  // Coordinator ruling (2026-10-05): the error frame is recognised, shows NO
  // toast/banner (presence is best-effort; Mail keeps working through its
  // request-scoped fallback), but is never silent: exactly one specific
  // diagnostic carrying the frame's closed-class `code`.
  it('an error frame records exactly one mailPanelObserverRefused diagnostic carrying its code, and no unknown-frame diagnostic', () => {
    send(errUnauthorized)
    const refused = vi.mocked(logDiagnostic).mock.calls.filter((c: unknown[]) => c[0] === 'mailPanelObserverRefused')
    expect(refused).toHaveLength(1)
    expect(refused[0][1]).toEqual(expect.objectContaining({ code: 'unauthorized_workspace' }))
    expect(unknownFrameTelemetry()).toEqual([])
  })
})

describe('control: a genuinely unknown frame type still takes the unknown-frame path', () => {
  it('warns once with the unknown type and the running count', () => {
    send({ type: UNKNOWN_TYPE })
    expect(unknownFrameWarnings()).toEqual([
      ['[chat] Unknown frame type', { type: UNKNOWN_TYPE, count: 1 }],
    ])
  })

  it('sends chatUnknownFrameType telemetry with the unknown type and count', () => {
    send({ type: UNKNOWN_TYPE })
    expect(unknownFrameTelemetry()).toEqual([
      ['chatUnknownFrameType', { frameType: UNKNOWN_TYPE, count: 1 }],
    ])
  })
})
