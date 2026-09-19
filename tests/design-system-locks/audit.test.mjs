import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import test, { after } from 'node:test'
import { fileURLToPath } from 'node:url'
import {
  SCANNER_FILES,
  SOURCE_EXTENSIONS,
  audit,
  composeCoverage,
  runCli,
} from '../../scripts/design-system-locks/audit.mjs'

const repoRoot = resolve(fileURLToPath(new URL('../..', import.meta.url)))
const auditScript = resolve(repoRoot, 'scripts/design-system-locks/audit.mjs')
const fixtureRoots = []

after(() => {
  for (const directory of fixtureRoots) rmSync(directory, { recursive: true, force: true })
})

function fingerprint(ruleId, path, syntax) {
  return createHash('sha256').update(JSON.stringify([ruleId, path, syntax])).digest('hex')
}

function fixture() {
  const directory = mkdtempSync(resolve(repoRoot, 'dist/design-system-baseline/cli-lanes/audit-'))
  fixtureRoots.push(directory)
  mkdirSync(resolve(directory, 'src'), { recursive: true })
  return directory
}

function write(root, rel, content) {
  const path = resolve(root, rel)
  mkdirSync(dirname(path), { recursive: true })
  writeFileSync(path, content)
  return rel
}

function triple(ruleId, path, syntax, extra = {}) {
  return { ruleId, path, syntax, fingerprint: fingerprint(ruleId, path, syntax), ...extra }
}

function baselineDoc(items) {
  return {
    version: 1,
    fingerprints: items.map((item) => ({
      fingerprint: item.fingerprint,
      ruleId: item.ruleId,
      path: item.path,
      syntax: item.syntax,
      count: item.count ?? 1,
    })),
  }
}

function ledgerDoc(items, extras = {}) {
  const document = {
    version: 1,
    entries: items.map((item) => ({
      fingerprint: item.fingerprint,
      ruleId: item.ruleId,
      path: item.path,
      syntax: item.syntax,
      owner: item.owner ?? 'lane-audit',
      replacement: item.replacement ?? 'replace with the registered token',
      expiryCheckpoint: item.expiryCheckpoint ?? 'C1',
    })),
    exceptions: extras.exceptions ?? [],
  }
  if (extras.reviewedBoundaries) document.reviewedBoundaries = extras.reviewedBoundaries
  return document
}

function finding(ruleId, path, syntax, message = 'forbidden syntax') {
  return { ruleId, path, syntax, message }
}

function scanner(extensions, scan) {
  return { extensions, scan, label: `fixture-${extensions.join('-')}` }
}

const defaultPolicy = {
  tokenCssNames: ['--color-accent'],
  resolvedTokens: { 'color.accent': '#D4AF37' },
}

function codes(report) {
  return report.errors.map((error) => error.code)
}

async function run(root, { files = {}, scanners, baseline, ledger, checkpoint = 'B', policy = defaultPolicy, extraCli = [] } = {}) {
  for (const [rel, content] of Object.entries(files)) write(root, rel, content)
  const reportPath = resolve(root, 'report.json')
  const baselinePath = resolve(root, 'baseline.json')
  const ledgerPath = resolve(root, 'ledger.json')
  writeFileSync(baselinePath, JSON.stringify(baseline ?? baselineDoc([])))
  writeFileSync(ledgerPath, JSON.stringify(ledger ?? ledgerDoc([])))
  const silent = { write() {} }
  const code = await runCli(
    ['--root', root, '--baseline', baselinePath, '--ledger', ledgerPath, '--checkpoint', checkpoint, '--report', reportPath, ...extraCli],
    { scanners, policy, stderr: silent },
  )
  return { code, report: JSON.parse(readFileSync(reportPath, 'utf8')), reportPath }
}

test('importing audit.mjs does not execute scanner modules', () => {
  const result = spawnSync(process.execPath, ['-e', 'import("./scripts/design-system-locks/audit.mjs")'], {
    cwd: repoRoot,
    encoding: 'utf8',
  })
  assert.equal(result.status, 0, result.stderr)
  const source = readFileSync(auditScript, 'utf8')
  assert.equal(/^(?:import|export)[\s\S]*?from\s+['"]\.\/(?:css-colors|ts-colors|typography|spacing|controls|status|coverage)\.mjs['"]/m.test(source), false)
  assert.equal(SCANNER_FILES.includes('coverage.mjs'), false)
  assert.deepEqual([...SCANNER_FILES], [
    'css-colors.mjs',
    'ts-colors.mjs',
    'typography.mjs',
    'spacing.mjs',
    'controls.mjs',
    'status.mjs',
  ])
})

test('CLI exits nonzero when baseline, ledger, checkpoint, or report is omitted', async () => {
  const root = fixture()
  write(root, 'src/ok.tsx', 'export const ok = true\n')
  const silent = { write() {} }
  for (const missing of ['baseline', 'ledger', 'checkpoint', 'report']) {
    const args = [
      '--root', root,
      '--baseline', resolve(root, 'baseline.json'),
      '--ledger', resolve(root, 'ledger.json'),
      '--checkpoint', 'B',
      '--report', resolve(root, 'report.json'),
    ].filter((token, index, all) => token !== `--${missing}` && all[index - 1] !== `--${missing}`)
    const code = await runCli(args, { scanners: [scanner(['.tsx'], () => [])], stderr: silent })
    assert.equal(code, 1, `expected nonzero when --${missing} is omitted`)
  }
})

test('CLI spawn without scanners fails closed and writes the JSON report', () => {
  const root = fixture()
  write(root, 'src/ok.tsx', 'export const ok = true\n')
  write(root, 'baseline.json', JSON.stringify(baselineDoc([])))
  write(root, 'ledger.json', JSON.stringify(ledgerDoc([])))
  write(root, 'policy.json', JSON.stringify(defaultPolicy))
  const emptyScanners = resolve(root, 'scanners')
  mkdirSync(emptyScanners, { recursive: true })
  const reportPath = resolve(root, 'report.json')
  const result = spawnSync(process.execPath, [
    auditScript,
    '--root', root,
    '--baseline', resolve(root, 'baseline.json'),
    '--ledger', resolve(root, 'ledger.json'),
    '--checkpoint', 'B',
    '--report', reportPath,
    '--policy', resolve(root, 'policy.json'),
    '--scanner-dir', emptyScanners,
  ], { cwd: repoRoot, encoding: 'utf8' })
  assert.notEqual(result.status, 0)
  const report = JSON.parse(readFileSync(reportPath, 'utf8'))
  assert.equal(report.ok, false)
  assert.ok(codes(report).includes('scanner-not-ready'))
  assert.equal(report.errors.some((error) => error.message.includes('css-colors.mjs')), true)
})

test('schema rejects a baseline missing fingerprints and a ledger entry missing owner', async () => {
  const root = fixture()
  write(root, 'src/ok.tsx', 'export const ok = true\n')
  const { report: baselineReport } = await run(root, {
    scanners: [scanner(['.tsx'], () => [])],
    baseline: { version: 1 },
    ledger: ledgerDoc([]),
  })
  assert.ok(codes(baselineReport).includes('schema'))
  assert.match(baselineReport.errors.map((error) => error.message).join('\n'), /baseline schema .*fingerprints/)

  const root2 = fixture()
  write(root2, 'src/ok.tsx', 'export const ok = true\n')
  const item = triple('css-colors.raw-hex', 'src/ok.tsx', '#ff0000')
  const { report: ledgerReport } = await run(root2, {
    scanners: [scanner(['.tsx'], () => [])],
    baseline: baselineDoc([item]),
    ledger: {
      version: 1,
      entries: [{ fingerprint: item.fingerprint, ruleId: item.ruleId, path: item.path, syntax: item.syntax, replacement: 'token', expiryCheckpoint: 'C1' }],
      exceptions: [],
    },
  })
  assert.ok(codes(ledgerReport).includes('schema'))
  assert.match(ledgerReport.errors.map((error) => error.message).join('\n'), /must have required property 'owner'/)
})

test('permitted fixture with no findings and empty baseline passes', async () => {
  const root = fixture()
  const { code, report } = await run(root, {
    files: { 'src/ok.tsx': 'export const color = "var(--color-accent)"\n' },
    scanners: [scanner(SOURCE_EXTENSIONS, () => [])],
  })
  assert.equal(code, 0)
  assert.equal(report.ok, true)
  assert.deepEqual(report.errors, [])
  assert.equal(report.scannedFileCount, 1)
})

test('forbidden finding not in the baseline is new debt and exits nonzero', async () => {
  const root = fixture()
  const { code, report } = await run(root, {
    files: { 'src/bad.tsx': 'const color = "#ff0000"\n' },
    scanners: [scanner(['.tsx'], ({ path }) => [finding('css-colors.raw-hex', path, '#ff0000')])],
  })
  assert.equal(code, 1)
  assert.deepEqual(codes(report), ['new-debt'])
  assert.match(report.errors[0].message, /new debt css-colors\.raw-hex src\/bad\.tsx #ff0000/)
  assert.equal(report.errors[0].syntax, '#ff0000')
  assert.equal(report.errors[0].path, 'src/bad.tsx')
})

test('matching baseline count with an owned unexpired ledger entry is allowed', async () => {
  const item = triple('css-colors.raw-hex', 'src/bad.tsx', '#ff0000')
  const root = fixture()
  const { code, report } = await run(root, {
    files: { 'src/bad.tsx': 'const color = "#ff0000"\n' },
    scanners: [scanner(['.tsx'], ({ path }) => [finding('css-colors.raw-hex', path, '#ff0000')])],
    baseline: baselineDoc([item]),
    ledger: ledgerDoc([item]),
    checkpoint: 'B',
  })
  assert.equal(code, 0, JSON.stringify(report.errors))
  assert.equal(report.ok, true)
  assert.equal(report.debt.acceptedCount, 1)
  assert.equal(report.debt.fingerprints[0].count, 1)
  assert.equal(report.debt.perRule['css-colors.raw-hex'], 1)
})

test('increased occurrence count is rejected; decrements are allowed', async () => {
  const item = triple('css-colors.raw-hex', 'src/bad.tsx', '#ff0000', { count: 1 })
  const increased = fixture()
  const up = await run(increased, {
    files: { 'src/bad.tsx': 'const a = "#ff0000"; const b = "#ff0000"\n' },
    scanners: [scanner(['.tsx'], ({ path }) => [
      finding('css-colors.raw-hex', path, '#ff0000', 'first'),
      finding('css-colors.raw-hex', path, '#ff0000', 'second'),
    ])],
    baseline: baselineDoc([item]),
    ledger: ledgerDoc([item]),
  })
  assert.equal(up.code, 1)
  assert.deepEqual(codes(up.report), ['increased-count'])
  assert.match(up.report.errors[0].message, /increased count .* 1 -> 2/)
  assert.equal(up.report.errors[0].count, 2)
  assert.equal(up.report.errors[0].baselineCount, 1)

  const two = triple('css-colors.raw-hex', 'src/bad.tsx', '#ff0000', { count: 2 })
  const decreased = fixture()
  const down = await run(decreased, {
    files: { 'src/bad.tsx': 'const a = "#ff0000"\n' },
    scanners: [scanner(['.tsx'], ({ path }) => [finding('css-colors.raw-hex', path, '#ff0000')])],
    baseline: baselineDoc([two]),
    ledger: ledgerDoc([two]),
  })
  assert.equal(down.code, 0, JSON.stringify(down.report.errors))
})

test('line shifts do not change the fingerprint', async () => {
  const item = triple('css-colors.raw-hex', 'src/bad.tsx', '#ff0000')
  const root = fixture()
  const { code, report } = await run(root, {
    files: { 'src/bad.tsx': '\n\nconst color = "#ff0000"\n' },
    scanners: [scanner(['.tsx'], ({ path }) => [{ ...finding('css-colors.raw-hex', path, '#ff0000'), line: 3, column: 16 }])],
    baseline: baselineDoc([item]),
    ledger: ledgerDoc([item]),
  })
  assert.equal(code, 0, JSON.stringify(report.errors))
})

test('expired and currently-due ledger entries fail; later expiry passes', async () => {
  const item = triple('css-colors.raw-hex', 'src/bad.tsx', '#ff0000')
  const scanners = [scanner(['.tsx'], ({ path }) => [finding('css-colors.raw-hex', path, '#ff0000')])]
  const files = { 'src/bad.tsx': 'const color = "#ff0000"\n' }

  const expired = await run(fixture(), {
    files, scanners, baseline: baselineDoc([item]), ledger: ledgerDoc([{ ...item, expiryCheckpoint: 'A' }]), checkpoint: 'B',
  })
  assert.ok(codes(expired.report).includes('expired-ledger'))

  const due = await run(fixture(), {
    files, scanners, baseline: baselineDoc([item]), ledger: ledgerDoc([{ ...item, expiryCheckpoint: 'B' }]), checkpoint: 'B',
  })
  assert.ok(codes(due.report).includes('expired-ledger'))

  const later = await run(fixture(), {
    files, scanners, baseline: baselineDoc([item]), ledger: ledgerDoc([{ ...item, expiryCheckpoint: 'C3' }]), checkpoint: 'B',
  })
  assert.equal(later.code, 0, JSON.stringify(later.report.errors))
})

test('missing, unknown, and duplicate ledger or baseline fingerprints fail', async () => {
  const item = triple('css-colors.raw-hex', 'src/bad.tsx', '#ff0000')
  const other = triple('css-colors.raw-hex', 'src/other.tsx', '#00ff00')
  const scanners = [scanner(['.tsx'], () => [])]
  const files = { 'src/bad.tsx': 'export {}\n' }

  const missing = await run(fixture(), { files, scanners, baseline: baselineDoc([item]), ledger: ledgerDoc([]) })
  assert.ok(codes(missing.report).includes('missing-ledger'))

  const unknown = await run(fixture(), { files, scanners, baseline: baselineDoc([]), ledger: ledgerDoc([item]) })
  assert.ok(codes(unknown.report).includes('unknown-ledger'))

  const duplicateLedger = await run(fixture(), {
    files, scanners, baseline: baselineDoc([item]), ledger: ledgerDoc([item, item]),
  })
  assert.ok(codes(duplicateLedger.report).includes('duplicate-ledger'))

  const duplicateBaseline = await run(fixture(), {
    files,
    scanners,
    baseline: { version: 1, fingerprints: [...baselineDoc([item]).fingerprints, ...baselineDoc([item]).fingerprints] },
    ledger: ledgerDoc([item]),
  })
  assert.ok(codes(duplicateBaseline.report).includes('duplicate-baseline'))

  const mismatch = await run(fixture(), {
    files,
    scanners,
    baseline: baselineDoc([{ ...item, fingerprint: other.fingerprint }]),
    ledger: ledgerDoc([item]),
  })
  assert.ok(codes(mismatch.report).includes('fingerprint-mismatch'))
})

test('permanent exceptions match exact path/rule/syntax and reject blanket directories', async () => {
  // Uses a registrable ruleId (css-colors/extension-boundary) throughout: the
  // point of this test is exact-triple matching and blanket-directory
  // rejection, not the separate registrable-ruleId gate covered by
  // 'a ledger exception naming a non-registrable ruleId fails closed' below.
  const root = fixture()
  mkdirSync(resolve(root, 'src/components'), { recursive: true })
  const allowed = await run(root, {
    files: { 'src/qr.tsx': 'const ink = "#000000"\n' },
    scanners: [scanner(['.tsx'], ({ path }) => [finding('css-colors/extension-boundary', path, '#000000')])],
    ledger: ledgerDoc([], { exceptions: [{ ruleId: 'css-colors/extension-boundary', path: 'src/qr.tsx', syntax: '#000000', reason: 'QR quiet zone' }] }),
  })
  assert.equal(allowed.code, 0, JSON.stringify(allowed.report.errors))

  const differentSyntax = await run(fixture(), {
    files: { 'src/qr.tsx': 'const ink = "#111111"\n' },
    scanners: [scanner(['.tsx'], ({ path }) => [finding('css-colors/extension-boundary', path, '#111111')])],
    ledger: ledgerDoc([], { exceptions: [{ ruleId: 'css-colors/extension-boundary', path: 'src/qr.tsx', syntax: '#000000', reason: 'QR quiet zone' }] }),
  })
  assert.ok(codes(differentSyntax.report).includes('new-debt'))

  const blanket = await run(fixture(), {
    files: { 'src/components/x.tsx': 'export {}\n' },
    scanners: [scanner(['.tsx'], () => [])],
    ledger: ledgerDoc([], { exceptions: [{ ruleId: 'css-colors/extension-boundary', path: 'src/components', syntax: '#000000', reason: 'too broad' }] }),
  })
  assert.ok(codes(blanket.report).includes('blanket-directory'))
})

test('a ledger exception naming a non-registrable ruleId fails closed and is never applied', async () => {
  // This is the reviewed HIGH finding: a hand-edited ledger.json exception
  // for an ordinary debt rule (e.g. css-colors/raw-color) used to silently
  // suppress the finding because accountFindings only ever checked
  // infrastructureKind() before consulting ledger.exceptions — never whether
  // the exception's ruleId was itself registrable. Only a "*/extension-boundary"
  // ruleId or exactly "ts-colors/unverified-governed-value" may be a ledger
  // exception (scripts/design-system-locks/audit.mjs::isAllowedExceptionRuleId); anything
  // else must block the audit AND must not suppress the finding.
  const root = fixture()
  const { code, report } = await run(root, {
    files: { 'src/foo.tsx': 'const ink = "#ff0000"\n' },
    scanners: [scanner(['.tsx'], ({ path }) => [finding('css-colors/raw-color', path, '#ff0000')])],
    ledger: ledgerDoc([], { exceptions: [{ ruleId: 'css-colors/raw-color', path: 'src/foo.tsx', syntax: '#ff0000', reason: 'hand-edited exception' }] }),
  })
  assert.equal(code, 1)
  assert.equal(report.ok, false)
  assert.ok(codes(report).includes('unregistrable-exception'), JSON.stringify(report.errors))
  const gate = report.errors.find((error) => error.code === 'unregistrable-exception')
  assert.match(gate.message, /css-colors\/raw-color/)
  assert.match(gate.message, /src\/foo\.tsx/)
  assert.equal(gate.ruleId, 'css-colors/raw-color')
  assert.equal(gate.path, 'src/foo.tsx')
  // The finding itself must still surface as new debt — the exception was
  // never applied, not merely reported as invalid alongside a silent pass.
  assert.ok(codes(report).includes('new-debt'), JSON.stringify(report.errors))
  assert.deepEqual(report.appliedExceptions, [])
})

test('a legitimate extension-boundary exception still applies once the registrable-ruleId gate is in place', async () => {
  const root = fixture()
  const { code, report } = await run(root, {
    files: { 'src/foo.tsx': 'const ink = "var(--color-accent)"\n' },
    scanners: [scanner(['.tsx'], ({ path }) => [finding('ts-colors/extension-boundary', path, 'DomStyle#backgroundColor<-selectedColor')])],
    ledger: ledgerDoc([], {
      exceptions: [{ ruleId: 'ts-colors/extension-boundary', path: 'src/foo.tsx', syntax: 'DomStyle#backgroundColor<-selectedColor', reason: 'user-authored colour', owner: 'lane-audit' }],
    }),
  })
  assert.equal(code, 0, JSON.stringify(report.errors))
  assert.equal(report.ok, true)
  assert.deepEqual(report.errors, [])
  assert.deepEqual(report.appliedExceptions, [
    { ruleId: 'ts-colors/extension-boundary', path: 'src/foo.tsx', syntax: 'DomStyle#backgroundColor<-selectedColor' },
  ])
})

test('parse, unsupported, and unclassified parse findings cannot be baselined away', async () => {
  const parseItem = triple('css-colors.parse-failure', 'src/bad.tsx', 'const x =')
  const parseScan = await run(fixture(), {
    files: { 'src/bad.tsx': 'const x =\n' },
    scanners: [scanner(['.tsx'], ({ path }) => [finding('css-colors.parse-failure', path, 'const x =', 'parse failure')])],
    baseline: baselineDoc([parseItem]),
    ledger: ledgerDoc([parseItem]),
  })
  assert.ok(codes(parseScan.report).includes('parse-failure'))
  assert.equal(parseScan.code, 1)

  const unsupportedItem = triple('ts-colors.unsupported', 'src/bad.tsx', 'theme[name]')
  const unsupported = await run(fixture(), {
    files: { 'src/bad.tsx': 'const color = theme[name]\n' },
    scanners: [scanner(['.tsx'], ({ path }) => [finding('ts-colors.unsupported', path, 'theme[name]', 'dynamic color')])],
    baseline: baselineDoc([unsupportedItem]),
    ledger: ledgerDoc([unsupportedItem]),
  })
  assert.ok(codes(unsupported.report).includes('unsupported'))

  const raw = await run(fixture(), {
    files: { 'src/bad.tsx': 'const x =\n' },
    scanners: [scanner(['.tsx'], ({ path }) => [finding('css-colors.raw-hex', path, 'const x =', 'failed to parse declaration')])],
  })
  assert.ok(codes(raw.report).includes('unclassified-parse'))
  assert.equal(codes(raw.report).includes('new-debt'), false)
})

test('thrown scanners and malformed output are blocking and not baselineable', async () => {
  const thrown = await run(fixture(), {
    files: { 'src/bad.tsx': 'export {}\n' },
    scanners: [scanner(['.tsx'], () => { throw new Error('boom') })],
  })
  assert.ok(codes(thrown.report).includes('scanner-exception'))
  assert.match(thrown.report.errors[0].message, /boom/)

  const notArray = await run(fixture(), {
    files: { 'src/bad.tsx': 'export {}\n' },
    scanners: [scanner(['.tsx'], () => ({ ruleId: 'x' }))],
  })
  assert.ok(codes(notArray.report).includes('malformed-scanner'))

  const emptySyntax = await run(fixture(), {
    files: { 'src/bad.tsx': 'export {}\n' },
    scanners: [scanner(['.tsx'], ({ path }) => [finding('css-colors.raw-hex', path, '   ')])],
  })
  assert.ok(codes(emptySyntax.report).includes('malformed-scanner'))
})

test('walks css/js/jsx/ts/tsx/svg, skips other types, and reports uncovered svg as unsupported', async () => {
  const seen = []
  const root = fixture()
  const walked = await run(root, {
    files: {
      'src/a.css': '.x{color:red}',
      'src/a.js': 'export default 1',
      'src/a.jsx': 'export default 1',
      'src/a.ts': 'export default 1',
      'src/a.tsx': 'export default 1',
      'src/a.svg': '<svg/>',
      'src/a.md': '# no',
      'src/a.json': '{}',
      'src/note.txt': 'no',
    },
    scanners: [scanner(['.css', '.js', '.jsx', '.ts', '.tsx', '.svg'], ({ path }) => {
      seen.push(path)
      return []
    })],
  })
  assert.equal(walked.code, 0, JSON.stringify(walked.report.errors))
  assert.deepEqual(seen, ['src/a.css', 'src/a.js', 'src/a.jsx', 'src/a.svg', 'src/a.ts', 'src/a.tsx'])
  assert.equal(walked.report.scannedFileCount, 6)

  const gap = await run(fixture(), {
    files: { 'src/icon.svg': '<svg/>', 'src/ok.tsx': 'export {}\n' },
    scanners: [scanner(['.tsx'], () => [])],
  })
  assert.ok(codes(gap.report).includes('unsupported'))
  assert.match(gap.report.errors.map((error) => error.message).join('\n'), /no scanner handles \.svg/)
})

test('unregistered tests are debt; only exact reviewedBoundaries exempt ordinary findings', async () => {
  const unregistered = await run(fixture(), {
    files: {
      'src/button.test.tsx': 'const color = "#ff0000"\n',
      'src/button.stories.tsx': 'const color = "#00ff00"\n',
    },
    scanners: [scanner(['.tsx'], ({ path }) => [finding('css-colors.raw-hex', path, '#ff0000')])],
  })
  assert.equal(unregistered.code, 1)
  assert.deepEqual(
    unregistered.report.errors.filter((error) => error.code === 'new-debt').map((error) => error.path).sort(),
    ['src/button.stories.tsx', 'src/button.test.tsx'],
  )

  const registered = await run(fixture(), {
    files: {
      'src/button.test.tsx': 'const color = "#ff0000"\n',
      'src/live.tsx': 'const color = "var(--color-accent)"\n',
    },
    scanners: [scanner(['.tsx'], ({ path }) => (
      path.endsWith('.test.tsx') ? [finding('css-colors.raw-hex', path, '#ff0000')] : []
    ))],
    ledger: ledgerDoc([], {
      reviewedBoundaries: [{ path: 'src/button.test.tsx', kind: 'test', reason: 'seeded fixture' }],
    }),
  })
  assert.equal(registered.code, 0, JSON.stringify(registered.report.errors))
  assert.equal(registered.report.appliedBoundaries[0].path, 'src/button.test.tsx')
})

test('exact test, story, spec, and canonical generated-token boundaries are accepted', async () => {
  const files = {
    'src/components/Button.test.tsx': 'export const color = "#ff0000"\n',
    'src/components/Button.spec.tsx': 'export const color = "#ff0000"\n',
    'src/components/Button.stories.tsx': 'export const color = "#ff0000"\n',
    'src/styles/tokens.generated.css': ':root { color: #ff0000; }\n',
    'src/styles/tokens.theme.generated.css': ':root { color: #ff0000; }\n',
    'src/design-system/tokens.ts': 'export const color = "#ff0000"\n',
  }
  const { code, report } = await run(fixture(), {
    files,
    scanners: [scanner(['.css', '.ts', '.tsx'], ({ path }) => [finding('css-colors.raw-hex', path, '#ff0000')])],
    ledger: ledgerDoc([], {
      reviewedBoundaries: [
        { path: 'src/components/Button.test.tsx', kind: 'test', reason: 'unit fixture' },
        { path: 'src/components/Button.spec.tsx', kind: 'test', reason: 'spec fixture' },
        { path: 'src/components/Button.stories.tsx', kind: 'story', reason: 'story fixture' },
        { path: 'src/styles/tokens.generated.css', kind: 'generated-tokens', reason: 'canonical css' },
        { path: 'src/styles/tokens.theme.generated.css', kind: 'generated-tokens', reason: 'canonical theme css' },
        { path: 'src/design-system/tokens.ts', kind: 'generated-tokens', reason: 'canonical ts' },
      ],
    }),
  })
  assert.equal(code, 0, JSON.stringify(report.errors))
  assert.deepEqual(
    report.appliedBoundaries.map((item) => item.path).sort(),
    Object.keys(files).sort(),
  )
})

test('disguised, mismatched, and generated-looking reviewed boundaries stay governed', async () => {
  const disguises = [
    ['src/components/App.tsx', 'test'],
    ['src/components/Settings.tsx', 'story'],
    ['src/components/Dashboard.tsx', 'generated-tokens'],
  ]
  const files = Object.fromEntries([
    ...disguises.map(([path]) => [path, 'export const color = "#ff0000"\n']),
    ['src/components/Button.test.tsx', 'export const color = "#ff0000"\n'],
    ['src/components/tokens.generated.ts', 'export const color = "#ff0000"\n'],
  ])
  const { code, report } = await run(fixture(), {
    files,
    scanners: [scanner(['.ts', '.tsx'], ({ path }) => [finding('css-colors.raw-hex', path, '#ff0000')])],
    ledger: ledgerDoc([], {
      reviewedBoundaries: [
        ...disguises.map(([path, kind]) => ({ path, kind, reason: `disguise ${kind}` })),
        { path: 'src/components/Button.test.tsx', kind: 'story', reason: 'kind mismatch' },
        { path: 'src/components/tokens.generated.ts', kind: 'generated-tokens', reason: 'lookalike' },
      ],
    }),
  })
  const invalidPaths = [
    'src/components/App.tsx',
    'src/components/Button.test.tsx',
    'src/components/Dashboard.tsx',
    'src/components/Settings.tsx',
    'src/components/tokens.generated.ts',
  ]
  assert.equal(code, 1)
  assert.deepEqual(
    report.errors.filter((error) => error.code === 'invalid-reviewed-boundary').map((error) => error.path).sort(),
    invalidPaths,
  )
  assert.deepEqual(
    report.errors.filter((error) => error.code === 'new-debt').map((error) => error.path).sort(),
    invalidPaths,
  )
  assert.deepEqual(report.appliedBoundaries, [])
})

test('a valid reviewed boundary still applies when a sibling disguise is rejected', async () => {
  const { code, report } = await run(fixture(), {
    files: {
      'src/components/Button.test.tsx': 'export const color = "#ff0000"\n',
      'src/components/App.tsx': 'export const color = "#ff0000"\n',
    },
    scanners: [scanner(['.tsx'], ({ path }) => [finding('css-colors.raw-hex', path, '#ff0000')])],
    ledger: ledgerDoc([], {
      reviewedBoundaries: [
        { path: 'src/components/Button.test.tsx', kind: 'test', reason: 'legitimate test' },
        { path: 'src/components/App.tsx', kind: 'test', reason: 'disguised application file' },
      ],
    }),
  })
  assert.equal(code, 1)
  assert.deepEqual(
    report.errors.filter((error) => error.code === 'invalid-reviewed-boundary').map((error) => error.path),
    ['src/components/App.tsx'],
  )
  assert.deepEqual(
    report.errors.filter((error) => error.code === 'new-debt').map((error) => error.path),
    ['src/components/App.tsx'],
  )
  assert.deepEqual(report.appliedBoundaries.map((item) => item.path), ['src/components/Button.test.tsx'])
})

test('route filenames containing $ are scanned and accounted', async () => {
  const { code, report } = await run(fixture(), {
    files: { 'src/routes/$workspaceId.tsx': 'const color = "#ff0000"\n' },
    scanners: [scanner(['.tsx'], ({ path }) => [finding('css-colors.raw-hex', path, '#ff0000')])],
  })
  assert.equal(code, 1)
  assert.equal(report.scannedFileCount, 1)
  assert.equal(report.errors[0].path, 'src/routes/$workspaceId.tsx')
  assert.equal(report.errors[0].code, 'new-debt')
})

test('deleted policy fails closed; injected scanners receive the live policy', async () => {
  const deleted = await run(fixture(), {
    files: { 'src/ok.tsx': 'export {}\n' },
    scanners: [scanner(['.tsx'], () => [])],
    policy: { deleted: true },
  })
  assert.ok(codes(deleted.report).includes('deleted-policy'))

  let received
  const live = await run(fixture(), {
    files: { 'src/ok.tsx': 'export {}\n' },
    scanners: [scanner(['.tsx'], (args) => {
      received = args.policy
      return []
    })],
    policy: defaultPolicy,
  })
  assert.equal(live.code, 0)
  assert.deepEqual(received.tokenCssNames, ['--color-accent'])
  assert.equal(received.resolvedTokens['color.accent'], '#D4AF37')
})

test('catalog validation rejects malformed names, subset violations, and unsafe sources', async () => {
  const validEntry = {
    source: 'src/components/ui/button.tsx',
    exports: ['Button', 'ButtonProps'],
    publicExports: ['Button'],
    publicTypes: ['ButtonProps'],
  }
  const invalidCatalogs = [
    { entries: [{ ...validEntry, source: '../outside.tsx' }] },
    { entries: [{ ...validEntry, source: 'src/components/ui/../ui/button.tsx' }] },
    { entries: [{ ...validEntry, publicExports: [42] }] },
    { entries: [{ ...validEntry, publicTypes: [''] }] },
    { entries: [{ ...validEntry, exports: ['Button', 'Button'] }] },
    { entries: [{ ...validEntry, publicExports: ['Missing'] }] },
    { entries: [{ ...validEntry, publicTypes: ['MissingProps'] }] },
  ]
  for (const catalog of invalidCatalogs) {
    const root = fixture()
    write(root, 'src/ok.tsx', 'export {}\n')
    const report = await audit({
      root,
      checkpoint: 'B',
      baseline: baselineDoc([]),
      ledger: ledgerDoc([]),
      policy: defaultPolicy,
      catalog,
      scanners: [{ label: 'controls.mjs', extensions: ['.tsx'], scan: () => [] }],
    })
    assert.deepEqual(codes(report), ['invalid-catalog'], JSON.stringify(catalog))
    assert.equal(report.scannedFileCount, 0)
  }
})

test('catalog validation rejects duplicate normalized source identities', async () => {
  const root = fixture()
  write(root, 'src/ok.tsx', 'export {}\n')
  const entry = { source: 'src/components/ui/button.tsx', exports: ['Button'], publicExports: ['Button'], publicTypes: [] }
  const report = await audit({
    root,
    checkpoint: 'B',
    baseline: baselineDoc([]),
    ledger: ledgerDoc([]),
    policy: defaultPolicy,
    catalog: { entries: [entry, { ...entry, source: 'src/components/ui/button.ts' }] },
    scanners: [{ label: 'controls.mjs', extensions: ['.tsx'], scan: () => [] }],
  })
  assert.deepEqual(codes(report), ['invalid-catalog'])
  assert.equal(report.scannedFileCount, 0)
})

test('valid catalog context remains cloned and deeply frozen for scanners', async () => {
  const root = fixture()
  write(root, 'src/ok.tsx', 'export {}\n')
  const catalog = { entries: [{ source: 'src/components/ui/button.tsx', exports: ['Button'], publicExports: ['Button'], publicTypes: [] }] }
  let received
  const report = await audit({
    root,
    checkpoint: 'B',
    baseline: baselineDoc([]),
    ledger: ledgerDoc([]),
    policy: defaultPolicy,
    catalog,
    scanners: [{ label: 'controls.mjs', extensions: ['.tsx'], scan: ({ catalog: context }) => { received = context; return [] } }],
  })
  assert.deepEqual(report.errors, [])
  assert.notEqual(received, catalog)
  assert.equal(Object.isFrozen(received), true)
  assert.equal(Object.isFrozen(received.entries), true)
  assert.equal(Object.isFrozen(received.entries[0]), true)
  assert.equal(Object.isFrozen(received.entries[0].publicExports), true)
})

test('audit does not compose coverage; composeCoverage is a separate entry', async () => {
  let coverageCalls = 0
  const coverageModule = {
    checkCoverage() {
      coverageCalls += 1
      return { errors: ['coverage should not run from audit()'] }
    },
  }
  const root = fixture()
  write(root, 'src/ok.tsx', 'export {}\n')
  const report = await audit({
    root,
    checkpoint: 'B',
    baseline: baselineDoc([]),
    ledger: ledgerDoc([]),
    policy: defaultPolicy,
    scanners: [scanner(['.tsx'], () => [])],
  })
  assert.equal(report.ok, true)
  assert.equal(coverageCalls, 0)

  const composed = await composeCoverage({
    root,
    indexPath: 'missing.json',
    evidencePaths: [],
    coverageModule,
  })
  assert.deepEqual(composed.errors, ['coverage should not run from audit()'])
  assert.equal(coverageCalls, 1)

  const missing = await composeCoverage({ coverageDir: resolve(root, 'empty-scanners') })
  assert.match(missing.errors.join('\n'), /coverage module not ready/)
})

test('unknown checkpoint and missing src tree fail without dumping the tree', async () => {
  const root = fixture()
  write(root, 'src/ok.tsx', 'export {}\n')
  write(root, 'baseline.json', JSON.stringify(baselineDoc([])))
  write(root, 'ledger.json', JSON.stringify(ledgerDoc([])))
  const reportPath = resolve(root, 'report.json')
  const code = await runCli(
    ['--root', root, '--baseline', resolve(root, 'baseline.json'), '--ledger', resolve(root, 'ledger.json'), '--checkpoint', 'Z', '--report', reportPath],
    { scanners: [scanner(['.tsx'], () => [])], policy: defaultPolicy, stderr: { write() {} } },
  )
  const report = JSON.parse(readFileSync(reportPath, 'utf8'))
  assert.equal(code, 1)
  assert.ok(codes(report).includes('invalid-checkpoint'))
  assert.equal(Object.hasOwn(report, 'files'), false)

  const empty = fixture()
  rmSync(resolve(empty, 'src'), { recursive: true, force: true })
  const missing = await run(empty, { scanners: [scanner(['.tsx'], () => [])] })
  assert.ok(codes(missing.report).includes('missing-source-tree'))
  assert.equal(Object.hasOwn(missing.report, 'files'), false)
})
