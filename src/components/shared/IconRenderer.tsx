// IconRenderer — maps agent.icon string names to Phosphor icon components.
// Covers all icon names agents are likely to set per BRD Appendix D.

import {
  Robot,
  MagnifyingGlass,
  PencilLine,
  Brain,
  Code,
  Terminal,
  Globe,
  FileText,
  Gear,
  Wrench,
  Star,
  Lightning,
  Compass,
  BookOpen,
  Chat,
  Database,
  Shield,
  Eye,
  Cpu,
  FlowArrow,
  Microscope,
  PencilSimple,
  Envelope,
  Flask,
  Folder,
  Headphones,
  Image,
  Key,
  Link,
  MagicWand,
  MapPin,
  Megaphone,
  MusicNote,
  PaintBrush,
  Palette,
  Phone,
  Rocket,
  Scales,
  ShieldCheck,
  ShoppingCart,
  SpeakerHigh,
  Sun,
  Tree,
  Trophy,
  Users,
  VideoCamera,
} from '@phosphor-icons/react'
import type { Icon as PhosphorIcon } from '@phosphor-icons/react'

const ICON_MAP: Record<string, PhosphorIcon> = {
  robot: Robot,
  'magnifying-glass': MagnifyingGlass,
  'pencil-line': PencilLine,
  brain: Brain,
  code: Code,
  terminal: Terminal,
  globe: Globe,
  'file-text': FileText,
  gear: Gear,
  wrench: Wrench,
  star: Star,
  lightning: Lightning,
  compass: Compass,
  'book-open': BookOpen,
  chat: Chat,
  database: Database,
  shield: Shield,
  eye: Eye,
  cpu: Cpu,
  'flow-arrow': FlowArrow,
  microscope: Microscope,
  'pencil-simple': PencilSimple,
  envelope: Envelope,
  flask: Flask,
  folder: Folder,
  headphones: Headphones,
  image: Image,
  key: Key,
  link: Link,
  'magic-wand': MagicWand,
  'map-pin': MapPin,
  megaphone: Megaphone,
  'music-note': MusicNote,
  'paint-brush': PaintBrush,
  palette: Palette,
  phone: Phone,
  rocket: Rocket,
  scales: Scales,
  'shield-check': ShieldCheck,
  'shopping-cart': ShoppingCart,
  'speaker-high': SpeakerHigh,
  sun: Sun,
  tree: Tree,
  trophy: Trophy,
  users: Users,
  'video-camera': VideoCamera,
}

interface IconRendererProps {
  /** Icon name string from agent.icon (e.g. "robot", "magnifying-glass") */
  icon?: string | null
  size?: number
  className?: string
  weight?: 'thin' | 'light' | 'regular' | 'bold' | 'fill' | 'duotone'
}

export function IconRenderer({ icon, size = 18, className, weight = 'regular' }: IconRendererProps) {
  // W6-B3 / C8: case-insensitive lookup. Agents may have `icon` set to
  // "Robot", "robot", or "ROBOT" (the backend sometimes emits the catalog's
  // canonical PascalCase, sometimes whatever the user typed in the form).
  // The previous direct `ICON_MAP[icon]` failed any case-mismatched input
  // and silently fell back to the Robot icon — which is why Mia, Jim, Ava,
  // and Ray all rendered the same default. `getIconComponent` (in
  // @/lib/agentIcons) is the canonical case-insensitive resolver; this
  // function stays here because it adds the larger BRD Appendix D catalog
  // (e.g. "magnifying-glass", "shield-check", "flow-arrow") that the
  // 10-entry create-modal catalog does not cover.
  const Icon = (icon && ICON_MAP[icon.toLowerCase()]) || Robot
  return <Icon size={size} className={className} weight={weight} />
}
