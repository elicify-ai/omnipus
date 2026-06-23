// rehype-phosphor-emoji — rehype plugin that replaces common emoji in text nodes
// with <span data-phosphor-icon="IconName"> elements for Phosphor icon rendering.
// Wire into MarkdownText's rehypePlugins and add a custom span component renderer.

import type { Plugin } from 'unified'
import type { Root, Element, Text, RootContent } from 'hast'
import { visit } from 'unist-util-visit'
import type { PhosphorIconName } from '@/lib/phosphor-emoji-icons'

// Emoji → Phosphor icon name map. Values are typed `PhosphorIconName` (the key
// union of PHOSPHOR_EMOJI_ICONS), so adding an emoji whose target icon is not in
// that allow-list is a COMPILE error — a drift guard at the type level.
export const EMOJI_MAP: Record<string, PhosphorIconName> = {
  '✅': 'CheckCircle',
  '✓': 'CheckCircle',
  '☑': 'CheckCircle',
  '⚠️': 'Warning',
  '⚠': 'Warning',
  'ℹ️': 'Info',
  'ℹ': 'Info',
  '❌': 'XCircle',
  '✗': 'XCircle',
  '✘': 'XCircle',
  '📁': 'Folder',
  '📂': 'FolderOpen',
  '📄': 'File',
  '📃': 'FileText',
  '💻': 'Terminal',
  '🖥️': 'Desktop',
  '🌐': 'Globe',
  '🔒': 'Lock',
  '🔓': 'LockOpen',
  '⭐': 'Star',
  '🌟': 'Star',
  '🚀': 'Rocket',
  '⚙️': 'Gear',
  '⚙': 'Gear',
  '🔧': 'Wrench',
  // Faces
  '😀': 'Smiley',
  '😄': 'Smiley',
  '😊': 'Smiley',
  '🙂': 'Smiley',
  '😉': 'SmileyWink',
  '😢': 'SmileySad',
  '😞': 'SmileySad',
  '😐': 'SmileyMeh',
  '😑': 'SmileyMeh',
  '😡': 'SmileyAngry',
  '😠': 'SmileyAngry',
  '😵': 'SmileyXEyes',
  // Hands / reactions
  '👍': 'ThumbsUp',
  '👎': 'ThumbsDown',
  // Hearts
  '❤️': 'Heart',
  '❤': 'Heart',
  '💕': 'Heart',
  '💗': 'Heart',
  // Time
  '⏰': 'Clock',
  '🕐': 'Clock',
  '🕑': 'Clock',
  // Writing / notes
  '📝': 'NotePencil',
  '📓': 'Note',
  '✏️': 'Pencil',
  '✏': 'Pencil',
  // Goals / targets
  '🎯': 'Target',
  '🏆': 'Trophy',
  // Sparkle / fire / light
  '✨': 'Sparkle',
  '🔥': 'Fire',
  '💡': 'Lightbulb',
  // Search
  '🔍': 'MagnifyingGlass',
  '🔎': 'MagnifyingGlass',
  // People / bots
  '👤': 'Person',
  '👥': 'Users',
  '🤖': 'Robot',
  // Sharing / links
  '🔗': 'Link',
  '📤': 'Upload',
  '📥': 'Download',
  // Eyes / visibility
  '👁️': 'Eye',
  '👁': 'Eye',
  // Notifications / labels / power / protection
  '🔔': 'Bell',
  '🏷️': 'Tag',
  '🏷': 'Tag',
  '⚡': 'Lightning',
  '🛡️': 'Shield',
  '🛡': 'Shield',
  '👑': 'Crown',
  // Misc UI
  '📷': 'Camera',
  '📸': 'Camera',
  '🗑️': 'Trash',
  '🗑': 'Trash',
  '❓': 'Question',
  '❔': 'Question',
}

// Build a regex that matches any of the keys (order matters — longer first)
const sortedKeys = Object.keys(EMOJI_MAP).sort((a, b) => b.length - a.length)
const EMOJI_PATTERN = sortedKeys.map((e) => e.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|')
// EMOJI_REGEX is used for exec/replace inside textToNodes (stateful, resets lastIndex before use)
const EMOJI_REGEX = new RegExp(EMOJI_PATTERN, 'g')
// EMOJI_TEST_REGEX is a separate instance used only for the .test() guard to avoid lastIndex contamination
const EMOJI_TEST_REGEX = new RegExp(EMOJI_PATTERN)

function textToNodes(value: string): (Text | Element)[] {
  const result: (Text | Element)[] = []
  let cursor = 0
  let match: RegExpExecArray | null

  EMOJI_REGEX.lastIndex = 0
  while ((match = EMOJI_REGEX.exec(value)) !== null) {
    const iconName = EMOJI_MAP[match[0]]
    if (!iconName) continue

    if (match.index > cursor) {
      result.push({ type: 'text', value: value.slice(cursor, match.index) })
    }

    result.push({
      type: 'element',
      tagName: 'span',
      properties: { 'data-phosphor-icon': iconName },
      children: [],
    } as Element)

    cursor = match.index + match[0].length
  }

  if (cursor < value.length) {
    result.push({ type: 'text', value: value.slice(cursor) })
  }

  return result
}

export const rehypePhosphorEmoji: Plugin<[], Root> = () => {
  return (tree) => {
    visit(tree, 'text', (node: Text, index, parent) => {
      if (typeof index !== 'number' || !parent) return
      // Skip emoji translation inside code/pre blocks — it breaks code literals
      if ('tagName' in parent && (parent.tagName === 'code' || parent.tagName === 'pre')) return
      if (!EMOJI_TEST_REGEX.test(node.value)) return

      const nodes = textToNodes(node.value)
      if (nodes.length <= 1) return

      parent.children.splice(index, 1, ...(nodes as RootContent[]))
      // Return the new index to avoid revisiting replaced nodes
      return index + nodes.length
    })
  }
}
