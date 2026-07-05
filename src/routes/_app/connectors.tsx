import { createFileRoute } from '@tanstack/react-router'
import { ConnectorsScreen } from '@/components/screens/ConnectorsScreen'

// autoCodeSplitting extracts this component into its own lazy chunk; the
// router-level defaultPendingComponent (src/main.tsx) supplies the fallback.
export const Route = createFileRoute('/_app/connectors')({
  component: ConnectorsScreen,
})
