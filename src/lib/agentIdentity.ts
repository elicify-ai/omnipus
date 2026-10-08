// Identity vocabulary for the agent mark. Slugs, labels, and groups match the
// contract and SPEC "Locked identity vocabulary". Hex values are not repeated
// here: swatches use the generated AgentColor enum. There is no palette
// mapper in the SPA; boot persists a palette hex.
import type { AgentFigure, AgentRole } from '@/lib/api/generated/openapi-types'

export const IDENTITY_GROUP_ORDER = ['Create', 'Build', 'Business', 'People', 'Personal'] as const
export type AgentRoleGroup = (typeof IDENTITY_GROUP_ORDER)[number]

const ROLE_ROWS = {
  writer: { label: 'Writer', group: 'Create' },
  designer: { label: 'Designer', group: 'Create' },
  image: { label: 'Image creator', group: 'Create' },
  video: { label: 'Video producer', group: 'Create' },
  audio: { label: 'Audio and voice', group: 'Create' },
  social: { label: 'Social media', group: 'Create' },
  developer: { label: 'Developer', group: 'Build' },
  data: { label: 'Data engineer', group: 'Build' },
  analyst: { label: 'Data analyst', group: 'Build' },
  itops: { label: 'IT and operations', group: 'Build' },
  automation: { label: 'Automation', group: 'Build' },
  security: { label: 'Security', group: 'Build' },
  quality: { label: 'Quality and QA', group: 'Build' },
  science: { label: 'Science and lab', group: 'Build' },
  orchestrator: { label: 'Orchestrator', group: 'Business' },
  project: { label: 'Project manager', group: 'Business' },
  product: { label: 'Product manager', group: 'Business' },
  sales: { label: 'Sales', group: 'Business' },
  marketing: { label: 'Marketing', group: 'Business' },
  finance: { label: 'Finance', group: 'Business' },
  legal: { label: 'Legal and compliance', group: 'Business' },
  support: { label: 'Customer support', group: 'Business' },
  documents: { label: 'Documents', group: 'Business' },
  researcher: { label: 'Researcher', group: 'Business' },
  people: { label: 'People and HR', group: 'People' },
  tutor: { label: 'Tutor', group: 'People' },
  knowledge: { label: 'Knowledge and library', group: 'People' },
  translator: { label: 'Translator', group: 'People' },
  general: { label: 'General assistant', group: 'Personal' },
  personal: { label: 'Personal assistant', group: 'Personal' },
  office: { label: 'Office assistant', group: 'Personal' },
} as const satisfies Record<AgentRole, { label: string; group: AgentRoleGroup }>

export const ROLE_VOCABULARY = (Object.keys(ROLE_ROWS) as AgentRole[]).map((slug) => ({
  group: ROLE_ROWS[slug].group,
  slug,
  label: ROLE_ROWS[slug].label,
}))

export const FIGURE_ART = {
  Robot: { art: 'robotSolid', face: 'eyes' },
  Man: { art: 'man', face: 'eyes' },
  Woman: { art: 'woman', face: 'eyes' },
  Omnipus: { art: 'octopus', face: 'none' },
} as const satisfies Record<AgentFigure, { art: 'robotSolid' | 'man' | 'woman' | 'octopus'; face: 'eyes' | 'none' }>

type MissingRole = Exclude<AgentRole, keyof typeof ROLE_ROWS>
type MissingFigure = Exclude<AgentFigure, keyof typeof FIGURE_ART>
type AssertNever<T extends never> = T
type _RoleCoverage = AssertNever<MissingRole>
type _FigureCoverage = AssertNever<MissingFigure>
void (null as unknown as _RoleCoverage | _FigureCoverage)
