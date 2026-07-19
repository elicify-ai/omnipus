# Settings → Security: analysis & UX recommendations

_Note for reading outside the TUI. Written 2026-07-13 from codebase walk of Settings → Security, sandbox, tool policy, god mode._

---

## 1. How security actually works (layered model)

Omnipus uses **several independent fences**, not one “security switch.” An agent action has to pass the ones that apply.

```
User message → Agent turn → wants tool X
        │
        ▼
┌───────────────────────────────────────┐
│ 0. God mode (Gateway tab)             │  If ON: floors tools to allow,
│    global bypass (restart to arm)     │  kills kernel sandbox, opens egress,
└───────────────────────────────────────┘  kills shell-guard. Audit/prompt-guard/
        │                                  rate limits stay on.
        ▼
┌───────────────────────────────────────┐
│ 1. Tool policy (allow / ask / deny)   │  Global + per-agent. Global Deny wins.
│    Explicit per tool — no default     │  "ask" → human approval modal.
│    fallback (Constraint #6)           │
└───────────────────────────────────────┘
        │  (if shell / bash)
        ▼
┌───────────────────────────────────────┐
│ 2. Shell-specific gates               │  exec approval, binary allowlist,
│                                       │  deny-pattern regexes, timeouts
└───────────────────────────────────────┘
        │
        ▼
┌───────────────────────────────────────┐
│ 3. Process sandbox (kernel / app)     │  Landlock FS + seccomp (Linux),
│    mode: enforce | permissive | off   │  Job Objects (Windows), or weak
│                                       │  env-var fallback
└───────────────────────────────────────┘
        │  (if network / HTTP)
        ▼
┌───────────────────────────────────────┐
│ 4. Network / SSRF                     │  Private IP / metadata block,
│                                       │  optional allow_internal,
│                                       │  port allow-list (Landlock ABI4+)
└───────────────────────────────────────┘
        │  (tool results back into LLM)
        ▼
┌───────────────────────────────────────┐
│ 5. Prompt guard                       │  Sanitize untrusted tool output
│ 6. Skill trust                        │  Hash-verify installed skills
│ 7. Cost / rate limits                 │  Daily $ + call rate caps
│ 8. Audit log                          │  What happened (HMAC chain)
│ 9. Credential vault                   │  Keys encrypted at rest
└───────────────────────────────────────┘
```

### What each fence means

| Layer | What it answers | Typical default |
|--------|------------------|-----------------|
| **Tool policy** | May this agent use *this* tool? | Explicit map; seeded per agent; global can force Deny |
| **Ask approval** | Does a human confirm risky tools? | When policy = `ask` |
| **Shell gates** | Which binaries/commands, how long? | Often ask + optional patterns |
| **Sandbox mode** | If the process misbehaves, does the **kernel** stop it? | `enforce` on capable Linux |
| **SSRF / egress** | Can tools hit my LAN / cloud metadata? | Block private ranges |
| **God mode** | Turn most of the above off for power users | Off; password + often restart |
| **Vault / audit / cost** | Secrets safe? Traceable? Budget-bound? | Vault on; audit on; cap configurable |

**Sandbox ≠ tool policy.**

- **Tool policy** = permission in the product.
- **Sandbox** = containment if the agent (or a binary it runs) tries to leave the box.

You can Allow `bash` and still have Landlock block `/etc/shadow`. You can Deny `bash` and never reach the sandbox for shell.

**God mode** collapses layers 1–4 (not audit / prompt-guard / rate limits). It lives under **Settings → Gateway**, easy to miss from Security.

---

## 2. What the Security tab contains today

### Primary (always visible)

1. **Security health** (`DiagnosticsSection` / doctor score)
2. **Protection settings**
   - Agent tool access → `policy_mode` allow/deny (“Must ask first” / “Run freely”)
   - Shell command approval → `exec_approval` auto/ask/deny
   - Daily spending limit
   - Skill trust
3. **Credential vault** (after Advanced)

### Advanced (collapsed)

- Global tool policy grid (`allow` / `ask` / `deny` per tool)
- Command execution timeouts + “enable deny patterns”
- Binary allowlist
- SSRF proxy status card
- Prompt injection level
- **Process sandbox** (status, mode, allowed paths, SSRF allow_internal, global shell deny regexes)
- Per-agent rate limits
- Audit log viewer

### Elsewhere (same story, different door)

| Control | Where it lives | Why that hurts |
|---------|----------------|----------------|
| **God mode** | Gateway | Biggest security lever not on Security |
| **Per-agent tools** | Agents → Tools | Global grid + agent grid; “which wins?” unclear |
| **Workspace team / delegation** | Workspaces | Trust to *other agents*, not host FS |
| **Browser evaluate** | Config/sandbox field | High risk, easy to not surface |

---

## 3. Why it feels confusing

### A. Three different “can agents run things?” controls

| UI label | Values | Real role |
|----------|--------|-----------|
| **Agent tool access** | Must ask / Run freely | Coarse global posture (`policy_mode`) |
| **Shell command approval** | Auto / Ask / Always deny | Shell-only |
| **Tool Access — Global Policies** (Advanced) | allow / ask / deny **per tool** | Actual catalog enforcement |

Users who set “Must ask first” then open the Advanced grid see every tool as allow/ask/deny and don’t know which is law.

### B. Sandbox is buried and jargon-heavy

Under Advanced: Landlock ABI, seccomp, blocked syscalls, enforce/permissive/off, allowed paths, SSRF presets **and** a separate “SSRF Proxy” card.

**Permissive** is easy to misread: looks “on” but **does not block**—only logs.

### C. God mode is the nuclear option, off-tab

God mode disables kernel sandbox, floors tool policies to allow, opens egress, kills shell-guard—while Security may still show “Must ask first” until doctor re-runs. Split surface = false confidence.

### D. Restart semantics are uneven

Sandbox mode / paths / some grants need **gateway restart**. Tool policies and vault often apply more live. “Saved” without live effect is common.

### E. Security health ≠ live posture map

Doctor score doesn’t answer: “What can Mia do now?”, “Is the kernel fence on?”, “Is god mode active?”

### F. Settings tab sprawl

~9 Settings tabs; Security itself is a long vertical stack + Advanced. Cognitive load compounds.

### G. Legacy / parallel names

exec / bash history, `policy_mode` vs per-tool policies, SSRF “proxy” vs `allow_internal` vs egress CIDRs.

---

## 4. How to make Settings more user-friendly

Guiding idea: **outcomes first, mechanisms second, raw config last.**

### 1 — One “Protection level” preset (primary)

| Preset | Tool feel | Shell | Sandbox | Notes |
|--------|-----------|-------|---------|--------|
| **Safe (recommended)** | Prefer ask/deny on powerful tools | Ask | Enforce | Fresh-install spirit |
| **Balanced** | Core allow; bash/browser ask | Ask | Enforce | Daily driver |
| **Power** | More allow | Auto for allowlisted binaries | Enforce | Still contained |
| **Unrestricted** | God-mode path | Off | Off | Password + restart + red banner |

Under the preset: **“Customize…”** opens current Advanced controls.

### 2 — Security home as a posture dashboard

Always visible:

```
Your setup is: BALANCED                    [Change]
● Agents need approval for shell & browser
● Kernel sandbox: ON (Linux Landlock)
● Private network access: blocked
● God mode: OFF
● Vault: locked · N keys
Score 86 — 2 improvements   [Review] [Check now]
```

Each line links to the control that owns it.

### 3 — Rename Advanced into 4 cards

1. **What agents may do** — global tool grid + “Global Deny wins; per-agent in Agents → Tools”
2. **Shell & commands** — approval, timeouts, binary allowlist, deny patterns (one place)
3. **Containment (sandbox & network)** — plain mode names:
   - **Block violations (recommended)** = enforce
   - **Log only (not protected)** = permissive
   - **No isolation (dev only)** = off  
   Then folders outside workspace; private network presets
4. **Oversight** — audit, rates, prompt guard, skill trust

Hide Landlock ABI / syscall lists under **“Technical details for support”**.

### 4 — Move God mode into Security (or dual-surface)

Nuclear switch next to the fences it disables. Gateway can keep a short “Runtime overrides” note.

### 5 — “Effective permissions” preview

> For **Jim**: bash = ask (global), browser_navigate = allow, kernel sandbox = enforce, god mode = off.

### 6 — Restart UX as a product rule

Badge **“Applies after restart”** on the control; primary CTA **Restart gateway**; don’t treat “Saved” alone as success if not live.

### 7 — Slim Settings chrome

Fewer top-level tabs; merge Performance under Gateway Advanced; Chat prefs under Profile; optional Data+Memory merge.

### 8 — Copy principles

| Avoid | Prefer |
|-------|--------|
| Landlock / seccomp / SSRF / ABI | Kernel isolation / private network block |
| policy_mode / tool_policies | Ask before tools / per-tool rules |
| Permissive | Log only — not protected |
| God mode (alone) | Disable all safety limits (god mode) |
| enable_deny_patterns | Block dangerous shell patterns |

### 9 — Keep vault & spend primary; audit one click away

Vault + daily spend stay top-level (good today). Audit is “What did agents do?”—arguably primary, not Advanced-only.

---

## 5. Suggested target IA

```
Security & privacy
├── Posture summary + preset [Safe | Balanced | Power | Unrestricted]
├── Spending limit (progress)
├── Credential vault
├── Skill sources trust
├── [If god mode on] Full-width danger banner → disable
│
└── Customize protection ▾
    ├── What agents may do (tool grid)
    ├── Shell & commands
    ├── Containment (sandbox + network)
    └── Oversight (audit, rates, prompt guard)
        └── Technical details (ABI, syscalls, doctor raw)
```

---

## 6. Bottom line

**Mechanisms:** solid multi-layer design (tool policy, human ask, shell filters, kernel sandbox, SSRF, prompt guard, skills, cost, audit, vault) with god mode as deliberate global override.

**UX confusion:** layers exposed as peer controls with overlapping names; god mode off-tab; sandbox/SSRF jargon-first; live vs restart unclear.

**Highest ROI (in order):**

1. Protection **presets**
2. **Posture dashboard** at top of Security
3. Rename sandbox modes + bury Landlock details
4. Surface **god mode** on Security
5. **Effective permissions** for one agent
6. Restart-required as first-class UI state

---

## Key source files

- `src/components/screens/SettingsScreen.tsx`
- `src/components/settings/SecuritySection.tsx`
- `src/components/settings/SandboxSection.tsx`
- `src/components/settings/DiagnosticsSection.tsx`
- `src/components/settings/GodModeControl.tsx` (Gateway)
- `pkg/config/sandbox.go`
- `docs/operations/sandbox-config.md`
- `docs/operations/security-considerations.md`
