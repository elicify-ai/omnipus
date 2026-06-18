import { lazy, Suspense } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { RouteFallback } from '@/components/shared/RouteFallback'

// Code-split: the trust-graph screen lazy-loads into its own chunk, matching the
// agents.index / agents.$agentId pattern. Static `/agents/trust` takes priority
// over the dynamic `/agents/$agentId` sibling in TanStack Router, so the literal
// "trust" segment resolves here, not as an agent id.
const TrustGraphScreen = lazy(() =>
  import('@/components/screens/TrustGraphScreen').then((m) => ({
    default: m.TrustGraphScreen,
  })),
)

function TrustGraphRoute() {
  return (
    <Suspense fallback={<RouteFallback />}>
      <TrustGraphScreen />
    </Suspense>
  )
}

// W6-C1 / G2: the Edit profile surfaces a summary link of the form
// `/agents/trust?agent=<id>` so the operator can jump from a single agent's
// profile directly into the delegation-policy editor with that agent's
// row pre-selected. The search param is OPTIONAL — visiting the route with
// no `?agent=` still works (TrustGraphScreen renders the full graph) — so
// we use `.optional()` rather than a required string. Empty strings are
// treated as absent (`.or(z.literal(''))` -> transform) so that links
// emitted with a blank id don't surface as a `?agent=` query string the
// graph screen has to special-case.
const trustSearchSchema = z.object({
  agent: z
    .string()
    .min(1)
    .optional()
    .transform((v) => (v === '' ? undefined : v)),
})

export const Route = createFileRoute('/_app/agents/trust')({
  validateSearch: trustSearchSchema,
  component: TrustGraphRoute,
})
