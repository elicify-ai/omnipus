package browser

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/logger"
)

const (
	cftManifestURL = "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json"
	cftChannel     = "Stable"
	cftDownloadID  = "chrome-headless-shell"
)

// globalManifestURLForTesting overrides cftManifestURL when set. Tests use
// this to point the installer at a local httptest server.
var globalManifestURLForTesting string

type cftManifest struct {
	Channels map[string]struct {
		Version   string                              `json:"version"`
		Downloads map[string][]cftManifestDownloadRef `json:"downloads"`
	} `json:"channels"`
}

type cftManifestDownloadRef struct {
	Platform string `json:"platform"`
	URL      string `json:"url"`
}

var installMu sync.Mutex

// EnsureChromium ensures a managed chrome-headless-shell binary is present
// under installRoot, downloading the latest stable Chrome for Testing build
// when it is missing. Returns the absolute path to the executable.
//
// Layout: <installRoot>/<version>/chrome-headless-shell-<platform>/chrome-headless-shell
//
// Concurrent calls are serialized; the second caller observes the freshly
// extracted binary and returns immediately.
func EnsureChromium(ctx context.Context, installRoot string) (string, error) {
	installMu.Lock()
	defer installMu.Unlock()

	platform, err := cftPlatform()
	if err != nil {
		return "", err
	}

	// If any prior install already left a usable binary, prefer it without
	// hitting the network. We pick the most recent version directory.
	if path := findInstalledBinary(installRoot, platform); path != "" {
		return path, nil
	}

	logger.InfoCF("browser", "Chromium not installed — downloading from chrome-for-testing",
		map[string]any{
			"install_root": installRoot,
			"platform":     platform,
			"channel":      cftChannel,
		})

	manifest, err := fetchCFTManifest(ctx)
	if err != nil {
		return "", fmt.Errorf("browser: fetch chrome-for-testing manifest: %w", err)
	}

	channel, ok := manifest.Channels[cftChannel]
	if !ok {
		return "", fmt.Errorf("browser: chrome-for-testing manifest missing %q channel", cftChannel)
	}
	downloads, ok := channel.Downloads[cftDownloadID]
	if !ok {
		return "", fmt.Errorf("browser: chrome-for-testing manifest missing %q downloads", cftDownloadID)
	}

	var zipURL string
	for _, d := range downloads {
		if d.Platform == platform {
			zipURL = d.URL
			break
		}
	}
	if zipURL == "" {
		return "", fmt.Errorf("browser: chrome-for-testing has no %s build for platform %s", cftDownloadID, platform)
	}

	versionDir := filepath.Join(installRoot, channel.Version)
	if mkdirErr := os.MkdirAll(versionDir, 0o700); mkdirErr != nil {
		return "", fmt.Errorf("browser: create install dir: %w", mkdirErr)
	}

	zipPath := filepath.Join(versionDir, cftDownloadID+"-"+platform+".zip")
	if dlErr := downloadFile(ctx, zipURL, zipPath); dlErr != nil {
		_ = os.Remove(zipPath)
		return "", fmt.Errorf("browser: download %s: %w", zipURL, dlErr)
	}

	if extractErr := extractZip(zipPath, versionDir); extractErr != nil {
		return "", fmt.Errorf("browser: extract %s: %w", zipPath, extractErr)
	}
	_ = os.Remove(zipPath)

	binaryPath := filepath.Join(versionDir, cftDownloadID+"-"+platform, headlessShellBinaryName())
	info, err := os.Stat(binaryPath)
	if err != nil {
		return "", fmt.Errorf("browser: extracted archive missing %s: %w", binaryPath, err)
	}
	if info.Mode()&0o111 == 0 {
		if err := os.Chmod(binaryPath, info.Mode()|0o755); err != nil {
			return "", fmt.Errorf("browser: chmod +x %s: %w", binaryPath, err)
		}
	}

	logger.InfoCF("browser", "Chromium install complete",
		map[string]any{
			"version": channel.Version,
			"binary":  binaryPath,
		})

	return binaryPath, nil
}

func findInstalledBinary(installRoot, platform string) string {
	entries, err := os.ReadDir(installRoot)
	if err != nil {
		return ""
	}
	binaryName := headlessShellBinaryName()
	subdir := cftDownloadID + "-" + platform
	// Walk version directories newest-first by ModTime so we pick the most
	// recently installed build when multiple coexist.
	type cand struct {
		path string
		mod  time.Time
	}
	var cands []cand
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		bin := filepath.Join(installRoot, e.Name(), subdir, binaryName)
		info, err := os.Stat(bin)
		if err != nil || info.Mode()&0o111 == 0 {
			continue
		}
		cands = append(cands, cand{path: bin, mod: info.ModTime()})
	}
	if len(cands) == 0 {
		return ""
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if c.mod.After(best.mod) {
			best = c
		}
	}
	return best.path
}

func cftPlatform() (string, error) {
	switch runtime.GOOS {
	case "linux":
		if runtime.GOARCH != "amd64" {
			return "", fmt.Errorf("chrome-for-testing has no linux/%s build; install chromium manually", runtime.GOARCH)
		}
		return "linux64", nil
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "mac-arm64", nil
		}
		return "mac-x64", nil
	case "windows":
		if runtime.GOARCH == "386" {
			return "win32", nil
		}
		return "win64", nil
	default:
		return "", fmt.Errorf("unsupported platform %s/%s for managed chromium install", runtime.GOOS, runtime.GOARCH)
	}
}

func headlessShellBinaryName() string {
	if runtime.GOOS == "windows" {
		return "chrome-headless-shell.exe"
	}
	return "chrome-headless-shell"
}

func fetchCFTManifest(ctx context.Context) (*cftManifest, error) {
	url := cftManifestURL
	if globalManifestURLForTesting != "" {
		url = globalManifestURLForTesting
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest HTTP %d", resp.StatusCode)
	}
	var m cftManifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

func downloadFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download HTTP %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".part-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, dest)
}

func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	// Pre-resolve destDir once so the per-entry guard below is a cheap
	// prefix compare instead of a per-entry Abs call.
	absDest, absErr := filepath.Abs(destDir)
	if absErr != nil {
		return fmt.Errorf("resolve dest dir: %w", absErr)
	}
	absDestPrefix := absDest + string(os.PathSeparator)

	for _, f := range r.File {
		// First-pass: reject the obvious "../escape" patterns and absolute
		// paths in the entry name before we touch the filesystem at all.
		clean := filepath.Clean(f.Name)
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") ||
			strings.Contains(clean, string(os.PathSeparator)+".."+string(os.PathSeparator)) {
			return fmt.Errorf("zip entry escapes archive root: %q", f.Name)
		}
		outPath := filepath.Join(destDir, clean)
		// Second-pass: the Clean+Join above resolves intra-name traversal,
		// but a symlink already on disk (from an earlier entry in the same
		// archive) could still redirect a write outside destDir. Take the
		// absolute path of outPath and require it to live strictly under
		// absDest. This is the zip-slip canonical guard (CodeQL go/zipslip).
		absOut, absOutErr := filepath.Abs(outPath)
		if absOutErr != nil {
			return fmt.Errorf("resolve entry path: %w", absOutErr)
		}
		if absOut != absDest && !strings.HasPrefix(absOut, absDestPrefix) {
			return fmt.Errorf("zip entry escapes archive root: %q", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(outPath, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode()
		if mode == 0 {
			mode = 0o644
		}
		out, err := os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			rc.Close()
			out.Close()
			return err
		}
		rc.Close()
		if err := out.Close(); err != nil {
			return err
		}
	}
	return nil
}
