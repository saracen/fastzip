package fastzip

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"hash/crc32"
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

	// order serializes entry writes so the archive is deterministic. It is
	// per-call state: Archive may be called concurrently on one Archiver, so
	// it must not live on the struct. It is nil unless stable file ordering
	// is enabled and entries are actually written concurrently; at a
	// concurrency of 1 the dispatch loop already writes them in order.
	var order *writeSerializer
	if a.options.stableFileOrder && fp != nil {
		// Entries that finish before their turn may be copied out of their
		// filepool slot and held in memory until written, up to this budget.
		// Tie it to the filepool's own footprint so memory scales the same way.
		bufferSize := a.options.bufferSize
		if bufferSize < 0 {
			bufferSize = filepool.DefaultBufferSize
		}
		order = newWriteSerializer(int64(concurrency) * int64(bufferSize))
		// Registered after the wg.Wait deferral above so it runs first (defers
		// are LIFO): any goroutine blocked on its write turn is released before
		// we wait on it, avoiding a hang when we return before dispatching every
		// entry.
		defer order.abort()
	}

	hdrs := make([]zip.FileHeader, len(names))

	// idx numbers the entries that are actually archived, contiguously from 0,
	// so the write serializer never stalls on a skipped index.
	idx := 0
	for _, name := range names {
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

		entryIdx := idx
		idx++

		hdr := &hdrs[entryIdx]
		fileInfoHeader(rel, fi, hdr)

		if ctx.Err() != nil {
			return ctx.Err()
		}

		switch {
		case hdr.Mode()&os.ModeSymlink != 0:
			err = a.createSymlink(order, entryIdx, path, fi, hdr)

		case hdr.Mode().IsDir():
			err = a.createDirectory(order, entryIdx, fi, hdr)

		default:
			if hdr.UncompressedSize64 > 0 {
				hdr.Method = a.options.method
			}

			if fp == nil {
				err = a.createFile(ctx, order, entryIdx, path, fi, hdr, nil)
			} else {
				f := fp.Get()
				wg.Go(func() error {
					err := a.createFile(ctx, order, entryIdx, path, fi, hdr, f)
					fp.Put(f)
					if err != nil && order != nil {
						// createFile can fail before it reaches its write turn
						// (for example os.Open or a compression error), leaving
						// the turn unconsumed. Abort so any later entry waiting on
						// this one is released instead of blocking wg.Wait below.
						// abort does not wait for a write in progress, so the
						// error reaches the errgroup at once and its ctx
						// cancellation stops that write at its next chunk.
						order.abort()
					}
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

// writeEntry runs fn with exclusive access to the underlying zip.Writer. Every
// write goes through here, including ordered ones: the serializer only decides
// when an entry may be written, and Archive calls running concurrently on one
// Archiver each have their own serializer.
func (a *Archiver) writeEntry(fn func() error) error {
	a.m.Lock()
	defer a.m.Unlock()
	return fn()
}

// writeEntryNoWait writes an entry that is dispatched synchronously and holds
// no filepool file (directories and symlinks). With stable file ordering the
// write is queued rather than waited for, so the dispatch loop does not wait
// for every earlier file to finish compressing each time it meets a directory.
// The queued write runs, in order, from whichever goroutine completes the
// entry before it; its error surfaces there.
func (a *Archiver) writeEntryNoWait(order *writeSerializer, idx int, fn func() error) error {
	if order != nil {
		return order.enqueue(idx, func() error { return a.writeEntry(fn) })
	}
	return a.writeEntry(fn)
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

func (a *Archiver) createDirectory(order *writeSerializer, idx int, fi os.FileInfo, hdr *zip.FileHeader) error {
	return a.writeEntryNoWait(order, idx, func() error {
		_, err := a.createHeader(fi, hdr)
		incOnSuccess(&a.entries, err)
		return err
	})
}

func (a *Archiver) createSymlink(order *writeSerializer, idx int, path string, fi os.FileInfo, hdr *zip.FileHeader) error {
	link, err := os.Readlink(path)
	if err != nil {
		return err
	}

	// Don't use a data descriptor to shave a few bytes and to make sure that the symlink can be stream-unzipped
	hdr.Flags &= ^uint16(0x8)
	hdr.Method = zip.Store
	hdr.CompressedSize64 = uint64(len(link))
	hdr.UncompressedSize64 = hdr.CompressedSize64
	hdr.CRC32 = crc32.ChecksumIEEE([]byte(link))

	return a.writeEntryNoWait(order, idx, func() error {
		w, err := a.createHeaderRaw(fi, hdr)
		if err != nil {
			return err
		}

		_, err = io.WriteString(w, link)
		incOnSuccess(&a.entries, err)
		return err
	})
}

func (a *Archiver) createFile(ctx context.Context, order *writeSerializer, idx int, path string, fi os.FileInfo, hdr *zip.FileHeader, tmp *filepool.File) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return a.compressFile(ctx, order, idx, f, fi, hdr, tmp)
}

// compressFile pre-compresses the file first to a file from the filepool,
// making use of zip.CreateRaw. This allows for concurrent files to be
// compressed and then added to the zip file when ready.
// If no filepool file is available (when using a concurrency of 1) or the
// compressed file is larger than the uncompressed version, the file is moved
// to the zip file using the conventional zip.CreateHeader.
func (a *Archiver) compressFile(ctx context.Context, order *writeSerializer, idx int, f *os.File, fi os.FileInfo, hdr *zip.FileHeader, tmp *filepool.File) error {
	comp, ok := a.compressors[hdr.Method]
	// if we don't have the registered compressor, it most likely means Store is
	// being used, so we revert to non-concurrent behaviour
	if !ok || tmp == nil {
		return a.compressFileSimple(ctx, order, idx, f, fi, hdr)
	}

	fw, err := comp(tmp)
	if err != nil {
		return err
	}

	br := bufioReaderPool.Get().(*bufio.Reader)
	defer bufioReaderPool.Put(br)
	br.Reset(f)

	n, err := io.Copy(io.MultiWriter(fw, tmp.Hasher()), br)
	dclose(fw, &err)
	if err != nil {
		return err
	}

	hdr.Flags |= 0x8
	// Use the size actually read rather than the stat-time size: the file may
	// have changed since enumeration, and CreateRaw writes these sizes into
	// the headers as given, so a stale size would make the entry unreadable.
	hdr.UncompressedSize64 = uint64(n)
	hdr.CompressedSize64 = tmp.Written()
	// if compressed file is larger, use the uncompressed version.
	if hdr.CompressedSize64 > hdr.UncompressedSize64 {
		f.Seek(0, io.SeekStart)
		hdr.Method = zip.Store
		return a.compressFileSimple(ctx, order, idx, f, fi, hdr)
	}
	hdr.CRC32 = tmp.Checksum()

	br.Reset(tmp)
	return a.writeFileEntry(ctx, order, idx, int64(hdr.CompressedSize64), br, func() (io.Writer, error) {
		return a.createHeaderRaw(fi, hdr)
	})
}

// compressFileSimple uses the conventional zip.createHeader. This differs from
// compressFile as it locks the zip _whilst_ compressing (if the method is not
// Store).
func (a *Archiver) compressFileSimple(ctx context.Context, order *writeSerializer, idx int, f *os.File, fi os.FileInfo, hdr *zip.FileHeader) error {
	br := bufioReaderPool.Get().(*bufio.Reader)
	defer bufioReaderPool.Put(br)
	br.Reset(f)

	return a.writeFileEntry(ctx, order, idx, int64(hdr.UncompressedSize64), br, func() (io.Writer, error) {
		return a.createHeader(fi, hdr)
	})
}

// writeFileEntry writes a file entry whose data is available from src, which
// is expected to hold n bytes. create opens the entry in the zip once the write
// has exclusive access to it.
//
// Without stable ordering, or when it is already this entry's turn, the data is
// streamed from src. Otherwise the caller would have to wait for earlier
// entries while holding its filepool slot, which starves the pool when many
// small files sit behind a slow one. So if n fits the serializer's memory
// budget, the data is copied out of src and the write queued, letting the
// caller return its slot right away. Over budget, it waits with the slot.
func (a *Archiver) writeFileEntry(ctx context.Context, order *writeSerializer, idx int, n int64, src io.Reader, create func() (io.Writer, error)) error {
	write := func(r io.Reader) error {
		return a.writeEntry(func() error {
			w, err := create()
			if err != nil {
				return err
			}

			_, err = io.Copy(countWriter{w, &a.written, ctx}, r)
			incOnSuccess(&a.entries, err)
			return err
		})
	}
	// stream reads src when it runs, so it sees the reassignment below.
	stream := func() error { return write(src) }

	if order == nil {
		return stream()
	}

	if ran, err := order.tryDo(idx, stream); ran {
		return err
	}

	if !order.reserve(n) {
		return order.do(idx, stream)
	}

	// Read one byte past n so a source that grew since it was stat'd is
	// noticed rather than silently truncated.
	buf := make([]byte, n+1)
	m, err := io.ReadFull(src, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		order.release(n)
		return err
	}
	if m <= int(n) {
		return order.enqueueBytes(idx, n, func() error {
			return write(bytes.NewReader(buf[:m]))
		})
	}

	// Larger than expected: write everything, including what was already
	// read, from the source once it is our turn.
	order.release(n)
	src = io.MultiReader(bytes.NewReader(buf), src)
	return order.do(idx, stream)
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
