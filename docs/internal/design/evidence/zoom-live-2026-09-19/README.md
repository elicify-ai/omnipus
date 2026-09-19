# Live zoom and initial-scale check — 2026-09-19

Measured on an isolated gateway built from `feat/design-system-conformance`, with scripted Playwright in desktop Chromium (1440×900), iPad Pro 11 landscape (Chromium and WebKit) and iPhone 13 (Chromium and WebKit). Full raw measurements are in `results.json`. `screenshot-manifest.txt` lists every screenshot taken with its sha256; four are kept here.

| Claim | Result |
|---|---|
| Task graph opens fitted, never below 80% | Confirmed: opens at 80%; with 40 tasks, 34 are off-screen at opening; it opens mid-chain, not at the start (`desktop-chromium-large-opening.png`) |
| Team graph opens tiny | Confirmed with correction: React Flow's default 50% floor applies; labels about 7px effective; agents cut off at top and bottom (`desktop-chromium-team-opening.png`) |
| Team graph "fit" frames differently from opening | Wrong: identical transform both times |
| Zoom ranges | Task graph 20–175%, team graph 50–200%, media viewer 50–800% |
| Inline chat Mermaid scaled to column | Confirmed: a wide diagram renders at 15–32% of natural width |
| Media viewer has no zoom buttons | Confirmed |
| Touch pinch in the media viewer | Zooms the media AND the page together (page `visualViewport.scale` about 2.0), measured in Chromium touch contexts; WebKit cannot synthesise pinch |
| Tall diagram in the viewer | Opens at 100% with the bottom cut off (`desktop-chromium-tall-mermaid-lightbox-opening.png`) |
| Wide diagram in the viewer | Defect: collapses to an unreadable strip about 300px wide at every viewport (`desktop-chromium-wide-mermaid-lightbox-opening.png`) |
| Mini-map | Task graph yes, team graph no |

Not verified: task and team graphs at iPhone width (the Graph tab tap was intercepted by an overlapping "Filter by agent" control in both engines); mobile WebKit wheel input; real devices. Source: the live-check lane report of 2026-09-19; the lead viewed the four kept screenshots.
