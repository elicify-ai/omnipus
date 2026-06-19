# Principles & Laws — the "why" behind the checklist

Cite these in REVIEW findings. Each: definition → origin/study → UI application
→ anti-pattern.

## Cognitive load & memory

**Cognitive Load Theory** — Working memory is limited; performance collapses
when load exceeds it. Sweller (1980s). *Intrinsic* = task complexity (keep),
*extraneous* = imposed by poor presentation (cut), *germane* = builds
understanding. → Strip decoration; group fields. ✗ Cluttered dashboards, walls
of options.

**Miller's Law (7±2 / chunking)** — Working memory holds ~7 items (true span is
nearer 4). Miller (1956). The real lesson is *chunking*, not a literal 7. →
Segment numbers, group nav. ✗ Flat 20-item menus, unbroken 16-digit strings.

**Recognition over Recall** — Recognizing with cues is far easier than recalling
unaided. Nielsen heuristic #6. → Visible menus, autocomplete, recently-viewed,
labeled icons; keep knowledge in the world. ✗ Command-only flows, re-typing a
code from a prior screen.

**Serial Position Effect** — First and last items in a sequence are best
remembered. Ebbinghaus (1885), Murdock (1962). → Put key nav/actions at start
and end. ✗ Burying the primary CTA mid-list.

**Von Restorff (Isolation) Effect** — The item that differs is noticed and
remembered. von Restorff (1933). → Make the primary CTA distinct. ✗ Emphasizing
everything (so nothing stands out).

## Decision & action

**Hick's Law** — Decision time grows logarithmically with number/complexity of
choices. Hick & Hyman (1952). → Limit options, highlight a default, split
complex steps. ✗ Mega-menus, 8 indistinguishable pricing tiers. *Exception:*
expert tools where users want many options.

**Choice Overload / Paradox of Choice** — Too many options → paralysis, lower
satisfaction, abandonment. Iyengar & Lepper jam study (2000); Schwartz. →
Curate, offer "most popular." ✗ Endless dropdowns.

**Dual-Process Theory (System 1 / System 2)** — Fast intuitive vs slow
deliberate thought. Kahneman, *Thinking, Fast and Slow* (2011). → Design browse/
first-impression for System 1; checkout/compare to *support* System 2. ✗ Dense
copy during a System-1 moment; rushing a high-stakes decision.

**Fogg Behavior Model (B=MAP)** — Behavior needs **M**otivation + **A**bility +
**P**rompt at once. BJ Fogg. Motivation/ability trade off; ability is the
reliable lever. → Make the action tiny, prompt at a high-ability moment. ✗
Prompting an action the user can't easily do.

**Default / Status-Quo Bias** — People stick with the preselected option.
Samuelson & Zeckhauser (1988); Thaler & Sunstein. → Default to the safe/
beneficial choice. ✗ Pre-checked add-ons, opt-out-by-default (dark pattern).

**Anchoring** — The first number frames all later judgments. Tversky & Kahneman
(1974). → Show genuine reference price next to sale price. ✗ Fake inflated
"original" prices.

**Loss Aversion** — Losses feel ~2× as strong as equivalent gains. Kahneman &
Tversky, Prospect Theory (1979). → Frame around protecting progress, sparingly
and honestly. ✗ Manufactured-urgency anxiety.

## Speed & targets

**Fitts's Law** — Time to hit a target depends on its size and distance. Fitts
(1954). → Big primary buttons near attention; use edges/corners; large mobile
targets. ✗ Tiny close buttons, far-flung critical actions.

**Doherty Threshold** — Sub-400 ms response keeps users in flow (+productivity).
Doherty & Thadani, IBM (1982). → Acknowledge input within 400 ms even if the
result isn't ready (optimistic UI, skeletons). ✗ Dead UI after a click →
rage-clicking.

## Memory of the experience & motivation

**Peak-End Rule** — Experiences are judged by the peak moment and the ending,
not the average. Kahneman et al. (1993), cold-water study. → Engineer a
delightful peak; end flows on a positive, clear note. ✗ Ending checkout on a
jarring error or blank page.

**Zeigarnik Effect** — Unfinished tasks nag and are better remembered. Zeigarnik
(1927). → Progress meters, "resume draft." ✗ Open loops with no path to close;
nagging incompletion the user can't resolve.

**Goal-Gradient & Endowed Progress** — Effort rises near a goal; an artificial
head start makes the goal feel closer. Hull (1932); Kivetz/Urminsky/Zheng
(2006); Nunes & Drèze (2006) — a 10-stamp card pre-stamped twice beats an
8-stamp blank card. → Show progress, give a genuine head start. ✗ Hidden or
fabricated progress.

## Familiarity, trust & simplicity

**Jakob's Law** — Users expect your site to work like the others they know
(their model is formed elsewhere). Nielsen. → Use conventional patterns (logo
top-left → home, cart top-right). ✗ Reinventing standard interactions for
novelty.

**Tesler's Law (Conservation of Complexity)** — Irreducible complexity must live
somewhere — system or user. Tesler, Xerox PARC. → Absorb it via smart defaults,
auto-detection, pre-fill. ✗ Offloading complexity onto the user to keep code
simple.

**Aesthetic-Usability Effect** — Pretty interfaces are *perceived* as more
usable and tolerate minor flaws. Kurosu & Kashimura, Hitachi (1995): 252 users,
26 ATM UIs — aesthetics correlated more with *perceived* than *actual* ease. →
Invest in polish for trust and patience. ✗ Using polish to mask real defects;
verify by watching behavior, not opinions.

**Postel's Law (Robustness)** — Be liberal in what you accept. Postel (1980). →
Accept phone/date/card in multiple formats and normalize. ✗ Rejecting valid
intent over formatting.

## Emotional design — Norman's three levels

Don Norman, *Emotional Design*. All three operate at once:
- **Visceral** — pre-conscious gut reaction; first-impression aesthetics.
  *Attracts.*
- **Behavioral** — the feel of *use*: control, competence, responsive feedback.
  *Retains.*
- **Reflective** — the remembered story, identity, brand bond. *Bonds.*

A beautiful (visceral) but frustrating (behavioral) product yields a *negative*
reflective memory. Handle sensitive moments (refunds, failures) gracefully —
that's where reflective trust is built or destroyed.

## Credibility — Fogg / Stanford

Stanford Web Credibility Project (3 yrs, 4,500+ users): visual design is the #1
cited credibility factor. Four credibility types: **surface** (design quality),
**presumed** (assumptions/stereotypes), **reputed** (third-party endorsement),
**earned** (built over repeated easy/personalized/responsive interaction). A
single violation can destroy years of accumulated credibility; typos and broken
links damage it disproportionately.
