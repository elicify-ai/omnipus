import { createFileRoute } from '@tanstack/react-router'
import { AppShell } from '@/components/layout/AppShell'
import { resetTokenValidationCache } from './authValidation'
import { authenticatedBeforeLoad } from './-authenticatedBeforeLoad'

// Re-exported so the login flow (and tests) can reset the validation cache (#359).
export { resetTokenValidationCache }

// Pathless layout route — wraps all app screens in AppShell
// Landing page (/landing) is a sibling, NOT nested here, so it renders without the shell
// /onboarding is also a sibling — no AppShell, no beforeLoad
export const Route = createFileRoute('/_app')({
  beforeLoad: authenticatedBeforeLoad,
  component: AppShell,
})
