// Design-system lock scanner tests — controls.mjs (raw controls and low-level imports).
//
// Specification sources (expected values derive from these, never from the
// implementation under test):
//   - design-system/enforcement/contract.json  — scannerApi, finding shape,
//     canonical syntax ("parsed declaration normalization, not whole file"),
//     failClosed (parse failures are explicit findings, never empty success).
//   - docs/internal/design/design-system-definition.md E1 and D5 — the banned
//     syntax list and the one-legal-component-path table.
//   - design-system/catalog.json — registered public boundaries: button.tsx
//     exposes Button/buttonVariants publicly; alert-dialog.tsx and
//     model-selector.tsx expose nothing publicly.
//
// Fixtures are inline sources. Importer paths are chosen per fixture because
// relative ui imports resolve against the importing file's location.

import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

import { extensions, scan } from '../../scripts/design-system-locks/controls.mjs'

const DEFAULT_PATH = 'src/features/fixture.tsx'
const CATALOG = JSON.parse(readFileSync(new URL('../../design-system/catalog.json', import.meta.url), 'utf8'))

function scanSource(source, path = DEFAULT_PATH) {
  return scan({ path, source, policy: {}, catalog: CATALOG })
}

function findingsFor(source, ruleId, path) {
  const findings = scanSource(source, path)
  return ruleId === undefined ? findings : findings.filter((finding) => finding.ruleId === ruleId)
}

function ruleIds(source, path) {
  return scanSource(source, path).map((finding) => finding.ruleId)
}

function syntaxes(source, ruleId, path) {
  return findingsFor(source, ruleId, path).map((finding) => finding.syntax)
}

// ---------------------------------------------------------------------------
// Scanner API surface
// ---------------------------------------------------------------------------

test('extensions declares the handled script surfaces with leading dots', () => {
  assert.deepEqual(extensions, ['.js', '.jsx', '.ts', '.tsx'])
})

test('scan validates its inputs loudly instead of returning empty success', () => {
  assert.throws(() => scan({ source: 'const x = 1' }), /path must be a non-empty string/)
  assert.throws(() => scan({ path: DEFAULT_PATH }), /source must be a non-empty string/)
})

test('scan tolerates an absent policy without changing detection', () => {
  const findings = scan({ path: DEFAULT_PATH, source: 'export const X = () => <button>go</button>' })
  assert.equal(findings.length, 1)
  assert.equal(findings[0].ruleId, 'controls/raw-button')
})

test('findings carry the contract finding shape and echo the given path', () => {
  const path = 'src/features/example/Fixture.tsx'
  const findings = scanSource('export const X = () => <button>go</button>', path)
  assert.equal(findings.length, 1)
  const [finding] = findings
  assert.equal(finding.path, path)
  assert.equal(typeof finding.ruleId, 'string')
  assert.ok(finding.ruleId.length > 0)
  assert.equal(typeof finding.message, 'string')
  assert.ok(finding.message.length > 0)
  assert.equal(typeof finding.syntax, 'string')
  assert.ok(finding.syntax.length > 0)
  assert.equal(finding.line, 1)
  assert.ok(Number.isInteger(finding.line) && finding.line > 0)
})

// ---------------------------------------------------------------------------
// Raw button
// ---------------------------------------------------------------------------

test('flags a raw JSX button', () => {
  assert.deepEqual(syntaxes('export const X = () => <button>go</button>'), ['<button>'])
})

test('allows the Button component', () => {
  assert.deepEqual(ruleIds("import { Button } from '@/components/ui/button'\nexport const X = () => <Button>go</Button>"), [])
})

test('allows member-expression JSX tags such as Radix composition', () => {
  assert.deepEqual(ruleIds('export const X = () => <SwitchPrimitives.Root data-x="" />'), [])
})

test('flags React.createElement("button")', () => {
  const source = "import * as React from 'react'\nexport const X = () => React.createElement('button', null, 'go')"
  assert.deepEqual(syntaxes(source), ['createElement("button")'])
})

test('flags document.createElement("button")', () => {
  assert.deepEqual(syntaxes("export const X = () => document.createElement('button')"), ['document.createElement("button")'])
})

test('flags automatic-runtime _jsx("button")', () => {
  const source = "import { _jsx } from 'react/jsx-runtime'\nexport const X = () => _jsx('button', { children: 'go' })"
  assert.deepEqual(syntaxes(source), ['createElement("button")'])
})

test('allows createElement with a component identifier', () => {
  const source = "import * as React from 'react'\nimport { Button } from '@/components/ui/button'\nexport const X = () => React.createElement(Button, null, 'go')"
  assert.deepEqual(ruleIds(source), [])
})

test('ignores button mentioned only in comments and strings', () => {
  const source = [
    '// a raw <button> in a comment is trivia, not syntax',
    "const template = '<button>legacy</button>'",
    'export const ok = 1',
  ].join('\n')
  assert.deepEqual(ruleIds(source), [])
})

test('button syntax is position- and trivia-free across attribute churn', () => {
  const oneLine = 'export const X = () => <button className="x" disabled>go</button>'
  const multiLine = 'export const X = () => (\n  <button\n    disabled\n    className="y"\n  >go</button>\n)'
  assert.equal(syntaxes(oneLine)[0], '<button>')
  assert.equal(syntaxes(multiLine)[0], '<button>')
})

// ---------------------------------------------------------------------------
// Raw dialog
// ---------------------------------------------------------------------------

test('flags a raw JSX dialog element', () => {
  assert.deepEqual(syntaxes('export const X = () => <dialog open>hi</dialog>'), ['<dialog>'])
})

test('flags createElement("dialog")', () => {
  const source = "import * as React from 'react'\nexport const X = () => React.createElement('dialog', { open: true })"
  assert.deepEqual(syntaxes(source), ['createElement("dialog")'])
})

test('allows the Dialog component', () => {
  assert.deepEqual(ruleIds("import { Dialog } from '@/components/ui/dialog'\nexport const X = () => <Dialog>hi</Dialog>"), [])
})

// ---------------------------------------------------------------------------
// Global confirm
// ---------------------------------------------------------------------------

test('flags window.confirm(...)', () => {
  assert.deepEqual(syntaxes('export const ask = () => window.confirm(\'Delete?\')'), ['window.confirm(...)'])
})

test('flags a bare global confirm(...)', () => {
  assert.deepEqual(syntaxes('export const ask = () => confirm(\'Delete?\')'), ['confirm(...)'])
})

test('flags globalThis.confirm(...) with the window-canonical syntax', () => {
  assert.deepEqual(syntaxes('export const ask = () => globalThis.confirm(\'Delete?\')'), ['window.confirm(...)'])
})

test('flags window["confirm"](...) element access', () => {
  assert.deepEqual(syntaxes('export const ask = () => window[\'confirm\'](\'Delete?\')'), ['window.confirm(...)'])
})

test('flags a call through a window.confirm alias', () => {
  const source = "const c = window.confirm\nexport const ask = () => c('Delete?')"
  assert.deepEqual(syntaxes(source), ['confirm(...)'])
})

test('flags a call through a destructured confirm', () => {
  const source = "const { confirm: c } = window\nexport const ask = () => c('Delete?')"
  assert.deepEqual(syntaxes(source), ['confirm(...)'])
})

test('flags a call through a shorthand destructured confirm', () => {
  const source = "const { confirm } = window\nexport const ask = () => confirm('Delete?')"
  assert.deepEqual(syntaxes(source), ['confirm(...)'])
})

test('flags a call through a transitive alias', () => {
  const source = "const a = window.confirm\nconst b = a\nexport const ask = () => b('Delete?')"
  assert.deepEqual(syntaxes(source), ['confirm(...)'])
})

test('allows a parameter named confirm that shadows the global', () => {
  const source = 'export const ask = (confirm: () => boolean) => confirm()'
  assert.deepEqual(ruleIds(source), [])
})

test('allows an imported confirm helper that is not the browser global', () => {
  const source = "import { confirm } from './safe-confirm'\nexport const ask = () => confirm('Delete?')"
  assert.deepEqual(ruleIds(source), [])
})

test('allows an alias of a locally imported confirm', () => {
  const source = "import { confirm } from './safe-confirm'\nconst c = confirm\nexport const ask = () => c('Delete?')"
  assert.deepEqual(ruleIds(source), [])
})

test('ignores confirm mentioned only in comments and strings', () => {
  const source = [
    '// replaces the legacy window.confirm pair',
    "const copy = \"don't call window.confirm here\"",
    'export const ok = 1',
  ].join('\n')
  assert.deepEqual(ruleIds(source), [])
})

// ---------------------------------------------------------------------------
// Checkbox-as-switch
// ---------------------------------------------------------------------------

test('flags a Checkbox with role="switch"', () => {
  const source = "import { Checkbox } from '@/components/ui/checkbox'\nexport const X = () => <Checkbox role=\"switch\" />"
  assert.deepEqual(syntaxes(source), ['<Checkbox role="switch">'])
})

test('flags a raw checkbox input with role="switch"', () => {
  assert.deepEqual(syntaxes('export const X = () => <input type="checkbox" role="switch" />'), ['<input type="checkbox" role="switch">'])
})

test('allows a semantic Checkbox without a switch role', () => {
  const source = "import { Checkbox } from '@/components/ui/checkbox'\nexport const X = () => <Checkbox checked onCheckedChange={() => {}} />"
  assert.deepEqual(ruleIds(source), [])
})

test('allows a plain checkbox input', () => {
  assert.deepEqual(ruleIds('export const X = () => <input type="checkbox" checked readOnly />'), [])
})

test('allows the Switch primitive', () => {
  const source = "import { Switch } from '@/components/ui/switch'\nexport const X = () => <Switch checked onCheckedChange={() => {}} />"
  assert.deepEqual(ruleIds(source), [])
})

test('flags role={"switch"} JSX expression literals', () => {
  const source = "import { Checkbox } from '@/components/ui/checkbox'\nexport const X = () => <Checkbox role={'switch'} />"
  assert.deepEqual(syntaxes(source), ['<Checkbox role="switch">'])
})

test('flags a renamed Checkbox import masquerading as a switch', () => {
  const source = "import { Checkbox as CB } from '@/components/ui/checkbox'\nexport const X = () => <CB role=\"switch\" />"
  assert.deepEqual(syntaxes(source), ['<Checkbox role="switch">'])
})

test('reports a Checkbox with a dynamic role as unresolvable, not silently allowed', () => {
  const source = "import { Checkbox } from '@/components/ui/checkbox'\nconst role = 'switch'\nexport const X = () => <Checkbox role={role} />"
  const findings = findingsFor(source, 'controls/checkbox-as-switch')
  assert.deepEqual(findings.map((finding) => finding.syntax), ['<Checkbox role={expr}>'])
  assert.match(findings[0].message, /dynamic role/)
})

test('checkbox input syntax is stable across attribute order churn', () => {
  const typeFirst = 'export const X = () => <input type="checkbox" role="switch" />'
  const roleFirst = 'export const X = () => <input role="switch" type="checkbox" />'
  assert.equal(syntaxes(typeFirst)[0], '<input type="checkbox" role="switch">')
  assert.equal(syntaxes(roleFirst)[0], '<input type="checkbox" role="switch">')
})

// ---------------------------------------------------------------------------
// Radix imports
// ---------------------------------------------------------------------------

test('flags a Radix primitive import', () => {
  assert.deepEqual(syntaxes("import { Dialog } from '@radix-ui/react-dialog'"), ['import { Dialog } from "@radix-ui/react-dialog"'])
})

test('flags a renamed Radix import', () => {
  assert.deepEqual(syntaxes("import { Root as R } from '@radix-ui/react-dialog'"), ['import { Root as R } from "@radix-ui/react-dialog"'])
})

test('flags a namespace Radix import', () => {
  assert.deepEqual(syntaxes("import * as RD from '@radix-ui/react-dialog'"), ['import * as RD from "@radix-ui/react-dialog"'])
})

test('flags a side-effect Radix import', () => {
  assert.deepEqual(syntaxes("import '@radix-ui/react-dialog'"), ['import "@radix-ui/react-dialog"'])
})

test('flags Radix re-exports', () => {
  assert.deepEqual(syntaxes("export { Dialog } from '@radix-ui/react-dialog'"), ['export { Dialog } from "@radix-ui/react-dialog"'])
  assert.deepEqual(syntaxes("export * from '@radix-ui/react-dialog'"), ['export * from "@radix-ui/react-dialog"'])
})

test('import syntax is stable across specifier formatting churn', () => {
  const oneLine = "import { Dialog } from '@radix-ui/react-dialog'"
  const multiLine = "import {\n  Dialog,\n} from '@radix-ui/react-dialog'"
  assert.equal(syntaxes(oneLine)[0], syntaxes(multiLine)[0])
})

// ---------------------------------------------------------------------------
// Low-level shadcn (ui kit) imports against the registered catalog
// ---------------------------------------------------------------------------

test('allows importing the public Button primitive', () => {
  assert.deepEqual(ruleIds("import { Button } from '@/components/ui/button'"), [])
})

test('allows importing other public exports such as buttonVariants', () => {
  assert.deepEqual(ruleIds("import { buttonVariants } from '@/components/ui/button'"), [])
})

test('flags importing internals of a source with no public exports', () => {
  assert.deepEqual(
    syntaxes("import { AlertDialog } from '@/components/ui/alert-dialog'"),
    ['import { AlertDialog } from "src/components/ui/alert-dialog"']
  )
})

test('flags importing sub-parts of a non-public primitive', () => {
  assert.deepEqual(
    syntaxes("import { AlertDialogAction } from '@/components/ui/alert-dialog'"),
    ['import { AlertDialogAction } from "src/components/ui/alert-dialog"']
  )
})

test('resolves relative ui imports to the catalog source for the fingerprint', () => {
  const source = "import { AlertDialog } from '../ui/alert-dialog'"
  const path = 'src/components/settings/Appearance.tsx'
  assert.deepEqual(
    syntaxes(source, 'controls/shadcn-low-level-import', path),
    ['import { AlertDialog } from "src/components/ui/alert-dialog"']
  )
})

test('flags namespace imports of ui kit sources as whole-module reach', () => {
  assert.deepEqual(
    syntaxes("import * as AlertDialogParts from '@/components/ui/alert-dialog'"),
    ['import * as AlertDialogParts from "src/components/ui/alert-dialog"']
  )
})

test('flags imports of ui modules that are not registered in the catalog', () => {
  assert.deepEqual(
    syntaxes("import { Mystery } from '@/components/ui/mystery-box'"),
    ['import { Mystery } from "src/components/ui/mystery-box"']
  )
})

test('flags a renamed import of a non-public export', () => {
  assert.deepEqual(
    syntaxes("import { AlertDialogAction as D } from '@/components/ui/alert-dialog'"),
    ['import { AlertDialogAction as D } from "src/components/ui/alert-dialog"']
  )
})

test('allows a renamed import of a public export', () => {
  assert.deepEqual(ruleIds("import { Button as B } from '@/components/ui/button'"), [])
})

test('leaves non-ui module imports alone', () => {
  assert.deepEqual(ruleIds("import { cn } from '@/lib/utils'\nimport { useThing } from '../hooks/thing'"), [])
})

// ---------------------------------------------------------------------------
// Fail-closed parsing
// ---------------------------------------------------------------------------

test('reports a parse failure as an explicit blocking finding', () => {
  const findings = scanSource('export const x = {')
  assert.equal(findings.length, 1)
  assert.equal(findings[0].ruleId, 'controls/parse-error')
  assert.ok(findings[0].syntax.length > 0)
  assert.match(findings[0].message, /failed to parse/i)
})

test('reports JSX in a .ts file as a parse failure, not as a raw-control finding', () => {
  const findings = scan({ path: 'src/features/fixture.ts', source: 'export const X = () => <button>go</button>', policy: {} })
  assert.equal(findings.length, 1)
  assert.equal(findings[0].ruleId, 'controls/parse-error')
})

// ---------------------------------------------------------------------------
// Review follow-up — design-system/enforcement/review-controls.json
//
// Fixtures below encode requirements controls-review-01 … controls-review-09.
// Expected values derive from the review document, contract.json and E1/D5 —
// never from the current implementation.
// ---------------------------------------------------------------------------

// controls-review-01: raw findings survive every path shape and fake policy.

test('review-01: raw findings persist under ui, story, and test path shapes', () => {
  const source = 'export const Fixture = () => <button>Run</button>'
  for (const path of [
    'src/components/ui/fixture.tsx',
    'src/components/ui/fixture.stories.tsx',
    'src/components/ui/fixture.test.tsx',
  ]) {
    assert.deepEqual(ruleIds(source, path), ['controls/raw-button'], `${path} must still report`)
  }
})

test('review-01: caller-supplied fake exclusion policies cannot suppress findings', () => {
  const source = "import { Dialog } from '@radix-ui/react-dialog'\nexport const Fixture = () => <button>Run</button>"
  const findings = scan({ path: DEFAULT_PATH, source, policy: { exclude: ['src/**'], allowedPaths: [DEFAULT_PATH], exceptions: [{ path: DEFAULT_PATH, ruleId: '*' }] } })
  assert.deepEqual(findings.map((finding) => finding.ruleId), ['controls/radix-import', 'controls/raw-button'])
})

test('review-01: a Radix import inside the ui directory still reports the low-level finding', () => {
  assert.deepEqual(
    ruleIds("import { Dialog } from '@radix-ui/react-dialog'", 'src/components/ui/fixture.tsx'),
    ['controls/radix-import'],
  )
})

// controls-review-02: occurrences are never deduplicated inside the scanner.

test('review-02: two raw buttons stay two occurrences with one canonical syntax and distinct lines', () => {
  const source = 'export const First = () => <button>First</button>\nexport const Second = () => <button>Second</button>'
  const findings = findingsFor(source, 'controls/raw-button')
  assert.equal(findings.length, 2)
  assert.deepEqual(findings.map((finding) => finding.syntax), ['<button>', '<button>'])
  assert.equal(findings[0].line, 1)
  assert.equal(findings[1].line, 2)
})

// controls-review-03: emitted React runtime forms, aliases, and shadowing.

test('review-03: flags jsx and jsxs calls imported from react/jsx-runtime', () => {
  const source = "import { jsx, jsxs } from 'react/jsx-runtime'\nexport const A = () => jsx('button', { children: 'x' })\nexport const B = () => jsxs('dialog', { children: [] })"
  assert.deepEqual(
    findingsFor(source).map(({ ruleId, syntax }) => ({ ruleId, syntax })),
    [
      { ruleId: 'controls/raw-button', syntax: 'createElement("button")' },
      { ruleId: 'controls/raw-dialog', syntax: 'createElement("dialog")' },
    ],
  )
})

test('review-03: flags jsxDEV imported from react/jsx-dev-runtime', () => {
  const source = "import { jsxDEV } from 'react/jsx-dev-runtime'\nexport const A = () => jsxDEV('button', { children: 'x' })"
  assert.deepEqual(syntaxes(source, 'controls/raw-button'), ['createElement("button")'])
})

test('review-03: flags renamed jsx runtime factory imports', () => {
  const source = "import { jsx as renderOne } from 'react/jsx-runtime'\nexport const A = () => renderOne('button', { children: 'x' })"
  assert.deepEqual(syntaxes(source, 'controls/raw-button'), ['createElement("button")'])
})

test('review-03: flags namespace jsx runtime calls', () => {
  const source = "import * as RTE from 'react/jsx-runtime'\nexport const A = () => RTE.jsx('button', { children: 'x' })"
  assert.deepEqual(syntaxes(source, 'controls/raw-button'), ['createElement("button")'])
})

test('review-03: flags a renamed createElement import from react', () => {
  const source = "import { createElement as h } from 'react'\nexport const A = () => h('button', null, 'go')"
  assert.deepEqual(syntaxes(source, 'controls/raw-button'), ['createElement("button")'])
})

test('review-03: flags destructured createElement from a React namespace import', () => {
  const source = "import * as React from 'react'\nconst { createElement } = React\nconst { createElement: ce } = React\nexport const A = () => createElement('button', null, 'go')\nexport const B = () => ce('dialog')"
  assert.deepEqual(
    findingsFor(source).map(({ ruleId, syntax }) => ({ ruleId, syntax })),
    [
      { ruleId: 'controls/raw-button', syntax: 'createElement("button")' },
      { ruleId: 'controls/raw-dialog', syntax: 'createElement("dialog")' },
    ],
  )
})

test('review-03: flags window.document.createElement for button and dialog', () => {
  const source = "export const mkButton = () => window.document.createElement('button')\nexport const mkDialog = () => window.document.createElement('dialog')"
  assert.deepEqual(
    findingsFor(source).map(({ ruleId, syntax }) => ({ ruleId, syntax })),
    [
      { ruleId: 'controls/raw-button', syntax: 'document.createElement("button")' },
      { ruleId: 'controls/raw-dialog', syntax: 'document.createElement("dialog")' },
    ],
  )
})

test('review-03: parameters named React and document shadow the factory globals', () => {
  const source = [
    'export function build(React: { createElement: (t: string) => void }, document: { createElement: (t: string) => void }) {',
    "  return [React.createElement('dialog'), document.createElement('button')]",
    '}',
  ].join('\n')
  assert.deepEqual(ruleIds(source), [])
})

test('review-03: imports named React and document from other modules are not the globals', () => {
  const source = [
    "import { React } from './fake-react'",
    "import { document } from './dom-mock'",
    "export const A = () => React.createElement('dialog')",
    "export const B = () => document.createElement('button')",
  ].join('\n')
  assert.deepEqual(ruleIds(source), [])
})

// controls-review-04: confirm detection follows lexical scope and alias flow.

test('review-04: allows a local confirm variable', () => {
  assert.deepEqual(ruleIds('const confirm = () => true\nexport const ask = () => confirm(\'x\')'), [])
})

test('review-04: allows a hoisted local confirm function used before its declaration', () => {
  const source = "export const ask = () => confirm('x')\nfunction confirm() { return true }"
  assert.deepEqual(ruleIds(source), [])
})

test('review-04: allows imports named window and globalThis to shadow the browser object', () => {
  const source = "import { window } from './window-mock'\nexport const ask = () => window.confirm('x')"
  assert.deepEqual(ruleIds(source), [])
})

test('review-04: allows parameters named window and globalThis to shadow the browser object', () => {
  const source = "export function ask(window: { confirm: (q: string) => boolean }) { return window.confirm('q') }"
  assert.deepEqual(ruleIds(source), [])
})

test('review-04: flags destructuring confirm from globalThis', () => {
  const source = "const { confirm: c } = globalThis\nexport const ask = () => c('Delete?')"
  assert.deepEqual(syntaxes(source, 'controls/global-confirm'), ['confirm(...)'])
})

test('review-04: flags an optional window.confirm?.(...) call', () => {
  assert.deepEqual(syntaxes("export const ask = () => window.confirm?.('Delete?')", 'controls/global-confirm'), ['window.confirm(...)'])
})

test('review-04: flags an element-access alias invoked later', () => {
  const source = "const c = window['confirm']\nexport const ask = () => c('Delete?')"
  assert.deepEqual(syntaxes(source, 'controls/global-confirm'), ['confirm(...)'])
})

test('review-04: nested shadowing changes detection only inside the shadowed scope', () => {
  const source = [
    "export const outer = () => confirm('yes')",
    'export const inner = (confirm: (q: string) => boolean) => confirm(\'no\')',
  ].join('\n')
  const findings = findingsFor(source, 'controls/global-confirm')
  assert.deepEqual(findings.map((finding) => finding.syntax), ['confirm(...)'])
  assert.equal(findings[0].line, 1)
})

// controls-review-05: checkbox-as-switch fails closed for unresolvable attributes.

test('review-05: flags an expression-literal type={\'checkbox\'} with a switch role', () => {
  assert.deepEqual(syntaxes("export const X = () => <input type={'checkbox'} role=\"switch\" />", 'controls/checkbox-as-switch'), ['<input type="checkbox" role="switch">'])
})

test('review-05: role matching is case-sensitive; role="Switch" is not the switch role', () => {
  const source = "import { Checkbox } from '@/components/ui/checkbox'\nexport const X = () => <Checkbox role=\"Switch\" />"
  assert.deepEqual(ruleIds(source), [])
})

test('review-05: input type matching is case-insensitive like the HTML attribute', () => {
  assert.deepEqual(syntaxes('export const X = () => <input type="CHECKBOX" role="switch" />', 'controls/checkbox-as-switch'), ['<input type="checkbox" role="switch">'])
})

test('review-05: a dynamic input type with a switch role is an explicit unresolved finding', () => {
  const source = "const type = 'checkbox'\nexport const X = () => <input type={type} role=\"switch\" />"
  const findings = findingsFor(source, 'controls/checkbox-as-switch')
  assert.deepEqual(findings.map((finding) => finding.syntax), ['<input type={expr} role="switch">'])
  assert.match(findings[0].message, /dynamic type/)
})

test('review-05: a dynamic role on a checkbox input is an explicit unresolved finding', () => {
  const source = "const role = 'switch'\nexport const X = () => <input type=\"checkbox\" role={role} />"
  const findings = findingsFor(source, 'controls/checkbox-as-switch')
  assert.deepEqual(findings.map((finding) => finding.syntax), ['<input type="checkbox" role={expr}>'])
  assert.match(findings[0].message, /dynamic role/)
})

test('review-05: an untyped input with a switch role is reported (decided policy)', () => {
  const findings = findingsFor('export const X = () => <input role="switch" />', 'controls/checkbox-as-switch')
  assert.deepEqual(findings.map((finding) => finding.syntax), ['<input role="switch">'])
  assert.match(findings[0].message, /no type attribute/)
})

test('review-05: a spread on an imported Checkbox is an explicit unresolved finding', () => {
  const source = "import { Checkbox } from '@/components/ui/checkbox'\nexport const X = (props: Record<string, unknown>) => <Checkbox {...props} />"
  const findings = findingsFor(source, 'controls/checkbox-as-switch')
  assert.deepEqual(findings.map((finding) => finding.syntax), ['<Checkbox {...props}>'])
  assert.match(findings[0].message, /spread/)
})

test('review-05: a spread on a Checkbox with an explicit switch role stays unresolved', () => {
  const source = "import { Checkbox } from '@/components/ui/checkbox'\nexport const X = (props: Record<string, unknown>) => <Checkbox {...props} role=\"switch\" />"
  assert.deepEqual(syntaxes(source, 'controls/checkbox-as-switch'), ['<Checkbox {...props}>'])
})

test('review-05: a spread on a checkbox input is an explicit unresolved finding', () => {
  const source = 'export const X = (props: Record<string, unknown>) => <input type="checkbox" {...props} />'
  const findings = findingsFor(source, 'controls/checkbox-as-switch')
  assert.deepEqual(findings.map((finding) => finding.syntax), ['<input {...props}>'])
  assert.match(findings[0].message, /spread/)
})

test('review-05: a spread on a switch-role input is an explicit unresolved finding', () => {
  const source = 'export const X = (props: Record<string, unknown>) => <input role="switch" {...props} />'
  assert.deepEqual(syntaxes(source, 'controls/checkbox-as-switch'), ['<input {...props}>'])
})

test('review-05: a namespace-imported Checkbox masquerading as a switch is detected', () => {
  const source = "import * as CB from '@/components/ui/checkbox'\nexport const X = () => <CB.Checkbox role=\"switch\" />"
  assert.deepEqual(syntaxes(source, 'controls/checkbox-as-switch'), ['<Checkbox role="switch">'])
})

// controls-review-06: every Radix dependency-introduction syntax is governed.

test('review-06: flags dynamic import() of a Radix package', () => {
  const source = "export const loadDialog = () => import('@radix-ui/react-dialog')"
  assert.deepEqual(syntaxes(source, 'controls/radix-import'), ['import("@radix-ui/react-dialog")'])
})

test('review-06: flags require() of a Radix package', () => {
  const source = "export const loadDialog = () => require('@radix-ui/react-dialog')"
  assert.deepEqual(syntaxes(source, 'controls/radix-import'), ['require("@radix-ui/react-dialog")'])
})

test('review-06: flags TypeScript import-equals of a Radix package', () => {
  const source = "import Dialog = require('@radix-ui/react-dialog')"
  assert.deepEqual(syntaxes(source, 'controls/radix-import'), ['import Dialog = require("@radix-ui/react-dialog")'])
})

test('review-06: flags type-only Radix imports', () => {
  assert.deepEqual(syntaxes("import type { DialogProps } from '@radix-ui/react-dialog'", 'controls/radix-import'), ['import type { DialogProps } from "@radix-ui/react-dialog"'])
})

test('review-06: flags type-only Radix re-exports', () => {
  assert.deepEqual(syntaxes("export type { DialogProps } from '@radix-ui/react-dialog'", 'controls/radix-import'), ['export type { DialogProps } from "@radix-ui/react-dialog"'])
})

test('review-06: flags deep subpaths below a Radix package', () => {
  assert.deepEqual(syntaxes("import { x } from '@radix-ui/react-dialog/dist/utils'", 'controls/radix-import'), ['import { x } from "@radix-ui/react-dialog/dist/utils"'])
})

// controls-review-07: catalog enforcement covers namespaces, types, re-exports,
// normalization, and every import form — not just named value imports.

test('review-07: flags namespace imports of a partly public ui module', () => {
  assert.deepEqual(
    syntaxes("import * as ButtonModule from '@/components/ui/button'", 'controls/shadcn-low-level-import'),
    ['import * as ButtonModule from "src/components/ui/button"'],
  )
})

test('review-07: flags default imports of ui modules (no public default export exists)', () => {
  assert.deepEqual(
    syntaxes("import B from '@/components/ui/button'", 'controls/shadcn-low-level-import'),
    ['import B from "src/components/ui/button"'],
  )
})

test('review-07: allows side-effect imports of registered ui modules', () => {
  assert.deepEqual(ruleIds("import '@/components/ui/button'"), [])
})

test('review-07: flags side-effect imports of unregistered ui modules', () => {
  assert.deepEqual(
    syntaxes("import '@/components/ui/mystery-box'", 'controls/shadcn-low-level-import'),
    ['import "src/components/ui/mystery-box"'],
  )
})

test('review-07: allows re-exporting public names from ui sources', () => {
  assert.deepEqual(ruleIds("export { Button } from '@/components/ui/button'\nexport { buttonVariants as bv } from '@/components/ui/button'"), [])
})

test('review-07: flags re-exporting non-public names from ui sources', () => {
  assert.deepEqual(
    syntaxes("export { AlertDialog } from '@/components/ui/alert-dialog'", 'controls/shadcn-low-level-import'),
    ['export { AlertDialog } from "src/components/ui/alert-dialog"'],
  )
})

test('review-07: flags export-star from a ui source with non-public exports', () => {
  assert.deepEqual(
    syntaxes("export * from '@/components/ui/alert-dialog'", 'controls/shadcn-low-level-import'),
    ['export * from "src/components/ui/alert-dialog"'],
  )
})

test('review-07: allows export-star from a ui source whose exports are all public', () => {
  assert.deepEqual(ruleIds("export * from '@/components/ui/button'"), [])
})

test('review-07: allows import type of names listed in catalog publicTypes', () => {
  assert.deepEqual(ruleIds("import type { ButtonProps } from '@/components/ui/button'\nimport type { AvatarImageProps } from '@/components/ui/avatar'"), [])
})

test('review-07: flags import type of names outside publicTypes and publicExports', () => {
  assert.deepEqual(
    syntaxes("import type { AlertDialogHeader } from '@/components/ui/alert-dialog'", 'controls/shadcn-low-level-import'),
    ['import type { AlertDialogHeader } from "src/components/ui/alert-dialog"'],
  )
})

test('review-07: allows inline type specifiers of public types', () => {
  assert.deepEqual(ruleIds("import { Button, type ButtonProps } from '@/components/ui/button'"), [])
})

test('review-07: normalizes extension and index spellings onto the catalog source', () => {
  assert.deepEqual(ruleIds("import { Button } from '@/components/ui/button.tsx'"), [])
  assert.deepEqual(ruleIds("import { Button } from '@/components/ui/button/index'"), [])
})

test('review-07: allows importing from the curated @omnipus/ui entry point', () => {
  assert.deepEqual(ruleIds("import { Button } from '@omnipus/ui'\nexport const X = () => <Button>go</Button>"), [])
})

test('review-07: dynamic import and require of ui internals are whole-module reach', () => {
  assert.deepEqual(
    syntaxes("export const load = () => import('@/components/ui/alert-dialog')", 'controls/shadcn-low-level-import'),
    ['import("src/components/ui/alert-dialog")'],
  )
  assert.deepEqual(
    syntaxes("export const load = () => require('@/components/ui/button')", 'controls/shadcn-low-level-import'),
    ['require("src/components/ui/button")'],
  )
})

// controls-review-08: finding paths are repository-relative POSIX before
// fingerprinting; one documented behavior for absolute and backslash inputs.

test('review-08: backslash input paths normalize to the same POSIX finding path', () => {
  const source = 'export const X = () => <button>go</button>'
  const forward = scan({ path: 'src/features/fixture.tsx', source, policy: {} })
  const backslashed = scan({ path: 'src\\features\\fixture.tsx', source, policy: {} })
  assert.deepEqual(
    backslashed.map(({ ruleId, path, syntax }) => ({ ruleId, path, syntax })),
    forward.map(({ ruleId, path, syntax }) => ({ ruleId, path, syntax })),
  )
  assert.equal(backslashed[0].path, 'src/features/fixture.tsx')
})

test('review-08: rejects absolute POSIX paths loudly', () => {
  assert.throws(() => scan({ path: '/Users/example/src/fixture.tsx', source: 'export const x = 1', policy: {} }), /repository-relative/)
})

test('review-08: rejects Windows drive-letter paths loudly', () => {
  assert.throws(() => scan({ path: 'C:\\repo\\src\\fixture.tsx', source: 'export const x = 1', policy: {} }), /repository-relative/)
})

// controls-review-09: generic member-expression JSX is not an exemption.

test('review-09: a Radix-backed member tag fails through the import rule, not the JSX form', () => {
  const source = "import * as RD from '@radix-ui/react-switch'\nexport const X = () => <RD.Root data-x=\"\" />"
  assert.deepEqual(ruleIds(source), ['controls/radix-import'])
})

// ---------------------------------------------------------------------------
// Final limit review follow-up (2026-09-18) — cross-function confirm captures,
// var hoisting, fully dynamic switch attributes.
//
// Specification sources (expected values derive from these, never from the
// implementation under test):
//   - contract.json failClosed — governed syntax that cannot be statically
//     excluded must become an explicit finding, never empty success.
//   - E1/D5 — window.confirm and checkbox-as-switch in feature code fail the
//     build; the review blessing for the capture case is "flag the prohibited
//     global-confirm reference where captured", which fixes the canonical
//     capture syntax at the global reference itself: `window.confirm`.
//   - ECMAScript scope semantics — `var` declarations bind for the whole
//     function (module) before their initializer runs, so a use before the
//     declaration resolves to the local (dead) binding, never the global.
// ---------------------------------------------------------------------------

// Bypass 1: a confirm captured in one function and called in another fails
// closed at the capture site.

test('limits: flags a confirm captured into an outer-scope variable', () => {
  const source = [
    'let c',
    'function capture() { c = window.confirm }',
    "function ask() { return c('x') }",
  ].join('\n')
  assert.deepEqual(syntaxes(source, 'controls/global-confirm'), ['window.confirm'])
})

test('limits: the capture flag does not depend on source order of the functions', () => {
  const source = [
    'let c',
    "function ask() { return c('x') }",
    'function capture() { c = window.confirm }',
  ].join('\n')
  assert.deepEqual(syntaxes(source, 'controls/global-confirm'), ['window.confirm'])
})

test('limits: flags capturing the bare global and the element-access form', () => {
  const bare = 'let c\nfunction capture() { c = confirm }'
  const elementAccess = "let c\nfunction capture() { c = window['confirm'] }"
  assert.deepEqual(syntaxes(bare, 'controls/global-confirm'), ['window.confirm'])
  assert.deepEqual(syntaxes(elementAccess, 'controls/global-confirm'), ['window.confirm'])
})

test('limits: flags capturing a tracked alias or a destructured assignment', () => {
  const aliased = 'let c\nconst d = window.confirm\nfunction capture() { c = d }'
  const destructured = 'let c\nfunction capture() { ({ confirm: c } = window) }'
  assert.deepEqual(syntaxes(aliased, 'controls/global-confirm'), ['window.confirm'])
  assert.deepEqual(syntaxes(destructured, 'controls/global-confirm'), ['window.confirm'])
})

test('limits: flags a capture into an undeclared implicit global', () => {
  assert.deepEqual(syntaxes('export function cap() { c = window.confirm }', 'controls/global-confirm'), ['window.confirm'])
})

test('limits: same-scope sequential assignment keeps one call finding, not a capture flag', () => {
  const source = "let c\nc = window.confirm\nexport const ask = () => c('Delete?')"
  assert.deepEqual(syntaxes(source, 'controls/global-confirm'), ['confirm(...)'])
})

test('limits: block writes inside the same function stay sequential', () => {
  const source = "function decide(flag: boolean) {\n  let c\n  if (flag) { c = window.confirm }\n  return c('Delete?')\n}"
  assert.deepEqual(syntaxes(source, 'controls/global-confirm'), ['confirm(...)'])
})

test('limits: a sequential reassignment to a local value clears the alias', () => {
  const source = "let c\nc = window.confirm\nc = () => true\nexport const ask = () => c('x')"
  assert.deepEqual(ruleIds(source), [])
})

test('limits: untracked closure writes do not flag', () => {
  const source = 'let h\nfunction setup() { h = () => true }\nexport const ask = () => h()'
  assert.deepEqual(ruleIds(source), [])
})

test('limits: factory-value closure writes stay outside the confirm rule (documented limitation)', () => {
  const source = 'let f\nexport function cap() { f = document.createElement }'
  assert.deepEqual(ruleIds(source), [])
})

test('limits: a sequential window-object assignment still reaches the member call', () => {
  const source = "let w\nw = window\nexport const ask = () => w.confirm('Delete?')"
  assert.deepEqual(syntaxes(source, 'controls/global-confirm'), ['window.confirm(...)'])
})

// False positive: `var` names bind for the whole function before their
// initializer runs, so earlier uses see the local binding, not the global.

test('limits: a module-level var confirm shadows the global before its declaration', () => {
  const source = "confirm('x')\nvar confirm = () => true"
  assert.deepEqual(ruleIds(source), [])
})

test('limits: a function-level var confirm shadows across nested blocks', () => {
  const source = [
    'export function askLocal(flag: boolean) {',
    "  if (flag) { confirm('local') }",
    '  var confirm = () => true',
    '}',
  ].join('\n')
  assert.deepEqual(ruleIds(source), [])
})

test('limits: var hoisting covers calls inside loops declared before the var', () => {
  const source = [
    'export function run(list: string[]) {',
    '  for (const item of list) { confirm(item) }',
    '  var confirm = () => true',
    '}',
  ].join('\n')
  assert.deepEqual(ruleIds(source), [])
})

test('limits: var hoisting does not cross function boundaries', () => {
  const source = "function inner() { var confirm = () => true }\nexport function outer() { return confirm('x') }"
  assert.deepEqual(syntaxes(source, 'controls/global-confirm'), ['confirm(...)'])
})

test('limits: a var alias initialized before use still reports', () => {
  const source = "var c = window.confirm\nexport const ask = () => c('Delete?')"
  assert.deepEqual(syntaxes(source, 'controls/global-confirm'), ['confirm(...)'])
})

test('limits: a use before a var alias initializer is dead code, not the global', () => {
  const source = "export const ask = () => c('x')\nvar c = window.confirm"
  assert.deepEqual(ruleIds(source), [])
})

// Bypass 2: an input whose type and role cannot be statically excluded from
// the checkbox-as-switch masquerade fails closed; known non-switch role or
// type is never hijacked.

test('limits: fully dynamic type and role fail closed as one unresolved finding', () => {
  const source = 'export const Fixture = ({ type, role }) => <input type={type} role={role} />'
  const findings = findingsFor(source, 'controls/checkbox-as-switch')
  assert.deepEqual(findings.map((finding) => finding.syntax), ['<input type={expr} role={expr}>'])
  assert.match(findings[0].message, /dynamic type/)
})

test('limits: a known non-switch role is not hijacked by a dynamic type', () => {
  const source = 'export const Fixture = ({ type }) => <input type={type} role="button" />'
  assert.deepEqual(ruleIds(source), [])
})

test('limits: a known non-switch type is not hijacked by a dynamic role', () => {
  const source = 'export const Fixture = ({ role }) => <input type="text" role={role} />'
  assert.deepEqual(ruleIds(source), [])
})

test('limits: a dynamic type without any role stays allowed', () => {
  const source = 'export const Fixture = ({ type }) => <input type={type} />'
  assert.deepEqual(ruleIds(source), [])
})

test('limits: an untyped input with a dynamic role fails closed', () => {
  const source = 'export const Fixture = ({ role }) => <input role={role} />'
  const findings = findingsFor(source, 'controls/checkbox-as-switch')
  assert.deepEqual(findings.map((finding) => finding.syntax), ['<input role={expr}>'])
  assert.match(findings[0].message, /dynamic role/)
})

test('limits: a spread beside a dynamic indicator is unresolved spread reach', () => {
  const withType = 'export const Fixture = (props: Record<string, unknown>) => <input type={props.type} {...props} />'
  const withBoth = 'export const Fixture = (props: Record<string, unknown>) => <input type={props.type} role={props.role} {...props} />'
  assert.deepEqual(syntaxes(withType, 'controls/checkbox-as-switch'), ['<input {...props}>'])
  assert.deepEqual(syntaxes(withBoth, 'controls/checkbox-as-switch'), ['<input {...props}>'])
})

test('limits: a spread with no checkbox, switch, or dynamic indicator stays allowed', () => {
  const source = 'export const Fixture = (props: Record<string, unknown>) => <input {...props} />'
  assert.deepEqual(ruleIds(source), [])
})

// ---------------------------------------------------------------------------
// FIX-P4 defect closure — TypeScript wrappers around the object of a member
// access or a call target (dist/design-system-baseline/cli-lanes/c1-prep/FIX-P4).
//
// Specification sources (expected values derive from these, never from the
// implementation under test):
//   - Parentheses are a runtime no-op and `as`/`satisfies`/`!`/angle-bracket
//     assertions are erased at compile time, so `(window).confirm(...)`,
//     `(window as any).confirm(...)`, `window!.confirm(...)`,
//     `(window satisfies Window).confirm(...)`, an aliased
//     `const w = window as any; w.confirm(...)`, `(React).createElement(...)`,
//     `(React as any).createElement(...)` and `(document as any).createElement(...)`
//     all call the real browser confirm or element factory at runtime and
//     must report identically to their unwrapped form (E1/D5, contract.json
//     failClosed).
//   - A computed member access whose key is not a string literal cannot be
//     proven to be anything in particular; once the object side is provably
//     one of the tracked globals, contract.json failClosed requires a report,
//     never silence — but an ordinary object/computed-key pair that is not a
//     tracked global must stay unreported (no over-broad matching).
// ---------------------------------------------------------------------------

test('FIX-P4: flags a parenthesized window.confirm(...) call the same as the bare form', () => {
  assert.deepEqual(syntaxes("export const ask = () => (window).confirm('Delete?')"), ['window.confirm(...)'])
})

test('FIX-P4: flags an `as any`-cast window.confirm(...) call', () => {
  assert.deepEqual(syntaxes("export const ask = () => (window as any).confirm('Delete?')"), ['window.confirm(...)'])
})

test('FIX-P4: flags a non-null-asserted window!.confirm(...) call', () => {
  assert.deepEqual(syntaxes("export const ask = () => window!.confirm('Delete?')"), ['window.confirm(...)'])
})

test('FIX-P4: flags a `satisfies`-qualified window.confirm(...) call', () => {
  assert.deepEqual(syntaxes("export const ask = () => (window satisfies Window).confirm('Delete?')"), ['window.confirm(...)'])
})

test('FIX-P4: flags an angle-bracket type-assertion window.confirm(...) call', () => {
  const findings = scan({ path: 'src/features/fixture.ts', source: "export const ask = () => (<any>window).confirm('Delete?')", policy: {}, catalog: CATALOG })
  assert.deepEqual(findings.map((finding) => finding.syntax), ['window.confirm(...)'])
})

test('FIX-P4: flags confirm reached through an `as any`-cast alias', () => {
  const source = "const w = window as any\nexport const ask = () => w.confirm('Delete?')"
  assert.deepEqual(syntaxes(source), ['window.confirm(...)'])
})

test('FIX-P4: flags a parenthesized React.createElement("button", ...) call', () => {
  const source = "import * as React from 'react'\nexport const X = () => (React).createElement('button', null, 'go')"
  assert.deepEqual(syntaxes(source), ['createElement("button")'])
})

test('FIX-P4: flags an `as any`-cast React.createElement("button", ...) call', () => {
  const source = "import * as React from 'react'\nexport const X = () => (React as any).createElement('button', null, 'go')"
  assert.deepEqual(syntaxes(source), ['createElement("button")'])
})

test('FIX-P4: flags an `as any`-cast document.createElement("button") call', () => {
  assert.deepEqual(syntaxes("export const X = () => (document as any).createElement('button')"), ['document.createElement("button")'])
})

test('FIX-P4: flags a call target wrapped directly, not just its object', () => {
  assert.deepEqual(syntaxes("export const ask = () => (window.confirm)('Delete?')"), ['window.confirm(...)'])
  assert.deepEqual(syntaxes("export const ask = () => (confirm)('Delete?')"), ['confirm(...)'])
})

test('FIX-P4: a wrapped non-browser object is not mistaken for the tracked global', () => {
  const source = "const obj = { confirm: () => true }\nexport const ask = () => (obj).confirm('Delete?')"
  assert.deepEqual(ruleIds(source), [])
})

test('FIX-P4: an `as`-cast of an unrelated local value is not mistaken for the tracked global', () => {
  const source = "type Ok = { confirm: () => boolean }\nexport function ask(x: Ok) { return (x as Ok).confirm() }"
  assert.deepEqual(ruleIds(source), [])
})

test('FIX-P4: a parenthesized non-browser array/element access stays allowed', () => {
  assert.deepEqual(ruleIds('const arr = [1, 2, 3]\nexport const x = () => (arr)[0]'), [])
})

test('FIX-P4: fails closed on a computed, non-literal confirm key on the browser object', () => {
  assert.deepEqual(syntaxes("export const ask = () => (window)['con' + 'firm']('Delete?')"), ['window.confirm(...)'])
})

test('FIX-P4: fails closed on a computed key reached through globalThis', () => {
  const source = "const key = 'confirm'\nexport const ask = () => globalThis[key]('Delete?')"
  assert.deepEqual(syntaxes(source), ['window.confirm(...)'])
})

test('FIX-P4: does not fail closed on a computed key against an unrelated object', () => {
  const source = "const obj = { run: () => {} }\nconst key = 'ru' + 'n'\nexport const x = () => obj[key]()"
  assert.deepEqual(ruleIds(source), [])
})

test('FIX-P4: a literal, non-confirm key on the browser object stays allowed', () => {
  assert.deepEqual(ruleIds("export const ask = () => window['alert']('hi')"), [])
})

// A parenthesized/cast reference is still tracked through the normal
// alias/call flow (one finding at the call site) rather than ALSO tripping
// the escaped-reference check on the wrapped inner node — a wrapper must not
// double-count the same source occurrence (contract.json occurrence
// counting; file header "one source occurrence is not counted twice").

test('FIX-P4: a parenthesized confirm alias initializer is not double-counted', () => {
  const source = "const c = (window.confirm)\nexport const ask = () => c('Delete?')"
  assert.deepEqual(syntaxes(source, 'controls/global-confirm'), ['confirm(...)'])
})

test('FIX-P4: an `as`-cast confirm alias initializer is not double-counted', () => {
  const source = "const c = window.confirm as any\nexport const ask = () => c('Delete?')"
  assert.deepEqual(syntaxes(source, 'controls/global-confirm'), ['confirm(...)'])
})
