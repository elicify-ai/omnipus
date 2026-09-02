// Package captureext embeds the gateway-owned MV3 "capture extension" —
// the tabCapture self-consume + WebRTC ingest page loaded into the managed,
// headless Chrome instance to stream an agent's active tab (audio+video) to
// the gateway. See docs/internal/design/webrtc-build/wave-plan.md (row
// W1-E) and docs/internal/design/wv1-spike-results.md (Q2/Q3) for the
// design this implements.
package captureext

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// embeddedExt holds the capture extension's assets, compiled into the
// binary so a fresh install can seed a working extension to disk without
// any external files. Layout mirrors an unpacked MV3 extension directory:
// embedded/{manifest.json, encoder.html, encoder.js, sw.js}.
//
//go:embed all:embedded
var embeddedExt embed.FS

// embeddedRoot is the prefix under which the extension files live inside
// embeddedExt.
const embeddedRoot = "embedded"

// ExtensionID is the deterministic Chrome extension ID for the embedded
// extension, computed from the public key pinned in embedded/manifest.json's
// "key" field (see ExtensionIDFromPublicKeyDER for the algorithm). Chrome
// derives an unpacked extension's ID purely from its manifest key, so this
// value is stable across installs, machines, and reloads as long as the
// manifest's "key" field is unchanged — this is what lets the launcher pass
// --allowlisted-extension-id up front instead of a two-phase
// learn-then-relaunch dance (wave-plan.md decision #1).
//
// This constant is verified against the embedded manifest by
// TestExtensionID_MatchesManifestKey and against a real Chrome
// Extensions.loadUnpacked result by the accompanying real-Chrome
// verification harness (see wave-plan.md row W1-E "Definition of done").
//
// The private key corresponding to this public key is a throwaway,
// generated once with openssl and intentionally NOT shipped or committed —
// only the public key (which is what pins the ID) lives in the manifest.
const ExtensionID = "nibcmjlmohchocjndnbajhmlhkeldobo"

// Version is the extension's manifest version string. It also names the
// versioned seed subdirectory: <destRoot>/captureext/<Version>/.
//
// MUST be bumped (together with embedded/manifest.json's "version") whenever
// ANY embedded asset changes. Seed is gated on this directory existing, and
// on an install whose data dir persists across upgrades (e.g. the UAT Fly
// volume) an unbumped Version means the new binary keeps loading the OLD
// extension from disk forever — three successive encoder.js fixes shipped to
// UAT 2026-07-31 and none of them ran, while every layer reported success.
// TestEmbeddedAssetsRequireVersionBump pins the embedded content hash to
// this constant so that omission is now a test failure, not a silent no-op.
const Version = "1.0.16"

// manifestKeyDoc is the minimal shape needed to read the "key" field back
// out of the embedded manifest.json for ID verification. It is not a wire
// type — it only ever round-trips through embedded static assets, never a
// gateway/SPA boundary — so it is exempt from the contract-first wire
// format rule (Constraint #8 is about pkg/gateway request/response and
// SPA-persisted JSON, not extension manifests).
type manifestKeyDoc struct {
	Version string `json:"version"`
	Key     string `json:"key"`
}

// ExtensionIDFromPublicKeyDER computes a Chrome extension ID from a
// DER-encoded SubjectPublicKeyInfo public key, following Chrome's
// documented algorithm: SHA-256 the DER bytes, take the first 16 bytes, and
// hex-encode them with each nibble mapped to a letter 'a'..'p' (0->'a',
// 1->'b', ..., 15->'p') instead of the usual 0-9a-f alphabet.
func ExtensionIDFromPublicKeyDER(der []byte) string {
	sum := sha256.Sum256(der)
	first16 := sum[:16]
	id := make([]byte, 0, 32)
	for _, b := range first16 {
		id = append(id, nibbleToLetter(b>>4), nibbleToLetter(b&0x0f))
	}
	return string(id)
}

func nibbleToLetter(n byte) byte {
	return 'a' + n
}

// ManifestExtensionID reads the "key" field out of the embedded
// manifest.json and returns the extension ID it computes to
// (ExtensionIDFromPublicKeyDER of the base64-decoded key). Used to verify
// ExtensionID hasn't drifted from the embedded manifest.
func ManifestExtensionID() (string, error) {
	raw, err := embeddedExt.ReadFile(embeddedRoot + "/manifest.json")
	if err != nil {
		return "", fmt.Errorf("captureext: read embedded manifest.json: %w", err)
	}
	var doc manifestKeyDoc
	if err = json.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("captureext: parse embedded manifest.json: %w", err)
	}
	if doc.Key == "" {
		return "", fmt.Errorf("captureext: embedded manifest.json has no \"key\" field")
	}
	der, err := base64.StdEncoding.DecodeString(doc.Key)
	if err != nil {
		return "", fmt.Errorf("captureext: decode manifest key: %w", err)
	}
	return ExtensionIDFromPublicKeyDER(der), nil
}

// Seed seeds the embedded capture extension into
// <destRoot>/captureext/<Version>/ via a staged-tmpdir + atomic rename (the
// pkg/skills.SeedDefaults pattern). Safe to call on every boot.
//
// The gate is CONTENT-verified, not existence-verified: if the directory
// already exists AND every embedded file matches it byte-for-byte, Seed does
// nothing and returns it unchanged; if any embedded file differs or is
// missing on disk, the whole directory is atomically REPLACED with the
// embedded content. This is the runtime backstop for the
// TestEmbeddedAssetsRequireVersionBump commit-time discipline: on 2026-08-13
// an encoder.js change shipped without a Version bump and the old
// existence-only gate kept the operator's install loading a two-day-stale
// extension while a fresh-home test install got the new one — every layer
// reported success. A drifted seed is always a bug (embedded assets changed
// without a bump), so replacing it is always correct; operator files are
// never expected inside this managed, versioned directory.
func Seed(destRoot string) (dir string, err error) {
	if destRoot == "" {
		return "", fmt.Errorf("captureext: seed destination root is empty")
	}

	destDir := filepath.Join(destRoot, "captureext", Version)

	replacing := false
	if _, statErr := os.Stat(destDir); statErr == nil {
		match, matchErr := seededContentMatches(destDir)
		if matchErr != nil {
			return "", fmt.Errorf("captureext: verify seeded content in %q: %w", destDir, matchErr)
		}
		if match {
			return destDir, nil // idempotent: already seeded, content verified
		}
		replacing = true
	} else if !os.IsNotExist(statErr) {
		return "", fmt.Errorf("captureext: stat %q: %w", destDir, statErr)
	}

	parent := filepath.Dir(destDir)
	if err = os.MkdirAll(parent, 0o755); err != nil {
		return "", fmt.Errorf("captureext: create parent dir %q: %w", parent, err)
	}

	tmpDir, err := os.MkdirTemp(parent, ".seed-captureext-*")
	if err != nil {
		return "", fmt.Errorf("captureext: create staging dir: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	walkErr := fs.WalkDir(embeddedExt, embeddedRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(embeddedRoot, p)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(tmpDir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, readErr := embeddedExt.ReadFile(p)
		if readErr != nil {
			return fmt.Errorf("read embedded file %q: %w", p, readErr)
		}
		if mkErr := os.MkdirAll(filepath.Dir(target), 0o755); mkErr != nil {
			return mkErr
		}
		return os.WriteFile(target, data, 0o644)
	})
	if walkErr != nil {
		return "", fmt.Errorf("captureext: stage embedded files: %w", walkErr)
	}

	if replacing {
		// Swap via an aside-rename (rename-over-existing-dir is not
		// portable): move the stale seed out of the way, move the staged
		// replacement in, then drop the stale copy. If the second rename
		// fails, restore the original so the extension keeps loading.
		aside := filepath.Join(parent, ".stale-captureext-"+filepath.Base(tmpDir))
		if err := os.Rename(destDir, aside); err != nil {
			return "", fmt.Errorf("captureext: move stale seed aside: %w", err)
		}
		if err := os.Rename(tmpDir, destDir); err != nil {
			_ = os.Rename(aside, destDir) // best-effort restore
			return "", fmt.Errorf("captureext: commit staged extension dir: %w", err)
		}
		committed = true
		_ = os.RemoveAll(aside)
		return destDir, nil
	}

	if err := os.Rename(tmpDir, destDir); err != nil {
		return "", fmt.Errorf("captureext: commit staged extension dir: %w", err)
	}
	committed = true

	return destDir, nil
}

// seededContentMatches reports whether every file embedded in the extension
// exists under destDir with identical bytes. Files present on disk but not
// embedded are ignored — the versioned dir is managed, and stale extras are
// swept by the replace path the next time real drift is detected.
func seededContentMatches(destDir string) (bool, error) {
	match := true
	err := fs.WalkDir(embeddedExt, embeddedRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !match {
			return nil
		}
		rel, relErr := filepath.Rel(embeddedRoot, p)
		if relErr != nil {
			return relErr
		}
		want, readErr := embeddedExt.ReadFile(p)
		if readErr != nil {
			return fmt.Errorf("read embedded file %q: %w", p, readErr)
		}
		got, diskErr := os.ReadFile(filepath.Join(destDir, rel))
		if diskErr != nil {
			// F11 (external review, 2026-08-13): ANY failure reading a
			// seeded file — missing (os.IsNotExist) OR unreadable for any
			// other reason (mode bits changed under it, a permission error,
			// a transient I/O hiccup) — means the seed no longer matches the
			// embedded content, i.e. DRIFT, which Seed's replace path exists
			// to fix. Before this fix only os.IsNotExist was treated as
			// drift; every other read error propagated up through
			// fs.WalkDir as a hard `err`, which Seed's caller turned into a
			// PERMANENT failure (see Seed's matchErr handling) instead of
			// falling through to the rename-based replace that would
			// actually recover it. Verified: chmod 000 on one seeded file
			// made Seed fail on every subsequent call forever, reporting
			// not_capable, while the replace that would have fixed it was
			// never reached.
			match = false
			// nilerr: returning nil here is the POINT — an unreadable seeded
			// file is drift to be REPLACED, not an error to propagate. See
			// the comment above.
			return nil //nolint:nilerr // unreadable seed == drift, handled by the replace path
		}
		if !bytes.Equal(want, got) {
			match = false
		}
		return nil
	})
	return match, err
}
