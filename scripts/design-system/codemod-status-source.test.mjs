import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { isInAllowedRoot } from './codemod-lib.mjs'
import { runCodemod, parseD4CanonicalHexTable, MODE_LITERAL, MODE_MISMATCH } from './codemod-status-source.mjs'

const fixtureRoots = []
after(() => {
  for (const root of fixtureRoots) rmSync(root, { recursive: true, force: true })
})

function fixtureRepo() {
  // CI runs `node --test scripts/design-system/codemod-*.test.mjs` on a
  // fresh checkout with no dist/ at all — create the parent explicitly
  // rather than relying on another test file having created it first.
  mkdirSync(resolve('dist/design-system-baseline'), { recursive: true })
  const root = mkdtempSync(resolve('dist/design-system-baseline/codemod-status-source-'))
  fixtureRoots.push(root)
  return root
}

function write(root, relPath, content) {
  const abs = resolve(root, relPath)
  mkdirSync(dirname(abs), { recursive: true })
  writeFileSync(abs, content, 'utf8')
  return abs
}

// ── shared fixture content ──────────────────────────────────────────────

const D4_DOC = `# Design system definition

Some preamble content unrelated to status.

### D4. Status is a complete, distinguishable presentation contract — amended 2026-09-17 after adversarial review

**Decision.** There is one map. The task palette is the winner.

| State | Colour | Hex | Non-colour cue |
|---|---|---|---|
| Inbox | Grey | \`#9CA3AF\` | Quiet circle |
| Next | Blue | \`#3B82F6\` | Ready / info |
| In progress | Forge Gold | \`#D4AF37\` | Live work (the one gold status) |
| Blocked | Orange | \`#F97316\` | Prohibit |
| Done | Green | \`#10B981\` | Check |
| Failed | Red | \`#EF4444\` | X |
| Cancelled (stopped by user) | Amber | \`#EAB308\` | Distinct from Failed; not a separate board column |

### D5. A later section that must not be swept into the D4 table

| Something | Else | \`#000000\` | Not a status |
`

const STATUS_COLORS_TS = `export const STATUS_COLORS: Record<string, string> = {
  inbox: '#9ca3af',
  next: '#3B82F6',
  in_progress: '#D4AF37',
  blocked: '#F97316',
  done: '#10b981',
  failed: '#ef4444',
}

export const TASK_CANCELLED_COLOR = '#EAB308'
`

const PLAN_STATE_COLORS_TS = `export const PLAN_STATE_COLORS: Record<string, string> = {
  draft: '#9ca3af',
  approved: '#3B82F6',
  running: '#D4AF37',
  done: '#10b981',
  failed: '#ef4444',
}

export const PLAN_CANCELLED_COLOR = '#EAB308'
`

const TASK_STATUS_CONFIG_TS = `export const STATUS_BADGE: Record<string, string> = {
  inbox:       'text-[var(--color-muted)] bg-white/5',
  next:        'text-[color:var(--color-accent)] bg-[var(--color-accent)]/10',
  in_progress: 'text-[color:var(--color-warning)] bg-[var(--color-warning)]/10',
  blocked:     'text-[color:var(--color-warning)] bg-[var(--color-warning)]/10',
  done:        'text-[color:var(--color-success)] bg-[var(--color-success)]/10',
  failed:      'text-[color:var(--color-error)] bg-[var(--color-error)]/10',
}
`

function checklistFieldTsx() {
  return `import { CircleHalf, Square } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

export function TaskChecklistField({ todos }: any) {
  return (
    <div>
      {todos.map((todo: any, idx: number) => (
        <div key={idx}>
          {todo.status === 'in_progress' ? (
            <CircleHalf size={13} className="shrink-0 text-[color:var(--color-warning)]" />
          ) : (
            <Square size={13} />
          )}
          <span className={cn(
            'flex-1',
            todo.status === 'in_progress' ? 'text-[color:var(--color-warning)]' : undefined,
          )}>
            {todo.text}
          </span>
        </div>
      ))}
    </div>
  )
}
`
}

function taskDetailPanelTsx({ inProgressText = 'In Progress', blockedText = 'Blocked (dependency unmet)' } = {}) {
  return `import { Badge } from '@/components/ui/badge'

export function TaskDetailPanel({ isRunning, task }: any) {
  return (
    <div>
      {isRunning ? (
        <Badge className="h-8 text-xs bg-[var(--color-warning)]/10 text-[color:var(--color-warning)] border-transparent rounded-md px-2 inline-flex items-center">
          ${inProgressText}
        </Badge>
      ) : task.status === 'blocked' ? (
        <Badge className="h-8 text-xs bg-[var(--color-warning)]/10 text-[color:var(--color-warning)] border-transparent rounded-md px-2 inline-flex items-center">
          ${blockedText}
        </Badge>
      ) : null}
    </div>
  )
}
`
}

const CALENDAR_TYPES_TS = `export type TaskStatus = 'inbox' | 'next' | 'in_progress' | 'blocked' | 'done' | 'failed'
export interface ChipStyle { bg: string; icon: string }

export const STATUS_STYLE: Record<TaskStatus, ChipStyle> = {
  done: { bg: '#34D399', icon: 'CheckCircle' },
  in_progress: { bg: '#60A5FA', icon: 'CircleNotch' },
  blocked: { bg: '#FBBF24', icon: 'Prohibit' },
  failed: { bg: '#F87171', icon: 'XCircle' },
  inbox: { bg: '#94A3B8', icon: 'Circle' },
  next: { bg: '#94A3B8', icon: 'Circle' },
}
`

const DEFAULT_MODEL_CARD_TSX = `export function DefaultModelCard() {
  return <p className="mt-1 text-sm text-red-400" data-testid="default-model-error">Could not load</p>
}
`

const AGENT_CARD_TSX = `export function AgentCard() {
  return <Badge className="text-[var(--color-warning)] border-[var(--color-warning)]/30 bg-[var(--color-warning)]/10">draft</Badge>
}
`

const LIBRARY_PDF_PREVIEW_TSX = `export function LibraryPdfPreview({ error }: any) {
  return <p className="mt-2 text-[var(--color-muted)]">{error}</p>
}
`

const ONBOARDING_TSX = `export function Onboarding() {
  return <p style={{ color: 'var(--color-muted)' }}>Hello</p>
}
`

const SET_GOAL_TOOL_UI_TSX = `export function SetGoalToolUI() {
  return <summary className="flex cursor-pointer list-none items-center gap-1.5 py-0.5 text-[var(--color-muted)]">Goal</summary>
}
`

const ASK_USER_QUESTION_CARD_TSX = `export function AskUserQuestionCard() {
  return <X size={12} weight="bold" className="text-[var(--color-muted)]" aria-hidden="true" />
}
`

/** Populates every file this codemod's two passes touch, already in the
 * MIGRATED state, so a test can layer just the ONE thing it wants to probe
 * on top without tripping "file not found" refusals for the rest. */
function writeFullyMigratedMismatchRepo(root) {
  write(root, 'docs/internal/design/design-system-definition.md', D4_DOC)
  write(root, 'src/components/workspaces/taskStatusConfig.ts', `export const STATUS_BADGE: Record<string, string> = {
  next: 'text-[color:var(--color-status-next)] bg-[var(--color-status-next)]/10',
  in_progress: 'text-[color:var(--color-status-in-progress)] bg-[var(--color-status-in-progress)]/10',
  blocked: 'text-[color:var(--color-status-blocked)] bg-[var(--color-status-blocked)]/10',
}
`)
  write(root, 'src/components/workspaces/TaskChecklistField.tsx', `export function X({ todo }: any) {
  return <span className="shrink-0 text-[color:var(--color-status-in-progress)]">{'text-[color:var(--color-status-in-progress)]'}</span>
}
`)
  write(root, 'src/components/workspaces/TaskDetailPanel.tsx', `export function TaskDetailPanel() {
  return <div>
    <Badge className="h-8 text-xs bg-[var(--color-status-in-progress)]/10 text-[color:var(--color-status-in-progress)] border-transparent rounded-md px-2 inline-flex items-center">In Progress</Badge>
    <Badge className="h-8 text-xs bg-[var(--color-status-blocked)]/10 text-[color:var(--color-status-blocked)] border-transparent rounded-md px-2 inline-flex items-center">Blocked (dependency unmet)</Badge>
  </div>
}
`)
  write(root, 'src/components/calendar/types.ts', `import { statusContract } from '@/design-system/status'
export const STATUS_STYLE: Record<string, { bg: string; icon: string }> = {
  done: { bg: statusContract.done.resolvedColor, icon: 'CheckCircle' },
  in_progress: { bg: statusContract.inProgress.resolvedColor, icon: 'CircleNotch' },
  blocked: { bg: statusContract.blocked.resolvedColor, icon: 'Prohibit' },
  failed: { bg: statusContract.failed.resolvedColor, icon: 'XCircle' },
  inbox: { bg: statusContract.inbox.resolvedColor, icon: 'Circle' },
  next: { bg: statusContract.next.resolvedColor, icon: 'Circle' },
}
`)
  write(root, 'src/components/settings/DefaultModelCard.tsx', `export function DefaultModelCard() {
  return <p className="mt-1 text-sm text-[color:var(--color-error)]">Could not load</p>
}
`)
}

// ── PASS 1 (--literal): design-system/status-literal ────────────────────

test('LITERAL match: statusColors.ts + planStateColors.ts hygiene swap is invisible and reconciles to 13', () => {
  const root = fixtureRepo()
  write(root, 'docs/internal/design/design-system-definition.md', D4_DOC)
  write(root, 'src/lib/statusColors.ts', STATUS_COLORS_TS)
  write(root, 'src/lib/planStateColors.ts', PLAN_STATE_COLORS_TS)

  const result = runCodemod({ repoRoot: root, apply: false, mode: MODE_LITERAL })

  assert.equal(result.totalRefusals, 0)
  assert.equal(result.totalFilesScanned, 2)
  assert.equal(result.totalFilesTouched, 2)
  // 6 STATUS_COLORS properties + TASK_CANCELLED_COLOR + import = 8 edits;
  // 5 PLAN_STATE_COLORS properties + PLAN_CANCELLED_COLOR + import = 7 edits.
  // (7 + 6 = 13 governed-value swaps, matching M5's 13-item count; the two
  // import insertions are additional structural edits, not colour swaps.)
  const colourSwaps = result.files.reduce((n, f) => n + f.edits.filter((e) => !e.syntax.startsWith('+ import')).length, 0)
  assert.equal(colourSwaps, 13)

  const statusColorsFile = result.files.find((f) => f.file === 'src/lib/statusColors.ts')
  assert.match(statusColorsFile.diff, /statusContract\.inbox\.resolvedColor/)
  assert.match(statusColorsFile.diff, /statusContract\.cancelled\.resolvedColor/)
  assert.match(statusColorsFile.diff, /import \{ statusContract \} from '@\/design-system\/status'/)

  const planStateColorsFile = result.files.find((f) => f.file === 'src/lib/planStateColors.ts')
  assert.match(planStateColorsFile.diff, /draft: statusContract\.inbox\.resolvedColor/)
  assert.match(planStateColorsFile.diff, /approved: statusContract\.next\.resolvedColor/)
  assert.match(planStateColorsFile.diff, /running: statusContract\.inProgress\.resolvedColor/)
})

test('LITERAL refusal: a literal that no longer matches the D4 canonical hex is refused, not guessed', () => {
  const root = fixtureRepo()
  write(root, 'docs/internal/design/design-system-definition.md', D4_DOC)
  write(root, 'src/lib/statusColors.ts', STATUS_COLORS_TS.replace("inbox: '#9ca3af',", "inbox: '#123456',"))
  write(root, 'src/lib/planStateColors.ts', PLAN_STATE_COLORS_TS)

  const result = runCodemod({ repoRoot: root, apply: false, mode: MODE_LITERAL })

  const statusColorsFile = result.files.find((f) => f.file === 'src/lib/statusColors.ts')
  const inboxRefusal = statusColorsFile.refusals.find((r) => /#123456/.test(r.reason))
  assert.ok(inboxRefusal, 'expected a refusal citing the drifted #123456 literal')
  assert.match(inboxRefusal.reason, /value changed since analysis/)
  assert.match(inboxRefusal.reason, /#9ca3af/)
  // the other 5 STATUS_COLORS properties + TASK_CANCELLED_COLOR still edit cleanly, plus the import insertion
  assert.equal(statusColorsFile.editCount, 7)
})

test('LITERAL refusal: structural drift (renamed object) is refused per property, not silently skipped', () => {
  const root = fixtureRepo()
  write(root, 'docs/internal/design/design-system-definition.md', D4_DOC)
  write(root, 'src/lib/statusColors.ts', STATUS_COLORS_TS.replace('STATUS_COLORS', 'RENAMED_STATUS_COLORS'))
  write(root, 'src/lib/planStateColors.ts', PLAN_STATE_COLORS_TS)

  const result = runCodemod({ repoRoot: root, apply: false, mode: MODE_LITERAL })
  const statusColorsFile = result.files.find((f) => f.file === 'src/lib/statusColors.ts')
  // TASK_CANCELLED_COLOR is a separate top-level const, unaffected by the
  // rename — it still edits cleanly, plus the import insertion it triggers.
  assert.equal(statusColorsFile.editCount, 2)
  assert.equal(statusColorsFile.refusals.length, 6)
  for (const r of statusColorsFile.refusals) assert.match(r.reason, /not found \(structural drift\)/)
})

test('LITERAL idempotence: a second --apply run makes zero further edits', () => {
  const root = fixtureRepo()
  write(root, 'docs/internal/design/design-system-definition.md', D4_DOC)
  write(root, 'src/lib/statusColors.ts', STATUS_COLORS_TS)
  write(root, 'src/lib/planStateColors.ts', PLAN_STATE_COLORS_TS)

  const first = runCodemod({ repoRoot: root, apply: true, mode: MODE_LITERAL })
  assert.equal(first.totalEdits, 15) // 13 colour swaps + 2 import insertions
  const second = runCodemod({ repoRoot: root, apply: true, mode: MODE_LITERAL })
  assert.equal(second.totalEdits, 0)
  assert.equal(second.totalRefusals, 0)
  assert.equal(second.totalAlreadyMigrated, 13)
})

// ── PASS 2 (--mismatch): design-system/status-mismatch ───────────────────

test('MISMATCH match: all five edit-sites reconcile to 14 occurrences across 13 ledger fingerprints', () => {
  const root = fixtureRepo()
  write(root, 'docs/internal/design/design-system-definition.md', D4_DOC)
  write(root, 'src/components/workspaces/taskStatusConfig.ts', TASK_STATUS_CONFIG_TS)
  write(root, 'src/components/workspaces/TaskChecklistField.tsx', checklistFieldTsx())
  write(root, 'src/components/workspaces/TaskDetailPanel.tsx', taskDetailPanelTsx())
  write(root, 'src/components/calendar/types.ts', CALENDAR_TYPES_TS)
  write(root, 'src/components/settings/DefaultModelCard.tsx', DEFAULT_MODEL_CARD_TSX)
  // excluded-site fixtures — must be scanned and refused, never edited
  write(root, 'src/components/agents/AgentCard.tsx', AGENT_CARD_TSX)
  write(root, 'src/components/library/preview/LibraryPdfPreview.tsx', LIBRARY_PDF_PREVIEW_TSX)
  write(root, 'src/routes/onboarding.tsx', ONBOARDING_TSX)
  write(root, 'src/components/chat/tools/SetGoalToolUI.tsx', SET_GOAL_TOOL_UI_TSX)
  write(root, 'src/components/chat/AskUserQuestionCard.tsx', ASK_USER_QUESTION_CARD_TSX)

  const result = runCodemod({ repoRoot: root, apply: false, mode: MODE_MISMATCH })

  assert.equal(result.totalFilesScanned, 10)
  assert.equal(result.totalFilesTouched, 5)
  assert.equal(result.totalRefusals, 5) // exactly the 5 excluded sites

  const byFile = Object.fromEntries(result.files.map((f) => [f.file, f]))

  assert.equal(byFile['src/components/workspaces/taskStatusConfig.ts'].editCount, 3)
  assert.match(byFile['src/components/workspaces/taskStatusConfig.ts'].diff, /--color-status-next/)
  assert.match(byFile['src/components/workspaces/taskStatusConfig.ts'].diff, /--color-status-in-progress/)
  assert.match(byFile['src/components/workspaces/taskStatusConfig.ts'].diff, /--color-status-blocked/)

  assert.equal(byFile['src/components/workspaces/TaskChecklistField.tsx'].editCount, 2)
  assert.equal(byFile['src/components/workspaces/TaskDetailPanel.tsx'].editCount, 2)
  assert.match(byFile['src/components/workspaces/TaskDetailPanel.tsx'].diff, /--color-status-in-progress/)
  assert.match(byFile['src/components/workspaces/TaskDetailPanel.tsx'].diff, /--color-status-blocked/)

  assert.equal(byFile['src/components/calendar/types.ts'].editCount, 7) // 6 colour swaps + 1 import insertion
  assert.match(byFile['src/components/calendar/types.ts'].diff, /statusContract\.inProgress\.resolvedColor/)
  assert.match(byFile['src/components/calendar/types.ts'].diff, /import \{ statusContract \} from '@\/design-system\/status'/)

  assert.equal(byFile['src/components/settings/DefaultModelCard.tsx'].editCount, 1)
  assert.match(byFile['src/components/settings/DefaultModelCard.tsx'].diff, /--color-error/)

  const totalEdits = result.files.reduce((n, f) => n + f.editCount, 0)
  // 3 (taskStatusConfig) + 2 (checklist) + 2 (detail panel) + 6 (calendar, incl. import) + 1 (default model card) = 14 colour/token edits
  // plus 1 import insertion in calendar/types.ts = 15 total edits recorded.
  assert.equal(totalEdits, 15)
})

test('MISMATCH refusal: each excluded site kind is refused with its M5 category, never edited', () => {
  const root = fixtureRepo()
  writeFullyMigratedMismatchRepo(root)
  write(root, 'src/components/agents/AgentCard.tsx', AGENT_CARD_TSX)
  write(root, 'src/components/library/preview/LibraryPdfPreview.tsx', LIBRARY_PDF_PREVIEW_TSX)
  write(root, 'src/routes/onboarding.tsx', ONBOARDING_TSX)
  write(root, 'src/components/chat/tools/SetGoalToolUI.tsx', SET_GOAL_TOOL_UI_TSX)
  write(root, 'src/components/chat/AskUserQuestionCard.tsx', ASK_USER_QUESTION_CARD_TSX)

  const result = runCodemod({ repoRoot: root, apply: false, mode: MODE_MISMATCH })

  assert.equal(result.totalEdits, 0, 'already-migrated real sites + excluded sites must produce zero edits')
  assert.equal(result.totalRefusals, 5)

  const byFile = Object.fromEntries(result.files.map((f) => [f.file, f]))
  assert.match(byFile['src/components/agents/AgentCard.tsx'].refusals[0].reason, /cross-domain-ambiguous/)
  assert.match(byFile['src/components/library/preview/LibraryPdfPreview.tsx'].refusals[0].reason, /likely-false-positive/)
  assert.match(byFile['src/routes/onboarding.tsx'].refusals[0].reason, /likely-false-positive/)
  assert.match(byFile['src/components/chat/tools/SetGoalToolUI.tsx'].refusals[0].reason, /likely-false-positive/)
  assert.match(byFile['src/components/chat/AskUserQuestionCard.tsx'].refusals[0].reason, /likely-false-positive/)
  for (const f of Object.values(byFile)) assert.equal(f.editCount, 0)
})

test('MISMATCH refusal: TaskDetailPanel refuses when the two occurrences cannot be told apart', () => {
  const root = fixtureRepo()
  write(root, 'docs/internal/design/design-system-definition.md', D4_DOC)
  write(root, 'src/components/workspaces/TaskDetailPanel.tsx', taskDetailPanelTsx({ inProgressText: 'Something Else', blockedText: 'Also Unrelated' }))

  const result = runCodemod({ repoRoot: root, apply: false, mode: MODE_MISMATCH })
  const file = result.files.find((f) => f.file === 'src/components/workspaces/TaskDetailPanel.tsx')
  assert.equal(file.editCount, 0)
  assert.ok(file.refusals.some((r) => /could not disambiguate/.test(r.reason)))
})

test('MISMATCH refusal: TaskDetailPanel refuses on an unexpected occurrence count instead of guessing', () => {
  const root = fixtureRepo()
  write(root, 'docs/internal/design/design-system-definition.md', D4_DOC)
  write(root, 'src/components/workspaces/TaskDetailPanel.tsx', `export function TaskDetailPanel() {
  return <Badge className="h-8 text-xs bg-[var(--color-warning)]/10 text-[color:var(--color-warning)] border-transparent rounded-md px-2 inline-flex items-center">In Progress</Badge>
}
`)
  const result = runCodemod({ repoRoot: root, apply: false, mode: MODE_MISMATCH })
  const file = result.files.find((f) => f.file === 'src/components/workspaces/TaskDetailPanel.tsx')
  assert.equal(file.editCount, 0)
  assert.equal(file.refusals.length, 1)
  assert.match(file.refusals[0].reason, /expected exactly 2 occurrences/)
})

test('MISMATCH idempotence: a second --apply run makes zero further edits', () => {
  const root = fixtureRepo()
  write(root, 'docs/internal/design/design-system-definition.md', D4_DOC)
  write(root, 'src/components/workspaces/taskStatusConfig.ts', TASK_STATUS_CONFIG_TS)
  write(root, 'src/components/workspaces/TaskChecklistField.tsx', checklistFieldTsx())
  write(root, 'src/components/workspaces/TaskDetailPanel.tsx', taskDetailPanelTsx())
  write(root, 'src/components/calendar/types.ts', CALENDAR_TYPES_TS)
  write(root, 'src/components/settings/DefaultModelCard.tsx', DEFAULT_MODEL_CARD_TSX)

  const first = runCodemod({ repoRoot: root, apply: true, mode: MODE_MISMATCH })
  assert.equal(first.totalEdits, 15) // 14 colour/token swaps + 1 import insertion (calendar/types.ts)
  const second = runCodemod({ repoRoot: root, apply: true, mode: MODE_MISMATCH })
  assert.equal(second.totalEdits, 0)
  assert.equal(second.totalRefusals, 0)
  assert.equal(second.totalAlreadyMigrated, 14)
})

// ── D4 doc parsing ────────────────────────────────────────────────────────

test('parseD4CanonicalHexTable reads exactly the 7 D4 states and stops before the next heading', () => {
  const root = fixtureRepo()
  write(root, 'docs/internal/design/design-system-definition.md', D4_DOC)
  const table = parseD4CanonicalHexTable(root)
  assert.deepEqual(
    Object.fromEntries(table),
    {
      inbox: '#9CA3AF',
      next: '#3B82F6',
      inProgress: '#D4AF37',
      blocked: '#F97316',
      done: '#10B981',
      failed: '#EF4444',
      cancelled: '#EAB308',
    },
  )
})

// ── allowed-roots guard ───────────────────────────────────────────────────

test('allowed-roots guard: every site this codemod can touch resolves inside src/ or packages/ui/src/', () => {
  const root = fixtureRepo()
  const touchableFiles = [
    'src/lib/statusColors.ts',
    'src/lib/planStateColors.ts',
    'src/components/workspaces/taskStatusConfig.ts',
    'src/components/workspaces/TaskChecklistField.tsx',
    'src/components/workspaces/TaskDetailPanel.tsx',
    'src/components/calendar/types.ts',
    'src/components/settings/DefaultModelCard.tsx',
    'src/components/agents/AgentCard.tsx',
    'src/components/library/preview/LibraryPdfPreview.tsx',
    'src/routes/onboarding.tsx',
    'src/components/chat/tools/SetGoalToolUI.tsx',
    'src/components/chat/AskUserQuestionCard.tsx',
  ]
  for (const f of touchableFiles) assert.equal(isInAllowedRoot(root, resolve(root, f)), true, f)
  // and the guard genuinely rejects paths this codemod must never write to
  assert.equal(isInAllowedRoot(root, resolve(root, 'design-system/enforcement/ledger.json')), false)
  assert.equal(isInAllowedRoot(root, resolve(root, 'docs/internal/design/design-system-definition.md')), false)
  assert.equal(isInAllowedRoot(root, resolve(root, '../outside.ts')), false)
})

// ── mutation proof (writes to an isolated scratch copy, then re-reads from disk) ──

test('MUTATION PROOF (literal): --apply actually mutates bytes on disk in a scratch copy', () => {
  const root = fixtureRepo()
  write(root, 'docs/internal/design/design-system-definition.md', D4_DOC)
  const statusColorsPath = write(root, 'src/lib/statusColors.ts', STATUS_COLORS_TS)
  write(root, 'src/lib/planStateColors.ts', PLAN_STATE_COLORS_TS)

  const before = readFileSync(statusColorsPath, 'utf8')
  assert.match(before, /inbox: '#9ca3af'/)

  const result = runCodemod({ repoRoot: root, apply: true, mode: MODE_LITERAL })
  assert.ok(result.totalEdits > 0)

  const after = readFileSync(statusColorsPath, 'utf8')
  assert.notEqual(after, before)
  assert.doesNotMatch(after, /inbox: '#9ca3af'/)
  assert.match(after, /inbox: statusContract\.inbox\.resolvedColor/)
  assert.match(after, /import \{ statusContract \} from '@\/design-system\/status'/)
})

test('MUTATION PROOF (mismatch): --apply actually mutates the rendered colour on disk in a scratch copy', () => {
  const root = fixtureRepo()
  write(root, 'docs/internal/design/design-system-definition.md', D4_DOC)
  const detailPanelPath = write(root, 'src/components/workspaces/TaskDetailPanel.tsx', taskDetailPanelTsx())

  const before = readFileSync(detailPanelPath, 'utf8')
  const beforeInProgressAmber = (before.match(/text-\[color:var\(--color-warning\)\]/g) || []).length
  assert.equal(beforeInProgressAmber, 2, 'fixture must start with both badges sharing the same amber token')

  const result = runCodemod({ repoRoot: root, apply: true, mode: MODE_MISMATCH })
  assert.equal(result.files.find((f) => f.file === 'src/components/workspaces/TaskDetailPanel.tsx').editCount, 2)

  const after = readFileSync(detailPanelPath, 'utf8')
  assert.notEqual(after, before)
  assert.doesNotMatch(after, /--color-warning/, 'the shared wrong-amber token must be fully gone after apply')
  assert.match(after, /--color-status-in-progress/)
  assert.match(after, /--color-status-blocked/)
})
