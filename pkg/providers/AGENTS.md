# pkg/providers — LLM provider adapters and catalog

## Running tests here

Scope to one symbol (`CGO_ENABLED=0 go test -tags goolm,stdjson -run
'^TestAdmit_AgainstTheEmbeddedSnapshot$' -p 1 ./pkg/providers/`) — model
admission against the embedded catalog snapshot, hermetic by construction.
CI is the authority for full-suite results.

## Deleted provider ids leave no trace (ADR-068 §2.4)

The `antigravity` and `claude-cli` provider ids are deleted — greenfield, no
alias, no shim, no migration, no error string naming them.
`scripts/check-no-removed-providers.sh` fails the build on any trace, in
content OR file name. A Go test cannot even spell the ids without becoming a
trace itself, so tests assemble them from fragments — copy the pattern in
`factory_removed_ids_test.go::removedIDFragments` instead of typing the ids.

Scope, so the rule is not over-applied: the OpenAI device-code/OAuth login is
RESTORED (ADR-068 §8b, 2026-08-23) and its symbols are legal; the
Anthropic/Claude store-OAuth ladder (`createClaudeAuthProvider`,
`createClaudeTokenSource`) is still deleted and still checked. Branches cut
before a deletion re-add it as an ordinary conflict-free addition — resolve by
keeping the deletion.
