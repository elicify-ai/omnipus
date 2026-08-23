# Release checklist

Run through this list before triggering the release workflow
(`.github/workflows/release.yml`, manual `workflow_dispatch` with the tag).
Items marked **(CI-enforced)** are also gated mechanically — the checklist
entry exists so a release is never *attempted* against a state CI will
reject.

- [ ] Branch fully green: every gate in `.github/workflows/pr.yml` passing
      on the commit to be tagged (Constraint #7 — no pre-existing-failure
      excuses).
- [ ] **Embedded provider catalog snapshot is at most 14 days old**
      (`pkg/providers/catalog/data/providers_catalog.json` `updated_at` —
      ADR-067 A-15/FR-006). Refresh by merging the scheduled snapshot PR
      from `elicify-ai/omnipus-provider-catalog` (or copying the latest
      release asset byte-for-byte, verifying its `.sha256` sidecar) —
      never by hand-editing the file. **(CI-enforced:** the release
      workflow's "Provider catalog snapshot age gate" step fails the tag
      when the snapshot is older than 14 days.**)**
- [ ] Contracts clean: `make verify-contracts` exits 0 on the tagged commit.
- [ ] SPA embed synced: `pkg/gateway/spa/` rebuilt from the current
      `dist/spa/` (the release build regenerates it, but a stale local
      smoke-test binary must not be what was verified).
- [ ] Human approval on the release PR to `main` — never admin-merge or
      auto-merge (CLAUDE.md "Merging to main").
