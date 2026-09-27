// MailPanelHost — the docked Mail panel in the app shell (SP-8: ONE shared
// side-panel surface; mail is panel CONTENT, not a shell change). Mirrors
// the docked-<aside>-as-flex-sibling pattern of LibraryPanel.tsx /
// BrowserLivePanel.tsx: it reads the ui store's activePanel slice, renders
// null when the open panel is not Mail, and lets the flex row shrink the
// chat region automatically. When the shell squad's production registry
// lands (wave 2), SidePanelShell renders the same content via
// mailPanelDefinition — this host is the live integration path until then.
import { useUiStore } from '@/store/ui'
import { MailPanel } from './MailPanel'

export function MailPanelHost() {
  const activePanel = useUiStore((s) => s.activePanel)
  if (activePanel?.id !== 'mail') return null
  return (
    <aside
      data-testid="mail-panel-docked"
      aria-label="Mail panel"
      className="flex h-full w-full min-w-0 sm:w-[45%] sm:min-w-[320px] sm:max-w-[720px] flex-shrink-0 flex-col overflow-hidden border-l border-[var(--color-border)] bg-[var(--color-surface-0)]"
    >
      <MailPanel workspaceId={activePanel.context.workspaceId ?? ''} />
    </aside>
  )
}
