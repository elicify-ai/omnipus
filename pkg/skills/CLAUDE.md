# pkg/skills — skills, registries, embedded defaults

## Running tests here

Scope to one symbol (`CGO_ENABLED=0 go test -tags goolm,stdjson -run
'^TestDefaultSkills_EmbeddedAndSeeded$' -p 1 ./pkg/skills/`) — the embedded
default set is compiled in and seeded into an empty skills dir on first boot.
CI is the authority for full-suite results.

## Fresh installs carry embedded skills

`embed.go` compiles a default skill set into the binary
(`//go:embed all:embedded`: daily-briefing, define-goal, plan,
skill-authoring, summarize) so a fresh install works offline. Adding a default
skill means adding it under `embedded/` — shipping it only to a marketplace
leaves fresh installs without it.

## The per-turn menu cap is removed — do not reintroduce it

`mount_threshold.go` (ADR-072 D1.2) only WARNS at mount-creation time when a
mount's recognised skills directory would contribute more than 500 discovered
skills. Nothing in this package ever truncates the per-turn skill menu
(FR-076); the old cap (D1.1) was removed. A threshold that silently drops
skills hides them from the agent with no error anywhere.

## Install integrity fails closed

Installs pin a sha256 manifest hash and honour the configured trust policy
(`wave3_hash_trust_test.go`); a pinned hash that mismatches the downloaded
artifact fails the install. Zip extraction and uninstall are
traversal-guarded (`wave3_zip_security_test.go`,
`uninstall_traversal_test.go`) — keep extraction and removal inside the
skills root; a path escape here is arbitrary file write outside the sandbox.
