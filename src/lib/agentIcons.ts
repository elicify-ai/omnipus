// Agent avatar icons — shared catalog
//
// Wave 6 / A-fix: this file was extracted from CreateAgentModal.tsx and
// AgentProfile.tsx (both had byte-identical 10-entry `as const` arrays).
// Consolidating here means the case-insensitive lookup that Wave B3
// (icon-case fix) needs lands in one place, and future agents/workers
// pull from a single source of truth.
//
// Consumers must NOT export their own `ICON_OPTIONS` — import this one.
// The `as const` shape is load-bearing: `IconName` derives from it.

import {
  Robot,
  Brain,
  Lightbulb,
  MagnifyingGlass,
  PencilSimple,
  Code,
  Chat,
  Gear,
  Shield,
  Rocket,
} from '@phosphor-icons/react'

export const ICON_OPTIONS = [
  { name: 'Robot', component: Robot },
  { name: 'Brain', component: Brain },
  { name: 'Lightbulb', component: Lightbulb },
  { name: 'MagnifyingGlass', component: MagnifyingGlass },
  { name: 'PencilSimple', component: PencilSimple },
  { name: 'Code', component: Code },
  { name: 'Chat', component: Chat },
  { name: 'Gear', component: Gear },
  { name: 'Shield', component: Shield },
  { name: 'Rocket', component: Rocket },
] as const

export type IconName = typeof ICON_OPTIONS[number]['name']

/**
 * Resolve an `IconName` to its Phosphor component. Case-insensitive —
 * `getIconComponent('robot')` and `getIconComponent('ROBOT')` both
 * return the Robot icon. Unknown / empty names fall back to Robot.
 */
export function getIconComponent(name: string | undefined | null): typeof Robot {
  if (!name) return Robot
  const lower = name.toLowerCase()
  return ICON_OPTIONS.find((o) => o.name.toLowerCase() === lower)?.component ?? Robot
}
