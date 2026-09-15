// KnowledgeRailPanel — the header shared by the reading rail's panels
// (ADR-067 FR-064, US-7 AS-9; the honesty rule of US-6 applied to a disclosure).
//
// ── Why this file exists ─────────────────────────────────────────────────────
// KnowledgeOutline.tsx and KnowledgeBacklinks.tsx each carried a private copy of
// this header, with a comment in both saying the copies were a deliberate
// consequence of a wave boundary and should "fold into one KnowledgeRailPanel
// when the rail itself is built". The rail is built, so they are folded.
//
// ── The rule this header enforces ───────────────────────────────────────────
// A COUNT AND ITS CAVEAT ARE ONE STATEMENT AND MUST NOT BE SEPARATED BY A
// DISCLOSURE. Both panels used to render a count on the header — which is always
// visible — and their qualifier ("this list is truncated", "this note's
// frontmatter could not be read") inside the collapsed body. With
// `collapsible` true the body starts closed, so a docked reader saw
// "LINKED MENTIONS 200" and nothing else: a confident number for a walk that a
// bound stopped. A closed disclosure is not a weaker warning than a tooltip; it
// renders nothing at all.
//
// So the header takes a `qualifier` that rides beside the count in the same
// always-visible row — the same treatment KnowledgeReader's rail toggles already
// used for `truncated`, applied to both panels so the two surfaces cannot
// disagree about the same fact. The terse `label` is what a sighted reader sees;
// `detail` is the full sentence, rendered screen-reader-only, because "200
// TRUNCATED" read aloud is not a sentence.

import type { ReactNode } from "react";
import { CaretDown, CaretRight } from "@phosphor-icons/react";

/** A caveat that must remain visible while the panel is collapsed. */
export interface KnowledgeRailQualifier {
  /** Terse, lower-case, two or three words at most: "truncated", "3 skipped". */
  label: string;
  /** The whole sentence, for assistive technology. Never abbreviated. */
  detail: string;
}

export interface KnowledgeRailPanelHeaderProps {
  title: string;
  /** Count of what is LOADED. Omit while loading — "0" would be a claim. */
  count?: number;
  collapsible: boolean;
  expanded: boolean;
  onToggle: () => void;
  testId: string;
  /** Caveats about the number beside them. Rendered in the always-visible row. */
  qualifiers?: KnowledgeRailQualifier[];
}

export function KnowledgeRailPanelHeader({
  title,
  count,
  collapsible,
  expanded,
  onToggle,
  testId,
  qualifiers,
}: KnowledgeRailPanelHeaderProps) {
  const body: ReactNode = (
    <>
      {collapsible &&
        (expanded ? (
          <CaretDown size={12} aria-hidden="true" />
        ) : (
          <CaretRight size={12} aria-hidden="true" />
        ))}
      <span className="font-medium uppercase tracking-wide">{title}</span>
      {count !== undefined && (
        <span
          className="text-[var(--color-muted)]"
          data-testid={`${testId}-count`}
        >
          {count}
        </span>
      )}
      {qualifiers?.map((q) => (
        <span
          key={q.label}
          data-testid={`${testId}-qualifier`}
          data-qualifier={q.label}
          className="text-[10px] uppercase tracking-wide text-[var(--color-warning)]"
        >
          {q.label}
          <span className="sr-only"> — {q.detail}</span>
        </span>
      ))}
    </>
  );

  const className =
    "flex w-full items-center gap-2 px-3 py-2 text-left text-[11px] text-[var(--color-secondary)]";

  if (!collapsible) return <h3 className={className}>{body}</h3>;

  return (
    <h3>
      <button
        type="button"
        tabIndex={0}
        data-testid={testId}
        aria-expanded={expanded}
        onClick={onToggle}
        className={`${className} hover:bg-[var(--color-surface-2)] transition-colors`}
      >
        {body}
      </button>
    </h3>
  );
}
