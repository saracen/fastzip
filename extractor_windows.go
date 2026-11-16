//go:build windows

package fastzip

import (
	"os"
	"time"
)

func lchmod(root *os.Root, name string, mode os.FileMode) error {
	if mode&os.ModeSymlink != 0 {
		return nil
	}

	// Only permission bits are applied, as before: setuid, setgid and sticky
	// bits stored in the archive are not honoured.
	return root.Chmod(name, mode.Perm())
}

func lchtimes(root *os.Root, name string, mode os.FileMode, atime, mtime time.Time) error {
	if mode&os.ModeSymlink != 0 {
		return nil
	}

	return root.Chtimes(name, atime, mtime)
}

func lchown(root *os.Root, name string, uid, gid int) error {
	return nil
}
