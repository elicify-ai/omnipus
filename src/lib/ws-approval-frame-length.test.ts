/**
 * ws-approval-frame-length.test.ts — the browser half of the UTF-16 length
 * rule for approval frames (gate round 2: security S4, silent-failure SF-07,
 * architect N1; founder decision 2026-10-20).
 *
 * The producer (pkg/gateway/ws_tool_approval.go::fitsWireLength, b78b0e108)
 * omits acting_agent_name (128) or origin_label (200) whose UTF-16 code-unit
 * length exceeds the contract max, because the browser's generated validator
 * (src/lib/api/generated/ws-schemas.ts, zod z.string().max) compares
 * JavaScript String.length — UTF-16 code units, where one character outside
 * the Basic Multilingual Plane (U+20000) counts twice. A value that fits in
 * code points but not UTF-16 units made the browser reject the WHOLE
 * tool_approval_required frame and the WHOLE session_state snapshot, every
 * other pending card included.
 *
 * That browser side was argued from the Zod rule but never executed. These
 * tests execute it: the EXACT JSON shape the Go producer emits (field list
 * and omitempty behaviour of the generated ToolApprovalRequiredFrame and
 * SessionStatePendingApproval wire structs, as marshaled by
 * broadcastToolApprovalRequired and pendingApprovalSnapshot) is parsed
 * through the real parser — _parseServerFrame, reached the way production
 * reaches it, through WsConnection.onmessage (the inline path vitest's jsdom
 * Worker-less environment takes; src/workers/ws-parser.worker.ts::
 * parseServerFrameInWorker mirrors the same schemas).
 *
 * Frames follow the developer's GREEN Go fixtures
 * (pkg/gateway/train3_fix2_wire_length_test.go): exactly-128-UTF-16-unit
 * acting_agent_name built from 64 supplementary-plane characters, and the
 * over-limit forms one unit above the rule. Kill proof: lower the generated
 * max by one in a scratch copy and the at-limit cases fail.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { WsConnection, type ServerFrame } from './ws'

// ── The boundary values (UTF-16 code units, not code points) ──────────────────

// U+20000: one code point, TWO UTF-16 code units in JavaScript.
const SUPPLEMENTARY = '\u{20000}'

// Exactly 128 UTF-16 units: 64 supplementary characters × 2 units. Even unit
// split — a pure supplementary string can only hit even counts.
const NAME_AT_LIMIT = SUPPLEMENTARY.repeat(64)
// One unit over the 128 rule: the at-limit string plus one BMP character.
const NAME_ONE_OVER = NAME_AT_LIMIT + 'A'

// Exactly 200 UTF-16 units: "for group " (10 units) + 95 × 2.
const LABEL_AT_LIMIT = 'for group ' + SUPPLEMENTARY.repeat(95)
// One unit over the 200 rule.
const LABEL_ONE_OVER = LABEL_AT_LIMIT + 'A'

// Fixture self-guard: these strings must really be at the UTF-16 boundary, or
// every assertion below proves nothing. String.length IS the UTF-16 unit count.
expect(NAME_AT_LIMIT.length).toBe(128)
expect(NAME_ONE_OVER.length).toBe(129)
expect(LABEL_AT_LIMIT.length).toBe(200)
expect(LABEL_ONE_OVER.length).toBe(201)

// ── The Go producer's exact wire shapes ───────────────────────────────────────
//
// Field lists from the generated Go wire structs (ToolApprovalRequiredFrame,
// SessionStatePendingApproval, SessionStateFrame in
// pkg/api/generated/asyncapi_types.gen.go): required fields always present,
// optional fields omitted entirely when nil (Go omitempty), nothing else —
// the Zod schemas are .strict(), so one invented key would be its own bug.

function approvalFrame(fields: {
  approvalId: string
  agentId: string
  actingAgentName?: string
  originLabel?: string
}): string {
  return JSON.stringify({
    type: 'tool_approval_required',
    approval_id: fields.approvalId,
    tool_call_id: `call-${fields.approvalId}`,
    tool_name: 'bash',
    args: { command: 'echo hi' },
    agent_id: fields.agentId,
    session_id: `sess-${fields.approvalId}`,
    turn_id: `turn-${fields.approvalId}`,
    expires_in_ms: 600_000,
    ...(fields.actingAgentName !== undefined ? { acting_agent_name: fields.actingAgentName } : {}),
    ...(fields.originLabel !== undefined ? { origin_label: fields.originLabel } : {}),
  })
}

function pendingApproval(fields: {
  approvalId: string
  agentId: string
  actingAgentName?: string
  originLabel?: string
}): Record<string, unknown> {
  return {
    approval_id: fields.approvalId,
    session_id: `sess-${fields.approvalId}`,
    tool_name: 'bash',
    agent_id: fields.agentId,
    expires_in_ms: 600_000,
    tool_call_id: `call-${fields.approvalId}`,
    turn_id: `turn-${fields.approvalId}`,
    args: { command: 'echo hi' },
    ...(fields.actingAgentName !== undefined ? { acting_agent_name: fields.actingAgentName } : {}),
    ...(fields.originLabel !== undefined ? { origin_label: fields.originLabel } : {}),
  }
}

function sessionStateFrame(pending: Record<string, unknown>[]): string {
  return JSON.stringify({
    type: 'session_state',
    user_id: 'train3-fix2-browser',
    pending_approvals: pending,
    emitted_at: new Date().toISOString(),
  })
}

// ── WsConnection harness (the production parse path, Worker-less in jsdom) ────

interface MockWsInstance {
  onopen: (() => void) | null
  onmessage: ((ev: { data: string }) => void) | null
  onclose: ((ev: { code: number; reason: string }) => void) | null
  onerror: (() => void) | null
  send: ReturnType<typeof vi.fn>
  close: ReturnType<typeof vi.fn>
  readyState: number
}

let lastWsInstance: MockWsInstance | null = null

const MockWebSocket = vi.fn(function () {
  const instance: MockWsInstance = {
    onopen: null,
    onmessage: null,
    onclose: null,
    onerror: null,
    send: vi.fn(),
    close: vi.fn(),
    readyState: 1, // OPEN
  }
  lastWsInstance = instance
  return instance
}) as unknown as typeof WebSocket & { OPEN: number; CLOSED: number }

MockWebSocket.OPEN = 1
MockWebSocket.CLOSED = 3

function makeCallbacks() {
  // Production delivers through the batch onFrames callback (DEL-F19); fan
  // each frame out so per-frame assertions read naturally.
  const onFrame = vi.fn()
  return {
    onFrame,
    onFrames: (frames: ServerFrame[]) => {
      for (const frame of frames) onFrame(frame)
    },
    onConnected: vi.fn(),
    onDisconnected: vi.fn(),
    onError: vi.fn(),
    onReconnectStateChange: vi.fn(),
  }
}

/** Feed one raw server frame through the REAL onmessage parse path. */
function deliver(raw: string): void {
  lastWsInstance?.onmessage?.({ data: raw })
}

/** The batch flush is rAF-scheduled (~16 ms in jsdom); wait it out. */
async function flushDelivery(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 50))
}

beforeEach(() => {
  vi.stubGlobal('WebSocket', MockWebSocket)
  vi.stubGlobal('localStorage', { getItem: vi.fn(() => null) })
  vi.stubGlobal('sessionStorage', { getItem: vi.fn(() => null) })
})

afterEach(() => {
  currentConn?.disconnect()
  currentConn = null
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

let currentConn: WsConnection | null = null

async function connectedParser(): Promise<{ onFrame: ReturnType<typeof vi.fn> } & ReturnType<typeof makeCallbacks>> {
  const cbs = makeCallbacks()
  const conn = new WsConnection(cbs)
  currentConn = conn
  conn.connect()
  lastWsInstance?.onopen?.()
  return cbs
}

// ── The tests ─────────────────────────────────────────────────────────────────

describe('approval frames at the browser UTF-16 length rule (S4/SF-07/N1)', () => {
  it('accepts a tool_approval_required whose acting_agent_name is exactly 128 UTF-16 units of supplementary-plane characters', async () => {
    const cbs = await connectedParser()
    deliver(
      approvalFrame({
        approvalId: 'at-limit',
        agentId: 'uni-at',
        actingAgentName: NAME_AT_LIMIT,
      }),
    )
    await flushDelivery()

    expect(cbs.onFrame).toHaveBeenCalledTimes(1)
    const frame = cbs.onFrame.mock.calls[0][0] as { type: string; acting_agent_name?: string }
    expect(frame.type).toBe('tool_approval_required')
    expect(frame.acting_agent_name).toBe(NAME_AT_LIMIT)
    // The unit count is the whole point: 128 units of a character that counts
    // twice must still validate against z.string().max(128).
    expect(frame.acting_agent_name?.length).toBe(128)
  })

  it('rejects the whole frame when acting_agent_name is one UTF-16 unit over the limit', async () => {
    const cbs = await connectedParser()
    deliver(
      approvalFrame({
        approvalId: 'one-over',
        agentId: 'uni-over',
        actingAgentName: NAME_ONE_OVER,
      }),
    )
    await flushDelivery()

    // One unit over the generated max makes the browser drop the WHOLE frame
    // — the reason the producer must omit such a name (b78b0e108).
    expect(cbs.onFrame).not.toHaveBeenCalled()
  })

  it('rejects the whole frame when origin_label is one UTF-16 unit over the limit', async () => {
    const cbs = await connectedParser()
    deliver(
      approvalFrame({
        approvalId: 'label-over',
        agentId: 'plain',
        actingAgentName: 'Plain Helper',
        originLabel: LABEL_ONE_OVER,
      }),
    )
    await flushDelivery()

    expect(cbs.onFrame).not.toHaveBeenCalled()
  })

  it('accepts an origin_label exactly at the 200-unit limit', async () => {
    const cbs = await connectedParser()
    deliver(
      approvalFrame({
        approvalId: 'label-at',
        agentId: 'plain',
        actingAgentName: 'Plain Helper',
        originLabel: LABEL_AT_LIMIT,
      }),
    )
    await flushDelivery()

    expect(cbs.onFrame).toHaveBeenCalledTimes(1)
    const frame = cbs.onFrame.mock.calls[0][0] as { origin_label?: string }
    expect(frame.origin_label).toBe(LABEL_AT_LIMIT)
    expect(frame.origin_label?.length).toBe(200)
  })

  it('parses a session_state snapshot with an omitted, an at-limit and a plain acting_agent_name — all three entries intact', async () => {
    const cbs = await connectedParser()
    deliver(
      sessionStateFrame([
        // The producer omitted the over-long name: the key is ABSENT.
        pendingApproval({ approvalId: 'snap-omitted', agentId: 'uni-over' }),
        pendingApproval({
          approvalId: 'snap-at-limit',
          agentId: 'uni-at',
          actingAgentName: NAME_AT_LIMIT,
          originLabel: LABEL_AT_LIMIT,
        }),
        pendingApproval({
          approvalId: 'snap-plain',
          agentId: 'plain',
          actingAgentName: 'Plain Helper',
          originLabel: 'asked by Plain Helper',
        }),
      ]),
    )
    await flushDelivery()

    expect(cbs.onFrame).toHaveBeenCalledTimes(1)
    const frame = cbs.onFrame.mock.calls[0][0] as {
      type: string
      pending_approvals: Array<Record<string, unknown>>
    }
    expect(frame.type).toBe('session_state')
    expect(frame.pending_approvals).toHaveLength(3)

    const byId = new Map(frame.pending_approvals.map((p) => [p.approval_id as string, p]))
    const omitted = byId.get('snap-omitted')
    const atLimit = byId.get('snap-at-limit')
    const plain = byId.get('snap-plain')
    expect(omitted, 'the omitted-name entry is listed').toBeTruthy()
    expect(atLimit, 'the at-limit entry is listed').toBeTruthy()
    expect(plain, 'the plain entry is listed').toBeTruthy()

    // Omitted means ABSENT on the parsed object — the strict schema never
    // sees the key, and the card still arrives.
    expect('acting_agent_name' in (omitted ?? {})).toBe(false)
    expect(atLimit?.acting_agent_name).toBe(NAME_AT_LIMIT)
    expect((atLimit?.acting_agent_name as string).length).toBe(128)
    expect(atLimit?.origin_label).toBe(LABEL_AT_LIMIT)
    expect(plain?.acting_agent_name).toBe('Plain Helper')
    expect(plain?.origin_label).toBe('asked by Plain Helper')
    // The three entries survive intact: each keeps its required identity.
    expect(omitted?.tool_call_id).toBe('call-snap-omitted')
    expect(atLimit?.tool_call_id).toBe('call-snap-at-limit')
    expect(plain?.tool_call_id).toBe('call-snap-plain')
  })
})
