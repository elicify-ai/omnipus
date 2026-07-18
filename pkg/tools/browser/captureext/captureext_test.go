package captureext

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestExtensionID_MatchesManifestKey guards against ExtensionID drifting
// from the embedded manifest.json's "key" field — if either is ever
// regenerated without updating the other, this fails immediately instead of
// surfacing as a mysterious real-Chrome ID mismatch.
func TestExtensionID_MatchesManifestKey(t *testing.T) {
	got, err := ManifestExtensionID()
	if err != nil {
		t.Fatalf("ManifestExtensionID() error: %v", err)
	}
	if got != ExtensionID {
		t.Fatalf("ExtensionID constant %q does not match ID computed from embedded manifest key %q", ExtensionID, got)
	}
}

// TestExtensionID_FormatSanity checks the pinned ID has the exact shape
// Chrome produces: 32 lowercase letters in the range a-p (a nibble-mapped
// hex string, never 0-9 or q-z).
func TestExtensionID_FormatSanity(t *testing.T) {
	if len(ExtensionID) != 32 {
		t.Fatalf("ExtensionID length = %d, want 32", len(ExtensionID))
	}
	if !regexp.MustCompile(`^[a-p]{32}$`).MatchString(ExtensionID) {
		t.Fatalf("ExtensionID %q does not match ^[a-p]{32}$", ExtensionID)
	}
}

// TestExtensionIDFromPublicKeyDER_KnownVector pins the ID-derivation
// algorithm itself against a fixed input/output pair so a future refactor
// of the hashing/nibble-mapping logic can't silently change extension IDs
// without a test noticing.
func TestExtensionIDFromPublicKeyDER_KnownVector(t *testing.T) {
	raw, err := embeddedExt.ReadFile(embeddedRoot + "/manifest.json")
	if err != nil {
		t.Fatalf("read embedded manifest: %v", err)
	}
	var doc manifestKeyDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse embedded manifest: %v", err)
	}

	// Re-derive from the raw base64 key material via the public helper and
	// confirm it round-trips to the same pinned constant a second way
	// (ManifestExtensionID already covers the base64-decode step; this
	// covers ExtensionIDFromPublicKeyDER in isolation).
	der, err := base64.StdEncoding.DecodeString(doc.Key)
	if err != nil {
		t.Fatalf("decode manifest key: %v", err)
	}
	if got := ExtensionIDFromPublicKeyDER(der); got != ExtensionID {
		t.Fatalf("ExtensionIDFromPublicKeyDER(manifest key) = %q, want %q", got, ExtensionID)
	}
}

// TestSeed_CreatesVersionedDirWithAllFiles verifies a first-time Seed call
// creates <destRoot>/captureext/<Version>/ populated with every embedded
// file, byte-identical to the embedded copy.
func TestSeed_CreatesVersionedDirWithAllFiles(t *testing.T) {
	destRoot := t.TempDir()

	dir, err := Seed(destRoot)
	if err != nil {
		t.Fatalf("Seed() error: %v", err)
	}

	wantDir := filepath.Join(destRoot, "captureext", Version)
	if dir != wantDir {
		t.Fatalf("Seed() dir = %q, want %q", dir, wantDir)
	}

	wantFiles := []string{"manifest.json", "encoder.html", "encoder.js", "sw.js"}
	for _, name := range wantFiles {
		wantContent, err := embeddedExt.ReadFile(embeddedRoot + "/" + name)
		if err != nil {
			t.Fatalf("read embedded %q: %v", name, err)
		}
		gotContent, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("seeded file %q missing or unreadable: %v", name, err)
		}
		if string(gotContent) != string(wantContent) {
			t.Errorf("seeded %q content mismatch with embedded copy", name)
		}
	}
}

// TestSeed_ContentIntegrity checks the seeded manifest.json is valid JSON,
// carries the pinned key/version, and declares the expected MV3 shape
// (permissions, background service worker) — catching accidental
// corruption or drift between the embedded manifest and what this task's
// constants assume.
func TestSeed_ContentIntegrity(t *testing.T) {
	destRoot := t.TempDir()
	dir, err := Seed(destRoot)
	if err != nil {
		t.Fatalf("Seed() error: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read seeded manifest.json: %v", err)
	}

	var manifest struct {
		ManifestVersion int      `json:"manifest_version"`
		Version         string   `json:"version"`
		Key             string   `json:"key"`
		Permissions     []string `json:"permissions"`
		Background      struct {
			ServiceWorker string `json:"service_worker"`
		} `json:"background"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("seeded manifest.json is not valid JSON: %v", err)
	}

	if manifest.ManifestVersion != 3 {
		t.Errorf("manifest_version = %d, want 3", manifest.ManifestVersion)
	}
	if manifest.Version != Version {
		t.Errorf("manifest version = %q, want %q (captureext.Version)", manifest.Version, Version)
	}
	if manifest.Key == "" {
		t.Error("manifest key is empty")
	}
	wantPerms := map[string]bool{"tabCapture": false, "tabs": false}
	for _, p := range manifest.Permissions {
		if _, ok := wantPerms[p]; ok {
			wantPerms[p] = true
		}
	}
	for p, found := range wantPerms {
		if !found {
			t.Errorf("manifest permissions missing %q (got %v)", p, manifest.Permissions)
		}
	}
	if manifest.Background.ServiceWorker != "sw.js" {
		t.Errorf("manifest background.service_worker = %q, want %q", manifest.Background.ServiceWorker, "sw.js")
	}

	// encoder.js and sw.js should not be empty and should not contain the
	// literal string "TODO" (zero-tolerance rule).
	for _, name := range []string{"encoder.js", "sw.js", "encoder.html"} {
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read seeded %q: %v", name, err)
		}
		if len(content) == 0 {
			t.Errorf("seeded %q is empty", name)
		}
		if strings.Contains(string(content), "TODO") {
			t.Errorf("seeded %q contains a TODO marker", name)
		}
	}
}

// TestSeed_EncoderJS_ContentGuards is a deliberately brittle, cheap interim
// guard over the seeded encoder.js content — QA regression-wave item 6.
// These are string/regex assertions against generated JS, not behavioral
// tests (there is no headless-Chrome harness in this package), so they are
// EXPECTED to need updating whenever encoder.js's implementation legitimately
// changes; each sub-check is commented with the specific fix/finding it
// guards so a future editor knows what would silently regress if the
// assertion were just deleted rather than updated.
func TestSeed_EncoderJS_ContentGuards(t *testing.T) {
	destRoot := t.TempDir()
	dir, err := Seed(destRoot)
	if err != nil {
		t.Fatalf("Seed() error: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "encoder.js"))
	if err != nil {
		t.Fatalf("read seeded encoder.js: %v", err)
	}
	content := string(raw)

	// (a) letterbox regression guard (W3 fix 5): min==max dimension pinning
	// MUST sit inside the SAME getUserMedia video.mandatory block as
	// chromeMediaSourceId — pinning min/max on an unrelated stream request
	// would silently stop constraining the capture surface to the tab's own
	// viewport, reintroducing black bars either side of the encoded frame.
	// Anchored on a regex over the mandatory block itself (order-sensitive,
	// not just "all four substrings exist somewhere in the file") rather than
	// four independent Contains checks, so a refactor that moved minWidth/
	// minHeight to some OTHER, unrelated stream request would still fail
	// this guard.
	videoMandatoryBlock := regexp.MustCompile(
		`chromeMediaSource:\s*'tab',\s*chromeMediaSourceId:\s*streamId,\s*minWidth:\s*capW,\s*minHeight:\s*capH,\s*maxWidth:\s*capW,\s*maxHeight:\s*capH,`,
	)
	if !videoMandatoryBlock.MatchString(content) {
		t.Error("encoder.js: expected chromeMediaSourceId immediately followed by minWidth/minHeight/maxWidth/maxHeight " +
			"(all pinned to capW/capH) in the SAME video.mandatory getUserMedia block — letterbox regression guard (W3 fix 5)")
	}

	// (b) offer-answer timeout arm (fix-wave C, review-flagged gap): a
	// browser_capture_offer sent with no browser_capture_answer response
	// must eventually force a reconnect rather than hang the encoder page
	// forever. Checks both the constant/arm function AND the actual
	// setTimeout wiring, not just the string "OFFER_ANSWER_TIMEOUT_MS"
	// appearing anywhere (which could be a stale, disconnected declaration).
	if !strings.Contains(content, "const OFFER_ANSWER_TIMEOUT_MS") {
		t.Error("encoder.js: missing OFFER_ANSWER_TIMEOUT_MS constant (fix-wave C offer-answer timeout)")
	}
	if !strings.Contains(content, "function armOfferAnswerTimeout") {
		t.Error("encoder.js: missing armOfferAnswerTimeout() (fix-wave C offer-answer timeout)")
	}
	if !regexp.MustCompile(`setTimeout\(\s*\(\)\s*=>\s*\{[^}]*offer-answer timeout`).MatchString(content) {
		t.Error("encoder.js: armOfferAnswerTimeout must actually arm a setTimeout whose callback reports an " +
			"\"offer-answer timeout\" — found the constant/function names but not the wired setTimeout call")
	}

	// (c) removed scaffolding stays removed: __omnipusMeasureAudioRMS was a
	// diagnostic-only audio-RMS measurement hook from earlier debugging that
	// was deliberately stripped — it must never silently creep back in via a
	// copy-paste from an old branch/backup.
	if strings.Contains(content, "__omnipusMeasureAudioRMS") {
		t.Error("encoder.js: __omnipusMeasureAudioRMS scaffolding must stay removed (was diagnostic-only, already stripped)")
	}
}

// TestSeed_Idempotent verifies a second Seed call to the same destRoot is a
// no-op: it returns the same directory without error, and pre-existing
// content is left untouched (including a deliberately "corrupted" file, to
// prove Seed never overwrites on a repeat call).
func TestSeed_Idempotent(t *testing.T) {
	destRoot := t.TempDir()

	dir1, err := Seed(destRoot)
	if err != nil {
		t.Fatalf("first Seed() error: %v", err)
	}

	// Simulate a user having hand-edited the seeded manifest after the
	// first boot; a second Seed() call must NOT clobber it.
	sentinelPath := filepath.Join(dir1, "manifest.json")
	sentinel := []byte(`{"manifest_version":3,"version":"1.0.0","sentinel":"user-edited"}`)
	if err := os.WriteFile(sentinelPath, sentinel, 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	dir2, err := Seed(destRoot)
	if err != nil {
		t.Fatalf("second Seed() error: %v", err)
	}
	if dir2 != dir1 {
		t.Fatalf("second Seed() dir = %q, want %q (same as first call)", dir2, dir1)
	}

	got, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("read sentinel after second Seed(): %v", err)
	}
	if string(got) != string(sentinel) {
		t.Fatalf("second Seed() overwrote user-edited manifest.json; got %q, want untouched sentinel %q", got, sentinel)
	}
}

// TestSeed_EmptyDestRoot verifies the explicit-error contract for a bad
// call, rather than silently seeding to an unexpected location.
func TestSeed_EmptyDestRoot(t *testing.T) {
	if _, err := Seed(""); err == nil {
		t.Fatal("Seed(\"\") returned nil error, want an error for empty destRoot")
	}
}

// TestSeed_AtomicNoPartialLeftovers checks Seed doesn't leave a stray
// staging directory behind after a successful run.
func TestSeed_AtomicNoPartialLeftovers(t *testing.T) {
	destRoot := t.TempDir()
	if _, err := Seed(destRoot); err != nil {
		t.Fatalf("Seed() error: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(destRoot, "captureext"))
	if err != nil {
		t.Fatalf("read captureext dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".seed-captureext-") {
			t.Fatalf("stray staging dir left behind: %s", e.Name())
		}
	}
}
