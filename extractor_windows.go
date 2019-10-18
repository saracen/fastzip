// +build windows

package fastzip

import (
	"os"
	"time"
)

func lchmod(name string, mode os.FileMode) error {
	if mode&os.ModeSymlink != 0 {
		return nil
	}

	return os.Chmod(name, mode)
}

func lchtimes(name string, mode os.FileMode, atime, mtime time.Time) error {
	if mode&os.ModeSymlink != 0 {
		return nil
	}

	return os.Chtimes(name, atime, mtime)
}

func lchown(name string, uid, gid int) error {
	return nil
}
