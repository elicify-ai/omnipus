#!/usr/bin/env bash
# check-greenfield-providers.sh
#
# ADR-067 exit proof, the shell half: SC-008, SC-009 and US-11.AC2.
#
# ADR-067 removed provider identity MAPPING from Omnipus. There is no alias
# table, no `_migrated` marker, no deprecation shim, no rename ladder and no
# per-vendor base-URL switch. A stored provider id is a catalog id or an
# operator-named custom row; anything else is `ErrUnknownProvider`, named and
# unhinted (FR-011, FR-015). The catalog's own `status: retired` value and the
# search-only `aliases[]` field are the two tokens kept on purpose (A-3,
# FR-030) — and only inside pkg/providers/catalog.
#
# WHAT IS CHECKED
#
#   SC-008  pkg/providers/capabilities is gone (folded into
#           pkg/providers/catalog, FR-005), and the prefix-stripping resolver
#           it exported has no occurrence anywhere under pkg/.
#   SC-009  `_migrated|alias|deprecat|retired` does not appear in the CODE of
#           pkg/providers or pkg/config, outside the catalog package's two
#           sanctioned tokens.
#   AC2     the SPA's bundled provider catalog (src/lib/generated/
#           providerCatalog.ts) and its alias resolver (src/lib/
#           providerMigration.ts) do not exist (US-11.AC2).
#
# WHY COMMENTS ARE STRIPPED FIRST
#
# SC-009 is written as a raw grep, but both packages document at length WHICH
# mechanisms ADR-067 retired and why. Rewriting that prose to satisfy a regex
# would delete the explanation of the decision this gate enforces — the same
# reasoning ADR-068 §2.4 uses to keep historical records in
# scripts/no-removed-providers.allow. So the scan runs over source with
# comments removed by a real Go lexer (strings, raw strings and runes
# respected — a naive `s://.*::` would truncate every URL and could hide a
# match that follows one on the same line).
#
# WHY BOTH THIS AND A GO TEST
#
# pkg/providers/greenfield_test.go asserts the same property on the AST and is
# the stricter of the two (case-INsensitive, so `Alias`/`Deprecated`/`Retired`
# are caught as identifiers). This script exists because (a) SC-009 is
# specified as a grep and a grep is what the spec's reviewer will run, and
# (b) the SPA half (US-11.AC2) has no Go surface at all. They are deliberately
# complementary; neither replaces the other.
#
# Usage:  bash scripts/check-greenfield-providers.sh
#         bash scripts/check-greenfield-providers.sh --self-test
#         REPO_ROOT=<dir> bash scripts/check-greenfield-providers.sh
#
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run.

set -uo pipefail

NAME="check-greenfield-providers"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# --- forbidden identifiers, assembled from fragments --------------------------
# Assembled so THIS file is never its own first hit if the scanned roots are
# ever widened to include scripts/ (the same idiom check-no-removed-providers.sh
# and pkg/providers/factory_source_test.go use).
SYM_STRIP="resolve""StrippedPrefix"

# --- SPA artefacts (US-11.AC2) ------------------------------------------------
# MUST_BE_ABSENT: deleted; their return is a regression.
SPA_MUST_BE_ABSENT=(
  "src/lib/generated/providerCatalog.ts"
  # T067-13 deleted the alias resolver and rewrote its two consumers onto the
  # exact-match catalogDisplay.ts::catalogEntryById. Promoted here from
  # SPA_PENDING in that same commit.
  "src/lib/providerMigration.ts"
)
# SPA_PENDING: NOT yet deleted, and this gate knows it. Each row is a hole,
# owned by a named task, that CLOSES ITSELF: the script fails if a pending path
# has already gone, forcing whoever deletes it to move the row into
# SPA_MUST_BE_ABSENT in the same commit. A hole that cannot outlive its fix.
#
# EMPTY, and that is the finished state: every SPA artefact US-11.AC2 names is
# now in SPA_MUST_BE_ABSENT. Add a row here only for a NEW artefact whose
# deletion is scheduled but not yet landed.
SPA_PENDING=()

usage() { sed -n '2,60p' "${BASH_SOURCE[0]}"; }

# ── SC-009 scanner ──────────────────────────────────────────────────────────
#
# python3 (not `grep -P`): it must lex Go strings, raw strings, runes and both
# comment forms to strip comments without truncating a `https://…` literal, and
# it must behave identically on a contributor's macOS/BSD grep and on the
# ubuntu-latest runner. python3 is already a hard dependency of this repo's
# other guard scripts (check-no-tool-error-from-status.sh, check-no-handwritten-
# wire-types.sh).
#
# The match is case-INsensitive — strictly stronger than SC-009's literal
# `grep -rnE '_migrated|alias|deprecat|retired'`, which by being case-sensitive
# cannot see a Go-idiomatic `Aliases`/`AliasTable`/`Deprecated` at all.
#
# WIDENED (C1/C2 fix): the literal pattern also missed a real provider-identity
# migration — `knownProviderProtocols` / `migrateProviderFields` /
# `migrateAgentPrimaryProvider` (pkg/config/config.go) silently split a
# "provider/model" prefix off a bare model id and rerouted it to whatever
# provider the prefix happened to name, at a measured wrong-vendor rate,
# because the token in that code is `migrate`, not `_migrated`, and the gate
# never went red. The pattern now also carries bare `migrat` (covers
# `migrate`/`migrated`/`migration`/`Migrate`, `_migrated` included), `legacy`,
# `backcompat` and `back_compat`, so the SAME class of "we still carry the old
# shape / we still translate the old id" machinery is caught regardless of
# which of these near-synonymous English words the author reached for.
#
# Three exemptions, all token- or exact-line-scoped, never file- or
# package-scoped:
#
#   1. pkg/providers/catalog/**  may owe a match to `aliases` or `retired` and
#      nothing else (A-3, FR-030). Remove those two tokens from the line; if it
#      still matches, it is still a violation — a rename table in the catalog
#      package is no more allowed than one anywhere else.
#   2. The encoding/json embed idiom — `type Alias <T>` declared inside a
#      Marshal/UnmarshalJSON body to shed the method set — is not provider
#      machinery. The exemption applies only in a file that actually declares
#      it, and only to the standalone identifier `Alias`.
#   3. GENERIC_EXEMPT_LINES (below): pkg/config carries several OTHER, already-
#      reviewed migration/legacy-format mechanisms that have nothing to do with
#      provider IDENTITY — CLI bearer-token relocation, the ADR-054
#      entities/agents.list strip, the legacy `[]string` FallbackModel wire
#      shape, channel-config legacy flags, per-agent mailbox legacy shape, and
#      bcrypt token verification against one legacy hash. Widening the pattern
#      to catch C1 also lit these up. Each is exempted by its EXACT
#      (comment-stripped, trimmed) line text — never by file, package or bare
#      word — so a genuinely new match anywhere, including in these same
#      files, still trips the gate unless its literal text is already listed.
#      Keep it SHORT: every row is a hole in SC-009.
#
# stdout: one `path:line: text` per violation. Empty stdout means clean.
sc009_scan() {
  python3 - "$@" <<'PYEOF'
import os
import re
import sys

PATTERN = re.compile(
    r'_migrated|alias|deprecat|retired|legacy|backcompat|back_compat|migrat',
    re.IGNORECASE)
CATALOG_TOKENS = re.compile(r'aliases|retired', re.IGNORECASE)
JSON_IDIOM_DECL = re.compile(r'^\s*type\s+Alias\s+[A-Za-z_][A-Za-z0-9_]*\s*$')
BARE_ALIAS = re.compile(r'\bAlias\b')
CATALOG_PREFIX = os.path.join('pkg', 'providers', 'catalog') + os.sep

# GENERIC_EXEMPT_LINES: exact-text exemptions for already-reviewed
# migration/legacy-format mechanisms in pkg/config that are NOT provider
# identity (see exemption 3 in the module header above). Keyed by path
# relative to the repo root (forward slashes), value is the SET of exact
# comment-stripped, trimmed line texts allowed in that file. A line matching
# PATTERN whose stripped text is not byte-identical to an entry here is still
# a violation — including a DIFFERENT line in the same file.
GENERIC_EXEMPT_LINES = {
    'pkg/config/cli_token_migration.go': {
        'const legacyCLIUsername = "cli"',
        'func migrateCLITokenOutOfUsers(cfg *Config, cfgPath string, onSelfHeal SelfHealWriteHook) {',
        'if cfg.Gateway.Users[i].Username == legacyCLIUsername {',
        'legacy := cfg.Gateway.Users[idx]',
        'case len(legacy.Tokens) > 0 && !legacy.Tokens[len(legacy.Tokens)-1].Hash.IsZero():',
        'last := legacy.Tokens[len(legacy.Tokens)-1]',
        'case !legacy.TokenHash.IsZero():',
        'entry = &TokenEntry{Hash: legacy.TokenHash}',
        'logger.InfoF("relocating legacy \\"cli\\" Gateway.Users entry to Gateway.CLIToken "+',
        'written, healErr := migrateCLITokenOnDisk(cfgPath)',
        '"runtime behavior is still correct (in-memory state is migrated), "+',
        'func migrateCLITokenOnDisk(path string) ([]byte, error) {',
        'return nil, fmt.Errorf("read config for CLI-token migration: %w", err)',
        'return nil, fmt.Errorf("parse config for CLI-token migration: %w", unmarshalErr)',
        'if username, _ := um["username"].(string); username == legacyCLIUsername {',
        'return nil, fmt.Errorf("serialize config for CLI-token migration: %w", err)',
        'return nil, fmt.Errorf("write config for CLI-token migration: %w", writeErr)',
    },
    'pkg/config/config.go': {
        'var legacy []string',
        'if err := json.Unmarshal(data, &legacy); err == nil {',
        'out := make(FallbackModelSlice, len(legacy))',
        'for i, s := range legacy {',
        'slog.Warn("config: unknown channel type in channels map — ignoring legacy or unsupported section",',
        'return fmt.Errorf("mailboxes: legacy entry for agent %q: %w", agentID, err)',
        'slog.Warn("config: dropping legacy mailbox without workspace_id (unreachable)",',
        'return fmt.Errorf("mailboxes: agent %q entry is malformed (mixed legacy/nested shape)", agentID)',
        'func VerifyTokenAgainst(tokens []TokenEntry, legacyHash BcryptHash, raw string) error {',
        'if len(tokens) == 0 && legacyHash.IsZero() {',
        'if !legacyHash.IsZero() && legacyHash.Verify(raw) == nil {',
        'cfg.migrateChannelConfigs()',
        'stripLegacyAgentsList(cfg, path, onSelfHeal)',
        'migrateCLITokenOutOfUsers(cfg, path, onSelfHeal)',
        'func (c *Config) migrateChannelConfigs() {',
    },
    'pkg/config/legacy_agents_list.go': {
        'func stripLegacyAgentsList(cfg *Config, cfgPath string, onSelfHeal SelfHealWriteHook) {',
        'logger.WarnF("failed to strip legacy agents.list from config.json on disk; runtime "+',
        'logger.WarnF("config: dropping legacy agents.list entries for core agent IDs from "+',
        'logger.WarnF("config: dropping legacy agents.list entries for custom agent IDs from "+',
        '"no migration is performed (operator-accepted, no back-compat) — these agent IDs are "+',
    },
    'pkg/config/validate.go': {
        '"(migration from pre-DefaultPolicy-removal config shape; CLAUDE.md hard constraint 6 — "+',
        # ADR-071 D1/D4 tool-policy KEY rename shim (load_tool -> ToolSearch,
        # hand_off/return_to_default -> switch_agent) — a persisted-config-key
        # migration, not provider identity mapping; unrelated to ADR-067.
        'var legacyToolPolicyKeyMigrations = map[string]string{',
        'func migrateLegacyToolPolicyMap[T ~string](m map[string]T) bool {',
        'for legacy, dest := range legacyToolPolicyKeyMigrations {',
        'if _, ok := m[legacy]; ok {',
        'byDest[dest] = append(byDest[dest], legacy)',
        'for dest, legacyKeys := range byDest {',
        'for _, legacy := range legacyKeys {',
        'merged = stricterToolPolicyValue(merged, string(m[legacy]))',
        'delete(m, legacy)',
        'func MigrateLegacyToolPolicyKeys(cfg *Config) bool {',
        'changed := migrateLegacyToolPolicyMap(cfg.Sandbox.ToolPolicies)',
        'if migrateLegacyToolPolicyMap(agentCfg.Tools.Builtin.Policies) {',
    },
}


def strip_comments(src: str) -> str:
    """Blank out // and /* */ comments, preserving line structure.

    String, raw-string and rune literals are respected, so `"https://x"` keeps
    its slashes and a comment marker inside a literal is not treated as one.
    """
    out = []
    i, n = 0, len(src)
    while i < n:
        c = src[i]
        nxt = src[i + 1] if i + 1 < n else ''
        if c == '/' and nxt == '/':
            while i < n and src[i] != '\n':
                out.append(' ')
                i += 1
            continue
        if c == '/' and nxt == '*':
            while i < n and not (src[i] == '*' and i + 1 < n and src[i + 1] == '/'):
                out.append('\n' if src[i] == '\n' else ' ')
                i += 1
            out.append('  ')
            i += 2
            continue
        if c in ('"', '`', "'"):
            quote = c
            out.append(c)
            i += 1
            while i < n:
                ch = src[i]
                if quote != '`' and ch == '\\' and i + 1 < n:
                    out.append(ch)
                    out.append(src[i + 1])
                    i += 2
                    continue
                out.append(ch)
                i += 1
                if ch == quote:
                    break
                if quote != '`' and ch == '\n':
                    break  # unterminated literal; do not run past the line
            continue
        out.append(c)
        i += 1
    return ''.join(out)


def main() -> int:
    roots = sys.argv[1:]
    if not roots:
        print('sc009_scan: no roots given', file=sys.stderr)
        return 2
    scanned = 0
    for root in roots:
        if not os.path.isdir(root):
            print('sc009_scan: not a directory: %s' % root, file=sys.stderr)
            return 2
        for dirpath, dirnames, filenames in os.walk(root):
            dirnames[:] = [d for d in dirnames if d != 'testdata']
            for name in sorted(filenames):
                if not name.endswith('.go') or name.endswith('_test.go'):
                    continue
                path = os.path.join(dirpath, name)
                try:
                    with open(path, encoding='utf-8') as fh:
                        src = fh.read()
                except OSError as exc:
                    print('sc009_scan: cannot read %s: %s' % (path, exc), file=sys.stderr)
                    return 2
                scanned += 1
                json_idiom = any(JSON_IDIOM_DECL.match(ln) for ln in src.splitlines())
                in_catalog = os.path.normpath(path).startswith(CATALOG_PREFIX)
                rel = os.path.normpath(path).replace(os.sep, '/')
                exempt_lines = GENERIC_EXEMPT_LINES.get(rel)
                for lineno, line in enumerate(strip_comments(src).splitlines(), 1):
                    if not PATTERN.search(line):
                        continue
                    stripped = line.strip()
                    if exempt_lines is not None and stripped in exempt_lines:
                        continue
                    probe = line
                    if in_catalog:
                        probe = CATALOG_TOKENS.sub('', probe)
                    if json_idiom:
                        probe = BARE_ALIAS.sub('', probe)
                    if PATTERN.search(probe):
                        print('%s:%d: %s' % (path, lineno, stripped))
    if scanned == 0:
        print('sc009_scan: no non-test .go files under %s' % ', '.join(roots), file=sys.stderr)
        return 2
    return 0


sys.exit(main())
PYEOF
}

# ── the scan ────────────────────────────────────────────────────────────────

run_scan() { # run_scan <repo_root>  -> 0 clean, 1 offenders, 2 cannot run
  local root="$1"
  cd "$root" || { echo "$NAME: cannot cd to $root" >&2; return 2; }

  local d
  for d in pkg/providers pkg/config src; do
    if [ ! -d "$d" ]; then
      echo "$NAME: expected directory '$d' not found under $root" >&2
      echo "  (wrong cwd, a renamed package, or a partial checkout — refusing to report a" >&2
      echo "   green verdict for a tree this script never actually scanned)" >&2
      return 2
    fi
  done

  local offenders=""
  add() { offenders="${offenders}$1"$'\n'; }

  # ── SC-008a: the folded package is gone (FR-005) ──────────────────────────
  if [ -e "pkg/providers/capabilities" ]; then
    add "[SC-008] pkg/providers/capabilities still exists — it was folded into"
    add "         pkg/providers/catalog (FR-005); exactly one catalog package,"
    add "         exactly one embedded document."
  fi

  # ── SC-008b: the prefix-stripping resolver is gone, repo-wide under pkg/ ──
  #
  # pkg/gateway/spa/ is excluded: it is the gitignored go:embed staging copy of
  # the Vite build, not source. A fresh GitHub checkout has no such directory,
  # so a guard that scans it is green on CI and red on any machine that has run
  # `make build` — a verdict that depends on local build state is not a verdict.
  local strip_hits rc
  strip_hits="$(grep -rInI -e "$SYM_STRIP" -- pkg 2>/dev/null)"
  rc=$?
  strip_hits="$(printf '%s' "$strip_hits" | grep -v '^pkg/gateway/spa/' || true)"
  if [ "$rc" -gt 1 ]; then
    echo "$NAME: grep failed (exit $rc) while scanning for the prefix resolver" >&2
    return 2
  fi
  if [ -n "$strip_hits" ]; then
    add "[SC-008] the retired prefix-stripping resolver survives under pkg/:"
    add "$(printf '%s\n' "$strip_hits" | head -n 10 | sed 's/^/         /')"
    add "         FR-003 replaced it with exact (provider, model) lookup: a miss is a miss."
  fi

  # ── SC-009: no alias/migration/deprecation machinery in the two packages ──
  local sc009
  sc009="$(sc009_scan pkg/providers pkg/config 2>&1)"
  rc=$?
  if [ "$rc" -gt 1 ]; then
    echo "$NAME: the SC-009 scanner failed (exit $rc):" >&2
    printf '%s\n' "$sc009" >&2
    return 2
  fi
  if [ -n "$sc009" ]; then
    add "[SC-009] alias / migration / deprecation machinery in pkg/providers or pkg/config:"
    add "$(printf '%s\n' "$sc009" | sed 's/^/         /')"
    add "         ADR-067 US-11.AC1: a stored id is a catalog id or a custom row."
    add "         The only tokens kept are the catalog's aliases[] field and its"
    add "         retired status value, inside pkg/providers/catalog only (A-3, FR-030)."
  fi

  # ── US-11.AC2: the SPA's bundled catalog and alias resolver ───────────────
  local f
  for f in "${SPA_MUST_BE_ABSENT[@]}"; do
    if [ -e "$f" ]; then
      add "[AC2]    $f exists — deleted by ADR-067 FR-025; the SPA reads"
      add "         GET /providers/catalog and nothing else."
    fi
  done
  for f in ${SPA_PENDING[@]+"${SPA_PENDING[@]}"}; do
    if [ ! -e "$f" ]; then
      add "[AC2]    $f is gone — good. Now move it from SPA_PENDING into"
      add "         SPA_MUST_BE_ABSENT in $NAME (same commit), so the gate keeps"
      add "         it deleted instead of merely expecting it."
    fi
  done

  offenders="$(printf '%s' "$offenders" | grep -v '^[[:space:]]*$' || true)"
  if [ -n "$offenders" ]; then
    echo "$NAME: ADR-067 greenfield violations:" >&2
    echo "" >&2
    printf '%s\n' "$offenders" >&2
    echo "" >&2
    echo "If you hit this after a merge or rebase from an older branch: that branch" >&2
    echo "predates ADR-067 and git re-added the mapping as an ordinary addition." >&2
    echo "RESOLVE BY KEEPING THE DELETION — do not restore the alias path." >&2
    return 1
  fi
  return 0
}

# ── self-test: prove the gate can go red ────────────────────────────────────
#
# A guard nobody has ever seen fail is a guard nobody should trust
# (docs/internal/false-green-patterns.md). This plants one violation of each
# class in a throwaway tree and asserts the scan reports it.

self_test() {
  local tmp fails=0
  tmp="$(mktemp -d)" || { echo "$NAME: mktemp failed" >&2; return 2; }
  trap 'rm -rf "$tmp"' RETURN

  fixture() { # fixture -> a minimal CLEAN tree in $tmp/case
    rm -rf "$tmp/case"
    mkdir -p "$tmp/case/pkg/providers/catalog" "$tmp/case/pkg/config" "$tmp/case/src/lib"
    cat > "$tmp/case/pkg/providers/catalog/document.go" <<'GO'
package catalog

// The retired status value and the aliases field are the two sanctioned
// tokens: a comment like this one, mentioning alias and deprecated and
// retired machinery, must never trip the gate.
const StatusRetired = "retired"

type Provider struct {
	Aliases []string `json:"aliases"`
}
GO
    cat > "$tmp/case/pkg/config/config.go" <<'GO'
package config

/* Block comments may also discuss the retired alias ladder and the
   deprecated provider prefixes without tripping the gate. */
const Endpoint = "https://example.invalid/v1" // a deprecated alias lived here

func marshal(c *Provider) any {
	type Alias Provider
	return (*Alias)(c)
}

type Provider struct{}
GO
    # SPA: every artefact US-11.AC2 names is absent in the clean case.
  }

  expect() { # expect <label> <want_rc>
    local label="$1" want="$2" got out
    out="$(run_scan "$tmp/case" 2>&1)"
    got=$?
    if [ "$got" -ne "$want" ]; then
      echo "SELF-TEST FAIL: $label -> exit $got, want $want" >&2
      printf '%s\n' "$out" | sed 's/^/    /' >&2
      fails=$((fails + 1))
    else
      echo "  ok  $label (exit $got)"
    fi
  }

  echo "$NAME --self-test"

  fixture
  expect "clean tree passes" 0

  fixture
  mkdir -p "$tmp/case/pkg/providers/capabilities"
  expect "SC-008: capabilities package back" 1

  fixture
  printf 'package providers\n\nfunc %s(s string) string { return s }\n' \
    "$SYM_STRIP" > "$tmp/case/pkg/providers/strip.go"
  expect "SC-008: prefix resolver back" 1

  fixture
  mkdir -p "$tmp/case/pkg/gateway/spa/assets"
  printf 'function %s(s){return s}\n' "$SYM_STRIP" > "$tmp/case/pkg/gateway/spa/assets/app-abc123.js"
  expect "SC-008: build output under pkg/gateway/spa/ is not source" 0

  fixture
  cat > "$tmp/case/pkg/config/aliases.go" <<'GO'
package config

var providerAliases = map[string]string{"z-ai": "zai"}
GO
  expect "SC-009: alias table in pkg/config" 1

  fixture
  cat > "$tmp/case/pkg/providers/deprecated.go" <<'GO'
package providers

const deprecatedProviderNote = "use zai instead"
GO
  expect "SC-009: deprecation literal in pkg/providers" 1

  fixture
  cat > "$tmp/case/pkg/providers/catalog/rename.go" <<'GO'
package catalog

var providerAliasRenames = map[string]string{"moonshot-cn-anthropic": "moonshotai-cn"}
GO
  expect "SC-009: catalog exemption is token-scoped, not package-wide" 1

  fixture
  cat > "$tmp/case/pkg/providers/url.go" <<'GO'
package providers

const note = "https://example.invalid/v1" + deprecatedSuffix
GO
  expect "SC-009: a match after a URL is not hidden by comment stripping" 1

  fixture
  cat > "$tmp/case/pkg/providers/jsonidiom.go" <<'GO'
package providers

var providerAliasTable = map[string]string{"z-ai": "zai"}

func marshal(c *Row) any {
	type Alias Row
	return (*Alias)(c)
}

type Row struct{}
GO
  expect "SC-009: the json Alias idiom does not license a real alias table" 1

  # ── C2 fix: mutation cases for the widened tokens ──────────────────────────
  # Each plants a violation that used ONLY the literal `_migrated|alias|
  # deprecat|retired` pattern would have missed — proving the widening (which
  # is what let the real C1 defect, migrateProviderFields /
  # migrateAgentPrimaryProvider, slip past this gate for real) actually bites.

  fixture
  cat > "$tmp/case/pkg/config/protomigrate.go" <<'GO'
package config

// A provider-shaped migration function using only the bare word "migrate" —
// no "_migrated", "alias", "deprecat" or "retired" anywhere on the line.
func migrateFallbackProtocol(id string) string { return id }
GO
  expect "SC-009: bare 'migrat' token (the exact C1 miss) in pkg/config" 1

  fixture
  cat > "$tmp/case/pkg/providers/protolegacy.go" <<'GO'
package providers

const legacyProtocolPrefix = "openrouter/"
GO
  expect "SC-009: 'legacy' token in pkg/providers" 1

  fixture
  cat > "$tmp/case/pkg/config/protobackcompat.go" <<'GO'
package config

const backcompatProviderShim = true
GO
  expect "SC-009: 'backcompat' token in pkg/config" 1

  fixture
  cat > "$tmp/case/pkg/providers/protoback.go" <<'GO'
package providers

var back_compatProviderMap = map[string]string{}
GO
  expect "SC-009: 'back_compat' token in pkg/providers" 1

  # ── C2 fix: GENERIC_EXEMPT_LINES precision (exact-line, not file-wide) ─────

  fixture
  cat > "$tmp/case/pkg/config/cli_token_migration.go" <<'GO'
package config

const legacyCLIUsername = "cli"

func migrateCLITokenOnDisk(path string) ([]byte, error) {
	return nil, nil
}
GO
  expect "SC-009: GENERIC_EXEMPT_LINES's own listed lines pass clean" 0

  fixture
  cat > "$tmp/case/pkg/config/cli_token_migration.go" <<'GO'
package config

const legacyCLIUsername = "cli"

func migrateSomethingElseEntirely(x string) string { return x }
GO
  expect "SC-009: an unlisted line in an exempted file still trips the gate" 1

  fixture
  mkdir -p "$tmp/case/src/lib/generated"
  printf 'export const CATALOG = []\n' > "$tmp/case/src/lib/generated/providerCatalog.ts"
  expect "AC2: bundled SPA catalog back" 1

  fixture
  printf 'export const x = 1\n' > "$tmp/case/src/lib/providerMigration.ts"
  expect "AC2: alias resolver back" 1

  fixture
  rm -rf "$tmp/case/pkg/config"
  expect "missing scan root reports cannot-run, not clean" 2

  if [ "$fails" -ne 0 ]; then
    echo "$NAME: $fails self-test case(s) failed — the gate's verdict cannot be trusted" >&2
    return 1
  fi
  echo "$NAME: self-test passed"
  return 0
}

# ── entry point ─────────────────────────────────────────────────────────────

case "${1:-}" in
  --self-test) self_test; exit $? ;;
  -h|--help)   usage; exit 0 ;;
  "")          ;;
  *)           echo "$NAME: unknown argument '$1'" >&2; usage >&2; exit 2 ;;
esac

REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
run_scan "$REPO_ROOT"
exit $?
