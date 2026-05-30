// ws-parser.worker.ts — off-main-thread WS frame Zod parsing.
// Receives: { id: number; raw: string }   Posts back: { id: number; frame: ServerFrame | null; droppedReason?: string }

import { WsFrame as WsFrameSchema, WsFrameType as WsFrameTypeSchema } from '@/lib/api/generated/schemas'
import { ClientFrameTypes } from '@/lib/api/generated/asyncapi-types'
import type { ServerFrame } from '@/lib/api/generated/asyncapi-types'

const CLIENT_FRAME_TYPES = new Set<string>(ClientFrameTypes)

// ── safeJsonParse ─────────────────────────────────────────────────────────────

function safeJsonParse(data: unknown): { ok: true; raw: unknown } | { ok: false; reason: string } {
  if (typeof data === 'string') {
    try {
      return { ok: true, raw: JSON.parse(data) }
    } catch {
      return { ok: false, reason: 'JSON parse error' }
    }
  }
  return { ok: false, reason: 'non-string input' }
}

// ── parseServerFrameInWorker ──────────────────────────────────────────────────
//
// Mirrors _parseServerFrame from ws.ts. Returns the parsed frame or null, plus
// a droppedReason string for observability (logged in main thread).

function parseServerFrameInWorker(
  data: unknown,
): { frame: ServerFrame; droppedReason?: never } | { frame: null; droppedReason: string } {
  const parsed = safeJsonParse(data)
  if (!parsed.ok) {
    return { frame: null, droppedReason: `json: ${parsed.reason}` }
  }
  const raw = parsed.raw

  const frameType =
    raw !== null && typeof raw === 'object' && 'type' in (raw as object)
      ? String((raw as Record<string, unknown>).type)
      : '_unknown'

  const result = WsFrameSchema.safeParse(raw)
  if (result.success) {
    if (CLIENT_FRAME_TYPES.has(frameType)) {
      return {
        frame: null,
        droppedReason: `client-direction frame spoofed from server (type="${frameType}")`,
      }
    }
    return { frame: result.data as ServerFrame }
  }

  // Forward-compat: unknown type not yet in spec.
  const knownTypes: readonly string[] = WsFrameTypeSchema.options
  const isKnownType = knownTypes.includes(frameType)

  if (frameType !== '_unknown' && !CLIENT_FRAME_TYPES.has(frameType) && !isKnownType) {
    return {
      frame: null,
      droppedReason: `unknown-type frame (type="${frameType}") — add to spec if this is a new server frame`,
    }
  }

  const first = result.error.issues[0]
  const desc = first
    ? `${first.path.join('.') || 'root'}: ${first.message}`
    : result.error.message
  return { frame: null, droppedReason: `invalid frame (${frameType}): ${desc}` }
}

// ── Worker message handler ────────────────────────────────────────────────────

export interface WorkerRequest {
  id: number
  raw: string
}

export interface WorkerResponse {
  id: number
  frame: ServerFrame | null
  droppedReason?: string
}

// Dedicated-worker postMessage handler. The "origin check missing" rule
// (js/missing-origin-check) is a generic warning that applies to window
// message handlers; this is a DedicatedWorker invoked from the same origin
// via new Worker(import.meta.url) (see src/store/ws-worker-bridge.ts), so
// cross-origin postMessage cannot reach this handler — only the page that
// instantiated the worker can talk to it. The DedicatedWorkerGlobalScope
// MessageEvent's .origin field is always the empty string for same-origin
// dedicated workers, so we don't gate on it; we only validate the payload
// shape before doing any work.
self.onmessage = (event: MessageEvent<WorkerRequest>) => {
  const data = event.data
  if (!data || typeof data !== 'object' || typeof data.id !== 'number' || typeof data.raw !== 'string') {
    return
  }
  const result = parseServerFrameInWorker(data.raw)
  const response: WorkerResponse = {
    id: data.id,
    frame: result.frame,
    droppedReason: result.droppedReason,
  }
  self.postMessage(response)
}
