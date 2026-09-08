// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// grep is the agent-facing surface of ADR-081 / workstream B
// (docs/internal/specs/unified-search-and-grep-spec.md, US-3 "Best-in-class
// agent grep tool"): recursive file name AND text content search over the
// calling agent's OWN workspace root and its mounts — no `workspace_id`
// argument, ever (FR-020, US-3 AS-7). It is a thin tools.Tool adapter over
// the frozen pkg/filegrep engine; every search bound, truncation reason, and
// case-mode rule lives there (see that package's own doc comment) — this
// file only resolves WHICH folders an agent may search, translates its
// arguments into filegrep.Options, and renders the result for the model.
package tools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/filegrep"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// grepBusyWait is MV-11's tool-side contract: wait up to this long for a
// walk semaphore slot, then return a structured busy error rather than
// blocking the turn indefinitely behind two other searches.
const grepBusyWait = 2 * time.Second

// GrepTool is the `grep` tool (FR-008).
type GrepTool struct {
	BaseTool
	// agentHome is the agent's fixed home directory — the exact same
	// constructor argument ReadFileTool/ListDirTool call "workspace"
	// (pre-ADR-046-rename). It feeds ResolveTurnFSPolicy exactly as those
	// tools' Execute methods do, so a turn re-rooted into a Workspace's
	// CoreTeam work tree (TurnWorkspaceDir) is honoured identically here —
	// grep searches whatever directory read_file would read from, never a
	// second, independently-computed idea of "this agent's workspace".
	agentHome string
	// restrict mirrors read_file/list_directory's own restrict flag
	// (fspolicy.FSScopeConfined vs Unrestricted). It is threaded through
	// only because ResolveTurnFSPolicy's signature requires it — grep never
	// actually consults policy.Scope (see grepRoots' doc comment): FR-020
	// confines every search to policy.WorkDir + its mounts unconditionally,
	// which is already narrower than what FSScopeUnrestricted would permit
	// for other tools' arbitrary path arguments.
	restrict bool
}

// NewGrepTool builds the tool. agentHome/restrict are the same values the
// call site in pkg/agent/instance.go already computed for
// NewReadFileTool/NewListDirTool.
func NewGrepTool(agentHome string, restrict bool) *GrepTool {
	return &GrepTool{agentHome: agentHome, restrict: restrict}
}

func (t *GrepTool) Name() string { return "grep" }

func (t *GrepTool) Description() string {
	return fmt.Sprintf(
		"Recursively search file NAMES and text CONTENT across your own workspace and every folder "+
			"mounted into it — never anywhere else; there is no workspace_id argument, so another "+
			"agent's or workspace's files are never reachable. Literal substring matching by default, "+
			"with smart case (a lowercase `pattern` matches any case; any uppercase letter makes the "+
			"match case-sensitive) — set `regex: true` for full RE2 syntax (no backreferences/lookaround). "+
			"Every file's NAME is always checked; TEXT content is scanned too (binaries are name-matched "+
			"only, skipped for content), up to a %d MiB cap per file, always skipping `.git`, `.library`, "+
			"`.omnipus-vault`, and anything `.gitignore`d. Use `path` to narrow to one subdirectory or one "+
			"mounted folder (by its mount name, optionally with a subpath, e.g. \"my-mount/src\") instead "+
			"of searching everything; `include_globs`/`exclude_globs` filter by doublestar pattern "+
			"(e.g. \"**/*.go\"). `context_lines` (0-5) adds surrounding lines to each content match. "+
			"A search is capped at %d total matches and %d matches per file by default — `max_matches`/"+
			"`max_matches_per_file` may only LOWER those caps, never raise them. The rendered result is "+
			"capped at %d characters with an explicit marker if cut. If a search is truncated for any "+
			"reason, the response says why and how to narrow it — narrower `path`/globs and lower caps "+
			"both make the next call more likely to finish uncapped. At most two searches (from this "+
			"tool and/or the Library search bar) run at once; a third waits briefly and then reports busy "+
			"with a retry hint.",
		filegrep.PerFileContentCap>>20, filegrep.DefaultMaxMatches, filegrep.DefaultMatchesPerFile,
		config.DefaultBuiltinSuccessCap,
	)
}

func (t *GrepTool) Scope() ToolScope       { return ScopeGeneral }
func (t *GrepTool) Category() ToolCategory { return CategoryFilesystem }

func (t *GrepTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Text to search for. Literal by default; set `regex: true` to use RE2 syntax instead.",
			},
			"regex": map[string]any{
				"type":        "boolean",
				"description": "Treat `pattern` as a full RE2 regular expression instead of a literal string.",
				"default":     false,
			},
			"case": map[string]any{
				"type": "string",
				"enum": []string{
					string(filegrep.CaseSmart), string(filegrep.CaseSensitive), string(filegrep.CaseInsensitive),
				},
				"description": "Case sensitivity. \"smart\" (default) is case-sensitive only when `pattern` " +
					"contains an uppercase letter; \"sensitive\" and \"insensitive\" force one mode always.",
				"default": string(filegrep.CaseSmart),
			},
			"path": map[string]any{
				"type": "string",
				"description": "Narrow the search to one subdirectory of your workspace, one mounted folder by " +
					"its mount name (optionally followed by a subpath, e.g. \"my-mount/src\"), or a single FILE " +
					"(e.g. \"src/main.go\" or \"my-mount/notes.txt\") to search just that one file. Omit to " +
					"search your whole workspace root plus every mount.",
			},
			"include_globs": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
				"description": "Only report files whose path matches at least one of these doublestar globs (e.g. \"**/*.go\"). " +
					"A pattern matches the WHOLE path relative to the search root, not just the filename: a bare " +
					"\"name.ext\" only matches a file sitting AT the root, never one in a subdirectory — write " +
					"\"**/name.ext\" to match that file at any depth. A glob that matches nothing is never a silent " +
					"no-op — the result states how many files it filtered out.",
			},
			"exclude_globs": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
				"description": "Never report files whose path matches any of these doublestar globs. Same anchoring " +
					"rule as include_globs: a pattern matches the whole relative path, so a bare \"name.ext\" only " +
					"excludes a root-level file — use \"**/name.ext\" to exclude it at any depth.",
			},
			"context_lines": map[string]any{
				"type":        "integer",
				"description": "Lines of context before and after each content match (0-5). Out-of-range values are clamped into range.",
				"default":     0,
			},
			"max_matches": map[string]any{
				"type": "integer",
				"description": fmt.Sprintf(
					"Cap on total hits returned. May only LOWER the server default (%d) — a higher value is silently clamped back down.",
					filegrep.DefaultMaxMatches,
				),
			},
			"max_matches_per_file": map[string]any{
				"type": "integer",
				"description": fmt.Sprintf(
					"Cap on hits contributed by a single file. May only LOWER the server default (%d) — a higher value is silently clamped back down.",
					filegrep.DefaultMatchesPerFile,
				),
			},
		},
		"required": []string{"pattern"},
	}
}

// Execute validates arguments, resolves the calling agent's own confined
// roots, runs filegrep.Search under the shared walk semaphore, and renders
// a compact, capped text result for the model.
func (t *GrepTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	patternRaw, ok := args["pattern"]
	if !ok || patternRaw == nil {
		return ErrorResult("grep: `pattern` is required")
	}
	pattern, ok := patternRaw.(string)
	if !ok {
		return ErrorResult("grep: `pattern` must be a string")
	}
	if pattern == "" {
		return ErrorResult("grep: `pattern` must not be empty")
	}

	regexFlag := getBoolArg(args, "regex")

	caseMode, err := grepCaseArg(args)
	if err != nil {
		return ErrorResult("grep: " + err.Error())
	}

	scope, err := grepStringArg(args, "path")
	if err != nil {
		return ErrorResult("grep: " + err.Error())
	}
	if scopeErr := validateGrepScope(scope); scopeErr != nil {
		return ErrorResult("grep: " + scopeErr.Error())
	}

	includeGlobs, err := grepStringSliceArg(args, "include_globs")
	if err != nil {
		return ErrorResult("grep: " + err.Error())
	}
	excludeGlobs, err := grepStringSliceArg(args, "exclude_globs")
	if err != nil {
		return ErrorResult("grep: " + err.Error())
	}

	contextLines, err := getInt64Arg(args, "context_lines", 0)
	if err != nil {
		return ErrorResult("grep: " + err.Error())
	}
	maxMatches, err := getInt64Arg(args, "max_matches", 0)
	if err != nil {
		return ErrorResult("grep: " + err.Error())
	}
	maxMatchesPerFile, err := getInt64Arg(args, "max_matches_per_file", 0)
	if err != nil {
		return ErrorResult("grep: " + err.Error())
	}

	if t.agentHome == "" {
		// Only reachable for the metadata-catalog instance (constructed with
		// an empty home, mirroring list_mounts/read_file's own guard) — never
		// executed against a real turn. Fail closed rather than resolving
		// against a relative/empty path.
		return ErrorResult("grep: no workspace configured")
	}

	policy, err := ResolveTurnFSPolicy(ctx, t.agentHome, t.restrict)
	if err != nil {
		return ErrorResult(fmt.Sprintf("grep: failed to resolve filesystem policy: %v", err))
	}

	roots, closeRoots, err := t.grepRoots(ctx, policy, scope)
	defer closeRoots()
	if err != nil {
		return ErrorResult(fmt.Sprintf("grep: %v", err))
	}

	acqCtx, cancel := context.WithTimeout(ctx, grepBusyWait)
	defer cancel()
	if !filegrep.TryAcquire(acqCtx) {
		logger.WarnCF("tool", "grep: search engine busy, refusing after wait", map[string]any{
			"agent_id": ToolAgentID(ctx), "wait": grepBusyWait.String(),
		})
		return ErrorResult(
			"grep: search engine is busy — 2 concurrent searches (from this tool and/or the Library " +
				"search bar) are already running and none freed up within 2s; wait a moment and retry, " +
				"or narrow with `path`/`include_globs`/`exclude_globs` so your next search finishes faster",
		)
	}
	defer filegrep.Release()

	result, searchErr := filegrep.Search(ctx, roots, filegrep.Options{
		Query:        pattern,
		Regex:        regexFlag,
		Case:         caseMode,
		IncludeGlobs: includeGlobs,
		ExcludeGlobs: excludeGlobs,
		ContextLines: int(contextLines),
		Limits: filegrep.Limits{
			Matches:        int(maxMatches),
			MatchesPerFile: int(maxMatchesPerFile),
		},
	})
	if searchErr != nil {
		// filegrep.Search's ONLY error return is a bad pattern (regex
		// compile failure, MV-1's 400 case) — every other outcome, including
		// every budget stop, is expressed in Result itself.
		return ErrorResult(fmt.Sprintf("grep: invalid pattern: %v", searchErr))
	}

	rendered := renderGrepResult(pattern, regexFlag, caseMode, result)
	rendered = grepCapOutput(rendered, result.Truncated)
	return NewToolResult(rendered)
}

// grepRoots resolves the fs.FS roots this call is confined to: the calling
// agent's own effective working directory (policy.WorkDir — its fixed home,
// or the per-turn Workspace re-root when it is a CoreTeam member) plus every
// mount on that SAME workspace (FR-020). It never consults an argument named
// workspace_id — there is no such argument — and it never widens beyond
// policy.WorkDir + that workspace's mounts regardless of policy.Scope: a
// Scope of FSScopeUnrestricted governs whether OTHER tools may resolve an
// argument path outside their working directory, a question grep's `path`
// argument never asks (validateGrepScope refuses ".." outright, and
// (*os.Root).OpenRoot refuses an escape at the syscall level even if it
// somehow reached one).
//
// # Resolving which workspace's mounts apply
//
// Mirrors ResolveTurnFSPolicy's own mount resolution (turn-carried
// ToolWorkspaceID, else workspace.FindForAgentPreferring) rather than
// calling through it, for the same reason ListMountsTool
// (pkg/tools/list_mounts.go) duplicates it locally: policy.AllowedRoots
// already carries the resolved host paths, but discards their NAMES, and a
// filegrep.Root needs a Name to prefix every reported hit with (matching how
// the Library addresses a mounted entry: "<mount>/<rel>"). Re-deriving the
// same workspace id the same way — rather than reusing policy.AllowedRoots
// directly — keeps this in lock-step with every other mount-consuming tool;
// see ResolveTurnFSPolicy's own doc comment for why the two resolutions must
// never disagree.
//
// The entries themselves come from workspace.LoadMounts, which is the
// VALIDATING reader: loadMountStore runs Mount.Validate over every entry and
// drops the failures with a WARN before returning any of them, so a
// hand-edited or partially-migrated record cannot contribute a search root
// here any more than it can contribute a write grant through
// workspace.AllowedMountRoots (itself LoadMounts plus a repeat of that same
// check). AllowedMountRoots is not used here only because it discards the
// mount NAMES a filegrep.Root needs. That the two readers return the same set
// is asserted in grep_mountvalidation_test.go, not assumed.
//
// # scope
//
// "" searches the whole workspace: the root plus every mount, each its own
// filegrep.Root. A non-empty scope opens ONE further-confined os.Root via
// (*os.Root).OpenRoot — pruning the walk itself, not just filtering after
// the fact — and returns a single Root whose Name is the resolved prefix, so
// a hit's reported path is still the full workspace-relative address no
// matter how deep the scope reaches. scope's first path segment is matched
// against the workspace's mount names FIRST (an exact segment match, never a
// prefix of a longer name); anything else is resolved as a subdirectory of
// the agent's own root.
//
// # A broken mount is never silently dropped
//
// A mount whose host folder cannot be opened right now (unplugged drive,
// renamed folder) still contributes a Root — backed by unreachableRootFS,
// which fails filegrep.Search's own fs.Stat(root.FS, ".") check and turns
// into Result.Truncated + filegrep.ReasonRootLost (FR-021) through that
// single existing code path, rather than a quiet reduction in coverage or a
// second, tool-local notion of "root lost".
//
// # The secret set is subtracted from every root
//
// An os.Root confines the walk to one host directory; it says nothing about
// WHICH files inside it an agent may see. Every root handed to the engine is
// therefore wrapped in carveOutFS, which applies fspolicy.IsCarveOut — the
// same predicate ResolvePath consults before it looks at a mount at all — to
// every entry the engine can list, name-match, or open. Without it a mount on
// an ANCESTOR of $OMNIPUS_HOME (warn-and-allow per workspace.CheckMountTarget,
// which hard-refuses only a target inside $OMNIPUS_HOME) makes credentials.json,
// master.key, cli.token, the config backups and system/audit.jsonl greppable —
// every one of them refused to read_file on the same turn, and every one of
// them covered by the warning CheckMountTarget prints when the operator creates
// that mount ("the installation's own secrets remain protected independently of
// this mount").
func (t *GrepTool) grepRoots(ctx context.Context, policy fspolicy.FSPolicy, scope string) ([]filegrep.Root, func(), error) {
	var opened []*os.Root
	closeAll := func() {
		for _, r := range opened {
			if cerr := r.Close(); cerr != nil {
				logger.WarnCF("tool", "grep: closing a confined root failed", map[string]any{"error": cerr.Error()})
			}
		}
	}

	home := config.OmnipusHomeDir()
	mountWorkspaceID := ToolWorkspaceID(ctx)
	if mountWorkspaceID == "" {
		if wsID, found := workspace.FindForAgentPreferring(home, ToolAgentID(ctx), ""); found {
			mountWorkspaceID = wsID
		}
	}
	var mounts []workspace.Mount
	if mountWorkspaceID != "" {
		if loaded, ok := workspace.LoadMounts(home, mountWorkspaceID); ok {
			mounts = loaded
		}
	}

	if scope != "" {
		if idx, rest, matched := splitGrepScopeMount(scope, mounts); matched {
			m := mounts[idx]
			var root filegrep.Root
			mr, mErr := os.OpenRoot(m.HostPath)
			if mErr != nil {
				// FR-021: a dead mount is root_lost, not a request error —
				// same unreachableRootFS carrier the default full-workspace
				// search uses below for the identical failure.
				root = filegrep.Root{Name: m.Name, FS: unreachableRootFS{err: mErr}}
			} else {
				opened = append(opened, mr)
				if rest == "" {
					root = filegrep.Root{Name: m.Name, FS: guardCarveOuts(m.HostPath, mr.FS(), policy)}
				} else {
					r, rErr := t.resolveScopedRoot(mr, m.HostPath, rest, m.Name, fmt.Sprintf("mount %q", m.Name), policy, &opened)
					if rErr != nil {
						return nil, closeAll, rErr
					}
					root = r
				}
			}
			return []filegrep.Root{root}, closeAll, nil
		}

		wr, wErr := os.OpenRoot(policy.WorkDir)
		if wErr != nil {
			return nil, closeAll, fmt.Errorf("cannot open your workspace root: %w", wErr)
		}
		opened = append(opened, wr)
		root, rErr := t.resolveScopedRoot(wr, policy.WorkDir, scope, "", "your workspace", policy, &opened)
		if rErr != nil {
			return nil, closeAll, rErr
		}
		return []filegrep.Root{root}, closeAll, nil
	}

	wr, wErr := os.OpenRoot(policy.WorkDir)
	if wErr != nil {
		return nil, closeAll, fmt.Errorf("cannot open your workspace root: %w", wErr)
	}
	opened = append(opened, wr)
	roots := []filegrep.Root{{Name: "", FS: guardCarveOuts(policy.WorkDir, wr.FS(), policy)}}
	for _, m := range mounts {
		mr, mErr := os.OpenRoot(m.HostPath)
		if mErr != nil {
			roots = append(roots, filegrep.Root{Name: m.Name, FS: unreachableRootFS{err: mErr}})
			continue
		}
		opened = append(opened, mr)
		roots = append(roots, filegrep.Root{Name: m.Name, FS: guardCarveOuts(m.HostPath, mr.FS(), policy)})
	}
	return roots, closeAll, nil
}

// resolveScopedRoot resolves subPath (relative to container, whose real host
// directory is containerHostPath) into exactly one filegrep.Root, routing on
// what subPath ACTUALLY IS rather than assuming it names a directory
// (DEFECT-G1, field report): (*os.Root).OpenRoot only ever opens a
// directory, and calling it on an existing FILE fails with an opaque
// platform-specific error (on macOS/Linux, a bare "not a directory" that
// is neither fs.ErrNotExist nor syscall.ENOTDIR by errors.Is — verified
// experimentally, not assumed) — which the old code wrapped as "not found
// in your workspace" regardless of what the real problem was. An agent who
// had just confirmed the file existed with `ls` was told, three calls
// running, that it did not.
//
// Stat'ing subPath FIRST and branching on what it reports removes the
// ambiguity at the source: OpenRoot is now only ever called on something
// already confirmed to be a directory, so its own error, if any, means a
// genuine race (e.g. the directory was replaced between the Stat and the
// Open) rather than a routing mistake.
//
//   - A directory keeps exactly the prior behavior: a further-confined
//     os.Root, with the ancestor .gitignore/.ignore layers above it
//     preserved (F8).
//   - A regular file becomes a SINGLE-FILE search: the file's PARENT
//     directory is opened as the confined root (os.Root can only confine a
//     directory), and singleEntryFS narrows that root's own listing to
//     exactly that one child. Every other engine rule that would apply to
//     the file if a directory walk had discovered it — hidden-dot pruning,
//     .gitignore/.ignore (the walk's own layer for the parent directory,
//     plus these same preserved ancestor layers above IT), and
//     include_globs/exclude_globs — still runs against that one entry
//     exactly as it would during an ordinary walk. A caller who ALSO
//     supplied include_globs/exclude_globs therefore gets the natural AND:
//     the named file must also pass those globs to produce a hit, not a
//     second, competing notion of "the scope". The carve-out guard
//     (guardCarveOuts) wraps this exactly as it wraps a directory scope, so
//     a carved-out secret named directly as `path` is still refused — see
//     grep_carveout_test.go's file-scope case.
//   - Anything else (a socket, device, FIFO, or a symlink loop that somehow
//     resolves to neither) is refused with a message naming what it
//     actually is, never a claim that it does not exist.
//
// namePrefix is the reported-path prefix already established for container
// ("" for the workspace root, the mount name for a mount); label names
// container in a caller-facing sentence ("your workspace", `mount "x"`).
// *opened accumulates every os.Root this opens so grepRoots' single
// closeAll can close them all on the caller's defer.
func (t *GrepTool) resolveScopedRoot(container *os.Root, containerHostPath, subPath, namePrefix, label string, policy fspolicy.FSPolicy, opened *[]*os.Root) (filegrep.Root, error) {
	info, statErr := container.Stat(subPath)
	if statErr != nil {
		return filegrep.Root{}, grepScopeStatError(subPath, label, statErr)
	}

	switch {
	case info.IsDir():
		ancestor := filegrep.LoadAncestorIgnore(container.FS(), subPath)
		sub, sErr := container.OpenRoot(subPath)
		if sErr != nil {
			return filegrep.Root{}, fmt.Errorf("path %q in %s could not be opened as a directory: %w", subPath, label, sErr)
		}
		*opened = append(*opened, sub)
		anchor := filepath.Join(containerHostPath, filepath.FromSlash(subPath))
		name := subPath
		if namePrefix != "" {
			name = namePrefix + "/" + subPath
		}
		return filegrep.Root{
			Name: name, FS: guardCarveOuts(anchor, sub.FS(), policy),
			ScopePrefix: subPath, AncestorIgnore: ancestor,
		}, nil

	case info.Mode().IsRegular():
		parentRel := path.Dir(subPath)
		base := path.Base(subPath)

		parent := container
		if parentRel != "." {
			p, pErr := container.OpenRoot(parentRel)
			if pErr != nil {
				return filegrep.Root{}, fmt.Errorf("path %q in %s could not be opened: %w", subPath, label, pErr)
			}
			*opened = append(*opened, p)
			parent = p
		}

		var ancestor []filegrep.AncestorIgnoreLayer
		scopePrefix := ""
		name := namePrefix
		if parentRel != "." {
			ancestor = filegrep.LoadAncestorIgnore(container.FS(), parentRel)
			scopePrefix = parentRel
			if namePrefix != "" {
				name = namePrefix + "/" + parentRel
			} else {
				name = parentRel
			}
		}

		anchor := filepath.Join(containerHostPath, filepath.FromSlash(parentRel))
		fsys := guardCarveOuts(anchor, singleEntryFS{fsys: parent.FS(), name: base}, policy)
		return filegrep.Root{
			Name: name, FS: fsys, ScopePrefix: scopePrefix, AncestorIgnore: ancestor,
		}, nil

	default:
		return filegrep.Root{}, fmt.Errorf(
			"path %q in %s is %s, not a directory or a regular file — grep can only search directories and regular files",
			subPath, label, describeFileKind(info.Mode()))
	}
}

// describeFileKind renders a Stat'd file's type as a short, human-readable
// phrase for an error message — never the raw fs.FileMode.Type().String()
// (e.g. "p---------"), which is accurate but reads as noise to an agent
// deciding what to do next.
func describeFileKind(mode fs.FileMode) string {
	switch {
	case mode&fs.ModeSymlink != 0:
		return "a symlink"
	case mode&fs.ModeNamedPipe != 0:
		return "a named pipe (FIFO)"
	case mode&fs.ModeSocket != 0:
		return "a socket"
	case mode&fs.ModeCharDevice != 0:
		return "a character device"
	case mode&fs.ModeDevice != 0:
		return "a device file"
	case mode&fs.ModeIrregular != 0:
		return "a file of an unrecognized kind"
	default:
		return fmt.Sprintf("a file of an unrecognized kind (mode %s)", mode)
	}
}

// grepScopeStatError turns a failed Stat on a `path` argument into a message
// that states what is actually true (DEFECT-G1's (b) requirement): the old
// code's single blanket "not found" sentence fired even when the real
// problem was "found, but it's a file" or "found, but permission was
// denied" or "the path escapes the workspace through a symlink" — every one
// of those produced the identical, sometimes false, wording. This routes on
// the real error instead.
func grepScopeStatError(subPath, label string, err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("path %q does not exist in %s", subPath, label)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("path %q exists in %s but could not be read (permission denied): %w", subPath, label, err)
	default:
		// Neither "not found" nor "permission denied" — e.g. a path
		// component treats a file as a directory, or a symlink escapes the
		// confined root. Surface the engine's own reason via %w rather than
		// guessing; it is truthful even when it isn't maximally specific.
		return fmt.Errorf("path %q in %s could not be resolved: %w", subPath, label, err)
	}
}

// singleEntryFS narrows a directory FS to expose exactly one named entry —
// the file grep's `path` argument resolved to when it names a regular file
// rather than a directory (see resolveScopedRoot). os.Root can only be
// opened on a directory, so a file-scoped search opens the file's PARENT as
// the confined root and uses this wrapper to restrict what that root's own
// listing (and therefore the walk) can see to that one child; the walk
// itself still applies every other per-entry rule (hidden-dot pruning,
// .gitignore/.ignore, include_globs/exclude_globs) to the one entry it is
// shown, exactly as it would if that file had been discovered during an
// ordinary directory walk.
type singleEntryFS struct {
	fsys fs.FS
	name string // the one visible entry's base name
}

func (s singleEntryFS) Open(name string) (fs.File, error) {
	if name == "." {
		return s.fsys.Open(name)
	}
	if name != s.name {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return s.fsys.Open(name)
}

func (s singleEntryFS) Stat(name string) (fs.FileInfo, error) {
	if name == "." {
		return fs.Stat(s.fsys, name)
	}
	if name != s.name {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	return fs.Stat(s.fsys, name)
}

func (s singleEntryFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name != "." && name != "" {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	entries, err := fs.ReadDir(s.fsys, ".")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.Name() == s.name {
			return []fs.DirEntry{e}, nil
		}
	}
	return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
}

// guardCarveOuts wraps an os.Root-backed fs.FS so that no path the secret
// carve-out refuses (fspolicy.IsCarveOut) can be listed, name-matched, or
// opened through it. hostAbs is the host directory fsys is anchored at; it is
// resolved through symlinks here because IsCarveOut judges containment by
// filesystem identity and needs the real location (an os.Root may itself have
// been opened through a symlinked component that stayed inside the root).
//
// The anchor is resolved by resolveRealpathUnderWorkDir — this package's own
// sanctioned resolver, the same one ResolvePath uses for both its work dir and
// its candidate path (FR-034 routes every path resolution in pkg/tools through
// that one function rather than a locally glued filepath.EvalSymlinks).
//
// A hostAbs that cannot be resolved yields an unreachableRootFS rather than an
// unguarded FS: without a trustworthy anchor there is no way to name the files
// this FS would expose, and an unnameable file cannot be judged. That surfaces
// through filegrep's existing ReasonRootLost path (see grepRoots' "A broken
// mount is never silently dropped"), so the coverage loss is stated to the
// caller rather than silently taken.
//
// Every question the guard asks — both the deny decision and the decision to
// run the exhaustive listing filter — is answered from policy alone. There is
// no separately-sourced $OMNIPUS_HOME parameter, because two sources for one
// identity can disagree: see carveOutRootHomes.
func guardCarveOuts(hostAbs string, fsys fs.FS, policy fspolicy.FSPolicy) fs.FS {
	resolved, err := resolveRealpathUnderWorkDir(hostAbs, "")
	if err != nil {
		logger.WarnCF("tool", "grep: cannot resolve a search root — refusing to search it unguarded", map[string]any{
			"path": hostAbs, "error": err.Error(),
		})
		return unreachableRootFS{err: err}
	}
	return carveOutFS{
		fsys:            fsys,
		root:            resolved,
		policy:          policy,
		carveOutHomes:   carveOutRootHomes(policy),
		hasCarveOutRoot: len(policy.CarveOuts) > 0,
	}
}

// GuardCarveOuts is guardCarveOuts for callers outside this package.
//
// It exists because pkg/filegrep has TWO consumers — this package's `grep`
// tool and the gateway's Library file-search endpoint
// (pkg/gateway/rest_library_files_search.go) — and the engine's own contract
// is that "confinement is the CALLER's job". Both callers face the identical
// installation shape: a mount on an ancestor of $OMNIPUS_HOME, which
// workspace.CheckMountTarget warns about rather than refusing, puts
// credentials.json / master.key / cli.token / auth.json / the config backups /
// system/audit.jsonl inside a searchable root.
//
// It is EXPORTED rather than reimplemented next to the second caller on
// purpose: two independent deny implementations that can disagree is a worse
// outcome than the bug either of them was written to fix. pkg/gateway already
// imports pkg/tools, so this needs no new dependency edge and creates no cycle
// (pkg/tools imports pkg/gateway/middleware, a different package).
//
// pkg/fspolicy would be the more natural home on merit — it owns IsCarveOut,
// the predicate this delegates to — but that package is deliberately a
// stdlib-only leaf (its own doc comment: importing pkg/tools or pkg/sandbox
// there would be an import cycle), and this wrapper needs this package's
// realpath resolver and the logger. pkg/tools is the closest package that can
// hold it without breaking that constraint.
//
// hostAbs is the host directory fsys is anchored at; policy supplies both the
// carve-out roots and the WorkDir whose own-tree exception keeps a caller's
// own files readable. See guardCarveOuts and carveOutFS for the full contract.
func GuardCarveOuts(hostAbs string, fsys fs.FS, policy fspolicy.FSPolicy) fs.FS {
	return guardCarveOuts(hostAbs, fsys, policy)
}

// carveOutRootHomes returns the directories that DIRECTLY CONTAIN policy's own
// carve-out roots, stat'd for identity comparison — in every production policy
// that is exactly one directory, $OMNIPUS_HOME.
//
// The identity is derived from the carve-out entries the deny check itself
// consults, never from a separately-sourced home path. Those were two values
// until this function existed: the deny decision judged against
// policy.CarveOuts (built by EffectiveFSPolicy from its own resolved root)
// while the decision to RUN the exhaustive listing filter judged against
// config.OmnipusHomeDir(). A policy whose CarveOuts came from a different root
// — a re-rooted policy, a test-constructed one, a future multi-home shape —
// made the two disagree, and the filter then never ran where the carve-out
// roots actually live: master.key, credentials.json, cli.token and auth.json
// became listable and NAME-matchable. Open() still refused their CONTENT, so
// that failure was invisible to any content-leak assertion.
//
// filepath.Dir is the correct derivation because every carve-out root is a
// direct child of $OMNIPUS_HOME (fspolicy's appCarveOutSecretPaths, and the
// backup-prefix rule likewise covers only files sitting directly in it).
// fspolicy.CoversSecretBackup already anchors on the carve-out roots this same
// way, for this same reason — this is that precedent applied to the second
// place the question is asked, not a new rule.
//
// Returns nil when any parent cannot be stat'd, which makes holdsCarveOutRoots
// answer "yes" for every directory and take the exhaustive path — the
// conservative direction.
func carveOutRootHomes(policy fspolicy.FSPolicy) []os.FileInfo {
	seen := make(map[string]struct{}, len(policy.CarveOuts))
	out := make([]os.FileInfo, 0, 1)
	for _, root := range policy.CarveOuts {
		parent := filepath.Dir(filepath.Clean(root))
		if _, dup := seen[parent]; dup {
			continue
		}
		seen[parent] = struct{}{}
		info, err := os.Stat(parent)
		if err != nil {
			return nil
		}
		out = append(out, info)
	}
	return out
}

// carveOutFS subtracts the secret carve-out from one confined root.
//
// The engine reaches a file through exactly three fs calls — fs.Stat(".") to
// prove the root opens, fs.ReadDir to enumerate a directory, and Open to read
// a file's content (also how fs.ReadFile loads a .gitignore) — and all three
// are guarded here. The ReadDir guard depends on this type satisfying
// fs.ReadDirFS, so fs.ReadDir dispatches to it rather than falling back to
// Open + ReadDirFile; if a caller ever enumerates a directory through the
// Open path instead, that listing is unfiltered, and only Open's own check
// (below) still applies to what it then reads.
//
// # Where each half of the guarantee is enforced
//
// Open is the CONTENT boundary and is checked unconditionally, for every byte
// the engine reads: full fspolicy.IsCarveOut parity with ResolvePath, alias
// leg included. It doubles as the I/O-time re-check ResolvePath performs at
// its own I/O boundary, covering a rename that moves a secret into an
// already-listed directory mid-walk.
//
// ReadDir is the EXISTENCE boundary: filtering here prunes the walk, so a
// denied directory is never descended into and a denied file is never
// name-matched (a name hit discloses that a file exists and its exact path,
// which is precisely what an agent probing for $OMNIPUS_HOME wants).
//
// # Why ReadDir does not run the full check on every entry
//
// IsCarveOut judges containment by filesystem identity, so it stats the
// candidate's whole ancestor chain and each carve-out root — measured at
// 120-200us per call on APFS. Running it per entry on the (single-threaded)
// walker adds several seconds to a 50,000-file search, which is the engine's
// own file cap: an exhaustive-by-default guard would convert searches that
// complete today into deadline truncations.
//
// It is not needed per entry, because of what the carve-out roots ARE. Every
// one of them (fspolicy's appCarveOutSecretPaths) is a DIRECT CHILD of
// $OMNIPUS_HOME, and the backup-prefix rule (CoversSecretBackup) likewise
// only covers files sitting directly in $OMNIPUS_HOME. So for a directory D
// that is not $OMNIPUS_HOME itself and whose own check already passed, a
// child C = D/leaf cannot newly become denied by a path rule:
//
//   - C == some carve-out root R would make D == R's parent == $OMNIPUS_HOME,
//     which is excluded;
//   - C strictly under some R implies D is under-or-equal R, so R already
//     covered D — and the own-tree exception that cleared D clears C too,
//     since C lies inside D and therefore inside the same work dir;
//   - the backup-prefix rule needs C directly in $OMNIPUS_HOME, i.e. D ==
//     $OMNIPUS_HOME, excluded.
//
// A directory that IS $OMNIPUS_HOME (reachable when a mount covers an
// ancestor of it — warn-and-allow, workspace.CheckMountTarget) takes the
// exhaustive path, which is what suppresses master.key, credentials.json and
// every other secret entry by name.
//
// The one rule this skips is IsCarveOut's hard-link ALIAS leg, which is
// path-independent: a file anywhere may share an inode with a file inside a
// directory-shaped carve-out. That leg can therefore still admit an entry's
// NAME here — and only its name, which is a name the agent chose for a file
// in its own tree and discloses nothing about the aliased secret. The alias's
// CONTENT stays refused, because Open runs the full check.
type carveOutFS struct {
	fsys   fs.FS
	root   string // absolute, symlink-resolved host path fsys is anchored at
	policy fspolicy.FSPolicy
	// carveOutHomes are the directories directly containing policy.CarveOuts'
	// own roots, stat'd for identity comparison — $OMNIPUS_HOME in every
	// production policy. Empty means "could not be answered"; with
	// hasCarveOutRoot set that forces the exhaustive path everywhere.
	carveOutHomes []os.FileInfo
	// hasCarveOutRoot records whether policy.CarveOuts holds anything at all,
	// which is what distinguishes "nothing to suppress" from "cannot tell
	// where the roots are".
	hasCarveOutRoot bool
}

// abs maps one slash-separated path relative to the root onto its host path.
// "." and "" name the root itself.
func (c carveOutFS) abs(name string) string {
	if name == "." || name == "" {
		return c.root
	}
	return filepath.Join(c.root, filepath.FromSlash(name))
}

// denied is the full check — the same predicate, with the same policy, that
// ResolvePath applies before it looks at a mount at all.
func (c carveOutFS) denied(name string) bool {
	return fspolicy.IsCarveOut(c.abs(name), c.policy)
}

func (c carveOutFS) refusal(op, name string) error {
	return &fs.PathError{Op: op, Path: name, Err: fs.ErrPermission}
}

func (c carveOutFS) Open(name string) (fs.File, error) {
	if c.denied(name) {
		return nil, c.refusal("open", name)
	}
	return c.fsys.Open(name)
}

func (c carveOutFS) Stat(name string) (fs.FileInfo, error) {
	if c.denied(name) {
		return nil, c.refusal("stat", name)
	}
	return fs.Stat(c.fsys, name)
}

func (c carveOutFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if c.denied(name) {
		return nil, c.refusal("readdir", name)
	}
	entries, err := fs.ReadDir(c.fsys, name)
	if err != nil {
		return nil, err
	}
	if !c.holdsCarveOutRoots(name) {
		return entries, nil
	}
	kept := make([]fs.DirEntry, 0, len(entries))
	for _, e := range entries {
		child := e.Name()
		if name != "." && name != "" {
			child = name + "/" + e.Name()
		}
		if c.denied(child) {
			continue
		}
		kept = append(kept, e)
	}
	return kept, nil
}

// holdsCarveOutRoots reports whether this directory directly contains any of
// policy.CarveOuts' own roots — the only directory whose entries can be
// carve-out roots or secret backups (see carveOutFS's doc comment). In every
// production policy that is exactly $OMNIPUS_HOME.
//
// The directories it compares against are derived from policy.CarveOuts, the
// same list the deny decision consults (carveOutRootHomes), so the existence
// filter and the deny check can no longer disagree about where the secrets
// live.
//
// Compared by filesystem identity, never by bytes: on a case-insensitive
// volume $OMNIPUS_HOME and $OMNIPUS_home are one directory and two strings,
// and it is the deny side that must not be fooled. Anything unanswerable — an
// unstattable directory, carve-out roots whose parents could not be stat'd —
// returns true, so the exhaustive check runs.
//
// A policy with NO carve-out roots returns false rather than true: with an
// empty CarveOuts list, denied() cannot refuse any entry (every leg of
// fspolicy.IsCarveOut — the backup prefix rule, the hard-link alias leg, and
// the per-root loop — iterates that same empty list), so the exhaustive filter
// would stat every entry's whole ancestor chain to reach a foregone "keep".
func (c carveOutFS) holdsCarveOutRoots(name string) bool {
	if !c.hasCarveOutRoot {
		return false
	}
	if len(c.carveOutHomes) == 0 {
		return true
	}
	info, err := os.Stat(c.abs(name))
	if err != nil {
		return true
	}
	for _, home := range c.carveOutHomes {
		if os.SameFile(info, home) {
			return true
		}
	}
	return false
}

// splitGrepScopeMount reports whether scope's first path segment names one
// of mounts exactly (never a prefix of a longer name), returning that
// mount's index and the remaining subpath (possibly "").
func splitGrepScopeMount(scope string, mounts []workspace.Mount) (idx int, rest string, matched bool) {
	first := scope
	rest = ""
	if i := strings.IndexByte(scope, '/'); i >= 0 {
		first = scope[:i]
		rest = scope[i+1:]
	}
	for i, m := range mounts {
		if m.Name == first {
			return i, rest, true
		}
	}
	return -1, "", false
}

// validateGrepScope rejects a `path` argument before any I/O is attempted,
// so a caller gets a clear reason rather than a raw *fs.PathError.
// (*os.Root).OpenRoot refuses the same escapes at the syscall level
// regardless — this is defense-in-depth for a better message, not the sole
// guard.
func validateGrepScope(scope string) error {
	if scope == "" {
		return nil
	}
	if strings.IndexByte(scope, 0) != -1 {
		return fmt.Errorf("`path` contains an embedded NUL byte")
	}
	if strings.HasPrefix(scope, "/") {
		return fmt.Errorf("`path` must be relative to your workspace, not absolute")
	}
	for _, seg := range strings.Split(scope, "/") {
		if seg == ".." {
			return fmt.Errorf("`path` may not contain \"..\"")
		}
	}
	return nil
}

// unreachableRootFS stands in for a Root whose real folder could not be
// opened. See grepRoots' doc comment for why this exists instead of
// dropping the root.
type unreachableRootFS struct{ err error }

func (u unreachableRootFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: u.err}
}

// grepCaseArg decodes the optional `case` argument, defaulting to smart-case
// (MV-13) and rejecting anything other than the three named modes.
func grepCaseArg(args map[string]any) (filegrep.CaseMode, error) {
	raw, ok := args["case"]
	if !ok || raw == nil {
		return filegrep.CaseSmart, nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("`case` must be a string")
	}
	switch filegrep.CaseMode(s) {
	case filegrep.CaseSmart, filegrep.CaseSensitive, filegrep.CaseInsensitive:
		return filegrep.CaseMode(s), nil
	default:
		return "", fmt.Errorf("`case` must be one of %q, %q, %q (got %q)",
			filegrep.CaseSmart, filegrep.CaseSensitive, filegrep.CaseInsensitive, s)
	}
}

// grepStringArg extracts an optional string argument, "" when absent.
func grepStringArg(args map[string]any, key string) (string, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("`%s` must be a string", key)
	}
	return s, nil
}

// grepStringSliceArg extracts an optional array-of-strings argument (also
// accepting a single bare string, since models occasionally send one glob
// unwrapped). nil/absent stays nil — filegrep treats a nil glob list as "no
// filter", never as "match nothing".
func grepStringSliceArg(args map[string]any, key string) ([]string, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil, nil
	}
	if s, isStr := raw.(string); isStr {
		if s == "" {
			return nil, nil
		}
		return []string{s}, nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("`%s` must be an array of strings", key)
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("`%s` must be an array of strings", key)
		}
		if s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// renderGrepResult turns a filegrep.Result into the compact, agent-facing
// text this tool returns (mission: "structured, agent-friendly — a compact
// text rendering"). Content-match lines are rendered "path:line: excerpt",
// context lines indented and marked with a trailing "-" (grep -C
// convention) rather than ":", and a name match (Hit.Line == 0, MV-14)
// renders without a line number. Context line numbers are reconstructed
// from Hit.Line and the context slices' documented ordering (filegrep.go:
// ContextBefore oldest-first immediately preceding the match,
// ContextAfter nearest-first immediately following it) — filegrep itself
// does not stamp a line number on each context entry.
func renderGrepResult(pattern string, regexFlag bool, caseMode filegrep.CaseMode, res filegrep.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "grep %q", pattern)
	if regexFlag {
		b.WriteString(" (regex)")
	}
	fmt.Fprintf(&b, " case=%s\n", caseMode)

	// The match count AND the truncation verdict are stated here, in the
	// first few lines, DELIBERATELY — not only in the stats footer after
	// every hit. A heavily truncated result can carry hundreds of hits
	// (each up to ~512 bytes of excerpt), so the footer is exactly what
	// grepCapOutput's 64,000-char cap slices away first; putting the
	// authoritative truncated/reason verdict here instead means it survives
	// that cut every time (MV-3a: the engine's reason must remain visible to
	// the caller, not merely be preserved somewhere in a body that gets cut
	// before it is ever read).
	fmt.Fprintf(&b, "%d match(es)", len(res.Hits))
	if res.Truncated {
		fmt.Fprintf(&b, " — truncated: true (reason: %s) — %s", res.TruncatedReason, grepNarrowingHint(res.TruncatedReason))
	} else {
		b.WriteString(" — truncated: false")
	}
	b.WriteString("\n\n")

	if len(res.Hits) == 0 {
		b.WriteString("(no hits)\n")
		// OBS-G1: a zero-hit result caused by include_globs/exclude_globs
		// filtering out every candidate is otherwise indistinguishable from
		// "the term genuinely is not there" — the stats footer below states
		// the count either way, but that footer is exactly what
		// grepCapOutput's cap slices away first on a large result, and on a
		// SMALL (here, zero-hit) result a reader has no reason to scan all
		// the way down to it. Stating the cause here, right next to the
		// verdict it explains, makes the two outcomes look different instead
		// of identical.
		if res.Stats.FilesFilteredGlob > 0 {
			fmt.Fprintf(&b, "%d file(s)/director(ies) were excluded by include_globs/exclude_globs before any "+
				"match was attempted — this alone can produce a zero-hit result even when the term exists.",
				res.Stats.FilesFilteredGlob)
			// "Large relative to files visited" (spec wording): more got
			// filtered out than survived to be searched — the shape a
			// mis-anchored bare filename glob produces (e.g. "spike.txt"
			// instead of "**/spike.txt"), as opposed to a glob that
			// legitimately narrowed a big tree down to a few files.
			if res.Stats.FilesFilteredGlob > res.Stats.FilesVisited {
				b.WriteString(" doublestar patterns match the WHOLE relative path, not just a filename: a bare " +
					"\"name.ext\" only matches a file sitting AT the search root, never one in a subdirectory — " +
					"use \"**/name.ext\" to match at any depth.")
			}
			b.WriteString("\n")
		}
	} else {
		for _, h := range res.Hits {
			if h.Kind == filegrep.KindName {
				fmt.Fprintf(&b, "%s  (name match)\n", h.Path)
				continue
			}
			for i, before := range h.ContextBefore {
				lineNo := h.Line - (len(h.ContextBefore) - i)
				fmt.Fprintf(&b, "%s-%d-  %s\n", h.Path, lineNo, before)
			}
			fmt.Fprintf(&b, "%s:%d:  %s\n", h.Path, h.Line, h.Excerpt)
			for i, after := range h.ContextAfter {
				lineNo := h.Line + i + 1
				fmt.Fprintf(&b, "%s-%d-  %s\n", h.Path, lineNo, after)
			}
			if len(h.ContextBefore) > 0 || len(h.ContextAfter) > 0 {
				b.WriteString("--\n")
			}
		}
	}

	b.WriteString("\n")
	fmt.Fprintf(&b, "stats: %d file(s) visited, %d byte(s) scanned", res.Stats.FilesVisited, res.Stats.BytesScanned)
	if res.Stats.FilesSkippedProblems > 0 {
		fmt.Fprintf(&b, ", %d unreadable/skipped", res.Stats.FilesSkippedProblems)
	}
	if res.Stats.FilesPrunedIgnored > 0 {
		fmt.Fprintf(&b, ", %d pruned (.gitignore/hidden/internal)", res.Stats.FilesPrunedIgnored)
	}
	if res.Stats.FilesSkippedFileCap > 0 {
		fmt.Fprintf(&b, ", %d file(s) beyond the per-file content cap", res.Stats.FilesSkippedFileCap)
	}
	if res.Stats.HitsCappedPerFile > 0 {
		fmt.Fprintf(&b, ", %d file(s) hit the per-file match cap", res.Stats.HitsCappedPerFile)
	}
	if res.Stats.FilesFilteredGlob > 0 {
		fmt.Fprintf(&b, ", %d file(s)/director(ies) excluded by include_globs/exclude_globs", res.Stats.FilesFilteredGlob)
	}
	b.WriteString("\n")
	// The truncated/reason verdict is already stated at the top (see above)
	// — it is not repeated here so there is exactly one place in the body
	// that can ever disagree with itself.
	return b.String()
}

// grepNarrowingHint gives a reason-specific "how to get an uncapped result
// next time" tip (Description's own promise, and part of the tool-cap
// marker's own hint below).
func grepNarrowingHint(reason filegrep.TruncatedReason) string {
	switch reason {
	case filegrep.ReasonMaxMatches:
		return "too many hits — narrow with `path`, `include_globs`/`exclude_globs`, or a more specific `pattern`"
	case filegrep.ReasonMaxFiles, filegrep.ReasonMaxBytes, filegrep.ReasonMaxDepth, filegrep.ReasonDeadline:
		return "the tree was larger than one search can cover — narrow with `path` or `include_globs`/`exclude_globs` to search fewer files"
	case filegrep.ReasonMaxOutput:
		return "results were larger than the output budget — narrow scope, lower `context_lines`, or lower `max_matches`/`max_matches_per_file`"
	case filegrep.ReasonRootLost:
		return "your workspace root or a mount became unreadable mid-search — check `list_mounts` and retry"
	default:
		return "narrow with `path`, `include_globs`/`exclude_globs`, or a lower `max_matches`"
	}
}

// grepCapOutput applies MV-12's 64,000-character tool serialization cap,
// AFTER the engine's own MV-3 truncation has already been rendered into the
// body above (MV-3a's layering rule). engineTruncated is true when
// Result.Truncated already carries a reason — in that case this cap, if it
// also fires, does NOT claim a second, different reason: it notes only that
// the rendering itself was additionally cut, leaving the engine's own
// stated reason (already in the body) as the authoritative one. Only when
// the engine did NOT truncate does a cap firing here get to say why on its
// own account: reason max_output.
func grepCapOutput(rendered string, engineTruncated bool) string {
	runes := []rune(rendered)
	if len(runes) <= config.DefaultBuiltinSuccessCap {
		return rendered
	}
	cut := string(runes[:config.DefaultBuiltinSuccessCap])
	reasonNote := "reason: max_output"
	if engineTruncated {
		reasonNote = "the engine's own truncation reason above still applies"
	}
	return cut + fmt.Sprintf(
		"\n... [output cut at %d characters; %s] — narrow with `path`, `include_globs`/`exclude_globs`, "+
			"a smaller `max_matches`/`max_matches_per_file`, or fewer `context_lines`\n",
		config.DefaultBuiltinSuccessCap, reasonNote,
	)
}
