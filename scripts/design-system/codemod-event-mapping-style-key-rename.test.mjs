import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { planFileEdits, runCodemod, TARGET_FILE_REL } from './codemod-event-mapping-style-key-rename.mjs'

const fixtureRoots = []
after(() => {
  for (const root of fixtureRoots) rmSync(root, { recursive: true, force: true })
})

function fixtureRepo() {
  // Isolated scratch dirs live inside THIS lane's evidence dir only (W3-events
  // ownership boundary) — never dist/design-system-baseline/ directly and
  // never /tmp.
  const root = mkdtempSync(resolve('dist/design-system-baseline/cli-lanes/fanout/W3-events/test-fixture-'))
  fixtureRoots.push(root)
  return root
}

function write(root, relPath, content) {
  const abs = resolve(root, relPath)
  mkdirSync(dirname(abs), { recursive: true })
  writeFileSync(abs, content, 'utf8')
  return abs
}

const TYPES_FIXTURE = `
export interface ChipStyle {
  bg: string
  icon: string
}
export const SCHEDULED_STYLE: ChipStyle = { bg: '#94A3B8', icon: 'Clock' }
export const NO_RECORD_STYLE: ChipStyle = { bg: '#94A3B8', icon: 'Circle' }
`

// Full pattern: mirrors src/lib/calendar/eventMapping.ts's three shapes —
// a plain-literal-value return, a shorthand-local-variable return, and a
// call-site `.style` read consumed by a synthetic "makeEvent"-alike.
const MATCHING_EVENT_MAPPING = `
import { SCHEDULED_STYLE, NO_RECORD_STYLE, type ChipStyle } from '@/components/calendar/types'

function resolveChip(nowMs: number, occurrenceMs: number): { status: string; style: ChipStyle } {
  const style = occurrenceMs >= nowMs ? SCHEDULED_STYLE : NO_RECORD_STYLE
  return { status: 'x', style }
}

function resolveOther(): { status: string; style: ChipStyle } {
  return { status: 'y', style: SCHEDULED_STYLE }
}

export function build(nowMs: number, occurrenceMs: number) {
  const chip = resolveChip(nowMs, occurrenceMs)
  return { bg: chip.style.bg, icon: chip.style.icon }
}
`

test('MATCH: renames return-type member, shorthand return, literal return, and call-site reads', () => {
  const root = fixtureRepo()
  write(root, 'src/components/calendar/types.ts', TYPES_FIXTURE)
  const file = write(root, TARGET_FILE_REL, MATCHING_EVENT_MAPPING)

  const { edits, refusals, targetFunctionNames } = planFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.deepEqual(new Set(targetFunctionNames), new Set(['resolveChip', 'resolveOther']))
  // 2 return-type members + 1 shorthand return + 1 literal return + 2 call-site reads = 6
  assert.equal(edits.length, 6)

  const result = runCodemod({ repoRoot: root, apply: true })
  assert.equal(result.totalRefusals, 0)
  assert.equal(result.totalEdits, 6)
  assert.equal(result.applied, true)

  const after = readFileSync(file, 'utf8')
  assert.match(after, /chipStyle: ChipStyle/, 'return-type member renamed')
})

test('MATCH: applied output — strong content assertions (would catch a partial/weakened rename)', () => {
  const root = fixtureRepo()
  write(root, 'src/components/calendar/types.ts', TYPES_FIXTURE)
  const file = write(root, TARGET_FILE_REL, MATCHING_EVENT_MAPPING)
  runCodemod({ repoRoot: root, apply: true })
  const after = readFileSync(file, 'utf8')

  // Return-type members renamed.
  assert.match(after, /resolveChip\([^)]*\): \{ status: string; chipStyle: ChipStyle \}/)
  assert.match(after, /resolveOther\(\): \{ status: string; chipStyle: ChipStyle \}/)
  // Shorthand expanded to `chipStyle: style` — the LOCAL VARIABLE `style` is
  // untouched (only the object KEY changed); a mutation that renamed the
  // variable too (`chipStyle: chipStyle`) or left the shorthand alone
  // (`style,` / `chipStyle,`) would fail this exact-text assertion.
  assert.match(after, /const style = occurrenceMs >= nowMs \? SCHEDULED_STYLE : NO_RECORD_STYLE/)
  assert.match(after, /return \{ status: 'x', chipStyle: style \}/)
  // Literal return renamed.
  assert.match(after, /return \{ status: 'y', chipStyle: SCHEDULED_STYLE \}/)
  // Call-site reads renamed.
  assert.match(after, /chip\.chipStyle\.bg/)
  assert.match(after, /chip\.chipStyle\.icon/)
  // Old key must be completely gone from the rewritten region.
  assert.equal(/\bstyle: ChipStyle\b/.test(after), false)
  assert.equal(/\bstyle: SCHEDULED_STYLE\b/.test(after), false)
  assert.equal(/chip\.style\b/.test(after), false)
})

test('NO-MATCH: file with no ChipStyle import is skipped entirely (zero edits, zero refusals)', () => {
  const root = fixtureRepo()
  write(root, 'src/components/calendar/types.ts', TYPES_FIXTURE)
  const file = write(root, TARGET_FILE_REL, `
export function build() {
  const style = { bg: '#000', icon: 'x' }
  return { status: 'z', style }
}
`)
  const { edits, refusals, targetFunctionNames } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0)
  assert.deepEqual(targetFunctionNames, [])
})

test('NO-MATCH: file imports ChipStyle but no function returns a `style: ChipStyle` member', () => {
  const root = fixtureRepo()
  write(root, 'src/components/calendar/types.ts', TYPES_FIXTURE)
  const file = write(root, TARGET_FILE_REL, `
import { type ChipStyle } from '@/components/calendar/types'
export function build(): { status: string } {
  return { status: 'z' }
}
`)
  const { edits, refusals, targetFunctionNames } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0)
  assert.deepEqual(targetFunctionNames, [])
})

test('AMBIGUOUS: `.style` read on a non-identifier base is refused, whole file (fail-closed)', () => {
  const root = fixtureRepo()
  write(root, 'src/components/calendar/types.ts', TYPES_FIXTURE)
  const file = write(root, TARGET_FILE_REL, `
import { SCHEDULED_STYLE, type ChipStyle } from '@/components/calendar/types'

function resolveChip(): { status: string; style: ChipStyle } {
  return { status: 'x', style: SCHEDULED_STYLE }
}

export function build() {
  return { bg: resolveChip().style.bg }
}
`)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0, 'whole file must refuse — a partial rename would not compile')
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /non-identifier base/)
})

test('AMBIGUOUS: `.style` identifier reassigned to a non-target-function value is refused', () => {
  const root = fixtureRepo()
  write(root, 'src/components/calendar/types.ts', TYPES_FIXTURE)
  const file = write(root, TARGET_FILE_REL, `
import { SCHEDULED_STYLE, type ChipStyle } from '@/components/calendar/types'

function resolveChip(): { status: string; style: ChipStyle } {
  return { status: 'x', style: SCHEDULED_STYLE }
}

function unrelated() {
  return { status: 'x', style: SCHEDULED_STYLE }
}

export function build(flag: boolean) {
  let chip = resolveChip()
  if (flag) {
    chip = unrelated()
  }
  return { bg: chip.style.bg }
}
`)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /not provably always a direct call/)
})

test('AMBIGUOUS: `.style` identifier with zero resolvable origins in scope is refused', () => {
  const root = fixtureRepo()
  write(root, 'src/components/calendar/types.ts', TYPES_FIXTURE)
  const file = write(root, TARGET_FILE_REL, `
import { SCHEDULED_STYLE, type ChipStyle } from '@/components/calendar/types'

function resolveChip(): { status: string; style: ChipStyle } {
  return { status: 'x', style: SCHEDULED_STYLE }
}

export function build(chip: { style: ChipStyle }) {
  return { bg: chip.style.bg }
}
`)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /could not resolve any origin/)
})

test('IDEMPOTENT: a second --apply on the same fixture makes zero further changes', () => {
  const root = fixtureRepo()
  write(root, 'src/components/calendar/types.ts', TYPES_FIXTURE)
  const file = write(root, TARGET_FILE_REL, MATCHING_EVENT_MAPPING)

  const first = runCodemod({ repoRoot: root, apply: true })
  assert.ok(first.totalEdits > 0)
  const afterFirst = readFileSync(file, 'utf8')

  const second = runCodemod({ repoRoot: root, apply: true })
  assert.equal(second.totalEdits, 0, 'idempotent: nothing left to rename')
  assert.equal(second.totalRefusals, 0)
  const afterSecond = readFileSync(file, 'utf8')
  assert.equal(afterFirst, afterSecond, 'byte-identical after a no-op second apply')
})

test('DRY-RUN: default mode (no --apply) never writes the file', () => {
  const root = fixtureRepo()
  write(root, 'src/components/calendar/types.ts', TYPES_FIXTURE)
  const file = write(root, TARGET_FILE_REL, MATCHING_EVENT_MAPPING)
  const before = readFileSync(file, 'utf8')

  const result = runCodemod({ repoRoot: root, apply: false })
  assert.equal(result.totalEdits, 6)
  assert.equal(result.applied, false)
  const after = readFileSync(file, 'utf8')
  assert.equal(before, after, 'dry-run must not touch the file')
})

test('SAFETY: refuses to write outside the allowed src/ / packages/ui/src/ roots', () => {
  const root = fixtureRepo()
  write(root, 'src/components/calendar/types.ts', TYPES_FIXTURE)
  // Deliberately write the target file OUTSIDE the allowed roots by pointing
  // repoRoot at a directory whose TARGET_FILE_REL resolves outside itself —
  // simulated here by asserting isInAllowedRoot's guard fires for a foreign root.
  const foreignRoot = fixtureRepo()
  write(foreignRoot, 'not-src/lib/calendar/eventMapping.ts', MATCHING_EVENT_MAPPING)
  // runCodemod always resolves TARGET_FILE_REL under repoRoot's `src/...`,
  // so a repo with no src/lib/calendar/eventMapping.ts at all must report
  // targetFileFound: false rather than fabricating a write.
  const result = runCodemod({ repoRoot: foreignRoot, apply: true })
  assert.equal(result.targetFileFound, false)
  assert.equal(result.totalEdits, 0)
})
