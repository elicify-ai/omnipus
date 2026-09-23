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

Unlock still "succeeds" with a passphrase — `UnlockWithKey`/
`UnlockWithPassphrase` verify NOTHING against stored data; they just set
the key. Every subsequent `Get` then fails with `store.go::ErrWrongKey`
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
Greenfield, no fallback read: entries written before the binding carry a nil
AAD and no longer decrypt, so an existing install must re-enter its
credentials (worded for users in `docs/troubleshooting.md`). Bump
`aadDomainTag` to change the AAD construction — ciphertexts sealed
under one tag cannot be opened under another.

## Error classification decides fatal-vs-degrade

`inject.go::InjectFromConfig` returns the bare `ErrStoreLocked` (NOT a
`*CredentialRefError`) when locked, because callers use that distinction:
only a cause unwrapping to `*NotFoundError` degrades that one provider;
anything else (e.g. ErrWrongKey inside a ref error) is store-wide and
fatal.

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
