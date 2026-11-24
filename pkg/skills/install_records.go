// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package skills

// Skills & Tools C-5 and C-24: what Omnipus keeps about an installed skill
// in its own data, never in the skill folder (R32) — the category and the
// revision the install verified against a registry hash — and the trust
// approvals a person gave. Both files live under $OMNIPUS_HOME/system, a
// protected inventory name agents cannot read or change on either layer.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

const (
	installRecordsName = "skill-install-records.json"
	trustApprovalsName = "skill-trust-approvals.json"
	systemDirName      = "system"
)

// Skill categories (C-5, R32's SkillsMP categories plus uncategorised).
const CategoryUncategorised = "uncategorised"

var skillCategories = []string{
	"tools", "business", "development", "testing_security", "data_ai", "devops",
	"documentation", "content_media", "research", "lifestyle", "databases",
	"blockchain", CategoryUncategorised,
}

// SkillCategories returns the allowed category values in their order.
func SkillCategories() []string { return append([]string(nil), skillCategories...) }

// IsSkillCategory reports whether c is an allowed category value.
func IsSkillCategory(c string) bool {
	for _, v := range skillCategories {
		if v == c {
			return true
		}
	}
	return false
}

// UnknownCategoryError is the refusal for a category outside the list.
func UnknownCategoryError(c string) error {
	return fmt.Errorf("unknown skill category %q: use one of %s", c, strings.Join(skillCategories, ", "))
}

// InstallRecord is what Omnipus stores about one installed skill.
type InstallRecord struct {
	Category string `json:"category"`
	// VerifiedRevision is the folder revision whose install matched a
	// registry hash; "" when the install was not verified.
	VerifiedRevision string `json:"verified_revision,omitempty"`
}

// TrustApproval is one person's approval of one skill revision (C-24).
type TrustApproval struct {
	Key        string    `json:"key"`
	Revision   string    `json:"revision"`
	ApprovedBy string    `json:"approved_by"`
	ApprovedAt time.Time `json:"approved_at"`
}

// recordsMu serialises the read-modify-write of both files in this process.
var recordsMu sync.Mutex

// systemDirFor returns $OMNIPUS_HOME/system for the global skills folder
// ($OMNIPUS_HOME/skills).
func systemDirFor(skillsDir string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(skillsDir)), systemDirName)
}

func readJSONMap[T any](path string) (map[string]T, error) {
	out := map[string]T{}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	return out, nil
}

func writeJSONMap[T any](path string, m map[string]T) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", filepath.Base(path), err)
	}
	if err := fileutil.WriteFileAtomic(path, raw, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// WriteInstallRecord stores the record of an installed skill.
func WriteInstallRecord(skillsDir, slug string, rec InstallRecord) error {
	if !IsSkillCategory(rec.Category) {
		return UnknownCategoryError(rec.Category)
	}
	recordsMu.Lock()
	defer recordsMu.Unlock()
	path := filepath.Join(systemDirFor(skillsDir), installRecordsName)
	m, err := readJSONMap[InstallRecord](path)
	if err != nil {
		return err
	}
	m[SkillKey(slug)] = rec
	return writeJSONMap(path, m)
}

// ReadInstallRecord returns the stored record of an installed skill.
func ReadInstallRecord(skillsDir, slug string) (InstallRecord, bool, error) {
	recordsMu.Lock()
	defer recordsMu.Unlock()
	m, err := readJSONMap[InstallRecord](filepath.Join(systemDirFor(skillsDir), installRecordsName))
	if err != nil {
		return InstallRecord{}, false, err
	}
	rec, ok := m[SkillKey(slug)]
	return rec, ok, nil
}

// SkillCategoryOf returns the stored category of an installed skill, or
// uncategorised when none is stored.
func SkillCategoryOf(skillsDir, slug string) (string, error) {
	rec, ok, err := ReadInstallRecord(skillsDir, slug)
	if err != nil {
		return "", err
	}
	if !ok || rec.Category == "" {
		return CategoryUncategorised, nil
	}
	return rec.Category, nil
}

// SkillVerified reports C-5 verification: true for a built-in skill, or for
// an installed skill whose install matched a registry hash and whose folder
// has not changed since. It returns the current revision for the trust
// check. A skill that cannot be read counts as unverified.
func SkillVerified(skillsDir, slug string) (verified bool, revision string, err error) {
	revision, err = ContentRevision(filepath.Join(skillsDir, slug))
	if err != nil {
		return false, "", err
	}
	if IsBuiltin(slug) {
		return true, revision, nil
	}
	rec, ok, err := ReadInstallRecord(skillsDir, slug)
	if err != nil {
		return false, revision, err
	}
	return ok && rec.VerifiedRevision != "" && rec.VerifiedRevision == revision, revision, nil
}

// RecordTrustApproval stores a person's approval of one skill revision. One
// record is kept per (key, revision).
func RecordTrustApproval(skillsDir string, a TrustApproval) error {
	if a.Key == "" || a.Revision == "" || a.ApprovedBy == "" {
		return errors.New("skills: a trust approval needs a key, a revision and a person")
	}
	recordsMu.Lock()
	defer recordsMu.Unlock()
	path := filepath.Join(systemDirFor(skillsDir), trustApprovalsName)
	m, err := readJSONMap[TrustApproval](path)
	if err != nil {
		return err
	}
	m[a.Key+"@"+a.Revision] = a
	return writeJSONMap(path, m)
}

// TrustApproved reports whether revision of key has a person's approval.
func TrustApproved(skillsDir, key, revision string) (bool, error) {
	if key == "" || revision == "" {
		return false, nil
	}
	recordsMu.Lock()
	defer recordsMu.Unlock()
	m, err := readJSONMap[TrustApproval](filepath.Join(systemDirFor(skillsDir), trustApprovalsName))
	if err != nil {
		return false, err
	}
	_, ok := m[key+"@"+revision]
	return ok, nil
}

// TrustApprovalsFor lists the approvals of one key, newest first.
func TrustApprovalsFor(skillsDir, key string) ([]TrustApproval, error) {
	recordsMu.Lock()
	defer recordsMu.Unlock()
	m, err := readJSONMap[TrustApproval](filepath.Join(systemDirFor(skillsDir), trustApprovalsName))
	if err != nil {
		return nil, err
	}
	var out []TrustApproval
	for _, a := range m {
		if a.Key == key {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ApprovedAt.After(out[j].ApprovedAt) })
	return out, nil
}

// FileCategory returns the category a skill folder suggests in its own
// SKILL.md (metadata.category) when that is an allowed value, else "".
func FileCategory(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return ""
	}
	front, _ := splitFrontmatter(string(raw))
	fm, err := parseFrontmatter(front)
	if err != nil {
		return ""
	}
	nested, _ := fm["metadata"].(map[string]any)
	c, _ := nested["category"].(string)
	if c = strings.TrimSpace(c); IsSkillCategory(c) {
		return c
	}
	return ""
}

// InstallCategory resolves the category of an install (C-5): the requested
// one when given (an unknown one is refused), else the file suggestion,
// else uncategorised.
func InstallCategory(requested, dir string) (string, error) {
	if requested != "" {
		if !IsSkillCategory(requested) {
			return "", UnknownCategoryError(requested)
		}
		return requested, nil
	}
	if c := FileCategory(dir); c != "" {
		return c, nil
	}
	return CategoryUncategorised, nil
}
