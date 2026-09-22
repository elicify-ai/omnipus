#!/usr/bin/env node

import { createHash } from 'node:crypto'
import { mkdir, readFile, readdir, realpath, writeFile } from 'node:fs/promises'
import { dirname, isAbsolute, relative, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { gzipSync } from 'node:zlib'
import { init, parse } from 'es-module-lexer'

const STORYBOOK_PATH = /(?:^|\/)(?:\.storybook|storybook-static)(?:\/|$)|\.stories\.[^/]+$/i
const STORYBOOK_MODULE = /(?:@storybook\/|storybook\/internal\/|__STORYBOOK_[A-Z_]+__)/
const HTML_ASSET = /<(script|link)\b[^>]*?(?:src|href)=["']([^"']+)["'][^>]*>/gi

function cssImports(source) {
  const imports = []
  let depth = 0
  let quote = null
  for (let index = 0; index < source.length; index += 1) {
    const char = source[index]
    if (quote) {
      if (char === '\\') index += 1
      else if (char === quote) quote = null
      continue
    }
    if (char === '"' || char === "'") { quote = char; continue }
    if (char === '/' && source[index + 1] === '*') {
      const end = source.indexOf('*/', index + 2)
      index = end === -1 ? source.length : end + 1
      continue
    }
    if (char === '{') { depth += 1; continue }
    if (char === '}') { depth = Math.max(0, depth - 1); continue }
    if (depth !== 0 || source.slice(index, index + 7).toLowerCase() !== '@import') continue
    const end = source.indexOf(';', index + 7)
    if (end === -1) throw new Error('unterminated CSS @import rule')
    const rule = source.slice(index + 7, end).trim()
    const match = rule.match(/^(?:url\(\s*)?(?:"([^"]+)"|'([^']+)'|([^\s)'";]+))\s*\)?/i)
    if (!match) throw new Error(`unsupported CSS @import rule: @import ${rule};`)
    imports.push(match[1] ?? match[2] ?? match[3])
    index = end
  }
  return imports
}

function normalizeReference(reference, importer, root) {
  if (/^(?:[a-z]+:)?\/\//i.test(reference) || reference.startsWith('data:')) return null
  const clean = decodeURIComponent(reference.split(/[?#]/, 1)[0])
  const target = clean.startsWith('/')
    ? resolve(root, `.${clean}`)
    : resolve(dirname(importer), clean)
  const rel = relative(root, target)
  if (rel === '..' || rel.startsWith(`..${sep}`) || isAbsolute(rel)) {
    throw new Error(`initial asset reference escapes distribution root: ${reference}`)
  }
  return rel.split(sep).join('/')
}

async function walk(root, directory = root) {
  const output = []
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const absolute = resolve(directory, entry.name)
    if (entry.isSymbolicLink()) throw new Error(`distribution contains unsupported symlink: ${relative(root, absolute)}`)
    if (entry.isDirectory()) output.push(...await walk(root, absolute))
    else if (entry.isFile()) output.push(relative(root, absolute).split(sep).join('/'))
  }
  return output.sort()
}

function bytesByType(assets, paths, extension) {
  const selected = paths.map((path) => assets.get(path)).filter((asset) => extension.test(asset.path))
  return {
    files: selected.length,
    rawBytes: selected.reduce((sum, asset) => sum + asset.rawBytes, 0),
    gzipBytes: selected.reduce((sum, asset) => sum + asset.gzipBytes, 0),
  }
}

async function collectInitialFiles(root, allPaths) {
  if (!allPaths.includes('index.html')) throw new Error(`missing production entry: ${resolve(root, 'index.html')}`)
  const pending = []
  const initial = new Set()
  const html = await readFile(resolve(root, 'index.html'), 'utf8')
  for (const match of html.matchAll(HTML_ASSET)) {
    const path = normalizeReference(match[2], resolve(root, 'index.html'), root)
    if (path && /\.(?:css|m?js)$/i.test(path)) pending.push(path)
  }

  while (pending.length > 0) {
    const path = pending.shift()
    if (initial.has(path)) continue
    if (!allPaths.includes(path)) throw new Error(`missing initial asset referenced by production entry: ${path}`)
    initial.add(path)
    const source = await readFile(resolve(root, path), 'utf8')
    let references = []
    if (/\.m?js$/i.test(path)) {
      await init
      references = parse(source)[0].filter((entry) => entry.type === 'static' && entry.specifier).map((entry) => entry.specifier)
    } else if (/\.css$/i.test(path)) {
      references = cssImports(source)
    }
    for (const reference of references) {
      const dependency = normalizeReference(reference, resolve(root, path), root)
      if (dependency && /\.(?:css|m?js)$/i.test(dependency)) pending.push(dependency)
    }
  }
  return [...initial].sort()
}

export async function measureProductionBundle({ dist, revision = null }) {
  if (!dist) throw new Error('measureProductionBundle requires a distribution directory')
  const root = await realpath(resolve(dist))
  const paths = await walk(root)
  const assetMap = new Map()
  const contentMatches = []

  for (const path of paths) {
    const body = await readFile(resolve(root, path))
    const asset = {
      path,
      rawBytes: body.byteLength,
      gzipBytes: gzipSync(body, { level: 9 }).byteLength,
      sha256: createHash('sha256').update(body).digest('hex'),
    }
    assetMap.set(path, asset)
    if (/\.(?:html|css|m?js|map|json)$/i.test(path)) {
      const source = body.toString('utf8')
      const evidence = source.match(STORYBOOK_MODULE)
      if (evidence) contentMatches.push({ path, evidence: evidence[0] })
    }
  }

  const initialFiles = await collectInitialFiles(root, paths)
  const assets = [...assetMap.values()]
  const outputPathMatches = paths.filter((path) => STORYBOOK_PATH.test(path))
  const totalRaw = assets.reduce((sum, asset) => sum + asset.rawBytes, 0)
  const totalGzip = assets.reduce((sum, asset) => sum + asset.gzipBytes, 0)

  return {
    schemaVersion: 1,
    generatedAt: new Date().toISOString(),
    revision,
    distribution: root,
    compression: { initial: 'sum of per-file gzip level 9 bytes', totalBudget: 'raw bytes' },
    initial: {
      files: initialFiles,
      javascript: bytesByType(assetMap, initialFiles, /\.m?js$/i),
      css: bytesByType(assetMap, initialFiles, /\.css$/i),
      gzipBytes: initialFiles.reduce((sum, path) => sum + assetMap.get(path).gzipBytes, 0),
    },
    total: { files: assets.length, rawBytes: totalRaw, gzipBytes: totalGzip },
    storybook: {
      clean: outputPathMatches.length === 0 && contentMatches.length === 0,
      outputPathMatches,
      contentMatches,
      limitation: 'Output scanning is supporting evidence; module provenance must be audited separately.',
    },
    assets,
  }
}

const BOOLEAN_FLAGS = new Set(['force'])

function parseArgs(argv) {
  const args = {}
  for (let index = 0; index < argv.length; index += 1) {
    const key = argv[index]
    if (!key.startsWith('--')) throw new Error(`invalid argument: ${key}`)
    if (BOOLEAN_FLAGS.has(key.slice(2))) {
      args[key.slice(2)] = true
      continue
    }
    if (index + 1 >= argv.length) throw new Error(`invalid argument: ${key}`)
    args[key.slice(2)] = argv[index += 1]
  }
  return args
}

async function main() {
  const args = parseArgs(process.argv.slice(2))
  if (!args.dist || !args.output) {
    throw new Error('usage: bundle-measure.mjs --dist <directory> --output <report.json> [--revision <git-sha>] [--force]')
  }
  const report = await measureProductionBundle({ dist: args.dist, revision: args.revision ?? null })
  const output = resolve(args.output)
  await mkdir(dirname(output), { recursive: true })
  try {
    await writeFile(output, `${JSON.stringify(report, null, 2)}\n`, { flag: args.force ? 'w' : 'wx' })
  } catch (error) {
    if (error?.code === 'EEXIST' && !args.force) {
      throw new Error(`output report already exists: ${output} (pass --force to replace it deliberately)`, { cause: error })
    }
    throw error
  }
  process.stdout.write(`${JSON.stringify({ output, initialGzipBytes: report.initial.gzipBytes, totalRawBytes: report.total.rawBytes, storybookClean: report.storybook.clean })}\n`)
  if (!report.storybook.clean) process.exitCode = 1
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  main().catch((error) => {
    process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`)
    process.exitCode = 1
  })
}
