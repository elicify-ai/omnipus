import { test, describe } from 'node:test'
import assert from 'node:assert/strict'
import { parseSourceFile } from './codemod-lib.mjs'
import { verifyFinding, buildVerdicts, buildSiblingMap, parseSyntax, summarizeCounts } from './registry-verify.mjs'

function cache(path, text) {
  const sourceFile = parseSourceFile(path, text)
  return new Map([[path, { sourceFile, text }]])
}

function verify(finding, text, { siblingMap } = {}) {
  return verifyFinding(finding, {
    sourceCache: cache(finding.path, text),
    siblingMap: siblingMap ?? new Map(),
  })
}

describe('parseSyntax', () => {
  test('parses a plain name boundary', () => {
    assert.deepEqual(parseSyntax('ChatImage#className'), { kind: 'name', owner: 'ChatImage', name: 'className' })
  })
  test('parses a member boundary', () => {
    assert.deepEqual(parseSyntax('Calendar#Chevron.className'), { kind: 'member', owner: 'Calendar', base: 'Chevron', key: 'className' })
  })
  test('parses a paint boundary', () => {
    assert.deepEqual(parseSyntax('AgentCard#dom-style.backgroundColor<-agent.color'), {
      kind: 'paint', owner: 'AgentCard', receiver: 'dom-style', property: 'backgroundColor', exprText: 'agent.color',
    })
  })
})

describe('PASS cases', () => {
  test('direct forward: className read straight into a JSX attribute', () => {
    const text = `
      export function Avatar({ className }: { className?: string }) {
        return <div className={className} />
      }
    `
    const finding = { ruleId: 'ts-colors/extension-boundary', path: 'src/components/Avatar.tsx', syntax: 'Avatar#className', count: 1 }
    const result = verify(finding, text)
    assert.equal(result.verdict, 'PASS')
    assert.equal(result.category, 'caller pass-through')
    assert.ok(result.evidence.length >= 2, 'expects a declaration entry plus at least one sink entry')
  })

  test('cn-merged with literals: className merged with literal classes through cn(...)', () => {
    const text = `
      import { cn } from '@/lib/utils'
      export function Card({ className }: { className?: string }) {
        return <div className={cn('rounded-md border', className)} />
      }
    `
    const finding = { ruleId: 'spacing/extension-boundary', path: 'src/components/Card.tsx', syntax: 'Card#className', count: 1 }
    const result = verify(finding, text)
    assert.equal(result.verdict, 'PASS')
    assert.equal(result.category, 'caller pass-through')
  })

  test('renamed prop: className destructured under a different local name (triggerClassName)', () => {
    const text = `
      export function SelectTrigger({ className: triggerClassName }: { className?: string }) {
        return <button className={triggerClassName} />
      }
    `
    const finding = { ruleId: 'ts-colors/extension-boundary', path: 'src/components/ui/select.tsx', syntax: 'SelectTrigger#triggerClassName', count: 1 }
    const result = verify(finding, text)
    assert.equal(result.verdict, 'PASS')
    assert.equal(result.category, 'caller pass-through')
  })

  test('two-hop alias through a local transparent joiner still resolves to PASS', () => {
    const text = `
      function classes(...parts: (string | undefined)[]): string {
        return parts.filter(Boolean).join(' ')
      }
      export function ChipListInput({ chipClassName }: { chipClassName?: string }) {
        const resolvedChipClassName = classes('base', chipClassName)
        return <span className={resolvedChipClassName} />
      }
    `
    const finding = { ruleId: 'typography/extension-boundary', path: 'src/components/workspaces/ChipListInput.tsx', syntax: 'ChipListInput#chipClassName', count: 1 }
    const result = verify(finding, text)
    assert.equal(result.verdict, 'PASS')
  })

  test('an unrelated renamed destructure key elsewhere in the body does not block a real direct forward (regression: Table#className)', () => {
    const text = `
      export const Table = React.forwardRef(({ className, containerProps }, ref) => {
        const { className: containerClassName, onKeyDown } = containerProps
        return <table className={cn('w-full', className)} data-x={containerClassName} />
      })
    `
    const finding = { ruleId: 'ts-colors/extension-boundary', path: 'src/components/ui/table.tsx', syntax: 'Table#className', count: 1 }
    const result = verify(finding, text)
    assert.equal(result.verdict, 'PASS')
    assert.equal(result.category, 'caller pass-through')
  })
})

describe('REJECT cases', () => {
  test('transformed: value is passed through .replace() before use', () => {
    const text = `
      export function Badge({ className }: { className?: string }) {
        return <div className={className.replace('a', 'b')} />
      }
    `
    const finding = { ruleId: 'ts-colors/extension-boundary', path: 'src/components/Badge.tsx', syntax: 'Badge#className', count: 1 }
    const result = verify(finding, text)
    assert.equal(result.verdict, 'REJECT')
    assert.match(result.reason, /\.replace\(\.\.\.\)`? is called/)
  })

  test('module-level const: the value is not a caller-supplied prop at all', () => {
    const text = `
      const DEFAULT_CLASS = 'text-sm'
      export function Foo() {
        return <div className={DEFAULT_CLASS} />
      }
    `
    const finding = { ruleId: 'ts-colors/extension-boundary', path: 'src/components/Foo.tsx', syntax: 'Foo#DEFAULT_CLASS', count: 1 }
    const result = verify(finding, text)
    assert.equal(result.verdict, 'REJECT')
    assert.match(result.reason, /module-level constant/)
  })

  test('non-prop source: value is a local variable computed inside the owner, not forwarded from a caller', () => {
    const text = `
      export function Foo({ children }: { children?: unknown }) {
        const computedClassName = 'text-sm'
        return <div className={computedClassName}>{children}</div>
      }
    `
    const finding = { ruleId: 'ts-colors/extension-boundary', path: 'src/components/Foo.tsx', syntax: 'Foo#computedClassName', count: 1 }
    const result = verify(finding, text)
    assert.equal(result.verdict, 'REJECT')
    assert.match(result.reason, /local variable/)
    assert.match(result.reason, /not a caller-supplied prop/)
  })

  test('status chrome: a user colour field is read on a severity/config lookup, not a persisted entity field', () => {
    const text = `
      const SEVERITY_CONFIG = { error: { bgColor: '#f00' } }
      function IssueCard({ config }: { config: { bgColor: string } }) {
        return <div style={{ backgroundColor: config.bgColor }} />
      }
    `
    const finding = { ruleId: 'ts-colors/extension-boundary', path: 'src/components/settings/DiagnosticsSection.tsx', syntax: 'IssueCard#dom-style.backgroundColor<-config.bgColor', count: 1 }
    const result = verify(finding, text)
    assert.equal(result.verdict, 'REJECT')
    assert.match(result.reason, /status\/severity\/config/)
  })

  test('test stand-in file: never an exception category regardless of shape', () => {
    const text = `
      export function StubAvatar({ className }: { className?: string }) {
        return <div className={className} />
      }
    `
    const finding = { ruleId: 'ts-colors/extension-boundary', path: 'src/components/Avatar.test.tsx', syntax: 'StubAvatar#className', count: 1 }
    const result = verify(finding, text)
    assert.equal(result.verdict, 'REJECT')
    assert.match(result.reason, /test stand-in/)
  })
})

describe('NEEDS-READ cases', () => {
  test('ambiguous: value is passed through an unrecognized function before the sink', () => {
    const text = `
      export function Foo({ className }: { className?: string }) {
        return <div className={formatClass(className)} />
      }
      function formatClass(x: string) { return x }
    `
    const finding = { ruleId: 'ts-colors/extension-boundary', path: 'src/components/Foo.tsx', syntax: 'Foo#className', count: 1 }
    const result = verify(finding, text)
    assert.equal(result.verdict, 'NEEDS-READ')
  })

  test('ambiguous: owner declaration cannot be located at all', () => {
    const text = `export const unrelated = 1`
    const finding = { ruleId: 'ts-colors/extension-boundary', path: 'src/components/Ghost.tsx', syntax: 'GhostOwner#className', count: 1 }
    const result = verify(finding, text)
    assert.equal(result.verdict, 'NEEDS-READ')
  })

  test('ambiguous: paint expression is not a simple property chain', () => {
    const text = `
      export function Foo({ agent }: { agent: { color: string } }) {
        return <div style={{ backgroundColor: getColor(agent) }} />
      }
      function getColor(a: { color: string }) { return a.color }
    `
    const finding = { ruleId: 'ts-colors/extension-boundary', path: 'src/components/Foo.tsx', syntax: 'Foo#dom-style.backgroundColor<-getColor(agent)', count: 1 }
    const result = verify(finding, text)
    assert.equal(result.verdict, 'NEEDS-READ')
  })
})

describe('draft rule fidelity', () => {
  test('the syntax triple is carried byte-identically into the draft rule', () => {
    const text = `
      export function Avatar({ className }: { className?: string }) {
        return <div className={className} />
      }
    `
    const finding = { ruleId: 'ts-colors/extension-boundary', path: 'src/components/agents/Avatar.tsx', syntax: 'Avatar#className', count: 3 }
    const result = verify(finding, text)
    assert.equal(result.verdict, 'PASS')
    assert.ok(result.draftRule)
    assert.equal(result.draftRule.ruleId, finding.ruleId)
    assert.equal(result.draftRule.path, finding.path)
    assert.equal(result.draftRule.syntax, finding.syntax)
    assert.equal(typeof result.draftRule.category, 'string')
    assert.equal(typeof result.draftRule.owner, 'string')
    assert.equal(typeof result.draftRule.reason, 'string')
  })

  test('a sibling rule already registered for the same (path, syntax) hands its category to a PASS verdict', () => {
    const text = `
      export function Calendar() {
        return { Chevron: ({ className: chevronClassName }: { className?: string }) => <svg className={chevronClassName} /> }
      }
    `
    const finding = { ruleId: 'ts-colors/extension-boundary', path: 'src/components/ui/calendar.tsx', syntax: 'Calendar#Chevron.className', count: 1 }
    const rulesDoc = { rules: [{ ruleId: 'typography/extension-boundary', path: finding.path, syntax: finding.syntax, category: 'third-party widget values', owner: 'third-party integration boundary exception registry', reason: 'x' }] }
    const siblingMap = buildSiblingMap(rulesDoc)
    const result = verify(finding, text, { siblingMap })
    assert.equal(result.verdict, 'PASS')
    assert.equal(result.category, 'third-party widget values')
  })
})

describe('buildVerdicts batch wiring', () => {
  test('skips findings already covered by an exact [ruleId, path, syntax] rule', () => {
    const auditReport = {
      debt: {
        fingerprints: [
          { ruleId: 'ts-colors/extension-boundary', path: 'src/components/Avatar.tsx', syntax: 'Avatar#className', count: 1 },
          { ruleId: 'ts-colors/raw-color', path: 'src/components/Avatar.tsx', syntax: '#fff', count: 1 },
        ],
      },
    }
    const rulesDoc = { rules: [{ ruleId: 'ts-colors/extension-boundary', path: 'src/components/Avatar.tsx', syntax: 'Avatar#className', category: 'caller pass-through', owner: 'x', reason: 'x' }] }
    const { verdicts, totalBoundaryFindings, totalUncovered } = buildVerdicts({ auditReport, rulesDoc, readSource: () => null })
    assert.equal(totalBoundaryFindings, 1)
    assert.equal(totalUncovered, 0)
    assert.equal(verdicts.length, 0)
  })

  test('NEEDS-READ when the source file cannot be read', () => {
    const auditReport = { debt: { fingerprints: [{ ruleId: 'ts-colors/extension-boundary', path: 'src/missing.tsx', syntax: 'Missing#className', count: 1 }] } }
    const rulesDoc = { rules: [] }
    const { verdicts } = buildVerdicts({ auditReport, rulesDoc, readSource: () => null })
    assert.equal(verdicts.length, 1)
    assert.equal(verdicts[0].verdict, 'NEEDS-READ')
  })

  test('summarizeCounts tallies verdicts and category buckets', () => {
    const verdicts = [
      { verdict: 'PASS', category: 'caller pass-through' },
      { verdict: 'PASS', category: 'caller pass-through' },
      { verdict: 'REJECT', category: null },
    ]
    const { counts, byCategory } = summarizeCounts(verdicts)
    assert.equal(counts.PASS, 2)
    assert.equal(counts.REJECT, 1)
    assert.equal(counts['NEEDS-READ'], 0)
    assert.equal(byCategory['PASS:caller pass-through'], 2)
  })
})
