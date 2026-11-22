// InstallReview.lostAnswer.test.tsx — order 53 (S-115; Dataset D15; C-13
// "Lost answer"). RED guard: the install review screen does not exist yet
// (order 28's guard covers the category contract; this file covers the
// lost-answer recovery). Oracles for its GREEN tests, transcribed from
// skills-tools-redesign-spec.md C-13 and Dataset D15 — the shown text per
// row, the poll and Retry behaviour, and the rule that the skills list alone
// never decides:
//   row 1  staged, then expired                          → "Not installed."
//   row 2  installed, installed_revision = reviewed      → "Installed weather."
//   row 3  publishing for ~3 s, then installed           → "Checking…", then
//                                                          "Installed weather."
//   row 4  publishing for more than 60 s                 → "Couldn't confirm
//          the install. Open Skills to see what is installed." + Retry
//   row 5  replacement failed; the list still shows R1   → "Not installed:
//          <reason>."; the list is not used to decide
//   row 6  status read fails or 404                      → "Couldn't confirm
//          the install. …" + Retry
//   Poll: while the state is publishing the review reads
//   GET /skills/install/review/{review_id} every second, for up to 60 seconds.
//   After Install is pressed, Cancel is disabled until a terminal state is
//   known (C-13).
import { describe, it, expect } from 'vitest'

describe('InstallReview — the install outcome after a lost answer (order 53)', () => {
  it('BLOCKED until the component exists', () => {
    const mod = import.meta.glob('/src/components/skills/InstallReview.tsx', { eager: false })
    expect(
      Object.keys(mod),
      'BLOCKED: InstallReview is not implemented — required by skills-tools-redesign-spec ' +
        'C-13 ("Lost answer": poll the status every second for up to 60 seconds; rows 1–6 of ' +
        'Dataset D15; the skills list alone never decides) and S-115',
    ).toHaveLength(1)
  })
})
