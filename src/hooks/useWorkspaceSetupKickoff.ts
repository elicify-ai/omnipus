// useWorkspaceSetupKickoff — fires Ava's (or whichever agent leads
// the workspace's core_team) workspace-setup interview exactly once, the
// first time a freshly-created workspace (server-seeded `setup_pending:
// true` — see POST /api/v1/workspaces) is opened. See
// useChatStore.sendWorkspaceSetupKickoff for the wire mechanics (kickoff
// content, `metadata.workspace_setup_kickoff`, new-turn placeholder).
//
// Three independent layers guard against a duplicate kickoff:
//   1. This hook's own per-workspace-id guard (below) — the primary
//      defense against re-firing within one component lifetime (repeated
//      renders, dependency churn, etc.). This guard now lives in the
//      chat store's `kickoffAttemptStatus` map, not a component-local
//      `useRef` — a `useRef` can only ever record "fired", with no path
//      back to "retry" once a kickoff terminally fails. The store-backed
//      guard has a real state machine (in-flight → done | failed) and
//      'failed' does NOT block a future fire, so a genuine server-side
//      failure can be retried once the workspace is (re)opened and server
//      truth confirms `setup_pending` is still true.
//   2. The optimistic queryClient.setQueryData cache clear (below) — so a
//      remount that re-reads the workspace lists/detail before the server's
//      own state has caught up still sees `setup_pending: false`. On
//      a terminal FAILURE this optimistic write is premature (the server
//      contract restores `setup_pending: true` for a genuine failure) — the
//      invalidate-on-failure effect below corrects it back to server truth.
//   3. Server-side rejection of a duplicate kickoff frame — the final,
//      authoritative backstop (the backend clears `setup_pending` on first
//      accepted kickoff and errors a repeat).
//
// Wired into WorkspaceTabContainer (called unconditionally, once the
// workspace is resolved) so it fires regardless of which tab is active —
// frames are handled at the chat-store level, so Ava's greeting streams
// into the chat bucket even while the user is looking at the Board tab.

import { useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { Agent, Workspace } from '@/lib/api'
import { workspacesQueryKeys } from '@/lib/api'
import { useChatAgents } from '@/hooks/useChatAgents'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import { logDiagnostic } from '@/lib/telemetry'

/**
 * Resolve the workspace's kickoff-lead agent.
 *   1. 'ava' when she is chat-eligible (present, active/idle, non-worker —
 *      the same `chatAgents` list AgentPicker and the "@" mention menu use)
 *      — matches the seeding default regardless of `core_team` ordering.
 *   2. Otherwise the first chat-eligible `core_team` member.
 *   3. Otherwise `null` — the caller logs and bails rather than returning
 *      silently forever (the original bug: `core_team?.[0] ?? 'ava'` with
 *      no chat-eligible match just returns `undefined` from `.find()` with
 *      no signal of WHY, every render, forever).
 */
function resolveKickoffAgent(workspace: Workspace, chatAgents: Agent[]): Agent | null {
  const ava = chatAgents.find((a) => a.id === 'ava')
  if (ava) return ava
  for (const id of workspace.core_team ?? []) {
    const candidate = chatAgents.find((a) => a.id === id)
    if (candidate) return candidate
  }
  return null
}

export function useWorkspaceSetupKickoff(workspace: Workspace | undefined): void {
  const queryClient = useQueryClient()
  const isConnected = useConnectionStore((s) => s.isConnected)
  const activeSessionId = useSessionStore((s) => s.activeSessionId)
  const { chatAgents } = useChatAgents()
  // Reactive read — this effect exists SOLELY to react to the
  // ASYNC transition into 'failed' (a server-side reject or a hard
  // disconnect, both learned about only via chat.ts's own WS-frame
  // handling long after this hook's own send call returned). The fire
  // effect below deliberately does NOT subscribe to this value (it reads
  // it fresh via getState() instead) — see that effect's own comment for
  // why a reactive subscription there would risk a tight retry loop.
  const kickoffStatus = useChatStore((s) => (workspace ? s.kickoffAttemptStatus[workspace.id] : undefined))

  useEffect(() => {
    if (!workspace) return
    // Never kick off an archived workspace — there is no live team to
    // interview a user into building, and re-activating a workspace is not
    // this hook's concern.
    if (workspace.status !== 'active') return
    if (!workspace.setup_pending) return
    if (!isConnected) return
    if (activeSessionId !== null) return
    // Non-reactive read on purpose — this effect only re-runs when
    // `workspace`/`isConnected`/`activeSessionId`/`chatAgents`/`queryClient`
    // change, the same triggers that would
    // have re-run it under the old `useRef` guard. A REACTIVE subscription
    // to `kickoffAttemptStatus` here would re-run this effect the instant a
    // fire flips the status to 'in-flight' (and again the instant a failure
    // flips it to 'failed') — and if a failure keeps recurring, that would
    // fire-fail-retry in a synchronous loop with no real-world gap between
    // attempts. Reading fresh state at the moment the effect naturally runs
    // for some OTHER reason keeps retries gated on genuine external
    // signals (the workspace object refreshing after the invalidate-on-
    // failure effect's refetch, isConnected flipping, etc.), exactly like
    // the old ref never re-fired on its own removal alone.
    if (useChatStore.getState().kickoffAttemptStatus[workspace.id] === 'in-flight') return

    const agent = resolveKickoffAgent(workspace, chatAgents)
    if (!agent) {
      // Never silent-forever without a log — if the workspace's
      // core_team names only non-chat-eligible agents (deleted, archived,
      // or still loading) AND Ava herself isn't chat-eligible either, this
      // workspace can never be auto-kicked-off. That's a real, debuggable
      // condition, not a "just wait" one.
      console.debug('[useWorkspaceSetupKickoff] no chat-eligible lead agent for workspace setup kickoff', {
        workspaceId: workspace.id,
        coreTeam: workspace.core_team,
      })
      logDiagnostic('chatWorkspaceSetupKickoffNoAgent', { workspaceId: workspace.id, coreTeam: workspace.core_team })
      return
    }

    // Claim the guard BEFORE sending — a synchronous re-render triggered by
    // the send itself (setActiveSession, bucket writes) must not re-enter
    // this effect body and fire a second frame. This is now a store
    // write (kickoffAttemptStatus[workspace.id] = 'in-flight'), independent
    // of whatever `sendWorkspaceSetupKickoff` itself does internally with
    // `pendingKickoff` — the two guards serve different layers (see the
    // module doc comment above).
    useChatStore.getState().markKickoffInFlight(workspace.id)

    const sent = useChatStore.getState().sendWorkspaceSetupKickoff({
      workspaceId: workspace.id,
      workspaceName: workspace.name,
      agentId: agent.id,
      agentType: agent.type ?? null,
    })

    if (!sent) {
      // The store bailed (offline / mid-stream / a session raced in between
      // the guard reads above and the store's own re-check, or a full
      // rollback after a failed send, or a DIFFERENT kickoff still
      // outstanding via the store's `pendingKickoff` guard) — release the
      // guard so a LATER render (e.g. once isConnected flips true, or the
      // workspace prop refreshes) retries.
      useChatStore.getState().resolveKickoffAttempt(workspace.id, 'failed')
      return
    }

    // Optimistically clear the flag on every cache a consumer might read it
    // from, mirroring the established pattern in WorkspaceTeamTab.tsx (the
    // no-op `invalidateQueries({queryKey: workspacesQueryKeys.list()})`
    // pitfall documented there applies here too — write the exact
    // {status:'active'}/{status:'archived'} keys directly rather than
    // relying on partial-match invalidation). This prevents a remount from
    // re-reading `setup_pending: true` before the server's own state (which
    // the backend clears on accepting this kickoff) is refetched.
    //
    // cancelQueries() FIRST for every key this write touches — an
    // in-flight refetch that read the workspace list/detail from disk
    // BEFORE this optimistic clear can otherwise land its (stale,
    // setup_pending:true) result AFTER setQueryData below, silently
    // resurrecting the flag and letting a later remount refire the kickoff.
    //
    // This write is PREMATURE if the kickoff goes on to terminally
    // FAIL (the server contract restores `setup_pending:true` in that case)
    // — the invalidate-on-failure effect below corrects it back to server
    // truth once that happens.
    const clearFlag = (list: Workspace[] | undefined) =>
      list?.map((w) => (w.id === workspace.id ? { ...w, setup_pending: false } : w))
    const listKeys = (['active', 'archived'] as const).map((status) => workspacesQueryKeys.list({ status }))
    const detailKey = workspacesQueryKeys.detail(workspace.id)
    void Promise.all([...listKeys, detailKey].map((key) => queryClient.cancelQueries({ queryKey: key }))).then(() => {
      for (const key of listKeys) {
        queryClient.setQueryData<Workspace[]>(key, clearFlag)
      }
      queryClient.setQueryData<Workspace>(detailKey, (w) => (w ? { ...w, setup_pending: false } : w))
    })
  }, [workspace, isConnected, activeSessionId, chatAgents, queryClient])

  // Honest retry: reacts to an ASYNC terminal failure — a server
  // reject or a hard disconnect, both surfaced only via chat.ts's own
  // WS-frame handling, long after the fire effect above already returned.
  // The fire effect's own optimistic setup_pending:false cache write (just
  // above) is premature for a genuine failure — invalidate (not just patch)
  // so real server truth replaces it: a genuine failure comes back
  // `setup_pending:true` (a later re-render of the fire effect, once this
  // refetch lands a fresh `Workspace` object, honestly decides whether to
  // retry); a DUPLICATE rejection comes back `setup_pending:false` (already
  // correctly cleared by the EARLIER, actually-successful attempt), which
  // the fire effect's own early-return guard naturally leaves alone — no
  // retry loop.
  useEffect(() => {
    if (!workspace) return
    if (kickoffStatus !== 'failed') return
    void queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.list({ status: 'active' }) })
    void queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.list({ status: 'archived' }) })
    void queryClient.invalidateQueries({ queryKey: workspacesQueryKeys.detail(workspace.id) })
  }, [kickoffStatus, workspace, queryClient])
}
