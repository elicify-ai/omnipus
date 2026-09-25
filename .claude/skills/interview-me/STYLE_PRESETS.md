---
name: spec-preview-style
description: Document template, styling, and SVG diagram reference for the interview-me spec preview published as a Claude Artifact. This is NOT a skill — it is a resource file read by the interview-me skill during preview generation.
---

# Spec Preview: Document Template & Diagram Reference

Claude reads this file when generating the spec preview for the Artifact tool. The preview is a **diagram-first document**: a reviewer should grasp the spec from the visuals alone, with prose as supporting detail. It is a page to *read and share*, not an interactive app — no comment boxes, no clipboard actions, no buttons.

**Artifact compatibility rules (hard constraints):**
- The Artifact tool wraps the file in its own `<!doctype html>…<head>…</head><body>` skeleton. Write ONLY page content: start with `<title>`, then one `<style>` block, then the document markup. NO `<!DOCTYPE>`, `<html>`, `<head>`, or `<body>` tags.
- Strict CSP: no external requests of any kind — no web fonts, no CDN scripts, no remote images. System font stack only; all diagrams are inline SVG.
- Responsive: relative units, `max-width:100%`. Every diagram and table sits inside a `.diagram-wrap` / `.table-wrap` with `overflow-x: auto` — the page body must never scroll horizontally.

## Design Tokens

```css
:root {
  /* Background */
  --bg-primary: #0a0f1c;
  --bg-section: #111827;
  --bg-card: #1a2236;

  /* Text */
  --text-primary: #f1f5f9;
  --text-secondary: #94a3b8;
  --text-muted: #64748b;

  /* Accent */
  --accent: #00d4aa;
  --accent-hover: #00f0c0;
  --accent-muted: rgba(0, 212, 170, 0.15);

  /* Status */
  --warn: #f59e0b;
  --danger: #ef4444;
  --info: #60a5fa;

  /* Borders & radius */
  --border: rgba(148, 163, 184, 0.15);
  --radius: 12px;

  /* Typography — system stack only (Artifact CSP blocks web fonts) */
  --font-body: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
  --font-mono: ui-monospace, 'SF Mono', Menlo, Consolas, 'Cascadia Code', monospace;
}
```

## Page Template

Replace `{{PLACEHOLDER}}` markers with generated content.

```html
<title>{{SPEC_TITLE}} — Spec</title>
<style>
  :root { /* paste Design Tokens block here */ }

  html { scroll-behavior: smooth; }
  body {
    background: var(--bg-primary);
    color: var(--text-primary);
    font-family: var(--font-body);
    font-size: 1rem;
    line-height: 1.7;
    padding: 2.5rem 1rem 4rem;
  }

  main { max-width: 860px; margin: 0 auto; }

  /* ============ HERO HEADER ============ */
  .hero { margin-bottom: 2.5rem; }
  .hero h1 { font-size: 2rem; line-height: 1.2; letter-spacing: -0.02em; }
  .hero .subtitle { color: var(--text-secondary); margin-top: 0.5rem; max-width: 60ch; }
  .meta-chips { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 1rem; }
  .chip {
    font-size: 0.78rem; font-weight: 600; padding: 4px 12px; border-radius: 999px;
    background: var(--accent-muted); color: var(--accent); border: 1px solid transparent;
  }
  .chip.warn { background: rgba(245, 158, 11, 0.15); color: var(--warn); }
  .chip.info { background: rgba(96, 165, 250, 0.15); color: var(--info); }
  .chip.muted { background: transparent; color: var(--text-muted); border-color: var(--border); }

  /* ============ AT A GLANCE ============ */
  .glance { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 12px; margin-bottom: 1.75rem; }
  .glance-card {
    background: var(--bg-section); border: 1px solid var(--border);
    border-radius: var(--radius); padding: 1rem 1.2rem;
  }
  .glance-card .label {
    font-size: 0.72rem; font-weight: 700; text-transform: uppercase;
    letter-spacing: 0.08em; color: var(--accent); margin-bottom: 0.3rem;
  }
  .glance-card .value { font-size: 0.92rem; color: var(--text-primary); line-height: 1.5; }
  .glance-card.risk .label { color: var(--warn); }

  /* ============ SECTIONS ============ */
  .section {
    background: var(--bg-section); border: 1px solid var(--border);
    border-radius: var(--radius); padding: 2rem; margin: 1.75rem 0;
  }
  .section h2 {
    font-size: 1.35rem; margin-bottom: 1rem; letter-spacing: -0.01em;
    display: flex; align-items: center; gap: 10px;
  }
  .section h2::before { content: ''; width: 4px; height: 1.1em; background: var(--accent); border-radius: 2px; }
  .section h3 { font-size: 1.05rem; margin: 1.25rem 0 0.5rem; color: var(--text-secondary); }
  .section-tldr {
    font-size: 0.95rem; color: var(--text-secondary); font-style: italic;
    border-left: 3px solid var(--accent); padding-left: 0.9rem; margin: 0 0 1rem;
  }
  .section p { margin: 0.6rem 0; }
  .section ul, .section ol { margin: 0.6rem 0 0.6rem 1.4rem; }
  .section li { margin: 0.3rem 0; }
  code { font-family: var(--font-mono); font-size: 0.88em; background: var(--bg-card); padding: 2px 6px; border-radius: 4px; }
  pre { background: var(--bg-card); border: 1px solid var(--border); border-radius: 8px; padding: 1rem; overflow-x: auto; margin: 0.75rem 0; }
  pre code { background: none; padding: 0; }
  blockquote { border-left: 3px solid var(--accent); padding-left: 1rem; color: var(--text-secondary); margin: 0.75rem 0; }

  /* ============ DIAGRAMS ============ */
  .diagram-wrap { overflow-x: auto; margin: 1.25rem 0; padding: 1rem 0.25rem; }
  .diagram-wrap svg { display: block; margin: 0 auto; min-width: 480px; max-width: 100%; height: auto; }
  .diagram-caption { text-align: center; color: var(--text-muted); font-size: 0.82rem; margin-top: 0.25rem; }

  /* ============ TABLES ============ */
  .table-wrap { overflow-x: auto; margin: 0.75rem 0; }
  table { border-collapse: collapse; width: 100%; font-size: 0.92rem; }
  th { text-align: left; color: var(--text-secondary); font-weight: 600; }
  th, td { padding: 10px 14px; border-bottom: 1px solid var(--border); vertical-align: top; }
  tbody tr:hover { background: rgba(148, 163, 184, 0.05); }

  /* ============ DECISIONS LOG (collapsible, no JS) ============ */
  details { border: 1px solid var(--border); border-radius: 8px; margin: 0.75rem 0; }
  summary { cursor: pointer; padding: 12px 16px; font-weight: 600; color: var(--text-secondary); }
  summary:hover { color: var(--text-primary); }
  details[open] summary { border-bottom: 1px solid var(--border); }
  details .table-wrap { padding: 0 16px 12px; }

  /* ============ TIMELINE (implementation order) ============ */
  .timeline { list-style: none; margin: 1rem 0 0 0; padding: 0; }
  .timeline li { position: relative; padding: 0 0 1.5rem 2.2rem; }
  .timeline li::before {
    content: attr(data-step); position: absolute; left: 0; top: 0;
    width: 26px; height: 26px; border-radius: 50%; background: var(--accent-muted);
    color: var(--accent); font-size: 0.78rem; font-weight: 700;
    display: flex; align-items: center; justify-content: center;
  }
  .timeline li:not(:last-child)::after {
    content: ''; position: absolute; left: 12px; top: 30px; bottom: 4px;
    width: 2px; background: var(--border);
  }
  .timeline .step-title { font-weight: 600; }
  .timeline .step-detail { color: var(--text-secondary); font-size: 0.9rem; }

  @media (max-width: 768px) {
    body { padding: 1.5rem 0.75rem 3rem; }
    .section { padding: 1.25rem; }
    .hero h1 { font-size: 1.5rem; }
  }
</style>

<main>
  <!-- Hero -->
  <header class="hero">
    <h1>{{SPEC_TITLE}}</h1>
    <p class="subtitle">{{ONE_SENTENCE_SUMMARY}}</p>
    <div class="meta-chips">
      <span class="chip">Draft for review</span>
      <span class="chip info">{{N_SECTIONS}} sections</span>
      <span class="chip muted">{{DATE}}</span>
      <!-- One chip per notable flag, e.g. <span class="chip warn">2 open risks</span> -->
    </div>
  </header>

  <!-- At a Glance: the 10-second read. 3-4 cards max. -->
  <div class="glance">
    <div class="glance-card"><div class="label">Problem</div><div class="value">{{PROBLEM_ONE_LINER}}</div></div>
    <div class="glance-card"><div class="label">Approach</div><div class="value">{{APPROACH_ONE_LINER}}</div></div>
    <div class="glance-card"><div class="label">Stack</div><div class="value">{{STACK_ONE_LINER}}</div></div>
    <div class="glance-card risk"><div class="label">Top Risk</div><div class="value">{{TOP_RISK_ONE_LINER}}</div></div>
  </div>

  <!-- FIRST section is always the big-picture flow diagram -->
  <section class="section">
    <h2>How It Works</h2>
    <div class="diagram-wrap">{{MAIN_FLOW_SVG}}</div>
    <p class="diagram-caption">{{MAIN_FLOW_CAPTION}}</p>
    <p>{{TWO_TO_FOUR_SENTENCE_OVERVIEW}}</p>
  </section>

  {{SECTIONS}}
  <!-- One .section per coverage area. Every section opens with a one-line TL;DR:
       <p class="section-tldr">{{ONE_LINE_GIST}}</p>
       Diagram-first: if the area describes a flow, sequence, or structure, the
       SVG diagram comes right after the TL;DR, then 2-5 sentences of prose.
       Only areas with no natural visual get prose/list/table alone. -->

  <!-- Decisions Log -->
  <section class="section">
    <h2>Decisions Log</h2>
    <details>
      <summary>{{N_DECISIONS}} decisions recorded — expand to view</summary>
      <div class="table-wrap">
        <!-- Six columns, in this order — same contract as the markdown spec's
             Decisions Log. Source is one of: Interview, Red Team, Drift Accepted. -->
        <table>
          <thead><tr><th>ID</th><th>Topic</th><th>Decision</th><th>Rationale</th><th>Source</th><th>Date</th></tr></thead>
          <tbody>{{DECISION_ROWS}}</tbody>
        </table>
      </div>
    </details>
  </section>

  <!-- Implementation Order -->
  <section class="section">
    <h2>Implementation Order</h2>
    <div class="diagram-wrap">{{DEPENDENCY_GRAPH_SVG}}</div>
    <p class="diagram-caption">Component dependency graph — build left to right</p>
    <ol class="timeline">
      {{TIMELINE_ITEMS}}
      <!-- <li data-step="1"><span class="step-title">Title</span><br><span class="step-detail">What and why</span></li> -->
    </ol>
  </section>
</main>
```

## SVG Diagram Reference

Diagrams are the heart of the preview. Draw every flow the interview surfaced:

| Interview content | Diagram to draw |
|---|---|
| System / architecture description | Horizontal component flow (client → API → services → storage) |
| User stories / journeys | Left-to-right user flow with decision branches |
| Data lifecycle | Data flow with store shapes |
| Error handling paths | Branch diagram off the happy path (danger-colored edges) |
| Dependency graph & build order | Layered DAG, left to right |

### Shared conventions

Every diagram SVG uses a `viewBox` (no fixed width/height attributes), inherits page fonts via CSS, and defines one arrowhead marker:

```html
<svg viewBox="0 0 760 {{H}}" role="img" aria-label="{{DESCRIPTION}}">
  <defs>
    <marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
      <path d="M0,0 L10,5 L0,10 z" fill="#64748b"/>
    </marker>
  </defs>
  <!-- edges first (under nodes), then nodes -->
</svg>
```

**Node shapes** (all text centered with `text-anchor="middle" dominant-baseline="middle"`, `font-size="13"`, `fill="#f1f5f9"`):

```html
<!-- Process step: rounded rect. Width ≈ 9px per label character + 32px, min 110 -->
<g>
  <rect x="20" y="20" width="130" height="46" rx="10" fill="#1a2236" stroke="rgba(0,212,170,0.4)"/>
  <text x="85" y="43" text-anchor="middle" dominant-baseline="middle" font-size="13" fill="#f1f5f9">Label</text>
</g>

<!-- Start / end: same rect but accent fill -->
<rect ... fill="rgba(0,212,170,0.15)" stroke="#00d4aa"/> <!-- text fill="#00d4aa" -->

<!-- Decision: diamond -->
<g>
  <path d="M340,20 L410,53 L340,86 L270,53 Z" fill="#1a2236" stroke="rgba(245,158,11,0.6)"/>
  <text x="340" y="53" text-anchor="middle" dominant-baseline="middle" font-size="12" fill="#f59e0b">valid?</text>
</g>

<!-- Data store: rect with double bottom border feel -->
<rect ... fill="#1a2236" stroke="rgba(96,165,250,0.5)" rx="4"/> <!-- text fill="#60a5fa" -->

<!-- External system: dashed border -->
<rect ... fill="none" stroke="#64748b" stroke-dasharray="5 4" rx="10"/> <!-- text fill="#94a3b8" -->
```

**Edges** — straight or elbow paths with the arrow marker; label edges where the branch matters:

```html
<path d="M150,43 L270,53" stroke="#64748b" stroke-width="1.5" fill="none" marker-end="url(#arrow)"/>
<text x="210" y="38" text-anchor="middle" font-size="11" fill="#94a3b8">yes</text>
<!-- Error/failure edges: stroke="#ef4444", label in the same red -->
```

### Layout rules

1. Flow left to right; ~56px horizontal gap between nodes, ~40px vertical gap between rows.
2. More than 5 nodes in a row → wrap to a second row with an elbow connector, or grow the viewBox width (the `.diagram-wrap` scrolls horizontally).
3. Branches: happy path stays on the main horizontal axis; alternatives branch below; error paths in `--danger` red.
4. Dependency graph: group nodes into vertical layers by build order (layer 0 = no dependencies, leftmost); edges always point right.
5. Keep labels short (1–3 words); put detail in the section prose, not the diagram.
6. Size the viewBox to the content — no dead space. Height = rows × 86 + 20.

### Worked example — request flow with validation branch

```html
<div class="diagram-wrap">
<svg viewBox="0 0 740 150" role="img" aria-label="Request flow: client to API, validation branch, then database">
  <defs>
    <marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
      <path d="M0,0 L10,5 L0,10 z" fill="#64748b"/>
    </marker>
  </defs>
  <path d="M130,45 L180,45" stroke="#64748b" stroke-width="1.5" fill="none" marker-end="url(#arrow)"/>
  <path d="M290,45 L340,45" stroke="#64748b" stroke-width="1.5" fill="none" marker-end="url(#arrow)"/>
  <path d="M470,45 L520,45" stroke="#64748b" stroke-width="1.5" fill="none" marker-end="url(#arrow)"/>
  <text x="495" y="38" text-anchor="middle" font-size="11" fill="#94a3b8">valid</text>
  <path d="M405,78 L405,110 L340,110" stroke="#ef4444" stroke-width="1.5" fill="none" marker-end="url(#arrow)"/>
  <text x="440" y="102" text-anchor="middle" font-size="11" fill="#ef4444">invalid</text>

  <g><rect x="20" y="22" width="110" height="46" rx="10" fill="rgba(0,212,170,0.15)" stroke="#00d4aa"/>
     <text x="75" y="45" text-anchor="middle" dominant-baseline="middle" font-size="13" fill="#00d4aa">Client</text></g>
  <g><rect x="180" y="22" width="110" height="46" rx="10" fill="#1a2236" stroke="rgba(0,212,170,0.4)"/>
     <text x="235" y="45" text-anchor="middle" dominant-baseline="middle" font-size="13" fill="#f1f5f9">API</text></g>
  <g><path d="M405,22 L470,45 L405,68 L340,45 Z" fill="#1a2236" stroke="rgba(245,158,11,0.6)"/>
     <text x="405" y="45" text-anchor="middle" dominant-baseline="middle" font-size="12" fill="#f59e0b">valid?</text></g>
  <g><rect x="520" y="22" width="120" height="46" rx="4" fill="#1a2236" stroke="rgba(96,165,250,0.5)"/>
     <text x="580" y="45" text-anchor="middle" dominant-baseline="middle" font-size="13" fill="#60a5fa">Database</text></g>
  <g><rect x="220" y="88" width="120" height="44" rx="10" fill="#1a2236" stroke="rgba(239,68,68,0.5)"/>
     <text x="280" y="110" text-anchor="middle" dominant-baseline="middle" font-size="13" fill="#ef4444">400 error</text></g>
</svg>
</div>
<p class="diagram-caption">Request validation flow</p>
```

## Section Content Generation Rules

The page is built for skimming — a reviewer should get the gist in three levels: **(1)** hero + At a Glance cards ≈ 10 seconds, **(2)** flow diagrams + section TL;DRs ≈ 1 minute, **(3)** full prose only where they care.

1. **At a Glance** — 3-4 cards, one line each: Problem, Approach, Stack, Top Risk. Ruthlessly short.
2. **How It Works** — always the first section, always has the main flow diagram. Generated from the requirement + pre-analysis summary.
3. **Every section starts with `<p class="section-tldr">`** — one sentence capturing that section's gist.
4. **Goals & Non-Goals** — Goals as `<ul>`, Non-Goals as a separate `<ul>` under an `<h3>`.
5. **One section per coverage area** — Technical Design, API Design, Data Model, Error Handling, etc. Diagram right after the TL;DR when the area has flow or structure; otherwise concise prose, lists, or tables.
6. **Decisions Log** — collapsible `<details>` table: #, Topic, Decision, Rationale. Collapsed by default.
7. **Implementation Order** — dependency graph SVG + the `.timeline` ordered list.

## Local-File Fallback

If the Artifact tool is unavailable and the preview is written as a local HTML file instead, wrap the exact same content in a full document:

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <!-- move the <title> and <style> here -->
</head>
<body>
  <!-- <main> content here -->
</body>
</html>
```
