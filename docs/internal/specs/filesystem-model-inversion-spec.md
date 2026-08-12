# Spec — Filesystem model inversion (`open` / `confined`)

- **Implements:** [ADR-060](../architecture/ADR-060-filesystem-read-exec-model-inversion.md)
- **Status:** Draft (pre-implementation)
- **Date:** 2026-08-12
- **Platforms:** macOS (Seatbelt), Linux (Landlock), Windows (no backend)

---

## 0. Scope

Adds `sandbox.filesystem_model`. In `open`: reads and execute are unrestricted, writes stay confined, and macOS additionally denies a credential list. In `confined`: today's behaviour, unchanged.

**Out of scope:** egress control (its own ADR); a Windows sandbox backend; ADR-057 session work.

**Non-negotiable:** `confined` must be behaviour-identical to today. Every existing test that passes in `confined` must keep passing, unmodified. That is the safety net for the whole change.

---

## 1. Functional requirements

### FR-1 — Config key

`OmnipusSandboxConfig.FilesystemModel string \`json:"filesystem_model"\`` — `open` | `confined`.

**Default is `confined` until every blocker in §5 clears, then flipped to `open` as the final step — and `open` then becomes the default for EVERY install, new and upgrading (operator decision, 2026-08-12).**

That is a deliberate posture change on upgrade, not an accident of seeding: `loadConfig`
unmarshals the operator's JSON over `DefaultConfig()`, so an existing `config.json` with
no `filesystem_model` key picks up the new default on the next boot. It is the right
outcome — the confined model does not work in practice and leaving existing installs on
it means the bug stays unfixed for exactly the people already using the product — but it
MUST ship with a release note that says plainly: reads and program execution become
unrestricted, writes are unchanged, and Omnipus's own secrets remain protected. An
operator who wants the old behaviour sets `filesystem_model: "confined"` explicitly.

The alternative considered and rejected was defaulting `open` for fresh installs only.
It doubles the test matrix and lets two people on the same version see different
behaviour, which makes every support conversation start with "which install are you?". The first draft defaulted to `open`, which breaks the §4 gate: with `open` as default, every existing test runs under the new model and fails at step 2, so the "confined is byte-identical" safety net never gets a chance to prove itself. Defaulting to `confined` means the whole change lands inert, is verified, and is switched on deliberately in one reviewable commit.

- **FR-1.1** Unknown value → decode error naming both valid values (mirror `SandboxMode.UnmarshalJSON`).
- **FR-1.2** Empty/absent → the seeded default (`confined` initially, `open` after the §5 blockers clear). Must survive `loadConfig` unmarshalling operator JSON over `DefaultConfig()`.
- **FR-1.3** Tag omits `omitempty`: the field is seeded non-empty, and omitting it on save would silently re-seed the default. (Same defect already fixed for `allowed_exec_paths`.)
- **FR-1.4** Not folded into `configTouched` in `applySandbox` — a seeded-always-present field would make it permanently true and disable the Docker permissive auto-downgrade.

### FR-2 — Policy computation

`DefaultPolicy` gains the model. In `open`:

- **FR-2.1** Omit `readOnlySystem` entirely (both bits — §6 of the ADR: removing read while keeping execute stops every child starting).
- **FR-2.2** Omit `allowedExecPaths` rules.
- **FR-2.3** Retain: `/tmp` RWX, `$TMPDIR` RWX, `allowed_paths` (write grants), `/dev/null`, `/dev/shm`.
- **FR-2.3a** `$OMNIPUS_HOME` is granted RWX **minus the secret set**: `master.key`, `credentials.json`, `config.json`, `cli.token`, `entities/`. **[VERIFIED]** on a real install these are five entries at one level, and `agents/` (workspaces, must stay writable) is distinct from `entities/` (per-agent tool policy, must not be). Excluding policy therefore does not touch any agent's own working directory.
- **FR-2.3b** The secret set is defined once, in one place, and consumed by both backends. Two lists would drift.
- **FR-2.4** Emit `SandboxPolicy.ReadsOpen = true` and `ExecOpen = true` so backends need no config access.
- **FR-2.5** In `confined`, produce a policy **byte-identical** to today's.

### FR-3 — macOS renderer

- **FR-3.1** When `ReadsOpen`, emit `(allow file-read*)` and `(allow process-exec)` in the preamble.
- **FR-3.2** Emit the credential deny block **last**, after every policy-derived allow. **No `(allow file-read* …)` may follow it.**
- **FR-3.2a** Every deny path that lies inside a WRITE grant MUST also emit `(deny file-write* (subpath …))`. **[VERIFIED]** a read-only deny is defeated in two syscalls: `rename(2)` moves `master.key` to a name outside the deny, then it reads normally. `truncate` likewise succeeds and destroys the vault irreversibly without any read. `mv` appears to fail only because it `stat()`s first — the raw syscall wins.
- **FR-3.2b** Deny paths OUTSIDE any write grant (`~/.ssh`, `~/.aws`, `~/.gnupg`) need read-deny only. **[VERIFIED]** symlink (both directions, and chains), child-created hardlink, case variance on both case-sensitive and case-insensitive volumes, `../` traversal, `openat`+dirfd, `fchdir`, `dd` and `tar` all fail against them. Residual: a readable parent still leaks the denied file's name, size and mtime.
- **FR-3.3** Each deny path is symlink-resolved via the same helper allows use.
- **FR-3.4** A deny path that does not exist is still emitted (a credential file created later must be covered).
- **FR-3.5** Deny paths are `~`-expanded through `expandUserPath`.
- **FR-3.6** In `confined`, the rendered profile is byte-identical to today's.

**Deny set (macOS) — exactly the FR-2.3a secret set, nothing else:** `master.key`, `credentials.json`, `config.json`, `cli.token`, `entities/`, all under `$OMNIPUS_HOME`.

**DECIDED 2026-08-12 (operator): Omnipus protects only Omnipus's own secrets.** An earlier draft also denied `~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.kube`, `~/.docker/config.json`, `~/.netrc`, `~/.git-credentials`, `~/.npmrc` and `~/Library/Keychains`. Those are **removed**. Rationale, in the operator's words: *"in both cases we should only protect onpus secrets we are not responsible for others"*. Three consequences follow, and all are intended:

1. **macOS and Linux now protect the identical set**, so the two backends are testable against one list and cannot drift. The earlier draft's asymmetry (macOS denies nine extra paths, Linux cannot) was the single largest source of per-platform divergence in this spec, and divergence is where this project's recent defects have come from.
2. **An agent can read the operator's own SSH and cloud keys.** This is the same posture Claude Code and Codex ship, and it is the posture the operator chose. It is not an oversight.
3. **FR-3.2b's read-only-deny case disappears.** Every remaining deny path sits inside the `$OMNIPUS_HOME` write grant, so FR-3.2a's read+write deny applies to all of them uniformly — one rule shape, not two. The verified findings behind FR-3.2b are retained in the ADR as evidence, but no longer describe any shipped rule.

`~/Library/Keychains` leaving the list also removes the one entry whose protection was only partial (`securityd` reads it on the client's behalf — FR-10). Nothing shipped now claims a protection it cannot deliver.

### FR-4 — Linux backend

- **FR-4.1** When `ReadsOpen`, remove `landlockAccessFSReadFile|landlockAccessFSReadDir` from `handledAccessFS`; when `ExecOpen`, also remove `landlockAccessFSExecute` — **except** that the secret set must remain unreachable, so reads stay HANDLED and the openness is achieved by granting, not by unhandling. See FR-4.5.
- **FR-4.2** Mask every rule's rights against `handledAccessFS` before `landlock_add_rule`. **Unmasked rights cause EINVAL, which `ApplyWithMode` treats as fatal → boot aborts (exit 78).**
- **FR-4.3** Drop any rule whose masked rights are **zero** (also EINVAL).
- **FR-4.4** Log once at boot, at WARN, naming the secret set as protected-by-non-grant so an operator can tell the mechanism from the macOS one.
- **FR-4.5** **Sibling granting.** For each secret, walk `/` → secret. At each level, grant read (and execute) on every sibling **except** the entry on the path. Grant everything else whole at the top level.
  - **FR-4.5a** Enumerate at **spawn**, not boot. **[VERIFIED]** `RestrictCurrentThread` already builds "a fresh ruleset matching the saved policy" per child spawn, so per-spawn enumeration adds a few directory listings and tens of syscalls per process start and removes staleness entirely.
  - **FR-4.5b** Only directories **on the path to a secret** are enumerated — about three levels. A filesystem walk is forbidden: it would be slow and would reintroduce the §2 defect class.
  - **FR-4.5c** **If any listing fails, the spawn MUST fail.** Falling back to granting the parent exposes the secret silently — the exact failure shape this work exists to eliminate.
  - **FR-4.5d** A test MUST assert that a directory created AFTER the gateway started is readable by a child spawned after it (proves FR-4.5a), and that the secret set is not (proves FR-4.5).

### FR-5 — Windows

- **FR-5.1** No code change. `FallbackBackend` is selected; the model affects nothing.
- **FR-5.2** Correct CLAUDE.md's "Windows (Job Objects+Restricted Tokens+DACL)" to state that no Windows filesystem backend exists.
- **FR-5.3** Boot on Windows logs the same FR-4.4 warning.

### FR-9 — The sandbox's own configuration must not be agent-writable

**[VERIFIED, PRE-EXISTING]** `$OMNIPUS_HOME` is granted RWX and contains `config.json`, `agents/<id>`, and `cli.token`. A sandboxed child can therefore read the gateway bearer token, set `sandbox.mode: off`, flip its own tool policy to `allow`, and truncate `master.key`.

This is not caused by this change — it is true in shipped code — but this spec MUST NOT ship a model that depends on write-confinement while that hole is open.

- **FR-9.1** `config.json`, `entities/`, `cli.token`, `master.key`, `credentials.json` MUST be denied **write** to sandboxed children under BOTH models. `agents/` is NOT in the set — it holds agent workspaces, which must stay writable. `entities/` holds per-agent tool policy, which must not.
- **FR-9.2** A test MUST spawn a real child and assert each is unwritable, on macOS and Linux.
- **FR-9.3** RESOLVED by FR-2.3a and FR-4.5: on macOS the set is denied read+write; on Linux it is never granted. No relocation outside `$OMNIPUS_HOME` is required, so the single-directory install story survives.
- **FR-9.4** This applies under **both** models. In `confined` the set must also be excluded — the hole is pre-existing and must not survive behind the safety net.

### FR-10 — The IPC default-deny is part of the credential protection

**[VERIFIED]** A path deny does not stop a daemon-mediated read: `securityd` serviced a keychain query against a denied file for a sandboxed client. `launchctl bootstrap` outside the sandbox exfiltrated a master key perfectly. Both are blocked today only by the preamble's `(deny default)` over `mach-lookup`.

- **FR-10.1** A rendered `open` profile MUST NOT contain any `(allow mach-lookup …)` beyond the mDNSResponder entry.
- **FR-10.2** A test MUST assert FR-10.1 against a rendered profile, so a future "make the toolchain work" edit cannot silently delete it.

### FR-6 — Secret redaction — **DROPPED 2026-08-12 (operator decision)**

Redaction was to scrub `RegisterSensitiveValues` entries out of tool output before it
reached agent context or the transcript. It is **not being built**, and the reason is
recorded here so it is not silently re-added later.

It existed to cover one specific gap: Linux was believed to have no way of protecting
the master key, so the plan was to catch the key on the way OUT instead. FR-4.5
sibling-granting removed that premise — the key is never granted to a child on Linux,
so nothing reaches redaction to be redacted. The layer would be code that reads as
protective while having no demonstrated job.

Two further reasons, both of which argue against building it as reassurance:

1. **It cannot deliver what its name implies.** Scrubbing matches literal strings. A
   key re-encoded as base64 or hex passes straight through. A control that stops the
   naive case and not the deliberate one invites more trust than it earns — the
   defect class this project keeps hitting.
2. **The real residual exposure is elsewhere.** `$OMNIPUS_HOME/sessions/**` holds
   plaintext transcripts under 90-day retention, and redaction never addressed that
   either.

**Reopen this only if a concrete leak path is demonstrated** — i.e. a case where a
secret provably reaches tool output despite the kernel exclusion. Absent that, it is
not defence in depth, it is unverified code.

### FR-7 — Observability

- **FR-7.1** `sandbox.applied` includes `filesystem_model`.
- **FR-7.2** `SandboxStatus` gains `filesystem_model`. Contract-first: `contracts/components/schemas/SandboxStatus.yaml` → `scripts/gen-contracts.sh` → generated diff in the same commit.
- **FR-7.3** `renderSandboxMode` reflects the model, with the accompanying **spec v7 update** its doc comment requires.

### FR-8 — Test dispositions (ADR-060 §7.3)

Every listed test gets an explicit disposition. **No test may be deleted without a replacement or a tracked issue.**

- **FR-8.1** The two red-team tests (`master_key`, `credentials`) become `confined`-model tests — they assert the `confined` path still blocks. This preserves the C1/C2 evidence rather than discarding it.
- **FR-8.2** The macOS adversarial tests are retargeted at a **denied credential path** instead of a workspace-outside path. Symlink and hardlink variants are mandatory: they are the interesting attacks against a path-based deny.
- **FR-8.3** `…ExecGrantDoesNotWidenReadBoundary` and `…PreexistingHardlink_IsAKnownGap` are deleted as vacuous under `open`, and **re-added under `confined`**.

---

## 2. BDD scenarios

### S-1 — Toolchain runs without enumeration (the whole point)
```gherkin
Given filesystem_model is "open"
  And node is installed at ~/.local/share/fnm/node-versions/v24/installation/bin/node
  And that path appears in NO config list
 When an agent runs `node --version` under the sandbox
 Then it succeeds
```

### S-2 — Writes stay confined
```gherkin
Given filesystem_model is "open"
 When an agent writes to ~/Documents/notes.txt
 Then the write is denied
  And no file is created
```

### S-3 — macOS credential deny survives the workspace allow (the FR-3.2 defeat)
```gherkin
Given filesystem_model is "open" on macOS
  And the policy grants $OMNIPUS_HOME read+write+execute
 When an agent reads $OMNIPUS_HOME/master.key
 Then the read is denied
```
*Fails if the deny block is emitted before the policy allows — the exact ordering bug ADR-060 §4.1 documents.*

### S-3b — the read-deny is not enough (FR-3.2a)
```gherkin
Given the same policy
 When an agent renames $OMNIPUS_HOME/master.key to $OMNIPUS_HOME/k
 Then the rename is denied
  And reading $OMNIPUS_HOME/k fails
```
*Without FR-3.2a this passes S-3 and still leaks the key: rename is not a read.*

### S-3c — integrity, not just confidentiality
```gherkin
Given the same policy
 When an agent truncates $OMNIPUS_HOME/master.key
 Then the truncate is denied
  And the file is unchanged
```
*A zero-length master key destroys every stored credential irreversibly, with no read involved.*

### S-4 — macOS deny resists symlink and hardlink
```gherkin
Given filesystem_model is "open" on macOS
  And a symlink in the workspace points at $OMNIPUS_HOME/master.key
 When an agent reads the symlink
 Then the read is denied
```
```gherkin
Given a hardlink in the workspace points at $OMNIPUS_HOME/master.key
 When an agent reads the hardlink
 Then the read SUCCEEDS
  And this is a documented, asserted limitation of path-based deny
```

### S-5 — Linux boots (FR-4.2/4.3 regression)
```gherkin
Given filesystem_model is "open" on Linux with Landlock
 When the gateway applies the policy
 Then no landlock_add_rule returns EINVAL
  And the gateway boots
```

### S-6 — `confined` is unchanged
```gherkin
Given filesystem_model is "confined"
 When the policy is computed and rendered
 Then it is byte-identical to the pre-ADR-060 output
```

### S-7 — Multi-tenant guard
```gherkin
Given filesystem_model is "open"
 When the gateway boots
 Then a WARN states credential files are not kernel-protected on Linux/Windows
  And names master.key
```

---

## 3. Test dataset — boundaries and adversarial cases

| # | Input | Expected | Why |
|---|---|---|---|
| 1 | `filesystem_model: "OPEN"` | decode error | case sensitivity pinned |
| 2 | `filesystem_model` absent | the seeded default | FR-1.2 (`confined` until the §4 gate clears, then `open`) |
| 3 | `filesystem_model: ""` | the seeded default | empty ≠ invalid |
| 4 | `filesystem_model: "opne"` | decode error naming both values | typo caught at load |
| 5 | config saved then reloaded | value survives | FR-1.3, no `omitempty` |
| 6 | `open`, read `/etc/passwd` | allowed | the inversion works |
| 7 | `open`, read `~/.ssh/id_rsa` (macOS) | **allowed** | DECIDED: Omnipus secrets only; not our keys to protect |
| 8 | `open`, read `~/.ssh/id_rsa` (Linux) | **allowed** | same as row 7 — the platforms now agree |
| 9 | `open`, write `/etc/hosts` | denied | writes unchanged |
| 10 | `open`, exec `~/.cargo/bin/rg` | allowed | execute opened |
| 11 | `open`, deny path does not exist yet, created later, then read | denied | FR-3.4 |
| 12 | `open`, deny path via `/private` alias (macOS) | denied | FR-3.3 resolution |
| 13 | `open`, `$OMNIPUS_HOME/master.key` (macOS) | denied | S-3 ordering |
| 14 | `open`, `$OMNIPUS_HOME/master.key` (Linux) | **denied** | FR-4.5 never grants it — this row was inverted before sibling-granting |
| 15 | `confined`, all of the above | today's behaviour | FR-2.5 |
| 16 | `open` + Landlock ABI 1–3 | boots, reads open, no net rules | ABI floor |
| 17 | `open` + `mode=permissive` | policy computed, not enforced | no interaction |
| 18 | `open` + `mode=off` | nothing applied | no interaction |
| 19 | `open`, rename `master.key` (macOS) | denied | FR-3.2a — read-deny alone is bypassed |
| 20 | `open`, truncate `master.key` (macOS) | denied | S-3c integrity |
| 21 | `open`, write `config.json` | denied | FR-9.1 — sandbox must not be self-disabling |
| 22 | `open`, read `cli.token` | denied | FR-9.1 — live gateway credential |
| 23 | `open`, deny path non-existent AND under a symlinked ancestor | denied once created | FR-3.3 × FR-3.4 intersection — where the silent no-op lives |
| 24 | rendered `open` profile contains `(allow mach-lookup` beyond mDNSResponder | test fails | FR-10.1 |
| 25 | `filesystem_model` changed via PUT /api/v1/security/sandbox-config | rejected or restart-required, stated explicitly | no hot-reload for sandbox policy |
| 26 | dir created after gateway start, read by a later child (Linux) | allowed | FR-4.5a — proves per-spawn enumeration |
| 27 | `entities/jim.json` written by a child | denied | FR-9 — agent must not edit its own tool policy |
| 28 | `agents/jim/` written by a child | allowed | workspaces stay writable; policy does not |
| 29 | a listing on the path to a secret fails | spawn fails | FR-4.5c — never fail open |
| 30 | secret set under `confined` | also excluded | FR-9.4 |

---

## 4. Implementation order

1. FR-1 config key + `confined` path (no behaviour change; proves the safety net).
2. FR-2 policy computation, both models.
3. FR-3 macOS renderer + deny-list, with S-3 first — it is the test most likely to fail.
4. FR-4 Linux, with S-5 first — EINVAL bricks boot.
5. FR-7 observability (contract regen).
6. FR-6 redaction — **blocked on O-1**.
7. FR-8 test dispositions.
8. FR-5.2 CLAUDE.md correction.

**Gate:** steps 1–2 must land with every existing test green in `confined` before step 3 starts. If `confined` is not byte-identical, stop — the rest is unsafe.

---

## 5. Risks

| Risk | Mitigation |
|---|---|
| Linux EINVAL bricks boot | FR-4.2/4.3 + S-5 first; no Linux host here, so CI must gate it |
| macOS deny defeated by ordering | FR-3.2 normative + S-3 |
| `confined` drifts from today | FR-2.5 byte-identical assertion, checked before anything else |
| Master key readable on Linux | **Resolved by FR-4.5** — never granted, so never readable. Was the spec's top blocker. |
| Multi-tenant runs `open` unknowingly | FR-4.4 boot warning + operator docs |
| Ten tests silently deleted | FR-8, no deletion without replacement |
| Read-deny bypassed by rename/truncate | FR-3.2a + S-3b/S-3c — **falsified the original S-3** |
| Agent rewrites its own sandbox policy | FR-9 — pre-existing; resolved by the same exclusion, under both models |
| Operator widens mach-lookup and silently deletes keychain protection | FR-10.2 test |

## 6. Explicitly unverified

Everything Linux. There is no Linux host in this environment; FR-4 is written from code reading (`sandbox_linux.go::computeRights`, `accessToLandlockRights`, `ApplyWithMode`) and the Landlock ABI contract. **FR-4 must be validated on a real Linux host or the CI worker before merge.** The macOS claims in FR-3 were executed against real children.
