import { createFileRoute } from '@tanstack/react-router'
import { SkillsToolsPage } from '@/components/skills/SkillsToolsPage'

// Skills & Tools (skills-tools-redesign spec FR-065): the route renders the
// tool permission grid, skill trust and Web & Search settings.
// autoCodeSplitting extracts this component into its own lazy chunk; the
// router-level defaultPendingComponent (src/main.tsx) supplies the fallback.
export const Route = createFileRoute('/_app/skills')({
  component: SkillsToolsPage,
})
