package fastzip

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/klauspost/compress/zip"
	"github.com/klauspost/compress/zstd"
	"github.com/saracen/fastzip/internal/filepool"
	"github.com/saracen/zipextra"
	"golang.org/x/sync/errgroup"
)

const irregularModes = os.ModeSocket | os.ModeDevice | os.ModeCharDevice | os.ModeNamedPipe

var bufioReaderPool = sync.Pool{
	New: func() interface{} {
		return bufio.NewReaderSize(nil, 32*1024)
	},
}

var (
	defaultCompressor     = FlateCompressor(-1)
	defaultZstdCompressor = ZstdCompressor(int(zstd.SpeedDefault))
)

// Archiver is an opinionated Zip archiver.
//
// Only regular files, symlinks and directories are supported. Only files that
// are children of the specified chroot directory will be archived.
//
// Access permissions, ownership (unix) and modification times are preserved.
type Archiver struct {
	// This 2 fields are accessed via atomic operations
	// They are at the start of the struct so they are properly 8 byte aligned
	written, entries int64

	zw      *zip.Writer
	options archiverOptions
	chroot  string
	m       sync.Mutex

	compressors map[uint16]zip.Compressor
}

// NewArchiver returns a new Archiver.
func NewArchiver(w io.Writer, chroot string, opts ...ArchiverOption) (*Archiver, error) {
	var err error
	if chroot, err = filepath.Abs(chroot); err != nil {
		return nil, err
	}

	a := &Archiver{
		chroot:      chroot,
		compressors: make(map[uint16]zip.Compressor),
	}

	a.options.method = zip.Deflate
	a.options.concurrency = runtime.GOMAXPROCS(0)
	a.options.stageDir = chroot
	a.options.bufferSize = -1
	for _, o := range opts {
		err := o(&a.options)
		if err != nil {
			return nil, err
		}
	}

	a.zw = zip.NewWriter(w)
	a.zw.SetOffset(a.options.offset)

	// register flate compressor
	a.RegisterCompressor(zip.Deflate, defaultCompressor)
	a.RegisterCompressor(zstd.ZipMethodWinZip, defaultZstdCompressor)

	return a, nil
}

// RegisterCompressor registers custom compressors for a specified method ID.
// The common methods Store and Deflate are built in.
func (a *Archiver) RegisterCompressor(method uint16, comp zip.Compressor) {
	a.zw.RegisterCompressor(method, comp)
	a.compressors[method] = comp
}

// Close closes the underlying ZipWriter.
func (a *Archiver) Close() error {
	return a.zw.Close()
}

// Written returns how many bytes and entries have been written to the archive.
// Written can be called whilst archiving is in progress.
func (a *Archiver) Written() (bytes, entries int64) {
	return atomic.LoadInt64(&a.written), atomic.LoadInt64(&a.entries)
}

// Archive archives all files, symlinks and directories.
func (a *Archiver) Archive(ctx context.Context, files map[string]os.FileInfo) (err error) {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	var fp *filepool.FilePool

	concurrency := a.options.concurrency
	if len(files) < concurrency {
		concurrency = len(files)
	}
	if concurrency > 1 {
		fp, err = filepool.New(a.options.stageDir, concurrency, a.options.bufferSize)
		if err != nil {
			return err
		}
		defer dclose(fp, &err)
	}

	wg, ctx := errgroup.WithContext(ctx)
	defer func() {
		if werr := wg.Wait(); werr != nil {
			err = werr
		}
	}()

	hdrs := make([]zip.FileHeader, len(names))

	for i, name := range names {
		fi := files[name]
		if fi.Mode()&irregularModes != 0 {
			continue
		}

		path, err := filepath.Abs(name)
		if err != nil {
			return err
		}

		if !strings.HasPrefix(path, a.chroot+string(filepath.Separator)) && path != a.chroot {
			return fmt.Errorf("%s cannot be archived from outside of chroot (%s)", name, a.chroot)
		}

		rel, err := filepath.Rel(a.chroot, path)
		if err != nil {
			return err
		}

		hdr := &hdrs[i]
		fileInfoHeader(rel, fi, hdr)

		if ctx.Err() != nil {
			return ctx.Err()
		}

		switch {
		case hdr.Mode()&os.ModeSymlink != 0:
			err = a.createSymlink(path, fi, hdr)

		case hdr.Mode().IsDir():
			err = a.createDirectory(fi, hdr)

		default:
			if hdr.UncompressedSize64 > 0 {
				hdr.Method = a.options.method
			}

			if fp == nil {
				err = a.createFile(ctx, path, fi, hdr, nil)
				incOnSuccess(&a.entries, err)
			} else {
				f := fp.Get()
				wg.Go(func() error {
					err := a.createFile(ctx, path, fi, hdr, f)
					fp.Put(f)
					incOnSuccess(&a.entries, err)
					return err
				})
			}
		}

		if err != nil {
			return err
		}
	}

	return wg.Wait()
}

func fileInfoHeader(name string, fi os.FileInfo, hdr *zip.FileHeader) {
	hdr.Name = filepath.ToSlash(name)
	hdr.UncompressedSize64 = uint64(fi.Size())
	hdr.Modified = fi.ModTime()
	hdr.SetMode(fi.Mode())

	if hdr.Mode().IsDir() {
		hdr.Name += "/"
	}

	const uint32max = (1 << 32) - 1
	if hdr.UncompressedSize64 > uint32max {
		hdr.UncompressedSize = uint32max
	} else {
		hdr.UncompressedSize = uint32(hdr.UncompressedSize64)
	}
}

func (a *Archiver) createDirectory(fi os.FileInfo, hdr *zip.FileHeader) error {
	a.m.Lock()
	defer a.m.Unlock()

	_, err := a.createHeader(fi, hdr)
	incOnSuccess(&a.entries, err)
	return err
}

func (a *Archiver) createSymlink(path string, fi os.FileInfo, hdr *zip.FileHeader) error {
	a.m.Lock()
	defer a.m.Unlock()

	w, err := a.createHeader(fi, hdr)
	if err != nil {
		return err
	}

	link, err := os.Readlink(path)
	if err != nil {
		return err
	}

	_, err = io.WriteString(w, link)
	incOnSuccess(&a.entries, err)
	return err
}

func (a *Archiver) createFile(ctx context.Context, path string, fi os.FileInfo, hdr *zip.FileHeader, tmp *filepool.File) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return a.compressFile(ctx, f, fi, hdr, tmp)
}

// compressFile pre-compresses the file first to a file from the filepool,
// making use of zip.CreateRaw. This allows for concurrent files to be
// compressed and then added to the zip file when ready.
// If no filepool file is available (when using a concurrency of 1) or the
// compressed file is larger than the uncompressed version, the file is moved
// to the zip file using the conventional zip.CreateHeader.
func (a *Archiver) compressFile(ctx context.Context, f *os.File, fi os.FileInfo, hdr *zip.FileHeader, tmp *filepool.File) error {
	comp, ok := a.compressors[hdr.Method]
	// if we don't have the registered compressor, it most likely means Store is
	// being used, so we revert to non-concurrent behaviour
	if !ok || tmp == nil {
		return a.compressFileSimple(ctx, f, fi, hdr)
	}

	fw, err := comp(tmp)
	if err != nil {
		return err
	}

	br := bufioReaderPool.Get().(*bufio.Reader)
	defer bufioReaderPool.Put(br)
	br.Reset(f)

	_, err = io.Copy(io.MultiWriter(fw, tmp.Hasher()), br)
	dclose(fw, &err)
	if err != nil {
		return err
	}

	hdr.CompressedSize64 = tmp.Written()
	// if compressed file is larger, use the uncompressed version.
	if hdr.CompressedSize64 > hdr.UncompressedSize64 {
		f.Seek(0, io.SeekStart)
		hdr.Method = zip.Store
		return a.compressFileSimple(ctx, f, fi, hdr)
	}
	hdr.CRC32 = tmp.Checksum()

	a.m.Lock()
	defer a.m.Unlock()

	w, err := a.createHeaderRaw(fi, hdr)
	if err != nil {
		return err
	}

	br.Reset(tmp)
	_, err = br.WriteTo(countWriter{w, &a.written, ctx})
	return err
}

// compressFileSimple uses the conventional zip.createHeader. This differs from
// compressFile as it locks the zip _whilst_ compressing (if the method is not
// Store).
func (a *Archiver) compressFileSimple(ctx context.Context, f *os.File, fi os.FileInfo, hdr *zip.FileHeader) error {
	br := bufioReaderPool.Get().(*bufio.Reader)
	defer bufioReaderPool.Put(br)
	br.Reset(f)

	a.m.Lock()
	defer a.m.Unlock()

	w, err := a.createHeader(fi, hdr)
	if err != nil {
		return err
	}

	_, err = br.WriteTo(countWriter{w, &a.written, ctx})
	return err
}

func (a *Archiver) createHeaderRaw(fi os.FileInfo, fh *zip.FileHeader) (io.Writer, error) {
	// When the standard Go library's version of CreateRaw was added, rather
	// than solely focus on custom compression in "raw" mode, it also removed
	// the convenience of setting up common zip flags and timestamp logic. This
	// here replicates what CreateHeader() does:
	// https://github.com/golang/go/blob/go1.17/src/archive/zip/writer.go#L271
	const zipVersion20 = 20

	utf8Valid1, utf8Require1 := detectUTF8(fh.Name)
	utf8Valid2, utf8Require2 := detectUTF8(fh.Comment)
	switch {
	case fh.NonUTF8:
		fh.Flags &^= 0x800
	case (utf8Require1 || utf8Require2) && (utf8Valid1 && utf8Valid2):
		fh.Flags |= 0x800
	}

	fh.CreatorVersion = fh.CreatorVersion&0xff00 | zipVersion20
	fh.ReaderVersion = zipVersion20

	if !fh.Modified.IsZero() {
		fh.ModifiedDate, fh.ModifiedTime = timeToMsDosTime(fh.Modified)
		fh.Extra = append(fh.Extra, zipextra.NewExtendedTimestamp(fh.Modified).Encode()...)
	}

	fh.Flags |= 0x8

	return a.createRaw(fi, fh)
}

// https://github.com/golang/go/blob/go1.17.7/src/archive/zip/writer.go#L229
func detectUTF8(s string) (valid, require bool) {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if r < 0x20 || r > 0x7d || r == 0x5c {
			if !utf8.ValidRune(r) || (r == utf8.RuneError && size == 1) {
				return false, false
			}
			require = true
		}
	}
	return true, require
}

// https://github.com/golang/go/blob/go1.17.7/src/archive/zip/struct.go#L242
func timeToMsDosTime(t time.Time) (fDate uint16, fTime uint16) {
	fDate = uint16(t.Day() + int(t.Month())<<5 + (t.Year()-1980)<<9)
	fTime = uint16(t.Second()/2 + t.Minute()<<5 + t.Hour()<<11)
	return
}
