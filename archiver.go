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

	"github.com/saracen/fastzip/internal/filepool"
	"github.com/saracen/fastzip/internal/zip"
	"golang.org/x/sync/errgroup"
)

const irregularModes = os.ModeSocket | os.ModeDevice | os.ModeCharDevice | os.ModeNamedPipe

var bufioReaderPool = sync.Pool{
	New: func() interface{} {
		return bufio.NewReaderSize(nil, 32*1024)
	},
}

// Archiver is an opinionated Zip archiver.
//
// Only regular files, symlinks and directories are supported. Only files that
// are children of the specified chroot directory will be archived.
//
// Access permissions, ownership (unix) and modification times are preserved.
type Archiver struct {
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
	a.options.concurrency = runtime.NumCPU()
	a.options.stageDir = chroot
	for _, o := range opts {
		err := o(&a.options)
		if err != nil {
			return nil, err
		}
	}

	a.zw = zip.NewWriter(w)

	// register standard flate compressor
	a.RegisterCompressor(zip.Deflate, stdFlateCompressor(5))

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

// Archive archives all files, symlinks and directories.
func (a *Archiver) Archive(files map[string]os.FileInfo) (err error) {
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
		fp, err = filepool.New(a.options.stageDir, concurrency)
		if err != nil {
			return err
		}
		defer fp.Close()
	}

	wg, ctx := errgroup.WithContext(context.Background())
	defer func() {
		if werr := wg.Wait(); werr != nil {
			err = werr
		}
	}()

	for _, name := range names {
		fi := files[name]
		if fi.Mode()&irregularModes != 0 {
			continue
		}

		path, err := filepath.Abs(name)
		if err != nil {
			return err
		}

		if !strings.HasPrefix(path, a.chroot) {
			return fmt.Errorf("%s cannot be archived from outside of chroot (%s)", name, a.chroot)
		}

		hdr, err := zip.FileInfoHeader(fi)
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(a.chroot, path)
		if err != nil {
			return err
		}

		hdr.Name = filepath.ToSlash(rel)
		if hdr.Mode().IsDir() {
			hdr.Name += "/"
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		switch hdr.Mode() & os.ModeType {
		case os.ModeDir:
			err = a.createDirectory(hdr)

		case os.ModeSymlink:
			err = a.createSymlink(path, hdr)

		default:
			hdr.Method = a.options.method

			if fp == nil {
				err = a.createFile(path, hdr, nil)
			} else {
				f := fp.Get()
				wg.Go(func() error {
					defer func() { fp.Put(f) }()

					return a.createFile(path, hdr, f)
				})
			}
		}

		if err != nil {
			return err
		}
	}

	return wg.Wait()
}

func (a *Archiver) createDirectory(hdr *zip.FileHeader) error {
	a.m.Lock()
	defer a.m.Unlock()

	_, err := a.createHeader(hdr)
	return err
}

func (a *Archiver) createSymlink(path string, hdr *zip.FileHeader) error {
	a.m.Lock()
	defer a.m.Unlock()

	w, err := a.createHeader(hdr)
	if err != nil {
		return err
	}

	link, err := os.Readlink(path)
	if err != nil {
		return err
	}

	_, err = io.WriteString(w, link)
	return err
}

func (a *Archiver) createFile(path string, hdr *zip.FileHeader, tmp *filepool.File) (err error) {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dclose(f, &err)

	br := bufioReaderPool.Get().(*bufio.Reader)
	defer bufioReaderPool.Put(br)
	br.Reset(f)

	comp, ok := a.compressors[hdr.Method]
	// if we don't have the registered compressor, it most likely means Store is
	// being used, so we revert to non-concurrent behaviour
	if !ok || tmp == nil {
		a.m.Lock()
		defer a.m.Unlock()

		w, err := a.createHeader(hdr)
		if err != nil {
			return err
		}

		_, err = br.WriteTo(w)
		return err
	}

	// if we have the compressor, let's compress to a file and then copy to the
	// zip concurrently
	fw, err := comp(tmp)
	if err != nil {
		return err
	}

	_, err = io.Copy(io.MultiWriter(fw, tmp.Hasher()), br)
	dclose(fw, &err)
	if err != nil {
		return err
	}

	hdr.CompressedSize64 = tmp.Written()
	// if compressed file is larger, use the uncompressed version.
	if hdr.CompressedSize64 > hdr.UncompressedSize64 {
		hdr.Method = zip.Store
		return a.createFile(path, hdr, nil)
	}
	hdr.CRC32 = tmp.Checksum()

	a.m.Lock()
	defer a.m.Unlock()

	w, err := a.createHeaderRaw(hdr)
	if err != nil {
		return err
	}

	br.Reset(tmp)
	_, err = io.Copy(w, br)
	return err
}
