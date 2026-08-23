package captureext

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
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
	if err = json.Unmarshal(raw, &doc); err != nil {
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
		`chromeMediaSource:\s*'tab',\s*chromeMediaSourceId:\s*streamId,(?:\s*//[^\n]*)*\s*minWidth:\s*Math\.round\(capW \* captureScale\),\s*minHeight:\s*Math\.round\(capH \* captureScale\),\s*maxWidth:\s*Math\.round\(capW \* captureScale\),\s*maxHeight:\s*Math\.round\(capH \* captureScale\),`,
	)
	if !videoMandatoryBlock.MatchString(content) {
		t.Error(
			"encoder.js: expected chromeMediaSourceId immediately followed by minWidth/minHeight/maxWidth/maxHeight " +
				"(all pinned to capW/capH x captureScale) in the SAME video.mandatory getUserMedia block — letterbox regression guard (W3 fix 5)",
		)
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
		t.Error(
			"encoder.js: __omnipusMeasureAudioRMS scaffolding must stay removed (was diagnostic-only, already stripped)",
		)
	}

	// (d) F3 (external review, 2026-08-13): capture_scale must define an
	// ABSENT field as 1, not leave captureScale sticky at whatever it was
	// last set to. A shared per-agent CaptureSession can serve a viewer that
	// drops from DPR 2 to DPR 1 (monitor change, or a second viewer joining
	// at 1x); the server sends capture_scale only when scale > 1, so
	// "absent means unchanged" pinned the encoder at 2x forever — 4x the
	// pixels against a tab now rendering at 1x. Pinned as an exact string:
	// the assignment must be unconditional (a ternary with an explicit `: 1`
	// fallback), not an `if (...) { captureScale = ... }` with no else,
	// which is exactly the sticky shape this guards against reintroducing.
	if !strings.Contains(
		content,
		"captureScale =\n      typeof msg.capture_scale === 'number' && isFinite(msg.capture_scale) ? Math.min(4, Math.max(1, msg.capture_scale)) : 1;",
	) {
		t.Error(
			"encoder.js: capture_scale handling must unconditionally assign captureScale with an explicit `: 1` " +
				"fallback for an absent/invalid field (F3) — a bare `if (...) { captureScale = ... }` with no else " +
				"reintroduces the sticky-across-recaptures bug",
		)
	}

	// (e) and (f): extract mungeVideoStartBitrate's function body in
	// isolation (bounded by the next top-level function declaration) so the
	// F4 guards below can't accidentally match an unrelated occurrence of
	// the same substrings elsewhere in the file.
	mungeFn := regexp.MustCompile(`(?s)function mungeVideoStartBitrate\(sdp\) \{.*?\nfunction teardownCapture`).FindString(content)
	if mungeFn == "" {
		t.Fatal("encoder.js: could not locate mungeVideoStartBitrate's function body (needed for F4 content guards)")
	}

	// (e) F4 (external review, 2026-08-13): the start-bitrate SDP hint must
	// target whichever payload type the m=video line lists FIRST (the
	// negotiated/preferred codec once setCodecPreferences reorders it), not
	// a hardcoded VP8-only rtpmap regex. setCodecPreferences REORDERS the
	// codec list but does not remove non-preferred codecs' rtpmap lines, so
	// a VP8-only match kept "succeeding" silently against VP8's
	// now-non-preferred payload type once H264 became primary — while H264,
	// the codec actually negotiated, got no hint at all and paid libwebrtc's
	// full ~60s conservative bandwidth-ramp on every connection.
	if strings.Contains(mungeFn, "VP8\\/90000") {
		t.Error(
			"encoder.js: mungeVideoStartBitrate must not hardcode a VP8-only rtpmap regex (F4) — it must target " +
				"the m=video line's preferred payload type regardless of which codec that is",
		)
	}
	if !strings.Contains(mungeFn, "mLineParts.length > 3 ? mLineParts[3] : null") {
		t.Error(
			"encoder.js: mungeVideoStartBitrate must derive the preferred payload type from the m=video line's " +
				"own field ordering (F4), not a codec-name regex",
		)
	}
	// A genuine miss (unparseable m=video line, or no rtpmap for the
	// preferred payload type) must be observable on the debug state surface,
	// not console-only — both miss branches must set lastError.
	if strings.Count(mungeFn, "window.__omnipusState.lastError = msg;") < 2 {
		t.Error(
			"encoder.js: mungeVideoStartBitrate must set window.__omnipusState.lastError on every miss path " +
				"(unparseable m-line AND no matching rtpmap) so a codec-target miss is observable, not silent (F4)",
		)
	}

	// (f) F12 (external review, 2026-08-13): both the pre-capture unmute and
	// the shutdown-time mute call chrome.tabs.update, which returns a
	// Promise in MV3 — a synchronous try/catch around a non-awaited call can
	// never observe a rejection, and a non-awaited unmute lets getUserMedia
	// start capturing before Chrome has actually applied it (silent-capture
	// risk, the same failure class as the --mute-audio incident this file
	// documents elsewhere, from the opposite direction).
	if !regexp.MustCompile(`await chrome\.tabs\.update\(tabId,\s*\{\s*muted:\s*false\s*\}\)`).MatchString(content) {
		t.Error("encoder.js: the pre-capture tab unmute must be awaited before getUserMedia starts (F12)")
	}
	if regexp.MustCompile(`try \{ chrome\.tabs\.update\(tabId, \{ muted: false \}\); \} catch`).MatchString(content) {
		t.Error("encoder.js: the fire-and-forget (non-awaited) pre-capture tab unmute must not reappear (F12)")
	}
	if !regexp.MustCompile(`await chrome\.tabs\.update\(capturedTabId,\s*\{\s*muted:\s*true\s*\}\)`).MatchString(content) {
		t.Error("encoder.js: the shutdown tab-mute must be awaited so a rejection (e.g. a closed tab) is actually caught (F12)")
	}
	if regexp.MustCompile(`try \{ chrome\.tabs\.update\(capturedTabId, \{ muted: true \}\); record\(`).MatchString(content) {
		t.Error("encoder.js: the shutdown tab-mute must not unconditionally record success before the update settles (F12)")
	}
}

// TestSeed_Idempotent verifies a second Seed call over an INTACT seed is a
// no-op: same directory, no error, no rewrite.
//
// CONTRACT CHANGE (2026-08-13): this test used to assert the opposite for
// edited content — that a hand-modified manifest.json survived a reseed.
// That "never overwrite" contract is exactly what stranded a persistent
// install on a two-day-stale encoder.js when an embedded-asset change
// shipped without a Version bump. The versioned seed dir is gateway-managed,
// not operator-editable; drifted content now gets replaced (see
// TestSeed_ReseedsOnContentDrift), and idempotence is only promised for a
// seed that still matches the embedded assets.
func TestSeed_Idempotent(t *testing.T) {
	destRoot := t.TempDir()

	dir1, err := Seed(destRoot)
	if err != nil {
		t.Fatalf("first Seed() error: %v", err)
	}

	dir2, err := Seed(destRoot)
	if err != nil {
		t.Fatalf("second Seed() error: %v", err)
	}
	if dir2 != dir1 {
		t.Fatalf("second Seed() dir = %q, want %q (same as first call)", dir2, dir1)
	}
	assertSeedMatchesEmbedded(t, dir2)
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

// versionContentHashes pins the sha256 of the embedded extension's full
// content per released Version. TestEmbeddedAssetsRequireVersionBump fails
// whenever an embedded asset changes without a matching Version bump — the
// exact omission that shipped three successive encoder.js fixes to a
// persistent-volume install (UAT, 2026-07-31) where Seed's already-seeded
// gate kept the OLD extension loading forever while every layer reported
// success. When this fails: bump Version in captureext.go AND
// embedded/manifest.json's "version" together, then ADD a new entry here —
// never edit an existing entry, the history is the point.
var versionContentHashes = map[string]string{
	"1.0.1": "5649686afe5871b13e5a31b0275d7aabe98143172f043e93d79c861947f99b38",
	"1.0.2": "366cea35b3775142c81c0a5d922b1a30db750731d4d277876303075a0b4b2d28",
	"1.0.3": "58cc11f1bbeac2bfdcf98917fd163aef577873b349630d880560ff46a2f1a0b5",
	"1.0.4": "b4452db3f20ccb56f733ea645f0d56f8142b6707fa5182df0298bc9f0b144575",
	"1.0.5": "ee383d255869ec8765da37e1dc2f7ca1971679d03d4868a0f72be578e7f60334",
	"1.0.6": "27002761ba1ae644d9c86ffa85d7dfed0bb09a33a879a0dfd531ee8d644a31f0",
	"1.0.7": "3d3421dcd59363f8ce3c0fe6c6463082c16125577fbeed01b5ed499accfd4b56",
	"1.0.8": "95d494bd8751af4a33a801a85503dff81100e68720d4ac09ec8b68b1148dd3cb",
	// 1.0.9 — round-2 F2/F7: the quality-adaptation loop no longer outlives
	// the evidence behind it (ADAPT_EVIDENCE_TTL_MS + adaptCarryOverIndex, so
	// a viewer never inherits a resolution a viewerless boot warm-up settled
	// on), and an adaptation that fails to APPLY is now reported to the
	// gateway instead of dying in the extension page's console.
	"1.0.9": "559e1bb80b4c0ac5abb63bb799594b23045be6d99498bf6b1a86c083baa064ec",
	// 1.0.10 — ignore a second SDP answer unless the PC is in have-local-offer
	// (CI ui-heavy 2026-08-16: ingest ICE died after "Failed to set remote
	// answer sdp: Called in wrong state: stable"), and do not call
	// setParameters when getParameters() returned empty encodings (Chrome
	// treats that as "getParameters has never been called").
	"1.0.10": "74d37064ceab571fb7890deb39f0f8fc434e10b00e58035988581046ac6b0fb4",
	// 1.0.11 — skip setParameters on Chrome's pre-negotiation encodings:[{}].
	"1.0.11": "2cbc1f94a415c83c6450c6c1557adecaf518b705b5ca058bf8635ede74d3fa03",
	// 1.0.12 — skip setParameters only on Chrome's literal placeholder
	// encodings:[{}] (shape), not on guessed fields, and report a
	// post-connected skip to the gateway instead of the console.
	"1.0.12": "492045aea8d6c732674f2064a244338abb6846cf75e908b1cadb005066dcac17",
	// 1.0.13 — serialize every getParameters()->setParameters() pair.
	// The InvalidStateError survived 1.0.11 and 1.0.12 on the hosted box:
	// the cause is overlapping applies invalidating each other's
	// transaction, not empty encodings.
	"1.0.13": "aea8a1f82b48584ffe0bcc0b55efa848893ddb40c8b7592cef667f7cb4d42241",
	// 1.0.14 — adapt_reset: restore full quality at the boot-warm handover
	// WITHOUT rebuilding the capture (a rebuild there measured ~17s to
	// first frame against ~4s without it).
	"1.0.14": "f04ed3f2e8fefe0490b160dad5b5e3223cf8381e6d13aa91123d7f41e8213394",
	// 1.0.15 — clamp the sender bitrate to the ceiling the gateway derives
	// from the VIEWER leg's RTCP receiver reports (ADR-069 Finding 2).
	"1.0.15": "b6bfa6fb6eb5ad0391c38e1d4f9d03d02e1fd1af202ffbfc4c162eb26da69b21",
}

func embeddedContentHash(t *testing.T) string {
	t.Helper()
	var paths []string
	if err := fs.WalkDir(embeddedExt, embeddedRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk embedded FS: %v", err)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		b, err := fs.ReadFile(embeddedExt, p)
		if err != nil {
			t.Fatalf("read embedded %s: %v", p, err)
		}
		h.Write([]byte(p))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestEmbeddedAssetsRequireVersionBump(t *testing.T) {
	got := embeddedContentHash(t)
	want, ok := versionContentHashes[Version]
	if !ok {
		t.Fatalf(
			"no pinned content hash for Version %q — add {%q: %q} to versionContentHashes (and confirm the Version bump is real)",
			Version,
			Version,
			got,
		)
	}
	if got != want {
		t.Fatalf(
			"embedded extension content changed but Version is still %q (hash %s, pinned %s) — bump Version in captureext.go AND embedded/manifest.json, then add the new entry to versionContentHashes",
			Version,
			got,
			want,
		)
	}
	// The manifest's own version string must ride along, or Chrome sees a
	// stale extension version even after a reseed.
	var doc manifestKeyDoc
	raw, err := fs.ReadFile(embeddedExt, embeddedRoot+"/manifest.json")
	if err != nil {
		t.Fatalf("read embedded manifest: %v", err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse embedded manifest: %v", err)
	}
	if doc.Version != Version {
		t.Fatalf(
			"embedded/manifest.json version %q != captureext.Version %q — they must be bumped together",
			doc.Version,
			Version,
		)
	}
}

// assertSeedMatchesEmbedded fails unless every embedded file exists under
// destDir byte-identical, and no staging/aside leftovers survive.
func assertSeedMatchesEmbedded(t *testing.T, destDir string) {
	t.Helper()
	if err := fs.WalkDir(embeddedExt, embeddedRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(embeddedRoot, p)
		if err != nil {
			return err
		}
		want, err := fs.ReadFile(embeddedExt, p)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(filepath.Join(destDir, rel))
		if err != nil {
			t.Errorf("seeded file %s: %v", rel, err)
			return nil
		}
		if string(got) != string(want) {
			t.Errorf("seeded file %s differs from embedded content", rel)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk embedded FS: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(destDir))
	if err != nil {
		t.Fatalf("read captureext parent dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".seed-captureext-") || strings.HasPrefix(e.Name(), ".stale-captureext-") {
			t.Errorf("leftover staging/aside dir: %s", e.Name())
		}
	}
}

// TestSeed_ReseedsOnContentDrift is the runtime backstop for the 2026-08-13
// incident: encoder.js changed without a Version bump, and the old
// existence-only gate kept a persistent install loading the stale extension
// forever while a fresh install got the new one. Seed must detect a seeded
// dir whose content drifted from the embedded assets — a tampered file AND a
// missing file — and atomically replace it with the embedded content.
func TestSeed_ReseedsOnContentDrift(t *testing.T) {
	for _, tc := range []struct {
		name  string
		drift func(t *testing.T, destDir string)
	}{
		{"tampered file", func(t *testing.T, destDir string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(destDir, "encoder.js"), []byte("// stale pre-bump encoder\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing file", func(t *testing.T, destDir string) {
			t.Helper()
			if err := os.Remove(filepath.Join(destDir, "manifest.json")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			first, err := Seed(root)
			if err != nil {
				t.Fatalf("initial Seed() error: %v", err)
			}
			tc.drift(t, first)
			second, err := Seed(root)
			if err != nil {
				t.Fatalf("Seed() after drift error: %v", err)
			}
			if second != first {
				t.Fatalf("reseed changed the seed dir: %q != %q", second, first)
			}
			assertSeedMatchesEmbedded(t, second)
		})
	}
}

// TestSeed_RecoversFromUnreadableSeededFile is the regression test for F11
// (external review, 2026-08-13): seededContentMatches used to treat any
// disk-read failure OTHER than os.IsNotExist (e.g. a seeded file whose mode
// bits changed under it) as a hard error, which Seed propagated as a
// PERMANENT failure — never reaching the rename-based replace path that
// would actually recover it. Verified live: chmod 000 on one seeded file
// made Seed fail forever on every subsequent call (the caller reports
// not_capable) while the fix (any non-ENOENT read failure means "drift,
// replace" not "abort") lets Seed self-heal. Skipped on Windows (no POSIX
// mode bits) and when running as root (root ignores mode bits, so chmod 000
// would not actually block the read and the test would pass for the wrong
// reason).
func TestSeed_RecoversFromUnreadableSeededFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file mode bits do not apply on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: file mode bits do not block reads")
	}

	root := t.TempDir()
	first, err := Seed(root)
	if err != nil {
		t.Fatalf("initial Seed() error: %v", err)
	}

	victim := filepath.Join(first, "encoder.js")
	if chmodErr := os.Chmod(victim, 0o000); chmodErr != nil {
		t.Fatalf("chmod 0000 %q: %v", victim, chmodErr)
	}
	t.Cleanup(func() {
		_ = os.Chmod(victim, 0o644) // restore so t.TempDir() cleanup can remove the tree
	})

	second, err := Seed(root)
	if err != nil {
		t.Fatalf("Seed() must recover from an unreadable seeded file via the replace path, got a hard error instead: %v", err)
	}
	if second != first {
		t.Fatalf("reseed changed the seed dir: %q != %q", second, first)
	}
	assertSeedMatchesEmbedded(t, second)
}

// TestSeed_NoRewriteWhenContentMatches proves the content-verified gate is
// still idempotent: a second Seed over an intact seed must not rewrite any
// file (observed via a sentinel mtime pushed into the past).
func TestSeed_NoRewriteWhenContentMatches(t *testing.T) {
	root := t.TempDir()
	dir, err := Seed(root)
	if err != nil {
		t.Fatalf("initial Seed() error: %v", err)
	}
	sentinel := filepath.Join(dir, "encoder.js")
	past := time.Now().Add(-24 * time.Hour)
	if err = os.Chtimes(sentinel, past, past); err != nil {
		t.Fatal(err)
	}
	again, err := Seed(root)
	if err != nil {
		t.Fatalf("second Seed() error: %v", err)
	}
	if again != dir {
		t.Fatalf("second Seed() returned %q, want %q", again, dir)
	}
	info, err := os.Stat(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(past) {
		t.Errorf("encoder.js was rewritten by an idempotent Seed (mtime %v, want %v)", info.ModTime(), past)
	}
	assertSeedMatchesEmbedded(t, dir)
}
