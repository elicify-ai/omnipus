// These exact approved badge paths already ship in the Phosphor chunk.
// Reuse the public components instead of storing a second path string.
import { Code, PaintBrush, Megaphone, Scales, LockKey, Lightning, BookOpen, Image, Microphone, ShareNetwork, Database, Cube, FileText, ListChecks, Flask, type Icon } from '@phosphor-icons/react'
import type { AgentIconRole } from '@/design-system/agent-identity'

export const REUSED_AGENT_BADGES = {
  developer: Code,
  designer: PaintBrush,
  marketing: Megaphone,
  legal: Scales,
  security: LockKey,
  automation: Lightning,
  knowledge: BookOpen,
  image: Image,
  audio: Microphone,
  social: ShareNetwork,
  data: Database,
  product: Cube,
  documents: FileText,
  personal: ListChecks,
  science: Flask,
} satisfies Partial<Record<AgentIconRole, Icon>>
