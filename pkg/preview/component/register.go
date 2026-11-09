// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package component

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/documentruntime"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// SupportedOfficeSeries is the LibreOffice release line the converter is
// pinned to (§10: official 26.8.x).
const SupportedOfficeSeries = "26.8."

// Platform returns the manifest platform string of the running build.
func Platform() string { return runtime.GOOS + "-" + runtime.GOARCH }

// RegisterOptions carries the operator CLI's inputs.
type RegisterOptions struct {
	Home     string
	Folder   string
	Operator string
	// WriteRoots is every agent write root known from configuration; a
	// folder overlapping one is refused (AD-4 writer exclusion).
	WriteRoots []WriteRoot
	// GOOS overrides the platform layout (tests); empty means runtime.GOOS.
	GOOS string
	Now  func() time.Time
}

func (o RegisterOptions) goos() string {
	if o.GOOS != "" {
		return o.GOOS
	}
	return runtime.GOOS
}

func (o RegisterOptions) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// RegisterRenderer registers an unpacked renderer release folder (it carries
// its packaged manifest.json). Every fingerprint is re-computed; the
// registered manifest is written into the protected data directory.
func RegisterRenderer(opts RegisterOptions) (*Manifest, error) {
	root, err := realDir(opts.Folder)
	if err != nil {
		return nil, err
	}
	if overlap := FirstOverlap(root, opts.WriteRoots); overlap != nil {
		return nil, fmt.Errorf("pdf renderer folder is writable by an agent (%s); choose a folder no agent can write", overlap.Grant)
	}
	data, err := os.ReadFile(filepath.Join(root, ManifestFile))
	if err != nil {
		return nil, fmt.Errorf("not an Omnipus PDF renderer release folder (no %s): %w", ManifestFile, err)
	}
	shipped, err := ParseManifest(data, KindPDFRenderer)
	if err != nil {
		return nil, fmt.Errorf("renderer release manifest: %w", err)
	}
	if shipped.Platform != opts.goos()+"-"+runtime.GOARCH && opts.GOOS == "" {
		return nil, fmt.Errorf("renderer is built for %s, this machine is %s", shipped.Platform, Platform())
	}
	for name, want := range shipped.Files {
		got, _, hashErr := HashFile(filepath.Join(root, filepath.FromSlash(name)))
		if hashErr != nil {
			return nil, fmt.Errorf("renderer file %s: %w", name, hashErr)
		}
		if got != want {
			return nil, fmt.Errorf("renderer file %s does not match its release manifest", name)
		}
	}
	m := *shipped
	m.Trust = TrustOperatorRegistered
	m.Root = root
	m.RegisteredBy = opts.Operator
	m.RegisteredAt = opts.now().UTC().Format(time.RFC3339)
	if err := writeRegistered(opts.Home, KindPDFRenderer, &m, nil); err != nil {
		return nil, err
	}
	return &m, nil
}

// officeLayout describes where a LibreOffice install keeps its program.
type officeLayout struct {
	root       string
	executable string // relative slash path
	extra      []string
	version    string
	services   string // absolute services folder (macOS), "" elsewhere
}

var officeVersionLine = regexp.MustCompile(`(?m)^\s*(?:ProductVersion|ProductMajor|buildid)\s*=`)

// locateOffice finds the install's layout from file names alone: nothing in
// the candidate folder is executed or parsed here, so the writer exclusion
// can run before anything else touches the folder (W2-S01).
func locateOffice(folder, goos string) (*officeLayout, error) {
	root, err := realDir(folder)
	if err != nil {
		return nil, err
	}
	need := func(rel ...string) error {
		for _, f := range rel {
			if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(f))); statErr != nil {
				return fmt.Errorf("not a LibreOffice folder (%s missing)", f)
			}
		}
		return nil
	}
	switch goos {
	case "darwin":
		// Accept the .app itself or the folder holding it.
		if !strings.HasSuffix(root, ".app") {
			if _, statErr := os.Stat(filepath.Join(root, "LibreOffice.app")); statErr == nil {
				if root, err = realDir(filepath.Join(root, "LibreOffice.app")); err != nil {
					return nil, err
				}
			}
		}
		exe := "Contents/MacOS/soffice"
		if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(exe))); statErr != nil {
			return nil, errors.New("not a LibreOffice app bundle (Contents/MacOS/soffice missing)")
		}
		return &officeLayout{root: root, executable: exe,
			services: filepath.Join(root, "Contents", "Resources", "services")}, nil
	case "windows":
		if filepath.Base(root) == "program" {
			root = filepath.Dir(root)
		}
		if err := need("program/soffice.exe", "program/soffice.bin"); err != nil {
			return nil, err
		}
		return &officeLayout{root: root, executable: "program/soffice.exe", extra: []string{"program/soffice.bin"}}, nil
	default:
		if filepath.Base(root) == "program" {
			root = filepath.Dir(root)
		}
		if err := need("program/soffice", "program/soffice.bin"); err != nil {
			return nil, err
		}
		return &officeLayout{root: root, executable: "program/soffice.bin"}, nil
	}
}

// insideRoot refuses a layout file that resolves (through symlinks) outside
// the install root: the writer exclusion checked the root only.
func (l *officeLayout) insideRoot(rel string) error {
	resolved, err := filepath.EvalSymlinks(filepath.Join(l.root, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("LibreOffice file %s: %w", rel, err)
	}
	if r, relErr := filepath.Rel(l.root, resolved); relErr != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return fmt.Errorf("LibreOffice file %s points outside the LibreOffice folder", rel)
	}
	return nil
}

// officeVersion reads the version from the install's metadata files. It
// never runs the candidate (W2-S01): macOS reads Info.plist; Linux and
// Windows read program/versionrc or program/version.ini and then the
// product registry (share/registry/main.xcd, ooSetupVersionAboutBox), which
// official 26.8 Linux builds need because their versionrc carries no
// product version (measured O3, 2026-10-31).
func (l *officeLayout) officeVersion(goos string) (string, error) {
	switch goos {
	case "darwin":
		return plistVersion(filepath.Join(l.root, "Contents", "Info.plist"))
	case "windows":
		if v, err := iniVersion(filepath.Join(l.root, "program", "version.ini")); err == nil {
			return v, nil
		}
	default:
		if v, err := iniVersion(filepath.Join(l.root, "program", "versionrc")); err == nil {
			return v, nil
		}
	}
	return registryVersion(filepath.Join(l.root, "share", "registry", "main.xcd"))
}

var plistShortVersion = regexp.MustCompile(`<key>CFBundleShortVersionString</key>\s*<string>([^<]+)</string>`)

func plistVersion(p string) (string, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("LibreOffice version unreadable: %w", err)
	}
	m := plistShortVersion.FindSubmatch(data)
	if m == nil {
		return "", errors.New("LibreOffice version unreadable (no CFBundleShortVersionString)")
	}
	return strings.TrimSpace(string(m[1])), nil
}

var iniProductVersion = regexp.MustCompile(`(?m)^\s*(?:ProductVersion|MsiProductVersion)\s*=\s*([0-9][0-9.]*)`)

func iniVersion(p string) (string, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("LibreOffice version unreadable: %w", err)
	}
	m := iniProductVersion.FindSubmatch(data)
	if m == nil {
		if officeVersionLine.Match(data) {
			return "", errors.New("LibreOffice version unreadable (no product version line)")
		}
		return "", errors.New("LibreOffice version unreadable")
	}
	return string(m[1]), nil
}

var registryAboutVersion = regexp.MustCompile(`oor:name="ooSetupVersionAboutBox"[^>]*>\s*<value>([0-9][0-9.]*)</value>`)

// registryVersion reads the full product version from LibreOffice's
// product registry file.
func registryVersion(p string) (string, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("LibreOffice version unreadable: %w", err)
	}
	m := registryAboutVersion.FindSubmatch(data)
	if m == nil {
		return "", errors.New("LibreOffice version unreadable (no ooSetupVersionAboutBox in share/registry/main.xcd)")
	}
	return string(m[1]), nil
}

// RegisterOffice registers an official LibreOffice 26.8.x install. On macOS
// it also writes the filtered services registry (AD-3) next to the manifest.
func RegisterOffice(opts RegisterOptions) (*Manifest, error) {
	// Order matters (W2-S01): locate by file names, refuse an
	// agent-writable folder, check the layout stays inside it, and only
	// then read anything from it. Nothing in the candidate is executed.
	layout, err := locateOffice(opts.Folder, opts.goos())
	if err != nil {
		return nil, err
	}
	if overlap := FirstOverlap(layout.root, opts.WriteRoots); overlap != nil {
		return nil, fmt.Errorf("LibreOffice folder is writable by an agent (%s); choose a folder no agent can write", overlap.Grant)
	}
	for _, f := range append([]string{layout.executable}, layout.extra...) {
		if outsideErr := layout.insideRoot(f); outsideErr != nil {
			return nil, outsideErr
		}
	}
	if layout.version, err = layout.officeVersion(opts.goos()); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(layout.version, SupportedOfficeSeries) {
		return nil, fmt.Errorf("LibreOffice %s is not supported; install the official %sx release", layout.version, SupportedOfficeSeries)
	}
	files := map[string]string{}
	for _, f := range append([]string{layout.executable}, layout.extra...) {
		sum, _, hashErr := HashFile(filepath.Join(layout.root, filepath.FromSlash(f)))
		if hashErr != nil {
			return nil, fmt.Errorf("LibreOffice file %s: %w", f, hashErr)
		}
		files[f] = sum
	}
	m := &Manifest{
		Schema:         ManifestSchema,
		Component:      KindOffice,
		Version:        layout.version,
		Platform:       opts.goos() + "-" + runtime.GOARCH,
		Executable:     layout.executable,
		Files:          files,
		FileListSHA256: FileListDigest(files),
		Trust:          TrustOperatorRegistered,
		Root:           layout.root,
		RegisteredBy:   opts.Operator,
		RegisteredAt:   opts.now().UTC().Format(time.RFC3339),
	}
	var registry map[string][]byte
	if layout.services != "" {
		registry, err = filteredServices(layout.services)
		if err != nil {
			return nil, err
		}
		digests := make(map[string]string, len(registry))
		for name, data := range registry {
			digests[name] = sha256Hex(data)
		}
		m.Registry = "services"
		m.RegistrySHA256 = FileListDigest(digests)
	}
	if err := writeRegistered(opts.Home, KindOffice, m, registry); err != nil {
		return nil, err
	}
	return m, nil
}

// filteredServices copies every .rdb of the bundle's services folder,
// filtering services.rdb with the AD-3 macOS spell-checker filter.
func filteredServices(dir string) (map[string][]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read LibreOffice services registry: %w", err)
	}
	out := map[string][]byte{}
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".rdb") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if readErr != nil {
			return nil, readErr
		}
		if e.Name() == "services.rdb" {
			data, err = documentruntime.FilterConverterRegistry(data)
			if err != nil {
				return nil, fmt.Errorf("filter LibreOffice services registry: %w", err)
			}
		}
		out[e.Name()] = data
	}
	if _, ok := out["services.rdb"]; !ok {
		return nil, errors.New("LibreOffice services registry has no services.rdb")
	}
	return out, nil
}

func writeRegistered(home string, kind Kind, m *Manifest, registry map[string][]byte) error {
	if home == "" {
		return errors.New("omnipus home is unknown")
	}
	dir := RegisteredDir(home, kind)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if registry != nil {
		regDir := filepath.Join(dir, m.Registry)
		if err := os.RemoveAll(regDir); err != nil {
			return err
		}
		if err := os.MkdirAll(regDir, 0o700); err != nil {
			return err
		}
		for name, data := range registry {
			if err := fileutil.WriteFileAtomic(filepath.Join(regDir, name), data, 0o600); err != nil {
				return err
			}
		}
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(filepath.Join(dir, ManifestFile), append(data, '\n'), 0o600)
}

// Unregister removes kind's registration. Removing a registration that does
// not exist is not an error.
func Unregister(home string, kind Kind) (bool, error) {
	dir := RegisteredDir(home, kind)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return true, os.RemoveAll(dir)
}

func realDir(folder string) (string, error) {
	if folder == "" {
		return "", errors.New("a folder is required")
	}
	abs, err := filepath.Abs(folder)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a folder", folder)
	}
	return resolved, nil
}
