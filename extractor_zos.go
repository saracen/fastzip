// +build zos
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
    err := lchmod(name, mode)
    if err != nil {
        return err
    }
    err = os.Chtimes(name, atime, mtime)
    if err != nil {
        return &os.PathError{Op: "lchtimes", Path: name, Err: err}
    }
    return nil
}

func lchown(name string, uid, gid int) error {
    return os.Lchown(name, uid, gid)
}
