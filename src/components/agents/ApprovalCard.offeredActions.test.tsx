// ApprovalCard.offeredActions.test.tsx — order 47 (C-12 decision table,
// C-26, R2-Q1). RED guard: the in-chat skill approval card component does
// not exist yet. Oracle for its GREEN tests — the actions a card offers are
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
//     C-26 choices in place of Always Allow
//   the five exact-action tools (C-26 point 9) never appear in this table
import { describe, it, expect } from 'vitest'

describe('ApprovalCard — offered actions per the C-12 decision table (order 47)', () => {
  it('BLOCKED until the component exists', () => {
    const mod = import.meta.glob('/src/components/**/ApprovalCard.tsx', { eager: false })
    expect(
      Object.keys(mod),
      'BLOCKED: the in-chat skill ApprovalCard is not implemented — required by ' +
        'skills-tools-redesign-spec C-12 (decision table rows 2–7) and C-26 (the time choices, ' +
        'never Always Allow per R2-Q1)',
    ).toHaveLength(1)
  })
})
