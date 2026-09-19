//go:build linux

package jerry

import "golang.org/x/sys/unix"

// RENAME_NOREPLACE works on Pi ext4 and removable FAT filesystems, unlike
// hard-link publication. Unsupported filesystems fail closed, never overwrite.
func publishUpload(staging, destination string) error {
	return unix.Renameat2(unix.AT_FDCWD, staging, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE)
}
