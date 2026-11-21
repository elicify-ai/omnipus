// frame-cases.bound-send-frames.test.ts: spec r9 4.10 / 4.8 / 4.7.
// server_capabilities, send_refused and command_received are consumed before
// the chat reducer (capability, refusal, command receipt). Driving them
// through the real useChatStore.handleFrame must not count them as unknown.
// Expected values are the spec's "already handled" outcome, not a log line
// copied from a run.
//
// cancel_idle is a known gap (#1841): nothing in the web client consumes it,
// so it stays on the unknown-frame path. Not asserted here.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from './store'
import { logDiagnostic } from '@/lib/telemetry'

vi.mock('@/lib/telemetry', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/telemetry')>()
  return { ...actual, logDiagnostic: vi.fn() }
})

const HANDLED = [
  { type: 'server_capabilities', send_origin: true },
  { type: 'send_refused', reason: 'wrong_chat', refused_frame: 'message' },
  { type: 'command_received', command: 'cancel', client_frame_id: 'cf-bound-1', session_id: 'sess_bound_frames' },
] as const

beforeEach(() => {
  vi.mocked(logDiagnostic).mockClear()
  vi.spyOn(console, 'warn').mockImplementation(() => {})
})

afterEach(() => {
  vi.mocked(console.warn).mockRestore()
})

describe('bound-send frames are not unknown chat frames', () => {
  it.each(HANDLED)('$type does not log chatUnknownFrameType or the unknown-frame warning', (frame) => {
    vi.mocked(logDiagnostic).mockClear()
    vi.mocked(console.warn).mockClear()

    useChatStore.getState().handleFrame(frame as never)

    const unknownLogs = vi.mocked(logDiagnostic).mock.calls.filter((call) => call[0] === 'chatUnknownFrameType')
    const unknownWarns = vi.mocked(console.warn).mock.calls.filter((call) => call[0] === '[chat] Unknown frame type')
    expect(unknownLogs, `${frame.type} is already handled (spec 4.10 / 4.8 / 4.7)`).toEqual([])
    expect(unknownWarns, `${frame.type} is not an unknown frame`).toEqual([])
  })
})
