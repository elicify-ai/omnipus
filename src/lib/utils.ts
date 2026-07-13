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
