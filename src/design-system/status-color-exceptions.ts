export const colorExceptionRegistry = Object.freeze({
  documentSurfaces: {
    owner: 'Library previews',
    rationale: 'Paper and signature canvases reproduce source-document colours.',
    scope: ['document preview', 'PDF preview', 'signature canvas'],
  },
  qrCodes: {
    owner: 'Connector setup',
    rationale: 'Machine-readable QR contrast is controlled by the encoded artifact.',
    scope: ['QR code foreground', 'QR code background'],
  },
  syntaxHighlighting: {
    owner: 'Code presentation',
    rationale: 'Language grammars need a separately governed syntax palette.',
    scope: ['code editor', 'code preview', 'syntax-highlighted chat blocks'],
  },
  dataVisualization: {
    owner: 'Data visualization',
    rationale: 'Series palettes use hue plus a second cue and are never status chrome.',
    scope: ['charts', 'graphs', 'diagrams', 'file-type indicators'],
  },
  userAuthoredColors: {
    owner: 'User-authored content',
    rationale: 'User data is preserved and receives contrast-aware surrounding treatment.',
    scope: ['user-selected colours', 'agent-selected colours', 'document content'],
  },
} as const)

export type ColorException = keyof typeof colorExceptionRegistry

