import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

import { loadPolicy } from '../../scripts/design-system-locks/policy.mjs'
import { scan as scanSpacing } from '../../scripts/design-system-locks/spacing.mjs'
import { scan as scanTypography } from '../../scripts/design-system-locks/typography.mjs'
import { scan as scanTsColors } from '../../scripts/design-system-locks/ts-colors.mjs'

// Cross-scanner false-green conformance suite.
//
// Purpose: independent reviews of spacing.mjs, typography.mjs and ts-colors.mjs
// have repeatedly found the same class of bug in different resolution paths --
// a real, off-scale/ungoverned class reaches a `className` sink while scan()
// reports zero findings. This suite makes "no silent pass" a single, shared,
// executable contract across all three value-resolving scanners.
//
// Every case is a template over one GOOD value and one BAD value, applied
// identically to all three scanners with each scanner's own GOOD/BAD pair
// (see the design doc handed to this suite's author). GOOD and BAD are never
// `p-4`/`text-sm`-style utilities: those are themselves violations under this
// repo's policy and would silently invalidate any case that used them.
//
// This suite is expected to be RED for some scanners today -- that is its
// purpose. Nothing here is marked todo/skip.

const policy = loadPolicy()

const SCANNERS = [
  { name: 'spacing', scan: scanSpacing, good: 'p-[var(--space-2)]', bad: 'p-[7px]' },
  { name: 'typography', scan: scanTypography, good: 'text-[length:var(--type-body-compact-size)]', bad: 'text-[10px]' },
  { name: 'ts-colors', scan: scanTsColors, good: 'text-[var(--color-muted)]', bad: 'text-red-500' },
]

const MAIN_PATH = 'src/components/Probe.tsx'

// A finite dispatcher whose every branch proves the target property absent
// via an explicit null prototype -- the shape the fixed scanners require
// before trusting an "absent" proof (mirrors ts-colors.mjs::absenceValue).
const DISPATCHER_ABSENT_NULL_PROTO =
  "function getConfig(s){ switch(s){ case 'a': return { __proto__: null }; default: return { __proto__: null } } }\n"

// The same dispatcher shape but with NO null prototype on its absent
// branches -- absence here must not be trusted without it.
const DISPATCHER_ABSENT_NO_NULL_PROTO =
  "function getConfig(s){ switch(s){ case 'a': return { label: 'A' }; default: return { label: 'B' } } }\n"

function dispatcherPresent(good) {
  return (
    "function getConfig(s){ switch(s){ case 'a': return { textClass: '" +
    good +
    "' }; default: return { textClass: '" +
    good +
    "' } } }\n"
  )
}

// Each case is { id, build(good, bad) -> { path, source, modules } }.
// `build` returns the scan() input for a single scanner invocation; `modules`
// is always supplied (Object.freeze'd), matching how the scanners' own repro
// scripts and audit orchestrator invoke scan().
const CASES = [
  {
    id: 'property written onto a helper result after the call',
    build: (good, bad) => ({
      source:
        DISPATCHER_ABSENT_NULL_PROTO +
        "export function V({status}){ const cfg = getConfig(status); cfg.textClass = '" +
        bad +
        "'; return <div className={cfg.textClass}/> }",
    }),
  },
  {
    id: 'Object.assign onto a helper result after the call',
    build: (good, bad) => ({
      source:
        DISPATCHER_ABSENT_NULL_PROTO +
        "export function V({status}){ const cfg = getConfig(status); Object.assign(cfg, { textClass: '" +
        bad +
        "' }); return <div className={cfg.textClass}/> }",
    }),
  },
  {
    id: 'delete then reassign a helper result property after the call',
    build: (good, bad) => ({
      source:
        dispatcherPresent(good) +
        "export function V({status}){ const cfg = getConfig(status); delete cfg.textClass; cfg.textClass = '" +
        bad +
        "'; return <div className={cfg.textClass}/> }",
    }),
  },
  {
    id: 'Object.defineProperty onto a helper result after the call',
    build: (good, bad) => ({
      source:
        DISPATCHER_ABSENT_NULL_PROTO +
        "export function V({status}){ const cfg = getConfig(status); Object.defineProperty(cfg, 'textClass', { value: '" +
        bad +
        "' }); return <div className={cfg.textClass}/> }",
    }),
  },
  {
    id: 'Reflect.set onto a helper result after the call',
    build: (good, bad) => ({
      source:
        DISPATCHER_ABSENT_NULL_PROTO +
        "export function V({status}){ const cfg = getConfig(status); Reflect.set(cfg, 'textClass', '" +
        bad +
        "'); return <div className={cfg.textClass}/> }",
    }),
  },
  {
    id: 'an alias write onto a helper result after the call',
    build: (good, bad) => ({
      source:
        DISPATCHER_ABSENT_NULL_PROTO +
        "export function V({status}){ const cfg = getConfig(status); const alias = cfg; alias.textClass = '" +
        bad +
        "'; return <div className={cfg.textClass}/> }",
    }),
  },
  {
    id: 'a write in a nested callback/closure onto a helper result',
    build: (good, bad) => ({
      source:
        DISPATCHER_ABSENT_NULL_PROTO +
        'function run(fn){ fn() }\n' +
        "export function V({status}){ const cfg = getConfig(status); run(() => { cfg.textClass = '" +
        bad +
        "' }); return <div className={cfg.textClass}/> }",
    }),
  },
  {
    id: 'reassignment of a helper-result binding inside a function',
    build: (good, bad) => ({
      source:
        DISPATCHER_ABSENT_NULL_PROTO +
        "export function V({status}){ let cfg = getConfig(status); cfg = { textClass: '" +
        bad +
        "' }; return <div className={cfg.textClass}/> }",
    }),
  },
  {
    id: 'absence proof without __proto__: null must not be trusted',
    build: () => ({
      source: DISPATCHER_ABSENT_NO_NULL_PROTO + "export function V({status}){ const cfg = getConfig(status); return <div className={cfg.textClass}/> }",
    }),
  },
  {
    id: 'a reassigned dispatcher function is not trusted by name lookup alone',
    build: (good, bad) => ({
      source:
        'function getConfig(status) {\n' +
        "  switch (status) { case 'a': return { __proto__: null }; default: return { __proto__: null } }\n" +
        '}\n' +
        "getConfig = function altGetConfig(status) { return { textClass: '" +
        bad +
        "' } }\n" +
        'export function Component({ status }) {\n' +
        '  const cfg = getConfig(status)\n' +
        '  return <div className={cfg.textClass} />\n' +
        '}',
    }),
  },
  {
    id: 'a local const record mutated after declaration',
    build: (good, bad) => ({
      source: "const SIZES = { small: '" + good + "' }\nSIZES.small = '" + bad + "'\nexport function V(){ return <i className={SIZES.small}/> }",
    }),
  },
  {
    id: 'Object.assign on a local const record after declaration',
    build: (good, bad) => ({
      source:
        "const SIZES = { small: '" +
        good +
        "' }\nObject.assign(SIZES, { small: '" +
        bad +
        "' })\nexport function V(){ return <i className={SIZES.small}/> }",
    }),
  },
  {
    id: 'an imported record mutated by the importer',
    build: (good, bad) => {
      const sizesPath = 'src/lib/sizes.ts'
      const sizesSource = "export const SIZES = { small: '" + good + "' }"
      const consumerSource =
        "import { SIZES } from '../lib/sizes'\nSIZES.small = '" + bad + "'\nexport function V(){ return <i className={SIZES.small}/> }"
      return {
        source: consumerSource,
        modules: Object.freeze({ [sizesPath]: sizesSource, [MAIN_PATH]: consumerSource }),
      }
    },
  },
  {
    id: 'export let records are not trusted like a const literal',
    build: (good, bad) => ({
      source: "export let SIZES = { small: '" + bad + "' }\nexport function V(){ return <i className={SIZES.small}/> }",
    }),
  },
  {
    id: 'destructuring from an object with a spread from an opaque parameter',
    build: (good) => ({
      source:
        "function f(o){ return { cls: '" +
        good +
        "', ...o } }\nexport function V({o}){ const { cls } = f(o); return <i className={cls}/> }",
    }),
  },
  {
    id: 'member access on the same spread object',
    build: (good) => ({
      source:
        "function f(o){ return { cls: '" + good + "', ...o } }\nexport function V({o}){ const c = f(o); return <i className={c.cls}/> }",
    }),
  },
  {
    id: 'a self-recursive helper must not throw',
    build: (good, bad) => ({
      source: "function cls(n){ return n > 0 ? cls(n - 1) : '" + bad + "' }\nexport function V(){ return <i className={cls(3)}/> }",
    }),
  },
  {
    id: 'mutual recursion must not throw',
    build: (good, bad) => ({
      source:
        "function clsA(n){ return n > 0 ? clsB(n - 1) : '" +
        bad +
        "' }\nfunction clsB(n){ return n > 0 ? clsA(n - 1) : '" +
        bad +
        "' }\nexport function V(){ return <i className={clsA(4)}/> }",
    }),
  },
  {
    id: 'an opaque left operand of ?? falls back to a real violation',
    build: (good, bad) => ({
      source: "export function V({opaque}){ const cls = opaque ?? '" + bad + "'; return <i className={cls}/> }",
    }),
  },
  {
    id: 'an opaque left operand of || falls back to a real violation',
    build: (good, bad) => ({
      source: "export function V({opaque}){ const cls = opaque || '" + bad + "'; return <i className={cls}/> }",
    }),
  },
  {
    id: 'a name-matched class-like parameter with an unknown value (literal default)',
    build: (good, bad) => ({
      source: "export function V({ widthClass = '" + bad + "' }){ return <div className={widthClass}/> }",
    }),
  },
  {
    id: 'a template with an unknown interpolation carries a real static violation',
    build: (good, bad) => ({
      source: 'export function V({c}){ return <div className={`' + bad + ' ${c}`}/> }',
    }),
  },
  {
    id: 'a nested helper parameter frame still surfaces the real value',
    build: (good, bad) => ({
      source:
        'function helper(param){ return middle(param) }\nfunction middle(param){ return param }\n' +
        "export function V(){ return <div className={helper('" +
        bad +
        "')}/> }",
    }),
  },
  {
    id: 'a missing module in the modules context fails closed, not silently clean',
    // The exporting file (which really would contain the BAD value, e.g.
    // `export const SIZES = { small: '<bad>' }`) is deliberately NOT included
    // in `modules` below -- the scanner can only see the consumer file,
    // matching a real cross-module read where the exporting file wasn't part
    // of the scanned batch.
    build: () => {
      const consumerSource = "import { SIZES } from '../lib/sizes-missing'\nexport function V(){ return <i className={SIZES.small}/> }"
      return {
        source: consumerSource,
        modules: Object.freeze({ [MAIN_PATH]: consumerSource }),
      }
    },
  },
]

function runScan(scan, input) {
  try {
    return { findings: scan(input), threw: null }
  } catch (error) {
    return { findings: null, threw: error }
  }
}

function assertNoCrash(scannerName, caseId, result) {
  if (result.threw) {
    assert.fail(scannerName + ': ' + caseId + ' -- scan() threw ' + result.threw.constructor.name + ': ' + result.threw.message)
  }
  assert.ok(Array.isArray(result.findings), scannerName + ': ' + caseId + ' -- scan() did not return an array')
  for (const finding of result.findings) {
    assert.equal(typeof finding.ruleId, 'string', scannerName + ': ' + caseId + ' -- a finding is malformed (not a crash-shaped object)')
    assert.equal(typeof finding.path, 'string', scannerName + ': ' + caseId + ' -- a finding is malformed (not a crash-shaped object)')
    assert.equal(typeof finding.syntax, 'string', scannerName + ': ' + caseId + ' -- a finding is malformed (not a crash-shaped object)')
  }
}

for (const { name: scannerName, scan, good, bad } of SCANNERS) {
  describe(scannerName, () => {
    it('CONTROL: the GOOD value alone reports zero findings', () => {
      const source = "export function V(){ return <div className='" + good + "'/> }"
      const result = runScan(scan, { path: MAIN_PATH, source, policy, modules: Object.freeze({ [MAIN_PATH]: source }) })
      assertNoCrash(scannerName, 'CONTROL good-alone', result)
      assert.deepEqual(
        result.findings,
        [],
        scannerName + ': CONTROL good-alone -- the GOOD fixture value itself is flagged; the case is invalid, not a scanner pass',
      )
    })

    it('CONTROL: the BAD value written directly as a literal reports at least one finding', () => {
      const source = "export function V(){ return <div className='" + bad + "'/> }"
      const result = runScan(scan, { path: MAIN_PATH, source, policy, modules: Object.freeze({ [MAIN_PATH]: source }) })
      assertNoCrash(scannerName, 'CONTROL bad-literal', result)
      assert.ok(
        result.findings.length > 0,
        scannerName + ': CONTROL bad-literal -- the BAD fixture value written as a plain literal is invisible; the case is invalid, not a scanner pass',
      )
    })

    for (const { id: caseId, build } of CASES) {
      it(caseId, () => {
        const built = build(good, bad)
        const path = MAIN_PATH
        const modules = built.modules || Object.freeze({ [path]: built.source })
        const result = runScan(scan, { path, source: built.source, policy, modules })
        assertNoCrash(scannerName, caseId, result)
        assert.ok(result.findings.length > 0, scannerName + ': ' + caseId + ' returned zero findings')
      })
    }
  })
}
