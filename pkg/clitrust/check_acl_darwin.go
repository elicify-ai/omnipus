//go:build darwin

package clitrust

import (
	"encoding/binary"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// macOS extended permissions (T11 round 3, security review S4-R2): a file or
// folder with ordinary 0755 bits can still carry an access-control-list
// entry that lets another account write it. The ACL is read with
// getattrlist(ATTR_CMN_EXTENDED_SECURITY) — pure Go, no cgo — and the simple
// safe rule is applied: ANY "allow" entry carrying a write, append, delete,
// delete-child, attribute/security-change, ownership or generic write/all
// right refuses the path, whoever it names (an entry for the owner alone is
// redundant with the mode bits, so refusing it costs nothing). "Deny"
// entries, such as the "everyone deny delete" macOS puts on home folders,
// are fine. If the ACL cannot be read, the path is refused.
//
// Layout (sys/kauth.h): kauth_filesec { u32 magic; guid owner; guid group;
// kauth_acl { u32 entrycount; u32 flags; kauth_ace[] } }, kauth_ace { guid
// applicable (16 bytes); u32 flags; u32 rights }.
const (
	kauthFilesecMagic = 0x012cc16d
	kauthFilesecNoACL = 0xffffffff
	kauthACEKindMask  = 0xf
	kauthACEPermit    = 1
	kauthACLMaxEntry  = 128

	// Rights that let a non-owner change, replace or take over the entry.
	kauthWriteRights = 1<<2 | // WRITE_DATA / ADD_FILE
		1<<4 | // DELETE
		1<<5 | // APPEND_DATA / ADD_SUBDIRECTORY
		1<<6 | // DELETE_CHILD
		1<<8 | // WRITE_ATTRIBUTES
		1<<10 | // WRITE_EXTATTRIBUTES
		1<<12 | // WRITE_SECURITY
		1<<13 | // TAKE_OWNERSHIP
		1<<21 | // GENERIC_ALL
		1<<23 // GENERIC_WRITE

	fsoptNoFollow = 0x1
)

// checkNoWriteACL refuses path when its macOS ACL grants write-type rights to
// anyone, or when the ACL cannot be read.
func checkNoWriteACL(path string) error {
	al := unix.Attrlist{
		Bitmapcount: unix.ATTR_BIT_MAP_COUNT,
		Commonattr:  unix.ATTR_CMN_RETURNED_ATTRS | unix.ATTR_CMN_EXTENDED_SECURITY,
	}
	buf := make([]byte, 16384)
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return fmt.Errorf("the permissions of %q cannot be read: %w", path, err)
	}
	// golang.org/x/sys/unix exports no libSystem wrapper for getattrlist
	// (only setattrlist), so the call goes through syscall.Syscall6, which on
	// Darwin is itself served by libSystem's syscall(). Verified on Darwin 25
	// against chmod +a fixtures (allow write refused, deny delete accepted).
	_, _, errno := syscall.Syscall6(unix.SYS_GETATTRLIST, //nolint:staticcheck // SA1019: no libSystem getattrlist wrapper exists in x/sys/unix; see comment above
		uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&al)),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), fsoptNoFollow, 0)
	if errno != 0 {
		return fmt.Errorf("the access-control list of %q cannot be read: %w", path, errno)
	}
	refuse := func(why string) error {
		return fmt.Errorf("the access-control list of %q %s", path, why)
	}
	le := binary.LittleEndian
	// u32 total length; attribute_set_t (5 x u32) of returned attributes;
	// then the attrreference_t {i32 offset; u32 length} of the ACL.
	if len(buf) < 24 || le.Uint32(buf[0:4]) < 24 {
		return refuse("cannot be read (short reply)")
	}
	if le.Uint32(buf[4:8])&unix.ATTR_CMN_EXTENDED_SECURITY == 0 {
		return nil // no ACL at all
	}
	const ref = 24
	if int(le.Uint32(buf[0:4])) < ref+8 {
		return refuse("cannot be read (truncated reply)")
	}
	off := int(int32(le.Uint32(buf[ref : ref+4])))
	n := int(le.Uint32(buf[ref+4 : ref+8]))
	if off < 0 || ref+off+n > len(buf) || n < 44 {
		return refuse("cannot be read (malformed reply)")
	}
	sec := buf[ref+off : ref+off+n]
	if le.Uint32(sec[0:4]) != kauthFilesecMagic {
		return refuse("cannot be read (unexpected format)")
	}
	count := le.Uint32(sec[36:40])
	if count == kauthFilesecNoACL || count == 0 {
		return nil
	}
	if count > kauthACLMaxEntry || 44+int(count)*24 > len(sec) {
		return refuse("cannot be read (malformed entry list)")
	}
	for i := 0; i < int(count); i++ {
		ace := sec[44+24*i : 44+24*(i+1)]
		flags, rights := le.Uint32(ace[16:20]), le.Uint32(ace[20:24])
		if flags&kauthACEKindMask == kauthACEPermit && rights&kauthWriteRights != 0 {
			return refuse("lets other accounts change or replace it; remove that entry (chmod -N) or choose a protected installation")
		}
	}
	return nil
}
