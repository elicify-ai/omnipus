// mailPanelDefinition.ts — Mail's side-panel registration payload (spec
// side-panel-shell-spec.md §8.1), exported from the mail module for the
// shell's production registry. Per the shell squad's wave-1 RED
// (src/store/panelShell.registry.test.ts::'registers exactly wave-1
// panels'), the production registry registers exactly library + browser in
// wave 1 and MUST NOT contain mail — mail is wave 2. So this file exports
// the definition only; wiring it into src/components/panel-shell/registry.tsx
// is the shell squad's wave-2 step (one entry, per SP-4). Nothing in this
// tree consumes it yet — the docked MailPanelHost is the live integration
// path today.
import type { PanelDefinition } from '@/components/panel-shell/types'
import { MailPanel } from './MailPanel'

/** The §8.1 PanelDefinition for the Mail panel. expandTarget is the
 * /workspaces/{ws}/mail deep link (the route stub redirects to chat and
 * reopens the panel). */
export const mailPanelDefinition: PanelDefinition = {
  id: 'mail',
  title: 'Mail',
  content: function MailPanelContent({ context }) {
    return <MailPanel workspaceId={context.workspaceId ?? ''} />
  },
  expandTarget: (context) =>
    context.workspaceId === undefined
      ? '/workspaces'
      : `/workspaces/${context.workspaceId}/mail`,
  // No beforeLeave: Mail keeps no unsaved-edit state outside the compose
  // dialog, which owns its own confirm-on-close (same posture as Browser —
  // the only wave-1 guard is the Library's).
}
