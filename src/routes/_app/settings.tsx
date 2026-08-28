import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { SettingsScreen } from '@/components/screens/SettingsScreen'
import type { SettingsTab } from '@/components/screens/SettingsScreen'

// `?tab=` opens a tab directly; `?tab=models&provider=&model=` is the ADR-068
// X-08 link target (T068-29) — it pre-fills a Models → Model overrides row for
// a model whose endpoint reported no context length. Unknown tab values fall
// back to the default tab rather than failing the route.
const SETTINGS_TABS = [
  'providers',
  'models',
  'integrations',
  'security',
  'gateway',
  'data',
  'memory',
  'devices',
  'performance',
  'chat',
  'about',
] as const satisfies readonly SettingsTab[]

const settingsSearchSchema = z.object({
  tab: z.enum(SETTINGS_TABS).optional().catch(undefined),
  provider: z.string().min(1).max(64).optional().catch(undefined),
  model: z.string().min(1).max(256).optional().catch(undefined),
})

// autoCodeSplitting (vite.config.ts) extracts this component into its own lazy
// chunk — no manual React.lazy/Suspense needed. Router-level
// defaultPendingComponent (src/main.tsx) supplies the loading skeleton.
export const Route = createFileRoute('/_app/settings')({
  validateSearch: settingsSearchSchema,
  component: SettingsRoute,
})

function SettingsRoute() {
  const { tab, provider, model } = Route.useSearch()
  const prefillOverride = provider && model ? { provider, model } : undefined
  // A pre-fill only makes sense on the Models tab — land there even without ?tab=.
  const initialTab: SettingsTab = tab ?? (prefillOverride ? 'models' : 'providers')
  return <SettingsScreen initialTab={initialTab} prefillOverride={prefillOverride} />
}
