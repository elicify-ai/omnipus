import { createFileRoute } from '@tanstack/react-router'
import { SettingsScreen } from '@/components/screens/SettingsScreen'

// autoCodeSplitting (vite.config.ts) extracts this component into its own lazy
// chunk — no manual React.lazy/Suspense needed. Router-level
// defaultPendingComponent (src/main.tsx) supplies the loading skeleton.
export const Route = createFileRoute('/_app/settings')({
  component: SettingsScreen,
})
