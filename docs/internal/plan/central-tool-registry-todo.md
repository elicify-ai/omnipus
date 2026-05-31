# Central Tool Registry — Working Notes

> **SUPERSEDED — Pre-spec scratch pad only.**
> This document was a working-notes capture used to seed the `/plan-spec` pass. The proper specification was produced and is the binding contract:
>
> - Spec: `docs/internal/specs/tool-registry-redesign-spec.md` (revision 6, fully implemented 2026-04-28)
> - Current as-is: `docs/internal/architecture/AS-IS-architecture.md` (§§3, 9)
>
> **Do not use this document for implementation guidance.**

**Original status note**: pre-spec scratch document. Captures the state of the planning conversation as of 2026-04-27 so the repo and context can be cleaned up before the proper `/plan-spec` pass starts. Not a spec; not implementation-ready.

---

## What we want to build

Replace the current tool architecture with a single central registry plus a per-agent allow/ask/deny policy filter applied before each LLM request.

### Locked-in design (confirmed by user)

1. **One central tool registry.** Single source of truth for the catalog. No hand-curated `builtinCatalog` slice that drifts from runtime. No `init()`-time `Register` pattern.
2. **Per-agent policy is a flat map**: `toolName → "allow" | "ask" | "deny"`.
   - No profiles, no roles, no inheritance.
   - Plus a single `default_policy` field per agent.
3. **Filter happens before the LLM call.** The `tools[]` array sent to the model is `{registry} ∩ {scope-allowed-for-this-agent} ∩ {policy ≠ deny}`. The model only sees tools the agent can actually use.
4. **No meta-discovery tool.** No `lookup_tool`, no `tool_search_tool`. Matches Anthropic / sst-opencode / OpenClaw state-of-the-art.
5. **"ask" surfaces a user approval prompt** via the existing tool-permission UI. The LLM still calls the tool; the loop pauses, the user approves or denies, the loop resumes (or returns a `permission_denied` result to the model).
6. **Tools are stateless.** Per-call dependencies flow via `context.Value` (already partially in place via `pkg/tools/base.go`). Construction-time deps that need hot-reload move to a shared atomic pointer.

### Bugs the redesign must fix (claimed by prior session — NOT re-verified this session)

- Hot-reload bug: Tier 1/3 workspace tools (`serve_workspace`, `run_in_workspace`) silently disappear after a config PUT because `ReloadProviderAndConfig` recreates the per-agent registry but only re-runs `registerSharedTools` and `WireAvaAgentTools`, not `WireTier13Deps`. Patched in this session via stored `tier13Deps` pointer; root cause (per-agent re-wiring) is what the redesign eliminates.
- Catalog drift: `builtinCatalog` in `pkg/tools/catalog.go:45` is hand-maintained; allegedly missing 5 entries (`serve_workspace`, `run_in_workspace`, `build_static`, `handoff`, `return_to_default`).
- Name mismatches: `bm25_search` / `regex_search` in catalog vs `tool_search_tool_bm25` / `tool_search_tool_regex` at runtime.
- Cataloged-but-never-instantiated: `remove_skill` is in the catalog but `NewRemoveSkillTool` is allegedly never called.

> **Verification needed.** These five claims came from a prior conversation summary. Lead must confirm each against current `main` + `feature/iframe-preview-tier13` before the spec quotes them as facts. Audit grep commands listed below.

---

## What's already in the codebase (verified this session, with file:line)

These are real and reusable — the redesign builds on them, not over them.

| Asset | Location | Why it matters |
|---|---|---|
| `Tool` interface | `pkg/tools/base.go:22-30` | Already minimal: `Name`, `Description`, `Parameters`, `Execute(ctx, args)`, `Scope`. Tools are mostly stateless already. |
| Request-scoped context | `pkg/tools/base.go:32-102` | Channel, chatID, agentID, sessionKey, transcriptSessionID flow via `context.Value`. The "stateless tool with call-time agent context" pattern exists. |
| `ToolRegistry` | `pkg/tools/registry.go:25-90` | Per-agent today (each `AgentInstance.Tools` is its own `*ToolRegistry`). The redesign replaces this with one shared instance. |
| `FilterToolsByPolicy` | `pkg/tools/compositor.go:298-374` | Already implements the global+agent layering with `deny > ask > allow` resolution. Returns the filtered tool slice + a name→effective-policy map. **Half of the redesign is already written**, just called from the wrong place. |
| `AgentBuiltinToolsCfg` | `pkg/config/config.go:495-523` | Wire format already correct: `default_policy: "allow"|"ask"|"deny"` + `policies: map[string]ToolPolicy`. Has `ResolvePolicy(toolName)` helper. |
| Frontend per-tool UI | `src/components/agents/ToolsAndPermissions.tsx:64-200+` | Already drives the flat allow/ask/deny map; auto-saves; has 4 client-side preset buttons (Unrestricted / Cautious / Standard / Minimal). |
| Three diverging REST endpoints | `pkg/gateway/rest.go:3047-3207` | `HandleTools` (default agent's registry), `HandleBuiltinTools` (catalog slice), `getAgentTools` (filter-by-policy). The redesign collapses or aligns these. |

---

## What I had wrong (corrected by user mid-conversation)

- **The "system agent" concept is not what CLAUDE.md describes.** CLAUDE.md says `omnipus-system` is a distinct hardcoded always-on agent with 35 exclusive `system.*` tools. The code in `pkg/agent/loop.go:5371-5408` (`WireSystemTools`) shows the 35 tools are wired onto whatever agent has `DefaultAgentID = "main"` — there is no separate system agent process or instance.
- **Other agents already use `system.*` tools.** `pkg/agent/loop.go:5417-5461` (`WireAvaAgentTools`) wires 4 of the 35 (`system.agent.create`, `system.agent.update`, `system.agent.delete`, `system.models.list`) onto Ava with a scope override to `ScopeCore`.
- Implication for the redesign: the "system agent" label is dead weight. `system.*` tools are ordinary catalog entries. Per-agent policy decides who gets them. CLAUDE.md needs an update once the spec lands.

---

## Open questions (Phase 1 of /plan-spec, not yet answered)

The list as last presented to the user, minus #1 which we discarded after the user's correction. To be re-asked one at a time, in plain English with options + recommendation, after the repo cleanup.

1. **Treatment of `system.*` tools** — *open, my framing was wrong, needs re-asking with the corrected understanding.* Options on the table: ordinary catalog entries with per-agent policy; same but default-deny on custom agents as a structural rail; drop the `system.` prefix entirely.
2. **MCP & skills.** Live in the central registry alongside builtins (registered/unregistered as servers connect / skills install)? Or stay as side registries that get merged at filter-time?
3. **Construction-time deps.** Today: closure-captured per-agent (provider keys, sandbox backend, Tier13 deps, message-bus callback). Two options: (a) tools constructed once at boot with a shared atomic-pointer to deps, updated on `ReloadProviderAndConfig`; (b) tools fully pure, all deps via `context.Value` at call time. Lean (a).
3. **Frontend policy presets.** Keep the 4 client-side preset buttons (frontend-only convenience that writes to the flat map) or remove (purer "no profiles" stance)?
4. **Tool name canonicalization.** `bm25_search`/`regex_search` (catalog) vs `tool_search_tool_bm25`/`tool_search_tool_regex` (runtime). Which is canonical, rename the other side. *Verify the mismatch exists first.*
5. **Legacy `Mode`/`Visible` fields.** `AgentBuiltinToolsCfg.Mode` (`inherit`/`explicit`) + `Visible` are auto-converted to policies at REST. Drop after one migration pass, or keep forever as compat shim?
6. **"Ask" execution semantics.** Concrete flow: LLM calls tool → if policy=ask, loop pauses + emits WS approval event → user approves (loop runs the tool) or denies (returns `permission_denied` result to the model). Confirm this is the spec.
7. **Hot-reload triggers.** Does the central registry rebuild on every config PUT, or only when specific keys change (provider keys, sandbox mode, Tier13 deps)? Lean: registry stays static, only the *policy filter view* and the *deps pointer* update on PUT.
8. **Single endpoint or three?** Today `/tools`, `/tools/builtin`, `/agents/{id}/tools` diverge. Collapse to one, or keep all three for SPA back-compat?

---

## Audit commands (verify the bug claims before the spec quotes them)

```bash
# 1. Catalog drift — are these 5 names missing from builtinCatalog?
for n in serve_workspace run_in_workspace build_static handoff return_to_default; do
  echo "=== $n ==="
  grep -n "\"$n\"" pkg/tools/catalog.go
done

# 2. Name mismatch — what does the runtime actually register?
grep -rn "Name() string" pkg/tools/ --include="*.go" | grep -i "search"
grep -rn "return \"tool_search\|return \"bm25\|return \"regex_search" pkg/tools/

# 3. Cataloged ghost — is NewRemoveSkillTool ever called?
grep -rn "NewRemoveSkillTool\|remove_skill" pkg/ --include="*.go" | grep -v _test.go

# 4. The 5 disjoint registries — count actual Register call sites
grep -rn "\.Register(" pkg/agent/ pkg/tools/ pkg/coreagent/ --include="*.go" | \
  grep -v _test.go | grep -v "// " | wc -l

# 5. Hot-reload re-wiring — what runs in handleConfigReload after Tier13 fix?
grep -n "registerSharedTools\|WireAvaAgentTools\|wireTier13DepsLocked\|WireSystemTools" pkg/agent/loop.go
```

---

## Cleanup checklist (before next planning session)

- [ ] Run the audit commands above and replace "claimed" with "verified" (or "false") in this doc
- [ ] CLAUDE.md still describes `omnipus-system` as a distinct always-on agent — outdated; update or remove once the redesign lands
- [ ] Decide whether the 4 frontend policy presets stay (Unrestricted / Cautious / Standard / Minimal in `ToolsAndPermissions.tsx:26-62`)
- [ ] Decide whether the three REST endpoints collapse before or after the registry refactor (low-risk to do them together)
- [ ] Re-read the prior session's claim of "5 disjoint registries" and either name them precisely or delete the claim

---

## Next planning step

When the repo + context cleanup is done, restart `/plan-spec` with this document as the input brief. The spec output goes to `docs/internal/specs/tool-registry-redesign-spec.md`.

Phase 1 of `/plan-spec` will re-ask the open questions one at a time with the corrected understanding from this document. Phase 1.5 will add the verified codebase context. Phase 2+ proceeds normally.
