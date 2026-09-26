# ADR-094 — founder decisions (#798)

| ID | Date | Decision (founder, verbatim first) |
|---|---|---|
| F794-1 | 2026-09-26 | Q1: "that is not an option because the preview is intended to run full web apps, full functional." Dropping in-preview login/storage (ADR-044 FR-3) is rejected. |
| F794-2 | 2026-09-26 | Q4: "everything, we need to be able to run full web apps, it is for software development." Previews need full browser capability: scripts, storage, cookies, login, forms, popups, downloads. |
| F794-3 | 2026-09-26 | Q3: allow cross-origin reads of files under a preview's token prefix (the token is the access key). |
| F794-4 | 2026-09-26 | Q5: accept and document the residual that an agent-built page could imitate an Omnipus login screen. |
| F794-5 | 2026-09-26 | Path: "separate address is not an option, they need to run under the preview path as it is now, or we can dynamically generate a subdomain within the Omnipus app alone without dependencies on the environment." A separately configured origin/port that needs environment setup is rejected. |
| F794-6 | 2026-09-26 | Q2′ default taken by team-lead (stated to founder, not objected): misconfigured origins (wildcard public_url, unparseable origin) fail closed. |
| F794-7 | 2026-09-26 | FQ-794-7: accept and document the /preview/ fallback residual (same-origin popup scripting); the hosted version must not rely on the fallback — tracked in elicify-ai/omnipus-ai#1177. |
