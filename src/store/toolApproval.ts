// Zustand store for tool approval queue (FR-011, FR-082).
// Manages pending tool-policy approvals that arrive via the tool_approval_required
// WS event and are resolved via POST /api/v1/tool-approvals/{id}.
//
// Design:
// - Approvals are queued in arrival order; the modal shows one at a time.
// - expires_in_ms is relative to receipt: expiresAt = Date.now() + expires_in_ms.
// - session_state reset (FR-052, FR-073, FR-081): on WS connect, clear any
//   queued approvals NOT present in the session_state payload, refresh
//   expiresAt for those that are, and add a STUB entry for any approval the
//   server still reports pending that this tab never saw a live
//   tool_approval_required frame for — see reconcileWithSessionState's own
//   comment for why (the reconnect-gap fix) and ToolApprovalModal.tsx's
//   isReconnectStub for how the stub renders.
// - Server-confirmed resolution: a tool_approval_resolved frame, or a 404/410
//   answer to an action, means the server no longer holds the approval open.
//   markResolved drops it AND remembers the id (resolvedIds), so a
//   session_state snapshot built before the resolution but delivered after it
//   cannot put the dialog back.
// - Scope: an approval carries the workspace of the session that asked for it.
//   isApprovalInScope decides whether it is shown for the active workspace.

import { create } from 'zustand'
import type { WsToolApprovalRequiredFrame, WsSessionStateFrame } from '@/lib/ws'
import type { CommandSegmentInfo } from '@/lib/api/generated/asyncapi-types'

export interface PendingToolApproval {
  approvalId: string
  toolCallId: string
  toolName: string
  args: Record<string, unknown>
  agentId: string
  sessionId: string
  turnId: string
  /** Absolute local clock expiry (ms). Computed as Date.now() + expires_in_ms on receipt. */
  expiresAt: number
  /**
   * Workspace of the session that asked (frame workspace_id). Undefined when
   * that session belongs to no workspace — such an approval is in every scope.
   */
  workspaceId?: string
  /**
   * ADR-092 D4: per-segment breakdown of a chained shell command
   * (`splitShellSegments`), present only on a live tool_approval_required
   * frame for a command the D3 matcher could not fully resolve. Undefined —
   * not an empty array — when the frame carried none (non-bash tool, an
   * unchained command, or a backend build that does not populate it yet);
   * ToolApprovalModal.tsx treats undefined/empty identically (no segment
   * breakdown shown). A reconnect stub (see reconcileWithSessionState below)
   * never has this — SessionStatePendingApproval carries no segments field.
   */
  segments?: CommandSegmentInfo[]
}

/**
 * How many server-confirmed resolved ids to remember. Only has to outlast the
 * window in which an already-built snapshot can still be in flight, so a
 * small bound is plenty; the list resets on reload, when the server's fresh
 * snapshot is authoritative anyway.
 */
export const RESOLVED_APPROVAL_MEMORY = 200

/**
 * Whether an approval belongs on screen for the active workspace. An approval
 * with no workspace, or a view with no active workspace ("All workspaces"),
 * shows everywhere; otherwise the two must match.
 */
export function isApprovalInScope(
  approval: Pick<PendingToolApproval, 'workspaceId'>,
  activeWorkspaceId: string | null,
): boolean {
  if (!approval.workspaceId || !activeWorkspaceId) return true
  return approval.workspaceId === activeWorkspaceId
}

interface ToolApprovalStore {
  /** Ordered queue of pending approvals. The first entry is the currently displayed one. */
  queue: PendingToolApproval[]

  /** Ids the server confirmed are no longer pending (most recent last, bounded). */
  resolvedIds: string[]

  /** Add an approval from a WS tool_approval_required frame. */
  enqueue: (frame: WsToolApprovalRequiredFrame) => void

  /**
   * Remove an approval from THIS tab only. Does not claim the server resolved
   * it: if the server still holds it pending, the next session_state snapshot
   * brings it back (as a reconnect stub), because the agent is still waiting.
   */
  dequeue: (approvalId: string) => void

  /**
   * Remove an approval the server confirmed is no longer pending (a
   * tool_approval_resolved frame, or a 404/410 answer to an action) and
   * remember its id so no later snapshot or duplicate frame resurrects it.
   */
  markResolved: (approvalId: string) => void

  /**
   * Reconcile the queue with a session_state reset frame (FR-052, FR-081).
   * Removes any queued approvals NOT present in the session_state payload,
   * refreshes expiresAt for those that are, and adds a stub entry for any
   * approval the server reports pending that this tab has no local entry for
   * (see this method's own comment for the full reconnect-gap rationale).
   * Ids in resolvedIds are ignored even if the snapshot still lists them.
   */
  reconcileWithSessionState: (frame: WsSessionStateFrame) => void
}

export const useToolApprovalStore = create<ToolApprovalStore>((set) => ({
  queue: [],
  resolvedIds: [],

  enqueue: (frame) => {
    const expiresAt = Date.now() + frame.expires_in_ms
    set((state) => {
      // A resolution already reached this tab (frames can cross): never show it.
      if (state.resolvedIds.includes(frame.approval_id)) return state
      // Deduplicate: if already queued, replace with updated expiry
      const existing = state.queue.findIndex((a) => a.approvalId === frame.approval_id)
      if (existing !== -1) {
        const updated = [...state.queue]
        updated[existing] = {
          ...updated[existing],
          expiresAt,
          segments: frame.segments,
        }
        return { queue: updated }
      }
      return {
        queue: [
          ...state.queue,
          {
            approvalId: frame.approval_id,
            toolCallId: frame.tool_call_id,
            toolName: frame.tool_name,
            args: frame.args,
            agentId: frame.agent_id,
            sessionId: frame.session_id,
            turnId: frame.turn_id,
            expiresAt,
            workspaceId: frame.workspace_id,
            segments: frame.segments,
          },
        ],
      }
    })
  },

  dequeue: (approvalId) => {
    set((state) => ({
      queue: state.queue.filter((a) => a.approvalId !== approvalId),
    }))
  },

  markResolved: (approvalId) => {
    set((state) => ({
      queue: state.queue.filter((a) => a.approvalId !== approvalId),
      resolvedIds: state.resolvedIds.includes(approvalId)
        ? state.resolvedIds
        : [...state.resolvedIds, approvalId].slice(-RESOLVED_APPROVAL_MEMORY),
    }))
  },

  reconcileWithSessionState: (frame) => {
    set((state) => {
      const resolved = new Set(state.resolvedIds)
      const livePending = frame.pending_approvals.filter((a) => !resolved.has(a.approval_id))
      const liveIds = new Set(livePending.map((a) => a.approval_id))
      const existingIds = new Set(state.queue.map((a) => a.approvalId))

      // Keep only those still in the server's live set; refresh their expiresAt.
      const refreshed = state.queue
        .filter((a) => liveIds.has(a.approvalId))
        .map((a) => {
          const serverEntry = livePending.find((s) => s.approval_id === a.approvalId)
          if (!serverEntry) return a
          return {
            ...a,
            expiresAt: Date.now() + serverEntry.expires_in_ms,
            workspaceId: serverEntry.workspace_id ?? a.workspaceId,
          }
        })

      // Reconnect-gap fix. The server still considers some of these
      // approvals pending even though THIS tab has never seen them — e.g.
      // the page was reloaded (the in-memory queue reset to empty) while an
      // approval was outstanding, or a second tab connected after the
      // approval was already created. Before this, such an approval simply
      // never entered the queue: not a blank dialog, no dialog at all, while
      // the agent sat blocked for the full expiry window with no UI anywhere
      // to act on it (ToolApprovalModal is the ONLY tool-approval surface).
      //
      // SessionStatePendingApproval (this frame's pending_approvals shape)
      // carries only approval_id/session_id/tool_name/agent_id/expires_in_ms
      // (+ optional workspace_id) — no args, tool_call_id, or turn_id — so
      // the original request cannot be reconstructed here. toolCallId and
      // turnId are stubbed to '' rather than omitted or invented:
      // ToolApprovalRequiredFrame (the WS frame enqueue() above consumes)
      // requires BOTH at minLength 1, so a genuine live frame can never
      // produce this shape. '' is therefore a collision-free sentinel, not
      // merely an unlikely one — do not read the two empty strings below as a
      // bug. ToolApprovalModal.tsx's isReconnectStub reads exactly this
      // (toolCallId === '' && turnId === '') and renders an honest "can't
      // show what's being asked" card with only Deny offered, instead of a
      // normal-looking card with suspiciously empty arguments.
      const newStubs: PendingToolApproval[] = livePending
        .filter((s) => !existingIds.has(s.approval_id))
        .map((s) => ({
          approvalId: s.approval_id,
          toolCallId: '',
          toolName: s.tool_name,
          args: {},
          agentId: s.agent_id,
          sessionId: s.session_id,
          turnId: '',
          // Same relative-delta-to-absolute-clock conversion as enqueue()
          // above: expires_in_ms is relative to THIS frame's receipt, never
          // an absolute timestamp — treating it as one would place expiresAt
          // near the 1970 epoch and render every reconnected approval as
          // already expired. The server only reports approvals it still
          // considers open, so pending_approvals never carries one that
          // already expired server-side; the Date.now() + delta conversion
          // still matters so a delta that happens to arrive at/near 0 lands
          // exactly on ToolApprovalModal.tsx's existing hasExpired state
          // (Dismiss-only) rather than rendering as freshly askable.
          expiresAt: Date.now() + s.expires_in_ms,
          workspaceId: s.workspace_id,
        }))

      return { queue: [...refreshed, ...newStubs] }
    })
  },
}))
