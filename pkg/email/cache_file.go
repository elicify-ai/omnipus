package email

// W2 encrypted per-mailbox folder-metadata snapshot store
// (spec mail-live-access-w2-discovery-and-cache-spec §3.6–§3.8, §3.10, §3.11;
// ADR-20261001 "What must be built" + filesystem table).
//
// One small AES-256-GCM sealed file per mailbox pair holds the resolved
// folder state — names, roles, UIDVALIDITY, schema/generation, transport and
// the last-validated time. It is metadata, never mail content: no header,
// subject, address or count data enters this file (§3.6). The envelope rules
// implemented here are R-3.7-1..8: authenticated encryption only (no
// plaintext fallback in either direction), a fresh crypto/rand nonce per
// write, purpose-separated keys SUPPLIED by the caller (the store never
// touches the credential store itself — the caller resolves keys via
// credentials.Store.DeriveSubkey with a stable namespaced purpose string),
// AAD binding purpose + schema version + pair identity + config generation +
// transport, atomic ciphertext-only replacement, reject-before-allocate, and
// never salvage.
//
// The first write is gated by the product-owned staging-exclusion evaluation
// (§3.8; cache_gate.go): a write happens only where the cache directory is
// provably excluded from data-directory version-control staging, checked
// in-process with git check-ignore-equivalent semantics — never by shelling
// out to git on this security-critical path (Hard Constraint #2). Where the
// exclusion is not provable the store refuses and the mailbox runs live-only
// with the visible cache_unavailable notice; that refusal is the product's
// own enforced safety outcome (check-and-refuse), never a machine-setup
// excuse.

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// Scope identifies one (agent, workspace) mailbox pair for cache keying and
// envelope binding. Both fields are opaque values supplied by the gateway
// integration (landing-order register row 12: a random minted pair ID stored
// beside the pair config, and a non-secret configuration-generation
// fingerprint); this package never derives, interprets or logs either.
type Scope struct {
	PairID     string
	Generation string
}

// Revision is the folder publication revision a read captured at its start
// (register row 10). Publications compare the captured value against the
// current one and refuse to publish when it is no longer current (§3.10
// R-3.10-3: a superseded read publishes nothing).
type Revision uint64

// Cache error classes (§3.12 closed class list; the wire notice_code for the
// disabled disk cache is "cache_unavailable", carried by the landed
// MailReadMetadata schema).
var (
	// ErrCacheCorrupt: a stored envelope failed authentication, is malformed,
	// oversized, foreign (wrong pair/generation/purpose) or carries an
	// unsupported version. Never salvage (R-3.7-7); the live path proceeds.
	ErrCacheCorrupt = errors.New("mail cache: sealed snapshot rejected (corrupt, foreign or unauthenticated)")
	// ErrCacheUnavailable: the cache cannot run on this attempt — locked keys,
	// an unsupported payload, an over-budget payload, or the product's
	// exclusion gate refusing the first write (§3.8). The mailbox runs
	// live-only with the visible cache_unavailable notice.
	ErrCacheUnavailable = errors.New("mail cache: unavailable (cache_unavailable)")
	// ErrStalePublication: the captured revision is no longer current, so the
	// publication is refused (§3.10 R-3.10-3).
	ErrStalePublication = errors.New("mail cache: publication superseded by a newer revision")
)

const (
	// snapshotSchemaVersion is the only payload schema this build writes or
	// reads; any other version is refused (§3.6 payload table, R-3.7-6/7).
	snapshotSchemaVersion = 1
	// envelopeVersion is the sealed-envelope format version; bumping it is the
	// supported rotation path for the envelope (never a silent change).
	envelopeVersion byte = 1
	// snapshotPayloadBudget is the Phase-1 per-mailbox payload budget
	// (§3.6, founder Q1=A): an oversized payload is a visible
	// cache-unavailable outcome, never truncation.
	snapshotPayloadBudget = 64 << 10
	// maxEnvelopeBytes caps the sealed file a Load will even read: the payload
	// budget plus the envelope's fixed overhead, with headroom — a small
	// multiple of the budget, checked from the directory entry BEFORE the
	// bytes are materialized (R-3.7-6).
	maxEnvelopeBytes = 96 << 10
	// folderCacheKeyBytes is the derived key length (credentials.Store.
	// DeriveSubkey's 32-byte output).
	folderCacheKeyBytes = 32
	// transportIMAP is the Phase-1 transport marker (§3.6 payload table).
	transportIMAP = "imap"

	envelopeMagic = "OMCF"
)

// SnapshotRole is one folder role's resolved state inside the sealed payload.
type SnapshotRole struct {
	Name string `json:"name"`
	// Source is one of the five register row-3 values:
	// override | special_use | fallback | saved | none.
	Source string `json:"source"`
	// UIDValidity is nil when never validated — an unknown epoch stays nil,
	// never a fabricated 0 (§3.11 UIDVALIDITY row, CX-4).
	UIDValidity  *uint32  `json:"uidvalidity"`
	Availability string   `json:"availability"`
	Ambiguity    []string `json:"ambiguity,omitempty"`
}

// SnapshotRoles carries the three UI roles (inbox stores only its last
// validated UIDVALIDITY; §3.6 payload table).
type SnapshotRoles struct {
	Inbox  SnapshotRole `json:"inbox"`
	Sent   SnapshotRole `json:"sent"`
	Drafts SnapshotRole `json:"drafts"`
}

// Snapshot is the plaintext payload inside the envelope — never on disk in
// this form (§3.6).
type Snapshot struct {
	SchemaVersion    int           `json:"schema_version"`
	PairIdentity     string        `json:"pair_identity"`
	ConfigGeneration string        `json:"config_generation"`
	Transport        string        `json:"transport"`
	Roles            SnapshotRoles `json:"roles"`
	LastValidatedAt  time.Time     `json:"last_validated_at"`
	SavedAt          time.Time     `json:"saved_at"`
}

// writeCacheFileFn is the package-local write indirection for the atomic
// replace (grill M-5; the pkg/credentials writeFileAtomicFn precedent): all
// sealed writes route through it so the crash-consistency test can park one
// without production test hooks.
var writeCacheFileFn = fileutil.WriteFileAtomic

// pairIDPattern constrains the opaque pair ID to a filesystem-safe shape.
// The value is minted by the gateway integration (register row 12); this
// guard keeps a malformed caller from escaping the cache directory.
var pairIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// FolderSnapshotStore loads, saves and deletes the sealed per-pair folder
// snapshot under <baseDir>/mail-cache/<opaque-pair-id>/folders.enc (§3.8).
// Keys are SUPPLIED: the store never touches the credential store (§4.1).
type FolderSnapshotStore struct {
	baseDir    string
	keys       func(Scope) ([]byte, error)
	purpose    string
	currentRev func(Scope) Revision
}

// NewFolderSnapshotStore builds a store rooted at baseDir (the resolved data
// root — production derives it from config.OmnipusHomeDir()). keys resolves
// the purpose-separated derived key per scope (ErrStoreLocked yields
// cache-unavailable, R-3.7-8); purpose is the derivation's namespaced info
// string (R-3.7-3) and doubles as the envelope's AAD purpose tag; currentRev
// is the current publication revision (register row 10).
func NewFolderSnapshotStore(baseDir string, keys func(Scope) ([]byte, error), purpose string, currentRev func(Scope) Revision) *FolderSnapshotStore {
	return &FolderSnapshotStore{baseDir: baseDir, keys: keys, purpose: purpose, currentRev: currentRev}
}

func (s *FolderSnapshotStore) snapshotPath(pairID string) (string, error) {
	if !pairIDPattern.MatchString(pairID) {
		return "", fmt.Errorf("%w: pair id is not a filesystem-safe opaque identifier", ErrCacheUnavailable)
	}
	return filepath.Join(s.baseDir, "mail-cache", pairID, "folders.enc"), nil
}

// Save seals snap and atomically replaces the pair's snapshot. Refusals, in
// order: unsupported schema version; payload over the 64 KiB budget; a
// superseded revision; an unusable key; the staging-exclusion gate (§3.8 —
// evaluated BEFORE any filesystem effect, so a refusal leaves nothing
// behind, not even the directory). A refusal never truncates, never writes a
// plaintext fallback, and never leaves a torn file (R-3.7-5).
func (s *FolderSnapshotStore) Save(scope Scope, snap Snapshot, captured Revision, derivedKey []byte) error {
	if snap.SchemaVersion != snapshotSchemaVersion {
		return fmt.Errorf("%w: unsupported snapshot schema version %d (this build writes version %d)", ErrCacheUnavailable, snap.SchemaVersion, snapshotSchemaVersion)
	}
	payload, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("%w: snapshot payload could not be marshalled: %w", ErrCacheUnavailable, err)
	}
	if len(payload) > snapshotPayloadBudget {
		return fmt.Errorf("%w: snapshot payload is %d bytes, over the %d-byte budget — refused visibly, never truncated (§3.6)", ErrCacheUnavailable, len(payload), snapshotPayloadBudget)
	}
	if captured != s.currentRev(scope) {
		return fmt.Errorf("%w: captured revision %d is no longer current (§3.10)", ErrStalePublication, captured)
	}
	key := derivedKey
	if key == nil {
		key, err = s.keys(scope)
		if err != nil {
			return fmt.Errorf("%w: derived key unavailable: %w", ErrCacheUnavailable, err)
		}
	}
	if len(key) != folderCacheKeyBytes {
		return fmt.Errorf("%w: derived key must be %d bytes, got %d", ErrCacheUnavailable, folderCacheKeyBytes, len(key))
	}
	if err := ensureStagingExclusion(s.baseDir); err != nil {
		return fmt.Errorf("%w: %w", ErrCacheUnavailable, err)
	}
	envelope, err := sealSnapshot(payload, s.purpose, snap, scope, key)
	if err != nil {
		return err
	}
	path, err := s.snapshotPath(scope.PairID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("%w: cache directory: %w", ErrCacheUnavailable, err)
	}
	return writeCacheFileFn(path, envelope, 0o600)
}

// Load reads and unseals the pair's snapshot. A missing file is a clean miss
// ((zero, false, nil)); every other failure yields the zero snapshot with
// the safe class — a refused read never returns partial or unauthenticated
// plaintext (R-3.7-1/7). Length caps are checked before reads that could
// materialize the bytes (R-3.7-6).
func (s *FolderSnapshotStore) Load(scope Scope) (Snapshot, bool, error) {
	key, err := s.keys(scope)
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("%w: derived key unavailable: %w", ErrCacheUnavailable, err)
	}
	if len(key) != folderCacheKeyBytes {
		return Snapshot{}, false, fmt.Errorf("%w: derived key must be %d bytes, got %d", ErrCacheUnavailable, folderCacheKeyBytes, len(key))
	}
	path, err := s.snapshotPath(scope.PairID)
	if err != nil {
		return Snapshot{}, false, err
	}
	info, statErr := os.Stat(path)
	if errors.Is(statErr, fs.ErrNotExist) {
		return Snapshot{}, false, nil
	}
	if statErr != nil {
		return Snapshot{}, false, fmt.Errorf("%w: stat: %w", ErrCacheUnavailable, statErr)
	}
	if info.Size() > maxEnvelopeBytes {
		return Snapshot{}, false, fmt.Errorf("%w: sealed envelope is %d bytes, beyond the %d-byte cap — rejected before any allocation (R-3.7-6)", ErrCacheCorrupt, info.Size(), maxEnvelopeBytes)
	}
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		return Snapshot{}, false, fmt.Errorf("%w: read: %w", ErrCacheUnavailable, readErr)
	}
	snap, openErr := openSnapshot(raw, s.purpose, scope, key)
	if openErr != nil {
		return Snapshot{}, false, openErr
	}
	return snap, true, nil
}

// Delete removes the pair's snapshot — the W2-supplied delete primitive for
// the mailbox-removal cascade (§3.8 E-4). Deleting a never-written pair is
// orphan-tolerant (no error).
func (s *FolderSnapshotStore) Delete(scope Scope) error {
	path, err := s.snapshotPath(scope.PairID)
	if err != nil {
		return err
	}
	if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
		return rmErr
	}
	// Best-effort empty-directory cleanup; never an error for the caller.
	_ = os.Remove(filepath.Dir(path))
	return nil
}

// envelopeAAD binds the envelope's identity fields: the derivation purpose
// tag, the schema version, the opaque pair identity, the configuration
// generation and the transport (R-3.7-4). The reader rebuilds this from its
// OWN scope, so a file copied between pairs, across generations or from
// another purpose fails authentication instead of decrypting.
func envelopeAAD(purpose string, schemaVersion int, pairID, generation, transport string) []byte {
	var b bytes.Buffer
	b.WriteString(purpose)
	b.WriteByte(0)
	var v [4]byte
	binary.BigEndian.PutUint32(v[:], uint32(schemaVersion))
	b.Write(v[:])
	b.WriteByte(0)
	b.WriteString(pairID)
	b.WriteByte(0)
	b.WriteString(generation)
	b.WriteByte(0)
	b.WriteString(transport)
	return b.Bytes()
}

// sealSnapshot encrypts payload with a fresh crypto/rand nonce and frames the
// envelope: magic | envelope version | schema version | transport | nonce |
// AES-256-GCM ciphertext (R-3.7-1/2; AAD per R-3.7-4).
func sealSnapshot(payload []byte, purpose string, snap Snapshot, scope Scope, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: cipher: %w", ErrCacheUnavailable, err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: gcm: %w", ErrCacheUnavailable, err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("%w: fresh nonce: %w", ErrCacheUnavailable, err)
	}
	if len(snap.Transport) > 255 {
		return nil, fmt.Errorf("%w: transport marker too long", ErrCacheUnavailable)
	}
	aad := envelopeAAD(purpose, snap.SchemaVersion, scope.PairID, scope.Generation, snap.Transport)
	ct := gcm.Seal(nil, nonce, payload, aad)

	var out bytes.Buffer
	out.WriteString(envelopeMagic)
	out.WriteByte(envelopeVersion)
	var v [4]byte
	binary.BigEndian.PutUint32(v[:], uint32(snap.SchemaVersion))
	out.Write(v[:])
	out.WriteByte(byte(len(snap.Transport)))
	out.WriteString(snap.Transport)
	out.Write(nonce)
	out.Write(ct)
	return out.Bytes(), nil
}

// openSnapshot parses and unseals an envelope. Every malformed shape — bad
// magic, bad envelope version, unsupported schema version, bad lengths,
// failed authentication, unparseable payload, or an authenticated identity
// that disagrees with the independently resolved scope — refuses with the
// corrupt class and returns zero plaintext (R-3.7-4/6/7).
func openSnapshot(raw []byte, purpose string, scope Scope, key []byte) (Snapshot, error) {
	headerLen := len(envelopeMagic) + 1 + 4 + 1 + gcmNonceOverhead()
	if len(raw) < headerLen {
		return Snapshot{}, fmt.Errorf("%w: envelope shorter than its fixed header", ErrCacheCorrupt)
	}
	if string(raw[:len(envelopeMagic)]) != envelopeMagic {
		return Snapshot{}, fmt.Errorf("%w: bad envelope magic", ErrCacheCorrupt)
	}
	pos := len(envelopeMagic)
	if raw[pos] != envelopeVersion {
		return Snapshot{}, fmt.Errorf("%w: unsupported envelope version %d", ErrCacheCorrupt, raw[pos])
	}
	pos++
	schema := int(binary.BigEndian.Uint32(raw[pos : pos+4]))
	pos += 4
	if schema != snapshotSchemaVersion {
		return Snapshot{}, fmt.Errorf("%w: unsupported snapshot schema version %d", ErrCacheCorrupt, schema)
	}
	tlen := int(raw[pos])
	pos++
	if tlen > len(raw)-pos {
		return Snapshot{}, fmt.Errorf("%w: transport marker length exceeds the envelope", ErrCacheCorrupt)
	}
	transport := string(raw[pos : pos+tlen])
	pos += tlen

	block, err := aes.NewCipher(key)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: cipher: %w", ErrCacheCorrupt, err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: gcm: %w", ErrCacheCorrupt, err)
	}
	nonceLen := gcm.NonceSize()
	if len(raw)-pos < nonceLen+gcm.Overhead() {
		return Snapshot{}, fmt.Errorf("%w: envelope too short for nonce and tag", ErrCacheCorrupt)
	}
	nonce := raw[pos : pos+nonceLen]
	pos += nonceLen
	ct := raw[pos:]
	aad := envelopeAAD(purpose, schema, scope.PairID, scope.Generation, transport)
	payload, err := gcm.Open(nil, nonce, ct, aad)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: authentication failed (wrong key, bit flip, or a foreign pair/generation/purpose)", ErrCacheCorrupt)
	}
	var snap Snapshot
	if err := json.Unmarshal(payload, &snap); err != nil {
		return Snapshot{}, fmt.Errorf("%w: authenticated payload is not a valid snapshot", ErrCacheCorrupt)
	}
	// The envelope's authenticated word is never its own authority: the
	// decrypted identity must agree with the independently resolved scope
	// (R-3.7-4; the grill's encryption disposition).
	if snap.SchemaVersion != schema {
		return Snapshot{}, fmt.Errorf("%w: payload schema version disagrees with the envelope", ErrCacheCorrupt)
	}
	if snap.PairIdentity != scope.PairID || snap.ConfigGeneration != scope.Generation {
		return Snapshot{}, fmt.Errorf("%w: authenticated pair/generation disagrees with the requesting scope", ErrCacheCorrupt)
	}
	return snap, nil
}

// gcmNonceOverhead is the AES-GCM standard nonce length used in the header
// size pre-check (the live size comes from the constructed GCM itself).
func gcmNonceOverhead() int { return 12 }
