"""Instrument check, independent of Omnipus code: reads the kernel's Landlock
ABI, then (in a forked child) restricts itself with a ruleset that handles
MAKE_SOCK (and the other v1 write rights) but grants nothing, and shows what
access(/tmp, W_OK) and bind() of a /tmp Unix socket return. Run under strace
to prove the tracer sees a Landlock denial."""
import ctypes
import os
import socket
import struct
import sys

sys.stdout.reconfigure(line_buffering=True)
SYS_CREATE, SYS_RESTRICT = 444, 446  # x86_64
libc = ctypes.CDLL(None, use_errno=True)
libc.syscall.restype = ctypes.c_long

abi = libc.syscall(SYS_CREATE, None, ctypes.c_size_t(0), ctypes.c_uint32(1))
print(f"landlock_abi={abi} errno={ctypes.get_errno()}")
if abi < 1:
    sys.exit(0)

path = f"/tmp/omnipus-diag-probe-{os.getpid()}.sock"
pid = os.fork()
if pid == 0:
    # v1 write-class rights incl. MAKE_SOCK (1<<9); nothing granted anywhere.
    handled = (1 << 1) | (1 << 4) | (1 << 5) | (1 << 6) | (1 << 7) | (1 << 8) | (1 << 9) | (1 << 10) | (1 << 11) | (1 << 12)
    attr = ctypes.create_string_buffer(struct.pack("<Q", handled), 8)
    fd = libc.syscall(SYS_CREATE, attr, ctypes.c_size_t(8), ctypes.c_uint32(0))
    if fd < 0:
        print(f"create_ruleset failed errno={ctypes.get_errno()}"); os._exit(2)
    if libc.prctl(38, 1, 0, 0, 0) != 0:
        print("no_new_privs failed"); os._exit(2)
    if libc.syscall(SYS_RESTRICT, ctypes.c_int(fd), ctypes.c_uint32(0)) != 0:
        print(f"restrict_self failed errno={ctypes.get_errno()}"); os._exit(2)
    print(f"child: access(/tmp, W_OK) -> {os.access('/tmp', os.W_OK)}")
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    try:
        s.bind(path)
        print("child: bind /tmp socket -> OK (NOT denied)")
        os.unlink(path)
    except OSError as e:
        print(f"child: bind /tmp socket -> {e.errno} {os.strerror(e.errno)}")
    os._exit(0)
_, status = os.waitpid(pid, 0)
print(f"child exit status={status}")
