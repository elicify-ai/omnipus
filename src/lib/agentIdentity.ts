// Identity vocabulary for the agent mark. Slugs, labels, and groups match the
// contract and SPEC "Locked identity vocabulary". Hex values are not repeated
// here: swatches use the generated AgentColor enum. There is no palette
// mapper in the SPA; boot persists a palette hex.
import type { AgentFigure, AgentRole } from '@/lib/api/generated/openapi-types'

export const ROLE_GROUPS = ['Create', 'Build', 'Business', 'People', 'Personal'] as const
export type AgentRoleGroup = (typeof ROLE_GROUPS)[number]

export const AGENT_ROLES = {
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

export const FIGURES = ['Robot', 'Man', 'Woman', 'Omnipus'] as const

export const FIGURE_PRESENTATION = {
  Robot: { art: 'robotSolid', face: 'eyes' },
  Man: { art: 'man', face: 'eyes' },
  Woman: { art: 'woman', face: 'eyes' },
  Omnipus: { art: 'octopus', face: 'none' },
} as const satisfies Record<AgentFigure, { art: 'robotSolid' | 'man' | 'woman' | 'octopus'; face: 'eyes' | 'none' }>

type MissingRole = Exclude<AgentRole, keyof typeof AGENT_ROLES>
type MissingFigure = Exclude<AgentFigure, (typeof FIGURES)[number]>
type AssertNever<T extends never> = T
type _RoleCoverage = AssertNever<MissingRole>
type _FigureCoverage = AssertNever<MissingFigure>
void (null as unknown as _RoleCoverage | _FigureCoverage)
