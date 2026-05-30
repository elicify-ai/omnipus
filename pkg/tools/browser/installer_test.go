package browser

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEnsureChromium_PrefersInstalledBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only path layout")
	}
	root := t.TempDir()
	platform, err := cftPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	versionDir := filepath.Join(root, "131.0.6778.108", cftDownloadID+"-"+platform)
	if mkdirErr := os.MkdirAll(versionDir, 0o755); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	binPath := filepath.Join(versionDir, headlessShellBinaryName())
	if writeErr := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); writeErr != nil {
		t.Fatal(writeErr)
	}

	got, err := EnsureChromium(context.Background(), root)
	if err != nil {
		t.Fatalf("EnsureChromium with cached binary: %v", err)
	}
	if got != binPath {
		t.Fatalf("expected cached binary %q, got %q", binPath, got)
	}
}

func TestEnsureChromium_DownloadsAndExtracts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only path layout")
	}
	platform, err := cftPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	// Build a fake CfT zip containing one chrome-headless-shell file.
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	subdir := cftDownloadID + "-" + platform
	header := &zip.FileHeader{Name: subdir + "/" + headlessShellBinaryName()}
	header.SetMode(0o755)
	w, err := zw.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, writeErr := w.Write([]byte("#!/bin/sh\nexit 0\n")); writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr := zw.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	zipBytes := zipBuf.Bytes()

	mux := http.NewServeMux()
	mux.HandleFunc("/zip", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(zipBytes)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	manifest := cftManifest{
		Channels: map[string]struct {
			Version   string                              `json:"version"`
			Downloads map[string][]cftManifestDownloadRef `json:"downloads"`
		}{
			cftChannel: {
				Version: "131.0.6778.999",
				Downloads: map[string][]cftManifestDownloadRef{
					cftDownloadID: {
						{Platform: platform, URL: srv.URL + "/zip"},
					},
				},
			},
		},
	}
	manifestBytes, _ := json.Marshal(manifest)
	mux.HandleFunc("/manifest", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(manifestBytes)
	})

	// Swap the manifest URL just for this test.
	prev := globalManifestURLForTesting
	globalManifestURLForTesting = srv.URL + "/manifest"
	defer func() { globalManifestURLForTesting = prev }()

	root := t.TempDir()
	got, err := EnsureChromium(context.Background(), root)
	if err != nil {
		t.Fatalf("EnsureChromium download path: %v", err)
	}
	if !strings.HasSuffix(got, headlessShellBinaryName()) {
		t.Fatalf("expected binary path, got %q", got)
	}
	info, err := os.Stat(got)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("expected executable bit on %s", got)
	}
}
