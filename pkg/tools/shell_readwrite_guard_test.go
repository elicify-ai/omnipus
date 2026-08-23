// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Tests for the ADR-068 read/write split in the bash command-TEXT path scan
// (guardCommand -> pathUseClassifier -> checkPathSegment).
//
// The decision being pinned (ADR-068 §2.1, founder-ratified option A): reads
// outside the working directory are ALLOWED; writes outside it still require an
// approved workspace mount. Before this change the guard treated both the same,
// which meant `bash cat ~/notes.txt` was refused while ADR-063 documented it as
// allowed and operators were creating WRITE mounts purely to obtain READ access.
//
// The oracle for every case below comes from that ruling plus the fail-closed
// constraint, never from reading the classifier:
//
//   - a command whose named outside path is provably only read  -> allowed
//   - a command that writes, or that MIGHT write, to an outside path -> blocked
//
// The second half is the important one. "Might write" covers unresolvable
// command names, unmodelled shell expansions, interpreters, unbalanced quotes
// and anything else this scanner cannot prove — all of which must keep behaving
// exactly as they did before ADR-068. TestGuardCommand_FailsClosedOnAmbiguity
// exists specifically so that a future "helpful" heuristic that starts guessing
// in the permissive direction fails here.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGuardCommand_ReadsOutsideWorkDirAllowed pins the ADR-068 §2.1 (A) ruling:
// a provable read of an absolute path outside the working directory is allowed,
// with no mount involved.
//
// Every case here was BLOCKED before ADR-068 — that is the intended behaviour
// change, and §1.1 of the ADR tabulates the first three by name as commands
// that were wrongly refused.
func TestGuardCommand_ReadsOutsideWorkDirAllowed(t *testing.T) {
	tool, cwd := guardFixture(t)

	cases := []struct {
		name string
		cmd  string
		why  string
	}{
		{
			name: "read of a system file",
			cmd:  `cat /etc/passwd`,
			why:  "the canonical case: ADR-062 opened reads, and ADR-068 aligns the bash guard with it",
		},
		{
			name: "home-directory tilde expansion",
			cmd:  `cat ~/.ssh/id_rsa`,
			why:  "`~/…` yields a fabricated candidate; it is still only a read, and reads are open (the secret set is a separate, unaffected carve-out)",
		},
		{
			name: "HOME variable expansion",
			cmd:  `cat $HOME/.ssh/id_rsa`,
			why:  "same shape via $HOME; the word-start scan must see past the expansion prefix",
		},
		{
			name: "listing two outside directories",
			cmd:  `ls /usr/local/bin/node /opt/homebrew/bin/node`,
			why:  "toolchain discovery — every candidate in the command is a read",
		},
		{
			name: "head with a flag before the path",
			cmd:  `head -n 5 /etc/hosts`,
			why:  "flags between the head and the path must not disturb the classification",
		},
		{
			name: "grep over an outside file",
			cmd:  `grep -n root /etc/passwd`,
			why:  "grep has no flag that writes to a path named on its command line",
		},
		{
			name: "quoted argument containing a segment separator",
			cmd:  `grep "root;admin" /etc/passwd`,
			why:  "the `;` is quoted, so it is data, not a segment break — the head stays `grep`",
		},
		{
			name: "assignment prefix before a read command",
			cmd:  `LC_ALL=C grep root /etc/passwd`,
			why:  "a leading VAR=value assignment is skipped when resolving the head, and is not the candidate's word",
		},
		{
			name: "read with stderr sent to /dev/null",
			cmd:  `cat /etc/hosts 2>/dev/null`,
			why:  "the redirect target is a safePaths pseudo-device; the read beside it must still be classified",
		},
		{
			name: "read with stderr folded into stdout",
			cmd:  `cat /etc/hosts 2>&1`,
			why:  "`>&1` is a file-descriptor duplication, not a write to a file",
		},
		{
			name: "read piped into a second command",
			cmd:  `cat /etc/hosts | head -3`,
			why:  "each pipeline segment is judged on its own head",
		},
		{
			name: "explicit end-of-flags marker",
			cmd:  `cat -- /etc/passwd`,
			why:  "`--` is an ordinary word; the path is still an argument, not the head",
		},
		{
			name: "input redirect from an outside file",
			cmd:  `cat < /etc/hosts`,
			why:  "`<` is a read; only `>` shapes mark a write target",
		},
		{
			name: "single absolute path with a stray colon suffix",
			cmd:  `cat /etc/passwd:evil`,
			why:  "not a colon-joined list, so it stays one literal candidate — and it is read as one file",
		},
		{
			name: "colon-joined list of two outside paths, read",
			cmd:  `cat /etc/passwd:/etc/hosts`,
			why:  "every segment of a colon list inherits the same read classification",
		},
		{
			name: "tilde-expanded colon-joined list mixing a real path with a safe device",
			cmd:  `cat ~/.ssh/id_rsa:/dev/null`,
			why:  "same, with one segment already exempt via safePaths",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tool.guardCommand(context.Background(), tc.cmd, cwd)
			require.Empty(t, got,
				"read outside the working dir must be allowed under ADR-068 (%s)\ncommand: %s", tc.why, tc.cmd)
		})
	}
}

// TestGuardCommand_WritesOutsideWorkDirStillBlocked is the other half of the
// ruling. Each case names an absolute path outside the working directory in a
// context that writes to it, or that this scanner cannot prove is a read.
//
// If any of these starts passing, the read/write split has become a hole rather
// than a distinction.
func TestGuardCommand_WritesOutsideWorkDirStillBlocked(t *testing.T) {
	tool, cwd := guardFixture(t)

	cases := []struct {
		name string
		cmd  string
		want string
	}{
		{
			name: "plain output redirect",
			cmd:  `printf hello > /etc/hosts`,
			want: "path outside working dir",
		},
		{
			name: "appending output redirect",
			cmd:  `printf hello >> /etc/hosts`,
			want: "path outside working dir",
		},
		{
			name: "read command redirected onto an outside path",
			cmd:  `cat /etc/passwd > /etc/copy`,
			want: "path outside working dir",
		},
		{
			name: "attached output redirect with no space",
			cmd:  `cat /etc/passwd >/etc/copy`,
			want: "path outside working dir",
		},
		{
			name: "combined stdout+stderr redirect",
			cmd:  `cat /etc/passwd &>/etc/copy`,
			want: "path outside working dir",
		},
		{
			name: "bash combined redirect with an attached target",
			cmd:  `cat /etc/passwd >&/etc/copy`,
			want: "path outside working dir",
		},
		{
			name: "read-write open of an outside path",
			cmd:  `cat <>/etc/hosts`,
			want: "path outside working dir",
		},
		{
			name: "redirect onto a tilde path",
			cmd:  `cat /etc/passwd > ~/stolen.txt`,
			want: "path outside working dir",
		},
		{
			name: "tee at the end of a pipeline",
			cmd:  `cat /etc/passwd | tee /etc/copy`,
			want: "path outside working dir",
		},
		{
			name: "tee with a quoted argument that fakes a segment break",
			cmd:  `tee "a;cat" /etc/copy`,
			want: "path outside working dir",
		},
		{
			name: "cp to an outside destination",
			cmd:  `cp notes.txt /etc/copy`,
			want: "path outside working dir",
		},
		{
			name: "mv to an outside destination",
			cmd:  `mv notes.txt /etc/copy`,
			want: "path outside working dir",
		},
		{
			name: "in-place sed",
			cmd:  `sed -i s/x/y/ /etc/hosts`,
			want: "path outside working dir",
		},
		{
			name: "sort with an output flag",
			cmd:  `sort -o /etc/out /etc/hosts`,
			want: "path outside working dir",
		},
		{
			name: "dd with an output file",
			cmd:  `dd of=/etc/hosts`,
			want: "path outside working dir",
		},
		{
			name: "truncate",
			cmd:  `truncate -s 0 /etc/hosts`,
			want: "path outside working dir",
		},
		{
			name: "interpreter that opens the path itself",
			cmd:  `python3 -c "open('/etc/hosts','w')"`,
			want: "path outside working dir",
		},
		{
			name: "tar extracting into an outside directory",
			cmd:  `tar -C/etc -xf a.tar`,
			want: "path outside working dir",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tool.guardCommand(context.Background(), tc.cmd, cwd)
			require.NotEmpty(t, got, "write outside the working dir must stay blocked: %s", tc.cmd)
			require.Contains(t, got, tc.want, "wrong rejection reason for: %s", tc.cmd)
		})
	}
}

// TestGuardCommand_FailsClosedOnAmbiguity covers the shapes the classifier
// cannot reason about. None of them is necessarily a write — several are almost
// certainly reads — but the guard must refuse anyway, because "probably a read"
// is exactly the inference ADR-068 §5 says this layer must never make.
//
// These cases are therefore NOT bugs to be fixed by loosening the classifier.
// Each one is a deliberate false block whose cost is one refused command, set
// against the cost of one unnoticed write escaping the working directory.
func TestGuardCommand_FailsClosedOnAmbiguity(t *testing.T) {
	tool, cwd := guardFixture(t)

	cases := []struct {
		name string
		cmd  string
		why  string
	}{
		{
			name: "command name comes from an expansion",
			cmd:  `C=cat; $C /etc/passwd`,
			why:  "the head resolves at runtime; a text guard cannot know what runs",
		},
		{
			name: "absolute path in command position after a semicolon",
			cmd:  `echo hi;/etc/shadow`,
			why:  "that is an EXEC, not a read; ADR-068 rules on reads only",
		},
		{
			name: "absolute path in command position after a pipe",
			cmd:  `echo hi|/bin/sh`,
			why:  "same, mid-pipeline",
		},
		{
			name: "bracket-adjacent absolute path",
			cmd:  `cat[/etc/shadow]`,
			why:  "the whole token is the command name (`[` is not a head terminator), so the path sits in command position — a head scanner that later models `[` must keep this blocked, which is why the case is pinned here",
		},
		{
			name: "brace expansion",
			cmd:  `cat {/etc/shadow,/etc/passwd}`,
			why:  "bash rewrites the words before parsing; this scanner does not model brace expansion, so the command it is judging is not the command that will run",
		},
		{
			name: "brace expansion with an empty first alternative",
			cmd:  `cat {,/etc/shadow}`,
			why:  "same",
		},
		{
			name: "redirect target hidden behind a variable",
			cmd:  `cat /etc/passwd > $OUT`,
			why:  "there is a write in this segment and the scan cannot see where it lands, so no read exemption is granted beside it",
		},
		{
			name: "redirect target hidden behind a glob",
			cmd:  `cat /etc/passwd > out*.txt`,
			why:  "same reasoning as $OUT",
		},
		{
			name: "unbalanced quote",
			cmd:  `cat "/etc/passwd`,
			why:  "the command cannot be tokenised, so nothing in it can be classified",
		},
		{
			name: "shell -c wrapper",
			cmd:  `sh -c "cat /etc/passwd"`,
			why:  "an interpreter's file access is invisible to a text scan",
		},
		{
			name: "compiler include flag",
			cmd:  `gcc -I/usr/include -c foo.c`,
			why:  "a genuine read that stays blocked: inferring 'this flag names a read-only directory' is the fragile inference this design refuses",
		},
		{
			name: "colon-joined PATH assignment",
			cmd:  `PATH=/etc/shadow:/usr/bin make`,
			why:  "the candidate is inside the segment's first word (an assignment prefix to an exec), not an argument to a read command",
		},
		{
			name: "echo does not open the path",
			cmd:  `echo x,/etc/shadow`,
			why:  "echo never opens the file, so there is no read to grant; keeping it off the allowlist also keeps this shape blocked",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tool.guardCommand(context.Background(), tc.cmd, cwd)
			require.NotEmpty(t, got,
				"the guard must fail closed here (%s)\ncommand: %s", tc.why, tc.cmd)
		})
	}
}

// TestReadOnlyShellCommands_ExcludesEveryKnownWriter pins the allowlist's
// membership criterion structurally rather than through commands, so a name
// added to readOnlyShellCommands without thinking fails here with the reason
// spelled out.
//
// Oracle: a name belongs on the allowlist only if the binary has no flag, in
// any common implementation, that writes to a filesystem path named on its own
// command line. Each entry below violates that, with the flag named.
func TestReadOnlyShellCommands_ExcludesEveryKnownWriter(t *testing.T) {
	writers := map[string]string{
		"sed":       "sed -i edits in place",
		"perl":      "perl -i edits in place",
		"ruby":      "ruby -i edits in place",
		"gawk":      "gawk -i inplace edits in place",
		"awk":       "awk can open files for writing from its program text",
		"sort":      "sort -o FILE writes",
		"find":      "find -exec / -delete writes",
		"xxd":       "xxd -r IN OUT writes",
		"tee":       "tee writes to every path it is given",
		"dd":        "dd of=FILE writes",
		"cp":        "cp writes its destination",
		"mv":        "mv writes its destination",
		"ln":        "ln creates its target",
		"install":   "install writes its destination",
		"rsync":     "rsync writes its destination",
		"tar":       "tar -x -C DIR extracts into DIR",
		"unzip":     "unzip -d DIR extracts into DIR",
		"truncate":  "truncate -s writes",
		"touch":     "touch creates",
		"mkdir":     "mkdir creates",
		"rm":        "rm deletes",
		"less":      "less allows shell escapes",
		"more":      "more allows shell escapes",
		"vi":        "vi writes",
		"view":      "view is vi",
		"python":    "an interpreter can open anything for writing",
		"python3":   "an interpreter can open anything for writing",
		"node":      "an interpreter can open anything for writing",
		"sh":        "an interpreter can open anything for writing",
		"bash":      "an interpreter can open anything for writing",
		"xargs":     "xargs runs an arbitrary command",
		"git":       "git writes working trees and repositories",
		"make":      "make runs arbitrary recipes",
		"gcc":       "gcc writes object files and binaries",
		"curl":      "curl -o / -O writes",
		"wget":      "wget -O writes",
		"echo":      "echo never opens the path, so there is no read to grant",
		"printf":    "printf never opens the path, so there is no read to grant",
		"chmod":     "chmod mutates the path",
		"chown":     "chown mutates the path",
		"ldd":       "ldd executes the object it inspects on some platforms",
		"openssl":   "openssl -out writes",
		"base64":    "base64 -o writes on some implementations",
		"gzip":      "gzip rewrites its input",
		"gunzip":    "gunzip rewrites its input",
		"zip":       "zip writes an archive",
		"cpio":      "cpio -o / -i writes",
		"split":     "split writes its output pieces",
		"csplit":    "csplit writes its output pieces",
		"tac":       "not audited; absent by default is the safe state",
		"shred":     "shred overwrites the path",
		"dirname_":  "sentinel: names not on the list must stay off it",
		"terraform": "terraform writes state",
	}

	for name, why := range writers {
		t.Run(name, func(t *testing.T) {
			require.False(t, readOnlyShellCommands[name],
				"%q must NOT be on the read-only allowlist: %s. The membership criterion is "+
					"'no flag, in any common implementation, writes to a path named on the command line' — "+
					"if a reviewer has to think about it, the answer is no (ADR-068 §5).", name, why)
		})
	}
}
