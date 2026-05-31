// Package tools — metadata path guard.
//
// Implements the fail-closed guard for agents/<id>/(SOUL|HEARTBEAT|MEMORY|AGENT).md:
// any generic file tool (read_file, write_file, edit_file, append_file) that
// resolves to one of these paths is rejected with a structured error that
// suggests agent.read_metadata / agent.write_metadata instead.
//
// The guard is case-insensitive and handles agent-prefixed or misspelled
// filenames via a small Levenshtein fuzzy match over the four canonical names.
//
// Security property: the guard runs AFTER validatePathWithAllowPaths has
// normalised the path to an absolute form, so "SOUL.md", "./soul.md", and a
// full absolute path all trigger the same check.

package tools

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// canonicalMetadataNames maps the canonical key (lowercase) to the on-disk
// capitalised filename.  These are the four files that must only be read or
// written through agent.read_metadata / agent.write_metadata.
var canonicalMetadataNames = map[string]string{
	"soul":      "SOUL.md",
	"heartbeat": "HEARTBEAT.md",
	"memory":    "MEMORY.md",
	"agent":     "AGENT.md",
}

// metadataFileMatch reports whether absPath is one of the four canonical
// metadata files inside an agents/<id>/ directory tree.
//
// Returns:
//   - fileKey  — canonical key (soul/heartbeat/memory/agent), or ""
//   - agentID  — the agent ID segment from the path, or ""
//   - ok       — true when the path matched
func metadataFileMatch(absPath string) (fileKey, agentID string, ok bool) {
	cleaned := filepath.Clean(absPath)

	// We need at least .../agents/<id>/<name>.md — three path components after
	// splitting from the right.
	dir := filepath.Dir(cleaned)              // .../agents/<id>
	base := filepath.Base(cleaned)            // <name>.md
	agentsDir := filepath.Dir(dir)            // .../agents
	agentsDirBase := filepath.Base(agentsDir) // "agents"

	if !strings.EqualFold(agentsDirBase, "agents") {
		return "", "", false
	}

	id := filepath.Base(dir)
	if id == "" || id == "." || id == "/" || id == agentsDirBase {
		return "", "", false
	}

	// Case-insensitive match against the four canonical filenames.
	baseLower := strings.ToLower(base)
	for key, canonical := range canonicalMetadataNames {
		if strings.ToLower(canonical) == baseLower {
			return key, id, true
		}
	}

	return "", "", false
}

// levenshtein returns the edit distance between a and b (bounded at maxDist+1
// so callers can bail early). This is a plain O(len(a)*len(b)) DP; it is only
// called on short strings (≤20 chars) so allocating a 2-D slice is fine.
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	row := make([]int, lb+1)
	for j := range row {
		row[j] = j
	}
	for i := 1; i <= la; i++ {
		prev := row[0]
		row[0] = i
		for j := 1; j <= lb; j++ {
			old := row[j]
			if a[i-1] == b[j-1] {
				row[j] = prev
			} else {
				row[j] = min3(prev, row[j], row[j-1]) + 1
			}
			prev = old
		}
	}
	return row[lb]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// fuzzyMatchMetadataKey strips common agent-prefixes and misspellings from
// basename and returns the best-matching canonical key, or "" when no key
// is close enough (distance ≤ 3).
func fuzzyMatchMetadataKey(basename string) string {
	// Strip .md extension for comparison.
	name := strings.ToLower(strings.TrimSuffix(strings.ToLower(basename), ".md"))

	// Exact match wins.
	if _, ok := canonicalMetadataNames[name]; ok {
		return name
	}

	// Strip common prefixes before fuzzy matching: "agent-", "<id>-", etc.
	// Try after the last "-" separator if any.
	if idx := strings.LastIndex(name, "-"); idx >= 0 {
		suffix := name[idx+1:]
		if _, ok := canonicalMetadataNames[suffix]; ok {
			return suffix
		}
	}

	// Fuzzy match with Levenshtein distance ≤ 3.
	best, bestDist := "", 4
	for key := range canonicalMetadataNames {
		d := levenshtein(name, key)
		if d < bestDist {
			bestDist = d
			best = key
		}
	}
	if bestDist <= 3 {
		return best
	}
	return ""
}

// metadataGuardError returns a structured JSON error string that tells the
// agent to use agent.read_metadata / agent.write_metadata instead.
//
// op should be "read" or "write".
// absPath is the resolved absolute path that was blocked.
func metadataGuardError(absPath, op string) string {
	fileKey, agentID, ok := metadataFileMatch(absPath)
	if !ok {
		// Path was not matched by metadataFileMatch but we are being called
		// from a code path that already confirmed a match — fall back to the
		// base filename for the fuzzy suggestion.
		fileKey = fuzzyMatchMetadataKey(filepath.Base(absPath))
		agentID = "(unknown)"
	}

	var suggestion string
	if op == "read" {
		if fileKey != "" {
			suggestion = `use system.agent.read_metadata(file="` + fileKey + `")`
		} else {
			suggestion = "use system.agent.read_metadata(file=<soul|heartbeat|memory|agent>)"
		}
	} else {
		if fileKey != "" {
			suggestion = `use system.agent.write_metadata(file="` + fileKey + `", content=...)`
		} else {
			suggestion = "use system.agent.write_metadata(file=<soul|heartbeat|memory|agent>, content=...)"
		}
	}

	var canonical string
	if fileKey != "" {
		canonical = canonicalMetadataNames[fileKey]
	} else {
		canonical = filepath.Base(absPath)
	}

	msg := map[string]any{
		"error": map[string]any{
			"code":       "USE_METADATA_TOOL",
			"message":    "agents/" + agentID + "/" + canonical + " is managed by agent metadata tools",
			"suggestion": suggestion,
		},
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return `{"error":{"code":"USE_METADATA_TOOL","message":"metadata file is managed by agent metadata tools","suggestion":"` + suggestion + `"}}`
	}
	return string(b)
}
