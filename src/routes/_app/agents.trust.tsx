import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { TrustGraphScreen } from '@/components/screens/TrustGraphScreen'

// autoCodeSplitting extracts the component into its own lazy chunk. Static
// `/agents/trust` takes priority over the dynamic `/agents/$agentId` sibling,
// so the literal "trust" segment resolves here, not as an agent id.

// W6-C1 / G2: the Edit profile surfaces a summary link of the form
// `/agents/trust?agent=<id>` so the operator can jump from a single agent's
// profile directly into the delegation-policy editor with that agent's row
// pre-selected. The search param is OPTIONAL — visiting with no `?agent=`
// still works — so we use `.optional()`; empty strings are treated as absent.
const trustSearchSchema = z.object({
  agent: z
    .string()
    .min(1)
    .optional()
    .transform((v) => (v === '' ? undefined : v)),
})

export const Route = createFileRoute('/_app/agents/trust')({
  validateSearch: trustSearchSchema,
  component: TrustGraphScreen,
})
