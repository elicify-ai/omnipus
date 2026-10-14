// Test-only preservation instrument: compares assertions and case declarations
// to the dispatched UI head, not to runtime output. No test/config is rewritten.
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import ts from 'typescript'

const base = 'ca09d06f9d34bbcc63f158dc63d06973b9bb62f3'
const files = [
  'src/components/layout/sidebar/WorkspaceAgentList.rooms.test.tsx',
  'src/components/chat/AddParticipant.test.tsx',
  'src/components/layout/AppShell.test.tsx',
  'src/components/layout/Sidebar.test.tsx',
  'src/components/layout/Sidebar.focus-trap.test.tsx',
  'src/components/layout/Sidebar.kb5.test.tsx',
  'src/components/layout/Sidebar.m5.test.tsx',
  'src/components/layout/Sidebar.panelGuard.test.tsx',
  'src/components/layout/Sidebar.panelGuard.matrix.test.tsx',
  'src/components/layout/Sidebar.shortcuts.editor.test.tsx',
  'src/store/chat.sessionModeUpdate.test.ts',
  'src/store/chat.recipient-send.test.ts',
  'src/lib/ws.test.ts',
  'src/components/chat/RoomTrafficEntry.test.tsx',
]

function snapshot(text, file) {
  const source = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true)
  assert.equal(source.parseDiagnostics.length, 0, `${file}: parse error`)
  const printer = ts.createPrinter({ removeComments: true })
  const print = (node) => printer.printNode(ts.EmitHint.Unspecified, node, source)
  const assertions = []
  const cases = []
  function visit(node) {
    if (ts.isCallExpression(node)) {
      const expression = print(node.expression)
      if (/^expect\(/.test(expression)) assertions.push(print(node))
      if (/^(?:it|test|describe)(?:\.|$)/.test(expression)) {
        cases.push([expression, node.arguments.filter((arg) => !ts.isArrowFunction(arg) && !ts.isFunctionExpression(arg)).map(print)])
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(source)
  return { assertions, cases }
}

// Positive instrument control: weakening an assertion or suppressing a case
// in an IN-MEMORY fixture must change the comparison. This is not a production
// mutation or a CHECK claim about any component.
const control = 'it("control", () => { expect(result).toEqual([1, 2]) })'
assert.notDeepEqual(snapshot(control, 'control.ts'), snapshot(control.replace('toEqual([1, 2])', 'toBeTruthy()'), 'control.ts'))
assert.notDeepEqual(snapshot(control, 'control.ts'), snapshot(control.replace('it(', 'it.skip('), 'control.ts'))
console.log('instrument control: assertion weakening and case suppression both detected')

// Accepted follow-ups add only these five stronger visibility assertions.
// Require each addition exactly once, then compare EVERY original assertion
// and case declaration in order. No original oracle is ignored or replaced.
const visibleMountAdditions = new Map([
  ['src/components/layout/sidebar/WorkspaceAgentList.rooms.test.tsx', [
    'expect(group).toBeVisible()', 'expect(other).toBeVisible()',
  ]],
  ['src/components/layout/AppShell.test.tsx', [
    'expect(connectionBanner).toBeVisible()', 'expect(approvalBanner).toBeVisible()',
  ]],
  ['src/components/chat/RoomTrafficEntry.test.tsx', ['expect(toggle).toBeVisible()']],
])

for (const file of files) {
  const original = snapshot(execFileSync('git', ['show', `${base}:${file}`], { encoding: 'utf8' }), file)
  const current = snapshot(readFileSync(file, 'utf8'), file)
  const additions = visibleMountAdditions.get(file) ?? []
  for (const expression of additions) {
    assert.equal(current.assertions.filter((item) => item === expression).length, 1, `${resolve(file)}: required visibility assertion missing or duplicated`)
    current.assertions.splice(current.assertions.indexOf(expression), 1)
  }
  assert.deepEqual(current, original, `${resolve(file)}: original assertion or case declaration changed`)
  console.log(`${resolve(file)}: original assertions=${current.assertions.length}; added visible-mount assertions=${additions.length}; case declarations=${current.cases.length}; original oracles unchanged`)
}

// Original (a)–(f) are a frozen prefix of the extended acceptance file.
// Compare to the prior pack, not the production head where the file was absent.
const acceptanceFile = 'src/components/layout/AppShell.stage1.test.tsx'
const priorAcceptance = snapshot(execFileSync('git', ['show', `8eafc5ac4a71735748a18ce29dae3cd5608d4ab3:${acceptanceFile}`], { encoding: 'utf8' }), acceptanceFile)
const extendedAcceptance = snapshot(readFileSync(acceptanceFile, 'utf8'), acceptanceFile)
assert.deepEqual(extendedAcceptance.assertions.slice(0, priorAcceptance.assertions.length), priorAcceptance.assertions)
assert.deepEqual(extendedAcceptance.cases.slice(0, priorAcceptance.cases.length), priorAcceptance.cases)
console.log('original nine real-shell acceptance oracles: unchanged in extended pack')

// The sole allowed expected-fixture-set addition is the independently specified
// K45 client discriminator. The exact-equality matcher above stays identical.
const oldWs = execFileSync('git', ['show', `${base}:src/lib/ws.test.ts`], { encoding: 'utf8' })
const newWs = readFileSync('src/lib/ws.test.ts', 'utf8')
function frameSet(text) {
  const match = /const expectedTypes = new Set\(\[([\s\S]*?)\]\)/.exec(text)
  assert.ok(match, 'exact client-frame set must remain present')
  return [...match[1].replace(/\/\/[^\n]*/g, '').matchAll(/'([^']+)'/g)].map((entry) => entry[1]).sort()
}
assert.deepEqual(frameSet(newWs), [...frameSet(oldWs), 'attach_room'].sort())
console.log('client-frame expected set: exactly old set plus contract K45 attach_room')
