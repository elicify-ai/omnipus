# Composer redesign — concept assets

Design exploration for the chat composer rebuild (2026-07). Four variants were
mocked (`4-variants.png`, `comparison.png` vs `current-composer.png`); the
operator approved **variant A1** with the attach control moved into the context
row (`A1-detailed.png` — six states; `A1-real-styles.png` — rebuilt with the
real design-system classes and Phosphor icons, vision-verified).

## Post-A1 evolution (shipped, 2026-07-14)

The shipped composer departs from the A1 PNGs in one deliberate way, per
operator direction after using the A1 build:

- The **context row** (attach · agent · model · tokens) renders **bare on the
  black shell above the card** — no frame, no fill. Only the input surface
  (textarea + send/stop + attachment chips) reads as a card.
- The **background-activity pills** render **bare below the card**, not folded
  inside it.
- The "Agents can make mistakes" disclaimer line was **removed**.
- `--color-surface-2` was darkened `#1a1a1e → #141416` centrally so raised
  panels sit closer to the shell (flatter look). Hover/selected tints that
  rested on surface-1 were repointed to `surface-3` to keep the affordance.

The PNGs are kept as the historical record of the approved concept; the code
in `src/components/chat/ChatScreen.tsx` (OmnipusComposer) is authoritative for
the shipped layout.
