//go:build !windows && !darwin

package clitrust

// checkNoWriteACL: on Linux a POSIX ACL that grants write to another user or
// group raises the group permission bits (the ACL mask), which the mode
// checks in check_unix.go already refuse, so no separate ACL read is needed.
func checkNoWriteACL(string) error { return nil }
