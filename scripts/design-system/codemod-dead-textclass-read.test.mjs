import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { computeSafeFunctionNames, computeSafeSentinelFunctionNames, planFileEdits, runCodemod } from './codemod-dead-textclass-read.mjs'
import { parseSourceFile } from './codemod-lib.mjs'

const fixtureRoots = []
after(() => {
  for (const root of fixtureRoots) rmSync(root, { recursive: true, force: true })
})

function fixtureRepo() {
  const root = mkdtempSync(resolve('dist/design-system-baseline/codemod-textclass-'))
  fixtureRoots.push(root)
  return root
}

function write(root, relPath, content) {
  const abs = resolve(root, relPath)
  mkdirSync(dirname(abs), { recursive: true })
  writeFileSync(abs, content, 'utf8')
  return abs
}

const SHARED_MODULE = `
export function getToolBadgeStatusConfig(status) {
  switch (status) {
    case 'running':
      return { indicator: null, label: 'Running...' }
    case 'success':
      return { indicator: null, label: 'Done' }
    default:
      return { indicator: null, label: 'Failed' }
  }
}

export function getSpanStatusDot(status) {
  switch (status) {
    case 'running':
      return { indicator: null, label: 'working' }
    default:
      return { indicator: null, label: 'done' }
  }
}
`

function writeSharedModule(root) {
  write(root, 'src/lib/toolStatusConfig.tsx', SHARED_MODULE)
}

// Mirrors the real src/components/chat/tools/toolResultSentinels.ts shape:
// a `let statusConfig = null` initializer, reassigned to a real-literal
// object (no `textClass`) on each of an if/else-if chain's branches, and
// returned as a field of the module's return object — exactly the shape
// ToolCallBadge.tsx/GenericToolCall.tsx read `sentinels.statusConfig` from.
const SENTINEL_MODULE_SAFE = `
export function detectToolResultSentinels(result) {
  const delegationFailure = result && result.error === 'delegation_denied' ? result : null
  const fileExistsRefusal = result && result.error === 'file_exists' ? result : null

  let statusConfig = null
  if (delegationFailure) {
    statusConfig = { indicator: null, label: 'Delegation denied' }
  } else if (fileExistsRefusal) {
    statusConfig = { indicator: null, label: 'File already exists' }
  }

  return {
    delegationFailure,
    fileExistsRefusal,
    any: !!delegationFailure || !!fileExistsRefusal,
    statusConfig,
  }
}
`

// Drift fixture: one branch now genuinely sets textClass, same as the
// drift-guard test for computeSafeFunctionNames — proves the second-order
// safety is re-derived from source, not hardcoded.
const SENTINEL_MODULE_UNSAFE = SENTINEL_MODULE_SAFE.replace(
  "statusConfig = { indicator: null, label: 'File already exists' }",
  "statusConfig = { indicator: null, label: 'File already exists', textClass: 'text-[var(--color-warning)]' }",
)

function writeSentinelModule(root, content = SENTINEL_MODULE_SAFE) {
  write(root, 'src/components/chat/tools/toolResultSentinels.ts', content)
}

test('computeSafeFunctionNames proves both factories safe when no branch sets textClass', () => {
  const root = fixtureRepo()
  writeSharedModule(root)
  const { safe, checked } = computeSafeFunctionNames(root)
  assert.deepEqual([...checked].sort(), ['getSpanStatusDot', 'getToolBadgeStatusConfig'])
  assert.deepEqual([...safe].sort(), ['getSpanStatusDot', 'getToolBadgeStatusConfig'])
})

test('computeSafeFunctionNames excludes a factory that DOES assign textClass on any branch (drift guard)', () => {
  const root = fixtureRepo()
  write(root, 'src/lib/toolStatusConfig.tsx', SHARED_MODULE.replace(
    "return { indicator: null, label: 'Failed' }",
    "return { indicator: null, label: 'Failed', textClass: 'text-[var(--color-error)]' }",
  ))
  const { safe } = computeSafeFunctionNames(root)
  assert.equal(safe.has('getToolBadgeStatusConfig'), false, 'must not be treated as safe once a branch sets textClass')
  assert.equal(safe.has('getSpanStatusDot'), true, 'the untouched sibling function stays safe')
})

// ── Case 1: the match — direct call, removed ────────────────────────────────
test('MATCH: direct getToolBadgeStatusConfig() call — removes the dead textClass argument, byte-identical otherwise', () => {
  const root = fixtureRepo()
  writeSharedModule(root)
  const before = `import { getToolBadgeStatusConfig } from '@/lib/toolStatusConfig'
import { cn } from '@/lib/utils'

export function Row({ status }) {
  const statusConfig = getToolBadgeStatusConfig(status)
  return <span className={cn('text-[var(--color-muted)] shrink-0', statusConfig.textClass)}>{statusConfig.label}</span>
}
`
  const file = write(root, 'src/components/Row.tsx', before)
  const { safe } = computeSafeFunctionNames(root)
  const { edits, refusals, text, source } = planFileEdits(root, file, safe)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  const after = editedText(text, edits)
  assert.equal(after, before.replace(", statusConfig.textClass)}>{statusConfig.label}", ")}>{statusConfig.label}"))
  // sanity: still parses as valid TSX after the edit
  const reparsed = parseSourceFile(file, after)
  assert.equal(reparsed.parseDiagnostics?.length ?? 0, 0)
  void source
})

test('MATCH: sole argument — cn(statusConfig.textClass) becomes cn()', () => {
  const root = fixtureRepo()
  writeSharedModule(root)
  const before = `import { getToolBadgeStatusConfig } from '@/lib/toolStatusConfig'
import { cn } from '@/lib/utils'

export function Row({ status }) {
  const statusConfig = getToolBadgeStatusConfig(status)
  return <span className={cn(statusConfig.textClass)}>{statusConfig.label}</span>
}
`
  const file = write(root, 'src/components/SoleArg.tsx', before)
  const { safe } = computeSafeFunctionNames(root)
  const { edits, refusals, text } = planFileEdits(root, file, safe)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  const after = editedText(text, edits)
  assert.match(after, /className=\{cn\(\)\}/)
})

test('MATCH: ternary chain of direct calls (e.g. BrowserNavigate/BrowserTool shape)', () => {
  const root = fixtureRepo()
  writeSharedModule(root)
  const before = `import { getToolBadgeStatusConfig } from '@/lib/toolStatusConfig'
import { cn } from '@/lib/utils'

export function Row({ isRunning, isCancelled }) {
  const statusConfig = isRunning
    ? getToolBadgeStatusConfig('running')
    : isCancelled
      ? getToolBadgeStatusConfig('cancelled')
      : getToolBadgeStatusConfig('success')
  return <span className={cn('shrink-0', statusConfig.textClass)}>{statusConfig.label}</span>
}
`
  const file = write(root, 'src/components/Ternary.tsx', before)
  const { safe } = computeSafeFunctionNames(root)
  const { edits, refusals, text } = planFileEdits(root, file, safe)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  const after = editedText(text, edits)
  assert.doesNotMatch(after, /textClass/)
})

test('MATCH: if/else-if reassignment chain of direct calls (e.g. GenericToolCall running/cancelled branches)', () => {
  const root = fixtureRepo()
  writeSharedModule(root)
  const before = `import { getToolBadgeStatusConfig } from '@/lib/toolStatusConfig'
import { cn } from '@/lib/utils'

export function Row({ isRunning, isCancelled }) {
  let statusConfig
  if (isRunning) {
    statusConfig = getToolBadgeStatusConfig('running')
  } else if (isCancelled) {
    statusConfig = getToolBadgeStatusConfig('cancelled')
  } else {
    statusConfig = getToolBadgeStatusConfig('success')
  }
  return <span className={cn('shrink-0', statusConfig.textClass)}>{statusConfig.label}</span>
}
`
  const file = write(root, 'src/components/IfElse.tsx', before)
  const { safe } = computeSafeFunctionNames(root)
  const { edits, refusals } = planFileEdits(root, file, safe)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
})

// ── Case 2: no-match — file never imports either factory ───────────────────
test('NO-MATCH: a file with its own unrelated `.textClass` read (no import of the shared factories) is left untouched', () => {
  const root = fixtureRepo()
  writeSharedModule(root)
  const before = `import { cn } from '@/lib/utils'

export function Row({ config }) {
  return <span className={cn('shrink-0', config.textClass)}>{config.label}</span>
}
`
  const file = write(root, 'src/components/Unrelated.tsx', before)
  const { safe } = computeSafeFunctionNames(root)
  const { edits, refusals } = planFileEdits(root, file, safe)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0, 'file is skipped entirely, not even flagged as a refusal — it never imports a safe factory')
})

// ── Case 3: ambiguous — must be refused, not edited ─────────────────────────
test('AMBIGUOUS: a ternary branch that is a real object literal setting textClass is refused (FileWriteConfirm shape)', () => {
  const root = fixtureRepo()
  writeSharedModule(root)
  const before = `import { getToolBadgeStatusConfig } from '@/lib/toolStatusConfig'
import { cn } from '@/lib/utils'

export function Row({ isRefusal, isRunning, status }) {
  const statusConfig = isRefusal && !isRunning
    ? { indicator: null, label: 'File already exists', textClass: 'text-[var(--color-warning)]' }
    : getToolBadgeStatusConfig(status)
  return <span className={cn('shrink-0', statusConfig.textClass)}>{statusConfig.label}</span>
}
`
  const file = write(root, 'src/components/Refusal.tsx', before)
  const { safe } = computeSafeFunctionNames(root)
  const { edits, refusals, text } = planFileEdits(root, file, safe)
  assert.equal(edits.length, 0, 'must not edit — one branch genuinely varies')
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /explicitly sets textClass/)
  assert.equal(readFileSync(file, 'utf8'), text, 'file on disk is untouched')
})

test('AMBIGUOUS: a `??` fallback to a property access is refused (ToolCallBadge/GenericToolCall shape)', () => {
  const root = fixtureRepo()
  writeSharedModule(root)
  const before = `import { getToolBadgeStatusConfig } from '@/lib/toolStatusConfig'
import { cn } from '@/lib/utils'

export function Row({ sentinels, status, durationMs }) {
  const config = sentinels.statusConfig ?? getToolBadgeStatusConfig(status, { durationMs })
  return <span className={cn('shrink-0', config.textClass)}>{config.label}</span>
}
`
  const file = write(root, 'src/components/Sentinel.tsx', before)
  const { safe } = computeSafeFunctionNames(root)
  const { edits, refusals } = planFileEdits(root, file, safe)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /unrecognized origin shape/)
})

test('AMBIGUOUS: used somewhere other than a direct class-builder argument is refused', () => {
  const root = fixtureRepo()
  writeSharedModule(root)
  const before = `import { getToolBadgeStatusConfig } from '@/lib/toolStatusConfig'

export function Row({ status }) {
  const statusConfig = getToolBadgeStatusConfig(status)
  const extra = statusConfig.textClass
  return <span>{extra}</span>
}
`
  const file = write(root, 'src/components/NotClassBuilder.tsx', before)
  const { safe } = computeSafeFunctionNames(root)
  const { edits, refusals } = planFileEdits(root, file, safe)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /not a direct argument/)
})

// ── Case 4: idempotency ──────────────────────────────────────────────────
test('IDEMPOTENT: a second --apply over an already-fixed tree makes zero further changes', () => {
  const root = fixtureRepo()
  writeSharedModule(root)
  write(root, 'src/components/Row.tsx', `import { getToolBadgeStatusConfig } from '@/lib/toolStatusConfig'
import { cn } from '@/lib/utils'

export function Row({ status }) {
  const statusConfig = getToolBadgeStatusConfig(status)
  return <span className={cn('shrink-0', statusConfig.textClass)}>{statusConfig.label}</span>
}
`)
  const first = runCodemod({ repoRoot: root, apply: true })
  assert.equal(first.totalEdits, 1)
  const afterFirst = readFileSync(resolve(root, 'src/components/Row.tsx'), 'utf8')

  const second = runCodemod({ repoRoot: root, apply: true })
  assert.equal(second.totalEdits, 0, 'idempotent: nothing left to remove')
  const afterSecond = readFileSync(resolve(root, 'src/components/Row.tsx'), 'utf8')
  assert.equal(afterSecond, afterFirst, 'file is byte-identical after the second apply')
})

test('never edits outside src/ or packages/ui/src/', () => {
  const root = fixtureRepo()
  writeSharedModule(root)
  write(root, 'scripts/other/Row.tsx', `import { getToolBadgeStatusConfig } from '@/lib/toolStatusConfig'
import { cn } from '@/lib/utils'
export function Row({ status }) {
  const statusConfig = getToolBadgeStatusConfig(status)
  return <span className={cn('shrink-0', statusConfig.textClass)}>{statusConfig.label}</span>
}
`)
  const result = runCodemod({ repoRoot: root, apply: true })
  assert.equal(result.totalFilesTouched, 0, 'scripts/ is outside the allowed roots and must never be scanned/edited')
})

// ── Second-order origin: sentinels.statusConfig (ToolCallBadge/GenericToolCall) ──

test('computeSafeSentinelFunctionNames proves detectToolResultSentinels safe when no literal branch sets textClass', () => {
  const root = fixtureRepo()
  writeSharedModule(root)
  writeSentinelModule(root, SENTINEL_MODULE_SAFE)
  const { safe: safeFactories } = computeSafeFunctionNames(root)
  const { safe, checked } = computeSafeSentinelFunctionNames(root, safeFactories)
  assert.deepEqual([...checked], ['detectToolResultSentinels'])
  assert.equal(safe.has('detectToolResultSentinels'), true)
})

test('computeSafeSentinelFunctionNames excludes detectToolResultSentinels once a branch assigns textClass (drift guard)', () => {
  const root = fixtureRepo()
  writeSharedModule(root)
  writeSentinelModule(root, SENTINEL_MODULE_UNSAFE)
  const { safe: safeFactories } = computeSafeFunctionNames(root)
  const { safe } = computeSafeSentinelFunctionNames(root, safeFactories)
  assert.equal(safe.has('detectToolResultSentinels'), false, 'must not be treated as safe once a branch sets textClass')
})

test('computeSafeSentinelFunctionNames returns empty (not a throw) when the repo has no toolResultSentinels module', () => {
  const root = fixtureRepo()
  writeSharedModule(root)
  const { safe: safeFactories } = computeSafeFunctionNames(root)
  const { safe, checked } = computeSafeSentinelFunctionNames(root, safeFactories)
  assert.equal(safe.size, 0)
  assert.equal(checked.size, 0)
})

test('MATCH: second-order `??` fallback to a proven-safe sentinel detector (ToolCallBadge shape) — removes the dead textClass argument', () => {
  const root = fixtureRepo()
  writeSharedModule(root)
  writeSentinelModule(root, SENTINEL_MODULE_SAFE)
  const before = `import { getToolBadgeStatusConfig } from '@/lib/toolStatusConfig'
import { cn } from '@/lib/utils'
import { detectToolResultSentinels } from './tools/toolResultSentinels'

export function ToolCallBadgeLike({ toolCall }) {
  const sentinels = detectToolResultSentinels(toolCall.result)
  const config = sentinels.statusConfig ?? getToolBadgeStatusConfig(toolCall.status, { durationMs: toolCall.duration_ms })
  return <span className={cn('text-[var(--color-muted)]', config.textClass)}>{config.label}</span>
}
`
  const file = write(root, 'src/components/ToolCallBadgeLike.tsx', before)
  const { safe: safeFactories } = computeSafeFunctionNames(root)
  const { safe: safeSentinels } = computeSafeSentinelFunctionNames(root, safeFactories)
  assert.equal(safeSentinels.has('detectToolResultSentinels'), true, 'sanity: fixture sentinel module must be proven safe')
  const { edits, refusals, text } = planFileEdits(root, file, safeFactories, safeSentinels)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  const after = editedText(text, edits)
  assert.doesNotMatch(after, /textClass/)
  assert.match(after, /className=\{cn\('text-\[var\(--color-muted\)\]'\)\}/)
  const reparsed = parseSourceFile(file, after)
  assert.equal(reparsed.parseDiagnostics?.length ?? 0, 0)
})

test('MATCH: second-order if/else-if reassignment branch `statusConfig = sentinels.statusConfig` (GenericToolCall shape) — removes the dead textClass argument', () => {
  const root = fixtureRepo()
  writeSharedModule(root)
  writeSentinelModule(root, SENTINEL_MODULE_SAFE)
  const before = `import { getToolBadgeStatusConfig } from '@/lib/toolStatusConfig'
import { cn } from '@/lib/utils'
import { detectToolResultSentinels } from './toolResultSentinels'

export function GenericToolCallLike({ isRunning, isCancelled, isError, result, durationMs }) {
  const sentinels = detectToolResultSentinels(result)
  let statusConfig
  if (isRunning) {
    statusConfig = getToolBadgeStatusConfig('running')
  } else if (isCancelled) {
    statusConfig = getToolBadgeStatusConfig('cancelled')
  } else if (sentinels.statusConfig) {
    statusConfig = sentinels.statusConfig
  } else if (isError) {
    statusConfig = getToolBadgeStatusConfig('error')
  } else {
    statusConfig = getToolBadgeStatusConfig('success', { durationMs })
  }
  return <span className={cn('text-[var(--color-muted)]', statusConfig.textClass)}>{statusConfig.label}</span>
}
`
  const file = write(root, 'src/components/tools/GenericToolCallLike.tsx', before)
  const { safe: safeFactories } = computeSafeFunctionNames(root)
  const { safe: safeSentinels } = computeSafeSentinelFunctionNames(root, safeFactories)
  const { edits, refusals, text } = planFileEdits(root, file, safeFactories, safeSentinels)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  const after = editedText(text, edits)
  assert.doesNotMatch(after, /textClass/)
})

test('AMBIGUOUS: second-order origin stays refused when toolResultSentinels.ts is NOT proven safe (drift guard propagates to consumers)', () => {
  const root = fixtureRepo()
  writeSharedModule(root)
  writeSentinelModule(root, SENTINEL_MODULE_UNSAFE)
  const before = `import { getToolBadgeStatusConfig } from '@/lib/toolStatusConfig'
import { cn } from '@/lib/utils'
import { detectToolResultSentinels } from './tools/toolResultSentinels'

export function ToolCallBadgeLike({ toolCall }) {
  const sentinels = detectToolResultSentinels(toolCall.result)
  const config = sentinels.statusConfig ?? getToolBadgeStatusConfig(toolCall.status, { durationMs: toolCall.duration_ms })
  return <span className={cn('text-[var(--color-muted)]', config.textClass)}>{config.label}</span>
}
`
  const file = write(root, 'src/components/ToolCallBadgeDrift.tsx', before)
  const { safe: safeFactories } = computeSafeFunctionNames(root)
  const { safe: safeSentinels } = computeSafeSentinelFunctionNames(root, safeFactories)
  assert.equal(safeSentinels.size, 0, 'sanity: drifted sentinel module must not be proven safe')
  const { edits, refusals, text } = planFileEdits(root, file, safeFactories, safeSentinels)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.equal(readFileSync(file, 'utf8'), text, 'file on disk is untouched')
})

test('AMBIGUOUS: second-order origin refused when the base identifier is not itself proven to originate from a safe sentinel-detector call', () => {
  const root = fixtureRepo()
  writeSharedModule(root)
  writeSentinelModule(root, SENTINEL_MODULE_SAFE)
  const before = `import { getToolBadgeStatusConfig } from '@/lib/toolStatusConfig'
import { cn } from '@/lib/utils'
import { detectToolResultSentinels } from './tools/toolResultSentinels'

export function Row({ sentinels, status, durationMs }) {
  const config = sentinels.statusConfig ?? getToolBadgeStatusConfig(status, { durationMs })
  return <span className={cn('shrink-0', config.textClass)}>{config.label}</span>
}
`
  const file = write(root, 'src/components/PropSentinel.tsx', before)
  const { safe: safeFactories } = computeSafeFunctionNames(root)
  const { safe: safeSentinels } = computeSafeSentinelFunctionNames(root, safeFactories)
  const { edits, refusals } = planFileEdits(root, file, safeFactories, safeSentinels)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /not proven to originate only from a safe sentinel-detector call/)
})

test('IDEMPOTENT: second-order origin — a second --apply over an already-fixed tree makes zero further changes', () => {
  const root = fixtureRepo()
  writeSharedModule(root)
  writeSentinelModule(root, SENTINEL_MODULE_SAFE)
  write(root, 'src/components/ToolCallBadgeLike2.tsx', `import { getToolBadgeStatusConfig } from '@/lib/toolStatusConfig'
import { cn } from '@/lib/utils'
import { detectToolResultSentinels } from './tools/toolResultSentinels'

export function ToolCallBadgeLike({ toolCall }) {
  const sentinels = detectToolResultSentinels(toolCall.result)
  const config = sentinels.statusConfig ?? getToolBadgeStatusConfig(toolCall.status, { durationMs: toolCall.duration_ms })
  return <span className={cn('text-[var(--color-muted)]', config.textClass)}>{config.label}</span>
}
`)
  const first = runCodemod({ repoRoot: root, apply: true })
  assert.equal(first.totalEdits, 1)
  const afterFirst = readFileSync(resolve(root, 'src/components/ToolCallBadgeLike2.tsx'), 'utf8')

  const second = runCodemod({ repoRoot: root, apply: true })
  assert.equal(second.totalEdits, 0, 'idempotent: nothing left to remove')
  const afterSecond = readFileSync(resolve(root, 'src/components/ToolCallBadgeLike2.tsx'), 'utf8')
  assert.equal(afterSecond, afterFirst, 'file is byte-identical after the second apply')
})

function editedText(text, edits) {
  const sorted = [...edits].sort((a, b) => b.start - a.start)
  let result = text
  for (const e of sorted) result = result.slice(0, e.start) + e.replacement + result.slice(e.end)
  return result
}
