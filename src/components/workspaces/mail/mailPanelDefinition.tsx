// mailPanelDefinition.tsx — Mail's side-panel registration payload (spec
// side-panel-shell-spec.md §8.1), registered in the shell's production
// registry (src/components/panel-shell/registry.tsx) at wave 2 (§10 "Mail
// adopts the shell" + FR-014: "Adding Mail MUST require only a new panel
// registration — no shell modification").
//
// Oracle — side-panel-shell-spec.md §1 panel inventory, mail row:
//   "| mail | Mail | Mail panel (feature/email-mail, D11) | Mail full page
//    (per email spec) |"
// with the email spec naming the full page:
//   email-mail-view-spec.md §17 (chat_link row): "…/#/workspaces/{wsId}/mail
//   ?mailbox={agentId}&folder=drafts&message=…"
//
// The `/#/` prefix is derived, not observed: the shell's Expand does
// window.open(expandTarget(context)) (src/components/panel-shell/
// usePanelShell.ts), and in a hash-history SPA a new-tab URL without the
// hash fragment names a GATEWAY path, not the SPA route — Library's and
// Browser's registry entries both carry it for exactly this reason. A
// expand target without `/#/` opens a dead tab. The /mail route behind it
// consumes §17 draft-link params into the panel intent, then retargets the
// address to the deep-link form chat?panel=mail (§8.2).
//
// Also pinned: no beforeLeave (§8.1: "beforeLeave is supplied ONLY by
// panels with unsaved-edit risk — Library"; Mail keeps no unsaved-edit
// state outside its compose dialog, which owns its own confirm-on-close —
// same posture as Browser).
import { lazy } from 'react'
import type { PanelDefinition } from '@/components/panel-shell/types'

// Lazy, like the shell's Library/Browser entries: the registry is imported
// by the AppShell eagerly, so the mail panel's code must ride the same
// on-open dynamic chunk path instead of the shell's eager one.
const MailPanel = lazy(async () => {
  const module = await import('./MailPanel')
  return { default: module.MailPanel }
})

/** The §8.1 PanelDefinition for the Mail panel. */
export const mailPanelDefinition: PanelDefinition = {
  id: 'mail',
  title: 'Mail',
  content: function MailPanelContent({ context }) {
    return (
      // The shell's own <Suspense> boundary hosts the lazy chunk ("Loading
      // Mail…" fallback) — same as Library/Browser.
      <MailPanel
        workspaceId={context.workspaceId ?? ''}
        mailboxId={context.mailboxId}
      />
    )
  },
  expandTarget: (context) =>
    context.workspaceId === undefined
      ? '/#/workspaces'
      : `/#/workspaces/${context.workspaceId}/mail`,
  // No beforeLeave: Mail keeps no unsaved-edit state outside the compose
  // dialog, which owns its own confirm-on-close (same posture as Browser —
  // the only guard in the registry is the Library's).
}
