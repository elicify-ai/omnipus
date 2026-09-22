import type { Decorator, Preview } from '@storybook/react-vite'
import { createElement } from 'react'

import './preview.css'

export type DesignSystemStoryParameters = {
  keyboard?: Array<{
    trigger: string
    key: string
    expectFocus?: string
    expectExpanded?: boolean
    expectScroll?: { selector: string; axis: 'x' | 'y'; direction: 'increase' | 'decrease' }
  }>
  pointerTargets?: string[]
  motionTargets?: string[]
  motionSetup?: { trigger: string; key: string }
  forcedColorTargets?: string[]
  forcedColors?: {
    boundaries?: string[]
    differences?: Array<{
      cue: 'foreground' | 'indicator' | 'progress'
      selector: string
      property: 'color' | 'backgroundColor' | 'borderTopColor' | 'outlineColor'
      againstSelector: string
      againstProperty: 'color' | 'backgroundColor' | 'borderTopColor' | 'outlineColor'
      actualSystemColor?: 'ButtonText' | 'Highlight' | 'HighlightText' | 'CanvasText'
      againstSystemColor?: 'ButtonFace' | 'Canvas' | 'Highlight'
    }>
    focus?: string[]
    states?: Array<{ selector: string; attribute: string; value: string }>
    stateTransitions?: Array<{
      selector: string
      attribute: string
      from: string
      action: 'click'
      to: string
      differences: Array<{
        cue: 'foreground' | 'indicator' | 'progress'
        selector: string
        property: 'color' | 'backgroundColor' | 'borderTopColor' | 'outlineColor'
        againstSelector: string
        againstProperty: 'color' | 'backgroundColor' | 'borderTopColor' | 'outlineColor'
        actualSystemColor?: 'ButtonText' | 'Highlight' | 'HighlightText' | 'CanvasText'
        againstSystemColor?: 'ButtonFace' | 'Canvas' | 'Highlight'
      }>
    }>
  }
  reflowExemptions?: Array<{ selector: string; reason: string }>
  browserAssertions?: Array<{ selector: string; attribute?: string; value?: string; text?: string }>
}

const exposeVerificationMetadata: Decorator = (Story, context) => {
  const metadata = context.parameters.designSystem ?? {}
  return createElement(
    'main',
    {
      'data-design-system-story': `${context.title}--${context.name}`,
      'data-design-system-config': JSON.stringify(metadata),
      style: { minHeight: '100%' },
    },
    // The isolated iframe is a complete document. Supply its page/component
    // heading scaffold without imposing visible layout on the story itself.
    createElement('h1', {
      style: {
        position: 'absolute',
        width: 1,
        height: 1,
        padding: 0,
        margin: -1,
        overflow: 'hidden',
        clip: 'rect(0, 0, 0, 0)',
        whiteSpace: 'nowrap',
        border: 0,
      },
    }, `${context.title}: ${context.name}`),
    createElement('h2', {
      style: {
        position: 'absolute',
        width: 1,
        height: 1,
        padding: 0,
        margin: -1,
        overflow: 'hidden',
        clip: 'rect(0, 0, 0, 0)',
        whiteSpace: 'nowrap',
        border: 0,
      },
    }, context.name),
    createElement(Story),
  )
}

const preview: Preview = {
  decorators: [exposeVerificationMetadata],
  parameters: {
    a11y: { test: 'error' },
    controls: { expanded: true },
    options: { storySort: { method: 'alphabetical' } },
  },
  tags: ['autodocs'],
}

export default preview
