// phosphor-emoji-icons — explicit allow-list of the Phosphor icons that the
// rehype-phosphor-emoji plugin can ever emit via `data-phosphor-icon`.
//
// IMPORTANT: do NOT use `import * as PhosphorIcons from '@phosphor-icons/react'`.
// The wildcard import pulls the ENTIRE icon set (~5MB) into the bundle because
// it defeats tree-shaking. The set of icon names that can appear in a
// `data-phosphor-icon` attribute is exactly the *values* of EMOJI_MAP in
// src/lib/rehype-phosphor-emoji.ts — so we import only those by name and look
// them up from a small Record. Any name not in the map falls back to a generic
// icon (Question), which should never happen in practice since the plugin only
// emits names from this same closed set.
//
// When EMOJI_MAP grows a new icon value, add the matching named import here.

import type { ComponentType } from 'react'
import {
  CheckCircle,
  Warning,
  Info,
  XCircle,
  Folder,
  FolderOpen,
  File,
  FileText,
  Terminal,
  Desktop,
  Globe,
  Lock,
  LockOpen,
  Star,
  Rocket,
  Gear,
  Wrench,
  Question,
} from '@phosphor-icons/react'

export type PhosphorIconComponent = ComponentType<{
  size?: number
  weight?: 'thin' | 'light' | 'regular' | 'bold' | 'fill' | 'duotone'
  className?: string
}>

// Allow-list keyed by the exact icon names produced by EMOJI_MAP.
export const PHOSPHOR_EMOJI_ICONS: Record<string, PhosphorIconComponent> = {
  CheckCircle,
  Warning,
  Info,
  XCircle,
  Folder,
  FolderOpen,
  File,
  FileText,
  Terminal,
  Desktop,
  Globe,
  Lock,
  LockOpen,
  Star,
  Rocket,
  Gear,
  Wrench,
}

// Fallback for an unrecognized name. The rehype plugin only ever emits names
// from the closed EMOJI_MAP set, so this is defensive — but it guarantees a
// real component is always returned so the caller never renders `undefined`.
export const PHOSPHOR_EMOJI_FALLBACK: PhosphorIconComponent = Question

// Resolve a `data-phosphor-icon` name to a concrete icon component, or null if
// the name is absent (so the caller can render the original span children).
export function resolvePhosphorEmojiIcon(name: string | undefined): PhosphorIconComponent | null {
  if (!name) return null
  return PHOSPHOR_EMOJI_ICONS[name] ?? PHOSPHOR_EMOJI_FALLBACK
}
