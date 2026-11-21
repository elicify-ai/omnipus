import { createFileRoute, redirect } from '@tanstack/react-router'
import { z } from 'zod'
import { SettingsScreen } from '@/components/screens/SettingsScreen'
import { resolveSettingsLink } from '@/components/settings/settingsSections'

// Settings redesign #1519. `?tab=` names the open section (FR-002). Old tab
// names keep working (FR-003, Dataset D-3): they are resolved to the section
// that holds those settings now by resolveSettingsLink, never refused here, so
// any string is accepted and an unknown value falls back to Providers.
// `?provider=&model=` pre-fills a Limits → Model overrides row (FR-004);
// `?focus=god-mode` lands on the God Mode button in Protection (FR-005);
// `?restarted=1` is the landing link after a port change (FR-063);
// `?row=<row id>` scrolls to and highlights one setting row (FR-075).
const settingsSearchSchema = z.object({
  tab: z.string().max(64).optional().catch(undefined),
  provider: z.string().min(1).max(64).optional().catch(undefined),
  model: z.string().min(1).max(256).optional().catch(undefined),
  focus: z.string().max(32).optional().catch(undefined),
  restarted: z.string().max(8).optional().catch(undefined),
  row: z.string().max(64).optional().catch(undefined),
})

// autoCodeSplitting (vite.config.ts) extracts this component into its own lazy
// chunk — no manual React.lazy/Suspense needed. Router-level
// defaultPendingComponent (src/main.tsx) supplies the loading skeleton.
export const Route = createFileRoute('/_app/settings')({
  validateSearch: settingsSearchSchema,
  beforeLoad: ({ search }) => {
    // The old Chat tab lives in Profile → Chat display now (D-3 row 10).
    if (search.tab === 'chat') {
      throw redirect({ to: '/profile', hash: 'chat-display', replace: true })
    }
    // FR-005: the God Mode button lives in Protection, so the focus link
    // names that section before the button consumes `focus`; otherwise the
    // address would fall back to the old tab and unmount the button.
    if (search.focus === 'god-mode' && search.tab !== 'protection') {
      throw redirect({ to: '/settings', search: { ...search, tab: 'protection' }, replace: true })
    }
  },
  component: SettingsRoute,
})

function SettingsRoute() {
  const search = Route.useSearch()
  const link = resolveSettingsLink(search)
  return (
    <SettingsScreen
      section={link.section}
      requestedTab={search.tab}
      prefillOverride={link.prefillOverride}
      focusGodMode={link.focusGodMode}
      backupRemoved={link.backupRemoved}
      restarted={search.restarted === '1'}
      targetRow={link.row}
    />
  )
}
