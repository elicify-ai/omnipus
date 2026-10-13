// useOpenOmnipus — the one click handler behind every way into Omnipus's chat:
// the fixed sidebar entry, Team's "Create with Omnipus", the sidebar's
// per-workspace "Add agent", and "Set up a team" (omnipus-system-agent
// FR-045, FR-100, FR-103; BDD-4.1, BDD-5.2, BDD-12.1).
//
// Reads the workspace list and the session list from the shared query cache
// (the sidebar keeps both warm) instead of subscribing, so a button does not
// re-render on every poll. The attach and any workflow message go out inside
// the click (src/store/omnipusChat.ts); only then does the view move to
// /omnipus. The active workspace never changes.
import { useCallback, useContext } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { QueryClientContext } from '@tanstack/react-query'
import { queryClient as appQueryClient } from '@/lib/queryClient'
import type { Session, Workspace } from '@/lib/api'
import { workspacesQueryKeys } from '@/lib/api'
import { openOmnipusChat, type OmnipusWorkflowRequest, type OpenOmnipusOutcome } from '@/store/omnipusChat'
import { useUiStore } from '@/store/ui'

export function useOpenOmnipus(): (workflow?: OmnipusWorkflowRequest) => OpenOmnipusOutcome {
  const navigate = useNavigate()
  // The provider's client when there is one (the app and most tests); the
  // app singleton otherwise — a sidebar row must not crash outside a provider.
  const queryClient = useContext(QueryClientContext) ?? appQueryClient
  return useCallback(
    (workflow?: OmnipusWorkflowRequest) => {
      const workspaces = queryClient.getQueryData<Workspace[]>(workspacesQueryKeys.list({ status: 'active' })) ?? []
      const sessions = queryClient.getQueryData<Session[]>(['sessions']) ?? []
      const outcome = openOmnipusChat({ workspaces, sessions, workflow })
      if (outcome === 'unavailable') {
        // Show the route's unavailable state with Retry rather than doing
        // nothing: the route re-reads the lists and offers Retry.
        useUiStore.getState().addToast({
          message: 'Omnipus is unavailable right now. Retry from the Omnipus chat.',
          variant: 'warning',
        })
      }
      if (outcome !== 'attach-failed') void navigate({ to: '/omnipus' })
      return outcome
    },
    [navigate, queryClient],
  )
}
