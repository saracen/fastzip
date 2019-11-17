package fastzip

import (
	"bufio"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/saracen/fastzip/internal/zip"
	"github.com/saracen/zipextra"
	"golang.org/x/sync/errgroup"
)

var bufioWriterPool = sync.Pool{
	New: func() interface{} {
		return bufio.NewWriterSize(nil, 32*1024)
	},
}

var defaultDecompressor = FlateDecompressor()

// Extractor is an opinionated Zip file extractor.
//
// Files are extracted in parallel. Only regular files, symlinks and directories
// are supported. Files can only be extracted to the specified chroot directory.
//
// Access permissions, ownership (unix) and modification times are preserved.
type Extractor struct {
	zr      *zip.ReadCloser
	m       sync.Mutex
	options extractorOptions
	chroot  string

	written, entries int64
}

// NewExtractor returns a new extractor.
func NewExtractor(filename string, chroot string, opts ...ExtractorOption) (*Extractor, error) {
	var err error
	if chroot, err = filepath.Abs(chroot); err != nil {
		return nil, err
	}

	e := &Extractor{
		chroot: chroot,
	}

	e.options.concurrency = runtime.NumCPU()
	for _, o := range opts {
		err := o(&e.options)
		if err != nil {
			return nil, err
		}
	}

	e.zr, err = zip.OpenReader(filename)
	if err != nil {
		return nil, err
	}

	e.RegisterDecompressor(zip.Deflate, defaultDecompressor)

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
	return e.zr.Close()
}

// Written returns how many bytes and entries have been written to disk.
// Written can be called whilst extraction is in progress.
func (e *Extractor) Written() (bytes, entries int64) {
	return atomic.LoadInt64(&e.written), atomic.LoadInt64(&e.entries)
}

// Extract calls ExtractWithContext with a background context.
func (e *Extractor) Extract() error {
	return e.ExtractWithContext(context.Background())
}

// ExtractWithContext extracts files, creates symlinks and directories from the
// archive.
func (e *Extractor) ExtractWithContext(ctx context.Context) (err error) {
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

		var path string
		path, err = filepath.Abs(filepath.Join(e.chroot, file.Name))
		if err != nil {
			return err
		}

		if !strings.HasPrefix(path, e.chroot) {
			return fmt.Errorf("%s cannot be extracted outside of chroot (%s)", path, e.chroot)
		}

		if err = os.MkdirAll(filepath.Dir(path), 0777); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		switch {
		case file.Mode().IsDir():
			err = e.createDirectory(path, file)

		case file.Mode()&os.ModeSymlink != 0:
			err = e.createSymlink(path, file)

		default:
			limiter <- struct{}{}

			gf := e.zr.File[i]
			wg.Go(func() error {
				defer func() { <-limiter }()
				err := e.createFile(ctx, path, gf)
				if err == nil {
					err = e.updateFileMetadata(path, gf)
				}
				return err
			})
		}
		if err != nil {
			return err
		}
	}

	if err = wg.Wait(); err != nil {
		return err
	}

	// update directory metadata last, otherwise modification dates are
	// incorrect.
	for _, file := range e.zr.File {
		if !file.Mode().IsDir() {
			continue
		}

		path, err := filepath.Abs(filepath.Join(e.chroot, file.Name))
		if err != nil {
			return err
		}

		err = e.updateFileMetadata(path, file)
		if err != nil {
			return err
		}
	}

	return nil
}

func (e *Extractor) createDirectory(path string, file *zip.File) error {
	err := os.Mkdir(path, file.Mode().Perm())
	if os.IsExist(err) {
		err = nil
	}
	return dinc(&e.entries, &err)
}

func (e *Extractor) createSymlink(path string, file *zip.File) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}

	r, err := file.Open()
	if err != nil {
		return err
	}
	defer r.Close()

	name, err := ioutil.ReadAll(r)
	if err != nil {
		return err
	}

	if err = os.Symlink(string(name), path); err != nil {
		return err
	}

	err = e.updateFileMetadata(path, file)

	return dinc(&e.entries, &err)
}

func (e *Extractor) createFile(ctx context.Context, path string, file *zip.File) (err error) {
	defer dinc(&e.entries, &err)

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}

	r, err := file.Open()
	if err != nil {
		return err
	}
	defer dclose(r, &err)

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
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

	return bw.Flush()
}

func (e *Extractor) updateFileMetadata(path string, file *zip.File) (err error) {
	fields, err := zipextra.Parse(file.Extra)
	if err != nil {
		return err
	}

	if err = lchtimes(path, file.Mode(), time.Now(), file.Modified); err != nil {
		return err
	}

	if err = lchmod(path, file.Mode()); err != nil {
		return err
	}

	if unixfield, ok := fields[zipextra.ExtraFieldUnixN]; ok {
		unix, err := unixfield.InfoZIPNewUnix()
		if err != nil {
			return err
		}

		if err := lchown(path, int(unix.Uid.Int64()), int(unix.Gid.Int64())); err != nil {
			if e.options.chownErrorHandler != nil {
				e.m.Lock()
				defer e.m.Unlock()

				if err = e.options.chownErrorHandler(file.Name, err); err != nil {
					return err
				}
			}
		}
	}

	return
}
