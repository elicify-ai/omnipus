// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/term"
)

const (
	// EnvMasterKey is the env var for a hex-encoded 256-bit master key.
	// When set, the key is used directly (no Argon2id KDF).
	EnvMasterKey = "OMNIPUS_MASTER_KEY"

	// EnvKeyFile is the env var for a path to a file containing the hex master key.
	// The file must have mode 0600 (owner read/write only).
	EnvKeyFile = "OMNIPUS_KEY_FILE"

	// EnvMasterKeySource is the env var for a path to a file holding the hex
	// master key that the instance CONSUMES at boot: it is read once, used to
	// unlock the store, and then deleted.
	//
	// Why it exists, when EnvKeyFile already reads a key from a file: the two
	// have opposite intentions about what happens next.
	//
	//   - EnvKeyFile PERSISTS. The file is expected to survive every boot —
	//     it IS the instance's copy of the key, deliberately kept at rest.
	//   - EnvMasterKeySource CONSUMES. The file is a delivery hop, not a
	//     home. After a successful unlock nothing holding the plaintext key
	//     remains on disk, and the platform that delivered it must deliver it
	//     again on the next boot.
	//
	// This is the mode a hosted control plane uses to hand a tenant's master
	// key to a fresh instance: the plaintext key exists only in the instance's
	// memory and in the transient delivery hop, never at rest and never in an
	// environment variable (which is visible in platform dashboards, machine
	// configuration and process listings).
	EnvMasterKeySource = "OMNIPUS_MASTER_KEY_SOURCE"

	// DefaultKeyFileName is the filename used for the auto-generated master
	// key when no env-var or explicit key file is configured. It is placed
	// next to credentials.json inside the Omnipus home directory, so the
	// default permission model (0700 home dir + 0600 key file) matches the
	// SSH private-key threat model.
	DefaultKeyFileName = "master.key"
)

// Unlock attempts to unlock store using the following provisioning priority:
//
//  0. OMNIPUS_MASTER_KEY_SOURCE environment variable — consume-once delivery
//     (path to a 0600 file with the hex key; the file is DELETED after a
//     successful unlock, and nothing is persisted)
//  1. OMNIPUS_MASTER_KEY environment variable (hex-encoded 256-bit key)
//  2. OMNIPUS_KEY_FILE environment variable (path to a 0600 file with hex key)
//  3. Default key file next to credentials.json ($OMNIPUS_HOME/master.key, 0600)
//  4. Auto-generate a fresh key on a truly fresh install (no credentials.json
//     and no master.key) and write it to the default path with 0600
//  5. Interactive passphrase prompt (requires TTY; derives key via Argon2id)
//
// # Mode 0 — consume-once delivery (no plaintext key at rest)
//
// Mode 0 exists for a hosted control plane that must hand a tenant's master
// key to a freshly provisioned instance at first boot without the plaintext
// key ever coming to rest. Modes 1-4 each fail that requirement: mode 1 puts
// the key in an environment variable (visible in the platform dashboard,
// machine configuration and process listing), modes 2 and 3 keep hex
// plaintext on disk by design, and mode 4 mints and persists a key of its
// own. Mode 0 reads the delivered file through the same loader modes 2 and 3
// use — symlink-aware, regular-file-only, strict 0600, audited on every
// attempt — unlocks the store, and then removes the file.
//
// The delivery point is meant to be a RAM-backed (tmpfs) mount, so the key
// never touches durable storage in the first place. The removal is what makes
// the guarantee ours rather than the platform's. If the removal fails — a
// read-only secret mount is a legitimate platform choice — the boot still
// succeeds and a WARN records that the plaintext key remains readable at
// rest. Note that when the delivery path is a symlink, removal unlinks the
// symlink and not its target; a delivery point that symlinks into durable
// storage leaves the target's plaintext behind.
//
// Consequence, and it is intended rather than a limitation: because mode 0
// persists nothing, THE PLATFORM MUST RE-DELIVER THE KEY ON EVERY BOOT. An
// instance restarted without a fresh delivery has no key and will not unlock.
//
// Mode 0 fails closed. Any error — a missing file, wrong permissions, bad hex,
// a store that refuses the key — aborts Unlock instead of falling through to
// modes 1-5. Falling through would reach mode 4 on a fresh-looking instance,
// auto-generate a DIFFERENT key, and strand the tenant's existing encrypted
// data behind a key nobody has. For the same reason, mode 0 refuses to run at
// all when $OMNIPUS_HOME/master.key already exists: two candidate keys is an
// ambiguity to report, not to resolve by guessing.
//
// # Modes 3 and 4 — headless first run
//
// Modes 3 and 4 make the headless first-run experience work: on a clean VPS,
// starting the gateway with no env vars set will mint a fresh master key,
// persist it next to credentials.json, log a prominent backup warning, and
// continue boot. Subsequent boots pick up the same file via mode 3. The file
// lives under the Omnipus home directory which is 0700 per BRD SEC-27, so the
// threat model matches the SSH private-key convention.
//
// If no TTY is available and no env vars / default key / auto-generate are
// possible (e.g., the default key file is present but unreadable), Unlock
// returns an error. Callers must check store.IsLocked() before use.
//
// Implements US-4 acceptance criteria; mode 0 implements IN-12.
func Unlock(store *Store) error {
	// $OMNIPUS_HOME/master.key — the persisted default key file. Modes 0, 3
	// and 4 all reason about it, so it is resolved once up front.
	defaultKeyPath := filepath.Join(filepath.Dir(store.Path()), DefaultKeyFileName)

	// Mode 0: OMNIPUS_MASTER_KEY_SOURCE — consume-once delivery. Fails closed:
	// an error here never falls through to a later mode (see the doc comment).
	if sourcePath := os.Getenv(EnvMasterKeySource); sourcePath != "" {
		if err := unlockFromDeliveredKey(store, sourcePath, defaultKeyPath); err != nil {
			return fmt.Errorf("credentials: OMNIPUS_MASTER_KEY_SOURCE %q failed: %w", sourcePath, err)
		}
		return nil
	}

	// Mode 1: OMNIPUS_MASTER_KEY direct hex key.
	if hexKey := os.Getenv(EnvMasterKey); hexKey != "" {
		key, err := hexToKey(hexKey)
		if err != nil {
			return fmt.Errorf("credentials: %w", err)
		}
		if err := store.UnlockWithKey(key); err != nil {
			return err
		}
		slog.Debug("credentials: unlocked via OMNIPUS_MASTER_KEY")
		return nil
	}

	// Mode 2: OMNIPUS_KEY_FILE — explicit path means failure is fatal (headless deployments
	// cannot fall back to an interactive prompt; silently continuing would cause a hang).
	if keyFile := os.Getenv(EnvKeyFile); keyFile != "" {
		key, err := loadKeyFile(keyFile)
		if err != nil {
			return fmt.Errorf("credentials: OMNIPUS_KEY_FILE %q failed: %w", keyFile, err)
		}
		if err := store.UnlockWithKey(key); err != nil {
			return err
		}
		// #nosec G706 -- keyFile is passed as a discrete slog field (operator-
		// set env var, not attacker input either way); both log sinks escape
		// control bytes in string field values (see the sink detail noted on
		// execproxy.go's SSRF-blocked log lines), so no forgery surface.
		slog.Debug("credentials: unlocked via OMNIPUS_KEY_FILE", "path", keyFile)
		return nil
	}

	// Mode 3: Default key file next to credentials.json. If the file exists,
	// load it; its failure is fatal for the same reason as OMNIPUS_KEY_FILE.
	if _, err := os.Stat(defaultKeyPath); err == nil {
		key, err := loadKeyFile(defaultKeyPath)
		if err != nil {
			return fmt.Errorf("credentials: default key file %q failed: %w", defaultKeyPath, err)
		}
		if err := store.UnlockWithKey(key); err != nil {
			return err
		}
		slog.Debug("credentials: unlocked via default key file", "path", defaultKeyPath)
		return nil
	}

	// Mode 4: Auto-generate on fresh install. Only fires when:
	//   - no env-var key
	//   - no env-var key file
	//   - no default key file at $OMNIPUS_HOME/master.key
	//   - no existing credentials.json (would strand the encrypted data)
	// The generation is atomic via O_EXCL so two concurrent boots cannot
	// write different keys. A prominent backup warning is logged + printed
	// to stderr so the operator sees it in systemd/Docker logs.
	if !store.Exists() {
		key, err := generateAndPersistMasterKey(defaultKeyPath)
		if err != nil {
			return fmt.Errorf("credentials: auto-generate master key: %w", err)
		}
		if err := store.UnlockWithKey(key); err != nil {
			return err
		}
		return nil
	}

	// Mode 5: Interactive passphrase (requires TTY). Return an error when no TTY is
	// available — a silent nil would leave the store locked and cause confusing downstream
	// failures. Callers that allow a locked store should check before calling Unlock.
	// #nosec G115 -- os.Stdin.Fd() is a small, kernel-assigned file descriptor
	// number (bounded by the process's open-file-descriptor limit, orders of
	// magnitude below MaxInt); the uintptr->int conversion cannot overflow in
	// practice. Standard idiom, matches golang.org/x/term's own examples.
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("credentials: no master key available and no TTY — "+
			"set OMNIPUS_MASTER_KEY, OMNIPUS_KEY_FILE, or provide %s for headless operation",
			defaultKeyPath)
	}

	passphrase, err := promptPassphrase("Enter Omnipus master passphrase: ")
	if err != nil {
		return fmt.Errorf("credentials: passphrase prompt: %w", err)
	}
	if passphrase == "" {
		return fmt.Errorf("credentials: passphrase must not be empty")
	}

	slog.Info("credentials: deriving key from passphrase (Argon2id, ~2s)...")
	if err := store.UnlockWithPassphrase(passphrase); err != nil {
		return err
	}
	slog.Debug("credentials: unlocked via interactive passphrase")
	return nil
}

// unlockFromDeliveredKey implements Unlock mode 0: read the master key from
// the platform's delivery point, unlock the store with it, then delete the
// delivery file so no plaintext key is left at rest.
//
// It never writes a key anywhere — in particular it never reaches
// generateAndPersistMasterKey, whose whole job is the opposite. Every error it
// returns is fatal to the boot: the caller wraps it and returns rather than
// trying a later mode, because a fall-through would auto-generate a different
// key and strand the tenant's data.
//
// sourcePath is the delivery point ($OMNIPUS_MASTER_KEY_SOURCE); defaultKeyPath
// is $OMNIPUS_HOME/master.key, checked only for the ambiguity refusal below.
func unlockFromDeliveredKey(store *Store, sourcePath, defaultKeyPath string) error {
	// Refuse before reading anything when a persisted key is also present.
	// Two candidate keys means we cannot know which one encrypts the store,
	// and picking one at random would either strand the data or silently
	// consume a key the operator meant to keep. Name both and stop.
	//
	// Match ONLY on "does not exist". A stat that fails for any other reason —
	// EACCES, ELOOP, a dangling symlink — means a file may well be there and we
	// cannot see it, which is exactly the murky case this guard exists for.
	// Treating those as absent would let mode 0 proceed and consume the
	// delivered key on top of a persisted one we simply failed to stat.
	if _, statErr := os.Stat(defaultKeyPath); statErr == nil {
		emitMasterKeyAuditRule(sourcePath, false,
			"ambiguous: persisted key also present at "+defaultKeyPath, "master_key_ambiguous")
		return fmt.Errorf(
			"delivered key %q and persisted key %q are both present: refusing to guess which one encrypts the store; remove whichever is stale",
			sourcePath, defaultKeyPath)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		emitMasterKeyAuditRule(sourcePath, false,
			fmt.Sprintf("cannot determine whether a persisted key exists at %s: %v", defaultKeyPath, statErr),
			"master_key_ambiguous")
		return fmt.Errorf(
			"cannot determine whether a persisted key exists at %q (%w): refusing to consume the delivered key while a persisted one may be present",
			defaultKeyPath, statErr)
	}

	// Refuse a symlinked delivery point. loadKeyFile deliberately follows
	// symlinks — that is right for modes 2 and 3, where the file is meant to
	// persist and an operator may legitimately link to it. It is wrong here:
	// removal below unlinks the LINK, not its target, so a delivery point
	// symlinked into durable storage would leave the plaintext key at rest
	// while every log line said it had been consumed. The one guarantee this
	// mode exists to provide would be silently false.
	//
	// #nosec G703 -- sourcePath is the platform-set OMNIPUS_MASTER_KEY_SOURCE
	// env var, never request-derived.
	if lInfo, lErr := os.Lstat(sourcePath); lErr == nil && lInfo.Mode()&os.ModeSymlink != 0 {
		emitMasterKeyAuditRule(sourcePath, false, "delivery point is a symlink", "master_key_delivery_symlink")
		return fmt.Errorf(
			"delivery point %q is a symlink: refusing, because consuming it would unlink the symlink and leave the key readable at its target",
			sourcePath)
	}

	// Same loader as modes 2 and 3: lstat/symlink detection, regular-file
	// check, strict 0600 enforcement, hex validation, and an audit record on
	// every outcome.
	key, err := loadKeyFile(sourcePath)
	if err != nil {
		return err
	}

	// Prove the key actually opens this store BEFORE destroying the only copy
	// of it. UnlockWithKey validates length and nothing else (store.go), so
	// without this check any 32 bytes are accepted and then the delivery file
	// is deleted. A stale or wrong-tenant key would unlock "successfully",
	// the instance would run unable to read a single existing credential, and
	// writes would append entries under the wrong key — leaving one file
	// holding two key generations, where a later boot with the RIGHT key
	// silently cannot read the newer half. Fail before the removal, not after.
	if verifyErr := verifyKeyOpensStore(store.Path(), key); verifyErr != nil {
		emitMasterKeyAuditRule(sourcePath, false,
			fmt.Sprintf("delivered key does not decrypt the existing store: %v", verifyErr),
			"master_key_wrong_key")
		return fmt.Errorf(
			"delivered key does not decrypt the existing credential store at %q (%w): refusing, and leaving %q in place so the correct key can be delivered",
			store.Path(), verifyErr, sourcePath)
	}

	if err := store.UnlockWithKey(key); err != nil {
		return err
	}

	// Consume the delivery point. The mount is expected to be RAM-backed, but
	// removing the file is what makes "no plaintext key at rest" our guarantee
	// rather than the platform's.
	//
	// #nosec G703 -- sourcePath is the operator/platform-set
	// OMNIPUS_MASTER_KEY_SOURCE env var, never request-derived, and was just
	// validated by loadKeyFile above.
	if rmErr := os.Remove(sourcePath); rmErr != nil {
		// Deliberately NOT fatal. A read-only secret mount is a legitimate
		// platform choice, and refusing to boot over it would strand a tenant
		// whose key was delivered correctly. Say plainly what the cost is.
		//
		// #nosec G706 -- path/error are discrete slog fields, escaped by both
		// log sinks (see the sink detail on execproxy.go's SSRF-blocked lines).
		slog.Warn("credentials: could not consume delivered master key",
			"path", sourcePath,
			"error", rmErr.Error(),
			"consequence", "the plaintext master key remains readable at rest at this path",
			"remedy", "deliver the key on a tmpfs (RAM-backed) mount the instance can write to, so it can be removed after boot",
		)
		emitMasterKeyAudit(sourcePath, true, fmt.Sprintf("consumed=false, remove failed: %v", rmErr))
		return nil
	}

	emitMasterKeyAudit(sourcePath, true, "consumed=true, delivery file removed")
	// #nosec G706 -- see the Warn above; path is a discrete slog field.
	slog.Debug("credentials: unlocked via OMNIPUS_MASTER_KEY_SOURCE (delivery file consumed)",
		"path", sourcePath)
	return nil
}

// verifyKeyOpensStore reports whether key can actually decrypt the store at
// path. It is a no-op for a store that does not exist yet or holds no entries —
// there is nothing to be wrong about, and any key is as good as any other for a
// store whose first write has not happened.
//
// It works on a throwaway Store rather than the caller's, so a failed check
// leaves no half-unlocked store holding a key we just rejected.
func verifyKeyOpensStore(path string, key []byte) error {
	probe := NewStore(path)
	if !probe.Exists() {
		return nil
	}
	if err := probe.UnlockWithKey(key); err != nil {
		return err
	}
	names, err := probe.List()
	if err != nil {
		return fmt.Errorf("reading the existing store: %w", err)
	}
	if len(names) == 0 {
		return nil
	}
	// AES-GCM is authenticated, so a wrong key fails the tag check rather than
	// returning plausible garbage. One entry is a sufficient probe.
	if _, err := probe.Get(names[0]); err != nil {
		return err
	}
	return nil
}

// generateAndPersistMasterKey mints a fresh 256-bit AES-256 key using
// crypto/rand, writes it atomically to path with mode 0600, and returns the
// raw bytes for immediate unlock. The write uses O_EXCL so a concurrent
// process cannot clobber a half-written file. On any error the caller should
// refuse to boot — a failed key generation means we have no encrypted store.
func generateAndPersistMasterKey(path string) ([]byte, error) {
	key := make([]byte, keyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("read random bytes: %w", err)
	}

	// Ensure the parent directory exists with restrictive perms. The Omnipus
	// home is normally 0700 already (per BRD SEC-27), but on a truly first
	// boot we may be the one creating it.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("mkdir parent: %w", err)
	}

	// O_EXCL guarantees atomic creation — if two processes race, one gets
	// an error and re-probes via mode 3 on the next Unlock call.
	hexKey := hex.EncodeToString(key)
	// #nosec G304 -- generateAndPersistMasterKey has exactly one caller
	// (Unlock, mode 4), which always passes defaultKeyPath — a fixed
	// $OMNIPUS_HOME-derived constant, never request-derived.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create key file %q: %w", path, err)
	}
	if _, writeErr := f.WriteString(hexKey); writeErr != nil {
		if closeErr := f.Close(); closeErr != nil {
			_ = closeErr
		}
		rmErr := os.Remove(path)
		if rmErr != nil {
			return nil, fmt.Errorf("write key file %q: %w (cleanup remove failed: %w)", path, writeErr, rmErr)
		}
		return nil, fmt.Errorf("write key file %q: %w", path, writeErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		rmErr := os.Remove(path)
		if rmErr != nil {
			return nil, fmt.Errorf("close key file %q: %w (cleanup remove failed: %w)", path, closeErr, rmErr)
		}
		return nil, fmt.Errorf("close key file %q: %w", path, closeErr)
	}

	// Operator-visible backup warning. Print to STDOUT (not stderr) so it
	// lands in systemd-journald / Docker logs on the operator's terminal.
	// The gateway's logger.initPanicFile dup2's stderr fd to a panic log
	// file before credentials.Unlock runs, so writes to os.Stderr at this
	// point go to $OMNIPUS_HOME/logs/gateway_panic.log — not the console.
	// Stdout is unaffected and is what systemd / Docker / tail watch.
	// Errors from these Fprintln calls are intentionally dropped: os.Stdout
	// writes essentially never fail in this context, there is nothing
	// meaningful to retry, and the warning is NOT solely reliant on stdout
	// — slog.Warn below records the same fact through the persistent log
	// path regardless of whether the operator's terminal received it.
	if _, err := fmt.Fprintln(os.Stdout); err != nil {
		_ = err
	}
	if _, err := fmt.Fprintln(os.Stdout, "================================================================"); err != nil {
		_ = err
	}
	if _, err := fmt.Fprintln(os.Stdout, "  Omnipus generated a new master key on fresh install."); err != nil {
		_ = err
	}
	if _, err := fmt.Fprintln(os.Stdout, "  Key file: "+path); err != nil {
		_ = err
	}
	if _, err := fmt.Fprintln(os.Stdout, ""); err != nil {
		_ = err
	}
	if _, err := fmt.Fprintln(os.Stdout, "  BACK THIS FILE UP. Losing it makes your encrypted credential"); err != nil {
		_ = err
	}
	if _, err := fmt.Fprintln(os.Stdout, "  store (API keys, channel tokens) permanently inaccessible."); err != nil {
		_ = err
	}
	if _, err := fmt.Fprintln(os.Stdout, "================================================================"); err != nil {
		_ = err
	}
	if _, err := fmt.Fprintln(os.Stdout); err != nil {
		_ = err
	}
	// slog.Warn still writes through the default slog handler (stderr →
	// panic log) — that's fine, it's the persistent record. The stdout
	// banner above is the operator-facing copy.
	slog.Warn("credentials: auto-generated master key", "path", path,
		"warning", "back up this file — losing it makes credentials.json permanently inaccessible")

	return key, nil
}

// DeriveKeyFromPassphrase derives a 32-byte AES-256 key from passphrase + salt
// using Argon2id with parameters per SEC-23b.
func DeriveKeyFromPassphrase(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, argonTime, argonMemory, argonThreads, keyLen)
}

// hexToKey decodes a 64-character hex string into a 32-byte key.
func hexToKey(hexKey string) ([]byte, error) {
	if len(hexKey) != 64 {
		return nil, fmt.Errorf("invalid master key: expected 64 hex characters (256 bits), got %d", len(hexKey))
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("invalid master key: not valid hex: %w", err)
	}
	return key, nil
}

// loadKeyFile reads a hex master key from path, enforcing strict 0600 permissions.
//
// Threat model (v0.2 #155 item 2): a master key file with mode bits beyond
// owner read+write (0o600) is a configuration smell — group- and world-readable
// keys defeat the encryption-at-rest model entirely. We refuse to load such a
// file rather than silently downgrade security. The check is symmetric across
// modes 2 (OMNIPUS_KEY_FILE) and 3 (default $OMNIPUS_HOME/master.key); the
// auto-generate path (mode 4) writes 0600 by construction so it never trips.
//
// Symlink handling: Lstat is consulted first so a symlink whose own metadata
// claims unsafe perms cannot be used to mask a 0600 target. When the path IS a
// symlink, we follow it via Stat and require the TARGET to be a regular file
// at 0600. The symlink's own permission bits (typically 0o777 — Linux ignores
// them anyway) are tolerated as long as the target is correctly locked down.
//
// Returns a clear error mentioning the actual mode so operators can fix it
// with a single chmod 600.
// loadKeyFile has exactly two callers (Unlock modes 2 and 3): OMNIPUS_KEY_FILE
// (an operator-set env var) and defaultKeyPath (a fixed $OMNIPUS_HOME
// constant) — path is never request-derived. path.Lstat/Stat below are the
// deliberate symlink-aware target-permission check described in the doc
// comment above (not a traversal bug: this function's whole purpose is to
// resolve and validate whatever path an operator configured).
func loadKeyFile(path string) ([]byte, error) {
	// Lstat first to detect symlinks. A symlink's own perms are usually 0o777
	// on Linux and irrelevant to security; what matters is the target.
	// #nosec G703 -- see the func doc comment above: path is operator-
	// configured (env var or fixed default), not request-derived.
	lInfo, lErr := os.Lstat(path)
	if lErr != nil {
		emitMasterKeyAudit(path, false, fmt.Sprintf("lstat: %v", lErr))
		return nil, fmt.Errorf("key file lstat %q: %w", path, lErr)
	}
	isSymlink := lInfo.Mode()&os.ModeSymlink != 0

	// Stat (follows symlinks) for the authoritative perm check on the target.
	// #nosec G703 -- same as the Lstat above.
	info, err := os.Stat(path)
	if err != nil {
		emitMasterKeyAudit(path, false, fmt.Sprintf("stat: %v", err))
		return nil, fmt.Errorf("key file stat %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		emitMasterKeyAudit(path, false, "not a regular file (target)")
		return nil, fmt.Errorf("key file %q target is not a regular file", path)
	}

	// Strict 0600: any bit outside owner-read+owner-write (0o600) is a refusal.
	// `mode &^ 0o600 != 0` is equivalent to "are there bits set that are not in
	// the 0o600 mask?". Catches 0o644 (world+group read), 0o640 (group read),
	// 0o604 (world read), 0o700 (owner exec — no), 0o660 (group write), etc.
	perm := info.Mode().Perm()
	if perm&^0o600 != 0 {
		emitMasterKeyAudit(path, false, fmt.Sprintf("perm %04o not 0600", perm))
		return nil, fmt.Errorf("master key file %q must have mode 0600 (got %04o); refusing to load", path, perm)
	}

	// #nosec G304,G703 -- same path as the Lstat/Stat calls above: operator-
	// configured (env var or fixed default), already permission-validated
	// (strict 0600 check above this line) before this read.
	data, err := os.ReadFile(path)
	if err != nil {
		emitMasterKeyAudit(path, false, fmt.Sprintf("read: %v", err))
		return nil, fmt.Errorf("read key file %q: %w", path, err)
	}

	hexKey := strings.TrimRight(string(data), "\r\n")
	key, hexErr := hexToKey(hexKey)
	if hexErr != nil {
		emitMasterKeyAudit(path, false, fmt.Sprintf("invalid hex: %v", hexErr))
		return nil, hexErr
	}

	// Success path. Note the symlink fact in the audit detail so an operator
	// reviewing logs can spot a deployment that points at a managed-by-Vault
	// symlinked key file without alarm.
	if isSymlink {
		emitMasterKeyAudit(path, true, fmt.Sprintf("loaded via symlink, target perm %04o", perm))
	} else {
		emitMasterKeyAudit(path, true, fmt.Sprintf("perm %04o", perm))
	}
	return key, nil
}

// emitMasterKeyAudit logs a master.key load attempt at the slog level. The
// audit subsystem may not yet be initialized when Unlock runs — this happens
// during boot before the audit Logger is constructed — so we use slog with
// structured fields that downstream parsers (and operator log aggregation)
// can reconstruct into an audit record.
//
// Threat note: every load attempt is recorded, success or failure. An attacker
// who provisioned a 0644 master.key to bypass mode-0600 enforcement leaves a
// loud audit footprint on every boot.
func emitMasterKeyAudit(path string, success bool, detail string) {
	emitMasterKeyAuditRule(path, success, detail, "master_key_perm_0600")
}

// emitMasterKeyAuditRule is emitMasterKeyAudit with an explicit policy_rule.
// The rule name is what an operator triaging the alert reads first, so a
// refusal must not borrow a rule that did not fire — a boot stopped because two
// candidate keys were present is not a file-permission violation, and sending
// whoever is paged to `chmod` wastes the part of an incident where time matters.
func emitMasterKeyAuditRule(path string, success bool, detail, policyRule string) {
	event := "credentials.master_key_load"
	decision := "deny"
	if success {
		decision = "allow"
	}
	// Use Info for success (routine boot event), Warn for denials (operator
	// attention required). Both go through the same slog handler so the audit
	// hook (when wired by the gateway) can pick them up.
	if success {
		// #nosec G706 -- `event` is the "credentials.master_key_load" local
		// constant two lines up (used as both message and message struct's
		// slog.Info first-arg by convention), not tainted; path/detail are
		// discrete slog fields, escaped by both log sinks (see the sink
		// detail noted on execproxy.go's SSRF-blocked log lines).
		slog.Info(event,
			"event", event,
			"decision", decision,
			"path", path,
			"detail", detail,
		)
		return
	}
	// #nosec G706 -- see the Info branch above; same event constant, same
	// structured-field reasoning.
	slog.Warn(event,
		"event", event,
		"decision", decision,
		"path", path,
		"detail", detail,
		"policy_rule", policyRule,
	)
}

// promptPassphrase reads a passphrase from the terminal without echo.
func promptPassphrase(prompt string) (string, error) {
	// #nosec G115 -- same as the Unlock TTY check above: os.Stdin.Fd() is a
	// small kernel-assigned FD, cannot overflow int in practice.
	fd := int(os.Stdin.Fd())
	fmt.Fprint(os.Stderr, prompt)
	raw, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr) // newline after silent input
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// PromptNewPassphrase prompts for a new passphrase with confirmation.
func PromptNewPassphrase() (string, error) {
	pass1, err := promptPassphrase("New passphrase: ")
	if err != nil {
		return "", err
	}
	if pass1 == "" {
		return "", fmt.Errorf("credentials: passphrase must not be empty")
	}
	pass2, err := promptPassphrase("Confirm passphrase: ")
	if err != nil {
		return "", err
	}
	if pass1 != pass2 {
		return "", fmt.Errorf("credentials: passphrases do not match")
	}
	return pass1, nil
}
