// Pure presentation vocabulary for AgentIcon. No API, store, or agent lookup.
// SPEC "Locked identity vocabulary" owns the labels, figures, and palette.
// Application conformance to the generated wire types lives in lib/agentIdentity.

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
} as const satisfies Record<string, { label: string; group: AgentRoleGroup }>

// not-wire-format: keys accepted by a data-supplied rendering component.
export type AgentIconRole = keyof typeof ROLE_ROWS

export const ROLE_VOCABULARY = (Object.keys(ROLE_ROWS) as AgentIconRole[]).map((slug) => ({
  group: ROLE_ROWS[slug].group,
  slug,
  label: ROLE_ROWS[slug].label,
}))

export const FIGURE_ART = {
  Robot: { art: 'robotSolid', face: 'eyes' },
  Man: { art: 'man', face: 'eyes' },
  Woman: { art: 'woman', face: 'eyes' },
  Omnipus: { art: 'octopus', face: 'none' },
  // The 5th figure. `art: 'monogram'` is deliberately NOT an AgentIconArtKey:
  // AgentIcon branches to `monogramInner` before the figure-art lookup, and the
  // `art` value is used only as the ink/glow span's `data-art` attribute. face
  // is 'none' because no eyes art is baked (the letter is the mark).
  Monogram: { art: 'monogram', face: 'none' },
} as const

// not-wire-format: figure keys accepted by AgentIcon, not an API resource.
export type AgentIconFigure = keyof typeof FIGURE_ART

// Governed identity ink, not UI chrome. Kept in the kit so the published
// component never depends on generated application schemas. Values and order
// are the founder's 2026-10-11 avatar palette decision (Option 2, metallic /
// brand-derived; coordination/squads/joint-merge-20261009/AVATAR-PALETTE-DECISION.md).
// lib/agentIdentity's compile-time coverage check pins this list to the
// generated AgentColor enum, so any contract drift fails typecheck.
export const AGENT_ICON_PALETTE = [
  '#6E8BA8', '#7A8C9E', '#4F8A80', '#5E6973', '#7B7290',
  '#B76E79', '#A97142', '#9C8A5B', '#CDBA96', '#8E9196',
] as const

// not-wire-format: palette ink accepted by AgentIcon.
export type AgentIconColor = (typeof AGENT_ICON_PALETTE)[number]
