# ADR-060 — Reads and execute default open; writes stay confined

- **Status:** Proposed
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

## 4. Per-platform mechanism

### 4.1 macOS — open, with a kernel-enforced credential deny-list

**[VERIFIED]** Seatbelt is last-match-wins, so a broad allow followed by narrow denies works:

```
(allow file-read*)                          → /etc/hosts readable
(deny  file-read* (subpath "<secret>"))     → Operation not permitted
```

**Normative ordering — the deny block MUST be emitted LAST.** Last-match-wins cuts both ways. A review demonstrated the defeat:

```
(allow file-read*) (deny file-read* (subpath S)) (allow file-read* (subpath S))
→ S is readable again, silently
```

`renderSeatbeltProfile` walks `policy.FilesystemRules` in policy order and emits `(allow file-read* (subpath …))` for every rule carrying read or execute — and `DefaultPolicy` puts `$OMNIPUS_HOME` **first**. So a deny block placed in the preamble would be re-opened by the workspace allow, and `master.key` would be readable through a profile that appears to deny it. That is the §1.1 "renders correctly, matches nothing" failure again.

Requirements:
1. The deny block is emitted after all policy-derived allows. No `(allow file-read* …)` may follow it.
2. Deny paths MUST be symlink-resolved with the same treatment allows get (`resolveSeatbeltPath`), or `~/.ssh` on a symlinked home and `/var` → `/private/var` will not match.
3. A deny for a path that does not exist yet (`~/.aws` created next week) MUST still be emitted. `deepestExistingAncestor` was written for allows and needs review for denies.
4. A test MUST assert that a policy granting `$OMNIPUS_HOME` still denies `master.key`.

### 4.2 Linux — open, with no kernel exception **[INFERRED]**

Landlock is **grant-only**. Rights accumulate along the ancestry; a deeper rule with fewer rights subtracts nothing, and stacked layers only intersect. There is no deny primitive, and "enumerate everything except the secrets" is the §2 defect in inverted form — a directory created after boot would be unreadable.

**Mount namespaces were rejected for the right reason.** They are reachable from pure Go (`CLONE_NEWUSER|CLONE_NEWNS` via `syscall.SysProcAttr`) with no new dependency, so hard constraint #1 does not forbid them. They are rejected because unprivileged user namespaces are disabled by default on Debian, restricted by AppArmor on Ubuntu 24.04, and blocked by Docker's default seccomp profile — availability would degrade unpredictably, in exactly the way §2 condemns.

**Implementation constraints (getting this wrong bricks boot):**

- Opening reads means removing `landlockAccessFSReadFile | landlockAccessFSReadDir` from `handledAccessFS` (currently `lb.allRights`, `sandbox_linux.go::computeRights`).
- `accessToLandlockRights` adds read bits unconditionally. `landlock_add_rule` returns **EINVAL** when `allowed_access` is not a subset of `handled_access_fs`, and `ApplyWithMode` hard-errors on EINVAL (only ENOENT is soft-skipped) → **gateway boot aborts, exit 78**.
- Therefore: every rule's rights MUST be masked against `handledAccessFS`, and any rule whose masked rights are **zero** MUST be dropped, not passed (zero `allowed_access` is also EINVAL). Read-only rules like `/dev/urandom` and `/etc/hosts` become zero-rights and must disappear.

**Scope limitation.** `ApplyToCmd` is a no-op on Linux; enforcement comes from `StartLocked` re-applying the saved policy to the launching thread. There is **one Landlock domain shared by the gateway and every child**, so opening reads opens them for the gateway process too. This cannot be scoped to children until the per-thread work in #156 lands — at which point Linux could return to confined reads for children while leaving the gateway unconstrained. That is a real future rollback path.

macOS is the opposite: `SeatbeltBackend.ApplyToCmd` renders a per-child profile, so a per-child posture is expressible there today.

### 4.3 Windows — no change, because there is no sandbox **[VERIFIED]**

`pkg/sandbox/sandbox_other.go` carries `//go:build !linux && !darwin`, so Windows selects `FallbackBackend`. `hardened_exec_windows.go` does job objects and kill-on-close only — no DACL, no restricted token, no filesystem policy. `FallbackBackend.CheckPath`/`CheckPathAccess` have **zero non-test callers**, so even the app-level check is unwired.

CLAUDE.md's "Windows (Job Objects+Restricted Tokens+DACL)" describes an intention, not shipped code. **Correcting that statement is in scope for this ADR**, because anyone reasoning about the boundary from the docs is currently misled.

Windows reads and execute are already open. This ADR does not make Windows worse.

## 5. What stays default-deny

- **Writes** — workspace, `/tmp`, `$TMPDIR`, operator `allowed_paths`. Nothing else. This is the property most operators actually assume ("the agent cannot modify my system") and it is untouched.
- **Network** — default-deny with a port allow-list, until egress control lands (§8).

## 6. What this removes from the policy

**[VERIFIED]** `readOnlySystem` grants `AccessRead | AccessExecute` — the execute bit exists so the dynamic loader can mmap shared objects. Under this ADR both bits become redundant *on macOS and Linux*, so the list can go. It must be removed as a unit with the Seatbelt preamble's `(allow process-exec (subpath "/bin") …)` lines, not piecemeal: deleting the read bits alone while execute stays confined would stop every child from starting.

`sandbox.allowed_exec_paths` (ADR-052 AC-6) becomes unnecessary for its original purpose. It is **retained** as an operator tool for the `confined` model (§9), not deleted.

## 7. Security consequences that must be stated, not implied

### 7.1 Pentest findings C1/C2 are REVERSED on Linux and Windows

`SecretFilesRelative` (`master.key`, `credentials.json`) exists to close pentest items C1 and C2 (v0.2 #155 item 8). Under reads-open on Linux and Windows, **Omnipus's own root of trust becomes readable by any agent shell.** The master key decrypts every stored provider key and channel token.

The named fallbacks are weak: `pkg/tools/shell.go`'s guard is a literal-token deny on `master.key`, defeated by `cat ~/.omnipus/mast*.key` or any interpreter. The `resolvepath.go` carve-out is real but governs Omnipus's own file tools, not `bash`.

**This is the single largest cost of this ADR and it must not be discovered later.** Options, to be decided in the spec:

- keep the master key in gateway memory only after boot, so there is no readable file;
- give it a different uid;
- accept it in writing for the `open` model and require `confined` for any deployment where it matters.

**macOS is NOT unaffected.** An earlier draft said the deny-list covers it. Adversarial testing on this host disproved that:

- The deny is `file-read*` only. `rename(2)` is not a read. `$OMNIPUS_HOME` is granted RWX (FR-2.3), the deny sits *inside* that write grant, so a child renames `master.key` to a name outside the deny and reads it in two syscalls. **[VERIFIED]** — `mv` appears to fail only because it `stat()`s first; raw `rename` succeeds. `truncate` also succeeds, destroying the vault irreversibly without reading anything.
- Credential paths in **non-writable** locations (`~/.ssh`, `~/.aws`, `~/.gnupg`) are genuinely protected: symlink, hardlink-creation, case variance, `../` traversal, `openat`/`fchdir`/`dd`/`tar` were all **[VERIFIED]** to fail against them.

So the deny-list works exactly where the parent directory is not writable, and fails where it is. Any deny path inside a write grant MUST also deny `file-write*` (covering rename, unlink, truncate, chmod), or the secret must be moved out of the writable subtree. This is a spec requirement, not a note.

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
