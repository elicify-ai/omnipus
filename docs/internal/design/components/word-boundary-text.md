# WordBoundaryText

Static text atom preserving the original string byte-for-byte. Native browser line breaking allows breaks at hyphens even with hyphens:none. The atom uses actual browser font metrics and its owner's available line width: a token that fits a whole line is an atomic inline box, while an oversized token remains ordinary text for the owner's overflow-wrap:break-word fallback. Whitespace is preserved. No strings are rewritten, no hyphens/word-joiners are substituted, and no character-count heuristic guesses a width.

The root can be span/p/h3 with caller presentation classes. ResizeObserver and font readiness refresh geometry. Zero/unmeasured geometry leaves source text intact. The text has no activation, keyboard target, pointer target, or animation; those manifest kinds truthfully do not apply. It is used in task title slots and full hover metadata without changing Tooltip.
