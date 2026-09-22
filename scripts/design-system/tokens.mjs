#!/usr/bin/env node

import { readFile, writeFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const LAYERS = ['primitive', 'semantic', 'component']
const KINDS = [
  'color', 'dimension', 'number', 'duration', 'fontFamily', 'fontWeight',
  'lineHeight', 'shadow', 'easing', 'string',
]
const ID_PATTERN = /^[a-zA-Z][a-zA-Z0-9-]*(\.[a-zA-Z0-9-]+)+$/
const CSS_PATTERN = /^--[a-zA-Z][a-zA-Z0-9-]*$/
const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const DEFAULT_SOURCES = [
  path.join(ROOT, 'design-system/tokens/colors.json'),
  path.join(ROOT, 'design-system/tokens/foundations.json'),
]
const OUTPUTS = {
  css: path.join(ROOT, 'src/styles/tokens.generated.css'),
  theme: path.join(ROOT, 'src/styles/tokens.theme.generated.css'),
  typescript: path.join(ROOT, 'src/design-system/tokens.ts'),
}

function isObject(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function requireNonEmptyString(value, diagnostic) {
  if (typeof value !== 'string' || value.length === 0) throw new Error(diagnostic)
}

function validateMetadataArray(value, diagnostic) {
  if (!Array.isArray(value) || value.length === 0 || value.some((item) => typeof item !== 'string' || item.length === 0)) {
    throw new Error(diagnostic)
  }
  if (new Set(value).size !== value.length) throw new Error(`${diagnostic}; values must be unique`)
}

function detectCycle(tokensById) {
  const visited = new Set()
  const active = new Set()
  const trail = []
  function visit(id) {
    if (active.has(id)) {
      const start = trail.indexOf(id)
      throw new Error(`token graph cycle: ${[...trail.slice(start), id].join(' -> ')}`)
    }
    if (visited.has(id)) return
    active.add(id)
    trail.push(id)
    const token = tokensById.get(id)
    if (token && 'ref' in token && tokensById.has(token.ref)) visit(token.ref)
    trail.pop()
    active.delete(id)
    visited.add(id)
  }
  for (const id of [...tokensById.keys()].sort()) visit(id)
}

export function validateTokenSources(sources) {
  if (!Array.isArray(sources) || sources.length === 0) throw new Error('at least one token source is required')
  const tokensById = new Map()
  const locationsById = new Map()
  const locationsByCss = new Map()

  sources.forEach((source, sourceIndex) => {
    const sourcePath = `source[${sourceIndex}]`
    if (!isObject(source)) throw new Error(`${sourcePath} must be an object`)
    if (source.version !== 1) throw new Error(`${sourcePath}.version must be 1`)
    if (!Array.isArray(source.tokens)) throw new Error(`${sourcePath}.tokens must be an array`)
    const unexpectedSourceProperty = Object.keys(source).find((key) => key !== 'version' && key !== 'tokens')
    if (unexpectedSourceProperty) throw new Error(`${sourcePath} has unsupported property "${unexpectedSourceProperty}"`)
    source.tokens.forEach((token, tokenIndex) => {
      const tokenPath = `${sourcePath}.tokens[${tokenIndex}]`
      if (!isObject(token)) throw new Error(`${tokenPath} must be an object`)
      if (typeof token.id !== 'string' || !ID_PATTERN.test(token.id)) throw new Error(`${tokenPath}.id must be a dot-separated stable key`)
      if (typeof token.css !== 'string' || !CSS_PATTERN.test(token.css)) throw new Error(`${tokenPath}.css must be a CSS custom property beginning with --`)
      if (!LAYERS.includes(token.layer)) throw new Error(`${tokenPath}.layer must be primitive, semantic, or component`)
      if (!KINDS.includes(token.kind)) throw new Error(`${tokenPath}.kind is unsupported: ${String(token.kind)}`)
      const hasValue = Object.hasOwn(token, 'value')
      const hasRef = Object.hasOwn(token, 'ref')
      if (hasValue === hasRef) throw new Error(`${tokenPath} must define exactly one of value or ref`)
      if (hasValue && typeof token.value !== 'string' && typeof token.value !== 'number') throw new Error(`${tokenPath}.value must be a CSS string or number`)
      if (hasRef) requireNonEmptyString(token.ref, `${tokenPath}.ref must be a non-empty token id`)

      const allowedKeys = new Set(['id', 'css', 'layer', 'kind', 'value', 'ref', 'description', 'owner', 'purpose', 'states', 'consumers', 'responsive'])
      const unexpected = Object.keys(token).find((key) => !allowedKeys.has(key))
      if (unexpected) throw new Error(`${tokenPath} has unsupported property "${unexpected}"`)
      if (token.responsive !== undefined && typeof token.responsive !== 'boolean') throw new Error(`${tokenPath}.responsive must be boolean`)
      for (const key of ['description', 'owner', 'purpose']) {
        if (token[key] !== undefined) requireNonEmptyString(token[key], `${tokenPath}.${key} must be a non-empty string`)
      }
      for (const key of ['states', 'consumers']) {
        if (token[key] !== undefined) validateMetadataArray(token[key], `${tokenPath}.${key} must be a non-empty array of strings`)
      }

      if (token.layer === 'primitive' && !hasValue) throw new Error(`primitive token "${token.id}" must define a raw value`)
      if (token.layer === 'primitive' && typeof token.value === 'string' && token.value.trim().length === 0) throw new Error(`primitive token "${token.id}" has an empty CSS value`)
      if (token.layer === 'primitive' && typeof token.value === 'number' && !Number.isFinite(token.value)) throw new Error(`primitive token "${token.id}" has a non-finite numeric value`)
      if (token.layer === 'primitive' && typeof token.value === 'string' && /var\s*\(/i.test(token.value)) {
        throw new Error(`primitive token "${token.id}" must not contain var(); use ref for graph dependencies`)
      }
      if (token.layer !== 'primitive' && !hasRef) throw new Error(`${token.layer} token "${token.id}" must reference a lower-layer token`)
      if (token.layer === 'component') {
        requireNonEmptyString(token.owner, `component token "${token.id}" requires non-empty owner metadata`)
        requireNonEmptyString(token.purpose, `component token "${token.id}" requires non-empty purpose metadata`)
        validateMetadataArray(token.states, `component token "${token.id}" requires non-empty states metadata`)
        validateMetadataArray(token.consumers, `component token "${token.id}" requires non-empty consumers metadata`)
      }
      if (tokensById.has(token.id)) throw new Error(`duplicate token id "${token.id}" at ${tokenPath}; first declared at ${locationsById.get(token.id)}`)
      if (locationsByCss.has(token.css)) throw new Error(`duplicate CSS custom property "${token.css}" at ${tokenPath}; first declared at ${locationsByCss.get(token.css)}`)
      tokensById.set(token.id, token)
      locationsById.set(token.id, tokenPath)
      locationsByCss.set(token.css, tokenPath)
    })
  })

  detectCycle(tokensById)
  for (const token of tokensById.values()) {
    if (!('ref' in token)) continue
    const target = tokensById.get(token.ref)
    if (!target) throw new Error(`token "${token.id}" references undefined token "${token.ref}"`)
    if (token.kind !== target.kind) throw new Error(`token "${token.id}" (${token.kind}) cannot reference "${target.id}" (${target.kind})`)
    if (LAYERS.indexOf(target.layer) >= LAYERS.indexOf(token.layer)) {
      throw new Error(`token "${token.id}" in ${token.layer} layer must reference a lower layer; "${target.id}" is ${target.layer}`)
    }
  }

  const resolved = {}
  function resolve(id) {
    if (Object.hasOwn(resolved, id)) return resolved[id]
    const token = tokensById.get(id)
    const value = 'value' in token ? token.value : resolve(token.ref)
    resolved[id] = value
    return value
  }
  for (const id of [...tokensById.keys()].sort()) resolve(id)
  return { tokens: [...tokensById.values()].sort((a, b) => a.id.localeCompare(b.id)), resolved }
}

export function generateArtifacts(sources) {
  const { tokens: sortedTokens, resolved } = validateTokenSources(sources)
  const cssById = new Map(sortedTokens.map((token) => [token.id, token.css]))
  const cssLines = [...sortedTokens]
    .sort((a, b) => a.css.localeCompare(b.css))
    .map((token) => `  ${token.css}: ${'value' in token ? token.value : `var(${cssById.get(token.ref)})`};`)
  const publicTokens = sortedTokens.filter((token) => token.layer !== 'primitive')
  const themeLines = sortedTokens
    .filter((token) => token.layer === 'semantic' && token.kind === 'color')
    .sort((a, b) => a.css.localeCompare(b.css))
    .map((token) => `  ${token.css}: var(${cssById.get(token.ref)});`)
  const tokenLines = publicTokens.map((token) => `  ${JSON.stringify(token.id)}: ${JSON.stringify(`var(${token.css})`)},`)
  const resolvedLines = Object.entries(resolved)
    .sort(([first], [second]) => first.localeCompare(second))
    .map(([id, value]) => `  ${JSON.stringify(id)}: ${JSON.stringify(value)},`)
  return {
    css: `/* Generated by scripts/design-system/tokens.mjs. Do not edit. */\n:root {\n${cssLines.join('\n')}\n}\n`,
    theme: `/* Generated by scripts/design-system/tokens.mjs. Do not edit. */\n@theme inline {\n${themeLines.join('\n')}\n}\n`,
    typescript: `// Generated by scripts/design-system/tokens.mjs. Do not edit.\nexport const tokens = {\n${tokenLines.join('\n')}\n} as const\n\nexport const resolvedTokens = {\n${resolvedLines.join('\n')}\n} as const\n\nexport type TokenId = keyof typeof resolvedTokens\n`,
  }
}

async function readSource(sourcePath, allowMissingFoundations) {
  try {
    return JSON.parse(await readFile(sourcePath, 'utf8'))
  } catch (error) {
    if (error?.code === 'ENOENT' && allowMissingFoundations && sourcePath.endsWith('/foundations.json')) return null
    if (error instanceof SyntaxError) throw new Error(`invalid JSON in ${sourcePath}: ${error.message}`, { cause: error })
    throw error
  }
}

export async function runGenerator({ check = false, allowMissingFoundations = false } = {}) {
  const sources = (await Promise.all(DEFAULT_SOURCES.map((sourcePath) => readSource(sourcePath, allowMissingFoundations)))).filter(Boolean)
  const artifacts = generateArtifacts(sources)
  const drift = []
  for (const [kind, outputPath] of Object.entries(OUTPUTS)) {
    if (check) {
      let current
      try {
        current = await readFile(outputPath, 'utf8')
      } catch (error) {
        if (error?.code !== 'ENOENT') throw error
      }
      if (current !== artifacts[kind]) drift.push(path.relative(ROOT, outputPath))
    } else {
      await writeFile(outputPath, artifacts[kind])
    }
  }
  if (drift.length > 0) throw new Error(`generated token artifacts are stale: ${drift.join(', ')}; run node scripts/design-system/tokens.mjs`)
  return artifacts
}

async function main() {
  const known = new Set(['--check', '--allow-missing-foundations'])
  const unknown = process.argv.slice(2).find((argument) => !known.has(argument))
  if (unknown) throw new Error(`unknown option: ${unknown}`)
  await runGenerator({ check: process.argv.includes('--check'), allowMissingFoundations: process.argv.includes('--allow-missing-foundations') })
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error) => {
    console.error(`token generation failed: ${error.message}`)
    process.exitCode = 1
  })
}
