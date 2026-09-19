import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { planFileEdits, runCodemod, verifyTrustedFacts, computeStoreBooleanFields, computeGeneratedWireBooleanFields } from './codemod-boolean-class-guard.mjs'
import { applyEdits } from './codemod-lib.mjs'

const fixtureRoots = []
after(() => {
  for (const root of fixtureRoots) rmSync(root, { recursive: true, force: true })
})

function fixtureRepo() {
  const root = mkdtempSync(resolve('dist/design-system-baseline/codemod-boolguard-'))
  fixtureRoots.push(root)
  return root
}

function write(root, relPath, content) {
  const abs = resolve(root, relPath)
  mkdirSync(dirname(abs), { recursive: true })
  writeFileSync(abs, content, 'utf8')
  return abs
}

function editedText(text, edits) {
  return applyEdits(text, edits.map((e) => ({ start: e.start, end: e.end, replacement: e.replacement })))
}

const CN_UTIL = `import { clsx } from 'clsx'
export function cn(...inputs) { return clsx(inputs) }
`

// Minimal stand-ins for the third-party/generated facts the codemod
// re-verifies on every run — same relative paths the real table cites, so a
// fixture repo exercises the exact re-derivation path, not a shortcut.
function writeDndKitStubs(root) {
  write(root, 'node_modules/@dnd-kit/core/dist/hooks/useDraggable.d.ts', 'export declare function useDraggable(args: any): {\n  isDragging: boolean;\n};\n')
  write(root, 'node_modules/@dnd-kit/core/dist/hooks/useDroppable.d.ts', 'export declare function useDroppable(args: any): {\n  isOver: boolean;\n};\n')
}

function writeXyflowStub(root) {
  write(root, 'node_modules/@xyflow/system/dist/esm/types/nodes.d.ts', 'export type NodeBase = {\n  selected?: boolean;\n};\n')
}

function writeChatStoreTypes(root) {
  write(root, 'src/store/chat/types.ts', `export interface ChatState {
  isStreaming: boolean
  sessionTokens: number
}
`)
}

function writeGeneratedSchemas(root) {
  write(root, 'src/lib/api/generated/schemas.ts', `type LibraryEntry = {
  name: string;
  is_hidden: boolean;
  size: number;
};
`)
  write(root, 'src/lib/api.ts', `export type { LibraryEntry } from './api/generated/schemas'\n`)
}

function baseFixture(root) {
  write(root, 'src/lib/utils.ts', CN_UTIL)
  writeDndKitStubs(root)
  writeXyflowStub(root)
  writeChatStoreTypes(root)
  writeGeneratedSchemas(root)
}

// ── Case 1: MATCH — each boolean-source tier closes the finding ────────────

test('MATCH: dnd-kit useDraggable().isDragging (BoardView shape) rewrites to a ternary', () => {
  const root = fixtureRepo()
  baseFixture(root)
  const before = `import { useDraggable } from '@dnd-kit/core'
import { cn } from '@/lib/utils'

export function Card({ id }) {
  const { setNodeRef, isDragging } = useDraggable({ id })
  return <div ref={setNodeRef} className={cn(isDragging && 'opacity-40')} />
}
`
  const file = write(root, 'src/components/Card.tsx', before)
  const facts = allFacts(root)
  const { edits, refusals, text } = planFileEdits(root, file, facts)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  const after = editedText(text, edits)
  assert.match(after, /cn\(isDragging \? 'opacity-40' : undefined\)/)
})

test('MATCH: Zustand store selector whose field is declared boolean (TokenCounter shape)', () => {
  const root = fixtureRepo()
  baseFixture(root)
  const before = `import { useChatStore } from '@/store/chat'
import { cn } from '@/lib/utils'

export function TokenCounter() {
  const isStreaming = useChatStore((s) => s.isStreaming)
  return <span className={cn('font-mono', isStreaming && 'text-[var(--color-secondary)]')} />
}
`
  const file = write(root, 'src/components/TokenCounter.tsx', before)
  const facts = allFacts(root)
  const { edits, refusals, text } = planFileEdits(root, file, facts)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  const after = editedText(text, edits)
  assert.match(after, /isStreaming \? 'text-\[var\(--color-secondary\)\]' : undefined/)
})

test('MATCH: generated wire-type boolean field access via a named local props interface (real LibraryEntryRow shape)', () => {
  const root = fixtureRepo()
  baseFixture(root)
  const before = `import type { LibraryEntry } from '@/lib/api'
import { cn } from '@/lib/utils'

interface RowProps {
  entry: LibraryEntry
}

export function Row({ entry }: RowProps) {
  return <div className={cn('flex', entry.is_hidden && 'opacity-60')} />
}
`
  const file = write(root, 'src/components/Row.tsx', before)
  const facts = allFacts(root)
  const { edits, refusals } = planFileEdits(root, file, facts)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
})

test('MATCH: useState(false) boolean state (LibraryPdfPreview `dirty` shape)', () => {
  const root = fixtureRepo()
  baseFixture(root)
  const before = `import { useState } from 'react'
import { cn } from '@/lib/utils'

export function Editor() {
  const [dirty, setDirty] = useState(false)
  return <button className={cn('icon', dirty && 'text-[var(--color-accent)]')} />
}
`
  const file = write(root, 'src/components/Editor.tsx', before)
  const facts = allFacts(root)
  const { edits, refusals } = planFileEdits(root, file, facts)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
})

test('MATCH: application-local hook return field re-derived from its own source file (LibraryTextPreview `isDirty` shape)', () => {
  const root = fixtureRepo()
  baseFixture(root)
  write(root, 'src/components/library/preview/useLibraryFileEditor.ts', `export interface UseLibraryFileEditorResult {
  draft: string
  isDirty: boolean
  status: string
}
export function useLibraryFileEditor() {
  const draft = ''
  const isDirty = draft !== ''
  return { draft, isDirty, status: 'idle' }
}
`)
  const before = `import { useLibraryFileEditor } from './useLibraryFileEditor'
import { cn } from '@/lib/utils'

export function Preview() {
  const { isDirty, status } = useLibraryFileEditor()
  return <button className={cn('icon', isDirty && status !== 'saving' && 'text-[var(--color-accent)]')} />
}
`
  const file = write(root, 'src/components/library/preview/LibraryTextPreview.tsx', before)
  const facts = allFacts(root)
  const { edits, refusals } = planFileEdits(root, file, facts)
  assert.equal(refusals.length, 0, JSON.stringify(refusals))
  assert.equal(edits.length, 1)
})

test('MATCH: Array.prototype.includes() call is Tier-0 provable regardless of receiver type (EdgeModeEditor `on` shape)', () => {
  const root = fixtureRepo()
  baseFixture(root)
  const before = `import { cn } from '@/lib/utils'

export function Chip({ modes, mode }) {
  const on = modes.includes(mode)
  const isLastOn = on && modes.length === 1
  return <button className={cn('rounded', isLastOn && 'cursor-not-allowed')} />
}
`
  const file = write(root, 'src/components/Chip.tsx', before)
  const facts = allFacts(root)
  const { edits, refusals } = planFileEdits(root, file, facts)
  assert.equal(refusals.length, 0, JSON.stringify(refusals))
  assert.equal(edits.length, 1)
})

test('MATCH: `!x` unary is always boolean, independent of what x resolves to (ChatScreen `!inputEnabled || isStreaming` shape)', () => {
  const root = fixtureRepo()
  baseFixture(root)
  const before = `import { useChatStore } from '@/store/chat'
import { cn } from '@/lib/utils'

export function Composer({ agentRemoved }) {
  const isStreaming = useChatStore((s) => s.isStreaming)
  const inputEnabled = !agentRemoved
  return <textarea className={cn('block', (!inputEnabled || isStreaming) && 'opacity-60 cursor-not-allowed')} />
}
`
  const file = write(root, 'src/components/Composer.tsx', before)
  const facts = allFacts(root)
  const { edits, refusals, text } = planFileEdits(root, file, facts)
  assert.equal(refusals.length, 0, JSON.stringify(refusals))
  assert.equal(edits.length, 1)
  const after = editedText(text, edits)
  assert.match(after, /\(!inputEnabled \|\| isStreaming\) \? 'opacity-60 cursor-not-allowed' : undefined/)
})

test('MATCH: NodeProps<T>.selected destructured from a typed function parameter (TaskNode shape)', () => {
  const root = fixtureRepo()
  baseFixture(root)
  const before = `import type { NodeProps } from '@xyflow/react'
import { cn } from '@/lib/utils'

function TaskNode({ data, selected }: NodeProps<any>) {
  return <div className={cn('node', selected && 'opacity-100')} />
}
`
  const file = write(root, 'src/components/TaskNode.tsx', before)
  const facts = allFacts(root)
  const { edits, refusals } = planFileEdits(root, file, facts)
  assert.equal(refusals.length, 0, JSON.stringify(refusals))
  assert.equal(edits.length, 1)
})

test('MATCH: bare className (no cn/clsx) with a Tier-0-provable guard rewrites — proven invisible via strict-boolean guarantee', () => {
  const root = fixtureRepo()
  baseFixture(root)
  const before = `export function Flag({ status }) {
  return <span className={status === 'error' && 'text-[var(--color-error)]'} />
}
`
  const file = write(root, 'src/components/Flag.tsx', before)
  const facts = allFacts(root)
  const { edits, refusals, text } = planFileEdits(root, file, facts)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  const after = editedText(text, edits)
  assert.match(after, /className=\{status === 'error' \? 'text-\[var\(--color-error\)\]' : undefined\}/)
})

// ── Case 2: NO-MATCH — not a class-list sink at all ─────────────────────────

test('NO-MATCH: a `&&` guard feeding a style value (not class list) is left untouched — never touches style sinks', () => {
  const root = fixtureRepo()
  baseFixture(root)
  const before = `export function Handle({ connecting }) {
  const style = { pointerEvents: connecting ? 'all' : 'none' }
  return <div style={style} data-flag={connecting && 'on'} />
}
`
  const file = write(root, 'src/components/Handle.tsx', before)
  const facts = allFacts(root)
  const { edits, refusals } = planFileEdits(root, file, facts)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0, 'data-flag is not cn/clsx/className/class — never even considered, not refused')
})

test('NO-MATCH: `&&` guard outside any JSX/class-builder context is left untouched', () => {
  const root = fixtureRepo()
  baseFixture(root)
  const before = `export function label(active) {
  const x = active && 'ACTIVE'
  return x
}
`
  const file = write(root, 'src/components/label.ts', before)
  const facts = allFacts(root)
  const { edits, refusals } = planFileEdits(root, file, facts)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0)
})

// ── Case 3: AMBIGUOUS — refused, not guessed ────────────────────────────────

test('AMBIGUOUS: a compound guard with one unprovable operand is refused whole (BoardView `isOver && canAccept` shape)', () => {
  const root = fixtureRepo()
  baseFixture(root)
  const before = `import { useDroppable } from '@dnd-kit/core'
import { cn } from '@/lib/utils'

export function Column({ activeTask, config }) {
  const { setNodeRef, isOver } = useDroppable({ id: config.status })
  const canAccept = activeTask ? canDropTransition(activeTask.status, config.status).ok : true
  return <div ref={setNodeRef} className={cn(isOver && canAccept && 'bg-[var(--color-accent)]/5')} />
}
`
  const file = write(root, 'src/components/Column.tsx', before)
  const facts = allFacts(root)
  const { edits, refusals, text } = planFileEdits(root, file, facts)
  assert.equal(edits.length, 0, 'must not edit — canAccept is not provably boolean (a ternary over an unproven `.ok` property access)')
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /canAccept/)
  assert.equal(readFileSync(file, 'utf8'), text, 'file on disk is untouched')
})

test('AMBIGUOUS: a hook-shaped destructure from a LOCAL function of the same name is refused (never trust by name alone)', () => {
  const root = fixtureRepo()
  baseFixture(root)
  const before = `import { cn } from '@/lib/utils'

// Not the real dnd-kit hook — a same-named local helper.
function useDraggable() {
  return { isDragging: Math.random() > 2 }
}

export function Card() {
  const { isDragging } = useDraggable()
  return <div className={cn(isDragging && 'opacity-40')} />
}
`
  const file = write(root, 'src/components/FakeHook.tsx', before)
  const facts = allFacts(root)
  const { edits, refusals } = planFileEdits(root, file, facts)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /isn't imported from @dnd-kit\/core/)
})

test('AMBIGUOUS: multiple candidate declarations for the same guard name in scope are refused, not guessed', () => {
  const root = fixtureRepo()
  baseFixture(root)
  const before = `import { cn } from '@/lib/utils'

export function Row({ flag }) {
  if (flag) {
    const dirty = true
    void dirty
  }
  const dirty = flag === 'x'
  return <div className={cn(dirty && 'opacity-60')} />
}
`
  const file = write(root, 'src/components/MultiDecl.tsx', before)
  const facts = allFacts(root)
  const { edits, refusals } = planFileEdits(root, file, facts)
  // The resolver deliberately does not model block scoping (see
  // resolveBinding's file-header note): it finds BOTH the inner, shadowed
  // `const dirty = true` and the outer `const dirty = flag === 'x'` as
  // candidates within the same function-level search and refuses rather than
  // guess which one is really in scope at the className site — a real JS
  // scope analysis would resolve this safely, but "refuse instead of guess"
  // is the correct conservative fallback for a codemod that doesn't have one.
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /multiple candidate declarations/)
})

// ── Case 4: idempotency ──────────────────────────────────────────────────

test('IDEMPOTENT: a second --apply over an already-fixed tree makes zero further changes', () => {
  const root = fixtureRepo()
  baseFixture(root)
  write(root, 'src/components/Card.tsx', `import { useDraggable } from '@dnd-kit/core'
import { cn } from '@/lib/utils'

export function Card({ id }) {
  const { setNodeRef, isDragging } = useDraggable({ id })
  return <div ref={setNodeRef} className={cn(isDragging && 'opacity-40')} />
}
`)
  const first = runCodemod({ repoRoot: root, apply: true })
  assert.equal(first.totalEdits, 1)
  const second = runCodemod({ repoRoot: root, apply: true })
  assert.equal(second.totalEdits, 0, 'ternary shape no longer matches `&&`-with-literal, so a second pass is a no-op')
})

// ── Case 5: trusted-fact re-verification (never a blind hardcode) ─────────

test('DRIFT GUARD: a trusted hook fact whose cited .d.ts no longer contains the expected snippet is excluded, not silently trusted', () => {
  const root = fixtureRepo()
  baseFixture(root)
  // Simulate a dnd-kit upgrade that renamed the field.
  write(root, 'node_modules/@dnd-kit/core/dist/hooks/useDraggable.d.ts', 'export declare function useDraggable(args: any): {\n  dragging: boolean;\n};\n')
  const { trustedHookReturns, broken } = verifyTrustedFacts(root)
  assert.equal(trustedHookReturns.has('useDraggable.isDragging'), false)
  assert.ok(broken.some((b) => b.key === 'useDraggable.isDragging'))

  const before = `import { useDraggable } from '@dnd-kit/core'
import { cn } from '@/lib/utils'

export function Card({ id }) {
  const { setNodeRef, isDragging } = useDraggable({ id })
  return <div ref={setNodeRef} className={cn(isDragging && 'opacity-40')} />
}
`
  const file = write(root, 'src/components/Card.tsx', before)
  const facts = allFacts(root)
  const { edits, refusals } = planFileEdits(root, file, facts)
  assert.equal(edits.length, 0, 'fact no longer verifies — refuses instead of trusting a stale table entry')
  assert.equal(refusals.length, 1)
})

// ── helper: assemble the same `facts` shape runCodemod builds ─────────────
function allFacts(root) {
  const { trustedHookReturns, trustedGenericParamProps } = verifyTrustedFacts(root)
  return {
    trustedHookReturns,
    trustedGenericParamProps,
    storeBooleanFields: new Map([['useChatStore', computeStoreBooleanFields(root, 'src/store/chat/types.ts')]]),
    localHookBooleanFields: new Map([['useLibraryFileEditor', computeStoreBooleanFields(root, 'src/components/library/preview/useLibraryFileEditor.ts')]]),
    generatedWireBooleanFields: computeGeneratedWireBooleanFields(root),
  }
}
