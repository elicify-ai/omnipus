// Application adapter for the kit's pure identity vocabulary. All values
// crossing the gateway still use generated types; this conformance check
// keeps the kit rendering keys/palette exactly aligned in both directions.
import type { AgentColor, AgentFigure, AgentRole } from '@/lib/api/generated/openapi-types'
import type { AgentIconColor, AgentIconFigure, AgentIconRole } from '@/design-system/agent-identity'

export { FIGURE_ART, IDENTITY_GROUP_ORDER, ROLE_VOCABULARY } from '@/design-system/agent-identity'
export type { AgentRoleGroup } from '@/design-system/agent-identity'

type AssertNever<T extends never> = T
type _IdentityCoverage = AssertNever<
  | Exclude<AgentRole, AgentIconRole> | Exclude<AgentIconRole, AgentRole>
  | Exclude<AgentFigure, AgentIconFigure> | Exclude<AgentIconFigure, AgentFigure>
  | Exclude<AgentColor, AgentIconColor> | Exclude<AgentIconColor, AgentColor>
>
void (null as unknown as _IdentityCoverage)
