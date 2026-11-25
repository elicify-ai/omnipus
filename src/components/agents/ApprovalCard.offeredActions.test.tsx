// ApprovalCard.offeredActions.test.tsx — order 47 (C-12 decision table,
// C-26, R2-Q1). The in-chat skill approval card's offered actions are
// exactly the C-12 decision table's rows, with C-26's time choices and
// never an "Always allow" (R2-Q1 removed it from the wire):
//   permission card (C-12 #2): Approve Once, Allow for 1 hour / 3 hours /
//     1 day, Deny
//   trust card (C-12 #3): "Approve this version", Deny — no time choice
//   combined card (C-12 #4): "Approve this version", "Approve this version
//     and allow for" 1 hour / 3 hours / 1 day, Deny
//   install card, unverified + Ask (C-12 #6): "Install and approve this
//     version", Deny — no time choice
//   install card, verified + Ask (C-12 #7): today's install card with the
//   C-26 choices in place of Always Allow
//   the five exact-action tools (C-26 point 9) never appear in this table
//
// CHECK round 1 finding 2 / H3: the earlier file held only a file-existence
// guard (an empty export-only placeholder passed it). The real interaction
// tests below drive the imported component; the missing module fails at
// import resolution, naming the spec, until GREEN lands it.
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { ComponentType } from 'react'
import type { ToolApprovalRequiredFrame } from '@/lib/api/generated/asyncapi-types'

// The pack's BLOCKED seam (tests/l1-ui-red-u23/support.tsx::requireModule
// convention): the missing module is a named, loud failure — never a fake
// component — while the acceptance oracles are the interaction tests below.
const cardModules = import.meta.glob<Record<string, unknown>>(['/src/components/agents/ApprovalCard.tsx'])
async function requireApprovalCard(): Promise<ComponentType<{ entry: unknown }>> {
  expect(
    Object.keys(cardModules),
    'BLOCKED: the in-chat skill ApprovalCard is not implemented — required by ' +
      'skills-tools-redesign-spec C-12 (decision table rows 2–7) and C-26 (the time choices, ' +
      'never Always Allow per R2-Q1)',
  ).toContain('/src/components/agents/ApprovalCard.tsx')
  const mod = (await cardModules['/src/components/agents/ApprovalCard.tsx']()) as Record<string, unknown>
  expect(typeof mod.ApprovalCard, 'BLOCKED: ApprovalCard public export').toBe('function')
  return mod.ApprovalCard as ComponentType<{ entry: unknown }>
}

let ApprovalCard: ComponentType<{ entry: unknown }>

beforeEach(async () => {
  ApprovalCard = await requireApprovalCard()
})

// The card entry the chat renders for an install_skill / skill-load ask.
// not-wire-format: test-local projection of the C-12 row inputs (the pack's
// fixture-cast convention) plus the two lists the server computes when it raises
// the card — `offered_actions` and `allow_durations`, both required fields of the
// wire frame (C-24, C-26; contracts ToolApprovalRequiredFrame), named here as the
// store entry names them. The card renders exactly those lists; the decision-table
// inputs stay on the entry so the card is renderable from either.
interface CardEntryFixture {
  toolName: string
  skillName?: string
  verified?: boolean
  trustOpen?: boolean
  permissionOpen?: boolean
  offeredActions: ToolApprovalRequiredFrame['offered_actions']
  allowDurations: ToolApprovalRequiredFrame['allow_durations']
}
type ServerLists = Pick<CardEntryFixture, 'offeredActions' | 'allowDurations'>

// What the server sends for each card kind — the examples in the contract's
// offered_actions description (C-24, C-26), one per C-12 row below.
const PERMISSION_CARD: ServerLists = { offeredActions: ['allow_once', 'allow_for', 'deny'], allowDurations: ['1h', '3h', '1d'] }
const TRUST_CARD: ServerLists = { offeredActions: ['approve_version', 'deny'], allowDurations: [] }
const COMBINED_CARD: ServerLists = {
  offeredActions: ['approve_version', 'approve_version_allow_for', 'deny'],
  allowDurations: ['1h', '3h', '1d'],
}
const UNVERIFIED_INSTALL_CARD: ServerLists = { offeredActions: ['allow_once', 'deny'], allowDurations: [] }

function renderCard(entry: CardEntryFixture) {
  return render(<ApprovalCard entry={entry as never} />)
}

const TIME_CHOICES = [/^allow for 1 hour$/i, /^allow for 3 hours$/i, /^allow for 1 day$/i]

function expectButtons(names: RegExp[], absent: RegExp[]) {
  for (const name of names) {
    expect(screen.getByRole('button', { name }), `expected button ${name}`).toBeInTheDocument()
  }
  for (const name of absent) {
    expect(screen.queryByRole('button', { name }), `expected NO button ${name}`).not.toBeInTheDocument()
  }
}

describe('ApprovalCard — offered actions per the C-12 decision table (order 47)', () => {
  it('C-12 #2: a permission card offers Approve Once, the three time choices and Deny — never Always Allow', () => {
    renderCard({ toolName: 'install_skill', skillName: 'weather', permissionOpen: true, ...PERMISSION_CARD })
    expectButtons(
      [/^approve once$/i, /^deny$/i, ...TIME_CHOICES],
      [/always allow/i, /approve this version/i],
    )
  })

  it('C-12 #3: a trust card offers "Approve this version" and Deny with no time choice', () => {
    renderCard({ toolName: 'Skill', skillName: 'notes-helper', verified: false, trustOpen: true, ...TRUST_CARD })
    expectButtons([/^approve this version$/i, /^deny$/i], [/^allow for 1 hour$/i, /^allow for 3 hours$/i, /^allow for 1 day$/i])
  })

  it('C-12 #4: a combined card offers the trust approval, the three "and allow for" choices and Deny', () => {
    renderCard({
      toolName: 'Skill',
      skillName: 'notes-helper',
      verified: false,
      trustOpen: true,
      permissionOpen: true,
      ...COMBINED_CARD,
    })
    expectButtons(
      [/^approve this version$/i, /^approve this version and allow for 1 hour$/i, /^approve this version and allow for 3 hours$/i, /^approve this version and allow for 1 day$/i, /^deny$/i],
      [/^approve once$/i],
    )
  })

  it('C-12 #6: an unverified install under Ask offers "Install and approve this version" and Deny with no time choice', () => {
    renderCard({
      toolName: 'install_skill',
      skillName: 'weather',
      verified: false,
      trustOpen: true,
      permissionOpen: true,
      ...UNVERIFIED_INSTALL_CARD,
    })
    expectButtons([/^install and approve this version$/i, /^deny$/i], [/^allow for 1 hour$/i, /^approve once$/i])
  })

  it('C-12 #7: a verified install under Ask offers the C-26 choices in place of Always Allow', () => {
    renderCard({ toolName: 'install_skill', skillName: 'weather', verified: true, permissionOpen: true, ...PERMISSION_CARD })
    expectButtons(
      [/^approve once$/i, /^deny$/i, ...TIME_CHOICES],
      [/always allow/i, /install and approve this version/i],
    )
  })
})
