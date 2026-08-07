// Package audit — auto-commit secrets scan (ADR-053 MIN-5).
//
// The Plan/Play engine auto-commits each member's write-set into the hidden
// go-git evidence repo at boundary points. A boundary commit is durable and
// tamper-evident by design — which means a secret that lands in a commit is
// PERSISTED in history even if the working-tree file is later deleted. MIN-5
// requires the commit path to scan for secrets BEFORE committing and to refuse
// (or force the caller to purge) when a registered credential appears.
//
// Two detection layers, both reused from existing machinery:
//
//   1. The credential store's sensitive-value REGISTRY. pkg/config's
//      Config.RegisterSensitiveValues records every resolved credential
//      plaintext and compiles them into a *strings.Replacer
//      (Config.SensitiveDataReplacer). The scanner runs content through that
//      replacer; if the output differs from the input, a registered credential
//      is present. This catches the exact secrets the operator provisioned,
//      regardless of their format.
//   2. The audit REDACTOR's format patterns (sk-…, ghp_…, JWTs, AWS keys, …).
//      This catches secrets the agent obtained at runtime and wrote to a file
//      without them ever being registered (e.g. an API response body).
//
// The scanner NEVER stores or logs the secret value itself — findings carry the
// class/label and location only.
//
// # Purge / GC procedure (SecretPurgeProcedure)
//
// If a secret slips into committed history despite the scan (e.g. an unusual
// format not covered, or a scan that ran in warn-only mode), the documented
// recovery is:
//
//  1. Stop taking new boundary commits into the affected work dir.
//  2. Rewrite history to excise the blob: with go-git, replay the branch
//     dropping/replacing the offending tree entry (or shell out to
//     `git filter-repo --path <file> --invert-paths`, or BFG). The engine's
//     repo is single-branch and short-lived, so a full rewrite is cheap.
//  3. Expire now-unreachable objects: `git reflog expire --expire=now --all`
//     then `git gc --prune=now --aggressive` (go-git: prune loose objects and
//     repack). This physically removes the secret blob from .git/objects.
//  4. ROTATE the leaked credential — history rewrite does not undo exposure.
//  5. Re-register the rotated value via Config.RegisterSensitiveValues so the
//     scanner and log redaction pick up the new plaintext.

package audit

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// SecretPurgeProcedure is the operator-facing purge/gc runbook, surfaced in the
// finding report so an owner who hits a leak knows the remediation without
// leaving the audit context.
const SecretPurgeProcedure = "purge: (1) halt commits into the work dir; " +
	"(2) rewrite history to drop the blob (go-git branch replay, or " +
	"`git filter-repo --path <file> --invert-paths`); " +
	"(3) `git reflog expire --expire=now --all && git gc --prune=now`; " +
	"(4) ROTATE the leaked credential; " +
	"(5) re-register the rotated value via RegisterSensitiveValues."

// SecretFinding is a single detected secret. It deliberately does NOT contain
// the secret value — only its class and location — so the finding itself is
// safe to log and surface.
type SecretFinding struct {
	// Path is the committed file path the secret was found in.
	Path string
	// Kind is "registered_credential" (matched the credential-store registry)
	// or "credential_pattern" (matched a format regex).
	Kind string
	// Detail is a human-readable label of the secret class (never the value).
	Detail string
	// Line is the 1-based line number of the first match (0 when unknown, e.g.
	// binary content or a registry match whose position is not tracked).
	Line int
}

// CommitScanResult aggregates the scan over a whole commit write-set.
type CommitScanResult struct {
	// Clean is true iff Findings is empty. A caller MUST refuse the auto-commit
	// (or run the purge procedure) when Clean is false (MIN-5 fail-closed).
	Clean bool
	// Findings are all secrets detected across every scanned file.
	Findings []SecretFinding
	// Files is the number of files scanned.
	Files int
}

// Report renders a non-secret-leaking summary suitable for an audit Details
// field or an owner notice. Empty when Clean.
func (r CommitScanResult) Report() string {
	if r.Clean {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d secret(s) detected in %d file(s); commit refused. ", len(r.Findings), r.Files)
	seen := make(map[string]struct{})
	for _, f := range r.Findings {
		loc := f.Path
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.Path, f.Line)
		}
		key := loc + "|" + f.Detail
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		fmt.Fprintf(&b, "[%s at %s] ", f.Detail, loc)
	}
	b.WriteString(SecretPurgeProcedure)
	return b.String()
}

// SecretScanner detects secrets on the auto-commit path (MIN-5). Construct once
// (from the credential store's replacer) and reuse; it is safe for concurrent
// use — all state is read-only after construction.
type SecretScanner struct {
	// replacer is the credential-store sensitive-value registry, obtained from
	// config.Config.SensitiveDataReplacer(). Nil disables the registry layer.
	replacer stringReplacer
	// patterns are the format regexes (defaultPatterns + custom). labels is
	// aligned 1:1 with patterns.
	patterns []*regexp.Regexp
	labels   []string
}

// stringReplacer is the minimal interface the registry layer needs. *strings.Replacer
// (returned by config.Config.SensitiveDataReplacer) satisfies it, so callers pass
// that directly without this package importing pkg/config (which would cycle:
// config → audit is the existing direction).
type stringReplacer interface {
	Replace(s string) string
}

// NewSecretScanner builds a scanner. registeredReplacer is the credential
// store's sensitive-value replacer (pass config.Config.SensitiveDataReplacer();
// nil is allowed and disables the registry layer). extraPatterns are additional
// operator-configured format regexes (security.audit.redaction_patterns). An
// invalid custom pattern returns an error.
func NewSecretScanner(registeredReplacer stringReplacer, extraPatterns []string) (*SecretScanner, error) {
	if len(defaultPatterns) != len(defaultPatternLabels) {
		// Guard against defaultPatterns / defaultPatternLabels drifting apart.
		return nil, fmt.Errorf("audit: defaultPatterns (%d) and defaultPatternLabels (%d) length mismatch — fix redactor.go",
			len(defaultPatterns), len(defaultPatternLabels))
	}
	s := &SecretScanner{replacer: registeredReplacer}
	for i, p := range defaultPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("audit: invalid default pattern %q: %w", p, err)
		}
		s.patterns = append(s.patterns, re)
		s.labels = append(s.labels, defaultPatternLabels[i])
	}
	for _, p := range extraPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("audit: invalid custom secret pattern %q: %w", p, err)
		}
		s.patterns = append(s.patterns, re)
		s.labels = append(s.labels, "operator-configured pattern")
	}
	return s, nil
}

// registryReplacer is nil-safe: a nil *strings.Replacer stored in the interface
// would still be non-nil at the interface level, so callers pass a real
// replacer or literal nil. We treat a nil interface as "no registry layer".
func (s *SecretScanner) hasRegistry() bool { return s.replacer != nil }

// ScanBytes scans a single file's content and returns every secret finding.
// path is used only to label findings.
func (s *SecretScanner) ScanBytes(path string, content []byte) []SecretFinding {
	var findings []SecretFinding
	if len(content) == 0 {
		return findings
	}
	text := string(content)

	// Layer 1: credential-store registry. If running content through the
	// registry replacer changes it, at least one registered credential is
	// present. We do not know its exact position from the replacer, so Line is
	// left 0; the presence itself is the fail-closed signal MIN-5 requires.
	if s.hasRegistry() {
		if s.replacer.Replace(text) != text {
			findings = append(findings, SecretFinding{
				Path:   path,
				Kind:   "registered_credential",
				Detail: "credential-store registered value",
				Line:   0,
			})
		}
	}

	// Layer 2: format patterns. Report the first match per pattern with its
	// line number; never store the matched value.
	for i, re := range s.patterns {
		loc := re.FindIndex(content)
		if loc == nil {
			continue
		}
		findings = append(findings, SecretFinding{
			Path:   path,
			Kind:   "credential_pattern",
			Detail: s.labels[i],
			Line:   lineOf(content, loc[0]),
		})
	}
	return findings
}

// ScanCommit scans an entire commit write-set (path → content) and returns the
// aggregate result. A non-Clean result MUST block the auto-commit (MIN-5).
func (s *SecretScanner) ScanCommit(files map[string][]byte) CommitScanResult {
	res := CommitScanResult{Clean: true, Files: len(files)}
	for path, content := range files {
		if fs := s.ScanBytes(path, content); len(fs) > 0 {
			res.Findings = append(res.Findings, fs...)
			res.Clean = false
		}
	}
	return res
}

// lineOf returns the 1-based line number of byte offset within content.
func lineOf(content []byte, offset int) int {
	if offset <= 0 {
		return 1
	}
	if offset > len(content) {
		offset = len(content)
	}
	return bytes.Count(content[:offset], []byte{'\n'}) + 1
}

// ScanReader is a convenience wrapper that buffers an io.Reader-backed file in
// one shot (small commit blobs only; the engine commits diffs, not large
// binaries). It is NOT a streaming / constant-memory reader — buf.ReadFrom
// reads the entire file into memory — but the line numbers it reports ARE
// accurate, because ScanBytes derives them by counting '\n' bytes over the
// buffered content (see lineOf).
func (s *SecretScanner) ScanReader(path string, r *bufio.Reader) ([]SecretFinding, error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		return nil, err
	}
	return s.ScanBytes(path, buf.Bytes()), nil
}
