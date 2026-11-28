# ADR (proposed): Agents run command-line programs (git, gh, fly …) with stored credentials and guided logins

- **Status:** Proposed, revision 2 (the one correction round after the grill-spec review, which returned REVISE: `/workspace/logs/1960/adr-cli-tools-credentials-review.md`). Founder questions Q1–Q4 are still open; this revision assumes no answers.
- **Date:** 2026-11-21
- **Deciders:** Daniel Piatkowski (founder) for Q1–Q4; architect for the technical decisions
- **Issue:** elicify-ai/omnipus #1960 (P0, the absolute-path refusal) and the founder's broader ask of 21 Nov 2026
- **Code baseline:** `origin/release/v0.1.1` @ `616261783`, read in a throwaway scratch worktree (since removed). No repository changes.
- **Evidence level (highest used):** 1 (user input: founder brief, issue text) plus codebase facts. Two saved run receipts: `/workspace/logs/1960/guard-probe.log` (the real guard function, exit 0) and `/workspace/logs/1960/git-helper-probe.log` (offline git helper composition, from the reviewer)
- **Related ADRs (by title):** ADR-062 "Reads and execute default open; writes stay confined"; ADR-068 "Bash text guard — third rule layer" (not the other ADR-068 on providers UX, which is the source of the device-code sign-in); ADR-092 "Shell permission modes: Ask / Auto / God Mode" (D8 network pre-flight); ADR-004 "Credential boot contract" (masking registration); "Export / import packages" ADR (D6 "secrets never leave")

Labels: **[FACT]** read in code or run (receipt named) · **[INFERENCE]** reasoned from code, not run · **[ASSUMPTION]** needs checking · **[UNKNOWN]**.

> ⚠️ **Correction (revision 2):** Revision 1 said `GH_CONFIG_DIR=/tmp/x gh …` was refused because the assignment counts as a write outside the workspace. That was wrong. The guard treats the first word of the segment, which is the assignment, as **the program being run** (`exec`). The re-run receipt shows the refusal text "RUNNING a program …" for that case (`guard-probe.log`). Impact: S0 must tell an assignment apart from the real program before it relaxes the exec rule. D1 is narrowed accordingly.
>
> ⚠️ **Correction (revision 2):** Revision 1 overstated several facts: masking ("same process-wide replacer" everywhere, `***`), PTY ("no PTY code anywhere"), the export ADR (it was described as shipped reuse), the network classifier (`fly` was assumed to be in it), and approval modes (Ask and Auto were treated as the same). Section 1 now gives the corrected facts. Impact: D3–D7 change. The direction (Option B) does not.

---

## 1. Problem Understanding

**Bottom line:** #1960 is one specific defect: running a program **by its full path** is refused, while the same program **by name** passes the guard. That can be fixed alone (S0) and closes the issue. The founder's broader goal is for agents to use `gh`, `git` over HTTPS and `fly` easily, with logins. That goal hits further obstacles (network hosts, credentials, logins) and is a separate feature (S1–S3).

### What happens today, by spelling

```
 "/usr/local/bin/gh api user"            "gh api user"
          │                                     │
  ① text guard: absolute program path    ① text guard: nothing to check → passes   [FACT, guard-probe.log]
     outside workspace → REFUSED (#1960)        │
          ✗                              ② approval: Ask asks; Auto flags gh as network-capable;
                                            no URL in the text → "blind" need: ports only, no host   [FACT, code]
                                                │
                                         ③ egress proxy (when configured): api.github.com not on the
                                            default host list → 403                                  [INFERENCE, high]
                                                │
                                         ④ authentication: no token in the child env unless the
                                            person set one; macOS keychain not reachable from the
                                            sandboxed child; config dirs outside the workspace not
                                            writable under an enforcing sandbox                      [INFERENCE, medium-high]
                                                │
                                         account access succeeds
```

The four steps are separate questions: the guard accepts the command, the network connects, the program authenticates, the account answers. A green result at one step proves nothing about the next.

### Current state (corrected)

| # | Area | What the release code does | Label |
|---|---|---|---|
| C1 | Text guard, program path | With the workspace guard on and no exemption (safe paths, configured allowed paths, mounts), an absolute path **in command position** outside the workspace is refused (`shell_path_guard.go` `checkPathSegment`, `outsideWorkDirRefusal`). Results: `/usr/local/bin/gh auth status; …` refused; `gh auth status; gh api user --jq .login`, `fly auth whoami`, `git ls-remote https://github.com/cli/cli` all pass. Passing the guard proves only that; it does not show the program is installed, logged in or online. | FACT (`guard-probe.log`, exit 0) |
| C2 | Kernel execute vs text guard | ADR-062 opened execute at the kernel layer (Linux `linuxFilesystemRules`, Seatbelt profile, `ExecOpen`). The text guard still refuses execution by absolute path, so the two layers disagree. **But by name and by full path can run different files:** the per-turn PATH puts workspace-installed and shared runtime bins before the host PATH (`shell_env.go` layers 1–3). A full path is an explicit choice of one file. | FACT |
| C3 | Refusal text | Advises copying/installing the program inside the workspace, mounting, or switching the guard off. | FACT |
| C4 | Assignment prefix | `GH_CONFIG_DIR=/tmp/ghcfg gh auth status` is refused **as a program run**: the classifier takes the segment's first word, here the assignment, as the program (`pathUseClassifier.classify`). | FACT (`guard-probe.log`) |
| C5 | Child environment | Ordinary sandboxed runs start from a closed allow-list (`pkg/childenv`: PATH, HOME, USER, locale, proxy vars, `XDG_*`, `OMNIPUS_CHILD_*`). External agent CLIs get a separate credential list on their own runner path only. **God Mode is the exception:** it inherits the gateway environment minus three boot secrets (`scrubbedEnv`). | FACT |
| C6 | Agent-private variables | `request_secret` (variable + workspace + agent) stores a value encrypted (kind `agent_private`). On a successful, authorized resolution, every bash child of that agent in that workspace receives it, after masking registration. A channel turn gets it only when the person opted in. If the store is missing or unreadable, the command still runs with a value-free notice instead. Name protection comes from `CheckPrivateVariableName` (grammar plus protected names), not from the order variables are appended. | FACT |
| C7 | Masking | Exact-value replacement with `[FILTERED]`. The model/transcript filter (`FilterSensitiveData`) **can be switched off** and skips content shorter than `filter_min_length` (default 8 bytes). Values of 3 bytes or fewer are never registered. Registration replaces the whole set on each call. The gateway log uses the registered replacer. **Audit rows use a different, pattern-based redactor.** User docs already call filtering best-effort and switchable (`docs/security.md`). | FACT |
| C8 | Export | The export ADR specifies secret names only (`needs_secrets`). The **shipped** exporter exports one agent and builds `needs_secrets` from removed executor environment names. The ADR's broader MCP placeholder scheme is a spec, not shipped reuse. Neither promises that free-text or child-written files contain no secrets. | FACT |
| C9 | Network | The fixed network-capable list contains `gh` and `git` but **not `fly`/`flyctl`**. Default proxy hosts: npm hosts, `github.com`, `raw.githubusercontent.com`, `objects.githubusercontent.com`, `nodejs.org`. Not `api.github.com`, no Fly host. A command with no URL in its text is a "blind" need: ports only. **The dynamic per-run host grant runs only in Auto** (`enforceNetworkPreflight` returns early outside Auto). Proxy variables are injected only when a proxy address is configured. | FACT |
| C10 | Login writes | Under an enforcing per-turn policy, writes outside the workspace and mounts are refused, so `gh auth login` / `fly auth login` from an agent most likely cannot save to `~/.config/gh` / `~/.fly`. Windows and fallback backends have no equivalent kernel confinement. An existing plaintext CLI config may still be **readable** under the open-read model. | INFERENCE (conditional) |
| C11 | macOS keychain | The child Seatbelt profile allows only `mDNSResponder` and `trustd.agent` mach services, not the general keychain services. A sandboxed `gh` that relies on keychain storage therefore probably sees no login. Env tokens and file storage are separate paths. **A gateway-owned process is outside this child profile.** | FACT (profile) / INFERENCE (gh effect) |
| C12 | Terminal | Bash exposes run / poll / read / kill, with no way to type into a running program. Dormant `ProcessSession.Write`/PTY fields exist in `pkg/tools/session.go`, and `go.sum` lists `creack/pty`. There is no **exposed** interactive terminal. | FACT |
| C13 | Provider sign-in | Model-provider device-code sign-in has server-side sessions with expiry and single-use state, persists before reporting success, and has a dialog with device-code / expired / denied / error states (`rest_sign_in.go`, `SignInDialog.tsx`). It is a reusable **pattern**, not a generic CLI login runner. | FACT |
| C14 | MCP | `request_secret` can fill `env_key` of an existing stdio MCP server. The built-in channel catalog has no GitHub or Fly entry. Whether some other connector exists was not checked exhaustively, and the decision does not depend on it. | FACT (narrowed) |
| C15 | Program path validator | `pkg/thirdpartycli` checks the path is absolute, resolves it, refuses agent-writable roots (all agents, workspaces, mounts, temp roots), then checks the file. It does not prove who published the program. | FACT |
| C16 | git evidence guard | Mutating git against Omnipus's hidden `work/.git` is refused; ordinary repos get an explicit allow from this guard, and the other controls still apply. | FACT |

## 2. Extracted Requirements

### Functional
- **FR-1 (S0, #1960):** Running a program by absolute path MUST NOT be refused **only because** the file lies outside the workspace. Protected-file checks, write checks on other operands, explicit command rules and existing workspace/mount execution stay as they are. `[issue #1960]`
- **FR-2:** A person MUST be able to give Omnipus a credential for a program (GitHub, Fly, custom) and have agents that are allowed to use it receive it in their commands. Scope is founder Q1. `[founder brief]`
- **FR-3:** `git` over HTTPS MUST use the selected stored GitHub credential for matching hosts, ignore any other helper the machine already has for those hosts, and leave no extra copy of the token anywhere. `[founder brief; review MAJ-001]`
- **FR-4:** Where a verified non-interactive recipe exists, a person MUST be able to complete a program's browser/device login from the web app, with the token captured into the Omnipus store. Where no recipe exists, the card says so and offers paste-token. `[founder brief]`
- **FR-5:** An enabled program's commands MUST be able to reach its named hosts in Ask, Auto and God Mode, without hand-editing `config.json`. `[INFERENCE from C9]`
- **FR-6:** Refusals from the path guard MUST name the guard rule and MUST NOT advise copying programs or switching the guard off. Errors that come from the program itself (its own 403, "not logged in") are passed through unchanged, not relabelled. `[issue #1960; review MAJ-005]`
- **FR-7:** "Connected as <account>" MUST be shown only after a real identity check with the captured credential. `[issue #1960]`
- **FR-8:** Agents MUST have the same program on/off control for an agent that the Settings page has (agent-tool parity). `[repo Definition of Done; review MIN-002]`

### Non-functional
- **NFR-1 Secrets hygiene, sink by sink (D7).** Program credentials do not appear in the model context, transcript, gateway log, audit rows or export bundles through Omnipus's own paths. Proportionate, not hardened: transformed or split values and files an agent writes itself are not covered.
- **NFR-2 Ease:** connecting GitHub takes one card or one paste; no config file edit.
- **NFR-3 Platforms:** macOS (Seatbelt), Linux (Landlock where available; whether it is commonly missing on Fly hosts is an [ASSUMPTION], not measured), Windows (no kernel sandbox). Each slice states which platforms it supports.
- **NFR-4 No regression** of protected files, write confinement, the git evidence guard, or ADR-092 approval semantics. This is a requirement with regression rows in the spec, not yet a fact.

### Constraints
- Single-user local product; the founder's proportionate-security rule.
- No second credential store; reuse the encrypted store, masking, approvals and the sign-in pattern.
- Contract first: program metadata, connection state, login attempt status/cancel and any chat card are defined in `contracts/openapi.yaml` / `contracts/asyncapi.yaml` before handlers and UI.
- No vendor OAuth app registration without the founder (outward-facing step).

## 3. Gaps and Ambiguities

| # | Missing / ambiguous | Why it matters | Working assumption | Resolve by |
|---|---|---|---|---|
| G1 | Credential scope: per person or per agent+workspace | Setup effort vs isolation | None: both shapes are designed below | Founder Q1 |
| G2 | Do `gh auth login --web … --insecure-storage` and `fly auth login` finish with stdin closed and an isolated config, and can the token be read back from that isolated config? | Decides whether S2 can support each program | gh likely `[ASSUMPTION, medium]`; fly unknown `[UNKNOWN]` | Spike per supported version and platform (§9) |
| G3 | Hosts each program needs | Profile host lists | gh: `api.github.com`, `github.com`, `uploads.github.com`, `codeload.github.com`; fly: `api.fly.io`, `api.machines.dev` `[ASSUMPTION, medium]` | Spike with proxy audit on; read `egress_denied` rows |
| G4 | Real 403 on the founder's Mac for plain `gh api user` | Step ③ is inferred | `[INFERENCE, high]` | Live repro on the Mac instance |
| G5 | Channel turns (Telegram/Discord) and program credentials | Prompt-injection exposure | None: both shapes are designed below | Founder Q4 |
| G6 | Windows login and git helper quoting | Platform support | Paste-token supported on Windows in S1; guided login "unsupported" on Windows until proven | Spec platform rows; hands-on check |
| G7 | Import an existing local login | Fast for the founder; reads the person's keychain/config | None: designed as an explicit button only | Founder Q3 |
| G8 | Installed `gh`/`fly`/`git` versions on users' machines | Flags and `GIT_CONFIG_COUNT` (git ≥ 2.31) | Profile records the minimum version; older versions get "unsupported version" | Spec |

## 4. Decision Criteria

| Criterion | Weight | Notes |
|---|---|---|
| Ease for the person | 30 % | Founder's main ask |
| Agent power (any token-in-env CLI) | 20 % | "Think holistically" |
| Secret hygiene and correct account | 20 % | Proportionate floor |
| Reuse / build cost | 20 % | Store, masking, approvals, sign-in pattern exist |
| Each slice visible | 10 % | Delivery rule |

## 5. Option Analysis

All options share **S0** (narrow #1960 fix). They differ in how credentials and logins work.

### Option A: today's secret cards plus documented recipes
| Dimension | Assessment |
|---|---|
| Strengths | Almost no code after S0. |
| Weaknesses | Tokens made by hand; set up per agent and per workspace; agents improvise git helper config; hosts still need `config.json`; Fly is not classified as network-capable. |
| Risks | Tokens written into `.git/config` or remote URLs inside the workspace; the wrong account is used through existing helpers (MAJ-001). |
| Complexity / cost | Low / days |
| Operational | Every user hits the same walls |

### Option B: program profiles, stored credentials, guided login (recommended)
A small built-in catalog (git, gh, fly; plus "custom"), stored as data. Each entry gives the token env vars, program names, hosts, git helper host, and an optional verified login / read-back / identity recipe.
| Dimension | Assessment |
|---|---|
| Strengths | One-time connect; correct git account (env-only helper with reset); hosts attached in every mode; reuses store, masking, approvals, sign-in lifecycle. |
| Weaknesses | Catalog upkeep; guided login only for programs with a verified recipe. |
| Risks | Vendor output/flags change → attempt ends "unsupported", paste-token fallback. A tricked agent can **use** an account it is allowed to use (accepted). |
| Complexity / cost | Medium / 3–4 slices |
| Operational | Fake-CLI orchestration tests plus real-CLI hands-on rows per platform |

### Option C: interactive web terminal
| Dimension | Assessment |
|---|---|
| Strengths | Any interactive program. |
| Weaknesses | New subsystem on 3 OSes; logins still write to keychain/config, so the token must still be captured; terminal bytes bypass masking unless every byte is filtered. |
| Risks | Unmasked output; an unsandboxed person-driven terminal; Windows terminal differences. |
| Complexity / cost | High / weeks |

### Option D: Omnipus-native vendor sign-in (own GitHub OAuth app) / connectors
| Dimension | Assessment |
|---|---|
| Strengths | Cleanest GitHub UX; no CLI files involved. |
| Weaknesses | Needs an Elicify-owned OAuth app (outward-facing); GitHub only; org policies may refuse OAuth-app tokens `[ASSUMPTION]`. |
| Risks | Vendor-specific upkeep; borrowing `gh`'s own client id would be impersonation, so it is not an option. |

## 6. Recommended Architecture

**Option B**, delivered in slices. **S0 ships independently and closes #1960.** Option D stays a possible later GitHub upgrade (founder Q2 C), and Option C stays deferred.

Why B: it is easiest for the person (connect once), it uses the correct account by construction (D4), and it reuses existing machinery. A leaves tokens in workspace files and uses the wrong account through existing helpers. C costs the most and widens the unmasked surface. D covers GitHub only and needs an outward-facing step.

### 6.1 Shape

```
 Settings → Programs                    Agent settings → Programs  (+ same field via agent tool, FR-8)
 ┌────────────────────────────────┐     ┌──────────────────────────────────┐
 │ GitHub (gh, git)  Connected as … │     │ GitHub [on]   Fly [off]          │
 │ Fly (fly, flyctl) Connect ▸      │     │ (channel switch: see Q4)         │
 │ Custom program    Add ▸          │     └──────────────────────────────────┘
 └────────────────────────────────┘
        │ paste token | guided login (verified recipes only) | import existing (Q3)
        ▼
 encrypted credential store, kind "program"  ─── masking set (+ retired values kept, D7)
        ▼
 bash child of an agent with GitHub on (ordinary sandboxed path):
   env  GH_TOKEN / GITHUB_TOKEN = token
        GIT_CONFIG_COUNT=2
          0: credential.https://github.com.helper = ""            (reset existing helpers)
          1: credential.https://github.com.helper = <fixed helper: answers "get" only,
                                                     reads token from env, ignores store/erase>
        GH_PROMPT_DISABLED=1
   network: program head → profile hosts attached to this run (Ask, Auto); God Mode: no proxy
```

### 6.2 Decisions

**D1: S0, narrow fix for #1960.**
- In `checkPathSegment`, an absolute path that is **the actual program word** of a segment is no longer refused only for lying outside the workspace. The classifier first skips leading `NAME=value` assignments and finds the real program word, using the same head resolution the network classifier already uses for wrappers (`env`, `command`, `nohup`, `time` and similar). Assignment values are then judged like any other operand, so `GH_CONFIG_DIR=/tmp/x` stays refused as an unproven write. Revision 1 wrongly called that a program run (C4).
- Unchanged: the protected-file (secret set) check runs first and still refuses; write checks on other operands; explicit command rules (deny beats ask beats allow); workspace and mount execution; approval modes.
- **No new writable-root scan for ordinary bash** (review MAJ-005). Under an enforcing sandbox, agents can write only to the workspace and mounts, and those are already in-boundary. On Windows/fallback a planted program could equally be run by name, so the absolute-path refusal added no real protection there.
- Refusal text: name the rule; remove "copy or install the program", "switch the guard off" and the path-obfuscation explanation from the agent-facing text. The operator remedy stays in the gateway log and docs.
- Diagnostics are limited to what the engine knows (guard rule, approval denial, egress proxy denial that the proxy audits). A program's own error output is passed through unchanged.
- Known difference stated in docs: by name, a workspace-installed copy may win (C2); by full path, the named file runs.

**D2: Program profiles are data.** Fields: id, display name, program names (`gh`; `git`; `fly`, `flyctl`; custom), token env vars, hosts, optional git helper host, minimum version, optional login recipe (command, isolated-storage flags, read-back command, identity command, allowed URL prefix for the card). Custom profiles allow env vars and hosts only, with no login recipe.

**D3: Credential storage and scope.** New store kind `program`, in the same encrypted file and under the same master key. Injected through the existing `PrivateEnvSource` seam. Scope depends on founder Q1:

| Q1 option | Storage key | Agent access | Effect on this design |
|---|---|---|---|
| A (recommended) one per person, on per agent | `program/<profile>` | Per-agent switch (`AgentConfig` field + agent-tool parity) | Adds one agent field and a Settings → Programs page |
| B per agent + workspace | `agent_private`-like key `(workspace, agent, profile)` | Implied by having a credential | No new agent field; setup repeated per agent/workspace; Settings page lists per pair |
| C one per person, all agents | `program/<profile>` | Always on | No switch; every agent, including helpers, can act on the account |

**D4: git uses the store with a reset helper chain (review MAJ-001).** Env-only config, applied last by git: an **empty** `credential.https://<host>.helper` entry first (clears every helper collected so far for that host, generic ones included), then a fixed helper that answers `get` only, reads the token from the environment, and does nothing on `store`/`erase`. The token value never appears in config text or a command line. The reviewer's offline receipt (`git-helper-probe.log`, git 2.43) shows that without the reset, an old helper is chosen and receives `store`; with the reset, neither happens. Requires git ≥ 2.31; older git → value-free notice "git too old for managed credentials". The managed names (`GIT_CONFIG_COUNT`, `GIT_CONFIG_KEY_*`, `GIT_CONFIG_VALUE_*`, `GIT_CONFIG_PARAMETERS`) become **reserved**: no private variable or custom profile may use them.

**D5: Precedence and conflicts (review MAJ-007).** Conflicts are refused visibly, so the selected identity never depends on ordering:
- Enabling a profile for an agent that already has a private variable with one of the profile's env names is refused, with a message naming the variable (remove it or keep the profile off). Creating such a private variable while the profile is on is refused too (`CheckPrivateVariableName` gains the active profile names for that agent).
- Two profiles enabled for one agent may not share an env name or a git helper host (checked at enable/save).
- Custom profile env names go through `CheckPrivateVariableName` (grammar, protected base names, `OMNIPUS_`/`LD_`/`DYLD_`, environment-setup names) plus the reserved git names.
- At run time a conflict that slipped through (for example a hand-edited config) leaves **both** values unset for that name and adds a value-free notice. Failing closed is better than silently picking one account.
- **Import:** an imported agent's program switches arrive **off, "needs local connection"**. They never bind silently to a local credential with the same name. The export lists them under `needs_secrets` by profile name.
- The channel-turn rule (Q4) applies the same way to private variables and program credentials.

**D6: Network for enabled programs, per mode (review MAJ-004).** An enabled profile adds itself to the network classifier's input **before** the permission decision. A segment whose resolved program word (by name, by absolute path after S0, or behind a wrapper) matches a profile program name counts as a **known-host** network need with the profile's hosts. It is not a blind need. Programs without an enabled profile keep today's behaviour. This is not a program allow-list.

| Mode | Behaviour |
|---|---|
| Ask | The command is asked as today. The card lists the profile hosts. Approve Once / Always Allow grants them under the same per-run / session egress token mechanism Auto uses. This extends today's Auto-only grant path to Ask. |
| Auto | Turning a profile on for an agent counts as the person's standing approval of **that program's hosts for that program's commands**. Auto runs them without a network prompt. Deny rules still win. Other hosts in the same command are still classified as today. |
| God Mode | No egress proxy is injected, so no host grant is needed. Env injection still happens. God Mode also inherits the wider gateway env (C5), so docs say a program credential is one of many values reachable there. |
| No kernel sandbox (Windows, fallback) | Same proxy grant when a proxy is configured; no kernel port control; stated in docs. |

Lifetime: hosts are granted per run (single-use token, revoked when the foreground run ends; background runs keep the existing documented "revoked at session end" gap). Turning a profile off affects new runs only. Already running background children keep their environment until they exit.

**D7: Secrets hygiene, sink by sink (review MAJ-003).**

| Sink | Contract | Mechanism |
|---|---|---|
| Model context, tool results, transcript | Program credentials are **always** filtered, even when the general filter switch is off and regardless of `filter_min_length`. | A separate always-on replacer for program values, applied where `tool_result_admit` filters today. Program credentials shorter than 8 characters are refused at save, so the short-value exclusions never apply. Replacement text `[FILTERED]`. |
| Gateway log | Filtered. | Program values added to the registered set (`RegisterSensitiveValues`). |
| Audit rows | **Value-free by construction**: events carry profile id, agent id, outcome; never values, URLs with codes, or raw login output. | Audit redactor not relied on; sentinel test asserts it. |
| Rotation / disconnect | Old values stay masked for the gateway's lifetime, because a running child may still print them. | "Retired program values" kept in the always-on replacer, not dropped by replace-semantics registration. |
| Login output | Never shown to agents or the transcript (D8). | Quarantined in the gateway-owned process. |
| Workspace files / git config | No token written by Omnipus. | Env-only helper (D4). |
| Export bundles | No program credential value; profile names only under `needs_secrets`. | Store is never exported; shipped exporter extended to list program connections. **Not promised:** files the agent itself wrote. |
| Disconnect | Removes the local copy only. | Card and docs say upstream revocation happens on github.com / fly.io. |

Accepted residual risks: transformed or split values (base64, wrapped lines) are not caught. An agent allowed to use an account can act as it, and masking hides the value, not that power.

**D8: Guided login as a gateway-owned attempt (review MAJ-002, MAJ-006).** Only for profiles with a **verified recipe** (G2). Otherwise the card offers paste-token and says guided login is not supported for this program/platform.

- *Program:* resolved and checked with the `thirdpartycli` path validator (absolute, resolvable, not in an agent-writable root, executable). This is where that stronger check belongs, because this process runs with gateway authority outside the agent sandbox.
- *Isolation:* a fresh protected temp dir in the Omnipus secret set. The environment is composed from scratch: no `GH_TOKEN`, `GITHUB_TOKEN`, `GH_ENTERPRISE_TOKEN`, `GH_HOST`, `FLY_API_TOKEN`, `FLY_ACCESS_TOKEN` from the gateway. The config dir points into the temp dir, and the recipe forces **file-only storage** (gh: `--insecure-storage`, to be verified per version), so the person's keychain and normal config are untouched. Read-back and identity check use the same isolated config and environment. If a program cannot be kept off the keychain, it gets no guided login (Fly until proven).
- *One authoritative attempt per (person, profile):* starting a new one cancels the old one. Each attempt has an id; publication compares the id under a lock, so a stale completion cannot publish.
- *States:* starting → waiting_for_person (URL + code) → verifying → connected | expired | cancelled | denied | failed | unsupported. The expiry is the vendor code's expiry, capped (e.g. 15 min).
- *Publication order:* read-back → identity check with the captured token → durable store write → masking registration → "Connected as <login>". A failure at any step keeps the previous credential and reports failed.
- *Cleanup:* the process group is killed on cancel, expiry or gateway shutdown. The temp dir is deleted on every terminal state, and leftovers are swept at gateway start.
- *Card content:* only a URL that matches the profile's allowed URL prefix (e.g. `https://github.com/login/device`) and the one-time code are shown. No raw output and no log view. If neither can be parsed, the attempt ends "unsupported, paste a token instead".
- *UI:* reuse the provider sign-in dialog lifecycle and design-system components (Dialog, Button, CopyButton, Switch, ConfirmDialog). Labelled fields, keyboard open/copy/cancel, focus return, announced state changes.
- *Agent entry:* a `request_program_login(profile)` tool raises the card in web chat only (same refusals as `request_secret` for channel and unattended turns). It is registered under the global tool-policy ceiling model with per-agent tightening only.

**D9: Import existing login (S3, only if Q3 = A).** A person-initiated button. The gateway runs the validated program's read-back (`gh auth token`, `fly auth token`) **outside** the sandbox with a composed environment, then runs the same identity check / store / masking sequence as D8. It never runs automatically. Errors are honest: keychain unavailable, login expired, program not found.

**D10: Interactive terminal deferred.** The dormant PTY fields stay unused until a program truly needs typed input.

### 6.3 Slices and what each proves

| Slice | Delivers | Visible result | Minimum acceptance evidence |
|---|---|---|---|
| **S0** (closes #1960) | D1 | `/usr/local/bin/gh --version` runs; the refusal text has no copy/disable advice | Absolute and plain spelling of the same chosen file; assignment prefix; wrappers; path-valued arguments and redirections still judged; workspace and mount programs; symlinked program; protected internal paths still refused; enforced and degraded sandbox. Version output proves launch only, not login. |
| **S1** | D2–D7, paste token | Paste a GitHub token once; agent runs `gh api user` and `git push` over HTTPS as the selected account; a printed token shows `[FILTERED]` | Real `gh api user`; private HTTPS git with an existing generic **and** host-specific helper present (no wrong account, no extra stored copy); `fly`/`flyctl`; custom profile; Ask/Auto/God; channel rule; conflicts; import arrives off; disconnect; sentinel assertions in transcript, gateway log, audit and export. Storing the token is not proof of account access. |
| **S2** | D8 | Connect GitHub → code + link → "Connected as <login>" | Stdin-closed login per supported version/platform; keychain and normal config unchanged; isolated read-back; real identity; denied / expired / cancelled / overlapping attempts; read-back or store failure keeps the old credential; cleanup and restart sweep; no secret in the card. Fake CLIs test the orchestration, not vendor compatibility. |
| **S3** (if Q3 = A) | D9 | One click imports the existing gh login | Person-initiated; validated program; correct account; no token in UI/logs; honest unavailable-keychain and expired errors. |
| Later | Option D or connectors; terminal only if needed | — | — |

Founder-question impact on slices: Q1 changes D3's key and whether S1 adds an agent field. Q2 A keeps the order above; Q2 B merges S1's paste path into S2 (nothing usable until S2 lands); Q2 C adds an outward-facing GitHub app step before S2. Q3 B drops S3. Q4 changes only the channel filter in D5/D6.

CONFIDENCE: High: D1 (narrow exec-path fix)
  Basis         : ADR-062 opened execute; the guard is advisory text scanning; the exemptions and protected checks stay
  Evidence      : guard-probe.log (exit 0); code read of checkPathSegment / pathUseClassifier
  Missing       : how many existing guard tests assert the exec refusal; wrapper-resolution edge cases
  Would improve : the S0 test table above, red-then-green

CONFIDENCE: Medium-High: Option B overall
  Basis         : every building block exists; new parts are the catalog, network hookup in Ask, the always-on replacer and the attempt runner
  Evidence      : code read (C5–C15); the review confirms the direction
  Missing       : live proof of steps ③/④ on the Mac; vendor host lists
  Would improve : G3/G4 runs before S1

CONFIDENCE: High: D4 (reset helper chain)
  Basis         : documented git behaviour; offline receipt
  Evidence      : git-helper-probe.log (4 cases, exit 0); gitcredentials docs
  Missing       : Windows quoting of the fixed helper; git < 2.31 share
  Would improve : S1 Windows row

CONFIDENCE: Medium: D8 (guided login)
  Basis         : gh documents file storage and web login; isolation designed explicitly
  Evidence      : gh manual and upstream source (via review); provider sign-in lifecycle in code
  Missing       : G2 spike results; any Fly recipe
  Would improve : spike per version/platform; Fly stays paste-only until proven

CONFIDENCE: Medium: D6 (network per mode)
  Basis         : reuses the existing token/host grant; the Ask extension is new code
  Evidence      : shell_permission_mode.go Auto-only path; preflight classifier
  Missing       : measured host lists
  Would improve : proxy audit rows from the G3 spike

CONFIDENCE: High: D7 (secrets sink contract)
  Basis         : contract narrowed to sinks Omnipus controls; limits stated
  Evidence      : config/security/audit code read
  Missing       : nothing blocking; sentinel tests in spec
  Would improve : sentinel tests green

## 7. Risks and Caveats

| Risk | Impact | Mitigation |
|---|---|---|
| S0 is mistaken for "GitHub works now" | Expectations | S0 closes #1960 only; S1 is tracked as its own feature |
| Wrong account through existing helpers | Pushes as the wrong person | D4 reset; S1 test with existing helpers |
| Guided login alters the person's keychain | Their normal login changes | D8 file-only isolated recipe; keychain-unchanged acceptance row; no recipe → no guided login |
| Stale attempt overwrites the account | Wrong account | D8 attempt id under lock |
| Tricked agent misuses an enabled account | Account-level harm | Per-agent switch (Q1 A/B), channel rule (Q4), advise least-scope tokens; ADR-092 approvals still apply |
| Masking gaps (transformed values, agent-written files) | Partial leak | Accepted and documented |
| Vendor changes flags, output or hosts | Login unsupported; 403 | Profile data update; paste-token fallback; proxy audit names the denied host |
| One-way doors | None major; all additive. S0 is a guard relaxation that can be reverted | — |

## 8. Confidence Assessment

| Decision | Confidence |
|---|---|
| D1 S0 exec-path fix | High |
| Option B direction | Medium-High |
| D3 scope | Depends on founder Q1; design covers all three |
| D4 reset helper chain | High |
| D5 precedence / import | High (refuse-visibly rule) |
| D6 network per mode | Medium |
| D7 sink contract | High |
| D8 guided login | Medium (recipe spike) |
| D9 import existing login | Medium (founder Q3; keychain prompts) |
| D10 defer terminal | Medium (reversible) |

## 9. Validation / Next Steps

### Testing strategy (carried into the spec)
| Level | What |
|---|---|
| Unit | S0 table (§6.3); env composition (profile vars, reserved names, conflict refusal, run-time fail-closed); D4 helper with existing helpers; D6 per-mode host attachment; D7 always-on replacer with the general switch off and short-content settings |
| Integration | D8 orchestration with fake CLIs: success, denied, expired, cancelled, overlapping, read-back failure, store failure, crash and restart sweep; sentinel secrets asserted absent from transcript, gateway log, audit and export |
| Browser (targeted Playwright) | Settings → Programs connect / paste / disconnect / conflict message; sign-in card states; keyboard and focus |
| Hands-on at the cut (touches the agent engine) | Real `gh`, `git`, `fly` on macOS and Linux; Windows for S1 paste-token and git helper. Per-platform supported/unsupported rows, recorded with logs |
| Post-landing | Omnipus Test Lab inside the live instance: a useful extra check, not a substitute for the hands-on rows |

### Documentation (review MAJ-008): affected pages, drafted by the implementing lead in the same change and checked by docs-verifier
| Page | Update |
|---|---|
| `/workspace/omnipus/docs/security.md` | Program-path behaviour (S0); program credentials and their sink contract; masking limits (always-on for program values, general filter still switchable); God Mode env exposure; disconnect is local, revoke upstream |
| `/workspace/omnipus/docs/tools.md` | Running programs by name vs full path; programs and network hosts per mode; `request_program_login` |
| `/workspace/omnipus/docs/settings.md` and `/workspace/omnipus/docs/agents.md` | Settings → Programs; per-agent switch; channel rule; import behaviour |

### Before S1/S2
1. **Spike (≤ 1 h per program):** run `gh auth login --web --hostname github.com --git-protocol https --insecure-storage` with an isolated config dir and stdin closed on macOS and Linux. Record output, exit code, the read-back from the isolated config, keychain unchanged, and hosts. Same for `fly auth login`. No recipe → paste-only. (G2, G3)
2. **Live repro** of steps ③/④ with plain `gh` on the Mac instance (G4).

### Handoff
- The review procedure allows one grill and one correction round. This is that correction. Next comes the founder interview (Q1–Q4), then `/plan-spec /workspace/logs/1960/adr-cli-tools-credentials.md`. S0 can be specified and built first, without waiting for the answers.

### Open founder questions (unchanged; not answered yet)

**Q1: Who gets the GitHub login?** Today a secret belongs to one agent in one workspace.
- **A (recommended)** one login per person, switched on per agent. *Impact:* one setup; adds a per-agent switch.
- **B** keep it per agent and workspace. *Impact:* no new switch; set up again for every agent and workspace.
- **C** one login that every agent gets automatically. *Impact:* simplest; every agent, helpers included, can act on the account.

**Q2: How should the person connect first?**
- **A (recommended)** paste a token in S1, sign-in card in S2. *Impact:* usable soonest; sign-in only where a recipe is proven.
- **B** wait and ship only the sign-in card. *Impact:* nothing usable until S2; Fly may stay unsupported.
- **C** register an Elicify GitHub app for "Sign in with GitHub" now. *Impact:* best GitHub experience; outward-facing setup and upkeep; GitHub only.

**Q3: May Omnipus offer "use my existing login on this computer"?**
- **A (recommended)** yes, as an explicit button. *Impact:* one click for you on the Mac; macOS may show a keychain prompt; adds S3.
- **B** no. *Impact:* always sign in or paste fresh; S3 dropped.

**Q4: Do Telegram/Discord-triggered chats get connected programs?**
- **A (recommended)** off by default, switchable per program. *Impact:* matches today's private variables; one extra switch.
- **B** always on. *Impact:* channel messages (lower trust) can drive account actions.
- **C** never. *Impact:* simplest and safest; channel-driven git/gh work impossible.

## Review response

| Finding | Response | Where |
|---|---|---|
| MAJ-001 git helper chain | Accepted: empty reset entry then a get-only fixed helper; git names reserved; test with existing helpers | D4, D5, S1 row |
| MAJ-002 keychain isolation | Accepted: isolated config, file-only storage, environment composed from scratch, keychain-unchanged row; no recipe → no guided login | D8, S2 row |
| MAJ-003 masking overstated | Accepted: sink-by-sink contract; always-on replacer for program values; min length 8; audit value-free; retired values kept; export promise narrowed; `[FILTERED]` | C7, D7 |
| MAJ-004 network hookup | Accepted: profiles feed the classifier before the decision; Ask/Auto/God/no-sandbox table; aliases, absolute heads, wrappers; lifetime and disable semantics | D6 |
| MAJ-005 S0 scope | Accepted: dropped the writable-root scan for ordinary bash; assignment/wrapper-aware program word; protected checks and exemptions unchanged; diagnostics limited to attributable layers; S0 ships alone; validator moved to the gateway-owned login | D1, D8, S0 row |
| MAJ-006 login lifecycle | Accepted: one attempt with id, expiry, cancel, terminal states, stale-publish guard, publication order, cleanup and restart sweep, quarantined output | D8 |
| MAJ-007 precedence / import | Accepted: refuse conflicts visibly, fail closed at run time, reserved names, imported switches arrive off | D5 |
| MAJ-008 documentation | Accepted: affected pages and updates named | §9 Documentation |
| MIN-001 labels / diagram | Accepted: diagram branched by spelling; C2, C4, C5, C8, C12, C14 corrected; ADRs cited by title; correction callouts | §1 |
| MIN-002 delivery evidence | Accepted: per-slice proof, platform rows, hands-on at the cut, agent-tool parity (FR-8), tool registration under the global ceiling; contract-first noted | §2, §6.3, §9 |
| FACT audit: unsaved run receipt | Accepted: guard probe re-run and saved | `/workspace/logs/1960/guard-probe.log` |
| Founder questions | Kept Q1–Q4 as instructed (the review merged Q1/Q4); per-option impact added | §9 |
