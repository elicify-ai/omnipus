---
name: browser-provisioning-deferred
description: Browser-provisioning follow-ups (ADR-042) — operator decisions 2026-07-14 on opt-out/ADR/CLI/port-limit
metadata: 
  node_type: memory
  type: project
  originSessionId: 45eb163f-c6f7-4e88-8de3-bcc8667cefc7
---

Browser provisioning shipped (commit `41c70b5d`, ADR-042, commit `90dabce0`) as download-at-boot (not bundled). Five follow-ups were triaged with the operator on 2026-07-14:

- **Opt-out (`tools.browser.preprovision`)**: FILED as **issue #507** (Feature, P2, area:tools+gateway). Skip the boot Google-fetch for airgapped/privacy setups; lazy resolution unchanged.
- **ADR-042**: DONE — `docs/internal/architecture/ADR-042-browser-provisioning.md` (decision: download-on-boot, not bundled; ~130MB vs Constraint #1).
- **`omnipus browser install` CLI**: SKIPPED (operator decision). Boot covers the normal install path.
- **Port-9223 / one-managed-Chrome-at-a-time limit**: ADR-043 WRITTEN + pushed (`4b0b5be8`, `docs/internal/architecture/ADR-043-browser-shared-chrome-per-agent-contexts.md`). Operator chose the **shared-Chrome + per-agent browser-contexts hybrid** (option C+041), implement on `bugfixes2`, ~2 sprints. KEY FINDING: chromedp 0.15.1 `WithNewBrowserContext` (verified) gives per-agent cookie/localStorage isolation within ONE Chrome — so the cross-agent login-sharing trade-off the operator accepted is MITIGABLE, not conceded. **Next steps (gate before implementation):** `/grill-spec` the ADR (coordinator D4 + browser-context adoption spike D2 are the targets), then a D2 tab-set-on-context spike + D3 contract audit, then `/plan-spec`. Not started yet — the ADR is the current state.
- **`Session()` ctx threading** (mid-download blocks): still open / deferred.

**Why:** the directive "install browser on installation or bundle it in" is satisfied by download-at-boot; these are the edges the operator chose to handle case-by-case rather than all-now.

**How to apply:** the port-limit investigation is the active task — when it lands, present options + recommendation and ask whether to proceed to an ADR/implementation. Don't re-litigate the opt-out/CLI decisions (operator already decided). Related: [[composer-redesign-a1]].
