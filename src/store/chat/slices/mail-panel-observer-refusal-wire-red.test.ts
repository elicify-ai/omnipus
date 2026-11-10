// RED: PANEL-MAIL-REFUSAL-WIRE-RED-1817 / foreground TW2 A decision.
// Independent oracle: ERROR IDs only are optional string(minLength:1), omit
// unavailable correlations; required type/code/error and strict extras remain.
// Exactly one mailPanelObserverRefused diagnostic carries the refusal code.
// REQUEST/ACK remain required nonempty IDs. No repair of generated schemas.
//
// These are explicitly CONTRACT-DERIVED schema inputs, not a claim to replay
// captured server bytes. The disjoint Go test proves the real producer's wire
// omission independently. Real generated WsFrame parsing and real ChatStore
// handleFrame remain inside this test boundary; only final logging is observed.
//
// Fresh CHECK mutation: remove only the production diagnostic call in a private
// isolated copy. Both valid-ID malformed-frame and omitted-ID tests must fail on
// this file's exact single diagnostic assertion once the contract is fixed.

import { beforeEach, describe, expect, it, vi } from 'vitest'
import {
  WsFrame as WsFrameSchema,
  MailPanelObserverFrame,
  MailPanelObserverAckFrame,
  MailPanelObserverErrorFrame,
} from '@/lib/api/generated/ws-schemas'
import { logDiagnostic } from '@/lib/telemetry'
import { useChatStore } from '../store'
import { handleMailPanelObserverFrame } from './mail-panel-observer-frames'

vi.mock('@/lib/telemetry', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/telemetry')>()
  return { ...actual, logDiagnostic: vi.fn() }
})

const observerId = 'qa-mail-observer'
const workspaceId = 'qa-mail-workspace'
const validRefusal = {
  type: 'mail_panel_observer_error',
  code: 'malformed_frame',
  error: 'The presence request was refused.',
  observer_id: observerId,
  workspace_id: workspaceId,
}

beforeEach(() => {
  vi.mocked(logDiagnostic).mockClear()
  useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {} })
})

function decodeRefusal(input: unknown) {
  const decoded = WsFrameSchema.safeParse(input)
  expect(
    decoded.success,
    `TW2 A: generated client decoder must accept the legitimate refusal; actual issues=${decoded.success ? 'none' : JSON.stringify(decoded.error.issues)}`,
  ).toBe(true)
  if (!decoded.success) throw decoded.error
  expect(decoded.data.type, 'TW2 forbids a generic ErrorFrame workaround').toBe('mail_panel_observer_error')
  if (decoded.data.type !== 'mail_panel_observer_error') {
    throw new Error('Generated decoder returned a non-Mail refusal')
  }
  return decoded.data
}

function expectOneRefusalDiagnostic(code: string): void {
  // Exact number, exact sink/event, exact required code; no other diagnostic
  // is allowed. Optional diagnostic metadata is not invented as a new spec.
  expect(vi.mocked(logDiagnostic).mock.calls).toEqual([
    ['mailPanelObserverRefused', expect.objectContaining({ code })],
  ])
}

describe('TW2 generated decoder to real frame handler', () => {
  it.each(['malformed_frame', 'unauthorized_workspace'])('valid IDs: %s reaches exactly one diagnostic (positive control)', (code) => {
    const input = { ...validRefusal, code }
    const decoded = decodeRefusal(input)
    expect(decoded).toEqual(input)
    useChatStore.getState().handleFrame(decoded)
    expectOneRefusalDiagnostic(code)
  })

  const optionalCorrelations = [
    { label: 'neither identifier', identifiers: {} },
    { label: 'only recoverable observer identifier', identifiers: { observer_id: observerId } },
    { label: 'only recoverable workspace identifier', identifiers: { workspace_id: workspaceId } },
  ]
  for (const code of ['malformed_frame', 'unauthorized_workspace']) {
    it.each(optionalCorrelations)('omitted unavailable IDs: $label survives generated decode and one diagnostic for '+code, ({ identifiers }) => {
      const input = {
        type: 'mail_panel_observer_error', code, error: 'The presence request was refused.', ...identifiers,
      }
      const decoded = decodeRefusal(input)
      expect(decoded).toEqual(input)
      useChatStore.getState().handleFrame(decoded)
      expectOneRefusalDiagnostic(code)
    })
  }

  it('valid strict ACK reaches the real handler without a refusal diagnostic', () => {
    const input = { type: 'mail_panel_observer_ack', action: 'open', observer_id: observerId, workspace_id: workspaceId }
    const decoded = WsFrameSchema.parse(input)
    expect(decoded).toEqual(input)
    if (decoded.type !== 'mail_panel_observer_ack') throw new Error('Expected generated ACK')
    useChatStore.getState().handleFrame(decoded)
    expect(vi.mocked(logDiagnostic).mock.calls).toEqual([])
  })

  it('unrelated generated pong remains unconsumed by Mail with no refusal diagnostic', () => {
    const decoded = WsFrameSchema.parse({ type: 'pong' })
    expect(decoded).toEqual({ type: 'pong' })
    if (decoded.type !== 'pong') throw new Error('Expected generated pong')
    expect(handleMailPanelObserverFrame(decoded)).toBe(false)
    expect(vi.mocked(logDiagnostic).mock.calls).toEqual([])
  })
})

describe('TW2 error-only optionality cannot weaken accepted identifiers', () => {
  it.each([
    { label: 'REQUEST', type: 'mail_panel_observer', schema: MailPanelObserverFrame },
    { label: 'ACK', type: 'mail_panel_observer_ack', schema: MailPanelObserverAckFrame },
  ])('$label still accepts exact min/min+1 IDs and rejects missing/empty/nonstring IDs', ({ type, schema }) => {
    // Contract minimum is 1; 0 is tested below. No maximum is invented.
    for (const id of ['q', 'qa']) {
      const input = { type, action: 'open', observer_id: id, workspace_id: id }
      expect(schema.parse(input)).toEqual(input)
      for (const key of ['observer_id', 'workspace_id']) {
        const missing = { ...input }
        expect(Reflect.deleteProperty(missing, key)).toBe(true)
        expect(schema.safeParse(missing).success, `${type}: missing ${key}`).toBe(false)
        for (const value of ['', null, 7]) {
          expect(schema.safeParse({ ...input, [key]: value }).success, `${type}: invalid ${key}=${JSON.stringify(value)}`).toBe(false)
        }
      }
    }
  })

  it('an ERROR correlation, when present, must still be a nonempty string', () => {
    expect(MailPanelObserverErrorFrame.parse(validRefusal)).toEqual(validRefusal)
    for (const key of ['observer_id', 'workspace_id']) {
      for (const value of ['', null, 7, false, {}, []]) {
        expect(MailPanelObserverErrorFrame.safeParse({ ...validRefusal, [key]: value }).success,
          `present ERROR ${key}=${JSON.stringify(value)} must not be accepted`).toBe(false)
      }
    }
    expect(vi.mocked(logDiagnostic).mock.calls).toEqual([])
  })

  it('ERROR required fields, code enum and additionalProperties stay strict', () => {
    expect(MailPanelObserverErrorFrame.parse(validRefusal)).toEqual(validRefusal)
    for (const key of ['type', 'code', 'error']) {
      const missing = { ...validRefusal }
      expect(Reflect.deleteProperty(missing, key)).toBe(true)
      expect(MailPanelObserverErrorFrame.safeParse(missing).success, `required ERROR ${key}`).toBe(false)
    }
    expect(MailPanelObserverErrorFrame.safeParse({ ...validRefusal, code: 'qa_unknown_code' }).success).toBe(false)
    expect(MailPanelObserverErrorFrame.safeParse({ ...validRefusal, qa_extra: true }).success).toBe(false)
    expect(vi.mocked(logDiagnostic).mock.calls).toEqual([])
  })
})
