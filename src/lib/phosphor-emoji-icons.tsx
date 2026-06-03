// phosphor-emoji-icons — explicit allow-list of the Phosphor icons that the
// rehype-phosphor-emoji plugin can ever emit via `data-phosphor-icon`.
//
// IMPORTANT: do NOT use `import * as PhosphorIcons from '@phosphor-icons/react'`.
// The wildcard import pulls the ENTIRE icon set (~5MB) into the bundle because
// it defeats tree-shaking. The set of icon names that can appear in a
// `data-phosphor-icon` attribute is exactly the *values* of EMOJI_MAP in
// src/lib/rehype-phosphor-emoji.ts — so we import only those by name and look
// them up from this small, sealed Record.
//
// Lookup behavior: a name absent from this allow-list resolves to `undefined`,
// and the consumers (markdown-text.tsx, historical-markdown.tsx) render the
// ORIGINAL span children unchanged in that case — there is no generic-icon
// fallback. This cannot happen in practice because EMOJI_MAP's icon values are
// typed `PhosphorIconName` (a key union of this map), so a new emoji whose icon
// is not added here is a COMPILE error, not a runtime miss.
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
} from '@phosphor-icons/react'

type PhosphorIconComponent = ComponentType<{
  size?: number
  weight?: 'thin' | 'light' | 'regular' | 'bold' | 'fill' | 'duotone'
  className?: string
}>

// Allow-list keyed by the exact icon names produced by EMOJI_MAP. Sealed with
// `as const satisfies` so `PhosphorIconName` is a precise key union usable as a
// compile-time drift guard against EMOJI_MAP.
export const PHOSPHOR_EMOJI_ICONS = {
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
} as const satisfies Record<string, PhosphorIconComponent>

export type PhosphorIconName = keyof typeof PHOSPHOR_EMOJI_ICONS
