import { clsx, type ClassValue } from 'clsx'
import { extendTailwindMerge } from 'tailwind-merge'

// Register the project's custom @utility classes (src/styles/globals.css) so
// tailwind-merge recognises them as members of the standard class groups and
// can dedupe/override them. Without this, a custom utility like `h-chrome-header`
// is treated as a standalone class and survives alongside a later `h-auto` /
// `min-h-0` override — the winner then decided by fragile CSS source order, not
// className order. `extend` ADDS these literal class names to the existing
// default groups (which keep their value validators) so they merge/override.
const twMerge = extendTailwindMerge({
  extend: {
    classGroups: {
      h: ['h-chrome-header'],
      'min-h': ['min-h-chrome-header', 'min-h-tap-target-min', 'min-h-tap-target-comfortable'],
      'min-w': ['min-w-tap-target-min', 'min-w-tap-target-comfortable'],
    },
  },
})

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

// initialOf — shared avatar-initial computation (Fix 9, bugfixes3 review).
// `name.charAt(0)` breaks two ways: (1) a name with leading whitespace
// produces a blank/space initial instead of skipping to the first real
// character, and (2) `charAt` reads a single UTF-16 code unit, so a name
// whose first character is outside the Basic Multilingual Plane (e.g. an
// emoji or astral-plane script) renders half a surrogate pair as garbage.
// `Array.from(name.trim())` iterates by Unicode code point, so the first
// element is always the whole first character. Falls back to '?' for an
// empty/whitespace-only name so callers never render nothing.
export function initialOf(name: string): string {
  return Array.from(name.trim())[0]?.toUpperCase() ?? '?'
}
