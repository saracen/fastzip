package fastzip

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/klauspost/compress/zip"
	"github.com/klauspost/compress/zstd"
	"github.com/saracen/zipextra"
	"golang.org/x/sync/errgroup"
)

var bufioWriterPool = sync.Pool{
	New: func() interface{} {
		return bufio.NewWriterSize(nil, 32*1024)
	},
}

var (
	defaultDecompressor     = FlateDecompressor()
	defaultZstdDecompressor = ZstdDecompressor()
)

// ErrSymlinkEscapesChroot is returned, wrapped with the offending entry, when
// an entry cannot be extracted because a symbolic link already on disk in its
// path is absolute or leads outside of the chroot, or, when extracting with
// SymlinkPolicyContain, when a symbolic link entry's target is absolute or
// resolves outside of the chroot.
var ErrSymlinkEscapesChroot = errors.New("symlink target is absolute or resolves outside of chroot")

// Extractor is an opinionated Zip file extractor.
//
// Files are extracted in parallel. Only regular files, symlinks and directories
// are supported. Files can only be extracted to the specified chroot directory.
//
// Every filesystem operation is performed relative to the chroot through an
// os.Root, so an entry is never created, written or has its metadata changed
// outside of the chroot, even when a symbolic link that leads outside of it
// already exists in the extraction directory from a previous extraction. A
// symbolic link or file found where an entry is to be extracted is replaced by
// the entry rather than written through. As os.Root never follows an absolute
// symbolic link, an entry whose path passes through one already on disk fails
// even if the link points inside the chroot.
//
// Symbolic link entries are created as stored in the archive by default, so
// the extracted tree may contain links that point outside of the chroot. See
// WithExtractorSymlinkPolicy to reject such links.
//
// Access permissions, ownership (unix) and modification times are preserved.
type Extractor struct {
	// This 2 fields are accessed via atomic operations
	// They are at the start of the struct so they are properly 8 byte aligned
	written, entries int64

	zr      *zip.Reader
	closer  io.Closer
	m       sync.Mutex
	options extractorOptions
	chroot  string
}

// NewExtractor opens a zip file and returns a new extractor.
//
// Close() should be called to close the extractor's underlying zip.Reader
// when done.
func NewExtractor(filename, chroot string, opts ...ExtractorOption) (*Extractor, error) {
	zr, err := zip.OpenReader(filename)
	if err != nil {
		return nil, err
	}

	return newExtractor(&zr.Reader, zr, chroot, opts)
}

// NewExtractor returns a new extractor, reading from the reader provided.
//
// The size of the archive should be provided.
//
// Unlike with NewExtractor(), calling Close() on the extractor is unnecessary.
func NewExtractorFromReader(r io.ReaderAt, size int64, chroot string, opts ...ExtractorOption) (*Extractor, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, err
	}

	return newExtractor(zr, nil, chroot, opts)
}

func newExtractor(r *zip.Reader, c io.Closer, chroot string, opts []ExtractorOption) (*Extractor, error) {
	var err error
	if chroot, err = filepath.Abs(chroot); err != nil {
		return nil, err
	}

	e := &Extractor{
		chroot: chroot,
		zr:     r,
		closer: c,
	}

	e.options.concurrency = runtime.GOMAXPROCS(0)
	for _, o := range opts {
		err := o(&e.options)
		if err != nil {
			return nil, err
		}
	}

	e.RegisterDecompressor(zip.Deflate, defaultDecompressor)
	e.RegisterDecompressor(zstd.ZipMethodWinZip, defaultZstdDecompressor)

	return e, nil
}

// RegisterDecompressor allows custom decompressors for a specified method ID.
// The common methods Store and Deflate are built in.
func (e *Extractor) RegisterDecompressor(method uint16, dcomp zip.Decompressor) {
	e.zr.RegisterDecompressor(method, dcomp)
}

// Files returns the file within the archive.
func (e *Extractor) Files() []*zip.File {
	return e.zr.File
}

// Close closes the underlying ZipReader.
func (e *Extractor) Close() error {
	if e.closer == nil {
		return nil
	}
	return e.closer.Close()
}

// Written returns how many bytes and entries have been written to disk.
// Written can be called whilst extraction is in progress.
func (e *Extractor) Written() (bytes, entries int64) {
	return atomic.LoadInt64(&e.written), atomic.LoadInt64(&e.entries)
}

// Extract extracts files, creates symlinks and directories from the
// archive. The chroot directory is created if it does not exist.
func (e *Extractor) Extract(ctx context.Context) (err error) {
	if err := os.MkdirAll(e.chroot, 0777); err != nil {
		return err
	}

	// Every operation below goes through root, which resolves each path
	// component relative to the chroot and fails if a symbolic link leads
	// outside of it. Checking the entry name alone cannot provide this: the
	// link may already be on disk from a previous extraction.
	root, err := os.OpenRoot(e.chroot)
	if err != nil {
		return err
	}
	defer dclose(root, &err)

	limiter := make(chan struct{}, e.options.concurrency)

	wg, ctx := errgroup.WithContext(ctx)
	defer func() {
		if werr := wg.Wait(); werr != nil {
			err = werr
		}
	}()

	for i, file := range e.zr.File {
		if file.Mode()&irregularModes != 0 {
			continue
		}

		var name string
		name, err = e.entryName(file)
		if err != nil {
			return err
		}

		if err := root.MkdirAll(filepath.Dir(name), 0777); err != nil {
			return e.explainEscape(root, name, err)
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		switch {
		case file.Mode()&os.ModeSymlink != 0:
			// defer the creation of symlinks
			// this is to prevent a traversal vulnerability where a symlink is
			// first created and then files are additional extracted into it
			continue

		case file.Mode().IsDir():
			err = e.createDirectory(root, name, file)

		default:
			limiter <- struct{}{}

			gf := e.zr.File[i]
			wg.Go(func() error {
				defer func() { <-limiter }()
				err := e.createFile(ctx, root, name, gf)
				if err == nil {
					err = e.updateFileMetadata(root, name, gf)
				}
				return e.explainEscape(root, name, err)
			})
		}
		if err != nil {
			return e.explainEscape(root, name, err)
		}
	}

	if err := wg.Wait(); err != nil {
		return err
	}

	// Create all symlinks. This will update parent directory mtimes to current time.
	for _, file := range e.zr.File {
		if file.Mode()&os.ModeSymlink == 0 {
			continue
		}

		name, err := e.entryName(file)
		if err != nil {
			return err
		}

		// createSymlink() handles the symlink's own timestamp preservation
		// but will update the parent directory's mtime to current time.
		if err := e.createSymlink(root, name, file); err != nil {
			return e.explainEscape(root, name, err)
		}
	}

	// Update ALL directory metadata after symlinks are created.
	// This ensures all directory timestamps and permissions are correctly preserved.
	for _, file := range e.zr.File {
		if !file.Mode().IsDir() {
			continue
		}

		name, err := e.entryName(file)
		if err != nil {
			return err
		}

		err = e.updateFileMetadata(root, name, file)
		if err != nil {
			return e.explainEscape(root, name, err)
		}
	}

	return nil
}

// entryName returns the path of an archive entry relative to the chroot.
// Entries whose name lexically escapes the chroot are rejected here to give a
// clear error; the os.Root used for every filesystem operation enforces
// containment regardless.
func (e *Extractor) entryName(file *zip.File) (string, error) {
	path, err := filepath.Abs(filepath.Join(e.chroot, file.Name))
	if err != nil {
		return "", err
	}

	if !strings.HasPrefix(path, e.chroot+string(filepath.Separator)) && path != e.chroot {
		return "", fmt.Errorf("%s cannot be extracted outside of chroot (%s)", path, e.chroot)
	}

	return filepath.Rel(e.chroot, path)
}

func (e *Extractor) createDirectory(root *os.Root, name string, file *zip.File) error {
	err := root.Mkdir(name, 0777)
	if errors.Is(err, fs.ErrExist) {
		// Anything other than a directory in the way is replaced rather than
		// kept: a symbolic link would otherwise be followed by the entries
		// below it, and a file would have the directory's metadata applied.
		var fi os.FileInfo
		fi, err = root.Lstat(name)
		if err == nil && !fi.IsDir() {
			if err = root.Remove(name); err == nil {
				err = root.Mkdir(name, 0777)
			}
		}
	}
	incOnSuccess(&e.entries, err)
	return err
}

func (e *Extractor) createSymlink(root *os.Root, name string, file *zip.File) error {
	r, err := file.Open()
	if err != nil {
		return err
	}
	defer r.Close()

	target, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	contain := e.options.symlinkPolicy == SymlinkPolicyContain
	if contain && symlinkTargetIsAbsolute(string(target)) {
		return e.symlinkEscapesError(name, string(target))
	}

	if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	if err := root.Symlink(string(target), name); err != nil {
		return err
	}

	if contain {
		// Resolving the link through root follows the whole chain, including
		// links that already existed on disk, and fails if it leaves the
		// chroot. A dangling link that stays inside the chroot is allowed.
		if _, err := root.Stat(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
			_ = root.Remove(name)
			if errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, fs.ErrPermission) {
				return fmt.Errorf("%s -> %s: cannot resolve symlink target: %w", filepath.Join(e.chroot, name), target, err)
			}
			return e.symlinkEscapesError(name, string(target))
		}
	}

	err = e.updateFileMetadata(root, name, file)
	incOnSuccess(&e.entries, err)

	return err
}

func (e *Extractor) symlinkEscapesError(name, target string) error {
	return fmt.Errorf("%s -> %s: %w (%s)", filepath.Join(e.chroot, name), target, ErrSymlinkEscapesChroot, e.chroot)
}

// explainEscape returns a clearer error if an operation on name failed because
// a symbolic link already on disk in its path leads outside of the chroot, as
// os.Root reports this as the bare syscall failure. Otherwise err is returned
// unchanged.
func (e *Extractor) explainEscape(root *os.Root, name string, err error) error {
	prefix := ""
	for _, part := range strings.Split(filepath.Dir(name), string(filepath.Separator)) {
		prefix = filepath.Join(prefix, part)

		fi, lerr := root.Lstat(prefix)
		if lerr != nil || fi.Mode()&os.ModeSymlink == 0 {
			continue
		}

		if _, serr := root.Stat(prefix); serr != nil && !errors.Is(serr, fs.ErrNotExist) {
			return fmt.Errorf("%s: %s: %w (%s)", filepath.Join(e.chroot, name), filepath.Join(e.chroot, prefix), ErrSymlinkEscapesChroot, e.chroot)
		}
	}

	return err
}

// symlinkTargetIsAbsolute reports whether a symbolic link target is absolute,
// including Windows drive-relative and rooted forms. Relative targets are not
// checked lexically: whether they leave the chroot depends on the links
// already on disk, so they are checked by resolving the created link through
// root instead.
func symlinkTargetIsAbsolute(target string) bool {
	target = filepath.FromSlash(target)
	return filepath.IsAbs(target) || filepath.VolumeName(target) != "" || strings.HasPrefix(target, string(filepath.Separator))
}

func (e *Extractor) createFile(ctx context.Context, root *os.Root, name string, file *zip.File) (err error) {
	if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	r, err := file.Open()
	if err != nil {
		return err
	}
	defer dclose(r, &err)

	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		return err
	}
	defer dclose(f, &err)

	bw := bufioWriterPool.Get().(*bufio.Writer)
	defer bufioWriterPool.Put(bw)

	bw.Reset(countWriter{f, &e.written, ctx})
	if _, err = bw.ReadFrom(r); err != nil {
		return err
	}

	err = bw.Flush()
	incOnSuccess(&e.entries, err)

	return err
}

func (e *Extractor) updateFileMetadata(root *os.Root, name string, file *zip.File) error {
	fields, err := zipextra.Parse(file.Extra)
	if err != nil {
		return err
	}

	if err := lchtimes(root, name, file.Mode(), time.Now(), file.Modified); err != nil {
		return err
	}

	if err := lchmod(root, name, file.Mode()); err != nil {
		return err
	}

	unixfield, ok := fields[zipextra.ExtraFieldUnixN]
	if !ok {
		return nil
	}

	unix, err := unixfield.InfoZIPNewUnix()
	if err != nil {
		return err
	}

	err = lchown(root, name, int(unix.Uid.Int64()), int(unix.Gid.Int64()))
	if err == nil {
		return nil
	}

	if e.options.chownErrorHandler == nil {
		return nil
	}

	e.m.Lock()
	defer e.m.Unlock()

	return e.options.chownErrorHandler(file.Name, err)
}
