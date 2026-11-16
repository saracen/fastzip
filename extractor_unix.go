//go:build !windows

package fastzip

import (
	"os"
	"path/filepath"
	"runtime"
	"time"

	"golang.org/x/sys/unix"
)

// atParent opens the parent directory of name through root and calls fn with
// the directory's descriptor and the base name. Operating relative to the
// parent descriptor with AT_SYMLINK_NOFOLLOW acts on the entry itself rather
// than on whatever it links to, and because root resolved the parent, the
// operation cannot be redirected outside of the chroot by a link in the path.
func atParent(root *os.Root, name string, fn func(dirfd int, base string) error) error {
	dir, err := root.Open(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer dir.Close()

	return fn(int(dir.Fd()), filepath.Base(name))
}

func lchmod(root *os.Root, name string, mode os.FileMode) error {
	if mode&os.ModeSymlink == 0 {
		// Only permission bits are applied, as before: setuid, setgid and sticky
		// bits stored in the archive are not honoured.
		return root.Chmod(name, mode.Perm())
	}

	// Linux has no mode on the symlink itself.
	if runtime.GOOS == "linux" {
		return nil
	}

	return atParent(root, name, func(dirfd int, base string) error {
		err := unix.Fchmodat(dirfd, base, uint32(mode.Perm()), unix.AT_SYMLINK_NOFOLLOW)
		if err != nil {
			return &os.PathError{Op: "lchmod", Path: name, Err: err}
		}
		return nil
	})
}

func lchtimes(root *os.Root, name string, mode os.FileMode, atime, mtime time.Time) error {
	if mode&os.ModeSymlink == 0 {
		return root.Chtimes(name, atime, mtime)
	}

	return atParent(root, name, func(dirfd int, base string) error {
		ts := []unix.Timespec{
			unix.NsecToTimespec(atime.UnixNano()),
			unix.NsecToTimespec(mtime.UnixNano()),
		}
		err := unix.UtimesNanoAt(dirfd, base, ts, unix.AT_SYMLINK_NOFOLLOW)
		if err != nil {
			return &os.PathError{Op: "lchtimes", Path: name, Err: err}
		}
		return nil
	})
}

func lchown(root *os.Root, name string, uid, gid int) error {
	return root.Lchown(name, uid, gid)
}
