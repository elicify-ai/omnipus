# pkg/credentials — encrypted credential store

AES-256-GCM store over `credentials.json`; Argon2id only on the
passphrase path. Boot orchestration lives in
`pkg/gateway/gateway_boot_credentials.go::bootCredentials` (ADR-004) —
an Unlock failure aborts boot before the agent loop or listener exist.

## Running tests here

`CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run
'^TestMasterKeyPerm_0700_Refused$' ./pkg/credentials/`

## The unlock ladder — keymgr.go::Unlock, fixed order

1. `OMNIPUS_MASTER_KEY` env (hex, used directly — no KDF)
2. `OMNIPUS_KEY_FILE` env (a failure here is FATAL: headless cannot
   fall back to a prompt)
3. default `master.key` next to credentials.json (load failure fatal)
4. auto-generate — ONLY on a fresh install, gated on `!store.Exists()`;
   deliberately refuses to mint over existing encrypted data
5. interactive passphrase, TTY only — headless with no key is a hard
   error, not a fallback

There is no plaintext or dev mode. Modes 1–4 feed
`store.go::UnlockWithKey`; only mode 5 derives a key (Argon2id, salt
persisted in credentials.json).

## What losing master.key actually costs

On a readable current-format store, `UnlockWithKey`/`UnlockWithPassphrase`
install the key without authenticating entries. Gateway boot, reload and REST
refresh separately call `store.go::HealthCheck`: any decryptable entry proves
readability; no decryptable entry in a nonempty store is a whole-store failure.
Unlock itself rejects file-read and format-probe errors before installing a key.
(Exception: on a pre-upgrade version-1 store the
one-time migration opens every entry, so a wrong key fails AT unlock with a
`*MigrationError` — see "Format versions" below.) Every subsequent `Get` then fails with `store.go::ErrWrongKey`
(see gateway_boot_credentials.go::reportInjectionErrors for the
boot-visible shape). Rotation (`store.go::Rotate*`) re-encrypts by
decrypting first, so it cannot recover without the old key. Net:
credentials become permanently inaccessible — stated in
`keymgr.go::generateAndPersistMasterKey` and ADR-004. Worse trap:
re-entering a key under the wrong key encrypts it under the wrong key
permanently.

## Permissions and locking

- `master.key` must be exactly 0600 at load (`keymgr.go::loadKeyFile`
  refuses 0644, 0660, even 0700; symlink targets checked via Stat).
  Every load attempt is audited (`credentials.master_key_load`).
- credentials.json is WRITTEN 0600 (dir 0700) but has no read-time perm
  check — the 0600 gate is on master.key only.
- The flock is on a sidecar `credentials.json.lock`, never the store
  file itself: locking the store file created it EMPTY on first write,
  which then parsed as corrupted. `writeFileAtomicFn` is a package var
  solely so store_lock_test.go can park a write inside the lock.
- A corrupted credentials.json is a hard stop — the store refuses to
  overwrite it ("manual fix required").

## AAD name binding (issue #85b)

Every entry is sealed and opened with `store.go::aadFor(name)` —
`"omnipus-credential-v1:" + name` — as AES-GCM additional authenticated data,
so a ciphertext moved to another name fails authentication instead of
decrypting under the name it was moved to. Entry failures return
`store.go::EntryAuthError`, which names the entry and unwraps to
`ErrWrongKey`, so the boot fatal-vs-degrade classification is unchanged.
No per-read fallback: `decrypt` only ever uses `aadFor(name)`. Bump
`aadDomainTag` to change the AAD construction — ciphertexts sealed
under one tag cannot be opened under another.

## Format versions and the one-time migration (`store_migrate.go`)

`"version": 1` = pre-binding, nil-AAD entries; `"version": 2`
(`storeVersion`) = name-bound. Unlock migrates a v1 file before any other
read (`installKeyLocked` → `migrateLegacyLocked`; passphrase mode goes through
`UnlockWithPassphrase`, which re-keys under a FRESH salt and, if another
process won the race, re-derives from the salt now on disk). Every entry is
opened with nil AAD (or `aadFor(name)`: pre-release v1 files from
release/v0.1.1 dev installs, keep accepting them). Each is re-sealed under its
own name, and the whole file is written in ONE `writeFileAtomicFn` call under
the sidecar flock (version re-checked under the lock). Then
`credentials.json.migrated` is written and `credentials.store_migrated` is
logged (counts only). `openLegacyEntry` is the ONLY nil-AAD read in the
package. **TODO(credential-migration-removal, #847):** the whole migration must be
deleted in a later release (see the header of `store_migrate.go`).

- Any entry fails → nothing written, store stays LOCKED, `*MigrationError`
  names every failing entry and unwraps to `*EntryAuthError`s (→
  `ErrWrongKey`), so gateway boot STOPS (fatal class, not degraded). Audit
  records (`store_migration_refused`, and mode 0's `master_key_load` via
  `keymgr.go::auditVerifyCategory`) carry counts/categories, never names.
- v1 file + `.migrated` record present → `ErrLegacyStoreAfterMigration`.
- `loadFileInternal` refuses every version but 2 (`ErrLegacyStoreFormat`,
  `ErrUnsupportedStoreVersion`): the safety net. No read path can open a v1
  file and no write path can stamp one as v2. Unreadable/corrupt format probes
  fail unlock immediately, including when no config references would read the store.
- **Residual risk, real window = lifetime of the current secret** (master key
  or passphrase), NOT one unlock: an attacker with data-dir write access and a
  pre-upgrade copy can swap entries in it, delete `.migrated` (same dir), and
  the next unlock re-migrates and launders the swap. The fresh salt only stops
  mixing old entries with post-upgrade ones; changing the secret closes it
  (`TestMigrate_WholeOldCopyRestoreIsResidualUntilSecretChanges`); removing
  the migration closes it for everyone.
- Windows: `WithFlock` is a no-op, so two processes can both migrate; each
  writes a complete valid file, last write wins (no mix, no swap).

## Error classification decides fatal-vs-degrade

`store.go::HealthCheck` decides readability from the store itself, not from
which refs are configured. Boot, reload and REST refresh reject a locked,
unreadable or undecodable store, or a nonempty store with no decryptable entry.
An absent file or empty current-format store is healthy. A readable entry anywhere
in the store, even unreferenced, lets a single entry-auth failure degrade only
its integration; missing entries are scoped too. `inject.go::InjectFromConfig`
still returns bare `ErrStoreLocked` when locked. Unknown resolution errors are
returned to the caller, never reduced to log-only warnings.

## DeriveSubkey — the sanctioned key-material path

`store.go::DeriveSubkey` uses `hkdf.New` (RFC 5869 Extract-then-Expand
with a nil salt), never returns the master key itself, and rejects empty
`info` even when locked. The nearby comment says Extract is skipped; the
call is still `hkdf.New`, not `hkdf.Expand`. Do not "fix" it — that would
rotate every derived subkey (audit-chain HMAC included) and brick existing
installs. The audit-chain HMAC key comes from here
(`DeriveSubkey(audit.AuditChainKeyInfo)`).

OAuth entries follow `<lowercase provider>_OAUTH`
(`oauth_entry.go::OAuthEntryName`). Gateway boot sweeps orphaned ones
(`gateway_boot_credentials.go`); renaming the suffix here silently strands
them. `RegisterSensitiveValues` is on `config.Config`, not this package.
