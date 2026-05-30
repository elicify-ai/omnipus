// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package credentials

import (
	"crypto/rand"
	"encoding/hex"
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

	// DefaultKeyFileName is the filename used for the auto-generated master
	// key when no env-var or explicit key file is configured. It is placed
	// next to credentials.json inside the Omnipus home directory, so the
	// default permission model (0700 home dir + 0600 key file) matches the
	// SSH private-key threat model.
	DefaultKeyFileName = "master.key"
)

// Unlock attempts to unlock store using the following provisioning priority:
//
//  1. OMNIPUS_MASTER_KEY environment variable (hex-encoded 256-bit key)
//  2. OMNIPUS_KEY_FILE environment variable (path to a 0600 file with hex key)
//  3. Default key file next to credentials.json ($OMNIPUS_HOME/master.key, 0600)
//  4. Auto-generate a fresh key on a truly fresh install (no credentials.json
//     and no master.key) and write it to the default path with 0600
//  5. Interactive passphrase prompt (requires TTY; derives key via Argon2id)
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
// Implements US-4 acceptance criteria.
func Unlock(store *Store) error {
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
		slog.Debug("credentials: unlocked via OMNIPUS_KEY_FILE", "path", keyFile)
		return nil
	}

	// Mode 3: Default key file next to credentials.json. If the file exists,
	// load it; its failure is fatal for the same reason as OMNIPUS_KEY_FILE.
	defaultKeyPath := filepath.Join(filepath.Dir(store.Path()), DefaultKeyFileName)
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
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create key file %q: %w", path, err)
	}
	if _, writeErr := f.WriteString(hexKey); writeErr != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write key file %q: %w", path, writeErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("close key file %q: %w", path, closeErr)
	}

	// Operator-visible backup warning. Print to STDOUT (not stderr) so it
	// lands in systemd-journald / Docker logs on the operator's terminal.
	// The gateway's logger.initPanicFile dup2's stderr fd to a panic log
	// file before credentials.Unlock runs, so writes to os.Stderr at this
	// point go to $OMNIPUS_HOME/logs/gateway_panic.log — not the console.
	// Stdout is unaffected and is what systemd / Docker / tail watch.
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "================================================================")
	fmt.Fprintln(os.Stdout, "  Omnipus generated a new master key on fresh install.")
	fmt.Fprintln(os.Stdout, "  Key file: "+path)
	fmt.Fprintln(os.Stdout, "")
	fmt.Fprintln(os.Stdout, "  BACK THIS FILE UP. Losing it makes your encrypted credential")
	fmt.Fprintln(os.Stdout, "  store (API keys, channel tokens) permanently inaccessible.")
	fmt.Fprintln(os.Stdout, "================================================================")
	fmt.Fprintln(os.Stdout)
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
func loadKeyFile(path string) ([]byte, error) {
	// Lstat first to detect symlinks. A symlink's own perms are usually 0o777
	// on Linux and irrelevant to security; what matters is the target.
	lInfo, lErr := os.Lstat(path)
	if lErr != nil {
		emitMasterKeyAudit(path, false, fmt.Sprintf("lstat: %v", lErr))
		return nil, fmt.Errorf("key file lstat %q: %w", path, lErr)
	}
	isSymlink := lInfo.Mode()&os.ModeSymlink != 0

	// Stat (follows symlinks) for the authoritative perm check on the target.
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
	event := "credentials.master_key_load"
	decision := "deny"
	if success {
		decision = "allow"
	}
	// Use Info for success (routine boot event), Warn for denials (operator
	// attention required). Both go through the same slog handler so the audit
	// hook (when wired by the gateway) can pick them up.
	if success {
		slog.Info(event,
			"event", event,
			"decision", decision,
			"path", path,
			"detail", detail,
		)
		return
	}
	slog.Warn(event,
		"event", event,
		"decision", decision,
		"path", path,
		"detail", detail,
		"policy_rule", "master_key_perm_0600",
	)
}

// promptPassphrase reads a passphrase from the terminal without echo.
func promptPassphrase(prompt string) (string, error) {
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
