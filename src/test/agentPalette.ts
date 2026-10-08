// Identity colours for tests.
//
// SPEC "Locked identity vocabulary" (agent-first-navigation-spec), ordered
// Azure → Grey. The values are the generated AgentColor enum — the wire
// palette — not a second list of hex literals copied into each test.
// `satisfies` checks each entry is one of those ten colours.

import type { Agent } from '@/lib/api'
import { AgentColor } from '@/lib/api/generated/schemas'

type PaletteColor = NonNullable<Agent['color']>

const generated = AgentColor.options

function at(index: number): PaletteColor {
  const value = generated[index]
  if (value === undefined) {
    throw new Error(`AgentColor.options[${index}] is missing — the palette has ten colours`)
  }
  return value as PaletteColor
}

// Index order is the contract enum order, which is the spec table order.
export const AZURE = at(0)
export const SKY = at(1)
export const CYAN = at(2)
export const INDIGO = at(3)
export const VIOLET = at(4)
export const PURPLE = at(5)
export const FUCHSIA = at(6)
export const PINK = at(7)
export const ORANGE = at(8)
export const GREY = at(9)

const _orderCheck = [
  AZURE, SKY, CYAN, INDIGO, VIOLET, PURPLE, FUCHSIA, PINK, ORANGE, GREY,
] as const satisfies readonly PaletteColor[]

void _orderCheck
