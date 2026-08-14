# ADR-064 — Remove the "main" sentinel agent

- **Status:** Accepted (2026-08-14) — operator decision, taken after a code review established what the sentinel still did.
- **Date:** 2026-08-14
- **Deciders:** founder (decided removal, and that there is no back-compat); lead (mechanism)
- **Related:** [ADR-054 D6.4](ADR-054-entities-separate-from-config.md) (made `config.Agents.Defaults.DefaultAgentID` the single source for default-agent resolution); [ADR-037](ADR-037-remove-delegation-graph.md) (the precedent this follows — a control that looked functional and had no effect); `pkg/config/legacy_agents_list.go` (the precedent for an agent-identity cutover with no migration).
- **Evidence level:** claims marked **[VERIFIED]** were read from code or executed on this host at commit `f52f1988` (pre-removal) and `36bc3689` (post-removal). Nothing here is inferred from documentation alone — the review that produced it corrected its own first pass twice.
- **Implemented by:** commit `36bc3689` on `feat/remove-main-sentinel-agent`.

---

## 1. Decision

The `"main"` agent is **removed entirely**, with **no backward compatibility and no data migration**.

Where a default agent is needed, the only answer is the seeded default agent
(`config.Agents.Defaults.DefaultAgentID`, seeded to Mia on fresh install). Never a
hardcoded name.

## 2. Context — what it actually was

`"main"` was a **shadow entity**: an identity that existed in the runtime and in persisted
user data while having no schema anywhere. **[VERIFIED]**

- Not in `contracts/`, not in `cfg.Agents.List`, not in the entity store.
- `GET /api/v1/agents/main` returned 404 — while `POST /api/v1/sessions` accepted it as an owner.
- Invisible in every UI. No user could chat with it, and it appeared in no agent list.
- `pkg/config/validate.go::ValidateToolPolicyCoverage` had **never** validated it, because that
  gate iterates `cfg.Agents.List` and the sentinel was never a member.

The 404-versus-accepted split was not a routing bug to patch. It was the visible symptom of an
entity nobody ever modelled.

Despite that, it was **not** dead. It was the live default agent on any install where nobody had
set one — which included every install upgraded from before the 2026-07-26 singleton seed. Both
default-agent ladders relied on it as a rung. **[VERIFIED]**

## 3. Why it had to go rather than be tidied

Three distinct harms, only the first of which is cosmetic.

**3.1 It could not be governed.** The per-agent tool-permissions screen is built from an agent's
execution registry. The sentinel was never in `cfg.Agents.List`, so the coverage gate never
checked it and no operator could see or change what it was allowed to do.

**3.2 It was a privilege-escalation entry point.** `NewAgentRegistry` registered it with **no
Tools and no Policies at all**. `pkg/tools/compositor.go`'s global×agent policy merge
(`resolveEffectivePolicyWith`) falls through to the GLOBAL floor for every tool an agent has no
per-agent entry for — which was *every* tool, for this agent — and `pkg/config/defaults.go`
seeds that floor permissively. Documented as a verified chain on 2026-07-26, not a theoretical
one. Removing the sentinel closes the entry point; the mechanism it exploited is unchanged and
still guarded by `populateAgentsListFromEntityStoreStrict`. **[VERIFIED]**

**3.3 It leaked into user data through a single silent substitution.**
`routing.NormalizeAgentID("")` returned `"main"`. That one line turned "nobody was named" into
"this specific agent" at 26 call sites, which is how the name reached session metadata,
per-line transcript owners, `session_lifecycle` records, cron job owners,
`Task.CreatedByAgentID`, `Plan.OwnerAgentID` — and, hardest of all, on-disk **filenames**
(`agent_main_session_<id>.jsonl`, via `pkg/memory/jsonl.go::sanitizeKey`). **[VERIFIED]**

## 4. What was removed

1. Both `DefaultAgentID` constants (`pkg/routing/agent_id.go`, `pkg/agent/registry.go`) and the
   runtime registration in `NewAgentRegistry`. The registry now contains only agents from
   `cfg.Agents.List` — every one of which the coverage gate validates.
2. The sentinel rung in **both** default-agent ladders. `AgentRegistry.GetDefaultAgent` may now
   return **nil**; `RouteResolver.resolveDefaultAgentID` returns **empty** with a WARN. Naming an
   agent that does not exist only moves the failure somewhere harder to read.
3. `NormalizeAgentID("")` returning `"main"` — it returns **empty**. The function sanitizes a
   string and cannot see config, so it has no honest default to offer. Callers must handle empty
   explicitly.
4. All **four** memory-tool gates — `remember`, `recall_memory`, `run_retrospective` (in
   `instance.go`) and `recall_conversation` (in `loop.go`, found only during the sweep). These
   were hardcoded identity checks, not capability checks. Whether an agent may remember is its
   tool policy.
5. The sentinel backstop in `attach_hydrate.go`.

## 5. What was deliberately KEPT

**The general session-owner fallback in `attach_hydrate.go`.** A transcript entry with no
`AgentID` of its own is attributed to the session's recorded owner. That is what lets a session
outlive the agent that created it, and the operator explicitly ruled it must stay.

What was removed is the **agent-of-last-resort behind it**. When neither the entry nor the
session names an owner, the entry is **skipped** — not hydrated under a blank owner, and not
turned into a hard failure of the session. Naming any specific agent there would hand one
agent's history to another.

`routing.DefaultMainKey` also stays: it is the session-key suffix in `agent:<id>:main`, an
unrelated concept that merely shares the word.

## 6. Consequences

**Accepted, by explicit operator decision:** installs with data owned by `"main"` are not
migrated. Sessions, tasks, plans, cron jobs and context files naming it will not resolve.

**Nil is now reachable.** `GetDefaultAgent()` could not previously return nil. Every call site
in `pkg/agent` and `pkg/gateway` was audited; all already nil-checked, apparently written
defensively in anticipation. **[VERIFIED]**

**An empty owner must never silently replace the sentinel.**
`pkg/gateway/websocket.go::handleChatMessage` now rejects a chat frame that names no agent when
no default resolves, returning an error frame rather than creating a session with an empty
owner. An empty owner is the same defect wearing a disguise, and harder to grep for.

## 7. What this does NOT fix

The two default-agent ladders **still disagree in their last resort** — `GetDefaultAgent` falls
back to the lexicographically-first non-worker, `resolveDefaultAgentID` to the first chat-target
in `cfg.Agents.List` slice order. They are only guaranteed to agree via the configured override.
That disagreement, not the sentinel, is the underlying defect; it caused a release blocker in
July 2026 and is unchanged here. Removing the sentinel removed a rung, not the divergence.

Recorded so the next person does not mistake this ADR for having closed it.

## 8. Verification

- Suite intact: **2036 → 2035** test functions; 4 removed, 7 added, no file deleted.
- `gofmt`, `go build`, `go vet` clean across the tree.
- Tests pinning the removal pass, including that an unattributable transcript entry reaches
  **no** agent — the property the whole change exists to protect.
