// Identity colours for tests.
//
// SPEC "Locked identity vocabulary" (agent-first-navigation-spec), ordered
// Azure → Grey. The values are the generated AgentColor enum — the wire
// palette — not a second list of hex literals copied into each test.
//
// Each colour is `AgentColor.options[n]` with a numeric literal, returned
// from its own named function. A variable index (`generated[index]`) is
// ts-colors/unsupported and cannot be baselined. Call these from a named
// function: an anonymous callback that writes the same literal is still
// unsupported.

import type { Agent } from '@/lib/api'
import { AgentColor } from '@/lib/api/generated/schemas'

type PaletteColor = NonNullable<Agent['color']>

export function azure(): PaletteColor {
  return AgentColor.options[0] as PaletteColor
}

export function sky(): PaletteColor {
  return AgentColor.options[1] as PaletteColor
}

export function cyan(): PaletteColor {
  return AgentColor.options[2] as PaletteColor
}

export function indigo(): PaletteColor {
  return AgentColor.options[3] as PaletteColor
}

export function violet(): PaletteColor {
  return AgentColor.options[4] as PaletteColor
}

export function purple(): PaletteColor {
  return AgentColor.options[5] as PaletteColor
}

export function fuchsia(): PaletteColor {
  return AgentColor.options[6] as PaletteColor
}

export function pink(): PaletteColor {
  return AgentColor.options[7] as PaletteColor
}

export function orange(): PaletteColor {
  return AgentColor.options[8] as PaletteColor
}

export function grey(): PaletteColor {
  return AgentColor.options[9] as PaletteColor
}

// Index order is the contract enum order, which is the spec table order.
const _orderCheck = [
  azure(),
  sky(),
  cyan(),
  indigo(),
  violet(),
  purple(),
  fuchsia(),
  pink(),
  orange(),
  grey(),
] as const satisfies readonly PaletteColor[]

void _orderCheck
