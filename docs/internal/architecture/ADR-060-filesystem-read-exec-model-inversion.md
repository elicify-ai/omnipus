# ADR-060 — Reads and execute default open; writes stay confined

- **Status:** Accepted (2026-08-12) — every open question the draft carried has been decided by the operator; see §4.0 (secret set), §7.1 (residual risk accepted), and the spec's FR-1 (default) and FR-6 (redaction dropped).
- **Date:** 2026-08-12
- **Deciders:** founder (decided the axis), lead (mechanism)
- **Related:** [ADR-052 Phase-3 AC-6](ADR-052-phase3-AC6-macos-seatbelt.md) (introduced `sandbox.allowed_exec_paths`, which this narrows); pentest items **C1/C2** (v0.2 #155 item 8) which this **reverses on Linux and Windows** — see §7; egress control, **not yet an ADR** — see §8.
- **Evidence level:** claims marked **[VERIFIED]** were executed on this host (macOS 26.5.2 / Darwin 25.5.0, x86_64) or read from code at commit `e6a80ccf`. Claims marked **[INFERRED]** are reasoned, not run — chiefly everything about Linux, which has no host available.
- **Supersedes in part:** the read half of `readOnlySystem` in `pkg/sandbox/sandbox.go::DefaultPolicy`, and `sandbox.allowed_exec_paths` as an enumeration mechanism.

---

## 1. Decision

Filesystem **reads and execute default to ALLOW**. Filesystem **writes remain default-DENY** with an explicit allow-list.

Where the platform can express it, a fixed **deny-list of credential paths** is subtracted. Where it cannot, reads are open with no kernel exception and protection moves to the layers named in §7.

An operator can restore the current posture with `sandbox.filesystem_model: confined` (§9).

### 1.1 Why execute is in scope — the correction that reshaped this ADR

The first draft opened reads only. A review found that **five of the eight defects cited as justification are execute failures**, which reads-open does not fix:

| Defect | Right actually needed | Fixed by reads-open? |
|---|---|---|
| `/opt/homebrew`, `/usr/local` — Homebrew tools unrunnable | **execute** | ✗ |
| `~/.local/share/fnm`, `fnm_multishells` — `node`/`npm` unrunnable | **execute** | ✗ |
| `~/.nvm`, `~/.volta`, `~/.asdf`, `~/.pyenv`, `~/.cargo/bin` | **execute** | ✗ |
| `/usr/local/go` — official Go install | **execute** | ✗ |
| `$TMPDIR` — every child creating a temp file | **write** | ✗ |
| `/private/var/select` — `/bin/sh` cannot start | read | ✓ |
| `/` — every child aborts, empty stderr | read | ✓ |
| `/tmp` vs `/private/tmp` — rule matches nothing | path resolution | n/a |

**[VERIFIED]** `pkg/sandbox/exec_paths.go` grants `AccessRead | AccessExecute` — the execute bit is what made `node` runnable, not the read bit.

Opening reads alone would have shipped the full security cost for roughly a quarter of the benefit, and the next Homebrew install would have reproduced the original bug with the identical symptom. Execute is therefore part of the decision, not an open question.

This also matches the comparison more honestly: Codex's default is `workspace-write` — *writes* are the confined axis, not reads.

## 2. Problem

The policy must name every directory an agent may read or execute from. Anything unnamed is invisible.

That list cannot be completed. The eight classes in §1.1 were each found as a separate defect, in one session, on one machine, each after a user-visible failure. The failure mode is uniform: the agent reports a broken toolchain, **the error never mentions the sandbox**, and the operator's rational response is to disable it entirely — losing the whole boundary rather than a part.

**Auto-detection was considered and rejected.** It is the same enumeration with more code: it cannot cover a tool nobody has installed yet, and each miss still presents as an unexplained failure.

## 3. What this does and does not buy

### 3.1 It is what the reference implementations do **[INFERRED — from public docs, not source]**

| | Read default | Execute default | Write default |
|---|---|---|---|
| Codex `workspace-write` (its default) | open | open | cwd + `--add-dir` |
| Anthropic sandbox-runtime | open (`denyRead` subtracts) | open | nothing until granted |
| Claude Code sandboxed Bash | open | open | allow-list |
| **Omnipus today** | **enumerated** | **enumerated** | allow-list |

Sources: [Codex sandboxing](https://mintlify.wiki/openai/codex/concepts/sandboxing), [Claude Code sandboxing](https://code.claude.com/docs/en/sandboxing), [sandbox-runtime](https://github.com/anthropic-experimental/sandbox-runtime).

### 3.2 This is a TRADE, not a free gain

An earlier draft argued that read-locking was near-worthless because egress is port-based, so moving strictness to the network was a net gain. **That argument is wrong and is retracted.**

Egress control cannot close two paths, by construction:

1. **The model provider.** Anything an agent reads enters its context and is sent to the LLM endpoint on the next turn. That endpoint must be allow-listed for the product to work. It is also persisted in plaintext to `$OMNIPUS_HOME/sessions/<id>/<date>.jsonl` under 90-day retention, so the exposure is durable.
2. **The agent's own channels.** Omnipus exists to post agent output to Telegram, Discord, Slack, Matrix. Those hosts are allow-listed by definition. An agent prompt-injected into `cat ~/.ssh/id_rsa && summarise` delivers the key to the channel. No domain allow-list stops this.

Both are the **tricked-agent** threat, not the malicious-agent threat — and that is the likelier one for a product whose input surface is untrusted chat and untrusted web content.

So: reads-open accepts a real, non-hypothetical exfiltration channel for anything an agent is induced to read. It is accepted because the capability cost of enumeration is unbearable and the protection removed was already partial — **not** because it is free.

**Required compensating control (normative):** known secret material MUST be scrubbed from tool output before it enters context or the transcript. `RegisterSensitiveValues` already exists in the credential boot contract (ADR-004) and is the correct hook. This is the only layer that addresses paths 1 and 2, and it is a condition of this ADR, not a follow-up.

## 4. Mechanism — one principle, two renderings

**The principle: the secret set is never reachable by a child, on any platform.**

The first draft used a different approach per platform and a reviewer broke the macOS one. The principle is now stated once and each backend renders it in its own idiom, so there is a single thing to reason about and a single thing to test.

### 4.0 The secret set, and why it is one level deep

`$OMNIPUS_HOME` contains both agent working data and the material that defines the sandbox. **[VERIFIED]** on a real install, the split is clean:

| Must stay agent-accessible | Must be unreachable |
|---|---|
| `agents/` (agent **workspaces**) | `master.key`, `credentials.json` |
| `sessions/`, `skills/`, `projects/`, `workspaces/` | `config.json` (holds `sandbox.mode`) |
| `logs/`, `browser/`, `system/` | `cli.token` (live gateway credential) |
| | `entities/` (per-agent **tool policy**) |

Note `agents/` and `entities/` are different things — workspaces versus policy — so excluding policy does not touch an agent's own working directory.

The excluded set is **five entries at one level**. That matters for §4.2: the enumeration is small and shallow, not a filesystem walk.

This is the "split" half of the decision and is worth doing on its own merits: it closes the pre-existing hole in §11 where an agent rewrites its own sandbox configuration.

### 4.1 macOS — global allow, explicit deny on the secret set

**[VERIFIED]** Seatbelt is last-match-wins, so:

```
(allow file-read*)                                   ← reads open
…policy allows…
(deny file-read* file-write* (subpath "<secret>"))   ← emitted LAST
```

**The deny MUST cover `file-write*`, not only `file-read*`.** A read-only deny was **[VERIFIED]** defeated in two syscalls: `rename(2)` is not a read, so a child moves `master.key` out of the denied path and reads it normally; `truncate` destroys the vault with no read at all. Denying writes removes both, because rename and truncate both require write on the source.

Ordering is normative: the deny block comes after every policy-derived allow. **[VERIFIED]** with the order flipped, the same rules leak every secret silently.

### 4.2 Linux — grant siblings, never grant the secret **[INFERRED]**

Landlock has no deny primitive, so exclusion means **never granting**. Walk from `/` down to each secret; at every level grant read on the siblings and skip the entry on the path.

Two properties make this cheap rather than a filesystem walk:

- Only the directories **on the path to a secret** are enumerated — about three levels. Everything else is granted whole at the top, so a new toolchain under `/opt` or `/usr/local` is covered automatically. That is the difference between this and the enumeration §2 condemns.
- **[VERIFIED]** `RestrictCurrentThread` already builds *"a fresh ruleset matching the saved policy"* on **every child spawn**, not once at boot. Computing the enumeration per spawn therefore costs a few directory listings and some tens of extra syscalls per process start — negligible against `fork`/`exec` — and means **there is no staleness**: a directory created seconds ago is included.

**Normative:** if a directory listing fails, the spawn MUST fail. Falling back to granting the parent would expose the secret silently, which is the exact failure shape this ADR was written to eliminate.

### 4.3 Windows — out of scope, tracked separately

**[VERIFIED]** there is no Windows sandbox backend: `sandbox_other.go` is `//go:build !linux && !darwin` → `FallbackBackend`, whose `CheckPath` has zero non-test callers. `hardened_exec_windows.go` does job objects only.

A viable design exists — a Low integrity-level token (a process may lower its own integrity with **no admin rights**), spawning children with it, plus deny ACEs on the secret set. Windows permissions support real deny rules, so the principle in §4 maps directly. `golang.org/x/sys/windows` is already a dependency and exposes the needed APIs, so it stays pure Go.

It is **deliberately not in this ADR**: bundling a second platform's sandbox into a change about the read model would repeat the mistake round 1 caught. What IS in scope is correcting CLAUDE.md, which currently claims a Windows backend that does not exist — anyone reasoning about the boundary from the docs is misled today.

## 5. What stays default-deny

- **Writes** — workspace, `/tmp`, `$TMPDIR`, operator `allowed_paths`. Nothing else. This is the property most operators actually assume ("the agent cannot modify my system") and it is untouched.
- **Network** — default-deny with a port allow-list, until egress control lands (§8).

## 6. What this removes from the policy

**[VERIFIED]** `readOnlySystem` grants `AccessRead | AccessExecute` — the execute bit exists so the dynamic loader can mmap shared objects. Under this ADR both bits become redundant *on macOS and Linux*, so the list can go. It must be removed as a unit with the Seatbelt preamble's `(allow process-exec (subpath "/bin") …)` lines, not piecemeal: deleting the read bits alone while execute stays confined would stop every child from starting.

`sandbox.allowed_exec_paths` (ADR-052 AC-6) becomes unnecessary for its original purpose. It is **retained** as an operator tool for the `confined` model (§9), not deleted.

## 7. Security consequences that must be stated, not implied

### 7.1 Pentest findings C1/C2 — preserved on macOS and Linux, reversed on Windows

An earlier draft concluded that reads-open reverses pentest items C1/C2 (`master.key`, `credentials.json` readable) on Linux and Windows. The §4 principle changes that:

- **macOS** — the secret set is denied for read *and* write (§4.1). C1/C2 hold, and the rename/truncate attacks are closed too, which they were not before.
- **Linux** — the secret set is never granted (§4.2). C1/C2 hold. **[INFERRED]** — unverified until run on a Linux host.
- **Windows** — no sandbox exists, so C1/C2 do not hold and did not hold before this ADR. Unchanged, not reversed.

The two red-team tests keep their meaning on macOS and Linux and should be extended to cover rename and truncate, not only read.

### 7.2 Multi-tenant deployment is incompatible with `open`

Reads-open means any tenant's agent reads every other tenant's `$OMNIPUS_HOME` subtree and the shared `master.key`. Multi-tenant deployments MUST pin `filesystem_model: confined`. This must appear in operator documentation, not only here.

### 7.3 Test impact — ten tests are invalidated

Every one of these asserts an out-of-workspace read is denied and breaks under `open`. Each needs a disposition in the spec:

| Test | Disposition |
|---|---|
| `redteam_master_key_test.go::TestRedteam_MasterKey_Exfil_Blocked` | darwin-only, or retarget to the tool layer |
| `redteam_credentials_test.go::TestRedteam_Credentials_Exfil_Blocked` | same |
| `seatbelt_adversarial_darwin_test.go::…SymlinkEscapeDenied` | retarget at a denied credential path |
| `…RuntimeSymlinkRaceDenied` | retarget |
| `…DotDotTraversalDenied` | retarget |
| `…MetacharacterPathsStillEnforce` (2nd assertion) | retarget |
| `…ExecGrantDoesNotWidenReadBoundary` | delete — vacuous under open exec |
| `…ChildCannotCreateHardlinkOutward` (`ln secret` arm) | retarget |
| `…PreexistingHardlink_IsAKnownGap` | delete — subsumed |
| `TestDefaultPolicy_ExecPathsNeverCarryWriteBit` | keep — write confinement is unchanged |

The macOS deny-list gives a natural replacement: the same attacks, retargeted at a denied credential path, must still fail — and the symlink and hardlink variants are the interesting ones against a path-based deny.

Deleting these without replacement removes the only evidence the boundary was ever tested (ADR-052 AC-6 checklist item 2).

### 7.4 The IPC default-deny is load-bearing and undocumented

**[VERIFIED]** A path-based deny cannot stop a *daemon-mediated* read. Under a profile with the credential deny in place, `securityd` serviced a keychain query against a denied file on the sandboxed client's behalf — the client was denied the file and served by the daemon anyway. Separately, `launchctl bootstrap` outside the sandbox exfiltrated a master key perfectly.

Both are blocked today only by the preamble's `(deny default)` covering `mach-lookup`, not by the deny-list. Neither this ADR nor the spec previously mentioned that dependency.

That matters because §2's whole pressure is toward *loosening* profiles when tools break. The first operator who adds `(allow mach-lookup)` to fix a toolchain silently deletes the protection for the data-protection keychain and reopens the launchd route, with no signal.

**Normative:** the mach/IPC default-deny is part of the credential protection. A rendered `open` profile MUST NOT widen `mach-lookup` beyond the mDNSResponder entry, and a test MUST assert it.

Note also that `~/Library/Keychains` protection is partial: **[VERIFIED]** effective for the legacy file-based keychain (read in-process), not for the modern data-protection keychain (read by `securityd`).

## 8. Sequencing: egress control

The first draft made this ADR conditional on egress control landing. That constraint is **withdrawn as unsatisfiable**, for reasons that must be recorded:

- **[VERIFIED]** `computeRights` sets `handledAccessNet` only when `abiVersion >= 4` (Landlock ABI v4 = kernel 6.8+). On Ubuntu 22.04 (5.15), Debian 12 (6.1) and RHEL 9 there is **no kernel network enforcement and no ADR can add one**. A precondition that can never be met on much of the installed base is not a constraint, it is a block.
- **[VERIFIED]** `pkg/sandbox/egress_proxy.go`: "HTTP/HTTPS only. Raw TCP connect bypasses the proxy entirely." It is wired via `HTTP_PROXY`/`HTTPS_PROXY` env vars, which a shell child can ignore. The layer nominated as the exfiltration defence does not currently constrain the actor this ADR newly empowers.
- Kernel-enforced egress would require removing 443 from `DefaultConnectPorts` and allowing only a proxy port — instantly breaking every child that speaks TLS directly and ignores proxy env vars, **with the identical failure signature this ADR exists to eliminate.** Egress control trades a filesystem enumeration problem for a network enumeration problem of the same defect class. It is more tractable (the host set is smaller and centrally proxied) but it is not free, and pretending otherwise would repeat this ADR's original mistake.

Egress control remains the right next investment and needs its own ADR. This ADR no longer depends on it, and §3.2 no longer claims it makes the trade free.

## 9. Rollback and operator control

**New config key:** `sandbox.filesystem_model: open | confined`, default `open`.

- `open` — this ADR.
- `confined` — today's behaviour: enumerated reads and execute, `allowed_exec_paths` honoured.

Required because the founder's rationale is a developer laptop, while servers and multi-tenant deployments have the opposite need. Without it there is no way back.

- **`sandbox.mode` interaction:** none. `permissive` already skips `landlock_restrict_self`; `off` applies nothing. The model selects *which policy* is computed, the mode selects *whether it is enforced*.
- **`DefaultChildPolicy`:** survives as the `confined` path and as the target for #156's per-thread work. Not retired.
- **Migration:** existing `allowed_paths` entries keep their **write** grants under `open`; their read/exec contribution becomes redundant. No config rewrite is required and none is performed.

## 10. Observability (normative)

The change is invisible unless reported:

1. `sandbox.applied` MUST log the model (`filesystem_model=open|confined`).
2. `GET /api/v1/security/sandbox-status` MUST expose it as a field. This is a contract-generated wire type — `contracts/components/schemas/SandboxStatus.yaml` — so hard constraint #8 applies: schema first, `scripts/gen-contracts.sh`, generated diff committed atomically.
3. On Linux and Windows, boot MUST warn once that credential files are not kernel-protected, naming `master.key` explicitly.
4. `renderSandboxMode` MUST reflect the model. Its doc comment states the mapping is "pinned in spec v7 and must not be changed without a corresponding spec update" — so the spec-v7 update is part of this work, not optional.

## 11. Consequences

**Positive**
- The §1.1 defect class closes for reads *and* execute — the whole class, not a quarter of it.
- Policy shrinks: `readOnlySystem`, the preamble's exec block, and the `allowed_exec_paths` seed all become unnecessary in `open`.
- Behaviour matches what users arriving from Claude Code or Codex expect.

**Negative**
- Real loss of confinement: total on Linux and Windows, partial on macOS.
- **C1/C2 reversed on Linux and Windows** (§7.1) — the master key becomes readable.
- Multi-tenant requires `confined` (§7.2).
- Ten tests need rework (§7.3).
- Two platforms behave differently — a documentation and support burden.

**Neutral**
- Writes are untouched by this ADR.

**Not neutral, and not caused by this ADR — a pre-existing finding surfaced while reviewing it**

"The agent cannot modify my system" does **not** hold today, independent of the read model. `$OMNIPUS_HOME` is granted RWX by `DefaultPolicy`, and it contains `config.json` (which holds `sandbox.mode`), `agents/<id>` (per-agent tool policies), `cli.token` (a live gateway bearer token), `master.key`, `credentials.json`, and plaintext session transcripts.

**[VERIFIED]** with real children: a sandboxed agent can read the gateway API token, rewrite its own `sandbox.mode` to `off`, flip its own tool policy from `deny` to `allow`, and truncate the master key. The sandbox's own configuration lives inside the region the sandbox makes writable, so the boundary is self-disabling — an attacker need not escape it, only edit it and wait for a restart.

This predates ADR-060 and is tracked separately. It is recorded here because ADR-060 must not claim a write-confinement property the product does not have.

## 12. Open questions

1. **Master key protection under `open`** (§7.1) — memory-only, separate uid, or accepted in writing? The spec must answer this.
2. **Credential deny-list contents** for macOS: `~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.kube`, `~/.docker/config.json`, `~/.netrc`, `~/.git-credentials`, `~/.npmrc`, `~/Library/Keychains`, plus `SecretFilesRelative`.
3. **Deliberate asymmetry** — macOS denies credentials, Linux cannot. Recommendation: keep it. Discarding real protection so two platforms match is strictly worse for users.
4. **Windows** — build a restricted-token/DACL backend, or document its absence and move on?
