"""DIAGNOSTIC ONLY. Re-creates, outside Omnipus, the Linux Landlock ruleset the
Office converter gets (pkg/preview/confine_linux.go::hooksFor +
confine_unix.go::confinement.policy + pkg/sandbox access mapping: Read =
READ_FILE|READ_DIR, Execute = EXECUTE, Write = v1 write class|REFER|TRUNCATE,
dir-only rights stripped for files, TCP bind/connect handled with a port-0
rule), optionally plus extra rights on /tmp, then execs the command.
usage: landlock_run.py --rx D --ro D --rw D --tmp none|sock|sock+remove -- cmd..."""
import ctypes
import os
import struct
import sys

sys.stdout.reconfigure(line_buffering=True)
libc = ctypes.CDLL(None, use_errno=True)
libc.syscall.restype = ctypes.c_long
CREATE, ADD, RESTRICT = 444, 445, 446
EXECUTE, WRITE_FILE, READ_FILE, READ_DIR = 1 << 0, 1 << 1, 1 << 2, 1 << 3
REMOVE_DIR, REMOVE_FILE, MAKE_CHAR, MAKE_DIR, MAKE_REG = 1 << 4, 1 << 5, 1 << 6, 1 << 7, 1 << 8
MAKE_SOCK, MAKE_FIFO, MAKE_BLOCK, MAKE_SYM, REFER, TRUNCATE = 1 << 9, 1 << 10, 1 << 11, 1 << 12, 1 << 13, 1 << 14
WRITE_CLASS = WRITE_FILE | REMOVE_DIR | REMOVE_FILE | MAKE_CHAR | MAKE_DIR | MAKE_REG | MAKE_SOCK | MAKE_FIFO | MAKE_BLOCK | MAKE_SYM
DIR_ONLY = READ_DIR | REMOVE_DIR | REMOVE_FILE | MAKE_CHAR | MAKE_DIR | MAKE_REG | MAKE_SOCK | MAKE_FIFO | MAKE_BLOCK | MAKE_SYM | REFER
ALL_FS = EXECUTE | READ_FILE | READ_DIR | WRITE_CLASS | REFER | TRUNCATE  # ABI 8, IOCTL_DEV not handled (as pkg/sandbox)
BIND_TCP, CONNECT_TCP = 1 << 0, 1 << 1

def die(msg):
    print(f"landlock_run: {msg} errno={ctypes.get_errno()}", file=sys.stderr)
    sys.exit(97)

argv = sys.argv[1:]
sep = argv.index("--")
opts, cmd = argv[:sep], argv[sep + 1:]
rules, tmp = [], "none"
i = 0
while i < len(opts):
    k, v = opts[i], opts[i + 1]
    i += 2
    if k == "--rx":
        rules.append((v, READ_FILE | READ_DIR | EXECUTE))
    elif k == "--ro":
        rules.append((v, READ_FILE | READ_DIR))
    elif k == "--rw":
        rules.append((v, READ_FILE | READ_DIR | WRITE_CLASS | REFER | TRUNCATE))
    elif k == "--tmp":
        tmp = v
    else:
        die(f"unknown option {k}")
if tmp == "sock":
    rules.append(("/tmp", MAKE_SOCK))
elif tmp == "sock+remove":
    rules.append(("/tmp", MAKE_SOCK | REMOVE_FILE))

attr = struct.pack("<QQ", ALL_FS, BIND_TCP | CONNECT_TCP)
fd = libc.syscall(CREATE, ctypes.c_char_p(attr), ctypes.c_size_t(16), ctypes.c_uint32(0))
if fd < 0:
    die("create_ruleset")
for path, access in rules:
    pfd = os.open(path, os.O_PATH | os.O_CLOEXEC)
    if not os.path.isdir(path):
        access &= ~DIR_ONLY
    pb = struct.pack("<Qi", access, pfd)  # landlock_path_beneath_attr is packed: 12 bytes
    if libc.syscall(ADD, ctypes.c_int(fd), ctypes.c_int(1), ctypes.c_char_p(pb), ctypes.c_uint32(0)) != 0:
        die(f"add_rule {path}")
    os.close(pfd)
    print(f"landlock_run: rule {path} access=0x{access:x}", file=sys.stderr)
net = struct.pack("<QQ", BIND_TCP | CONNECT_TCP, 0)
if libc.syscall(ADD, ctypes.c_int(fd), ctypes.c_int(2), ctypes.c_char_p(net), ctypes.c_uint32(0)) != 0:
    die("add net rule")
if libc.prctl(38, 1, 0, 0, 0) != 0:
    die("no_new_privs")
if libc.syscall(RESTRICT, ctypes.c_int(fd), ctypes.c_uint32(0)) != 0:
    die("restrict_self")
os.execv(cmd[0], cmd)
