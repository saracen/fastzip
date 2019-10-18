// +build !windows

package fastzip

import (
	"os"
	"runtime"
	"time"

	"golang.org/x/sys/unix"
)

func lchmod(name string, mode os.FileMode) error {
	var flags int
	if runtime.GOOS == "linux" {
		if mode&os.ModeSymlink != 0 {
			return nil
		}
	} else {
		flags = unix.AT_SYMLINK_NOFOLLOW
	}

	err := unix.Fchmodat(unix.AT_FDCWD, name, uint32(mode), flags)
	if err != nil {
		return &os.PathError{Op: "lchmod", Path: name, Err: err}
	}

	return nil
}

func lchtimes(name string, mode os.FileMode, atime, mtime time.Time) error {
	at := unix.NsecToTimeval(atime.UnixNano())
	mt := unix.NsecToTimeval(mtime.UnixNano())
	tv := [2]unix.Timeval{at, mt}

	err := unix.Lutimes(name, tv[:])
	if err != nil {
		return &os.PathError{Op: "lchtimes", Path: name, Err: err}
	}

	return nil
}

func lchown(name string, uid, gid int) error {
	return os.Lchown(name, uid, gid)
}
