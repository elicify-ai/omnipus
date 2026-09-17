package documentruntime

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

const ManifestRevision = "f5c241825045341b9b71a9c1f369c24ba6de4f9c"
const FinalizeCommand = "omnipus-document-runtime finalize"

var revisionPattern = regexp.MustCompile(`^[a-f0-9]{40,64}$`)

//go:embed probe/omnipus_document_probe/*.py
var probeFiles embed.FS

type Requirement struct {
	Import       string `json:"import"`
	Distribution string `json:"distribution,omitempty"`
	Version      string `json:"version"`
}
type Asset struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type Manifest struct {
	Revision           string        `json:"revision"`
	Python             string        `json:"python"`
	Node               string        `json:"node"`
	Converter          string        `json:"converter"`
	PythonRequirements []Requirement `json:"python_requirements"`
	Assets             []Asset       `json:"assets"`
	Platforms          []string      `json:"platforms"`
}

type Layout struct{ Prefix, Bin, Lib, Skills, Manifest, Cache string }

func ResolveLayout(dataRoot, revision, workerID string) (Layout, error) {
	if !filepath.IsAbs(dataRoot) {
		return Layout{}, errors.New("document data root must be absolute")
	}
	if !revisionPattern.MatchString(revision) {
		return Layout{}, errors.New("document manifest revision must be an immutable lowercase hex digest")
	}
	if workerID == "" || strings.ContainsAny(workerID, `/\\`) {
		return Layout{}, errors.New("document worker ID must be a single path component")
	}
	prefix := filepath.Join(filepath.Clean(dataRoot), "toolchains", "documents", revision)
	return Layout{Prefix: prefix, Bin: filepath.Join(prefix, "bin"), Lib: filepath.Join(prefix, "lib"), Skills: filepath.Join(prefix, "skills"), Manifest: filepath.Join(prefix, "manifest.json"), Cache: filepath.Join(filepath.Clean(dataRoot), "cache", "documents", workerID)}, nil
}

func WorkerSandboxRules(layout Layout) (readExec []string, writable []string) {
	return []string{layout.Prefix}, []string{layout.Cache}
}
func AdminSandboxRules(layout Layout) (readExec []string, writable []string) {
	return []string{layout.Prefix}, []string{layout.Prefix, layout.Cache}
}

func ChildEnvironment(base []string, layout Layout) []string {
	out := make([]string, 0, len(base)+6)
	for _, kv := range base {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		upper := strings.ToUpper(key)
		if strings.Contains(upper, "KEY") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "CREDENTIAL") || strings.Contains(upper, "PASSWORD") {
			continue
		}
		switch upper {
		case "PATH", "PYTHONPATH", "NODE_PATH", "XDG_CACHE_HOME", "HOME", "TMPDIR", "LANG", "LC_ALL":
			continue
		}
		out = append(out, kv)
	}
	pathValue := layout.Bin
	for _, kv := range base {
		if strings.HasPrefix(strings.ToUpper(kv), "PATH=") {
			pathValue += string(os.PathListSeparator) + strings.TrimPrefix(kv, kv[:5])
			break
		}
	}
	return append(out, "PATH="+pathValue, "PYTHONPATH="+filepath.Join(layout.Lib, "python"), "NODE_PATH="+filepath.Join(layout.Lib, "node_modules"), "OMNIPUS_DOCUMENT_SKILLS="+layout.Skills, "XDG_CACHE_HOME="+layout.Cache, "HOME="+layout.Cache)
}

func InstallProbe(layout Layout, manifest Manifest) error {
	if layout.Prefix == "" || !filepath.IsAbs(layout.Prefix) {
		return errors.New("document prefix must be absolute")
	}
	if manifest.Revision != filepath.Base(layout.Prefix) {
		return errors.New("manifest revision does not match prefix")
	}
	for _, dir := range []string{layout.Bin, filepath.Join(layout.Lib, "python"), filepath.Join(layout.Lib, "node_modules"), layout.Skills, layout.Cache} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	target := filepath.Join(layout.Lib, "python", "omnipus_document_probe")
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	entries, _ := fs.ReadDir(probeFiles, "probe/omnipus_document_probe")
	for _, entry := range entries {
		data, err := probeFiles.ReadFile("probe/omnipus_document_probe/" + entry.Name())
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(target, entry.Name()), data, 0o644); err != nil {
			return err
		}
	}
	sort.Slice(manifest.Assets, func(i, j int) bool { return manifest.Assets[i].Path < manifest.Assets[j].Path })
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return fileutil.WriteFileAtomic(layout.Manifest, data, 0o644)
}

// FinalizeAdminSetup is the last step of the Admin-owned installation. The
// dependency installer must first place Python, Node and the converter inside
// layout.Prefix and populate the manifest with their absolute paths. This
// function refuses host-path shortcuts, materializes the packaged probe and
// skill assets, verifies checksums, and only then publishes manifest.json.
func FinalizeAdminSetup(layout Layout, manifest Manifest) error {
	if err := validateRuntimeExecutable(layout.Prefix, manifest.Python, "python"); err != nil {
		return err
	}
	if err := validateRuntimeExecutable(layout.Prefix, manifest.Node, "node"); err != nil {
		return err
	}
	if err := validateRuntimeExecutable(layout.Prefix, manifest.Converter, "converter"); err != nil {
		return err
	}
	if len(manifest.PythonRequirements) == 0 {
		return errors.New("document manifest has no Python requirements")
	}
	if err := os.MkdirAll(layout.Skills, 0o755); err != nil {
		return fmt.Errorf("create document skills directory: %w", err)
	}
	if err := InstallEmbeddedSkills(layout); err != nil {
		return fmt.Errorf("install document skill packages: %w", err)
	}
	if err := VerifyAssets(layout.Prefix, manifest.Assets); err != nil {
		return err
	}
	return InstallProbe(layout, manifest)
}

func validateRuntimeExecutable(prefix, name, component string) error {
	if !filepath.IsAbs(name) {
		return fmt.Errorf("document %s path must be absolute", component)
	}
	realPrefix, err := filepath.EvalSymlinks(prefix)
	if err != nil {
		return fmt.Errorf("resolve document prefix: %w", err)
	}
	realName, err := filepath.EvalSymlinks(name)
	if err != nil {
		return fmt.Errorf("resolve document %s: %w", component, err)
	}
	rel, err := filepath.Rel(realPrefix, realName)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("document %s resolves outside versioned prefix", component)
	}
	info, err := os.Stat(realName)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return fmt.Errorf("document %s is not an executable regular file", component)
	}
	return nil
}

var DocumentSkillIDs = []string{"elicify-docx", "elicify-xlsx", "elicify-pptx", "elicify-pdf"}

func IsDocumentSkill(id string) bool {
	for _, candidate := range DocumentSkillIDs {
		if id == candidate {
			return true
		}
	}
	return false
}

func InstallEmbeddedSkills(layout Layout) error {
	for _, id := range DocumentSkillIDs {
		dest := filepath.Join(layout.Skills, id)
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		if err := skills.ExportEmbeddedPackage(id, dest); err != nil {
			return err
		}
	}
	return nil
}

func VerifyAssets(prefix string, assets []Asset) error {
	root, err := os.OpenRoot(prefix)
	if err != nil {
		return fmt.Errorf("open managed document prefix: %w", err)
	}
	defer root.Close()
	for _, asset := range assets {
		if filepath.IsAbs(asset.Path) || strings.HasPrefix(filepath.Clean(asset.Path), "..") {
			return fmt.Errorf("asset path escapes prefix: %s", asset.Path)
		}
		data, err := root.ReadFile(filepath.Clean(asset.Path))
		if err != nil {
			return fmt.Errorf("asset %s: %w", asset.Path, err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != asset.SHA256 {
			return fmt.Errorf("asset %s checksum mismatch", asset.Path)
		}
	}
	return nil
}

func ProbeArgv(layout Layout) []string {
	return []string{"python", "-m", "omnipus_document_probe", "--manifest", layout.Manifest, "--format", "all"}
}
