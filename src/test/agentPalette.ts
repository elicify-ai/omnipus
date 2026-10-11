// Identity colours for tests.
//
// Founder decision 2026-10-11 (AVATAR-PALETTE-DECISION.md), ordered
// Steel blue → Pewter. The values are the generated AgentColor enum — the
// wire palette — not a second list of hex literals copied into each test.
//
// Each colour is `AgentColor.options[n]` with a numeric literal, returned
// from its own named function. A variable index (`generated[index]`) is
// ts-colors/unsupported and cannot be baselined. Call these from a named
// function: an anonymous callback that writes the same literal is still
// unsupported.

import type { Agent } from '@/lib/api'
import { AgentColor } from '@/lib/api/generated/schemas'

type PaletteColor = NonNullable<Agent['color']>

export function steelBlue(): PaletteColor {
  return AgentColor.options[0] as PaletteColor
}

export function titanium(): PaletteColor {
  return AgentColor.options[1] as PaletteColor
}

export function verdigris(): PaletteColor {
  return AgentColor.options[2] as PaletteColor
}

export function gunmetal(): PaletteColor {
  return AgentColor.options[3] as PaletteColor
}

export function graphiteViolet(): PaletteColor {
  return AgentColor.options[4] as PaletteColor
}

export function roseGold(): PaletteColor {
  return AgentColor.options[5] as PaletteColor
}

export function bronze(): PaletteColor {
  return AgentColor.options[6] as PaletteColor
}

export function antiqueBrass(): PaletteColor {
  return AgentColor.options[7] as PaletteColor
}

export function champagne(): PaletteColor {
  return AgentColor.options[8] as PaletteColor
}

export function pewter(): PaletteColor {
  return AgentColor.options[9] as PaletteColor
}

// Index order is the contract enum order, which is the decision table order.
const _orderCheck = [
  steelBlue(),
  titanium(),
  verdigris(),
  gunmetal(),
  graphiteViolet(),
  roseGold(),
  bronze(),
  antiqueBrass(),
  champagne(),
  pewter(),
] as const satisfies readonly PaletteColor[]

void _orderCheck
