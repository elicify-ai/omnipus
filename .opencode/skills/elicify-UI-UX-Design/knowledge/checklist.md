# The Scored Checklist

~90 checkable criteria across 11 domains. Each is a **DO** item with the
threshold or law that makes it enforceable. In REVIEW mode, a failed item is a
finding; cite the bracketed law/number. The five **always-on** domains (1, 4,
6, 7, 10) apply to nearly every surface; the rest are surface-specific.

Severity guide: **Critical** = broken / illegal / blocks the task ·
**Important** = measurably hurts outcomes · **Minor** = polish.

---

## 1. Cognitive load & memory  *(always-on)*

- [ ] Remove every element/style that conveys no meaning — extraneous load is
  wasted working memory. [Cognitive Load Theory, Sweller]
- [ ] Chunk long strings and lists into meaningful groups (card numbers in
  blocks of 4, phone numbers segmented). [Miller's Law]
- [ ] Limit a single group of related choices/nav items to ~5–7. [Miller's Law]
- [ ] Show options / recently-used / autocomplete instead of forcing recall;
  never make users re-type info from a prior screen. [Recognition over Recall]
- [ ] Every icon has a text label or tooltip. [Recognition over Recall]
- [ ] Most important items placed first and last in any list. [Serial Position]
- [ ] Exactly one element is visually dominant per screen/section — if
  everything is emphasized, nothing is. [Von Restorff] **(Critical if 3+ equal
  "primary" CTAs compete.)**

## 2. Decision-making & action

- [ ] Options reduced at each decision point; one recommended/default choice
  highlighted. [Hick's Law / Choice Overload]
- [ ] Long/complex processes broken into smaller sequential steps. [Hick's Law]
- [ ] The target action is made *easy* (fewer steps/fields) before relying on
  persuasion; prompt when ability is already high. [Fogg B=MAP]
- [ ] Defaults are the safe/beneficial option — never a pre-checked add-on or
  opt-out trap. [Status-quo bias] **(Critical = dark default.)**
- [ ] Browse/landing surfaces designed for fast scanning (System 1); compare/
  checkout surfaces give the detail deliberation needs (System 2). [Dual-process]

## 3. Speed, targets & responsiveness  *(numbers are hard limits)*

- [ ] Primary tap/click targets large and near the point of attention; exploit
  edges/corners. [Fitts's Law]
- [ ] Touch targets ≥ 24×24 px (WCAG 2.5.8 AA); prefer 44×44 px. **(Critical if
  below 24px with no spacing exception.)**
- [ ] Visible feedback for any action within ~100 ms; sub-400 ms target for
  command response. [Doherty Threshold]
- [ ] LCP ≤ 2.5 s, INP ≤ 200 ms, CLS ≤ 0.1 (75th percentile). [Core Web Vitals]
- [ ] No layout shift after load — reserve space for images/embeds/ads. [CLS]
- [ ] >10 s operations show a progress bar + time estimate + exit/background
  option. [Nielsen response limits]

## 4. Visual hierarchy & layout  *(always-on)*

- [ ] One primary action per page/section; secondary and tertiary visibly
  quieter (filled → outline/muted → text-only button tiers).
- [ ] Hierarchy built from size **+** weight **+** color together, not size
  alone; use a 3-tier text-color ramp (dark / medium grey / light grey).
- [ ] Spacing uses a consistent 8px (or 4px) scale — every margin/padding a
  multiple of the base unit. [8-pt grid]
- [ ] Spacing is unambiguous: a label sits visibly closer to the element it
  describes than to the next group. [Proximity / Gestalt]
- [ ] Content width is constrained; the screen is not force-filled.
- [ ] Every element aligns to a shared edge/axis; the number of alignment axes
  is minimized.
- [ ] Patterns are consistent — a button looks and behaves the same everywhere;
  radii, shadows, type styles reused from tokens. [Jakob's Law / consistency]

## 5. Typography  *(see visual-system.md for the full scale)*

- [ ] Body text ≥ 16px on web (≥14px only for dense UI); nothing below 12px.
- [ ] Body line-height 1.5–1.6; headings/display 1.1–1.3.
- [ ] Line length 45–75 characters (~66 ideal; `max-width: 65ch`); mobile 30–50.
  **(Important if >85 chars.)**
- [ ] ≤ 2 typefaces and ≤ 7 distinct sizes; a modular scale (ratio ≥ 1.2).
- [ ] Body copy left-aligned; never justified, never centered for long
  paragraphs.

## 6. Color & contrast  *(always-on — contrast is legal)*

- [ ] Normal text contrast ≥ 4.5:1; large text (≥24px or 18.66px bold) ≥ 3:1.
  [WCAG 1.4.3] **(Critical = fail.)**
- [ ] UI component borders, icons, focus rings, and state changes ≥ 3:1.
  [WCAG 1.4.11]
- [ ] Information never conveyed by color alone; links carry a non-color cue
  (underline) + 3:1 vs surrounding text. [WCAG 1.4.1]
- [ ] A defined palette: 5–10 shades per color, 2–3 base hues + grey ramp +
  semantic colors; ~60-30-10 neutral/secondary/accent distribution.
- [ ] Greys carry slight warm/cool saturation (not pure #808080).

## 7. Happy-path & friction  *(always-on for any task flow)*

- [ ] The next/primary action is self-evident at a glance; CTA label states the
  intent ("Place order", not "Submit"). [Krug]
- [ ] Total cost / outcome shown up front — no surprises deferred to a later
  step. [Baymard: #1 abandonment cause] **(Critical for checkout.)**
- [ ] Forms ask the minimum (target 6–8 fields for checkout); single column,
  persistent top-aligned labels (never placeholder-as-label), easiest fields
  first. [Baymard / Wroblewski]
- [ ] Smart defaults pre-fill where a common answer exists (never for sensitive/
  consent fields).
- [ ] Inline validation on field-exit (not premature while typing); input is
  **preserved** on error; errors are plain-language with a suggested fix.
  [Wroblewski +22% completions] **(Critical if error wipes input.)**
- [ ] Required vs optional fields explicitly marked; optional fields eliminated
  first.
- [ ] Commit step is the most prominent element; no new info/fees introduced at
  review; destructive actions de-emphasized or removed.
- [ ] Clear success state confirms what happened and what's next; back button
  works; mistakes are cheap and reversible.
- [ ] Multi-step flows show a step tracker ("Step 2 of 4"). [Zeigarnik / goal-grad]

## 8. States: loading · empty · error  *(every data-driven view)*

- [ ] < 1 s loads show no spinner; short blocking actions (1–10 s) a spinner;
  full-page content loads a **skeleton** (feels 30–50% faster); > 10 s a
  determinate progress bar.
- [ ] Skeletons reveal real content immediately as data arrives (not a splash).
- [ ] Optimistic UI for high-frequency actions (like/send/add) with rollback on
  error.
- [ ] Empty states explain *why* it's empty + show what's possible + one
  prominent CTA — never a bare "No data".
- [ ] Error messages explain + empathize + give a fix; tone matches severity;
  never blame the user.

## 9. Motion & micro-interactions  *(see visual-system.md for timings)*

- [ ] UI transitions 200–300 ms (small) / 300–500 ms (large); micro-interactions
  < 300 ms; exit shorter than enter.
- [ ] `ease-out` as the default; never `ease-in` for entering elements.
- [ ] Only `transform` and `opacity` animated (GPU-composited); 60 fps / 16 ms
  frame budget held. **(Important if animating width/height/top/left.)**
- [ ] `prefers-reduced-motion` honored (fade/dissolve fallback, no parallax).
  [WCAG 2.3.3] **(Critical if motion is unavoidable and triggers vestibular
  issues.)**
- [ ] ≤ 3 flashes per second [WCAG 2.3.1]; pause/stop control for auto-motion
  > 5 s [WCAG 2.2.2]; no info conveyed by motion alone.

## 10. Accessibility  *(always-on — see accessibility.md for full mapping)*

- [ ] Every function keyboard-operable; no focus trap; Esc closes overlays.
  [WCAG 2.1.1/2.1.2] **(Critical = fail.)**
- [ ] Visible focus indicator everywhere (never `outline:none` without
  replacement); focus not obscured by sticky headers. [WCAG 2.4.7 / 2.4.11]
- [ ] Logical focus order; no positive `tabindex`; modals trap-then-restore
  focus. [WCAG 2.4.3]
- [ ] Skip-to-content link as the first focusable element. [WCAG 2.4.1]
- [ ] Native HTML before ARIA; every interactive element has an accessible name
  + correct role/state. [4.1.2; WebAIM: misused ARIA is worse than none]
- [ ] Semantic landmarks + logical heading hierarchy; all form fields labeled.
- [ ] Single-pointer alternative to every drag/gesture. [WCAG 2.5.7/2.5.1]
- [ ] Usable at 200% text zoom and 320px reflow; custom text spacing tolerated.
- [ ] Tested with a real screen reader, not just an automated scan (<40%
  coverage).

## 11. Trust, credibility & ethics

- [ ] Professional, consistent visual design matched to purpose; zero typos or
  broken links (these disproportionately destroy credibility). [Stanford/Fogg]
- [ ] Real-org proof: address, contact, named team with credentials.
- [ ] Social proof is genuine and only shown when impressive (weak counts signal
  unpopularity); behavioral signals used for relevant discovery. [NN/g]
- [ ] Security/reassurance cues at payment/data moments; plain-language privacy
  (what/why/how), not buried fine print.
- [ ] **No dark patterns** — no fake urgency/countdowns, pre-ticked opt-ins,
  confirmshaming, roach-motel cancellation. Now EU-illegal. **(Critical.)**
- [ ] Sensitive moments (refunds, failures) handled gracefully — reflective-
  level trust is built or destroyed here. [Norman]
- [ ] Any persuasion mechanic passes the **Regret Test** (fully-informed user
  wouldn't regret it). [Eyal]

## 12. 2026 / AI-native  *(only if the product has AI features)*

- [ ] Color system on semantic tokens; dark mode a first-class theme (off-white
  on near-black, AA verified in *both* modes, manual override).
- [ ] AI features ship the **trust stack**: citations/sources, confidence cues,
  streaming/interruptible output, visible "what it's doing", easy override/stop,
  safe "I don't know" fallback, "Why am I seeing this?" affordance.
- [ ] Irreversible/high-stakes AI actions require explicit confirmation; agents
  have a visible emergency stop.
- [ ] Generative/morphing layouts avoided where users rely on muscle memory.
- [ ] AI is solving a validated need, not bolted on (the "AI fatigue" anti-pattern).

---

## Scoring a review

Tally Critical / Important / Minor. Verdict:
- **FAIL** — any Critical unresolved (a11y failure, contrast fail, lost input,
  dead happy path, dark pattern, illegal).
- **PASS WITH NOTES** — no Criticals; some Important/Minor outstanding.
- **PASS** — no Criticals, Importants addressed.

Always close with the **top 3 fixes by impact**, threshold-cited.
