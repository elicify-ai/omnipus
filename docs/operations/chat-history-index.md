# Repairing a saved chat history index

The history index is disposable metadata, not a second copy of the conversation.
Use the separately built **chat-history-index-repair** command to prepare existing
saved chats or repair a missing/inconsistent index. It reuses the product's Go
index and projection engine. It is not installed with Omnipus and is not an
Omnipus product command, HTTP endpoint or automatic startup migration.

**Do not use this runbook on live data until the candidate has passed independent
review, native-platform tests and the copied-data rehearsal.** A successful
command is not proof of user-interface acceptance or exclusive access to data.

## Safety decision: checks, not a maintenance lock

**F1=A is an ACCEPTED, FOUNDER-DECIDED residual risk.** The squad recommended B,
a shared per-data-root maintenance lock. The founder declined that additional
production scope. This command checks native process identity, receipts and
source fingerprints before and after publishing indexes. Those observations do
**not** exclude another writer. An unexpected writer can race between checks;
some interleavings can be detected only after publication or remain undetected.
There is no new startup guard, lock, transferable lease or automatic restore.

Team-lead owns the live procedure. Arrange a quiet window, tell the **chief not
to touch the instance or its data**, and keep other writers and automation quiet.
On the founder's macOS installation, identify the actual live launchd service
and use **`launchctl bootout` on that verified service target** so KeepAlive
cannot restart it. Do not guess the service label/domain. Sending a signal,
killing the process, or ordinary stopping while its launchd job stays loaded is
not sufficient. The repair command never runs launchctl or stops a process.

## Build and rehearse first

A compatible Go toolchain (the version in the candidate's Go module), a native
Linux/macOS/Windows environment and access to the complete instance data are
operator prerequisites. The command needs no running gateway, model, credentials
unlock, Python runtime or embedded web application. Python is used by the CI
wrapper only, not by the built repair executable.

From the checked-out candidate, the explicit `history-index-repair` Make target
builds the operator executable only. It is not a dependency of a product build,
installer or release target. Replace `/path/to/candidate-checkout` with the
checkout of the exact candidate being repaired:

```sh
make -C /path/to/candidate-checkout history-index-repair
```

The native output is
`/path/to/candidate-checkout/build/tools/chat-history-index-repair`
(with `.exe` on Windows). Build the tool and read the schemas below from that
same candidate checkout. A tool built from an older checkout refuses every
symbolic link (`unsafe_kind`) and writes version 1 manifests, which this
version refuses (`unsupported_manifest_version`). The underlying recipe uses `CGO_ENABLED=0` and Go tags
`goolm,stdjson`. Build from a Git checkout: the command reads its real embedded
Go VCS revision for `tool_commit`; it accepts no caller-supplied commit stamp.

Before live use, rehearse with copied canonical data and the real native-child
fixture, not invented stopped evidence. The command-package tests build a tiny
native child that writes the real gateway PID file, signals readiness over a
pipe, and waits for input. They capture it while running, require stopped-receipt
to refuse, then wait for its actual clean exit (PID removal) or crash exit 86
(stale PID). They produce a fresh full backup, exercise the actual CLI apply and
verify, check the real shared reader and repeat without changing index bytes or
times. Native Linux and Windows CI emit named test counts; a cross-build is not
native certification. macOS needs its own executed rehearsal. A written test,
synthetic-only success or direct publisher call is not that evidence.

## Explicit paths and phase interface

Every filesystem operand must be an explicit canonical absolute native path.
There is no default home. The full backup must be separate from, and neither
inside nor an alias of, the source. Put binding, receipt, manifest, optional plan
and optional scratch outside **both** trees. Resolve paths before use.

File and folder names are recorded exactly as the operating system stores
them. On Linux and macOS any character except `/` and the NUL byte is a valid
name character, so names with `:`, `\`, an em dash, or a trailing space or dot
(for example a vault note called `Programme — AI Architect: Innovating
Business Processes`) are inventoried and backed up, never refused. Still
refused with `unsafe_path`: names that are not valid UTF-8 text (the manifest
stores names as JSON text), a NUL byte, `.` or `..` parts, and two names that
differ only in letter case. On Windows a manifest entry whose name Windows
cannot hold (control characters, `\ : * ? " < > |`, or a trailing space or
dot) is refused with `non_native_name` — for example a manifest written on
macOS and read on Windows.

Symbolic links and special files (sockets, named pipes, devices) are handled by
where they sit in the data root:

| Location | Handling |
| --- | --- |
| Under the data root's top-level `sessions/` folder (name matched ignoring letter case) | Refused with `unsafe_kind` (exit 2). The command reads this subtree canonically and writes indexes into it. |
| Anywhere else (for example a workspace's link to the user's Desktop, browser-profile `Singleton*` links, toolchain `.bin` links) | Recorded, never followed and never opened. A link is inventoried as itself — its exact target text, permission mode and its own modification time — and nothing behind it is inventoried, even when it points outside the data root. A special file is inventoried by its type, mode and modification time. |

The backup must therefore preserve links **as links** with the identical target
text and modification time, and recreate special files as the same type,
rather than following or skipping them. A missing link, a different target, or
a regular file or folder in a link's place refuses the manifest
(`backup_incomplete`) and later verification (`backup_tampered`). On macOS,
`cp -a` was checked to preserve link targets, link modes and nanosecond link
modification times, and to copy named pipes; it does **not** copy sockets (it
prints `is a socket (not copied)` yet still exits 0), and the bundled
`openrsync` drops sub-second times, so a copy made with it is refused with
`backup_incomplete` (the manifest compares nanosecond times). If the stopped instance's data root still
contains a socket, a `cp -a` copy lacks it and the manifest step refuses with
`backup_incomplete` until the copy holds a socket at the same path with the
same mode and modification time.

The examples below use isolated workspace locations. Set the variables to the
verified instance, its actual native executable and separate evidence locations;
these examples are not instructions to select a live home automatically.
The command resolves native path aliases before containment checks (including
macOS's `/var` to `/private/var` alias). Evidence binds the resulting canonical
location; an alias resolving into the source or backup is still refused.

```sh
TOOL=/path/to/candidate-checkout/build/tools/chat-history-index-repair
ROOT=/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/fixtures/rehearsal/source
BACKUP=/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/fixtures/rehearsal/backup
GATEWAY=/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/fixtures/rehearsal/omnipus
BINDING=/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/repair-evidence/rehearsal/instance-binding.json
RECEIPT=/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/repair-evidence/rehearsal/stopped-receipt.json
MANIFEST=/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/repair-evidence/rehearsal/backup-manifest.json
OPERATOR=Daniel-Piatkowski
```

Create designated evidence directories before writing outputs. Record the exact
candidate SHA, native platform, selected instance/service identity and quiet
window with the evidence. Do not publish the backup or private evidence files:
they contain operator paths, process identities and full inventory digests.

## Live procedure: one apply per planned attempt

| Step | Operation and required evidence |
| --- | --- |
| 1. Dry-run first | `"$TOOL" dry-run --data-root "$ROOT"`. No prior backup or stopped receipt is required. Default dry-run writes nothing anywhere; it prints deterministic session plans and refusal classes. A plan from a running instance is advisory, not a frozen source snapshot. |
| 2. Handle unconverted data first | An `unconverted` refusal (exit 2) means use the compatible product's **normal boot**, which owns the established saved-chat cutover, before indexing. The repair command never runs conversion. If normal cutover fails, stop and report it. Preserve any pre-cutover backup as rollback evidence, then repeat dry-run and take a **fresh post-cutover authoritative full backup**. Never index half-converted data or use the old backup as the new equality baseline. |
| 3. Capture while running | `"$TOOL" stopped-receipt --capture-instance --data-root "$ROOT" --gateway-executable "$GATEWAY" --operator "$OPERATOR" --output "$BINDING"`. This requires the actual existing gateway PID file and a positively identified running native process. It is an instance binding, **not** a stopped receipt. |
| 4. Full count-verified backup | Take an actual complete instance copy, preserving every file's bytes, permissions and modification time and the directory inventory, with links copied as links (not followed) — see the table above. Include configuration, encrypted credentials, uploads, session metadata, archives, dispositions and preexisting indexes. The command does **not** make the copy. Run `"$TOOL" manifest --data-root "$ROOT" --backup-root "$BACKUP" --output "$MANIFEST"` against that real copy. It prints `files`, `directories`, `symlinks`, `special` and total `entries` counts. Record independently matching counts as well as hashes/modes/times/link targets, not just a success flag. |
| 5. Quiet window and actual stop | Tell the chief not to touch the instance/data. On macOS, boot out the verified live launchd service so KeepAlive cannot restart it. Independently observe actual process exit. On every platform disable the applicable restart authority operationally; the command does not inspect or control service managers. |
| 6. Recheck stopped source | Graceful PID removal and shutdown writes invalidate a backup made while running. If equality changed, refuse; repeat dry-run and take a fresh complete count-verified backup/manifest **while stopped**. There is deliberately no PID-file exclusion. Do not edit a manifest to hide drift. |
| 7. Produce stopped receipt | `"$TOOL" stopped-receipt --data-root "$ROOT" --instance-binding "$BINDING" --operator "$OPERATOR" --output "$RECEIPT"`. The same root object and bound native process must be checkable. A missing PID file or bootout's return code alone is not proof. The producer leaves either an absent PID file or the exact captured stale bytes unchanged. |
| 8. Apply once | `"$TOOL" apply --data-root "$ROOT" --backup-root "$BACKUP" --backup-manifest "$MANIFEST" --instance-binding "$BINDING" --stopped-receipt "$RECEIPT" --confirm-stopped`. Invoke once for this planned live attempt. Confirmation is acknowledgement, not proof. Every session is preflighted before any publication. Native/evidence/root and full backup/source checks run before and after each publication and at completion. Keep the quiet window in force. |
| 9. Independent post-check | `"$TOOL" verify --data-root "$ROOT" --backup-root "$BACKUP" --backup-manifest "$MANIFEST"`. Independently repeat canonical and backup inventory/count/hash/mode/time checks and shared-reader checks. Exit 0 from apply alone is insufficient. Inspect actual published/unchanged/refused counts, especially after interruption. |
| 10. Resume deliberately | Only after successful post-checks restore normal service operation and perform user-interface acceptance on the exact candidate. Keep backed-up data and the command/verification evidence according to the operator's retention policy. |

### Failed post-check or damage

Keep the instance stopped, the macOS job booted out and all writers quiet. Preserve
the failure log and actual publication counts. If checks flag damage, team-lead
**restores the full verified backup**, then independently checks restored counts
and integrity and reruns dry-run. Restoration may replace the root object or
invalidate process binding, receipts and manifest/source equality. Re-establish
all invalidated evidence through the checked procedure before a **separately
deliberate rerun**. Never fabricate a dead PID or reuse mismatched evidence.
There is no automatic restore/retry loop in this tool.

## What each phase may write

| Phase | Permitted writes |
| --- | --- |
| `dry-run` | None by default, including no index, lock, source temp, chmod or log. Explicit `--plan-output` writes only its designated out-of-tree file; optional `--scratch-root` must also be out-of-tree. Read-access times are outside the zero-tool-write guarantee. |
| `manifest` | Only its explicitly named out-of-tree manifest; source and backup are read-only. |
| `stopped-receipt --capture-instance` | Only its out-of-tree instance binding. No PID cleanup, service action or store constructor. |
| `stopped-receipt` | Only its out-of-tree stopped observation. No PID cleanup or adopted PID. |
| `apply` | Only the designated derived history subtree within each session. No canonical archive, metadata, model window, credential, configuration, upload or backup writes. |
| `verify` | No writes and no hidden repair. |

Matching completed indexes are byte/permission/mtime unchanged on repeat. An
interrupted batch may have published some sessions; it is not whole-instance
atomic. Rerun only after reviewing the failure and revalidating every condition.
The shared engine safely replaces its own unfinished derived publication state
and retains a valid prior generation until a replacement is complete. Public
occurrence tokens remain stable only when final canonical bytes are unchanged.

## Private formats and full backup verification

The authoritative private JSON schemas ship alongside the command source:

| Schema | Contents |
| --- | --- |
| `/path/to/candidate-checkout/cmd/chat-history-index-repair/backup-manifest.schema.json` | Version 2; exact source/backup roots, UTC creation time, actual built-tool commit, sorted non-null complete entries and canonical fingerprint. Each normalized relative UTF-8 path has kind (`file`, `directory`, `symlink` or `special`), size, permission mode and nanosecond modification time as a lossless decimal string; files include their actual lowercase SHA-256, including the real empty digest for zero bytes. Directories have size 0 and no digest. A `symlink` has size 0, no digest and its exact `link_target` text (a target that is not valid UTF-8 is refused as `unsafe_path`). A `special` entry has size 0, no digest and a `special_type` (`named_pipe`, `socket`, `char_device`, `block_device` or `irregular`). Version 1 manifests (written before link support) are refused with `unsupported_manifest_version`; produce a new manifest. |
| `/path/to/candidate-checkout/cmd/chat-history-index-repair/instance-binding.schema.json` | Version/type, canonical root and PID-file path, native directory-object identity, actual positive captured PID, hash of exact PID-file bytes, native process-birth identity, native executable, UTC capture time, operator, actual tool commit and derived instance ID. |
| `/path/to/candidate-checkout/cmd/chat-history-index-repair/stopped-receipt.schema.json` | Version/type, matching root/instance/PID, hash of the actual binding bytes, absent/same-stale PID-file state, UTC native inspection time, operator and actual tool commit. |

Strict decoders reject unknown fields, nulls, duplicates, extra JSON objects,
unsupported versions and mismatching identities. Evidence files use checked
atomic replacement and private 0600 permissions where supported. These are
trusted-operator consistency receipts, not cryptographic attestations or leases.
Unknown, inaccessible, reused or ambiguous processes never count as stopped.
There is no `--force`, `--assume-stopped`, dead-PID or caller-commit override.

The manifest producer hashes the actual complete backup and compares full
source/copy inventories, including counts. Backup verification always checks
its saved full inventory, including any **old** index bytes. Current-source
equality excludes only designated per-session derived history subtrees, so the
same verified backup can support an idempotent index-only rerun. All canonical
files, including the gateway PID file, remain covered. The manifest step and
backup verification compare **directory** modification times as well, so a copy
that does not preserve folder times is refused at the manifest step with
`backup_incomplete` (and a copy whose folder times change later is refused by
verification with `backup_tampered`). Only
the comparison of the current source against the manifest ignores directory
modification times, because permitted index creation changes its parent
directory's time; directory names/kinds and modes still compare there.
The fingerprint's sorted length-prefixed tuple includes path, kind, canonical
size, digest and file mtime; directory mtime is encoded as empty. A `symlink`
tuple adds its link target and a `special` tuple adds its type, so a retargeted
link is detected as drift. It is not a
user assertion that a backup succeeded.

## Outcomes and exit codes

Per-session plans print session ID, action (`create`, `unchanged`, `rebuild`),
source-group count and partition count. A folder directly under `sessions/`
that the product's own session list skips is reported as
`session="<name>" action=skipped reason=<reason>` and counted in `skipped=`:
`meta_missing` when it has no `meta.json` (for example an out-of-band
`agent:<agent>:task:<task>` folder), `hidden_dir` when its name starts with a
dot (other than the legacy `.context` folder). A skipped folder is never
indexed and never refuses the run, but it is canonical data: it stays in the
full inventory, and a backup missing it is refused. A folder with
`context.jsonl` is unconverted data and is refused, never skipped. Task
sessions that do have `meta.json` (including `agent:…:task:…` names) are
ordinary sessions and are planned and published. Summaries include mode, session/action
counts, actual published/unchanged/refused/skipped counts and `canonical_writes=0`.
That last field describes the command's scope; verify it with independent tree
checks, not by trusting printed text. Output uses fixed refusal classes, never
canonical bodies, credentials, raw cursor values or physical archive addresses.

| Exit | Meaning and response |
| --- | --- |
| 0 | This phase completed with no refusal. It proves neither exclusive access nor completed UI acceptance. |
| 2 | A precondition refused: `arguments`, `unsupported_manifest_version`, `backup_missing`, `backup_incomplete`, `backup_tampered`, `backup_wrong_root`, `unsafe_path`, `non_native_name`, `unsafe_kind`, `stopped_evidence_missing`, `stopped_evidence_unknown`, `instance_running`, `source_changed` or per-session `unconverted`. Correct the cause and repeat the checked procedure, never bypass it. |
| 1 | Integrity or I/O failure: `corrupt_archive`, `invalid_reference`, `index_verification`, `io_error`, `evidence_invalid` (an evidence file the command was about to write failed its own schema check), interrupted publication or unavailable tool build identity. Preserve evidence and actual publication counts. |

A failed all-session preflight publishes no sessions. A detected late failure
can follow real publications, which are reported rather than described as zero.
Mixed refusals select exit 2 if any precondition/unconverted class is present;
otherwise integrity/I/O selects 1. Refused sessions never produce success exit 0.
After apply reaches its publication stage, the final safety guard still runs
when the publication loop ends with an error. Inventory, all-session preflight
and initial-guard failures can return earlier, before that stage. A final
failure whose fixed class has not already been reported adds another refusal
and participates in exit precedence: one publication followed by
`index_verification` and then `source_changed` reports
`published=1 unchanged=0 refused=2`, exit 2. Final-guard deduplication is by
**fixed failure class**, not equality of underlying causes. A final failure in
an already reported class is not counted again; later unattempted sessions do
not acquire fictional refusals or publications.

Offline reconstruction can ignore a missing settlement, projection or retraction
target only when the validated existing index proves its token/rank tombstone
and the captured retained catalog proves the target partition is absent. A
missing address or file by itself is not expiry proof. Unexplained missing
targets—including missing records inside a retained partition—still refuse as
`invalid_reference`, without inventing an orphan tool result or rewriting the
remaining canonical effect bytes.

## Runtime limits are different from offline repair

Ordinary operation may reconcile **at most 64** consistent mutation facts after
a known valid index checkpoint. It does not manufacture a baseline from missing
metadata, repeatedly walk a lifetime in batches, or start a preparing-history
worker. Larger/inconsistent gaps need this operator command. History unavailability
is not permission to cancel running work: current sending, approvals and Stop
remain separately available with an explicit history error.

Retention follows actual deletions. On native Windows, a reader-held sharing-busy
partition remains present/indexed, later eligible removals continue, and a later
normal sweep retries after the handle closes. Genuine permission or integrity
errors are not silently treated as expiry. Linux/macOS unlink/reselection is a
separate platform path. Native execution is required to certify these behaviors.
Expired occurrence fences retain **token and private rank only**; their content-free
ledger can grow with lifetime expired occurrences on disk, not as a lifetime
in-memory map. Rich expired metadata is retired; deleting the session removes
the ledger. Operator backup retention remains separate.
