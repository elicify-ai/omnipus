# Sandbox Redesign — Findings, Solutions, Open Questions

Status: design draft, pre-implementation
Date: 2026-05-03
Trigger: Jim's penetration test (see `agents/jim/PENTEST_REPORT.md` on the public deployment)

This document captures (a) what the pentest found, (b) what the root causes are, (c) the menu of solutions discussed, and (d) the proposed file-layout redesign that surfaced from the conversation. It is the reference point we go back to when we start implementing — keep it in sync as we make decisions.

---

## 1. What Jim found

Jim is a regular agent. With nothing more than the tools he was already given, he proved:

| # | Finding | Severity | Confirmed against code |
|---|---|---|---|
| C1 | Could read every other agent's directory, the whole platform config, the credentials store, and the master encryption key | Critical | `pkg/sandbox/sandbox.go:165-172` — Landlock allows full RWX on the entire `$OMNIPUS_HOME` |
| C2 | Master key + encrypted credentials sit inside agent-reachable filesystem | Critical | `master.key` lives at `$OMNIPUS_HOME/master.key` |
| C3 | 22 plaintext secrets leaked through `process.env` (GitHub, Cloudflare, Fly.io, OpenRouter, Ghost, Minimax, Resend, Stalwart, Tavily, Google Cloud creds, …) | Critical | `pkg/sandbox/hardened_exec.go:42-46` — env scrubber is a 3-entry deny-list, allow-by-default |
| C4 | Shell guard bypassed by writing exploit code to a file via `write_file`, then running `node script.js` (only the command string `node script.js` is analysed) | Critical | The guard is path-string analysis; file contents are never inspected |
| H1 | Egress proxy blocks HTTP to internal addresses but raw TCP `net.connect()` to `169.254.169.254`, `10.x.x.x`, `localhost:22` works | High | `EgressProxyAddr` is HTTP/HTTPS only, documented limitation |
| H2 | No real container — runs as host UID 1000, sees real hostname, host IPs, SSH info, tmux session, the user is in the `sudo` group | High | No UID drop, no UTS/PID/mount namespace |
| H3 | GCC blocked but Node.js/Python/Perl/netcat fully usable | High | Filesystem `/usr/bin` is in the read-only allow-list to keep tools working |
| H4 | `/proc/self/maps` readable — exposes ASLR base, library layout | High | `/proc` is in the read-only system path list (Chromium needs it) |

The findings are accurate, not exaggerated.

---

## 2. Root causes (the nine findings collapse into four)

**RC-1 — The kernel sandbox is process-global, not per-agent.**
Landlock is applied once at gateway boot. Every spawned child inherits the same allow-list, which covers the whole `$OMNIPUS_HOME`. Landlock has no concept of "agent A's child" vs "agent B's child" so they see each other's files. C1, C2, C4 (the kernel layer doesn't help the tool guard) all trace to this.

**RC-2 — Environment scrubbing is inverted.**
`sensitiveEnvKeys` denies three named variables; everything else passes through to children. A secure default is the opposite — drop everything by default, let through a small allow-list. C3 is purely this.

**RC-3 — Secrets live inside the agent-reachable filesystem.**
`master.key` and `credentials.json` are siblings of `agents/` under `$OMNIPUS_HOME`. Any rule that lets an agent into its workspace also lets it into the secrets dir. C2 is this.

**RC-4 — Tool-layer guards are not the kernel boundary.**
Path-string analysis is fine as a safety net; it can't be the real boundary because file-based execution always bypasses it. Once RC-1 is fixed properly, the kernel says no regardless of what the tool guard saw, and C4 is moot.

H1, H2, H4 are defense-in-depth gaps that survive even with RC-1..RC-4 fixed; addressed at the bottom of section 3.

---

## 3. Solution menu (what we discussed)

### Tier A — quick wins, all three OSes, days of work

| # | Fix | Linux | macOS | Windows |
|---|---|---|---|---|
| A1 | Replace env scrubber deny-list with a strict allow-list (PATH, HOME, USER, LANG, LC_*, TZ, TMPDIR, OMNIPUS_*, AGENT_*). Operator can extend via `cfg.Sandbox.ChildEnv`. | ✓ | ✓ | ✓ |
| A2 | Auto-generated master.key is written to `$HOME/.omnipus-keys/<install-id>.key` (outside `$OMNIPUS_HOME`). Existing installs get migrated on boot with a notice. | ✓ | ✓ | ✓ |
| A3 | Stop covering `$OMNIPUS_HOME` with a single recursive RW rule. Replace with sub-rules per category (see section 4). Carve `credentials.json`, `config.json`, `master.key` out of every RW rule entirely. | ✓ | partial | partial |
| A4 | Hide `/proc/self/{maps,mem,syscall,environ}` from the Landlock `/proc` read rule | ✓ | n/a | n/a |

A1 + A2 + A3 alone close C1, C2, C3, C4. A1 is the highest-ROI single change.

### Tier B — per-agent kernel scope

| # | Fix | Linux | macOS | Windows |
|---|---|---|---|---|
| B1 | Pass `agentID` into `hardened_exec.Run`. Build a narrowed policy: RW on the agent's private workspace + assigned project workspaces, R-only system paths, deny everything else. Apply via `RestrictCurrentThread` so children inherit. | ✓ | n/a | n/a |
| B2 | Per-agent macOS `sandbox-exec` profile generated at spawn time | n/a | ✓ | n/a |
| B3 | Per-agent Windows AppContainer SID + DACL with explicit deny on sibling agent dirs | n/a | n/a | ✓ |
| B4 | Per-agent UID via setuid helper (cleanest cross-OS but adds operator friction) | ✓ | ✓ | ✓ |

The pure-Go path is **B1 on Linux + B2 on macOS + B3 on Windows**. The shared interface is `SandboxBackend.ApplyForAgent(agentID, perAgentPolicy)`. B4 is the future-proof answer; skip for v1.

### Tier C — kernel-layer network filtering (Linux)

The egress proxy is HTTP/HTTPS only by design (see `hardened_exec.go:96`). To block raw TCP to internal CIDRs:

| # | Approach | Verdict |
|---|---|---|
| C1 | nftables/iptables rules emitted at boot, scoped to a cgroup all agent children join | Strong, but not pure Go |
| C2 | Per-agent network namespace (`CLONE_NEWNET`) with veth + nft routing | Very strong, very heavy, requires CAP_NET_ADMIN |
| C3 | eBPF cgroup egress filter (`BPF_PROG_TYPE_CGROUP_SOCK_ADDR`) | Best technical answer, Cilium-level complexity |
| C4 | Accept proxy-only as documented limitation; rely on RC-1..RC-3 to remove the secrets the agent could exfiltrate | Zero new code, doesn't fix H1 |

Lean: **C4 today, C1 as opt-in `sandbox.experimental.kernel_egress_filter=true`** for paranoid operators.

### Tier D — out of scope

`runsc`/gVisor, bubblewrap, nsjail, Docker/podman. All violate "single Go binary, no runtime deps" (CLAUDE.md hard constraint #1). Could land later as opt-in "Hardened install".

---

## 4. File-layout redesign (the conversation that followed)

The current `$OMNIPUS_HOME` mixes everything in one tree:

```
$OMNIPUS_HOME/
├── config.json           gateway-only, no secrets
├── credentials.json      gateway-only secrets
├── master.key            gateway-only secret
├── agents/<id>/          per-agent — but Landlock can't tell them apart
│   ├── memory/
│   ├── sessions/
│   └── (working files mixed in here)
├── sessions/<sid>/       per-session transcripts
├── workspace/            stale older layout, currently empty
├── media/  browser/  channels/  pins/  projects/  skills/  system/  tasks/  backups/  logs/
```

Three separate problems are fused together:

1. **Memory** (the agent's long-term notes — like a journal) is mixed with
2. **Per-agent session bookkeeping** (mostly empty, never user-visible), and
3. **Working files / project artefacts** (Jim's `hello-world/`) all under `agents/<id>/`.

That's why Ava has empty `memory/` and `sessions/` folders — boot-time `MkdirAll` creates them for every agent, even those that never write anything.

### The two-room model

Decision (pending confirmation): every agent gets the **same uniform shape**, plus shared project rooms:

```
$OMNIPUS_HOME/                              gateway only (sandbox children NEVER reach this dir)
├── config.json
├── credentials.json
├── master.key            (or moved to $HOME/.omnipus-keys/ per A2)
├── system/  backups/  logs/

$OMNIPUS_DATA/                              shared, agent-readable area
├── media/                                  refs only; gateway gates access
├── sessions/<sid>/<YYYY-MM-DD>.jsonl       global by session id
├── skills/                                 read-only

$OMNIPUS_AGENTS/                            per-agent isolated subtrees
├── jim/
│   ├── workspace/                          PRIVATE — only Jim's children get RW
│   └── memory/                             PRIVATE — Jim's long-term notes
├── ava/    mia/    ray/    max/            same shape
└── <uuid>/                                 custom agents — same shape

$OMNIPUS_PROJECTS/                          shared rooms — multiple agents collaborate
├── elicify-website/
│   ├── .omnipus-project.json               { name, members: [jim, ava], owner, created }
│   └── (project files agents work on together)
└── hello-world/
    └── ...
```

Three roots vs one is open (see section 6 Q1).

### How collaboration works

A session is bound to **at most one project** for its lifetime. When the agent's child spawns inside a session:

- Their kernel rules give them: **their own private workspace** + **the bound project's folder** + read-only system paths
- Cwd is the project folder if a project is bound, otherwise the private workspace
- Two agents in the same project = both see the same files, neither can touch the other's private room
- Memory stays private to each agent (their journal, never shared)

### Membership

A project has a roster JSON (`projects/<id>/.omnipus-project.json`). Adding an agent to the roster = next time that agent spawns in a session bound to this project, the kernel rules let them in. Removing them = next spawn, denied.

### Concurrency on shared files

Default: trust the agents to coordinate via chat ("you take the CSS, I'll do the API"). If real collisions become a problem, add a soft-lock tool (`project.claim_file(path)`) later. Don't ship git-style branches — too heavy for the use case.

---

## 5. Order of work (proposed)

When we start cutting code:

1. **A1** — env allow-list (highest ROI single change, closes C3)
2. **A2** — relocate auto-generated master.key (closes C2)
3. **Layout split** — physically separate per-agent / projects / secrets / data on disk (prerequisite for A3 and B1)
4. **A3 + B1** — narrow Landlock per agent, with the new layout the rules become trivial
5. **A4** — hide `/proc/self/{maps,mem,syscall,environ}`
6. **C4 acknowledged + C1 as opt-in** — document the raw-TCP egress limitation, ship nft-based filtering as opt-in
7. **B2 / B3** — macOS sandbox-exec profile + Windows AppContainer/DACL backends (later wave)

A1 + A2 + layout split + A3 + B1 takes us from "Jim broke out in 30 seconds" to "Jim's children can't even see master.key, can't read other agents' files, can't read sibling projects they're not in."

---

## 6. Open questions still to settle

These need answers before implementation. Pre-coding decisions only.

1. **One root or three?**
   Keep `$OMNIPUS_HOME` and carve subtrees with rules — less disruptive for existing installs.
   Or split into `$OMNIPUS_HOME` (secrets), `$OMNIPUS_DATA` (shared), `$OMNIPUS_AGENTS` (private), `$OMNIPUS_PROJECTS` (shared rooms) — cleaner kernel rules, more env vars.

2. **Project creation flow.**
   - (a) Explicit user action only ("create new project"), or
   - (b) Ava can create one during onboarding, or
   - (c) Auto-create on first agent write to a path with `project=`.
   Lean: (a) primary + (b) Ava-driven during interview. (c) is tempting but easy to abuse.

3. **Session ↔ project binding.**
   One project per session for its lifetime — switching projects = new session?
   Or session can hop between projects on a `project.switch` tool call?
   Lean: one per session. Dramatically simpler kernel-rule lifecycle.

4. **Memory placement.**
   Per-agent only (each agent's journal is private)?
   Or also a shared `projects/<id>/notes/` for cross-agent project notes?
   Lean: per-agent for v1, add project notes only if asked.

5. **Default when no project is bound.**
   Agent works in their private workspace?
   Or in a "default shared" workspace?
   Lean: private workspace — safer default; agents can always invoke `system.project.create` to share.

6. **Cross-project copy.**
   If Jim is a member of project A and project B, can he copy files between them?
   Lean: yes — he's authorized in both, the kernel can grant both during the session if both are bound. (But sessions are 1:1 with projects per Q3; so this becomes "no in v1, agent must move via private workspace as a temp staging area"). Worth re-litigating.

7. **Existing-install migration.**
   Move files at boot with a notice, hard-fail with remediation message, or run both layouts in parallel for a release?
   Lean: migrate at boot + log a one-time WARN. Hard-fail is alarming for users; parallel is technical debt.

8. **Operator opt-out.**
   Do we keep a "sandbox=off" mode that turns this all off (legacy behavior)?
   Lean: yes — the existing `sandbox.mode=off` already does this. New layout still applies on disk, but kernel rules don't.

---

## 7. What we already shipped this session (context)

The pre-pentest work on `feature/iframe-preview-tier13` already did:

- Per-thread Landlock re-application via `RestrictCurrentThread` + `StartLocked` (closes the Go M:N scheduler bypass)
- Kernel-enforced bind-port allow-list (Landlock `NET_BIND_TCP` ABI v4)
- Sandbox-aware `exec` (routes through `hardened_exec.Run` when sandbox is on)
- Removed `Sandbox.Enabled` legacy bool; everything uses `ResolvedMode()`
- Unified `web_serve` tool replacing `serve_workspace` + `run_in_workspace`

The pentest tells us those steps were necessary but not sufficient. The bind-port allow-list works (Jim couldn't bind 5173). The thread-restrict works (children inherit). What's missing is the per-agent narrowing on top of the kernel boundary that already enforces.

---

## References

- Pentest report: `agents/jim/PENTEST_REPORT.md` on the deployment, summary in this conversation
- Current sandbox policy: `pkg/sandbox/sandbox.go::DefaultPolicy`
- Env scrubber: `pkg/sandbox/hardened_exec.go::scrubGatewayEnv`
- Per-thread Landlock: `pkg/sandbox/sandbox_linux.go::RestrictCurrentThread`
- Boot contract (master.key location options): `docs/internal/architecture/ADR-004-credential-boot-contract.md`
- Per-agent sandbox boundary (existing ADR): `docs/internal/architecture/ADR-009-per-agent-sandbox-as-security-boundary.md`
