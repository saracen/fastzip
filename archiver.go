package fastzip

import (
	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const irregularModes = os.ModeSocket | os.ModeDevice | os.ModeCharDevice | os.ModeNamedPipe

// Archiver is an opinionated Zip archiver.
//
// Only regular files, symlinks and directories are supported. Only files that
// are children of the specified chroot directory will be archived.
//
// Access permissions, ownership (unix) and modification times are preserved.
type Archiver struct {
	zw      *zip.Writer
	br      *bufio.Reader
	options archiverOptions
	chroot  string
}

// NewArchiver returns a new Archiver.
func NewArchiver(w io.Writer, chroot string, opts ...ArchiverOption) (*Archiver, error) {
	var err error
	if chroot, err = filepath.Abs(chroot); err != nil {
		return nil, err
	}

	a := &Archiver{
		br:     bufio.NewReaderSize(nil, 32*1024),
		chroot: chroot,
	}

	a.options.method = zip.Deflate
	for _, o := range opts {
		err := o(&a.options)
		if err != nil {
			return nil, err
		}
	}

	a.zw = zip.NewWriter(w)

	return a, nil
}

// RegisterCompressor registers custom compressors for a specified method ID.
// The common methods Store and Deflate are built in.
func (a *Archiver) RegisterCompressor(method uint16, comp zip.Compressor) {
	a.zw.RegisterCompressor(method, comp)
}

// Close closes the underlying ZipWriter.
func (a *Archiver) Close() error {
	return a.zw.Close()
}

// Archive archives all files, symlinks and directories.
func (a *Archiver) Archive(files map[string]os.FileInfo) error {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

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

		switch hdr.Mode() & os.ModeType {
		case os.ModeDir:
			err = a.createDirectory(hdr)

		case os.ModeSymlink:
			err = a.createSymlink(path, hdr)

		default:
			err = a.createFile(path, hdr)
		}

		if err != nil {
			return err
		}
	}

	return nil
}

func (a *Archiver) createDirectory(hdr *zip.FileHeader) error {
	_, err := a.createHeader(hdr)
	return err
}

func (a *Archiver) createSymlink(path string, hdr *zip.FileHeader) error {
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

func (a *Archiver) createFile(path string, hdr *zip.FileHeader) (err error) {
	hdr.Method = a.options.method
	w, err := a.createHeader(hdr)
	if err != nil {
		return err
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dclose(f, &err)

	a.br.Reset(f)
	_, err = a.br.WriteTo(w)

	return err
}
