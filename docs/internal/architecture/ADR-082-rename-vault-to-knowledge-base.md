# ADR-082 — Rename "vault" to "Knowledge Base", in three staged phases

- **Status:** Accepted (founder-ratified, 2026-09-07). The naming rules and the three carve-outs are decided. Two sub-questions are marked OPEN in §7 and do not block the shape.
- **Deciders:** Daniel Piatkowski (founder, ratifying decision); architect (staging, migration, verification)
- **Date:** 2026-09-07
- **Number verification:** `ADR-082` is genuinely free. Checked three ways on `integrate/library-improvements-v0.1.1`: the directory listing tops out at `ADR-081`; `git log --all --name-only --no-renames -- 'docs/internal/architecture/*'` shows no `ADR-082` path on **any** branch, past or present; and `git grep 'ADR-082'` across the last 400 commits of all refs returns nothing. This check is not ceremony — ADR-081 was drafted as "ADR-069" on a feature branch and had to be renumbered after the merge revealed `release/v0.1.1` already owned 069.
- **Supersedes:** [ADR-067](ADR-067-omnipus-knowledge-base-and-render-first-preview.md) §2 **D1**, third paragraph ("*The name `.omnipus-vault/` is decided, not provisional*"), together with its §5 closing note ("*O-3 (marker name) is **closed** — decided as `.omnipus-vault/` in D1*") and the Appendix A row **M-15** that recorded that closure. **Nothing else in ADR-067 D1 is superseded** — the detection rule (either marker alone suffices), the never-create-`.obsidian/` asymmetry, and the marker-trust paragraph all stand unchanged. ADR-067 itself is **not edited**: historical ADRs are the audit trail of what we believed and when, and rewriting one destroys the only record that the belief ever changed.
- **Relates:** [ADR-068](ADR-068-vault-records-typed-record-layer.md) (the typed record layer, whose wire schemas carry most of the `Vault*` names), [ADR-081](ADR-081-unified-library-search-and-grep-engine.md) (unified search, same surface), [ADR-022](ADR-022-credential-vault-reauth.md) (the *credential* vault — carve-out 1, never renamed)
- **Constraints in force:** Hard Constraint #7 (release responsibility — every branch fully green), Hard Constraint #8 (contract-first wire formats; generated types are the only legal cross-boundary types)

---

## 1. Context

### 1.1 What was ratified

The founder has ratified that the product concept currently called a "vault" is called a
**Knowledge Base** — in the interface a person reads, in the text a model reads, and in the
code. This is not a copy change with the code left alone.

### 1.2 The word already means three different things

The reason this is worth doing is the reason ADR-067 D1 gave for choosing `.omnipus-vault/` in
the first place: *"one identifier must not mean two things across the product."* That principle
now argues against the name it was used to justify. In this tree, "vault" means three unrelated
things:

| Meaning | Roughly | Fate |
|---|---|---|
| The Omnipus knowledge base — a folder of notes Omnipus indexes and searches | ~96% of occurrences | **Renamed** |
| The **credential** vault — the AES-256-GCM encrypted secret store | ~50 lines across 18 files | **Never renamed** |
| An **Obsidian** vault — somebody else's product, whose vocabulary we quote | 160 files mention Obsidian | **Never renamed** |

Measured on this branch, 2026-09-07: about **7,400** occurrences of `vault`/`Vault`/`VAULT`
outside `node_modules/`, `dist/`, and the embedded SPA. Treat that as an order of magnitude, not
a target — it moves by tens every day as other branches merge, and a rename that chases an exact
count is chasing a number that was never stable.

### 1.3 Most of the work is already done

The prior impact assessment attributes this to thirty-nine commits between 2026-08-23 and
2026-09-07, which moved the agent-facing tool layer from `vault_*` to `knowledge_*`. That commit
count is carried over from the assessment and was **not** independently re-derived here — the
end state was, which is what the decisions below rest on:

- All twenty shipped tools carry `knowledge_` names (`knowledge_find`, `knowledge_search`,
  `knowledge_create`, …). No `vault_*` tool name survives anywhere in `pkg/` except one
  historical comment at `pkg/coreagent/catalog_count_test.go:49`.
- No tool-policy key in `pkg/config/defaults.go` contains "vault". Nothing an operator has
  written into their `config.json` tool policy needs to change.

### 1.4 Renaming the wire *type* names changes zero bytes on the wire

This is the finding that makes the whole rename cheap, and it was checked by grep rather than
inferred:

- **Zero** property names in `contracts/` contain "vault".
- **Zero** Go struct tags in `pkg/` match `json:"…vault…"`.

The twenty-two `Vault*.yaml` schema files name *Go and TypeScript symbols*. Rename
`VaultFindRow` to `KnowledgeFindRow` and the JSON a client receives is byte-for-byte identical.
A concrete way to picture it: the label on the box changes; the contents and the shipping
address do not.

### 1.5 What actually breaks for an existing install

Four things — the three the prior impact assessment named, plus one it missed.

1. **The on-disk marker directory `.omnipus-vault/`.** Three sites:
   `pkg/knowledge/marker.go`'s `MarkerDirName`; `pkg/records/schema.go`'s `VaultMarkerDirName`,
   which deliberately **restates the literal rather than importing it** so that `pkg/records`
   keeps no storage dependency; and the `alwaysPruned` set in `pkg/filegrep/filegrep.go`, which
   stops recursive grep descending into it.
2. **The marker *file* inside that directory — `vault.json`** (`markerFileName`,
   `pkg/knowledge/marker.go`). The prior assessment counted three breaks and this is a fourth.
   It matters because changing only the directory constant leaves the code opening
   `.omnipus-kb/vault.json`, which is neither name and will read as a corrupt knowledge base
   rather than an obvious mistake. No ADR has ever named this file; ADR-067 D2 described its
   payload only. This ADR names it for the first time.
3. **The REST path `POST /library/{workspace_id}/vaults`** (`contracts/openapi.yaml:8400`,
   `operationId: createVault`), called by the SPA at `src/lib/api.ts:4771`.
   `operationId: findVault` (`openapi.yaml:8813`) names a generated client method, not a wire
   byte, but it is renamed in the same pass.
4. **The wire enum *value* `omnipus_vault`** (`contracts/components/schemas/KnowledgeBaseInfo.yaml:49`),
   produced in exactly one place: `pkg/gateway/rest_knowledge.go:314`.

### 1.6 One correction to the prior assessment

The assessment stated that "every `Vault*` type today is a Find/Search type, i.e. an operation."
That is not true, and the naming rule in §2 D4 is built on the corrected version.
`VaultRecord` is a *thing inside* a knowledge base (a note that declares a record type),
`VaultIndexState` is the state of the index *over* one, and `VaultTermCount` is the index's
vocabulary. None of the three is an operation. The rule that actually fits the shipped
precedent is simpler: `KnowledgeBase*` when the type **is** the container or describes it as a
whole; `Knowledge*` for everything else.

---

## 2. Decisions

### D1 — Rename in the code, not only in the copy

The concept is renamed at every layer: interface copy, model-facing text, wire schema names,
on-disk names, and internal identifiers. Copy-only would leave an operator reading "knowledge
base" on screen and finding `.omnipus-vault/` in their own folder — the exact split-vocabulary
state we are removing.

### D2 — Supersede ADR-067 D1's ruling that the marker name is decided

ADR-067 D1 closed open question O-3 by ruling the marker directory name `.omnipus-vault/`
**"decided, not provisional"**, and §5 recorded O-3 as closed on that basis. That ruling is
superseded. The marker directory is **`.omnipus-kb/`**, and the marker file inside it is
**`knowledge-base.json`**.

ADR-067's ruling was correct when made — it fixed a real defect, where revision 1 stated the
detection rule normatively while leaving the name open. What changed is not the reasoning but
the concept the name refers to.

### D3 — Three carve-outs, never renamed

| # | Carve-out | Why it stays |
|---|---|---|
| 1 | **The credential vault** — the encrypted secret store (`pkg/credentials`, `omnipus credentials rotate`, and the user-visible "Credential Vault" panel at `src/components/settings/SecuritySection.tsx:609`) | A different product surface with an established security vocabulary. "Vault" for secrets is the industry word — our own `pkg/credentials/keymgr.go` comments already reference HashiCorp-Vault-managed key files. Renaming it would make "knowledge base" mean both *your notes* and *your passwords*, which is the ambiguity this ADR exists to remove, re-created in a worse place. |
| 2 | **An Obsidian vault** — external vocabulary we quote | Renaming it would make us factually wrong about somebody else's product. It also includes a shipped CLI flag: `omnipus records import-obsidian --vault PATH` (`cmd/omnipus/internal/records/command.go`). The Go package `pkg/vaultimport` is the Obsidian importer and is covered by this carve-out — it is **not** renamed in phase 3. |
| 3 | **"Library"** — not a synonym, an orthogonal thing | The Library is the file explorer that *contains* knowledge bases. The route prefix `/library/{workspace_id}/…` is unchanged; only the `/vaults` collection segment moves. |

### D4 — Naming, per layer

| Layer | Rule | Today | After |
|---|---|---|---|
| Interface copy (what a person reads) | "knowledge base", sentence case | "Create Vault" | "Create knowledge base" |
| Model-facing text (tool descriptions, prompts, error strings an agent reads) | "knowledge base" | `no schema in this vault` | `no schema in this knowledge base` |
| Wire schema — the **container** | `KnowledgeBase*` | `KnowledgeBaseInfo`, `KnowledgeBaseView` (already correct) | unchanged |
| Wire schema — **everything else** (operations, contents, index state) | `Knowledge*` | `VaultFindRequest`, `VaultSearchRequest`, `VaultRecord`, `VaultIndexState`, `VaultTermCount` | `KnowledgeFindRequest`, `KnowledgeSearchRequest`, `KnowledgeRecord`, `KnowledgeIndexState`, `KnowledgeTermCount` |
| Wire property and enum **values** | `snake_case` | `omnipus_vault` | `omnipus_kb` |
| REST path segment | kebab-case, matching the existing `/knowledge/base-views` | `/library/{id}/vaults` | `/library/{id}/knowledge-bases` |
| `operationId` | same container/other split as the schema names | `createVault`, `findVault` | `createKnowledgeBase`, `knowledgeFind` |
| On disk | `.omnipus-kb/` and `.omnipus-kb/knowledge-base.json` | `.omnipus-vault/vault.json` | `.omnipus-kb/knowledge-base.json` |

`VaultFindRequest` is a good example of why the rename is overdue rather than cosmetic: it is
literally the request type of the tool already named `knowledge_find`.

**Bare "kb" is banned in prose and in type names** — it reads as "kilobyte", and a reader who
has to pause on an abbreviation has been charged for nothing. There are exactly **two**
sanctioned exceptions, both machine identifiers where brevity is the point and the two mirror
each other one-for-one: the directory `.omnipus-kb` and the enum value `omnipus_kb` that names
it. A reader who meets `omnipus_kb` in a payload can find `.omnipus-kb` on disk without a
lookup table. The enum's `description:` field spells the full term out. The marker *file* takes
the fully-spelled `knowledge-base.json` instead, because it sits in the operator's own folder
where a person may meet it with no context and four saved characters buy nothing.

### D5 — Three phases, in order

**Phase 1 — copy and model-facing text.** Interface strings and the text agents read. No
identifier changes, no schema changes, no bytes on the wire, nothing on disk. "Zero risk" here
means zero risk to operator data and to running clients — it does **not** mean zero test churn:
SPA tests that assert on visible strings will fail, and tool-description edits touch the
`pkg/coreagent` catalog pins. Those failures are the change working, not a break.

**Phase 2 — wire contract and the on-disk marker.** The only phase that can break an existing
install. Contains the schema renames, the REST path, the enum value, and the marker directory
and file, plus the read-only fallback in D6.

**Phase 3 — residual internal identifiers.** Go package `pkg/vaultprops` →
`pkg/knowledgeprops`; `records.VaultMarkerDirName` → `records.KnowledgeMarkerDirName`; local
variables, helper names, and test fixture folder names. Nothing here crosses a boundary.
`pkg/vaultimport` is **excluded** (carve-out 2).

The order is not arbitrary. Phase 1 is reviewable by reading, so it can land while other
branches are still editing `src/components/library/**` and `pkg/**`. Phase 2 is the only phase
whose review needs to be slow and adversarial, and putting it alone in its own change is what
makes that affordable.

### D6 — The marker migration: read both, write one, move nothing

- **Omnipus writes `.omnipus-kb/` only.** Never `.omnipus-vault/` again — not on create, not on
  repair, not on "upgrade". One writer, one name.
- **Omnipus reads both.** A folder containing `.omnipus-vault/` is still detected as a knowledge
  base, and its `vault.json` is still read for the display name. An operator who upgrades and
  opens their existing knowledge base sees it work exactly as before.
- **Omnipus does not move the operator's directory.** The marker sits inside the operator's own
  folder, which may be under their own version control, or synced by Obsidian Sync, iCloud, or
  Dropbox. Concretely: an operator whose knowledge base is a git repository would find an
  unexplained rename in their next `git status`, propagated to every device, performed by a
  program they did not ask to do it. The move is **offered**, never performed silently.
- **The fallback expires.** It is deleted in the release **after** the one that ships phase 2 —
  that is, at **v0.2**. Carrying it past v0.2 requires a new ADR. This is not tidiness: a
  fallback with no expiry means both names are correct forever, which restores exactly the
  ambiguity the rename removes.
- **The REST path and the enum value get no alias.** The SPA is embedded in the same binary and
  ships in lockstep, so for the first-party client the rename is atomic. Stated plainly as a
  cost rather than smoothed over: **anyone who has already scripted against
  `POST …/vaults` gets a 404 with no deprecation window**, and an older SPA pointed at a newer
  backend would drop the `KnowledgeBaseInfo` payload at the Zod edge (Constraint #8's
  drop-and-count behaviour). The endpoint is days old and Omnipus's REST surface carries no
  compatibility promise at v0.1.x — but that is a judgement, not a guarantee, and it is
  recorded here so it can be revisited (Q-2).
- **The changelog carries all four breaks**, in operator language, not schema names.

---

## 3. Verification hazard — read before trusting any phase-2 green

The marker rename is close to a worked example from
[`../false-green-patterns.md`](../false-green-patterns.md) — **trap #2, "A substring scan is not
a behavioural test"**, where a guard test kept passing 673/673 with the entire feature it
guarded deleted, because the assertion matched something that survived the deletion.

Here the permissive thing is not a substring scan but the fallback itself. **A detection test
passes because both names are accepted. That proves detection is permissive. It proves nothing
whatsoever about migration** — a build that still *writes* `.omnipus-vault/` on create passes
every detection test, because the detector accepts what the writer produced.

Four checks are required before any phase-2 result is reported as green.

**1. Delete the fallback locally and confirm the old-name test FAILS.**
Remove the `.omnipus-vault` branch from detection in your working tree, re-run the knowledge
detection suite, and confirm the old-name test now fails. If it still passes, that test is not
exercising the fallback — it is matching on something else (a fixture that also carries
`.obsidian/`, a helper that creates both markers, an assertion on a path string). Restore the
fallback and paste the observed failure output into the change description. This is checklist
items 5 and 6 of `false-green-patterns.md`: mutation-test what you wrote, and for a test change,
prove the previous version was actually broken.

**2. Assert on the filesystem after a create, not on the detector.**
Create a knowledge base, list the root's directory entries, and assert `.omnipus-kb` is present
**and `.omnipus-vault` is absent**. A detector-based assertion structurally cannot catch a
writer that still writes the old name.

**3. Check the duplicated literal in `pkg/records`.**
`pkg/records/schema.go` restates `.omnipus-vault` rather than importing it, deliberately. A
change that updates `pkg/knowledge` and not `pkg/records` **compiles cleanly and passes both
packages' own tests**, because each is internally consistent — and then reads schemas from a
directory that no longer exists. `TestSchema_LoadPathMatchesFR001` pins the literal; confirm it
pins the new one, and mutation-test it.

**4. Check the `filegrep` prune set, which no test currently guards.**
A stale `.omnipus-vault` in `alwaysPruned` is silent: grep begins descending into the new marker
directory and returning control-plane YAML as ordinary content hits. Nothing fails. Run a grep
across a knowledge base and assert zero hits originate inside the marker directory.

The governing rule from that document: *reasoning about the code predicted the wrong answer
three times out of three; every real defect came from a measurement.* Measure.

---

## 4. Gates, per phase

Project rule, in force throughout: **the full Go suite runs on CI, never locally.** This runs in
an ephemeral, resource-constrained devpod, and linking the gateway test binary has OOM-killed
sessions. At most one narrowly-scoped local test
(`-tags goolm,stdjson -run '^TestName$' -p 1`); everything else goes to the CI worker
(`fly ssh console --app ci-omnipus -C "/cache/runci.sh <ref> <gate>"`), and
`deploy/ci-worker/CLAUDE.md` is read before a verdict from that worker is trusted.

| Phase | Gates to re-run |
|---|---|
| **1 — copy** | `npm run typecheck` (the `-b` form; plain `tsc --noEmit` is a silent no-op here), `npx vitest run`, `gofmt -l .`, `golangci-lint run --build-tags=goolm,stdjson`, full Go suite **on CI**. Plus a human reading the Library and knowledge screens — a copy change is the one kind of change whose correctness a test cannot fully assert. |
| **2 — wire + marker** | Everything from phase 1, **plus**: `make gen-contracts` followed by `make verify-contracts`, with the regenerated `pkg/api/generated/` and `src/lib/api/generated/` committed in the **same** commit as the spec change (Constraint #8, step 4); the four checks in §3; `CGO_ENABLED=1 go build -tags goolm,stdjson ./...`; `govulncheck ./...`; the SPA embed sync (`npm run build` → `rm -rf pkg/gateway/spa` → copy from `dist/spa/`) before any end-to-end run, or the binary serves a stale SPA and the old names appear to survive; and the `e2e` gate on the CI worker. |
| **3 — internal identifiers** | `gofmt -l .`, `golangci-lint run --build-tags=goolm,stdjson`, full Go suite on CI, `CGO_ENABLED=1 go build`, `govulncheck ./...`. **No contract regeneration should be needed.** If `make verify-contracts` reports drift during phase 3, something crossed the wire boundary that was supposed to be internal — that is a finding, not a chore. |

---

## 5. Consequences

### 5.1 Gained

- One word for one thing. An operator reading "knowledge base" on screen finds `.omnipus-kb/` in
  their folder and `KnowledgeFindRequest` in the API, with no translation step.
- The 96% case stops colliding with the two carve-outs. "Is the vault locked?" currently has two
  correct and unrelated answers.
- The rename lands as three reviewable changes rather than one 7,400-occurrence commit that
  nobody can review and that conflicts with every branch in flight.

### 5.2 Costs and new obligations

- A dual-read fallback exists in the detection path until v0.2, with a deletion date that
  someone must actually honour.
- Operators keep a directory named `.omnipus-vault/` until they choose to move it. The product
  and one operator's disk disagree, visibly, for as long as they decline.
- One un-aliased REST path break and one un-aliased enum-value break, both accepted above.
- Every phase-2 green costs the four checks in §3. That is the price of the fallback; a
  migration whose test suite is satisfied by the fallback is not tested.

### 5.3 Explicitly worse than before

Between phase 1 and phase 2 the product is **deliberately inconsistent**: the interface says
"knowledge base" while the API says `vaults` and the disk says `.omnipus-vault`. This is the
staging working as intended, but anyone reading the code in that window will meet both
vocabularies at once, and the phase-1 change description must say so.

### 5.4 Residual

Historical documents keep the old word. ADR-067, ADR-068, ADR-081 and the specs beneath them
say "vault" and will continue to. They record what was believed when they were written, and
that is their only job. New prose uses the new name; old prose is left alone — the same
principle that keeps this ADR from editing ADR-067.

---

## 6. Alternatives rejected

**One big-bang rename.** ~7,400 occurrences in a single commit is unreviewable in practice, and
it conflicts with every branch currently touching `src/components/library/**` and `pkg/**`.
Rejected on reviewability, not on effort.

**Dual-write the marker — write both names for compatibility.** Makes both names permanently
correct and guarantees the ambiguity survives the rename. Rejected: a compatibility mechanism
that has no end state is not a migration.

**Rename above the disk only — keep `.omnipus-vault/`.** Leaves the operator staring at a
directory named for a concept the product no longer has, and contradicts ADR-067 D1's own
principle. Rejected.

**Move the operator's directory automatically on first open.** Rejected on the concrete failure:
an operator whose knowledge base is a git repository or a synced folder gets an unexplained
rename pushed to every device by a program they did not ask.

**Rename the credential vault too, for consistency.** Rejected — carve-out 1. Consistency across
two unrelated concepts is not consistency; it is a collision.

---

## 7. Open questions (do not block the shape)

| # | Question |
|---|---|
| **Q-1** | How the marker move is *offered*: a `doctor`-style action, a Library action on the knowledge base, a CLI subcommand, or all three. The decision that it is offered and never silently performed (D6) is closed; the surface is not. |
| **Q-2** | Whether `POST /library/{workspace_id}/vaults` needs a deprecation alias after all. Turns entirely on whether any external consumer exists — the judgement in D6 that v0.1.x REST carries no compatibility promise is the founder's to confirm or overturn. |

---

## 8. Affected components

- **Backend:** `pkg/knowledge/marker.go` (marker directory and file names), `pkg/records/schema.go`
  (the deliberately duplicated literal), `pkg/filegrep/filegrep.go` (`alwaysPruned`),
  `pkg/gateway/rest_knowledge.go` (the single enum producer), `pkg/vaultprops` → `pkg/knowledgeprops`
  (phase 3). **Not** `pkg/vaultimport`, **not** `pkg/credentials`.
- **Contracts:** `contracts/openapi.yaml` (one path, two `operationId`s), the twenty-two
  `contracts/components/schemas/Vault*.yaml` files, `KnowledgeBaseInfo.yaml` (the enum value), plus
  the regenerated `pkg/api/generated/` and `src/lib/api/generated/`.
- **Frontend:** `src/components/library/**` (copy and the create dialog), `src/lib/api.ts` (the
  path). **Not** `src/components/settings/SecuritySection.tsx` — carve-out 1.
- **Variants:** all three deployment modes identically. The marker is file-based and the SPA is
  embedded, so nothing here is variant-specific.
