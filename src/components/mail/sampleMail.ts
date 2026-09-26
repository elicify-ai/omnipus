// sampleMail.ts — sample data for the Mail UI prototype (founder decision D35).
//
// D35: "frontend-lead builds the Mail panel, draft preview/edit, compose and
// signature editor as design-system stories with sample data (no backend)".
// Everything here is PRESENTATIONAL sample data: no endpoint is called, no
// wire type is declared (wire types come only from src/lib/api/generated/,
// which this prototype deliberately does not touch — shared rule 4), and
// nothing here is persisted or shipped outside the stories.
//
// Sample bodies are pre-rendered HTML strings consumed by MailHtmlFrame. The
// real feature renders Markdown to sanitized HTML server-side (spec FR-004)
// and serves bodies under a token-scoped, script-free preview route
// (FR-019); the prototype simulates both postures client-side purely so the
// stories can show the states.

export type FolderKey = 'inbox' | 'sent' | 'drafts'

export interface MailboxSample {
  id: string
  /** Human label shown in the picker: "<agent> · <workspace>". */
  label: string
  address: string
}

export interface FolderSummary {
  key: FolderKey
  label: string
  unread: number
  total: number
}

export interface MailAttachment {
  id: string
  name: string
  sizeBytes: number
  contentType: string
}

export interface MessageSummary {
  id: string
  folder: FolderKey
  /** Display name, e.g. "Nadia Brekke". */
  fromName: string
  fromAddress: string
  /** Recipient names shown on sent items ("To: Nadia Brekke"). */
  toNames: string[]
  subject: string
  preview: string
  /** ISO timestamp. */
  date: string
  unread: boolean
  /**
   * D38: the agent read this message via read_message — shown as a small
   * "read by agent" tag so the human still sees what it handled.
   */
  readByAgent: boolean
  hasAttachments: boolean
}

export interface MessageDetail extends MessageSummary {
  ccNames: string[]
  bodyKind: 'html' | 'text'
  /** Pre-rendered body (prototype). Real impl: token-scoped preview route. */
  bodyHtml: string
  bodyText: string
  /** D17: remote images ship blocked until "Load images". */
  remoteImagesBlocked: boolean
  attachments: MailAttachment[]
}

export interface DraftSample {
  id: string
  subject: string
  to: string[]
  cc: string[]
  bcc: string[]
  /** Markdown source the human would edit (D23). */
  markdownBody: string
  /** Pre-rendered preview (prototype). */
  renderedHtml: string
  origin: 'agent' | 'external'
  /** Set on external drafts: D24 formatting-loss statement. */
  formatLossNotice?: string
  attachments: MailAttachment[]
  /** Chat-context hint, e.g. "Posted by Aisha 5m ago". */
  chatContext?: string
}

export interface ConnectionSample {
  status: 'ok' | 'error' | 'backoff'
  /** Sanitized error class text (spec FR-018 — class-named, never raw). */
  message?: string
  /** "Retrying at 14:32" (D29/R2-8: the schedule is shown, not hidden). */
  nextRetryAt?: string
}

export interface MailSampleData {
  mailboxes: MailboxSample[]
  /** Mailbox currently selected in the picker. */
  activeMailboxId: string
  folders: FolderSummary[]
  messages: MessageDetail[]
  draft: DraftSample
  connection: ConnectionSample
  lastChecked: string
  signatureHtml: string
  signatureCharCount: number
  signatureMaxChars: number
}

const now = new Date()
function iso(minutesAgo: number): string {
  return new Date(now.getTime() - minutesAgo * 60_000).toISOString()
}

export function formatMailTime(isoDate: string): string {
  const d = new Date(isoDate)
  if (Number.isNaN(d.getTime())) return ''
  const today = new Date()
  const sameDay =
    d.getFullYear() === today.getFullYear() &&
    d.getMonth() === today.getMonth() &&
    d.getDate() === today.getDate()
  if (sameDay) return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  const sameYear = d.getFullYear() === today.getFullYear()
  const opts: Intl.DateTimeFormatOptions = { month: 'short', day: 'numeric' }
  if (!sameYear) opts.year = 'numeric'
  return d.toLocaleDateString(undefined, opts)
}

export function formatMailBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '—'
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB']
  let value = bytes / 1024
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit += 1
  }
  return `${value.toFixed(value >= 10 ? 0 : 1)} ${units[unit]}`
}

/** Inline SVG stand-ins for the loaded image state — no network in the prototype. */
function bannerArt(label: string): string {
  const svg =
    '<svg xmlns="http://www.w3.org/2000/svg" width="640" height="120">' +
    '<rect width="640" height="120" fill="#204E3B"/>' +
    '<text x="20" y="66" font-family="Arial, sans-serif" font-size="22" fill="#F2F5F3">' + label + '</text></svg>'
  return 'data:image/svg+xml;utf8,' + encodeURIComponent(svg)
}

function chartArt(): string {
  const bars = [
    [0, 46], [1, 58], [2, 40], [3, 74], [4, 66], [5, 92],
  ]
    .map(
      ([index, height]) =>
        `<rect x="${20 + index * 40}" y="${110 - height}" width="26" height="${height}" fill="#3E7BB6"/>`,
    )
    .join('')
  const svg =
    '<svg xmlns="http://www.w3.org/2000/svg" width="260" height="130">' +
    '<rect width="260" height="130" fill="#EEF2F5"/>' + bars + '</svg>'
  return 'data:image/svg+xml;utf8,' + encodeURIComponent(svg)
}

/**
 * Assemble the reading-pane body for the HTML sample. The prototype
 * simulates D17's remote-image posture by swapping image markup between the
 * blocked and loaded variants of the SAME sample body — the real feature
 * re-mints its token server-side instead (FR-019).
 */
export function composeSampleBodyHtml(loadRemote: boolean): string {
  const image = loadRemote
    ? `<img src="${bannerArt('Helios Labs — quarterly report')}" alt="Helios Labs quarterly banner" style="max-width:100%;border-radius:6px">`
    : '<div style="border:1px dashed #9a9a9a;padding:18px;text-align:center;color:#6b6b6b;font-size:13px;font-family:Arial,sans-serif">Image blocked to protect your privacy</div>'
  const cid = loadRemote
    ? `<img src="${chartArt()}" alt="Revenue chart" style="max-width:100%;border-radius:6px">`
    : '<div style="border:1px dashed #9a9a9a;padding:18px;text-align:center;color:#6b6b6b;font-size:13px;font-family:Arial,sans-serif">Inline image available once loaded</div>'
  return [
    '<div style="font-family:Arial,Helvetica,sans-serif;color:#1f1f1f;line-height:1.5;padding:20px">',
    image,
    '<h2 style="font-size:18px">Quarterly report — draft for review</h2>',
    '<p>Hi Aisha,</p>',
    '<p>The Q3 numbers are in. Draft structure below — the summary section is the part I would like a second pair of eyes on before this goes to the board.</p>',
    '<ul><li>Revenue: up 14% quarter over quarter</li><li>Churn: flat at 2.1%</li><li>Outlook: raised for Q4</li></ul>',
    cid,
    '<p>Could you look at the summary section by Thursday?</p>',
    '<p>Thanks,<br>Nadia</p>',
    '</div>',
  ].join('')
}

const agentDraftMarkdown = [
  'Hi Lars,',
  '',
  'Attached is the signed renewal for your review. Two things changed since the last version:',
  '',
  '1. The term is now **24 months** (was 12).',
  '2. Support moved to *priority* hours.',
  '',
  'Happy to walk through the diff before you forward it internally.',
  '',
  'Best,',
  'Aisha',
].join('\n')

const externalDraftMarkdown = [
  'Team,',
  '',
  'Short agenda for Thursday:',
  '- Q3 wrap-up (15 min)',
  '- Roadmap review (30 min)',
  '- AOB',
  '',
  'If you cannot attend, send notes in advance.',
].join('\n')

const agentDraftHtml = [
  '<div style="font-family:Arial,Helvetica,sans-serif;color:#1f1f1f;line-height:1.6;padding:20px">',
  '<p>Hi Lars,</p>',
  '<p>Attached is the signed renewal for your review. Two things changed since the last version:</p>',
  '<ol><li>The term is now <strong>24 months</strong> (was 12).</li>',
  '<li>Support moved to <em>priority</em> hours.</li></ol>',
  '<p>Happy to walk through the diff before you forward it internally.</p>',
  '<p>Best,<br>Aisha</p>',
  '</div>',
].join('')

export const sampleMailData: MailSampleData = {
  mailboxes: [
    { id: 'mb-aisha', label: 'Aisha · Client Work', address: 'aisha@omnipus.example' },
    { id: 'mb-dmitri', label: 'Dmitri · Research', address: 'dmitri@omnipus.example' },
  ],
  activeMailboxId: 'mb-aisha',
  folders: [
    { key: 'inbox', label: 'Inbox', unread: 2, total: 5 },
    { key: 'sent', label: 'Sent', unread: 0, total: 2 },
    { key: 'drafts', label: 'Drafts', unread: 0, total: 2 },
  ],
  messages: [
    {
      id: 'm1',
      folder: 'inbox',
      fromName: 'Nadia Brekke',
      fromAddress: 'nadia@helios-labs.example',
      toNames: ['Aisha'],
      subject: 'Quarterly report — draft for review',
      preview:
        'The Q3 numbers are in. Draft structure below — the summary section is the part I would like a second pair of eyes on before this goes to the board.',
      date: iso(18),
      unread: true,
      readByAgent: false,
      hasAttachments: true,
      ccNames: [],
      bodyKind: 'html',
      bodyHtml: composeSampleBodyHtml(false),
      bodyText:
        'The Q3 numbers are in. Draft structure below — the summary section is the part I would like a second pair of eyes on before this goes to the board.\n\n- Revenue: up 14% quarter over quarter\n- Churn: flat at 2.1%\n- Outlook: raised for Q4\n\nCould you look at the summary section by Thursday?\n\nThanks,\nNadia',
      remoteImagesBlocked: true,
      attachments: [
        {
          id: 'a1',
          name: 'q3-financials.xlsx',
          sizeBytes: 842_112,
          contentType: 'application/vnd.ms-excel',
        },
        {
          id: 'a2',
          name: 'board-summary.pdf',
          sizeBytes: 1_966_080,
          contentType: 'application/pdf',
        },
      ],
    },
    {
      id: 'm2',
      folder: 'inbox',
      fromName: 'Helios Labs Billing',
      fromAddress: 'billing@helios-labs.example',
      toNames: ['Aisha'],
      subject: 'Invoice 2041 — payment confirmed',
      preview: 'Your payment of 4,800.00 EUR was received. Thank you. This is a receipt for your records.',
      date: iso(52),
      unread: false,
      readByAgent: true,
      hasAttachments: false,
      ccNames: [],
      bodyKind: 'text',
      bodyHtml: '',
      bodyText:
        'Your payment of 4,800.00 EUR was received. Thank you.\n\nThis is a receipt for your records. Invoice 2041 is now closed.',
      remoteImagesBlocked: false,
      attachments: [],
    },
    {
      id: 'm3',
      folder: 'inbox',
      fromName: 'Marc Delacroix',
      fromAddress: 'marc@delacroix-partners.example',
      toNames: ['Aisha'],
      subject: 'Re: Kickoff call follow-up',
      preview: 'Thanks for the notes. I will circulate the timeline to our side and come back to you early next week.',
      date: iso(190),
      unread: true,
      readByAgent: false,
      hasAttachments: false,
      ccNames: ['Dmitri'],
      bodyKind: 'text',
      bodyHtml: '',
      bodyText:
        'Thanks for the notes. I will circulate the timeline to our side and come back to you early next week.\n\nBest,\nMarc',
      remoteImagesBlocked: false,
      attachments: [],
    },
    {
      id: 'm4',
      folder: 'inbox',
      fromName: 'Lars Hoft',
      fromAddress: 'lars@nordwind-gmbh.example',
      toNames: ['Aisha'],
      subject: 'Contract renewal — countersigned',
      preview: 'All signed on our side as well. The 24-month term starts October 1st. Welcome aboard again.',
      date: iso(1_500),
      unread: false,
      readByAgent: true,
      hasAttachments: true,
      ccNames: [],
      bodyKind: 'html',
      bodyHtml: contractRenewalBodyHtml(),
      bodyText: 'All signed on our side as well. The 24-month term starts October 1st.',
      remoteImagesBlocked: false,
      attachments: [
        {
          id: 'a3',
          name: 'renewal-signed.pdf',
          sizeBytes: 3_145_728,
          contentType: 'application/pdf',
        },
      ],
    },
    {
      id: 'm5',
      folder: 'inbox',
      fromName: 'Dmitri Volkov',
      fromAddress: 'dmitri@omnipus.example',
      toNames: ['Aisha'],
      subject: 'Research digest, week 38',
      preview: 'Three papers this week; the retrieval-augmentation survey is the one worth your time.',
      date: iso(2_900),
      unread: false,
      readByAgent: false,
      hasAttachments: false,
      ccNames: [],
      bodyKind: 'text',
      bodyHtml: '',
      bodyText: 'Three papers this week; the retrieval-augmentation survey is the one worth your time.',
      remoteImagesBlocked: false,
      attachments: [],
    },
    {
      id: 's1',
      folder: 'sent',
      fromName: 'Aisha',
      fromAddress: 'aisha@omnipus.example',
      toNames: ['Nadia Brekke'],
      subject: 'Re: Quarterly report — draft for review',
      preview: 'Summary section reviewed. Two comments inline; overall structure is strong.',
      date: iso(300),
      unread: false,
      readByAgent: false,
      hasAttachments: false,
      ccNames: [],
      bodyKind: 'text',
      bodyHtml: '',
      bodyText: 'Summary section reviewed. Two comments inline; overall structure is strong.',
      remoteImagesBlocked: false,
      attachments: [],
    },
    {
      id: 's2',
      folder: 'sent',
      fromName: 'Aisha',
      fromAddress: 'aisha@omnipus.example',
      toNames: ['Lars Hoft'],
      subject: 'Contract renewal — signed copy',
      preview: 'Please find the countersigned agreement attached for your records.',
      date: iso(1_560),
      unread: false,
      readByAgent: false,
      hasAttachments: true,
      ccNames: [],
      bodyKind: 'text',
      bodyHtml: '',
      bodyText: 'Please find the countersigned agreement attached for your records.',
      remoteImagesBlocked: false,
      attachments: [
        {
          id: 'a4',
          name: 'renewal-omnipus-signed.pdf',
          sizeBytes: 3_145_728,
          contentType: 'application/pdf',
        },
      ],
    },
  ],
  draft: {
    id: 'd1',
    subject: 'Re: Contract renewal — signed copy',
    to: ['lars@nordwind-gmbh.example'],
    cc: [],
    bcc: [],
    markdownBody: agentDraftMarkdown,
    renderedHtml: agentDraftHtml,
    origin: 'agent',
    attachments: [
      {
        id: 'a5',
        name: 'renewal-redline.docx',
        sizeBytes: 512_000,
        contentType: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
      },
    ],
    chatContext: 'Aisha (agent) created this draft 12m ago and posted the link in chat',
  },
  connection: { status: 'ok' },
  lastChecked: iso(2),
  signatureHtml: [
    '<div style="font-family:Arial,sans-serif;font-size:13px;color:#1f1f1f">',
    '<strong>Aisha</strong> · Client Work, Omnipus',
    '<br>+49 30 1234 5678 · <a href="https://omnipus.example">omnipus.example</a>',
    '</div>',
  ].join(''),
  signatureCharCount: 164,
  signatureMaxChars: 16_384,
}

/** The external (owner-started) draft variant used by the D24 edit story. */
export const sampleExternalDraft: DraftSample = {
  id: 'd2',
  subject: 'Meeting notes — Thursday',
  to: ['team@omnipus.example'],
  cc: [],
  bcc: [],
  markdownBody: externalDraftMarkdown,
  renderedHtml: [
    '<div style="font-family:Arial,sans-serif;color:#1f1f1f;line-height:1.6;padding:20px">',
    '<p>Team,</p>',
    '<p>Short agenda for Thursday:</p>',
    '<ul><li>Q3 wrap-up (15 min)</li><li>Roadmap review (30 min)</li><li>AOB</li></ul>',
    '<p>If you cannot attend, send notes in advance.</p>',
    '</div>',
  ].join(''),
  origin: 'external',
  formatLossNotice:
    'This draft was started in another mail program. Opening it in Omnipus converts it to Omnipus formatting: styling, images and table layout may be lost; text, headings, lists and links are preserved.',
  attachments: [],
  chatContext: undefined,
}

/** Body art for the countersigned-renewal sample (m4). */
function signatureArt(): string {
  const svg =
    '<svg xmlns="http://www.w3.org/2000/svg" width="640" height="96">' +
    '<rect width="640" height="96" fill="#1F3A2E"/>' +
    '<text x="20" y="54" font-family="Arial, sans-serif" font-size="20" fill="#EAF2ED">' +
    'Contract renewal — countersigned by Nordwind GmbH</text></svg>'
  return 'data:image/svg+xml;utf8,' + encodeURIComponent(svg)
}

/** m4's own HTML body so the images-loaded story is internally coherent. */
function contractRenewalBodyHtml(): string {
  return [
    '<div style="font-family:Arial,Helvetica,sans-serif;color:#1f1f1f;line-height:1.6;padding:20px">',
    `<img src="${signatureArt()}" alt="Nordwind GmbH countersigned stamp" style="max-width:100%;border-radius:6px">`,
    '<p>Hi Aisha,</p>',
    '<p>All signed on our side as well. The 24-month term starts October 1st — the countersigned copy is attached for your records.</p>',
    '<p>Welcome aboard again.</p>',
    '<p>Best,<br>Lars</p>',
    '</div>',
  ].join('')
}

/** Variant of the sample data with the connection in a backoff state. */
export const sampleConnectionBackoff: MailSampleData = {
  ...sampleMailData,
  connection: {
    status: 'backoff',
    message: 'Mail server unreachable — connection timed out',
    nextRetryAt: '14:32',
  },
}

/** Variant with an empty folder (nothing in Drafts yet). */
export const sampleEmptyDrafts: MailSampleData = {
  ...sampleMailData,
  messages: sampleMailData.messages.filter((m) => m.folder !== 'drafts'),
  folders: sampleMailData.folders.map((f) => (f.key === 'drafts' ? { ...f, total: 0 } : f)),
}

/** Variant used for the loading story (skeleton rows, no messages yet). */
export const sampleLoading: MailSampleData = {
  ...sampleMailData,
  messages: [],
}
