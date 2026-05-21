# Skills

Skills are reusable bundles of prompt content and tool policy that an agent can pull in on demand. A skill lives in its own directory with a single required file, `SKILL.md`, that combines YAML frontmatter (metadata + tool allow-list) with a Markdown body (the prompt). Skills are how the operator and the community extend an agent's capabilities without forking the agent definition itself: drop a directory under `~/.omnipus/skills/`, restart, and the loader makes the skill visible to every agent in the workspace. Skills can be vendored into a project (workspace skills), installed globally for an operator, shipped as a builtin, or fetched at runtime from the ClawHub community registry.

## SKILL.md format

The loader at `pkg/skills/loader.go:230-303` parses each skill's `SKILL.md`. Frontmatter is optional — if absent, the loader falls back to the directory name for `name` and the first paragraph of the body for `description` (`pkg/skills/loader.go:305-337`). Frontmatter may be either YAML (preferred) or a single JSON object (legacy; `pkg/skills/loader.go:258-270`). Recognized fields are mapped to `SkillMetadata` (`pkg/skills/loader.go:30-39`):

| Field           | Type           | Required | Meaning                                                                                                                                                              |
| --------------- | -------------- | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `name`          | string         | yes\*    | Skill identifier. Must match `^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$`, max 64 chars (`pkg/skills/loader.go:21-26, 48-67`). Falls back to the directory name when omitted.   |
| `description`   | string         | yes\*    | Short one-line description. Max 1024 chars. Falls back to the first paragraph of the body.                                                                          |
| `argument-hint` | string         | no       | Hint for invocation arguments (e.g. `"[query]"`). Stored in `SkillMetadata.ArgumentHint`.                                                                            |
| `context`       | string         | no       | One of `workspace` / `global` / `builtin`. ClawHub-extended; carried through but not enforced by the loader (`pkg/skills/loader.go:283-285`).                        |
| `allowed-tools` | list or string | no       | Tools this skill expects access to. YAML sequence or comma-separated string. Surfaced to the policy engine via `DiscoverAllTools` — see [Discovery](#discovery).    |
| `model-hint`    | string         | no       | Optional preferred model name; the loader records it but the runtime does not auto-route on it today.                                                                |

\* The validator in `loader.go:48-67` requires both `name` and `description` to be non-empty after fallback. Skills that still fail validation are skipped with a `slog.Warn` and don't appear in `ListSkills()`.

Unknown frontmatter keys are preserved under `SkillMetadata.Extra` for forward compatibility (`pkg/skills/loader.go:292-301`).

### Sample SKILL.md

```markdown
---
name: github-prs
description: Open, list, and review GitHub pull requests from inside Omnipus.
argument-hint: "<command> [args...]"
context: workspace
allowed-tools:
  - workspace.shell
  - web_fetch
model-hint: anthropic/claude-3.5-haiku
---

# GitHub PRs

When the user asks about pull requests, use `gh pr` via `workspace.shell` for
list / view / create operations. Fall back to `web_fetch` against the GitHub
REST API only when `gh` is unavailable.

## Conventions

- Always show the PR number, title, and CI status together.
- Group results by repo when listing across multiple projects.
```

The body is plain Markdown; everything between the closing `---` and end of file is concatenated into the agent's context (see [Discovery](#discovery)).

## Discovery

There is no separate "skill registration" step — the loader scans three directories every time it is asked for the list, in priority order (`pkg/skills/loader.go:99-159`):

1. **Workspace skills** — `<workspace>/skills/<name>/SKILL.md`. The workspace is the agent's working directory, typically `~/.omnipus/`.
2. **Global skills** — `<globalSkillsDir>/<name>/SKILL.md`. By default this is `~/.omnipus/skills/` (set by `cmd/omnipus/internal/skills/command.go:44-47`).
3. **Builtin skills** — `<builtinSkillsDir>/<name>/SKILL.md`. By default `~/.omnipus/omnipus/skills/`, populated by `omnipus skills install-builtin`.

Earlier directories win — a workspace skill with the same name as a global one shadows it. Each entry must be a directory containing a `SKILL.md`; other files in the parent directory are ignored.

Once per turn, `ContextBuilder` (`pkg/agent/context.go:267-275`) calls `BuildSkillsSummary()` and injects a `<skills>` XML block into the agent's system prompt. Each skill contributes one entry containing its name, description, on-disk path, and source (`workspace`/`global`/`builtin`). The block does **not** include the body — the agent is told to "read its SKILL.md file using the `read_file` tool" when it wants to actually use a skill. This is a deliberately simple form of progressive disclosure: every skill announces itself, only the relevant body is paid for in tokens.

`DiscoverAllTools(loader)` (`pkg/skills/discovery.go:21-44`) walks the same list of installed skills and emits one `DiscoveredTool` per entry in `allowed-tools`. The policy engine still has to approve each one (SEC-04, SEC-07) — declaring a tool in `allowed-tools` does **not** grant the skill permission to call it.

The MCP bridge (`pkg/skills/mcp_bridge.go`) reuses the same `DiscoveredTool` shape so MCP server tools and skill-declared tools flow through the same policy pipeline.

## Skill tools

Two tools let an agent search and install skills at runtime. Both share a single `RegistryManager` instance built from `tools.skills.registries` in `config.json` (`cmd/omnipus/internal/skills/helpers.go:25-41`).

### `find_skills`

Defined at `pkg/tools/skills_search.go:29-98`. Searches every enabled registry concurrently and returns ranked results.

**Arguments**

| name    | type    | required | notes                                                |
| ------- | ------- | -------- | ---------------------------------------------------- |
| `query` | string  | yes      | Free-text query, lower-cased and trimmed.             |
| `limit` | integer | no       | 1–20, default 5.                                      |

Results are cached by trigram similarity (`pkg/skills/search_cache.go:28-29`, Jaccard ≥ 0.7) so repeat or near-duplicate queries don't burn round-trips. Output is a human-readable list of `slug`, `version`, `score`, `registry`, `display name`, and `summary`, with an "Use `install_skill` with the slug" trailer (`pkg/tools/skills_search.go:122-138`).

Under the hood `RegistryManager.SearchAll` (`pkg/skills/registry.go:140-230`) fans out to every enabled registry with a per-call 1-minute deadline and a configurable concurrency cap (`MaxConcurrentSearches`, default 2). If at least one registry succeeds it returns the partial result plus a `PartialSearchError`; if all fail it returns the last error.

### `install_skill`

Defined at `pkg/tools/skills_install.go:39-179`. Downloads and extracts a skill into `<workspace>/skills/<slug>/`.

**Arguments**

| name       | type    | required | notes                                                          |
| ---------- | ------- | -------- | -------------------------------------------------------------- |
| `slug`     | string  | yes      | Validated by `utils.ValidateSkillIdentifier`.                  |
| `registry` | string  | yes      | Registry name (e.g. `clawhub`). Same validation as slug.       |
| `version`  | string  | no       | Defaults to `latest`.                                          |
| `force`    | boolean | no       | If `true`, removes any existing install before re-installing.  |

The tool holds a process-wide mutex while installing (`pkg/tools/skills_install.go:77-78` — TODO: scope down to per-slug). It refuses to overwrite an existing directory unless `force=true`, then delegates to `registry.DownloadAndInstall` (`pkg/skills/clawhub_registry.go:230-308`). That call:

1. Fetches metadata for the slug.
2. Streams the ZIP to a temp file, capped at `MaxZipSize` bytes (default 50 MiB, `pkg/skills/clawhub_registry.go:21,400-403`).
3. **Verifies the SHA-256 of the ZIP against `latestVersion.sha256` from the metadata** (`pkg/skills/clawhub_registry.go:288-300`, SEC-09). On mismatch the install aborts with `hash verification failed`.
4. Extracts via `utils.ExtractZipFile`, which rejects path-traversal entries and any entry with the symlink bit set (`pkg/utils/zip.go:17,53-55`).
5. Returns an `InstallResult` with `Verified`, `IsMalwareBlocked`, `IsSuspicious`, and `Summary`. The caller blocks malware-flagged installs outright and surfaces a warning for `IsSuspicious`.

Successful installs also write `.skill-origin.json` into the skill directory (`pkg/tools/skills_install.go:181-206`) recording the registry, slug, installed version, and install timestamp — `omnipus skills update` uses this to re-resolve the source registry.

## ClawHub registry

ClawHub is the community-curated skill index, baked into the default config at `https://clawhub.ai` (`pkg/skills/clawhub_registry.go:39-42`). The registry exposes three endpoints, all overridable in config:

| Purpose              | Default path        | Method | Notes                                                            |
| -------------------- | ------------------- | ------ | ---------------------------------------------------------------- |
| Search               | `/api/v1/search`    | GET    | `?q=<query>&limit=<n>`. Returns `{ results: [...] }`.            |
| Skill metadata       | `/api/v1/skills/{slug}` | GET | Returns slug, display name, summary, `latestVersion.sha256`, moderation flags. |
| Download             | `/api/v1/download`  | GET    | `?slug=<slug>&version=<ver>`. Returns `application/zip`.         |

Per-registry settings live under `tools.skills.registries.clawhub` (`pkg/config/config.go:1521-1544`): `enabled`, `base_url`, `auth_token_ref` (env-var name resolved at boot), `search_path`, `skills_path`, `download_path`, `timeout`, `max_zip_size`, `max_response_size`. Search responses are size-limited to `MaxResponseSize` bytes (default 2 MiB).

Outbound calls go through `utils.DoRequestWithRetry` and — when the gateway is the caller — an SSRF-safe HTTP client (`pkg/skills/clawhub_registry.go:71-84`, `pkg/skills/installer.go:50-78`) that blocks RFC1918 / metadata-endpoint targets per SEC-24.

How a skill ends up on ClawHub is out of scope for this repo; that is the registry's own publishing flow (`https://clawhub.ai`). From Omnipus's side a published skill needs to ship a SHA-256 in its `latestVersion` payload for hash verification to succeed.

## GitHub installs

The CLI also supports installing directly from a GitHub repo (`pkg/skills/installer.go:135-167`). `InstallFromGitHub` accepts:

- shorthand `owner/repo`,
- `owner/repo/sub/path` to install only a subdirectory,
- a full `https://github.com/owner/repo/tree/<ref>/<path>` URL.

It walks `https://api.github.com/repos/<owner>/<repo>/contents/<path>?ref=<ref>` (`pkg/skills/installer.go:151-156`), downloads `SKILL.md` at the root plus every file under the conventional subdirectories `scripts/`, `references/`, `assets/`, `templates/`, `docs/` (`pkg/skills/installer.go:282-297`). If the API call fails it falls back to a single raw `SKILL.md` fetch from `raw.githubusercontent.com` (`pkg/skills/installer.go:222-254`). There is **no** hash verification on the GitHub path today — only ClawHub installs are SHA-256 verified.

## CLI surface

`omnipus skills <subcommand>` (`cmd/omnipus/internal/skills/command.go`):

| Subcommand        | Source file        | What it does                                                                                                                      |
| ----------------- | ------------------ | --------------------------------------------------------------------------------------------------------------------------------- |
| `list`            | `list.go`          | Lists installed skills across workspace / global / builtin with their source label and description.                                |
| `install <repo>`  | `install.go`       | Installs from a GitHub reference. With `--registry <name> <slug>`, installs from the named registry (e.g. `--registry clawhub`).  |
| `install-builtin` | `installbuiltin.go` | Copies the seeded `weather`, `news`, `stock`, `calculator` skills from `./omnipus/skills` into the workspace (`helpers.go:295-340`). |
| `list-builtin`    | `listbuiltin.go`   | Lists skills under the builtin directory.                                                                                          |
| `remove <name>`   | `remove.go`        | Removes a workspace skill directory. Aliases: `rm`, `uninstall`.                                                                  |
| `update <name>`   | `update.go`        | Re-installs to the latest version using the registry recorded in `.skill-origin.json` (falls back to `clawhub`).                  |
| `search [query]`  | `search.go`        | Runs `RegistryManager.SearchAll` against the configured registries.                                                                |
| `show <name>`     | `show.go`          | Prints the skill body (frontmatter stripped) to stdout.                                                                            |

Both `install` (registry mode) and `update` enforce `sandbox.skill_trust` (see [Security](#security)).

## UI surface

The SPA has a top-level **Skills & Tools** route at `/skills` (`src/routes/_app/skills.tsx`, linked from the sidebar at `src/components/layout/Sidebar.tsx:24`). It lists installed skills and exposes a "Browse Skills" button that opens `SkillBrowser` (`src/components/skills/SkillBrowser.tsx`).

`SkillBrowser` is currently a **stub**: it shows "ClawHub registry not yet available" and only supports "Install from file" (uploading a SKILL.md / ZIP). It hard-codes the unavailable message even though the backend `find_skills` tool talks to a live ClawHub. Tracked as issue [#14](https://github.com/dapicom-ai/omnipus/issues/14).

The Security settings tab includes a `SkillTrustSection` (`src/components/settings/SkillTrustSection.tsx`) that reads and writes `sandbox.skill_trust` via `GET`/`PUT /api/v1/settings/skill-trust` (`pkg/gateway/rest_skill_trust.go`). All three levels — `block_unverified`, `warn_unverified`, `allow_all` — are selectable, with copy describing the trade-off.

The gateway-side REST API surface for skills is `pkg/gateway/rest.go:2084-2160`: `GET /api/v1/skills` (list), `POST /api/v1/skills/search` (returns 501 today), `POST /api/v1/skills/install` (returns 501), and `DELETE /api/v1/skills/{name}`. The CLI and the `install_skill` tool are the supported install paths until the REST surface lands.

## Security

Hash-based trust is governed by `sandbox.skill_trust` in `config.json` (`pkg/config/sandbox.go:12-43, 324-327`). Three valid values:

| Level               | Behavior                                                                                                                                                   |
| ------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `block_unverified`  | Reject any install whose ZIP can't be hash-verified against `latestVersion.sha256`. The partial install is removed (`cmd/omnipus/internal/skills/helpers.go:95-103`). |
| `warn_unverified`   | **Default.** Allow the install but print a warning that the skill couldn't be hash-verified.                                                                |
| `allow_all`         | Skip hash verification entirely.                                                                                                                            |

The empty string is treated as `warn_unverified` (`pkg/policy/wave3_skill_trust_test.go:34-41`). Unknown values are rejected at config decode time so a typo fails boot rather than silently downgrading to a permissive level.

`allow_all` is intended to be loud, but the doctor warning is **not yet wired** — `omnipus doctor` does not currently flag the setting. Tracked as issue [#99](https://github.com/dapicom-ai/omnipus/issues/99).

Additional gates already in place:

- **Malware flag.** If ClawHub metadata returns `moderation.isMalwareBlocked: true`, the install is aborted and the partial directory removed unconditionally — independent of `skill_trust` (`pkg/tools/skills_install.go:137-149`).
- **Suspicious flag.** Surfaced as a warning in the install output but does not block.
- **Path traversal & symlinks.** The ZIP extractor rejects entries whose path escapes the target directory and any entry with the symlink mode bit set (`pkg/utils/zip.go:53-55`).
- **SSRF.** When constructed with `NewSkillInstallerWithSSRF` or a `cfg.HTTPClient` from `security.SSRFChecker.SafeClient()`, outbound HTTP refuses RFC1918 / metadata-IP targets (`pkg/skills/installer.go:50-78`, `pkg/skills/clawhub_registry.go:71-84`).
- **Size cap.** ZIPs over `MaxZipSize` (default 50 MiB) and search responses over `MaxResponseSize` (default 2 MiB) are truncated and the install fails.

## What's not shipping yet

- Issue [#14](https://github.com/dapicom-ai/omnipus/issues/14) — the **Browse Skills** modal in the SPA is a stub. The backend `find_skills` tool already speaks to ClawHub correctly; only the UI is gated.
- Issue [#99](https://github.com/dapicom-ai/omnipus/issues/99) — `omnipus doctor` does **not** warn when `sandbox.skill_trust = allow_all`. The setting is reachable from the UI and CLI but the doctor surface is silent.
- Issue [#152](https://github.com/dapicom-ai/omnipus/issues/152) — **auto-load on relevance** (Anthropic-style progressive disclosure) is not implemented. Today the agent gets the full `<skills>` summary every turn and must `read_file` the bodies it wants to use; there is no model-driven match-and-load step.
- `POST /api/v1/skills/search` and `POST /api/v1/skills/install` return HTTP 501 (`pkg/gateway/rest.go:2129-2143`). Use the CLI or the in-loop `install_skill` tool.
- GitHub installs are not hash-verified. `sandbox.skill_trust` only applies to ClawHub installs (the GitHub flow has no manifest to verify against).
- `model-hint` is parsed but not acted on — the runtime does not auto-switch models based on a skill's preferred model.

## Quick start

Install a skill from ClawHub via the CLI, list it, and use it:

```bash
omnipus skills install --registry clawhub github-prs   # downloads, hash-verifies, extracts to ~/.omnipus/skills/github-prs/
omnipus skills list                                    # confirms 'github-prs (workspace)' is loaded
```

Or from inside a chat with an agent that has the tools enabled:

```text
> find me a skill for working with github pull requests
[agent calls find_skills(query="github pull requests")]
> install the github-prs one
[agent calls install_skill(slug="github-prs", registry="clawhub")]
```

On the next turn the new skill appears in the system prompt's `<skills>` block and the agent can `read_file ~/.omnipus/skills/github-prs/SKILL.md` to pull its body into context.
