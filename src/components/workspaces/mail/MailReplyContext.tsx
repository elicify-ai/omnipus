/**
 * MailReplyContext.tsx — the Reply / Reply all compose prefill (ADR-20261001
 * F5; w4 spec US-5). The panel wave integrates this hook into the compose
 * flow (MailPanel.tsx is that wave's file); the integration points are in
 * the w4 build report.
 *
 * The recipients come from the server's ONE shared rule
 * (BuildReplyRecipients via the reply-context endpoint) — the SPA implements
 * no second recipient algorithm. Plain Reply fills the sender/Reply-To only;
 * Reply all additionally fills Cc (original To + Cc minus the mailbox's own
 * address and the primary, deduplicated server-side; the original Bcc is
 * never copied). The quoted original arrives as the server's escaped
 * Markdown projection, editable before Send.
 *
 * Staleness: a context response for a message other than the one now open
 * is DISCARDED, never applied — a delayed reply cannot hijack the compose
 * for a different message (US-5.AC-5).
 */

import { useCallback, useRef, useState } from 'react'
import { fetchMailReplyContext } from '@/lib/api/mail'

export type MailReplyMode = 'reply' | 'reply_all'

export interface MailReplyContextState {
  to: string[]
  cc: string[]
  bcc: string[]
  subject: string
  quotedBody: string
  inReplyTo: string | null
}

export interface MailReplyContextHook {
  /** loading of the in-flight context request, if any */
  loading: boolean
  /** load the context for one message; stale responses are discarded */
  loadContext: (args: {
    workspaceId: string
    agentId: string
    folder: string
    ref: string
    mode: MailReplyMode
  }) => Promise<MailReplyContextState | null>
}

export function useMailReplyContext(): MailReplyContextHook {
  const [loading, setLoading] = useState(false)
  // The request sequence guard: only the LATEST load may apply its result,
  // and only while the open message is still the one it was asked for.
  const seqRef = useRef(0)

  const loadContext = useCallback<MailReplyContextHook['loadContext']>(
    async ({ workspaceId, agentId, folder, ref, mode }) => {
      const seq = ++seqRef.current
      setLoading(true)
      try {
        const resp = await fetchMailReplyContext(workspaceId, agentId, folder, ref, { mode })
        if (seq !== seqRef.current) {
          // A newer context request superseded this one (or the compose
          // moved to another message): discard, never overwrite compose
          // input for a different message.
          return null
        }
        return {
          to: resp.to,
          cc: resp.cc,
          bcc: resp.bcc,
          subject: resp.subject,
          quotedBody: resp.body_markdown,
          inReplyTo: resp.in_reply_to ?? null,
        }
      } finally {
        if (seq === seqRef.current) {
          setLoading(false)
        }
      }
    },
    [],
  )

  return { loading, loadContext }
}
