import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { cpSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { validateCatalog } from './catalog.mjs'

const fixtureRoots = []
after(() => {
  for (const root of fixtureRoots) rmSync(root, { recursive: true, force: true })
})

function fixture() {
  const directory = mkdtempSync(resolve('dist/design-system-baseline/catalog-'))
  fixtureRoots.push(directory)
  cpSync('design-system/catalog.json', resolve(directory, 'design-system/catalog.json'), { recursive: true })
  cpSync('design-system/surfaces.json', resolve(directory, 'design-system/surfaces.json'), { recursive: true })
  cpSync('design-system/manifests', resolve(directory, 'design-system/manifests'), { recursive: true })
  cpSync('src', resolve(directory, 'src'), { recursive: true })
  return directory
}

function mutateJson(directory, file, mutation) {
  const path = resolve(directory, file)
  const value = JSON.parse(readFileSync(path, 'utf8'))
  mutation(value)
  writeFileSync(path, JSON.stringify(value))
}

test('current catalog closes every source, export, and discovered surface boundary', () => {
  const structuralErrors = validateCatalog().filter((error) =>
    !error.startsWith('planned verification gap:'))
  assert.deepEqual(structuralErrors, [])
})

test('rejects direct declarations and TypeScript parse failures in the public entry', () => {
  const direct = fixture()
  writeFileSync(resolve(direct, 'src/index.ts'), 'export const AppThing = 1\n')
  assert.match(validateCatalog(direct).join('\n'), /only named re-exports/)
  const broken = fixture()
  writeFileSync(resolve(broken, 'src/index.ts'), 'export {')
  assert.match(validateCatalog(broken).join('\n'), /TypeScript parse failure/)
})

test('rejects malformed top-level catalog shapes without throwing', () => {
  const directory = fixture()
  writeFileSync(resolve(directory, 'design-system/catalog.json'), JSON.stringify({ version: 1, classifications: 'primitive', entries: 'wrong' }))
  assert.match(validateCatalog(directory).join('\n'), /classifications and entries arrays/)
})

test('enforces the catalog and surface JSON schemas before semantic traversal', () => {
  const directory = fixture()
  mutateJson(directory, 'design-system/catalog.json', (catalog) => { delete catalog.entries[0].exports })
  mutateJson(directory, 'design-system/surfaces.json', (inventory) => { delete inventory.surfaces[0].entry.path })
  const errors = validateCatalog(directory).join('\n')
  assert.match(errors, /catalog schema \/entries\/0 must have required property 'exports'/)
  assert.match(errors, /surfaces schema \/surfaces\/0\/entry must have required property 'path'/)
})

test('rejects a missing UI source classification', () => {
  const directory = fixture()
  mutateJson(directory, 'design-system/catalog.json', (catalog) => catalog.entries.shift())
  assert.match(validateCatalog(directory).join('\n'), /uncataloged ui source/)
})

test('rejects a missing TypeScript UI classification and an omitted module export', () => {
  const directory = fixture()
  mutateJson(directory, 'design-system/catalog.json', (catalog) => {
    catalog.entries = catalog.entries.filter((entry) => entry.source !== 'src/components/ui/channel-logo.ts')
    const button = catalog.entries.find((entry) => entry.source === 'src/components/ui/button.tsx')
    button.exports = button.exports.filter((name) => name !== 'ButtonProps')
  })
  const errors = validateCatalog(directory).join('\n')
  assert.match(errors, /uncataloged ui source: src\/components\/ui\/channel-logo\.ts/)
  assert.match(errors, /uncataloged module export: src\/components\/ui\/button\.tsx#ButtonProps/)
})

test('rejects aliased and double-quoted public exports omitted from the catalog', () => {
  const directory = fixture()
  writeFileSync(resolve(directory, 'src/index.ts'), 'export { Button as RenamedButton } from "./components/ui/button"\n')
  assert.match(validateCatalog(directory).join('\n'), /uncataloged public export: .*#RenamedButton/)
})

test('rejects unknown schema keys and invalid classifications', () => {
  const directory = fixture()
  mutateJson(directory, 'design-system/catalog.json', (catalog) => {
    catalog.entries[0].classification = 'miscellaneous'
    catalog.entries[0].surprise = true
  })
  assert.match(validateCatalog(directory).join('\n'), /unknown catalog entry key: surprise[\s\S]*unclassified source/)
})

test('rejects a missing inline surface and a non-concrete verification mapping', () => {
  const directory = fixture()
  mutateJson(directory, 'design-system/surfaces.json', (inventory) => {
    const inline = inventory.surfaces.findIndex((surface) => surface.id.includes('#overlay:'))
    inventory.surfaces.splice(inline, 1)
    inventory.surfaces[0].verification = ['browser']
  })
  const errors = validateCatalog(directory).join('\n')
  assert.match(errors, /invalid verification mapping/)
  assert.match(errors, /missing discovered surface: .*#overlay:/)
})

test('rejects an unowned TypeScript helper and an omitted dynamic tab family', () => {
  const directory = fixture()
  mutateJson(directory, 'design-system/surfaces.json', (inventory) => {
    inventory.sourceOwners = inventory.sourceOwners.filter((owner) => owner.source !== 'src/components/calendar/types.ts')
    inventory.surfaces = inventory.surfaces.filter((surface) => surface.id !== 'src/components/library/search/LibrarySearchBar.tsx#dynamic-tabs')
  })
  const errors = validateCatalog(directory).join('\n')
  assert.match(errors, /missing application source owner: src\/components\/calendar\/types\.ts/)
  assert.match(errors, /missing discovered surface: src\/components\/library\/search\/LibrarySearchBar\.tsx#dynamic-tabs/)
})

test('rejects missing and duplicate verification kinds required by a surface', () => {
  const directory = fixture()
  mutateJson(directory, 'design-system/surfaces.json', (inventory) => {
    const route = inventory.surfaces.find((surface) => surface.kind === 'route')
    route.verification = route.verification.filter((check) => check.kind !== 'reflow')
    const tab = inventory.surfaces.find((surface) => surface.kind === 'tab')
    tab.verification.push({ ...tab.verification[0], checkId: `${tab.verification[0].checkId}:duplicate` })
  })
  const errors = validateCatalog(directory).join('\n')
  assert.match(errors, /missing reflow verification:/)
  assert.match(errors, /duplicate surface verification kind:/)
})

test('rejects a manifest that silently omits a required check kind', () => {
  const directory = fixture()
  const manifest = resolve(directory, 'design-system/manifests/dialog.json')
  const value = JSON.parse(readFileSync(manifest, 'utf8'))
  value.checks = value.checks.filter((check) => check.kind !== 'keyboard')
  writeFileSync(manifest, JSON.stringify(value))
  assert.match(validateCatalog(directory).join('\n'), /manifest Dialog omits required check kind: keyboard/)
})
