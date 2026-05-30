# Omnipus UI/UX Design Specification

## Appendix C — User Interface & Experience

**Version:** 1.0 DRAFT  
**Date:** March 28, 2026  
**Parent Document:** Omnipus BRD v1.0  
**Related:** Appendix A (Windows Security), Appendix B (Feature Parity)  
**Status:** For Review

-----

## C.1 Purpose

This appendix defines the complete user interface and experience design for Omnipus. It specifies every screen, interaction pattern, navigation flow, and design principle derived from competitive analysis of OpenClaw, Omnipus, ChatGPT, Claude Artifacts, and Manus AI.

**Design philosophy:** Chat-first, minimalist, inline-everything. No separate canvas panel. Sidebar defaults to overlay but can be pinned for power users. Rich content renders inline in chat and expands to fullscreen when needed. The app feels like a modern messaging client with superpowers, not an enterprise admin console.

-----

## C.2 Technology Stack

| Layer | Decision |
|---|---|
| Language | TypeScript |
| UI library | React 19 |
| Build tool | Vite 6 |
| Chat primitives | AssistantUI (custom gateway runtime adapter) |
| Components | shadcn/ui (Radix + Tailwind) |
| Icons | Phosphor Icons (`@phosphor-icons/react`) — see C.3 |
| Styling | Tailwind CSS v4 + Framer Motion |
| State (UI) | Zustand |
| State (server) | TanStack Query |
| Router | TanStack Router |
| Charts | Recharts |
| Code highlighting | Shiki (or AssistantUI built-in) |
| Mermaid diagrams | mermaid.js (lazy-loaded) |
| Markdown | react-markdown + remark-gfm (non-chat), AssistantUI built-in (chat) |
| Package format | `@omnipus/ui` npm package (library mode) |

### C.2.0 Brand Design System

See `docs/brand/brand-guidelines.md` for the complete brand identity. Key design tokens:

**Color palette — "The Sovereign Deep":**

| Token | Name | HEX | Usage |
|---|---|---|---|
| `--color-primary` | Deep Space Black | `#0A0A0B` | App background, primary surfaces (60%) |
| `--color-secondary` | Liquid Silver | `#E2E8F0` | Typography, borders, secondary surfaces (30%) |
| `--color-accent` | Forge Gold | `#D4AF37` | CTAs, interactive elements, highlights, mascot details (10%) |
| `--color-success` | Emerald | `#10B981` | Success states, connected indicators |
| `--color-error` | Ruby | `#EF4444` | Error states, denied actions |

**Dark-first design:** The primary palette is dark-mode native (Deep Space Black background). Light mode is a secondary consideration — invert primary/secondary while maintaining accent and semantic colors.

**Typography:**

| Role | Font | Weight | Usage |
|---|---|---|---|
| Headlines | Outfit | 700 (Bold) | Section headers, modal titles, onboarding text |
| Body | Inter | 400 (Regular) | All body text, settings, descriptions |
| Technical | JetBrains Mono | 400/500 | Code blocks, tool call parameters, audit log entries, API metrics |

**Mascot integration:** The octopus mascot ("Master Tasker") appears in:
- Onboarding screens (full mascot with task icons)
- System agent avatar (simplified octopus head)
- Loading/empty states (subtle mascot illustration)
- Favicon and app icons (simplified head)
- README and marketing (full mascot on Deep Space Black)

### C.2.1 Three Product Variants

Omnipus ships as three product variants, all sharing the same `@omnipus/ui` component library and Go agentic core. **Open Source ships first.**

| Variant | Priority | Build tool | Shell | Gateway connection | Output |
|---|---|---|---|---|---|
| **Open source** (primary) | Ship first | Vite 6 | Browser | WebSocket to Go gateway on localhost | Static SPA → embedded in Go binary via `go:embed`. Community-facing. Similar to Omnipus/OpenClaw distribution. |
| **Desktop** | Ship second | Vite 6 + electron-builder | Electron v33+ | WebSocket to Go child process | DMG / EXE / AppImage. Polished, premium UX. Auto-updates. |
| **Cloud** (SaaS) | Ship third | Consumer's bundler | Host app's shell | WebSocket to cloud gateway | `@omnipus/ui` npm package. Scalable, multi-tenant, expanded integrations. |

**Design implications:**
- The UI is designed for the browser-embedded open source variant first — responsive web layout, no Electron dependencies.
- The Desktop variant adds Electron-specific features (window management, system tray, native menus, auto-update, file dialogs) on top of the same component library.
- The SaaS variant adds team features, billing, and managed infrastructure but reuses the same component library.
- All UI components must work in both browser and Electron contexts. Electron-specific features are isolated behind a shell abstraction layer.

### C.2.2 Component Architecture

```
@omnipus/ui (npm package — shared across all variants)
├── ChatPanel          (AssistantUI-based)
├── CommandCenter      (shadcn/ui)
├── AgentsView         (shadcn/ui)
├── SkillsBrowser      (shadcn/ui)
├── SettingsPanel      (shadcn/ui)
├── RichComponent      (inline + expandable content)
└── OmnipusProvider     (context: gateway URL, auth token, config)
```

Hosted variant: the host app imports `@omnipus/ui` components individually. No reimplementation. All three variants share the same React components. SSR compatible via `"use client"` directives. Style isolation via Tailwind prefix.

-----

## C.3 Icon System

### C.3.1 Icon Library: Phosphor Icons

Omnipus uses **Phosphor Icons** (`@phosphor-icons/react`) as its icon library.

| Property | Value |
|---|---|
| Library | Phosphor Icons |
| Package | `@phosphor-icons/react` |
| Total icons | ~9,000 |
| License | MIT (all free) |
| Styles/weights | 6 — thin, light, regular, bold, fill, duotone |
| Grid | 256×256 (scales to any size) |
| Tree-shakeable | Yes |
| TypeScript | Yes |

**Why Phosphor over alternatives:**

| Factor | Decision rationale |
|---|---|
| **Not "AI slop"** | Lucide (29M downloads/week) is the default in shadcn/ui, Cursor, v0, and every AI-generated template. Using it makes Omnipus look like every other AI app. Phosphor is distinctive. |
| **6 weights** | Thin, light, regular, bold, fill, and duotone enable visual hierarchy without mixing libraries. Use regular for UI chrome, bold for emphasis, thin for backgrounds, duotone for decorative contexts. |
| **9,000 icons** | Sufficient coverage for the emoji-to-icon translator (see C.3.2). Covers agent avatars, tool badges, status indicators, and obscure mapping needs. |
| **Expressive** | Icons can carry meaning the way emoji do — critical for the emoji-to-icon translator. Phosphor's variety in objects, concepts, and nature icons exceeds Lucide and Tabler. |
| **Professional aesthetic** | Balanced between friendly and professional. Not as geometric as Heroicons, not as playful as emoji. |

Alternatives evaluated and rejected: Lucide (too ubiquitous), Tabler (fewer icons/weights, slightly less expressive), Heroicons (only ~300 icons, missing robot/brain/database/antenna), Radix Icons (only ~300 icons, poor bundle performance), Hugeicons (paid tier for full styles).

### C.3.2 Emoji-to-Icon Translator

**Principle:** No emoji in the production UI. The emoji-to-icon translator applies **only to LLM chat output text and user chat input** — not to stored data or UI chrome. All stored icon values (agent icons, channel icons) use Phosphor icon names (e.g., `"robot"`, `"paper-plane-tilt"`), and the UI renders them directly as Phosphor components. The translator exists solely because LLMs naturally produce emoji in their chat output.

```
LLM output:  "🔍 Searching for AWS pricing..."
Chat renders: [PhosphorSearchIcon] Searching for AWS pricing...
```

The LLM does not need to know about the icon library. It uses emoji naturally. The React markdown renderer maps emoji codepoints to Phosphor icon components before display.

**Implementation approach:**

```tsx
const emojiToIcon: Record<string, { icon: Icon, weight?: IconWeight }> = {
  '🔍': { icon: MagnifyingGlass },
  '📄': { icon: File },
  '⚙️': { icon: Gear },
  '🔧': { icon: Wrench },
  '🛡️': { icon: Shield },
  '✅': { icon: CheckCircle, weight: 'fill' },
  '❌': { icon: XCircle, weight: 'fill' },
  '⚠️': { icon: Warning, weight: 'fill' },
  '💡': { icon: Lightbulb },
  '🔒': { icon: Lock },
  '💬': { icon: ChatCircle },
  '🤖': { icon: Robot },
  '🫀': { icon: Heartbeat },
  // ... ~80 mappings total
}
```

Applied as a custom remark plugin in the markdown renderer. Only applies to prose text — code blocks render emoji literally.

**Mapping categories (~80 total):**

| Category | Emoji mapped | Phosphor icons used |
|---|---|---|
| Actions | 🔍 ✏️ 🗑️ 📎 📋 ⬇️ ⬆️ 🔄 | MagnifyingGlass, Pencil, Trash, Paperclip, Clipboard, DownloadSimple, UploadSimple, ArrowsClockwise |
| Status | ✅ ❌ ⚠️ ⏸ ⟳ ● | CheckCircle, XCircle, Warning, Pause, SpinnerGap, Circle |
| Objects | 📄 📁 💻 🔧 🛡️ 🔒 🔑 ⚙️ | File, Folder, Laptop, Wrench, Shield, Lock, Key, Gear |
| Communication | 💬 📡 📮 🔔 ✉️ | ChatCircle, Broadcast, Mailbox, Bell, Envelope |
| Data | 📊 📈 💰 💎 | ChartBar, TrendUp, CurrencyDollar, Diamond |
| Agent/AI | 🤖 🧠 🔬 ✍️ 💡 🎯 ⚡ | Robot, Brain, Microscope, PencilSimple, Lightbulb, Target, Lightning |
| System | ⏰ 🫀 🌐 📝 | Clock, Heartbeat, Globe, Note |

**Behavior rules:**

| Rule | Detail |
|---|---|
| Code blocks | No replacement. Emoji renders literally. |
| User input | Also mapped. User types emoji, sees icon. Consistent both directions. |
| Unmapped emoji | Falls through to native platform emoji rendering. |
| Skin tone variants | Ignored. Map base emoji only. |
| Dark mode | Icons use `currentColor`, automatically adapt. Native emoji don't. |
| User override | Settings → Preferences → "Show native emoji" toggle. Default: icons. |

### C.3.3 Agent Profile Pictures

Agent avatars use Phosphor icons on colored circles. Icons are stored as Phosphor icon names in the data model (e.g., `"icon": "robot"`), not emoji. No emoji anywhere in stored data or UI chrome.

**Icon picker (in create agent modal and agent profile):**

Grouped by category: Agents & AI, Work, Development, Creative, Communication, Research, Security, Nature, Abstract. ~60 icons available for selection. Each renders as white icon on the agent's color circle.

**Color palette (7 named colors):**

| Name | Hex | Default assignment |
|---|---|---|
| Green | `#22C55E` | General Assistant |
| Blue | `#3B82F6` | Available |
| Purple | `#A855F7` | Researcher |
| Yellow | `#EAB308` | Content Creator |
| Orange | `#F97316` | Available |
| Red | `#EF4444` | Available |
| Gray | `#6B7280` | Available |

**Core agent defaults:**

| Agent | Phosphor icon | Color | Rendering |
|---|---|---|---|
| Omnipus | Custom octopus SVG | Brand color | Octopus on brand circle |
| General Assistant | Robot | Green | White Robot on green circle |
| Researcher | Microscope | Purple | White Microscope on purple circle |
| Content Creator | PencilSimple | Yellow | White Pencil on yellow circle |

**Avatar sizes across the UI:**

| Context | Size |
|---|---|
| Session bar | 24px |
| Activity feed, task cards | 24px |
| Chat message headers | 32px |
| Agent cards (grid) | 48px |
| Agent profile page | 64px |

**Custom image upload:**

| Property | Spec |
|---|---|
| Accepted formats | PNG, JPG, WebP, SVG |
| Max file size | 1MB |
| Auto-processing | Cropped to square, resized to 128×128 |
| Storage | `~/.omnipus/agents/<agent-id>/avatar.png` |
| Fallback | If no image uploaded, Phosphor icon on color circle |

**Color usage beyond avatars:**

Agent color appears as subtle accents: task card project dots, activity feed agent indicators, session list color bars. Not colored borders everywhere — just enough to distinguish agents at a glance.

-----

## C.4 Core Design Principles

| Principle | Rule |
|---|---|
| **Chat-first** | Chat is the home screen. Everything else is secondary. |
| **Zero-step connection** | Opening the app = gateway starts = connected = ready to chat. No manual "start gateway" button. |
| **Sidebar defaults to overlay** | Sidebar is an overlay drawer by default, maximizing content area. Users can pin it open for persistent navigation. Chat-first means chat gets full width until the user opts for pinned navigation. |
| **No separate canvas** | Rich content renders inline in chat. Expands to fullscreen when needed. Pin to persist. |
| **Inline-expand pattern** | Small edits happen inline. Deep browsing in fullscreen overlays. Consistent everywhere. |
| **Gateway is invisible** | Users never see "gateway," "WebSocket," or "port 18800." The app just works. |
| **Settings are not primary navigation** | Settings at bottom of sidebar, separate from operational screens. |
| **Problems are loud, healthy is quiet** | Attention items are prominent. Normal state is calm and minimal. |

-----

## C.5 Navigation Structure

### C.6.1 Sidebar (Overlay Drawer with Pin Option)

The sidebar defaults to an **overlay drawer** but can be **pinned** to remain permanently visible on desktop/tablet.

| Position | Item | Phosphor Icon |
|---|---|---|
| Top | **Chat** | ChatCircle |
| | **Command Center** | Gauge |
| | **Agents** | Robot |
| | **Skills & Tools** | Puzzle |
| Bottom | **Settings** | Gear |
| Bottom | **Pin sidebar** | PushPin (unpinned) / PushPinSlash (pinned) |

**Overlay mode (default):**
- Default state: closed. Content always has full width.
- Open: hamburger (☰) or `Cmd+B` / `Ctrl+B`. Slides in from left with drop shadow.
- No dimmed backdrop. Content behind stays fully visible and clickable.
- Close triggers: click outside sidebar, press Escape, click ☰, or click any nav item.
- Clicking a nav item navigates AND closes the sidebar.

**Pinned mode:**
- Click the pin icon at the bottom of the sidebar to pin it open.
- Sidebar becomes a permanent panel on the left. Content area shrinks to accommodate.
- Navigation clicks switch the view without closing the sidebar.
- Pin state persists in user preferences.
- Useful for power users who switch between views frequently (agents, skills, settings).
- On phone breakpoints: pin is not available — always overlay mode.

**Responsive behavior:**
- Desktop (>1024px): overlay or pinned (user choice).
- Tablet (768–1024px): overlay or pinned (user choice).
- Phone (<768px): always overlay, pin icon hidden.

### C.6.2 Session/Agent Hierarchy (Slide-over Panel)

Accessed from `[< Sessions]` or ☰ in the session bar. Slides over from the left within the chat screen.

```
AGENTS
▼ 🤖 Work Agent
  │ AWS Pricing Research ●
  │ Weekly Report Draft
  │ + New session
▼ 🤖 Personal Agent
  │ Travel Planning
  │ + New session
▶ 🤖 DevOps Agent (3 sessions)
```

Accordion-style: select agent → shows sessions. Select session → loads in chat. Green dot on active session.

-----

## C.6 Screen Specifications

### C.6.1 Chat Screen

The primary interface. Where 80% of the user experience lives.

**Layout:**
```
┌─────────────────────────────────────────────────────────┐
│ ☰  Agent: Work ▼  Model: opus ▼   🫀 28m  💰 $0.42  ⟳ │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  Message thread (scrollable, full width)                │
│  - User messages                                        │
│  - Agent messages (streaming, markdown, code)           │
│  - Tool call badges (collapsible)                       │
│  - Approval prompts (inline action buttons)             │
│  - File outputs (preview + download)                    │
│  - Rich components (expand + pin)                       │
│                                                         │
├─────────────────────────────────────────────────────────┤
│ [📎] [📷] [🎤]  Type a message...                 [⬆] │
└─────────────────────────────────────────────────────────┘
```

#### C.6.1.1 Session Bar

```
☰  [< Sessions]  Agent: Work ▼  Model: claude-opus ▼  🫀 28m  💰 $0.42  ⟳ 12,847 tokens
```

| Element | Behavior |
|---|---|
| ☰ | Opens sidebar drawer |
| `< Sessions` | Opens agent/session hierarchy slide-over |
| Agent selector | Dropdown to switch active agent |
| Model selector | Dropdown grouped by provider |
| 🫀 Heartbeat | Time until next heartbeat. Click for last result. |
| 💰 Cost | Session total. Click for per-message breakdown. |
| ⟳ Tokens | Context window usage. Click for progress bar. |

#### C.6.1.2 Rich Content Rendering

| Content type | Renderer |
|---|---|
| Markdown (headings, lists, bold, links, tables) | react-markdown + remark-gfm |
| Mermaid diagrams | mermaid.js (lazy-loaded, inline render) |
| Code blocks | Shiki (syntax highlighting, copy button) |
| LaTeX / math | remark-math + rehype-katex |
| Images (agent-generated) | Inline with lightbox |
| File output | Custom component: thumbnail + `[↗ Preview]` + `[⬇ Download]` |
| Tool calls | Generic + custom component registry |

#### C.6.1.3 Message Types

**User message:** Text, images (drag-drop/paste), file attachments. Timestamp.

**Agent message (streaming):** Token-by-token rendering. Cursor (█) shows active stream position. `⟳ thinking` shown before first token. Token count + cost below each message.

**System messages:** Centered, muted. Heartbeat results, session resumed, context window warnings.

#### C.6.1.4 Tool Call Components

**Architecture:** Extensible registry. Custom components per tool, generic fallback.

```tsx
const toolComponents: Record<string, React.ComponentType<ToolCallProps>> = {
  'web_search': WebSearchResult,
  'web_fetch': WebFetchPreview,
  'exec': TerminalOutput,
  'file.read': FileReadPreview,
  'file.write': FileWriteConfirm,
  'file.list': FileTreeView,
  'browser.navigate': BrowserScreenshot,
  'spawn': SubAgentCard,
  'cron.add': CronScheduleCard,
  'memory.store': MemoryCard,
  // MCP/ClawHub tools use GenericToolCall
}
```

**States:**
| Badge | Meaning |
|---|---|
| ⟳ (spinning) | Running — dashed border, progress indicator |
| ✓ (green) | Success — solid border, collapsed by default |
| ✗ (red) | Failed — red badge, retry button, error visible |
| ⏸ (yellow) | Waiting for approval |

**Collapsed (default after completion):**
```
🔧 web_search ✓  0.3s  ▸ Click to expand
```

**Expanded:**
```
🔧 web_search ✓  0.3s                    [Copy] [Rerun]
┌─────────────────────────────────────────┐
│ Parameters:                             │
│   query: "AWS m5 instance pricing"      │
│ Result:                                 │
│   Found 5 results:                      │
│   1. aws.amazon.com/ec2/pricing...      │
└─────────────────────────────────────────┘
```

**Built-in tools with custom components:**

| Tool | Custom component | Renders as |
|---|---|---|
| web_search | ✅ | Search result cards with title, URL, snippet |
| web_fetch | ✅ | URL header + content preview |
| browser.navigate | ✅ | Inline screenshot thumbnail |
| browser.screenshot | ✅ | Inline image with lightbox |
| exec | ✅ | Terminal block: command, stdout, exit code badge |
| file.read | ✅ | Filename + syntax-highlighted preview |
| file.write | ✅ | Filename + path + diff preview |
| file.list | ✅ | Tree view with file icons |
| cron.add/remove | ✅ | Human-readable schedule + next run |
| spawn | ✅ | Sub-agent card: label, task, status, result |
| memory.store/recall | ✅ | Memory card: key, content, timestamp |
| message | ✅ | Channel icon + recipient + message preview |
| image.analyze | ✅ | Inline thumbnail + analysis text |
| MCP / ClawHub tools | Generic | JSON parameters + result |

#### C.6.1.5 Exec Approval Block

```
┌─────────────────────────────────────────────┐
│ ⚠️ Agent wants to execute:                  │
│  $ git pull origin main                     │
│ Working directory: ~/projects/omnipus        │
│ Matched policy: tools.exec.approval=ask     │
│ [✓ Allow]  [✗ Deny]  [✓+ Always Allow]     │
└─────────────────────────────────────────────┘
```

Shows command, working directory, policy rule that triggered approval. Three action buttons.

#### C.6.1.6 File Output Component

```
┌─────────────────────────────────────────────┐
│ 📄 quarterly_report.pdf         2.3 MB      │
│ ┌─────────────────────────────────┐         │
│ │  Thumbnail preview              │         │
│ └─────────────────────────────────┘         │
│ [📌 Pin]  [↗ Preview]  [⬇ Download]        │
└─────────────────────────────────────────────┘
```

| File type | Inline preview | Fullscreen preview | Download |
|---|---|---|---|
| PDF | First page thumbnail | Full PDF viewer | ✅ |
| DOCX | File icon + name | Rendered as HTML | ✅ |
| PPTX | First slide thumbnail | Slide-by-slide viewer | ✅ |
| CSV/Excel | First 5 rows mini table | Full table viewer | ✅ |
| Image | Inline render | Lightbox fullscreen | ✅ |
| HTML | Compact rendered preview | Fullscreen sandboxed render | ✅ |
| Mermaid | Inline rendered diagram | Fullscreen with zoom/pan | ✅ as SVG/PNG |
| Code files | Syntax-highlighted snippet | Full file with line numbers | ✅ |
| Other/binary | File icon + metadata | No preview | ✅ |

#### C.6.1.7 Inline Content Actions

Every rich component in chat has contextual action buttons:

| Content | Actions |
|---|---|
| Rich output (dashboard, chart) | `[📌 Pin]` `[↗ Expand]` `[📋 Copy]` |
| File | `[📌 Pin]` `[↗ Preview]` `[⬇ Download]` |
| Code block | `[📌 Pin]` `[↗ Expand]` `[📋 Copy]` |
| Mermaid diagram | `[📌 Pin]` `[↗ Expand]` `[⬇ SVG]` |
| Image | `[📌 Pin]` `[↗ Expand]` `[⬇ Download]` |
| Plain text response | `[📌 Pin]` `[📋 Copy]` |

**Pin** saves the response as a persistent artifact accessible from a pinned items list.

**Expand/Preview** opens the content fullscreen with a mini chat input at the bottom for in-place iteration with the agent.

#### C.6.1.8 Fullscreen Preview

```
┌─────────────────────────────────────────────────────────┐
│ 📄 Q1_Report.pdf                   [⬇ Download]  [✕]   │
├─────────────────────────────────────────────────────────┤
│                                                         │
│         Full content at full width/height               │
│                                                         │
├─────────────────────────────────────────────────────────┤
│ [💬]  "Make the executive summary shorter"         [⬆] │
└─────────────────────────────────────────────────────────┘
```

Same pattern for all content: file previews, expanded charts, diagrams, interactive components. Mini chat enables iteration without returning to main thread. Agent updates the content in-place.

#### C.6.1.9 Composer

```
[📎 Files] [📷 Photo] [🎤 Voice]  Type a message... (Shift+Enter newline)  [⬆]
```

| Element | Behavior |
|---|---|
| 📎 Files | System file picker. Any type. |
| 📷 Photo | Image picker / camera on mobile |
| 🎤 Voice | Hold-to-record or toggle voice mode |
| Drag-drop | Files anywhere on chat panel |
| Cmd+V paste | Paste screenshots directly |
| Text input | Auto-grows up to 6 lines |
| Slash commands | `/session new`, `/pins`, `/agent switch work` — autocomplete popup |
| @ mentions | `@work` routes to specific agent in multi-agent setup |

-----

### C.6.2 Command Center

Single-column scrollable feed. Combines system monitoring (dashboard) with work management (GTD task board).

**Layout:**
```
┌─────────────────────────────────────────────────────────┐
│ ☰  Command Center                            [+ Task]   │
├─────────────────────────────────────────────────────────┤
│                                                         │
│ ● Gateway online · 3 agents · 5/6 channels · $4.82     │
│                                                         │
│ ─── Attention (if any) ─────────────────────────────── │
│ [Inline approval blocks, alerts, warnings]              │
│                                                         │
│ ─── Projects & Tasks ───────────────────────────────── │
│ [All] [● Infra Q2] [● Product Launch] [● Ops] [+]     │
│                                                         │
│ 📥 INBOX  ⏭️ NEXT  ⟳ ACTIVE  ⏸ WAITING  ✓ DONE       │
│ [Draggable task cards with agent, project, status]      │
│                                                         │
│ ─── Agents ─────────────────────────────────────────── │
│ [Compact agent rows, expandable on click]               │
│                                                         │
│ ─── Activity ───────────────────────────────────────── │
│ [Reverse-chronological event feed]                      │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

#### C.6.2.1 Status Bar

Single line, muted: `● Gateway online · 3 agents · 5/6 channels · $4.82 today`

Turns orange/red if problems. Click any segment to navigate to relevant detail.

#### C.6.2.2 Attention Section

Only appears when items need attention. Collapses to nothing when all clear.

| Item type | Shows |
|---|---|
| Exec approval waiting | Command + inline [Allow] [Deny] [Always Allow] buttons |
| Heartbeat flagged | Issue description + [→ Open session] [🔇 Dismiss] |
| Channel disconnected | Channel name + error + [→ Settings] |
| Agent error | Error message + [→ Chat] |

#### C.6.2.3 Task Board

Two views, toggled via `[List | Board]` switch in the section header. Default: **List view**. Preference persists.

**List view (default):** Simple flat list of tasks grouped by status. Each row shows: task name, assigned agent, status badge, cost. Sortable by date, status, agent. Expandable rows for detail. Best for users who want a straightforward task overview without learning GTD methodology.

**Board view (GTD):** Kanban-style columns. Drag-and-drop between columns. Best for power users familiar with GTD or kanban workflows.

**Board columns:** Inbox → Next → Active → Waiting → Done

| Column | GTD Stage | Purpose |
|---|---|---|
| 📥 Inbox | Capture | Unclarified inputs. Not yet assigned or analyzed. |
| ⏭️ Next | Clarified + Organized | Clear tasks, assigned to agent, ready to execute. |
| ⟳ Active | Engage | Agent currently working. Live status. |
| ⏸ Waiting | Waiting For | Blocked on external input. Tracked. |
| ✓ Done | Complete | Finished. Archived after configurable period. |

**Task card:**
```
┌───────────┐
│ Task name │
│           │
│ 🤖 Agent  │
│ ⟳ status  │
│ $cost     │
│ ● project │
└───────────┘
```

Drag-and-drop between columns. Click → slide-over detail panel with description, activity log, files, linked session.

**Project bar:** Tab filter above the board. `[All Tasks]` shows everything. Click a project → filters to that project's cards. `[+]` creates a new project (name + color, minimal).

#### C.6.2.4 Agent Summary Rows

Compact, expandable on click:

```
Collapsed:
🤖 Work  ● active · opus · 3 tasks · $3.12        [→ Chat]

Expanded:
🤖 Work  ● active · opus · 3 tasks · $3.12        [→ Chat]
┌────────────────────────────────────────────────────────┐
│ Session: AWS Pricing Research · 2m ago                 │
│ Today: 8.2K tokens · 14 tool calls                    │
│ 🫀 Heartbeat: OK (next in 28m)                        │
│ Context: ██████░░░░ 34% used                          │
└────────────────────────────────────────────────────────┘
```

-----

### C.6.3 Agents Screen

#### C.6.3.1 Agent List (Profile Cards)

Responsive grid: 1 column (1-3 agents), 3 columns (4+). Cards show avatar, name, status, description, model, cost, badge (🔒 system / 🔒 core / ✏️ custom).

```
┌─────────────┐ ┌─────────────┐ ┌─────────────┐ ┌─────────────┐
│  🐙         │ │  🟢 🤖      │ │  🟣 🔬      │ │  🟡 ✍️      │
│  Omnipus     │ │  General    │ │  Researcher │ │  Content    │
│  System     │ │  Assistant  │ │             │ │  Creator    │
│  ● always on│ │  ● active   │ │  ● idle     │ │  ● idle     │
│  "Your guide│ │  "Versatile │ │  "Deep      │ │  "Long-form │
│   to Omnipus"│ │   helper"   │ │   research" │ │   writing"  │
│  🔒 system  │ │  🔒 core    │ │  🔒 core    │ │  🔒 core    │
│   [→ Chat]  │ │   [→ Chat]  │ │   [→ Chat]  │ │   [→ Chat]  │
└─────────────┘ └─────────────┘ └─────────────┘ └─────────────┘
```

`[+ New Agent]` at the bottom.

#### C.6.3.2 Agent Types

| Type | Badge | Prompts | Deletable | Editable |
|---|---|---|---|---|
| **System** | 🔒 system | Hardcoded in Go binary, hidden | No | Model only |
| **Core** | 🔒 core | Hardcoded in Go binary, hidden | Can deactivate | Model, tools, skills, heartbeat, name, picture |
| **Custom** | ✏️ custom | SOUL.md + AGENTS.md in workspace | Yes | Everything |

Core agent prompts are compiled into the binary: not visible, not editable, not stored as files, not accessible via file.read, versioned with releases.

#### C.6.3.3 Agent Profile Page

Single-column scrollable page. Sections vary by agent type:

| Section | System agent | Core agent | Custom agent |
|---|---|---|---|
| Avatar + name + description | ✅ (name editable) | ✅ Editable | ✅ Editable |
| Profile picture + color | ❌ | ✅ Editable | ✅ Editable |
| Model + fallbacks + advanced | ✅ | ✅ Editable | ✅ Editable |
| Identity (SOUL.md, AGENTS.md) | ❌ Hidden | ❌ Hidden | ✅ Editable |
| HEARTBEAT.md | ❌ | ✅ Editable | ✅ Editable |
| Tools & Skills | ❌ | ✅ Editable | ✅ Editable |
| Rate Limits | ❌ (exempt) | ✅ Editable | ✅ Editable |
| Stats | ✅ | ✅ | ✅ |
| Recent Activity | ✅ | ✅ | ✅ |
| Sessions | ✅ | ✅ | ✅ |
| Workspace Files | ❌ | ✅ | ✅ |
| Memory | ❌ | ✅ | ✅ |

**Rate Limits section:** Shows inherited global defaults with option to override per-agent. Fields: LLM calls/hour, tool calls/minute, outbound messages/minute. Each shows current usage alongside the limit as a progress bar. "Use global defaults" toggle (default on). Override values only editable when toggle is off.

#### C.6.3.4 Model Configuration (Advanced)

Default: single model dropdown grouped by provider.

`[⚙️ Advanced]` expands to:
- Fallback 1 (provider/model)
- Fallback 2 (provider/model)
- Parameters: temperature, max tokens, top P, top K (provider-specific visibility)
- Agent loop: max tool iterations, timeout per tool
- Context: bootstrap max chars
- `[Reset to defaults]`

#### C.6.3.5 Agent Activity View (Fullscreen)

Accessible via `[View all →]` on Recent Activity. Fullscreen overlay with:
- Search + filters (type, time range)
- Events grouped by day
- Each event shows: timestamp, tool/action, status, session name, key detail
- Expandable rows for full parameters/results
- Pending approvals actionable inline
- File outputs with `[↗ View]` `[⬇ Download]`

#### C.6.3.6 Create Agent Modal

Four fields: profile picture (pick icon + color or upload), name (required), description (optional), model (dropdown with `[⚙️ Advanced]`). Color picker. `[Cancel]` `[Create Agent]`.

#### C.6.3.7 Agent Card Actions

Long-press or ⋯ menu: Duplicate, Delete. Delete requires confirmation with affected data listed. Duplicate creates a copy with fresh workspace/sessions/memory.

-----

### C.6.4 Skills & Tools Screen

**Tabs:** `[Installed Skills]` `[MCP Servers]` `[Channels]` `[Built-in Tools]`

#### C.6.4.1 Installed Skills Tab

List of installed skill cards showing: name, version, verification status, description, author, stars, agent assignment, `[↗ Details]` `[🗑️ Remove]`.

`[+ Browse for more skills]` at bottom → opens skill browser fullscreen overlay with search, trending/new/popular filters, category filter, install cards with `[Install]` button.

**Install modal:** Skill name + tools provided + required credentials (with input fields) + agent assignment checkboxes. `[Cancel]` `[Install]`.

**Skill detail (fullscreen):** Description, tools provided, requirements, SKILL.md preview, agent access, version history with changelog, `[⬆ Update]` `[🗑️ Remove]`.

#### C.6.4.2 MCP Servers Tab

List of connected MCP server cards: name, package, transport type, connection status, tools discovered, agent assignment.

Expandable detail: discovered tools list, agent access with per-tool allow/deny, environment variables, `[✏️ Edit Config]` `[🗑️ Remove]`.

**Add MCP Server modal:** Name, transport (stdio/SSE/HTTP), command + args (stdio) or URL (SSE/HTTP), environment variables, agent assignment. `[Cancel]` `[Connect & Add]`.

#### C.6.4.3 Channels Tab

Channels use a technology-independent provider model. Native Go channels, bridged channels (wrapping external implementations via stdin/stdout JSON protocol), and community channels all appear identically in the UI.

Three sections: Enabled channels (with status, `[Configure]` `[Disable]`), Available channels (bundled but not yet enabled, `[Enable]`), Community (`[+ Add custom channel]`).

Bridged channels show `"Requires: Java installed"` or similar when the bridge runtime is needed.

WhatsApp shows dual-mode selection on enable: `[Personal (free — QR scan)]` or `[Business API (paid)]`.

#### C.6.4.4 Built-in Tools Tab

List of all built-in tools. Each tool expandable inline with:
- **Global configuration:** Approval mode, timeout, provider selection, rate limits, credential status — editable here.
- **Agent access:** Which agents have this tool, with override indicators (`⚙️ override: timeout 120s`).
- **Usage stats:** Calls this week, denied, waiting.

Global defaults set here. Per-agent overrides set on agent profile.

-----

### C.6.5 Settings Screen

Six sections, each a navigation card on the settings main page. Click → section detail page. Back button returns.

#### C.6.5.1 Profile & Preferences
Name, timezone, language, theme (light/dark/system), font size.

#### C.6.5.2 Providers
Configured providers (with connection status, API key management, available models). Available providers (with `[+ Configure]` → inline expansion for API key + endpoint + `[Save & Connect]`).

#### C.6.5.3 Routing & Policies
Channel routing rules (which users/groups route to which agents), allow_from restrictions per channel, DM policy configuration. This section configures HOW channels route messages — channel install/enable is in Skills & Tools → Channels.

#### C.6.5.4 Security & Policy
- **Policy:** Default policy mode (allow/deny), exec approval, SSRF protection.
- **Prompt Injection Defense:** Strictness level selector (Low / Medium / High) with a one-line description of each level's behavior. Default: Medium. Detail of what each level does is defined in the technical spec (SEC-25); the UI exposes the selector and explains the tradeoff (lower = fewer false positives but less protection, higher = more aggressive filtering but may block legitimate content).
- **Rate Limits & Cost Control:** Global daily cost cap (USD) with progress bar showing today's spend. Per-agent default limits (LLM calls/hour, tool calls/minute, outbound messages/minute). Per-channel outbound rate limits (auto-populated with platform defaults, editable). Current usage shown alongside each limit. When a limit is hit, agent shows a system message in chat: *"Rate limit reached (30 tool calls/min). Retrying in 28s..."*
- **Credentials Vault:** Encrypted credentials list, add/edit/remove, encryption status.
- **Audit Log:** Log output config, redaction patterns. `[↗ View Audit Log]` → fullscreen overlay with search, filters, explainable decisions per entry. *(Tamper-evident chain toggle descoped v1.0.)*
- **Device Trust:** Paired devices list with role and remove option.
- **Diagnostics:** `[Run omnipus doctor]` button, last run results, risk score, recommendations.

#### C.6.5.5 Gateway
Bind address (localhost/LAN/all), port, auth mode (token/password/none), gateway token with rotate/copy, Tailscale setup, SSH tunnel instructions, connection status.

#### C.6.5.6 Data & Backup
Create backup (with optional encryption), previous backups list, restore from backup, data management (workspace size, session count, memory entries, clear actions with warnings).

#### C.6.5.7 About
Version, available updates with changelog, platform diagnostics, system info copy, open source licenses.

-----

## C.7 Onboarding Flow

### C.7.1 First Launch

**Step 1: Provider setup (only non-chat UI)**

Minimal welcome screen with provider selection buttons. User picks provider → enters API key → `[Connect]` → connection tested → screen transitions to chat.

This is the only time the user sees a non-chat setup screen. It exists because there's no agent without a provider (chicken-and-egg).

**Step 2: Omnipus system agent takes over in chat**

After provider connects, the user is in the normal chat interface. The Omnipus system agent (🐙) introduces itself and presents preconfigured agents to activate:

```
🐙 Omnipus
You're connected! I'm Omnipus, your system assistant.

You have 3 agents ready to activate:

[🟢 🤖 General Assistant — ✅ Activated]
[🟣 🔬 Researcher — Activate]
[🟡 ✍️ Content Creator — Activate]

General Assistant is active by default.
Want to start chatting, or set up anything else?

[→ Chat with General Assistant]
[Set up a channel]
[Create a custom agent]
[I'm good, just let me explore]
```

Every option leads to more conversation with the Omnipus agent OR exits to chatting with an activated agent. The user controls the pace.

### C.7.2 Onboarding Principles

| Principle | Rule |
|---|---|
| Provider setup is the only non-chat UI | Everything else is conversational via Omnipus agent |
| Under 30 seconds to first chat | Provider → key → connect → chatting |
| Channel setup is deferred | Not part of onboarding. Discoverable later via Omnipus agent or Settings. |
| Agent naming is deferred | Preconfigured agents have names already. Custom agents created later. |
| Never shown again | After first setup, app opens to last used agent's chat |
| Resume if interrupted | Closing mid-onboarding: provider saved if completed, resume from next step on relaunch |

-----

## C.8 Project Model

**Status:** Conceptual — to be detailed in separate specification.

| Property | Description |
|---|---|
| Name | User-defined, required |
| Description | Optional |
| Color | Visual identifier on board cards and project tabs |
| Agent(s) | Which agents work on this project |
| Tasks | Cards on the GTD board |
| Workspace directory | `workspace/projects/<slug>/` |
| Memory | Project-scoped, isolated from other projects |
| Instructions | Optional project-specific agent instructions |
| Secrets | Optional project-scoped credentials |

**Board integration:** Every project appears as a filter tab on the command center task board. Tasks show their project color dot. `[All Tasks]` shows everything. Tasks without a project appear under All Tasks only.

**Context isolation:** When an agent works on a project task, it gets that project's memory, files, and instructions. No bleed between projects.

**No enforcement:** User decides what a project is. No required fields beyond name. No mandatory workflows.

-----

## C.9 Responsive Behavior

### C.9.1 Breakpoints

| Size | Width | Typical device |
|---|---|---|
| Desktop | >1024px | Laptop, desktop monitor |
| Tablet | 640–1024px | iPad, small laptop |
| Phone | <640px | Mobile phones |

### C.9.2 Sidebar (All Screens)

| Desktop | Tablet | Phone |
|---|---|---|
| Overlay drawer, ~250px wide | Same | Full-width overlay with dimmed backdrop |
| Content behind visible + clickable | Same | Content behind dimmed (phone sidebar IS the screen) |

### C.9.3 Chat Screen Adaptation

| Element | Desktop | Tablet | Phone |
|---|---|---|---|
| Session bar | Full: ☰ + Sessions + Agent + Model + 🫀 + 💰 + ⟳ | Compact: ☰ + Agent + Model. 🫀 💰 ⟳ behind `[⋯]` | Minimal: ☰ + Agent name only. All else behind `[⋯]` |
| Message width | Max ~720px centered (readable line length) | Max ~720px or full width | Full width with padding |
| Tool call badges | Full detail inline | Same | Collapsed: icon + name + status only. Tap to expand. |
| Exec approval | Full with 3 buttons side by side | Same | Buttons stack vertically |
| File output | Thumbnail + buttons side by side | Same | Thumbnail above, buttons below |
| Composer | All icons: 📎 📷 🎤 + input + ⬆ | Same | 📎 only (others behind `[+]` menu) + input + ⬆ |

### C.9.4 Command Center Adaptation

| Element | Desktop | Tablet | Phone |
|---|---|---|---|
| Status bar | Single line | Same | Two lines (wraps) |
| Project tabs | Horizontal scroll | Same | Same |
| GTD Board | All 5 columns visible | 3 visible + horizontal scroll | Swipe between columns, each ~85% width |
| Task cards | Full detail | Same | Compact (shorter description) |
| Agent rows | Full with [→ Chat] | Same | Name + status only, tap to expand |
| Activity feed | Full detail | Same | Timestamp + icon + tool. Tap for detail. |

Phone GTD board uses standard mobile kanban swipe pattern: each column takes most of screen width with peek of next column. Long-press to pick up card, swipe to move between columns.

### C.9.5 Agents Screen Adaptation

| Element | Desktop | Tablet | Phone |
|---|---|---|---|
| Card grid | 3 columns (4+), 1 column (1-3) | 2 columns | 1 column full-width |
| Card content | Full detail | Same | Compact: description truncated to 1 line |
| Profile page | Single column | Same | Same (naturally responsive) |
| Create agent modal | Centered ~500px | Same | Full-screen modal |
| Activity/memory/file overlays | Full detail | Same | Compact rows, tap to expand |

### C.9.6 Skills & Tools Adaptation

| Element | Desktop | Tablet | Phone |
|---|---|---|---|
| Tab bar | Full | Same | Horizontal scroll if needed |
| Cards | Full detail | Same | Compact: author + stars same line |
| Expanded tool config | Labels beside controls | Same | Labels above controls (stacked) |
| Install / Add MCP modals | Centered ~500px | Same | Full-screen modal |

### C.9.7 Settings Adaptation

| Element | Desktop | Tablet | Phone |
|---|---|---|---|
| Section cards | Full width | Same | Same |
| Section detail pages | Labels beside controls | Same | Labels above controls (stacked) |
| Audit log overlay | Full detail per row | Same | Compact: decision + agent + tool. Tap for detail. |

### C.9.8 Fullscreen Overlays (All Screens)

| Desktop | Tablet | Phone |
|---|---|---|
| Nearly full viewport with small margin | Same | True full screen, no margin |
| [✕] close in top right | Same | [← Back] in top left |
| Mini chat at bottom | Same | Mini chat behind [💬] floating button. Tap to expand. |

### C.9.9 Responsive Patterns Summary

| Pattern | Rule |
|---|---|
| Single column content | Already responsive — works on all sizes |
| Card grids | 3 → 2 → 1 columns |
| Modals | Centered on desktop/tablet → full-screen on phone |
| Labels + controls | Side by side → stacked on phone |
| Kanban board | Side by side → horizontal swipe on phone |
| Data density | Full → compact with tap-to-expand on phone |
| Session bar | Progressive disclosure: full → compact → minimal |
| Composer | All icons → single attach button with submenu |
| Overlays | Margined → true fullscreen on phone |

### C.9.10 Responsive Anti-Patterns (What We Avoid)

| Anti-pattern | Why |
|---|---|
| Separate mobile app with different UI | One codebase, responsive. Same screens everywhere. |
| Hiding entire sections on mobile | Everything accessible. Compact, not hidden. |
| Bottom tab bar on mobile | Hamburger sidebar is consistent across all sizes. |
| Platform-specific navigation patterns | Same interaction model everywhere. |

-----

## C.10 Open Items (To Be Detailed)

| Topic | Status | Notes |
|---|---|---|
| Preconfigured core agents — full roster, personalities, icons, tool defaults | ✅ Defined | 3 core (General Assistant, Researcher, Content Creator) + 1 system (Omnipus). See Appendix D. |
| Omnipus system agent — full capability spec, available tools, operations it can perform | ✅ Defined | See Appendix D (Omnipus Agent Spec) |
| Interactive tour content and flow design | Parked | Content for guided walkthroughs |
| Omnipus agent knowledge base content | Parked | Documentation, best practices, agentic guidance |
| Project data model — API, storage format, config schema | Parked | Detailed integration spec needed |
| Custom tool call components — 15 built-in tools, individual design per tool | Parked | Each needs wireframe |
| Profile picture system — icon library, color palette, upload specs | ✅ Defined | Phosphor Icons, 7-color palette, custom upload specs. See C.3. |
| Pinned artifacts — storage, retrieval, management UI | ✅ Defined | Data model in E.8, system tools in D.4.9, UI access points specified. |

-----

*End of Appendix C*
