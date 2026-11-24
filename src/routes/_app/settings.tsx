import { useMemo } from 'react'
import { createFileRoute, redirect, useLocation } from '@tanstack/react-router'
import { z } from 'zod'
import { SettingsScreen } from '@/components/screens/SettingsScreen'
import { resolveSettingsLink } from '@/components/settings/settingsSections'

// Settings redesign #1519. `?tab=` names the open section (FR-002). Old tab
// names keep working (FR-003, Dataset D-3): they are resolved to the section
// that holds those settings now by resolveSettingsLink, never refused here, so
// any string is accepted and an unknown value falls back to Providers.
// `?provider=&model=` pre-fills a Limits → Model overrides row (FR-004);
// `?focus=god-mode` lands on the God Mode button in Protection (FR-005);
// `?restarted=1` is the landing link after a port change (FR-063); the
// router's search parser reads the bare 1 as a number, so both forms count;
// `?row=<row id>` scrolls to and highlights one setting row (FR-075).
const settingsSearchSchema = z.object({
  tab: z.string().max(64).optional().catch(undefined),
  provider: z.string().min(1).max(64).optional().catch(undefined),
  model: z.string().min(1).max(256).optional().catch(undefined),
  focus: z.string().max(32).optional().catch(undefined),
  restarted: z.union([z.literal(1), z.literal('1')]).optional().catch(undefined),
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
  // FR-004/D-3 (U1-8): every navigation to a pre-fill link is a new request,
  // even with the same provider and model (for example the same link followed
  // again after Discard). The object keeps one identity per history entry, so
  // the Limits form can tell a new request from a re-render.
  const navigationKey = useLocation({ select: (l) => l.state.__TSR_key ?? l.state.key })
  const provider = link.prefillOverride?.provider
  const model = link.prefillOverride?.model
  const prefillOverride = useMemo(
    () => (provider && model ? { provider, model } : undefined),
    // navigationKey is the point: a new history entry is a new request.
    [provider, model, navigationKey],
  )
  return (
    <SettingsScreen
      section={link.section}
      requestedTab={search.tab}
      prefillOverride={prefillOverride}
      focusGodMode={link.focusGodMode}
      backupRemoved={link.backupRemoved}
      restarted={search.restarted !== undefined}
      targetRow={link.row}
    />
  )
}
