// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package component implements office-preview component identity and trust
// (office-preview spec AD-4): where the PDF renderer and LibreOffice may come
// from, their fingerprints, the checks before every launch, operator
// registration, and the writer exclusion.
//
// Sources, in order: (1) an operator-registered manifest in Omnipus's
// protected data directory ($OMNIPUS_HOME/system/components/<kind>/), written
// only by the operator CLI (`omnipus pdf-renderer register`, `omnipus office
// register`); (2) packaged slots next to the executable, mirroring
// pkg/tools/browser/package_chrome.go::packageChromeRootCandidates, each with
// a shipped manifest.json. There is no environment-variable root and no PATH
// search, and nothing a workspace, a tool argument or agent-editable
// configuration supplies is ever consulted.
package component

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Kind names a component.
type Kind string

// The two components.
const (
	KindPDFRenderer Kind = "pdf-renderer"
	KindOffice      Kind = "office"
)

// Trust values recorded in a manifest.
const (
	TrustPackaged           = "packaged"
	TrustOperatorRegistered = "operator-registered"
)

// ManifestSchema is the manifest format version.
const ManifestSchema = 1

// ManifestFile is the packaged manifest's name inside a component root.
const ManifestFile = "manifest.json"

// Manifest is a component's identity (AD-4 "Manifest").
type Manifest struct {
	Schema    int    `json:"schema"`
	Component Kind   `json:"component"`
	Version   string `json:"version"`
	// BuildID and ProtocolVersion identify the renderer build (also part of
	// the page-cache key, AD-9).
	BuildID         string `json:"build_id,omitempty"`
	ProtocolVersion int    `json:"protocol_version,omitempty"`
	Platform        string `json:"platform"`
	// Executable is the program to launch, a slash path relative to Root.
	Executable string `json:"executable"`
	// Files maps slash paths relative to Root to SHA-256 (lower hex). It
	// always includes Executable; for the renderer also the pdfium shared
	// library (founder decision D1 A); for LibreOffice also soffice.bin on
	// Windows.
	Files map[string]string `json:"files"`
	// FileListSHA256 fingerprints the sorted "<path> <sha256>\n" lines.
	FileListSHA256 string `json:"file_list_sha256"`
	// Registry is the folder holding the filtered UNO services registry
	// (office on macOS, AD-3) and RegistrySHA256 the FileListDigest of its
	// files. For a packaged component it is relative to Root; for an
	// operator-registered one it lives next to the registered manifest.
	Registry       string `json:"registry,omitempty"`
	RegistrySHA256 string `json:"registry_sha256,omitempty"`
	Trust          string `json:"trust"`
	// Root is recorded for operator-registered components only (the
	// verified real path at registration).
	Root         string `json:"root,omitempty"`
	RegisteredBy string `json:"registered_by,omitempty"`
	RegisteredAt string `json:"registered_at,omitempty"`
}

// FileListDigest computes FileListSHA256 for files.
func FileListDigest(files map[string]string) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		b.WriteString(name)
		b.WriteByte(' ')
		b.WriteString(files[name])
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// ParseManifest decodes and validates a manifest strictly.
func ParseManifest(data []byte, want Kind) (*Manifest, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest is not valid JSON: %w", err)
	}
	if dec.More() {
		return nil, errors.New("manifest has trailing data")
	}
	if m.Schema != ManifestSchema {
		return nil, fmt.Errorf("manifest schema %d not supported", m.Schema)
	}
	if m.Component != want {
		return nil, fmt.Errorf("manifest is for %q, want %q", m.Component, want)
	}
	if m.Trust != TrustPackaged && m.Trust != TrustOperatorRegistered {
		return nil, fmt.Errorf("manifest trust %q unknown", m.Trust)
	}
	if err := validRelPath(m.Executable); err != nil {
		return nil, fmt.Errorf("manifest executable: %w", err)
	}
	if len(m.Files) == 0 {
		return nil, errors.New("manifest lists no files")
	}
	if _, ok := m.Files[m.Executable]; !ok {
		return nil, errors.New("manifest does not fingerprint its executable")
	}
	for name, sum := range m.Files {
		if err := validRelPath(name); err != nil {
			return nil, fmt.Errorf("manifest file %q: %w", name, err)
		}
		if !isSHA256Hex(sum) {
			return nil, fmt.Errorf("manifest file %q has no valid SHA-256", name)
		}
	}
	if m.FileListSHA256 != FileListDigest(m.Files) {
		return nil, errors.New("manifest file-list fingerprint does not match its files")
	}
	if m.Component == KindPDFRenderer && (m.BuildID == "" || m.ProtocolVersion < 1) {
		return nil, errors.New("renderer manifest lacks build id or protocol version")
	}
	if m.Registry != "" && !isSHA256Hex(m.RegistrySHA256) {
		return nil, errors.New("manifest registry has no valid SHA-256")
	}
	if m.Trust == TrustOperatorRegistered && !filepath.IsAbs(m.Root) {
		return nil, errors.New("registered manifest has no absolute root")
	}
	return &m, nil
}

func validRelPath(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return fmt.Errorf("%q is not a relative slash path", p)
	}
	if path.Clean(p) != p || p == "." || strings.HasPrefix(p, "../") || p == ".." {
		return fmt.Errorf("%q is not a clean path inside the component", p)
	}
	return nil
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && strings.ToLower(s) == s
}

// HashFile returns the SHA-256 of a regular, non-symlink file.
func HashFile(p string) (string, os.FileInfo, error) {
	info, err := os.Lstat(p)
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("%s is not a regular file", filepath.Base(p))
	}
	f, err := os.Open(p)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", nil, err
	}
	return hex.EncodeToString(h.Sum(nil)), info, nil
}

// HashDir fingerprints every regular file directly inside dir (no
// subfolders, no symlinks).
func HashDir(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	files := make(map[string]string, len(entries))
	for _, e := range entries {
		sum, _, hashErr := HashFile(filepath.Join(dir, e.Name()))
		if hashErr != nil {
			return nil, hashErr
		}
		files[e.Name()] = sum
	}
	if len(files) == 0 {
		return nil, errors.New("folder is empty")
	}
	return files, nil
}

// Status classifies a component for the person and the model (MV-1).
type Status int

// Component states.
const (
	StatusReady Status = iota
	StatusMissing
	StatusDamaged
	StatusUnsafe
)

// Component is a resolved, verified component.
type Component struct {
	Kind     Kind
	Manifest *Manifest
	// Root is the verified real path of the component root.
	Root string
	// Executable is the verified real absolute path passed to the spawn
	// unchanged.
	Executable string
	// Registry is the verified real path of the filtered registry folder
	// (office on macOS).
	Registry string
	// Source names where the component came from (for doctor lines).
	Source string

	stamps map[string]fileStamp
}

type fileStamp struct {
	size    int64
	modTime time.Time
}

// ComponentError is a resolution or verification failure with its status.
type ComponentError struct {
	Status Status
	Reason string
}

func (p *ComponentError) Error() string { return p.Reason }

func damaged(format string, args ...any) *ComponentError {
	return &ComponentError{Status: StatusDamaged, Reason: fmt.Sprintf(format, args...)}
}

// load builds a Component from a manifest and root, verifying every
// fingerprint.
func load(kind Kind, m *Manifest, root, registryDir, source string) (*Component, *ComponentError) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, damaged("component folder unreadable: %v", err)
	}
	c := &Component{Kind: kind, Manifest: m, Root: realRoot, Source: source}
	exe := filepath.Join(realRoot, filepath.FromSlash(m.Executable))
	realExe, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return nil, damaged("executable missing: %v", err)
	}
	if !within(realExe, realRoot) {
		return nil, damaged("executable resolves outside the component folder")
	}
	c.Executable = realExe
	if m.Registry != "" {
		base := realRoot
		if m.Trust == TrustOperatorRegistered {
			base = registryDir
		}
		reg := filepath.Join(base, filepath.FromSlash(m.Registry))
		realReg, regErr := filepath.EvalSymlinks(reg)
		if regErr != nil {
			return nil, damaged("filtered registry missing: %v", regErr)
		}
		c.Registry = realReg
	}
	if p := c.Verify(); p != nil {
		return nil, p
	}
	return c, nil
}

// Verify re-hashes every fingerprinted file (and the registry) against the
// manifest. The renderer is verified before every launch (AD-4).
func (c *Component) Verify() *ComponentError {
	stamps := make(map[string]fileStamp, len(c.Manifest.Files)+1)
	for name, want := range c.Manifest.Files {
		p := filepath.Join(c.Root, filepath.FromSlash(name))
		got, info, err := HashFile(p)
		if err != nil {
			return damaged("%s unreadable: %v", name, err)
		}
		if got != want {
			return damaged("%s fingerprint mismatch", name)
		}
		stamps[p] = fileStamp{size: info.Size(), modTime: info.ModTime()}
	}
	if c.Registry != "" {
		files, err := HashDir(c.Registry)
		if err != nil {
			return damaged("filtered registry unreadable: %v", err)
		}
		if FileListDigest(files) != c.Manifest.RegistrySHA256 {
			return damaged("filtered registry fingerprint mismatch")
		}
		for name := range files {
			p := filepath.Join(c.Registry, name)
			if info, statErr := os.Lstat(p); statErr == nil {
				stamps[p] = fileStamp{size: info.Size(), modTime: info.ModTime()}
			}
		}
	}
	c.stamps = stamps
	return nil
}

// VerifyIfChanged re-hashes only when a fingerprinted file's size or
// modification time changed since the last verification (LibreOffice, AD-4).
func (c *Component) VerifyIfChanged() *ComponentError {
	for p, st := range c.stamps {
		info, err := os.Lstat(p)
		if err != nil || info.Size() != st.size || !info.ModTime().Equal(st.modTime) {
			return c.Verify()
		}
	}
	return nil
}

func within(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Resolver finds components. Zero value is not usable; see NewResolver.
type Resolver struct {
	// Home is $OMNIPUS_HOME (registered manifests live below it).
	Home string
	// Executable is the running omnipus executable (packaged slots are
	// relative to it).
	Executable string
	// GOOS selects the platform's candidate layout (defaults to runtime).
	GOOS string
}

// NewResolver returns a resolver for the running executable.
func NewResolver(home string) (*Resolver, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if resolved, evalErr := filepath.EvalSymlinks(exe); evalErr == nil {
		exe = resolved
	}
	return &Resolver{Home: home, Executable: exe, GOOS: runtime.GOOS}, nil
}

// slotName is the packaged folder name of a kind (§10).
func slotName(kind Kind) string {
	if kind == KindOffice {
		return "office"
	}
	return "pdfium"
}

// PackagedCandidates returns the packaged slots for kind, in order (§10,
// mirroring packageChromeRootCandidates).
func (r *Resolver) PackagedCandidates(kind Kind) []string {
	exeDir := filepath.Dir(r.Executable)
	slot := slotName(kind)
	goos := r.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	if goos == "darwin" {
		return []string{
			filepath.Join(exeDir, "..", slot),
			filepath.Join(exeDir, "..", "..", "..", slot),
		}
	}
	return []string{
		filepath.Join(exeDir, "..", slot),
		filepath.Join(exeDir, "..", "share", "omnipus", slot),
	}
}

// RegisteredDir is the protected folder holding kind's registered manifest.
func RegisteredDir(home string, kind Kind) string {
	return filepath.Join(home, "system", "components", string(kind))
}

// Resolve returns the usable component of kind, or a Problem. An
// operator-registered component wins over a packaged one (it is the
// operator's explicit choice); a registration that fails verification is
// reported as damaged and never falls back to another program (AD-4).
func (r *Resolver) Resolve(kind Kind) (*Component, *ComponentError) {
	regDir := RegisteredDir(r.Home, kind)
	regManifest := filepath.Join(regDir, ManifestFile)
	if data, err := os.ReadFile(regManifest); err == nil {
		m, parseErr := ParseManifest(data, kind)
		if parseErr != nil {
			return nil, damaged("registered manifest invalid: %v", parseErr)
		}
		if m.Trust != TrustOperatorRegistered {
			return nil, damaged("registered manifest is not marked operator-registered")
		}
		return load(kind, m, m.Root, regDir, "registered: "+m.Root)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, damaged("registered manifest unreadable: %v", err)
	}
	for _, cand := range r.PackagedCandidates(kind) {
		data, err := os.ReadFile(filepath.Join(cand, ManifestFile))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, damaged("packaged manifest unreadable: %v", err)
		}
		m, parseErr := ParseManifest(data, kind)
		if parseErr != nil {
			return nil, damaged("packaged manifest invalid: %v", parseErr)
		}
		if m.Trust != TrustPackaged {
			return nil, damaged("packaged manifest is not marked packaged")
		}
		return load(kind, m, cand, "", "packaged: "+filepath.Clean(cand))
	}
	return nil, &ComponentError{Status: StatusMissing, Reason: "not installed"}
}
