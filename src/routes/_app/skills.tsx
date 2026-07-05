import { createFileRoute } from '@tanstack/react-router'
import { SkillsScreen } from '@/components/screens/SkillsScreen'

// autoCodeSplitting extracts this component into its own lazy chunk; the
// router-level defaultPendingComponent (src/main.tsx) supplies the fallback.
export const Route = createFileRoute('/_app/skills')({
  component: SkillsScreen,
})
