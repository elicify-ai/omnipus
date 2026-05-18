import {
  BookOpen,
  Code,
  ListChecks,
  Lightning,
  SlidersHorizontal,
} from '@phosphor-icons/react'
import type { Icon } from '@phosphor-icons/react'

export interface ToolPreset { // not-wire-format: UI-only preset definition with Phosphor icon; never serialised over any API boundary
  label: string
  icon: Icon
  tools: string[] | 'all'
}

export const TOOL_PRESETS = {
  researcher: {
    label: 'Read-only Researcher',
    icon: BookOpen,
    tools: ['read_file', 'list_dir', 'web_search', 'web_fetch', 'message'],
  },
  developer: {
    label: 'Developer',
    icon: Code,
    tools: [
      'read_file',
      'write_file',
      'edit_file',
      'list_dir',
      'exec',
      'web_search',
      'web_fetch',
      'message',
      'send_file',
      'browser.navigate',
      'browser.click',
      'browser.type',
      'browser.screenshot',
      'browser.get_text',
      'browser.wait',
    ],
  },
  task_manager: {
    label: 'Task Manager',
    icon: ListChecks,
    tools: [
      'read_file',
      'list_dir',
      'task_list',
      'task_create',
      'task_update',
      'message',
    ],
  },
  unrestricted: {
    label: 'Unrestricted',
    icon: Lightning,
    tools: 'all' as const,
  },
  custom: {
    label: 'Custom',
    icon: SlidersHorizontal,
    tools: [] as string[],
  },
} as const satisfies Record<string, ToolPreset>

export type PresetKey = keyof typeof TOOL_PRESETS
