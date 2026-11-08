// @vitest-environment node
// R-CHK / FR-036: exact same tool names in the server and approval cards.
// This is a cross-language consistency invariant, not a second handwritten list.
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import { ALWAYS_ASK_TOOLS, isAlwaysAskTool } from './alwaysAskTools'

function goAlwaysAskNames(source: string): string[] {
  // Keep quoted literals intact while blanking comments. A commented-out old
  // declaration must never satisfy the check for the live server declaration.
  const active = source.replace(
    /"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`[^`]*`|\/\/[^\n]*|\/\*[\s\S]*?\*\//g,
    token => token.startsWith('//') || token.startsWith('/*') ? token.replace(/[^\n]/g, ' ') : token,
  )
  const declarations = [...active.matchAll(/^var\s+AlwaysAskTools\s*=\s*\[\]string\s*\{([^}]*?)\}[ \t]*(?:\n|$)/gm)]
  if (declarations.length !== 1) throw new Error('expected one active AlwaysAskTools string-slice literal')
  const parsed: unknown = JSON.parse(`[${declarations[0][1].trim().replace(/,\s*$/, '')}]`)
  if (!Array.isArray(parsed) || !parsed.every(value => typeof value === 'string')) {
    throw new Error('AlwaysAskTools must contain only string names')
  }
  return parsed as string[]
}

const sameNames = (left: readonly string[], right: readonly string[]) =>
  JSON.stringify([...left].sort()) === JSON.stringify([...right].sort())

describe('Q7 always-ask server/card parity', () => {
  it('the actual TypeScript list equals the actual Go AlwaysAskTools list', () => {
    const here = dirname(fileURLToPath(import.meta.url))
    const server = goAlwaysAskNames(readFileSync(resolve(here, '../../pkg/tools/always_ask.go'), 'utf8'))
    expect(server.length, 'instrument: an empty extraction cannot certify parity').toBeGreaterThan(0)
    expect(new Set(server).size, 'no duplicated server marks').toBe(server.length)
    expect(new Set(ALWAYS_ASK_TOOLS).size, 'no duplicated card marks').toBe(ALWAYS_ASK_TOOLS.length)
    expect([...ALWAYS_ASK_TOOLS].sort()).toEqual([...server].sort())
    expect(sameNames(ALWAYS_ASK_TOOLS, server)).toBe(true)
  })

  it('FR-036 pins the specified tool and exact-name matching', () => {
    expect(ALWAYS_ASK_TOOLS).toEqual(['set_channel_outbound'])
    expect(isAlwaysAskTool('set_channel_outbound')).toBe(true)
    for (const nearMiss of ['Set_channel_outbound', 'set_channel_outbound_extra', 'mcp_set_channel_outbound', '']) {
      expect(isAlwaysAskTool(nearMiss), nearMiss).toBe(false)
    }
  })

  it('the instrument detects an added/removed mark and ignores stale commented declarations', () => {
    const one = 'var AlwaysAskTools = []string{"set_channel_outbound"}\n'
    const two = 'var AlwaysAskTools = []string{\n"set_channel_outbound", // first\n"second_tool",\n}\n'
    expect(goAlwaysAskNames(one)).toEqual(['set_channel_outbound'])
    expect(goAlwaysAskNames(two)).toEqual(['set_channel_outbound', 'second_tool'])
    expect(sameNames(goAlwaysAskNames(one), goAlwaysAskNames(two))).toBe(false)
    expect(sameNames(goAlwaysAskNames(two), goAlwaysAskNames(one))).toBe(false)
    expect(goAlwaysAskNames(`// ${one}/* ${one} */\n${two}`)).toEqual(['set_channel_outbound', 'second_tool'])
    expect(() => goAlwaysAskNames(`/* ${one} */`)).toThrow('expected one active AlwaysAskTools string-slice literal')
    expect(() => goAlwaysAskNames(`${one.trim()} + extra\n`)).toThrow('expected one active AlwaysAskTools string-slice literal')
  })
})
