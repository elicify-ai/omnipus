#!/usr/bin/env node
import { existsSync } from 'node:fs'
import { readFile, readdir } from 'node:fs/promises'
import { relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'
import Ajv2020 from 'ajv/dist/2020.js'

const manifestSchema = JSON.parse(await readFile(new URL('../../design-system/manifest.schema.json', import.meta.url), 'utf8'))
const validateSchema = new Ajv2020({ allErrors: true }).compile(manifestSchema)

const CHECK_KINDS = new Set(['unit', 'interaction', 'axe', 'keyboard', 'browser', 'pointer', 'reduced-motion', 'forced-colors', 'root-size', 'zoom', 'reflow'])
const PLAYWRIGHT_PROJECTS = ['chromium', 'firefox', 'webkit', 'chromium-coarse-pointer']
const STORYBOOK_PROJECTS = ['chromium', 'firefox', 'webkit']

function parseArgs(argv) {
  const result = {}
  for (let index = 0; index < argv.length; index += 2) {
    const key = argv[index]; const value = argv[index + 1]
    if (!key?.startsWith('--') || value === undefined) throw new Error(`invalid argument: ${key ?? ''}`)
    result[key.slice(2)] = value
  }
  return result
}

const normalizedPath = (path) => path.replaceAll('\\', '/').replace(/^\.\//, '')
function pathMatches(actual, declared) {
  const left = normalizedPath(actual); const right = normalizedPath(declared)
  return resolve(left) === resolve(right)
}

function playwrightResults(report) {
  const results = []
  const reportRoot = report.config?.rootDir ?? process.cwd()
  function visit(suite, inheritedFile = '') {
    const file = suite.file ?? inheritedFile
    for (const spec of suite.specs ?? []) for (const test of spec.tests ?? []) {
      const attempts = test.results ?? []
      results.push({ runner: 'playwright', file: resolve(reportRoot, spec.file ?? file), test: spec.title, project: test.projectName ?? test.projectId,
        pass: test.status === 'expected' && attempts.length > 0 && attempts.every((attempt) => attempt.status === 'passed'), attempts: attempts.map((attempt) => attempt.status) })
    }
    for (const child of suite.suites ?? []) visit(child, file)
  }
  for (const suite of report.suites ?? []) visit(suite)
  return results
}

function vitestProject(source) {
  return source.match(/design-system-storybook-(chromium|firefox|webkit)\.json$/)?.[1] ?? null
}
function vitestResults(report, source) {
  return (report.testResults ?? []).flatMap((file) => (file.assertionResults ?? []).map((assertion) => ({
    runner: 'vitest', file: file.name,
    test: assertion.fullName ?? [...(assertion.ancestorTitles ?? []), assertion.title].join(' '),
    project: vitestProject(source),
    pass: assertion.status === 'passed' && !(assertion.failureMessages?.length > 0), attempts: [assertion.status],
  })))
}

function parseEvidence(report, source) {
  if (Array.isArray(report.suites)) return playwrightResults(report).map((result) => ({ ...result, source }))
  if (Array.isArray(report.testResults)) return vitestResults(report, source).map((result) => ({ ...result, source }))
  throw new Error(`${source}: unsupported evidence report shape`)
}

function validateManifest(manifest, manifestFile) {
  const errors = []
  if (!validateSchema(manifest)) {
    for (const error of validateSchema.errors ?? []) errors.push(`${manifestFile}: schema ${error.instancePath || '/'} ${error.message} ${JSON.stringify(error.params)}`)
  }
  const nonempty = (value) => typeof value === 'string' && value.trim().length > 0
  if (manifest?.version !== 1) errors.push(`${manifestFile}: version must be 1`)
  if (!nonempty(manifest?.component)) errors.push(`${manifestFile}: component must be non-empty`)
  if (!Array.isArray(manifest?.stories) || manifest.stories.length === 0) errors.push(`${manifestFile}: stories must be non-empty`)
  if (!Array.isArray(manifest?.checks) || manifest.checks.length === 0) errors.push(`${manifestFile}: checks must be non-empty`)
  for (const [index, check] of (Array.isArray(manifest?.checks) ? manifest.checks : []).entries()) {
    const prefix = `${manifestFile}: checks[${index}]`
    if (!nonempty(check?.id)) errors.push(`${prefix}.id must be non-empty`)
    if (!CHECK_KINDS.has(check?.kind)) errors.push(`${prefix}.kind is invalid`)
    if (typeof check?.applicable !== 'boolean') errors.push(`${prefix}.applicable must be boolean`)
    if (check?.applicable === true) {
      if (!nonempty(check.file)) errors.push(`${prefix}.file must be non-empty when applicable`)
      if (!nonempty(check.test)) errors.push(`${prefix}.test must be non-empty when applicable`)
      if (check.kind !== 'unit' && !nonempty(check.story)) errors.push(`${prefix}.story must be non-empty for applicable non-unit checks`)
    } else if (check?.applicable === false && !nonempty(check.reason)) errors.push(`${prefix}.reason must be non-empty when not applicable`)
  }
  return errors
}

function executionContract(kind) {
  if (kind === 'unit') return { runner: 'vitest', projects: [] }
  if (kind === 'interaction') return { runner: 'vitest', projects: STORYBOOK_PROJECTS }
  return { runner: 'playwright', projects: PLAYWRIGHT_PROJECTS }
}

async function storyHasExplicitPlay(file, exportName) {
  const source = await readFile(resolve(file), 'utf8')
  const ast = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, file.endsWith('x') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)
  let found = false
  const unwrap = (node) => {
    while (ts.isSatisfiesExpression(node) || ts.isAsExpression(node) || ts.isParenthesizedExpression(node)) node = node.expression
    return node
  }
  function visit(node) {
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === exportName && node.initializer) {
      const initializer = unwrap(node.initializer)
      if (ts.isObjectLiteralExpression(initializer)) found ||= initializer.properties.some((property) => property.name?.getText(ast) === 'play')
    }
    if (ts.isBinaryExpression(node) && ts.isPropertyAccessExpression(node.left) && ts.isIdentifier(node.left.expression) && node.left.expression.text === exportName && node.left.name.text === 'play') found = true
    ts.forEachChild(node, visit)
  }
  visit(ast)
  return found
}

export async function verifyCoverage({ manifestDir, storybookIndex, evidenceFiles = [] }) {
  const files = (await readdir(manifestDir)).filter((file) => file.endsWith('.json')).sort()
  if (files.length === 0) throw new Error('no component manifests found; verification cannot pass empty')
  const manifests = await Promise.all(files.map(async (file) => { const path = resolve(manifestDir, file); return { file: path, value: JSON.parse(await readFile(path, 'utf8')) } }))
  const index = storybookIndex ? JSON.parse(await readFile(storybookIndex, 'utf8')) : null
  const evidence = (await Promise.all(evidenceFiles.map(async (file) => parseEvidence(JSON.parse(await readFile(file, 'utf8')), file)))).flat()
  const errors = manifests.flatMap(({ file, value }) => validateManifest(value, relative(process.cwd(), file)))
  if (errors.length) return { pass: false, manifests: manifests.length, checks: 0, evidenceResults: evidence.length, errors }
  const ids = new Set()
  for (const { value: manifest } of manifests) {
    for (const path of [manifest.source, manifest.documentation, ...(manifest.stories ?? []).map((story) => story.file)]) if (typeof path === 'string' && !existsSync(resolve(path))) errors.push(`${manifest.component}: missing declared file ${path}`)
    for (const story of manifest.stories ?? []) for (const exported of story.exports ?? []) {
      const found = index && Object.values(index.entries ?? {}).some((entry) => entry.type === 'story' && entry.exportName === exported && pathMatches(entry.importPath, story.file))
      if (!found) errors.push(`${manifest.component}: Storybook index missing ${story.file}#${exported}`)
    }
    for (const check of manifest.checks ?? []) {
      if (ids.has(check.id)) errors.push(`${manifest.component}: duplicate check id ${check.id}`)
      ids.add(check.id)
      if (check.applicable !== true || typeof check.file !== 'string' || typeof check.test !== 'string') continue
      if (!existsSync(resolve(check.file))) errors.push(`${manifest.component}/${check.id}: missing test file ${check.file}`)
      if (check.kind !== 'unit') {
        const declaredStory = (manifest.stories ?? []).find((story) => story.exports?.includes(check.story)
          && (check.kind !== 'interaction' || pathMatches(story.file, check.file)))
        const indexedStory = declaredStory && Object.values(index?.entries ?? {}).find((entry) => entry.type === 'story' && entry.exportName === check.story && pathMatches(entry.importPath, declaredStory.file))
        if (!declaredStory || !indexedStory) errors.push(`${manifest.component}/${check.id}: story evidence mapping is not declared in the manifest and Storybook index`)
        else if (check.kind === 'interaction' && indexedStory.name !== check.test) errors.push(`${manifest.component}/${check.id}: test must equal Storybook display name ${indexedStory.name}`)
        if (check.kind === 'interaction' && !(await storyHasExplicitPlay(check.file, check.story))) errors.push(`${manifest.component}/${check.id}: interaction story ${check.story} must declare an explicit play function`)
      }
      const matches = evidence.filter((result) => pathMatches(result.file, check.file) && result.test === check.test)
      if (matches.length === 0) { errors.push(`${manifest.component}/${check.id}: no exact executed evidence for ${check.file}::${check.test}`); continue }
      if (matches.some((result) => !result.pass)) errors.push(`${manifest.component}/${check.id}: evidence contains a failed or retried-failure result`)
      const contract = executionContract(check.kind)
      const incompatibleRunners = [...new Set(matches.filter((result) => result.runner !== contract.runner).map((result) => result.runner))]
      for (const runner of incompatibleRunners) errors.push(`${manifest.component}/${check.id}: evidence runner ${runner} is incompatible with ${check.kind === 'interaction' ? 'interaction' : check.kind === 'unit' ? 'unit' : 'browser-family'} check; expected ${contract.runner}`)
      const compatibleMatches = matches.filter((result) => result.runner === contract.runner)
      for (const project of contract.projects) {
        const projectResults = compatibleMatches.filter((result) => result.project === project)
        if (projectResults.length === 0) errors.push(`${manifest.component}/${check.id}: missing required project ${project}`)
        else if (projectResults.some((result) => !result.pass)) errors.push(`${manifest.component}/${check.id}: project ${project} did not pass cleanly`)
      }
    }
  }
  return { pass: errors.length === 0, manifests: manifests.length, checks: ids.size, evidenceResults: evidence.length, errors }
}

async function main() {
  const args = parseArgs(process.argv.slice(2))
  const report = await verifyCoverage({ manifestDir: resolve(args.manifests ?? 'design-system/manifests'), storybookIndex: args.index ? resolve(args.index) : null,
    evidenceFiles: (args.evidence ?? '').split(',').filter(Boolean).map((file) => resolve(file)) })
  process.stdout.write(`${JSON.stringify(report, null, 2)}\n`); if (!report.pass) process.exitCode = 1
}
if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) main().catch((error) => { process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`); process.exitCode = 1 })
