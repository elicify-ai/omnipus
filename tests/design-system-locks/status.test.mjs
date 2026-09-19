import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

import { extensions, scan } from '../../scripts/design-system-locks/status.mjs'

// Expected values come from docs/internal/design/design-system-definition.md §D4
// (the seven-state task-palette winner) and design-system/enforcement/contract.json
// (scanner API, finding shape, fail-closed parse/unsupported coverage). Hexes are
// transcribed from the constitution, not from scanner output.
const D4 = {
  inbox: '#9CA3AF',
  next: '#3B82F6',
  'in-progress': '#D4AF37',
  blocked: '#F97316',
  done: '#10B981',
  failed: '#EF4444',
  cancelled: '#EAB308',
}

const POLICY = {
  tokenCssNames: [
    '--color-status-inbox',
    '--color-status-next',
    '--color-status-in-progress',
    '--color-status-blocked',
    '--color-status-done',
    '--color-status-failed',
    '--color-status-cancelled',
    '--status-inbox-foreground',
    '--color-muted',
    '--color-info',
    '--color-accent',
    '--color-blocked',
    '--color-success',
    '--color-error',
    '--color-warning',
    '--color-cancelled',
    '--primitive-color-grey',
    '--color-surface-2',
    '--color-secondary',
  ],
  resolvedTokens: {
    'color.status.inbox': D4.inbox,
    'color.status.next': D4.next,
    'color.status.in-progress': D4['in-progress'],
    'color.status.blocked': D4.blocked,
    'color.status.done': D4.done,
    'color.status.failed': D4.failed,
    'color.status.cancelled': D4.cancelled,
    'color.muted': D4.inbox,
    'color.info': D4.next,
    'color.accent.default': D4['in-progress'],
    'color.blocked': D4.blocked,
    'color.success': D4.done,
    'color.error': D4.failed,
    'color.warning': D4.cancelled,
    'color.cancelled': D4.cancelled,
    'primitive.color.grey': D4.inbox,
    'color.surface.2': '#111113',
    'color.secondary': '#E5E7EB',
  },
}

const RULE = {
  mismatch: 'design-system/status-mismatch',
  literal: 'design-system/status-literal',
  unsupported: 'design-system/status-unsupported',
  parseError: 'design-system/status-parse-error',
}

function run(source, extra = {}) {
  return scan({
    path: extra.path ?? 'src/fixture.ts',
    source,
    policy: extra.policy ?? POLICY,
    modules: extra.modules,
  })
}

function ids(findings) {
  return findings.map((finding) => finding.ruleId)
}

describe('design-system status lock API', () => {
  it('exports a synchronous scan function and dotted extensions for TS/JS/CSS/SVG', () => {
    assert.equal(typeof scan, 'function')
    assert.equal(scan.constructor.name, 'Function')
    for (const ext of ['.ts', '.tsx', '.js', '.jsx', '.css', '.svg']) {
      assert.ok(extensions.includes(ext), `extensions must include ${ext}`)
    }
    assert.ok(extensions.every((ext) => ext.startsWith('.')), 'every extension includes the leading dot')
  })

  it('returns findings with the shared contract shape and POSIX path', () => {
    const findings = run('export const STATUS_COLORS = { inbox: "#9CA3AF" }\n')
    assert.ok(findings.length > 0)
    for (const finding of findings) {
      assert.equal(typeof finding.ruleId, 'string')
      assert.ok(finding.ruleId.length > 0)
      assert.equal(finding.path, 'src/fixture.ts')
      assert.equal(typeof finding.syntax, 'string')
      assert.ok(finding.syntax.length > 0)
      assert.equal(typeof finding.message, 'string')
      assert.ok(finding.message.length > 0)
      assert.equal(typeof finding.line, 'number')
      assert.ok(finding.line >= 1)
      assert.equal(typeof finding.column, 'number')
      assert.ok(finding.column >= 1)
    }
  })
})

describe('governed cross-module status paint', () => {
  const paletteModule = `
    export const PALETTE = { inbox: '#9CA3AF', next: '#3B82F6', in_progress: '#D4AF37', blocked: '#F97316', done: '#10B981', failed: '#EF4444' }
    export const CANCELLED_COLOR = '#EAB308'
    export function lookup(value) { return PALETTE[value] ?? PALETTE.inbox }
    export function display(record) { return record.cancelled ? CANCELLED_COLOR : lookup(record.status) }
  `

  it('proves a dynamic imported palette lookup from the governed module source', () => {
    const source = `import { PALETTE as paints } from '@/lib/palette'; export const View = ({ item }) => { const statusColor = paints[item.status]; return <i status={item.status} style={{ color: statusColor }} /> }`
    const modules = { 'src/fixture.tsx': source, 'src/lib/palette.ts': paletteModule }
    assert.deepEqual(run(source, { path: 'src/fixture.tsx', modules }), [])
  })

  it('proves imported helper return branches and rejects a wrong governed branch', () => {
    const good = `import { display as choose } from '@/lib/palette'; export const View = ({ task }) => task.status === 'failed' && <i style={{ color: choose(task), backgroundColor: \`${'${choose(task)}'}1a\` }} />`
    const badModule = `${paletteModule}\nexport function wrong(record) { return record.status === 'failed' ? PALETTE.next : PALETTE[record.status] }`
    const bad = `import { wrong as choose } from '@/lib/palette'; export const View = ({ task }) => task.status === 'failed' && <i style={{ color: choose(task) }} />`
    assert.deepEqual(run(good, { path: 'src/fixture.tsx', modules: { 'src/fixture.tsx': good, 'src/lib/palette.ts': paletteModule } }), [])
    const findings = run(bad, { path: 'src/fixture.tsx', modules: { 'src/fixture.tsx': bad, 'src/lib/palette.ts': badModule } })
    assert.ok(findings.some((finding) => finding.ruleId === RULE.mismatch))
  })

  it('fails closed when an imported paint module is absent from governed context', () => {
    const source = `import { PALETTE } from '@/external/palette'; export const View = ({ item }) => <i status="failed" style={{ color: PALETTE[item.status] }} />`
    const findings = run(source, { path: 'src/fixture.tsx', modules: { 'src/fixture.tsx': source } })
    assert.ok(findings.some((finding) => finding.ruleId === RULE.unsupported))
  })
})

describe('permitted D4 token references', () => {
  it('accepts a seven-state map that only references status tokens', () => {
    const source = `
      export const STATUS_COLORS = {
        inbox: 'var(--color-status-inbox)',
        next: 'var(--color-status-next)',
        in_progress: 'var(--color-status-in-progress)',
        blocked: 'var(--color-status-blocked)',
        done: 'var(--color-status-done)',
        failed: 'var(--color-status-failed)',
        cancelled: 'var(--color-status-cancelled)',
      }
    `
    assert.deepEqual(run(source), [])
  })

  it('accepts generated-token element access for the matching D4 state', () => {
    const source = `
      import { tokens } from './tokens'
      export const STATUS_COLORS = {
        inbox: tokens['color.status.inbox'],
        'in-progress': tokens['color.status.in-progress'],
      }
    `
    assert.deepEqual(run(source), [])
  })

  it('accepts a CSS status selector that uses the matching status custom property', () => {
    const source = `[data-status="inbox"] { color: var(--color-status-inbox); }`
    assert.deepEqual(run(source, { path: 'src/fixture.css' }), [])
  })

  it('accepts the muted semantic token for inbox when policy resolves it to D4 grey', () => {
    const source = `export const STATUS_BADGE = { inbox: 'text-[var(--color-muted)]' }`
    assert.deepEqual(run(source), [])
  })

  it('does not treat a status label map without colours as a palette', () => {
    const source = `export const STATUS_LABELS = { inbox: 'Inbox', next: 'Next', warning: 'Warning' }`
    assert.deepEqual(run(source), [])
  })

  it('does not call arbitrary warning copy a status palette', () => {
    const source = `
      export const toast = { warning: 'Disk space is low' }
      export function Banner() { return <p className="text-warning">warning</p> }
    `
    assert.deepEqual(run(source, { path: 'src/fixture.tsx' }), [])
  })

  it('does not treat pagination next/done colour keys as a D4 status map', () => {
    const source = `
      export const paginationColors = {
        next: '#111111',
        done: '#222222',
      }
    `
    assert.deepEqual(run(source), [])
  })
})

describe('forbidden D4 mismatches', () => {
  it('reports mismatch when in_progress uses the calendar blue instead of Forge Gold', () => {
    const findings = run('export const STATUS_COLORS = { in_progress: "#60A5FA" }\n')
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, RULE.mismatch)
    assert.match(findings[0].message, /in-progress/)
    assert.match(findings[0].message, /#D4AF37/)
    assert.match(findings[0].message, /#60A5FA/)
    assert.match(findings[0].syntax, /in_progress/)
    assert.match(findings[0].syntax, /#60A5FA/)
    assert.equal(findings[0].syntax.includes('export const'), false)
  })

  it('reports mismatch for a nested chip bg under a D4 key', () => {
    const findings = run(`
      export const STATUS_STYLE = {
        in_progress: { bg: '#60A5FA', icon: 'CircleNotch' },
      }
    `)
    assert.ok(findings.some((finding) => finding.ruleId === RULE.mismatch))
    const mismatch = findings.find((finding) => finding.ruleId === RULE.mismatch)
    assert.match(mismatch.syntax, /#60A5FA/)
  })

  it('reports mismatch when a status map uses the warning token for in-progress', () => {
    const findings = run(`export const STATUS_BADGE = { in_progress: 'text-[color:var(--color-warning)]' }`)
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, RULE.mismatch)
    assert.match(findings[0].message, /in-progress/)
  })

  it('reports mismatch for a CSS status selector with a non-D4 hex', () => {
    const findings = run('[data-status="in_progress"] { background: #60A5FA; }', { path: 'src/fixture.css' })
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, RULE.mismatch)
    assert.equal(findings[0].syntax.includes('/*'), false)
    assert.match(findings[0].syntax, /#60A5FA/)
  })

  it('reports mismatch for a Tailwind hue utility on a D4 key', () => {
    const findings = run(`export const STATUS_CLASS = { blocked: 'bg-yellow-400 text-black' }`)
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, RULE.mismatch)
  })

  it('reports mismatch for a class-builder ternary that ties a D4 key to a hex class', () => {
    const findings = run(`
      export function chip(status: string) {
        return status === 'in_progress' ? 'bg-[#60A5FA]' : 'bg-[var(--color-status-inbox)]'
      }
    `)
    assert.ok(findings.some((finding) => finding.ruleId === RULE.mismatch))
  })

  it('reports mismatch for a switch case that returns a non-D4 hex', () => {
    const findings = run(`
      export function statusColor(status: string) {
        switch (status) {
          case 'done': return '#34D399'
          default: return 'var(--color-status-inbox)'
        }
      }
    `)
    assert.ok(findings.some((finding) => finding.ruleId === RULE.mismatch && finding.syntax.includes('#34D399')))
  })

  it('reports mismatch for a shorthand status map value resolved in-file', () => {
    const findings = run(`
      const done = '#EF4444'
      export const STATUS_COLORS = { done }
    `)
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, RULE.mismatch)
    assert.match(findings[0].message, /done/)
    assert.match(findings[0].message, /#10B981/)
  })

  it('reports mismatch for a style alias applied to explicit status chrome', () => {
    const findings = run(`
      const doneStyle = { backgroundColor: '#EF4444' }
      export const DoneChip = () => <span status="done" style={doneStyle}>Done</span>
    `, { path: 'src/fixture.tsx' })
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, RULE.mismatch)
    assert.match(findings[0].message, /done/)
  })

  it('reports mismatch when a status selector uses another status token on a side border', () => {
    const findings = run('[data-status="done"] { border-left-color: var(--color-status-failed); }', { path: 'src/fixture.css' })
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, RULE.mismatch)
    assert.match(findings[0].message, /done/)
  })
})

describe('independently embedded D4 literals', () => {
  it('reports literal when a D4 key embeds the matching hex instead of a token', () => {
    const findings = run('export const STATUS_COLORS = { inbox: "#9CA3AF" }\n')
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, RULE.literal)
    assert.match(findings[0].message, /inbox/)
    assert.match(findings[0].message, /#9CA3AF/)
    assert.match(findings[0].message, /--color-status-inbox/)
  })

  it('reports literal for a CSS status custom property set to the matching D4 hex', () => {
    const findings = run(':root { --color-status-done: #10B981; }', { path: 'src/fixture.css' })
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, RULE.literal)
  })

  it('reports literal for TASK_CANCELLED_COLOR even when the hex matches D4 cancelled', () => {
    const findings = run(`export const TASK_CANCELLED_COLOR = '#EAB308'\n`)
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, RULE.literal)
    assert.match(findings[0].message, /cancelled/)
  })

  it('uppercases hex and strips comments from canonical syntax', () => {
    const findings = run(`
      export const STATUS_COLORS = {
        // live work
        inbox: '#9ca3af',
      }
    `)
    assert.equal(findings.length, 1)
    assert.equal(findings[0].syntax.includes('//'), false)
    assert.match(findings[0].syntax, /#9CA3AF/)
  })

  it('still reports a matching literal on a chart-like path (no scanner self-exemption)', () => {
    const findings = run(
      'export const STATUS_COLORS = { failed: "#EF4444" }\n',
      { path: 'src/components/workspaces/graph/taskGraph.ts' },
    )
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, RULE.literal)
    assert.equal(findings[0].path, 'src/components/workspaces/graph/taskGraph.ts')
  })
})

describe('aliases and equivalent states', () => {
  it('treats in_progress, in-progress, and inProgress as the same D4 state', () => {
    const sources = [
      'export const STATUS_COLORS = { in_progress: "#60A5FA" }\n',
      'export const STATUS_COLORS = { "in-progress": "#60A5FA" }\n',
      'export const STATUS_COLORS = { inProgress: "#60A5FA" }\n',
    ]
    for (const source of sources) {
      const findings = run(source)
      assert.equal(findings.length, 1, source)
      assert.equal(findings[0].ruleId, RULE.mismatch)
      assert.match(findings[0].message, /in-progress/)
    }
  })

  it('maps plan aliases draft, approved, and running onto the D4 palette', () => {
    const findings = run(`
      export const PLAN_STATE_COLORS = {
        draft: '#9CA3AF',
        approved: '#60A5FA',
        running: 'var(--color-status-in-progress)',
      }
    `)
    const rules = ids(findings).sort()
    assert.deepEqual(rules, [RULE.literal, RULE.mismatch].sort())
    assert.ok(findings.some((finding) => finding.ruleId === RULE.literal && /inbox/.test(finding.message)))
    assert.ok(findings.some((finding) => finding.ruleId === RULE.mismatch && /next/.test(finding.message)))
  })
})

describe('unsupported dynamic coverage and parse failures', () => {
  it('reports unsupported for a computed status key that cannot be resolved', () => {
    const findings = run(`
      export const STATUS_COLORS = {
        [statusKey]: '#9CA3AF',
        next: 'var(--color-status-next)',
      }
    `)
    assert.ok(findings.some((finding) => finding.ruleId === RULE.unsupported))
    assert.ok(findings.some((finding) => finding.message.toLowerCase().includes('computed')))
    assert.equal(findings.some((finding) => finding.ruleId === RULE.literal && finding.syntax.includes('next')), false)
  })

  it('constant-folds a computed string key and still classifies the colour', () => {
    const findings = run(`
      export const STATUS_COLORS = {
        ['in' + '_progress']: '#60A5FA',
      }
    `)
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, RULE.mismatch)
  })

  it('reports unsupported for a spread inside a status palette', () => {
    const findings = run(`
      export const STATUS_COLORS = {
        ...otherPalette,
        inbox: 'var(--color-status-inbox)',
      }
    `)
    assert.ok(findings.some((finding) => finding.ruleId === RULE.unsupported))
    assert.ok(findings.some((finding) => finding.message.toLowerCase().includes('spread')))
  })

  it('reports unsupported when a status-named binding is not a static palette', () => {
    const findings = run(`export const STATUS_COLORS = createPalette()\n`)
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, RULE.unsupported)
  })

  it('reports parse-error for invalid TypeScript instead of empty success', () => {
    const findings = run('export const STATUS_COLORS = { inbox: "#9CA3AF"\n', { path: 'src/broken.ts' })
    assert.ok(findings.length > 0)
    assert.equal(findings[0].ruleId, RULE.parseError)
    assert.equal(findings[0].path, 'src/broken.ts')
    assert.ok(findings[0].syntax.length > 0)
  })

  it('reports parse-error for invalid CSS instead of empty success', () => {
    const findings = run('[data-status="inbox"] { color: #9CA3AF', { path: 'src/broken.css' })
    assert.ok(findings.length > 0)
    assert.equal(findings[0].ruleId, RULE.parseError)
  })

  it('reports parse-error for an unknown extension instead of empty success', () => {
    const findings = run('inbox: #9CA3AF', { path: 'src/fixture.unknown' })
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, RULE.parseError)
  })

  it('reports unsupported for an unregistered var even when its resolved hex matches D4', () => {
    const findings = run(`export const STATUS_COLORS = { done: 'var(--color-fake)' }`, {
      policy: {
        tokenCssNames: ['--color-status-done'],
        resolvedTokens: {
          'color.status.done': D4.done,
          'color.fake': D4.done,
        },
      },
    })
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, RULE.unsupported)
  })
})

describe('SVG status chrome', () => {
  it('reports literal for an SVG fill that matches D4 on a status class', () => {
    const source = `<svg><circle class="status-inbox" fill="#9CA3AF" /></svg>`
    const findings = run(source, { path: 'src/fixture.svg' })
    assert.ok(findings.some((finding) => finding.ruleId === RULE.literal))
  })

  it('accepts an SVG fill that uses the matching status token', () => {
    const source = `<svg><circle class="status-inbox" fill="var(--color-status-inbox)" /></svg>`
    assert.deepEqual(run(source, { path: 'src/fixture.svg' }), [])
  })

  it('reports mismatch for status paint inside an SVG style element', () => {
    const source = `<svg><style>.status-done { fill: #EF4444; }</style><circle class="status-done" /></svg>`
    const findings = run(source, { path: 'src/fixture.svg' })
    assert.ok(findings.some((finding) => finding.ruleId === RULE.mismatch && finding.message.includes('done')))
  })
})

describe('TypeScript 6 JSX names (member, namespaced tag, namespaced attribute)', () => {
  it('does not throw on a member JSX tag and still classifies status paint', () => {
    const source = `
      export const Chip = () => (
        <Icons.StatusChip status="done" style={{ backgroundColor: '#EF4444' }}>Done</Icons.StatusChip>
      )
    `
    const findings = run(source, { path: 'src/fixture.tsx' })
    assert.equal(findings.some((finding) => finding.ruleId === RULE.parseError), false)
    assert.ok(findings.some((finding) => finding.ruleId === RULE.mismatch && /done/.test(finding.message)))
  })

  it('does not throw on a namespaced JSX tag and still classifies status paint', () => {
    const source = `
      export const Mark = () => (
        <svg:g className="status-inbox" fill="#9CA3AF">dot</svg:g>
      )
    `
    const findings = run(source, { path: 'src/fixture.tsx' })
    assert.equal(findings.some((finding) => finding.ruleId === RULE.parseError), false)
    assert.ok(findings.some((finding) => finding.ruleId === RULE.literal && /inbox/.test(finding.message)))
  })

  it('does not throw on a namespaced JSX attribute and still classifies sibling status paint', () => {
    const source = `
      export const Chip = () => (
        <span xml:lang="en" status="done" style={{ color: '#EF4444' }}>Done</span>
      )
    `
    const findings = run(source, { path: 'src/fixture.tsx' })
    assert.equal(findings.some((finding) => finding.ruleId === RULE.parseError), false)
    assert.ok(findings.some((finding) => finding.ruleId === RULE.mismatch && /done/.test(finding.message)))
  })

  it('treats a member tag without status paint as clean, not a parse failure', () => {
    const source = `
      export const DialogRoot = () => <Dialog.Root><Dialog.Content>Hi</Dialog.Content></Dialog.Root>
    `
    assert.deepEqual(run(source, { path: 'src/fixture.tsx' }), [])
  })
})

describe('non-paint status and state bindings stay clean', () => {
  it('does not treat a cancelled boolean as a missing status palette', () => {
    const source = `
      export function ToolApproval() {
        let cancelled = false
        return cancelled
      }
    `
    assert.deepEqual(run(source), [])
  })

  it('does not treat a test badge query as a missing status palette', () => {
    const source = `
      export function testBadge() {
        const badge = screen.getByTestId("tool-call-badge")
        return badge
      }
    `
    assert.deepEqual(run(source, { path: 'src/fixture.test.tsx' }), [])
  })

  it('does not treat a status domain record as a missing status palette', () => {
    const source = `
      export function run(isRunning: boolean) {
        const status = isRunning ? { type: 'running' } : { type: 'complete' }
        return status
      }
    `
    assert.deepEqual(run(source), [])
  })

  it('does not treat unrelated locals inside a status-named function as a palette', () => {
    const source = `
      export function statusColor(status: string) {
        const label = "ready"
        const count = 2
        return status + label + count
      }
    `
    assert.deepEqual(run(source), [])
  })

  it('still fail-closes an unresolved identifier in a status colour map', () => {
    const findings = run(`export const STATUS_COLORS = { inbox: mysteryColor }\n`)
    assert.ok(findings.some((finding) => finding.ruleId === RULE.unsupported))
    assert.ok(findings.some((finding) => /mysteryColor/.test(finding.syntax) || /mysteryColor/.test(finding.message)))
  })

  it('still fail-closes an unresolved style alias on explicit status chrome', () => {
    const source = `
      export const Chip = () => <span status="done" style={unknownStyle}>Done</span>
    `
    const findings = run(source, { path: 'src/fixture.tsx' })
    assert.ok(findings.some((finding) => finding.ruleId === RULE.unsupported))
    assert.ok(findings.some((finding) => /unknownStyle/.test(finding.syntax) || /unknownStyle/.test(finding.message)))
  })

  it('does not treat error words in test ids, messages, or JSX data props as status paint', () => {
    const source = `
      const smokeTestId = 'provider'
      const translatedMessage = 'Try again'
      const record = { error: translatedMessage, done: false }
      export const View = ({ error }: { error: string }) => <Panel data-testid={\`${'${smokeTestId}'}-error\`} error={error} record={record} />
    `
    assert.deepEqual(run(source, { path: 'src/fixture.tsx' }), [])
  })

  it('keeps status-condition context limited to paint-bearing attributes and values', () => {
    const source = `
      export const View = ({ phase, errorMsg, probeStatus, probedModel }) => <>
        {phase === 'error' && <p className="text-[var(--color-error)]">{errorMsg}</p>}
        {probeStatus === 'success' && (
          <p style={{ color: 'var(--color-success)' }}>
            {\`Signed in — ${'${probedModel}'} is ready\`}
          </p>
        )}
      </>
    `
    assert.deepEqual(run(source, { path: 'src/fixture.tsx' }), [])
  })

  it('does not treat ordinary status-bearing record updates as colour maps', () => {
    const source = `
      const errMsg = {
        content: isCancelAck ? '' : translatedMessage,
        status: isCancelAck ? 'interrupted' : 'error',
        ...(!isCancelAck && llmError ? { errorCode: llmError.code } : {}),
      }
      const next = { ...applyMessageArray(messages, state), isStreaming: false }
      export { errMsg, next }
    `
    assert.deepEqual(run(source, { path: 'src/fixture.ts' }), [])
  })

  it('does not treat non-paint status records, spreads, or DOM style reads as palettes', () => {
    const source = `
      const next = { kickoffAttemptStatus: { ...state.kickoffAttemptStatus, [workspaceId]: 'in-flight' } }
      const cancelledColor = screen.getByText('Cancelled').style.color
      export { next, cancelledColor }
    `
    assert.deepEqual(run(source, { path: 'src/fixture.test.tsx' }), [])
  })

  it('parses complete registered var references and ignores neutral paint around the matching status accent', () => {
    const source = `
      export const STATUS_STYLES = {
        error: 'bg-[var(--color-surface-2)] border-[var(--color-error)]/30 text-[var(--color-secondary)]',
        done: 'bg-[var(--color-surface-2)] border-[var(--color-success)]/30 text-[var(--color-secondary)]',
      }
    `
    assert.deepEqual(run(source), [])
  })

  it('retains the complete var reference in mismatch evidence', () => {
    const findings = run(`export const STATUS_STYLES = { done: 'text-[var(--color-warning)]' }`)
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, RULE.mismatch)
    assert.match(findings[0].message, /var\(--color-warning\)/)
  })
})
