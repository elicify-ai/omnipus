import { createFileRoute } from '@tanstack/react-router'
import { UsageScreen } from '@/components/screens/UsageScreen'

// autoCodeSplitting extracts this component into its own lazy chunk; the
// router-level defaultPendingComponent (src/main.tsx) supplies the fallback.
export const Route = createFileRoute('/_app/usage')({
  component: UsageScreen,
})
