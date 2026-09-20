import { describe, it, expect } from 'vitest'
import { readFileSync } from 'fs'
import { resolve } from 'path'

// Read the CSS file content once.
const cssPath = resolve(process.cwd(), 'src/styles/globals.css')
const cssContent = readFileSync(cssPath, 'utf-8')

// globals.css states each brand colour as a reference to the registered
// primitive token rather than a literal hex, so the brand value has to be
// resolved through that one level of indirection before it can be compared.
const tokensContent = readFileSync(resolve(process.cwd(), 'src/styles/tokens.generated.css'), 'utf-8')

function declaredValue(css: string, name: string): string | null {
  const match = css.match(new RegExp(`${name}\\s*:\\s*([^;]+);`))
  return match ? match[1].trim() : null
}

function resolveColor(name: string): string | null {
  const declared = declaredValue(cssContent, name)
  if (declared === null) return null
  const reference = declared.match(/^var\(\s*(--[A-Za-z0-9-]+)\s*\)$/)
  return reference ? declaredValue(tokensContent, reference[1]) : declared
}

// test_brand_colors_defined
// Traces to: wave0-brand-design-spec.md Scenario: Brand color tokens are available (US-1 AC1, FR-001)
describe('Brand Color Tokens — globals.css @theme', () => {
  const colorTokens: Array<{ name: string; hex: string; label: string }> = [
    // Dataset: Brand Color Tokens row 1
    { name: '--color-primary', hex: '#0a0a0b', label: 'Deep Space Black' },
    // Dataset: Brand Color Tokens row 2
    { name: '--color-secondary', hex: '#e2e8f0', label: 'Liquid Silver' },
    // Dataset: Brand Color Tokens row 3
    { name: '--color-accent', hex: '#d4af37', label: 'Forge Gold' },
    // Dataset: Brand Color Tokens row 4
    { name: '--color-success', hex: '#10b981', label: 'Emerald' },
    // Dataset: Brand Color Tokens row 5
    { name: '--color-error', hex: '#ef4444', label: 'Ruby' },
  ]

  it.each(colorTokens)(
    'CSS token $name ($label) resolves to $hex',
    ({ name, hex }) => {
      // Verify the variable is declared AND that the value it resolves to is
      // the brand hex — not merely that both strings appear in the file.
      expect(cssContent).toContain(name)
      expect(resolveColor(name)?.toLowerCase()).toBe(hex.toLowerCase())
    }
  )

  it('all 5 brand color tokens are defined in @theme block', () => {
    const themeMatch = cssContent.match(/@theme\s*\{([^}]+)\}/s)
    expect(themeMatch).not.toBeNull()
    const themeBlock = themeMatch![1]

    expect(themeBlock).toContain('--color-primary')
    expect(themeBlock).toContain('--color-secondary')
    expect(themeBlock).toContain('--color-accent')
    expect(themeBlock).toContain('--color-success')
    expect(themeBlock).toContain('--color-error')
  })
})

// test_font_families_defined
// Traces to: wave0-brand-design-spec.md Scenario: Typography font families resolve (US-1 AC2, FR-002)
describe('Font Family Tokens — globals.css @theme', () => {
  it('--font-headline uses Outfit with system fallbacks', () => {
    expect(cssContent).toContain('--font-headline')
    expect(cssContent).toContain('Outfit')
    // Fallback stack verification
    expect(cssContent).toMatch(/--font-headline.*Outfit.*sans-serif/s)
  })

  it('--font-body uses Inter with system fallbacks', () => {
    expect(cssContent).toContain('--font-body')
    expect(cssContent).toContain('Inter')
    expect(cssContent).toMatch(/--font-body.*Inter.*sans-serif/s)
  })

  it('--font-mono uses JetBrains Mono with monospace fallback', () => {
    expect(cssContent).toContain('--font-mono')
    expect(cssContent).toContain('JetBrains Mono')
    expect(cssContent).toMatch(/--font-mono.*JetBrains Mono.*monospace/s)
  })
})

// test_package_json_structure
// Traces to: wave0-brand-design-spec.md Scenario: Package.json is correctly configured (US-8 AC1, FR-019)
describe('Package.json structure — @omnipus/ui', () => {
  const pkgPath = resolve(process.cwd(), 'package.json')
  const pkg = JSON.parse(readFileSync(pkgPath, 'utf-8'))

  it('package name is @omnipus/ui', () => {
    expect(pkg.name).toBe('@omnipus/ui')
  })

  it('type is "module" (ESM)', () => {
    expect(pkg.type).toBe('module')
  })

  it('has main, module, and types entry points', () => {
    expect(pkg.main).toBeTruthy()
    expect(pkg.module).toBeTruthy()
    expect(pkg.types).toBeTruthy()
  })

  it('has peerDependencies for react and react-dom', () => {
    expect(pkg.peerDependencies).toBeDefined()
    expect(pkg.peerDependencies['react']).toBeTruthy()
    expect(pkg.peerDependencies['react-dom']).toBeTruthy()
  })

  it('React version is ^19.x', () => {
    expect(pkg.peerDependencies['react']).toMatch(/\^19/)
  })

  // Traces to: wave0-brand-design-spec.md Scenario: Required dependencies are present (US-8 AC5)
  it('has all required Omnipus dependencies', () => {
    const allDeps = { ...pkg.dependencies, ...pkg.devDependencies }

    expect(allDeps['@tanstack/react-router']).toBeTruthy()
    expect(allDeps['zustand']).toBeTruthy()
    expect(allDeps['@phosphor-icons/react']).toBeTruthy()
    expect(allDeps['framer-motion']).toBeTruthy()
    expect(allDeps['tailwindcss']).toBeTruthy()
    expect(allDeps['vite']).toBeTruthy()
  })

  it('uses Zustand (not Jotai) for state management (FR-024)', () => {
    const allDeps = { ...pkg.dependencies, ...pkg.devDependencies }
    expect(allDeps['zustand']).toBeTruthy()
    expect(allDeps['jotai']).toBeUndefined()
  })
})
