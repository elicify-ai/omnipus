# Sandbox limitations and known gaps

The Omnipus process sandbox is deliberately scoped. It applies Landlock (filesystem and TCP port rules) plus a seccomp BPF filter to the gateway process itself before any HTTP listener binds (the only earlier step is reserving the gateway's own loopback egress-proxy port, which adds no permission for sandboxed children), and inherits both layers to every forked child via Landlock's per-thread domain and `SECCOMP_FILTER_FLAG_TSYNC`. It does **not** attempt to be a container, a hypervisor, a network firewall, or a kernel-level network packet filter — destination-IP-level egress for compiled binaries spawned through `bash` (ADR-036 unified the retired `exec`/`workspace_shell`/`workspace_shell_bg` tools into it) is enforced in userspace by the SSRF checker and the egress proxy, not at the kernel layer. This page lists the specific places where enforcement degrades from kernel-level to application-level checks, or where a feature appears in the config but is unimplemented.

## Platforms with partial or no kernel confinement

Which backend you get is per-platform. On Windows there is no kernel sandbox at all: `sandbox.SelectBackend()` always returns the `FallbackBackend`. On Linux kernels older than 5.13, Landlock is unavailable and the same fallback applies. On macOS, Seatbelt confines the children Omnipus launches through its sandboxed execution path when enforcement is active, but the gateway process itself is never confined, and external command-line workers are not launched through that path; the fallback applies there only when `sandbox-exec` is missing or disabled. Whenever the fallback is in force, `applySandbox` follows the graceful-degradation path (`applyNonLinuxSandbox` in `pkg/gateway/sandbox_apply.go`): the gateway logs `sandbox.degraded` at boot and continues serving with application-level path checks only.

### No Landlock filesystem confinement

Outside Linux there is no Landlock filesystem confinement. On macOS, children are kernel-confined by a Seatbelt profile when sandboxing is in enforce mode; the gateway itself never is, and enforce mode is required — permissive mode installs no Seatbelt profile at all. Tool path-guard checks (`pkg/tools/path_audit.go`, the per-tool allow-list inside each builtin) are still enforced in Go, but a compromised tool that bypasses the Go check has no kernel net to catch it on Windows, on old-Linux fallback, or on the unconfined macOS gateway.

### No seccomp filter

The deny-list of dangerous syscalls (`ptrace`, `mount`, `bpf`, `kexec_load`, `init_module`, etc.) is a Linux-only mechanism and is not installed on any other platform — see `pkg/sandbox/seccomp_linux.go:28-42` for the canonical list.

### No kernel-level port allow-list on Windows and old Linux

`cfg.Sandbox.DevServerPortRange` is honored by the gateway's own dev-server registry. On macOS the port allow-list is also enforced in each child's Seatbelt profile. On Windows and pre-5.13 Linux the kernel does not enforce it, so there a compiled child can bind any port the OS permits.

### Windows kernel sandbox unimplemented

Windows has no kernel sandbox: `selectBackendPlatform` (`pkg/sandbox/sandbox_other.go`) returns the `FallbackBackend` everywhere Windows runs. Job Objects are used, but only to cap a child's memory and to kill children when the gateway dies — not for confinement. A real confinement design exists (a low-integrity token plus deny rules on secrets, no admin rights needed) and is deliberately deferred.

`/health` and `/api/v1/security/sandbox-status` both report `backend: "fallback"` and `kernel_level: false` in this configuration, and the field `disabled_by: "kernel_unsupported"` may appear when the operator asked for `enforce` but the kernel cannot deliver it.

### Shell path guard: quoted-literal exception depends on root writability

The bash tool's command-text path guard (`pkg/tools/shell_path_guard.go`) can accept a stand-alone quoted absolute literal, such as `'/api/v1/users'`, when it meets the quote and surrounding-text conditions (no unquoted `{` or `}` may appear in the command or in shell text the guard can read — the `-c` script of a nested `sh`, `bash`, `zsh`, `dash`, `ksh`, `mksh`, `ash` or `fish`, or the arguments of `eval`, each recognised in any letter case — so no shell brace expansion such as `{'/tm',}p` can join it into a real path, and no backslash at the end of a line may continue the command outside quotes; any `<<` withholds the exception, even inside quotes or a comment, except the one plain here-document form: the whole command is one plain program name (letters, digits and underscores only, so `python3.12` does not qualify) that is not a recognised shell name or `eval`, optionally one space and a single `-` (the usual way to tell a program to read its script from standard input), one space, `<<'MARKER'` with a marker of letters, digits and underscores, the text, and the marker alone on the last line with nothing after it, as in `python3 <<'PY'` or `python3 - <<'PY'` (any other argument, such as `python3 -I <<'PY'`, or a second `-` does not qualify); inside that form the text is data, so braces, `<<` or a line continuation in it do not matter; when the guard cannot locate a nested shell's script with certainty, such as an unrecognised option before `-c` (plain short options and argument-less long options such as `--norc` are understood; `-o`, `--rcfile` and unknown options are not), or a script read from standard input (a pipe, an input redirection such as `bash<in.txt`, `-s` with or without positional arguments, a script file named `-` or `/dev/stdin`), the exception is withheld; a shell's arguments are read only from its own simple command, never from the command after a `;`, `&&` or `|`, and a redirection written against the shell's name (`bash>out.txt`) is read as a redirection, as the shell does. Shells are recognised by name only: a renamed or versioned shell such as `bash5` is not recognised, so its `-c` script is not read and `bash5 <<'PY'` keeps the exception, and a program that passes its input on to a shell is not followed. This is not a complete shell parser, so the exception is not a filesystem boundary), contains none of the template or glob characters, its top-level folder does not exist, and the gateway's effective user cannot write the filesystem root (`pkg/tools/shell_standalone_literal.go`).

On Windows the root always counts as writable, so this exception is disabled. On Unix the decision uses effective write access, not the account name: root on a writable filesystem disables it, while a read-only root can permit it. If the root check itself fails, the exception is off for that command and the cause is logged.

With the workspace-path guard enabled, `../` text is still refused rather than resolved. Joined, formatted or brace-expanded outside-path fragments do not gain the exception. The guard examines command text, not the contents of a script file or paths assembled by a running program.

## External command-line workers run outside the Omnipus sandbox

A worker that runs on Claude Code, Codex or opencode, and **Check CLI** under **Settings → Third-party agents** (`POST /api/v1/system/cli-validate`), start their program directly, not through the sandboxed execution path, on every platform. Omnipus scrubs the program's environment and forces a worker's HTTP and HTTPS traffic through the runner egress proxy, but it applies no Landlock, seccomp or other filesystem confinement of its own: the tool relies on its own sandbox, and a working folder is not a boundary. Restrictions the program inherits from the gateway still apply: on Linux, the gateway's installed system-call filter applies to it too. Claude Code runs with its own permission prompts turned off, and a Codex standard parameter such as `--dangerously-bypass-approvals-and-sandbox`, set under **Settings → Third-party agents**, removes Codex's own sandbox, so Codex can then read and write anything the operating-system account running Omnipus can. The standard parameters are person-only, not passed through the safety filter (only a model option is dropped, when Omnipus passes the worker's own model), and each run that uses them is audited; see [security](../security.md#third-party-agent-programs-and-parameters). The program checks decide who may set or check a program and which file may start (see [security](../security.md#checking-an-external-command-line-program)); they do not confine the program once it runs. Their list of folders agents can write to also includes the operating system's temporary folder when it is absolute and is not the shared `/tmp` or `/private/tmp`, together with its resolved spelling. On macOS this is normally the folder named by `TMPDIR`; Windows uses its own temporary-folder selection. Shared temporary folders are not added automatically by this rule, but locations there are still checked when another configured root covers them. This reflects the normal enforcing per-turn sandbox policy, which removes the blanket shared-temp write grant. It is not a promise of protection in God Mode or without an enforcing sandbox.

## Document preview programs

The programs behind document previews (LibreOffice, the PDF renderer and the
PDF text extractor; see [Documents in chat](../documents.md)) run as separate,
resource-limited processes. Their memory limit differs by platform:

| Platform | Memory limit |
|---|---|
| Linux 4.7 and later | Kernel-enforced data limit (`RLIMIT_DATA`): counts the private writable memory a program uses, not address space it only reserves; plus a stack limit of 8 MiB (never above the inherited one). Shared writable memory is not counted. |
| Linux older than 4.7, or booted with `ignore_rlimit_data` | The limit is set but the kernel does not enforce it on `mmap`. Every preview result says `memory limit not enforced: <reason>`, and the gateway logs it at start. |
| macOS | Best effort: a watchdog samples memory use and stops a program above its budget; use may briefly exceed it. |
| Windows | Job Object memory limit; there is no file isolation. |

When the kernel sandbox cannot be set up for these programs, they run with
the limits only and the result says `converted without sandbox: <reason>`.

When the kernel sandbox is active, LibreOffice can read only its own program
files, the folder that holds the Omnipus program, the system libraries and
fonts, and the temporary folder of the current preview. On Linux it can also
read the random-number source (`/dev/urandom`) and the local list of user
accounts (`/etc/passwd` and `/etc/nsswitch.conf`). On macOS it can also
read, but not change, the checked list of LibreOffice parts it may load, and
it can check that the folders above these exist, without seeing what is in
them. It cannot read your files, the agents' files, other previews or anything
else Omnipus stores. Checking a folder shows when something in it was last
added or removed, but not what. On Linux the kernel sandbox cannot stop any
program from checking whether a file or folder exists; LibreOffice still
cannot list folders or read files outside the folders and files named here.

**LibreOffice on Linux.** LibreOffice keeps its control connection point in the
shared `/tmp` folder. While it converts a document, it may create connection
points there, and it may delete files and connection points (not folders) in
`/tmp` and its sub-folders that the account Omnipus runs as is allowed to
delete. A document crafted to take over LibreOffice could use this during the
preview to delete such files, including other programs' connection points, and
to put its own connection point in their place. It cannot read, list or change
what is inside files there, and it cannot create files, folders or links there.
If the Omnipus data folder lies under `/tmp`, it is inside this delete right
too, so keep the data folder outside `/tmp`.

LibreOffice also reads the list of user accounts (account names, home folders
and login shells; no passwords, and every account on the computer can already
read it), so that its connection point belongs to the account Omnipus runs as
instead of being open to every account on the computer.

With `sandbox.filesystem_model` set to `confined`, Omnipus's own sandbox on
Linux is set up at startup with these extra permissions for document previews,
and nothing more:

| Extra permission | What it covers | Why |
|---|---|---|
| Read | The list of user accounts (`/etc/passwd`) and the system font folders (`/usr/share/fonts` and, when it exists, `/usr/local/share/fonts`) | Previews keep the installed fonts, and LibreOffice's connection point stays private to the account Omnipus runs as |
| Read and run | The Office preview and PDF preview program folders installed with Omnipus: `/usr/share/omnipus/office` and `/usr/share/omnipus/pdfium` for the `.deb` and `.rpm` packages, `/usr/local/share/omnipus/office` and `/usr/local/share/omnipus/pdfium` for the one-line installer's default and the Docker image, `<install folder>/../share/omnipus/office` and `…/pdfium` for another installer folder | Without it, the preview programs cannot read their own files in this mode and every preview fails |
| Read and run | Each folder registered with `omnipus office register` or `omnipus pdf-renderer register`; on Linux only a folder inside Omnipus's own folder can be registered (`/usr/share/omnipus` for the `.deb` and `.rpm` packages, `/usr/local/share/omnipus` for the one-line installer's default and the Docker image, `$HOME/.local/share/omnipus` for the per-user installer folder `$HOME/.local/bin`, `<install folder>/../share/omnipus` for another installer folder) | The same, for a copy you installed yourself |
| Read and run | The Omnipus program file itself (only the file, not its folder) | Omnipus starts each preview program through itself |

Only folders that are installed when Omnipus starts get these permissions, and
never for writing. The folders around them are not included (for example not
the whole `/usr/local/bin` or `/usr/share/omnipus`). The whole file system
(`/`), a protected system folder such as `/etc`, and any folder that lies
inside or contains a folder Omnipus or an agent can write (the Omnipus data
folder, `/tmp` and folders allowed for writing) are refused, and a preview
that needs such a folder says so. These permissions belong to Omnipus's own
startup sandbox only: the separate sandbox Omnipus builds for each agent shell
command never includes them, and agents cannot read or run the preview
programs, with two exceptions:

- The real folder that holds the preview programs, after any links are
  followed, lies inside a system folder that agents can already read and run,
  such as `/opt`, `/usr/lib`, `/usr/lib64`, `/lib`, `/lib64` or `/usr/bin`.
  What counts is where Omnipus's own folder (`<install folder>/../share/omnipus`)
  or a packaged program folder (`<install folder>/../office`, `…/pdfium`)
  really is, not where the `omnipus` program file is. This happens, for example, when
  the installer is run with `OMNIPUS_INSTALL_DIR=/opt/omnipus/bin`, or when
  `/usr/local/share/omnipus` is a link to a folder under `/opt`. Agents can
  then read and run the preview programs through that folder. The standard
  installs, left as installed, are not this case: `/usr/bin/omnipus` with
  `/usr/share/omnipus` for the `.deb` and `.rpm` packages,
  `/usr/local/share/omnipus` for the installer's default folder and the
  Docker image, and `$HOME/.local/share/omnipus` for the per-user folder, as
  long as no link moves these folders into such a system folder.
- You give agents that access yourself: a `sandbox.allowed_exec_paths` entry,
  or an approval that lets a command read or run there, gives it to the
  commands it applies to.

The default `open` mode needs none of this, because it already allows reading
and running programs everywhere.

At start, the gateway logs one `sandbox.component_grants` line naming each
folder it granted and each refused or skipped entry with its reason.

A preview program installed, registered, moved or reinstalled while Omnipus
runs is used only after Omnipus is stopped and started (for example with your
service manager). The in-app restart is not enough in the confined sandbox,
because the restarted Omnipus can keep the permissions of the first start.
Until then a preview says so, and `omnipus office register` and
`omnipus pdf-renderer register` print a reminder. If a program was added after
the first start and Omnipus was then restarted from inside the app, a preview
says: `Office preview cannot read its program files (<reason>). If Omnipus was
restarted from inside the app after this component was installed or registered,
stop and start Omnipus (for example with your service manager); otherwise
reinstall or re-register the Office preview component.` The PDF renderer says
the same in its own words.

A stricter setup is not switched on yet (issue #1856): a separate private folder for each preview, and a filter that stops LibreOffice from creating network connections of its own.

Agents can never get write access to these programs' folders: shell approvals
(**Allow** and **Allow once**), mounts (including one that contains a program
folder, such as `/`) and new or changed write allowances that would give it, or
that Omnipus cannot reduce to a folder, are refused where they would be created
(the texts are in [Documents in chat](../documents.md#what-previews-need)).
Access already in force is not refused: before every preview Omnipus checks for
it and turns the component off for everyone.

Limits of this protection:

- On Windows, and when these programs run without the sandbox, the kernel does
  not stop an agent's `bash` from writing an install folder. The refusal covers
  only the writes Omnipus can see in the command text.
- The shell refusal is part of the Auto-approve check. In God Mode that check
  does not run.
- A command that is still running and was given write access to a folder before
  that folder became a program folder keeps that access. So does a command
  still running after its mount was removed.
- A folder agents can already write that is redirected by a link into a program
  folder while a preview runs is seen by the next preview, which refuses; the
  running preview is not stopped.

If Omnipus stops during a preview, its next start ends the preview programs
that were left running and removes their temporary folders. It acts only on a
record Omnipus wrote outside the programs' own folder, and it stops a program
only when its process ID, start time and program file all match that record.
Limits of this cleanup:

- On Windows, and when the programs run without the sandbox, nothing keeps the
  programs from changing that record. This gives them nothing they could not
  already do: they then run with Omnipus's own account and could stop those
  processes and delete those folders themselves.
- A program that left its process group, or a group whose first process has
  already ended, is not stopped. A process that no longer matches its record
  (for example a reused process ID) is not stopped either: its folder is kept
  and the gateway log reports it at start.

## Landlock rights are gated by kernel version

Omnipus asks the kernel for a set of Landlock rights when it applies the sandbox. Each right arrived with a specific kernel version, so an older kernel may not know all of them. The ABI (application binary interface — here, the version of Landlock's interface your kernel exposes) is reported by the kernel; Omnipus asks only for the rights that version supports.

| Right | Kernel that introduced it |
|---|---|
| Filesystem access (read, write, create, remove, and the like) | 5.13 (ABI v1) |
| REFER — linking or moving files | 5.19 (ABI v2) |
| TRUNCATE | 6.2 (ABI v3) |
| Network rules (TCP bind and connect) | 6.7 (ABI v4) |
| Device controls (ioctl) | 6.10 (ABI v5) — recognised, not yet requested |

If the kernel rejects the ruleset anyway — it reports an ABI it does not actually enforce — Omnipus no longer refuses to boot. It falls back to application-level checks, logs a `sandbox.degraded` warning naming the kernel and its ABI, and reports the fallback truthfully in the sandbox status, each agent's environment preamble, and `/health` (`applied: false`). A ruleset rejection that is not a rights-support problem — a malformed rule, a net-port rule rejected on a capable kernel, or a `restrict_self` failure — still stops the boot, as before.

## Kernels 5.13–5.18 (Landlock ABI v1): handled in code, not run on real hardware

Omnipus is tested on Linux kernels 6.x. Kernel 5.13–5.18 (Landlock ABI v1) — notably Ubuntu 22.04's original 5.15 kernel — is handled in code and covered by unit tests, but has not been exercised on a real ABI v1 kernel. Docker, Fly.io machines, and GitHub's `ubuntu-22.04` runner all provide a modern kernel (6.8–6.17) regardless of the distribution image, so no available environment reproduces ABI v1.

Running Omnipus on such a kernel is not recommended. Use kernel 5.19 or newer for kernel-level file protection. On an older kernel you get application-level checks only: a program that ignores those checks is not contained by the operating system.

To see what you have: open the Security screen (or the sandbox status), and look for the `sandbox.degraded` warning in the log.

## Permissive mode on kernels < 6.12

Linux did not gain a native permissive Landlock semantic until 6.12. On older kernels (which covers most production hosts as of mid-2026), `sandbox.mode = "permissive"` follows the audit-only degradation path in `pkg/sandbox/sandbox_linux.go:367-383`.

The Landlock ruleset is built and rules are added, so any malformed rule still surfaces at boot. `PR_SET_NO_NEW_PRIVS` is set (required for the seccomp filter). `landlock_restrict_self` is **deliberately skipped** — calling it would enforce the policy, the opposite of what the operator asked for. The seccomp BPF program is installed with `SECCOMP_RET_LOG` instead of `SECCOMP_RET_ERRNO(EPERM)` (`pkg/sandbox/seccomp_linux.go:64,139,148-153`), so blocked syscalls proceed but emit a kernel audit entry.

The gateway logs `sandbox.permissive.downgraded` at INFO with the kernel ABI version, and `PolicyApplied()` still returns `true` so the status endpoint correctly reports "policy was computed and logged". On kernel 6.12 and newer the path is true log-then-enforce. Plan for that kernel floor if you need permissive mode to behave as a real audit-only stepping stone before enforcement.

## SSRF allowlist limitations

`cfg.Sandbox.SSRF.AllowInternal` is the operator-controlled allow-list that exempts entries from SSRF blocking. The parser in `pkg/security/ssrf.go:95-117` accepts exactly three forms:

| Form | Examples |
|---|---|
| Exact IPv4 or IPv6 address | `127.0.0.1`, `::1`, `192.168.1.5` |
| CIDR range | `10.0.0.0/8`, `fc00::/7` |
| Exact hostname (case-insensitive) | `localhost`, `internal.corp` |

Glob host patterns are **not** supported. `*.internal.corp` will be stored as a literal hostname and will match nothing at lookup time. There is no plan to add glob support in v0.1; operators with many internal hosts should put them behind a single CIDR or enumerate them explicitly.

The CIDR allow-list interacts with the kernel layer asymmetrically. Landlock `NET_CONNECT_TCP` (ABI v4+) filters by destination port only — the kernel cannot match by IP. So a compiled binary spawned through `bash` (ADR-036 unified the retired `exec`/`workspace_shell`/`workspace_shell_bg` tools into it) that dials `https://192.168.1.1/` on port 443 is permitted by the kernel (443 is on the connect allow-list for legitimate HTTPS) and only the gateway-controlled HTTP clients (`search_web`, `fetch_url`, the skills installer, MCP fetches) see the CIDR filter via `SSRFChecker`. This gap is documented in `pkg/config/sandbox.go:248-257`; operators concerned about it should set `bash`'s tool-policy to `deny` or `ask` (CLAUDE.md hard constraint 6 — `bash` has no feature-flag gate, only an explicit per-agent tool-policy entry) on agents that handle untrusted content.

## Container detection blind spot

Container detection cannot distinguish one deployment shape from bare metal: a cgroup-v2 pod in its own cgroup namespace, with service links disabled and no `/.dockerenv`. Its cgroup path is only `0::/`, which is also valid on a bare-metal system. Omnipus therefore may not warn that an unlimited container is sizing work against the node's memory.

Set `OMNIPUS_CONTAINERIZED=1` for this deployment shape. This declares that Omnipus is containerized so the missing-memory-limit warning can run; it does not set a memory limit. Configure the limit in the container runtime as well.

## Production warning banner

When `OMNIPUS_ENV=production` is set in the environment and `sandbox.mode` resolves to `off` or `permissive`, the gateway prints a multi-line banner to stderr at boot and then every 60 seconds thereafter. The banner is intentionally loud and unmissable in journald or Docker logs.

Two variants exist in `pkg/gateway/sandbox_apply.go:163-183`.

### `productionNagBanner`

Fires when `mode=off` and `OMNIPUS_ENV=production` (`pkg/gateway/sandbox_apply.go:257-263`). Reads: `SANDBOX DISABLED IN PRODUCTION ENVIRONMENT`.

### `permissiveNagBanner`

Fires whenever `mode=permissive`, regardless of `OMNIPUS_ENV` (`pkg/gateway/sandbox_apply.go:417-428`). Reads: `SANDBOX IN PERMISSIVE MODE — NOT ENFORCED. DO NOT USE IN PRODUCTION.`

The 60-second repeat is implemented by `StartNagBanner` (`pkg/gateway/sandbox_apply.go:505-540`), a goroutine started after boot and stopped at shutdown. There is no environment variable, config flag, or runtime API that silences the banner — by design. The repeat interval is hardcoded; operators who find the banner spamming their logs should fix the underlying configuration rather than try to suppress the output.
