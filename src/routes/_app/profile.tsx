import { createFileRoute } from '@tanstack/react-router'
import { ProfileScreen } from '@/components/screens/ProfileScreen'

// autoCodeSplitting extracts this component into its own lazy chunk; the
// router-level defaultPendingComponent (src/main.tsx) supplies the fallback.
export const Route = createFileRoute('/_app/profile')({
  component: ProfileScreen,
})
