package filepool

import (
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type filePoolCloseError []error

func (e filePoolCloseError) Len() int {
	return len(e)
}

func (e filePoolCloseError) Error() string {
	if len(e) == 1 {
		return e[0].Error()
	}

	var sb strings.Builder
	for _, err := range e {
		sb.WriteString(err.Error() + "\n")
	}

	return sb.String()
}

func (e filePoolCloseError) Unwrap() error {
	if len(e) > 1 {
		return e[1:]
	}
	return nil
}

// FilePool represents a pool of files that can be used as buffers.
type FilePool struct {
	files   []*File
	limiter chan int
}

// New returns a new FilePool.
func New(dir string, size int) (*FilePool, error) {
	if size <= 0 {
		return nil, fmt.Errorf("pool size must be greater than zero")
	}
	fp := &FilePool{}

	fp.files = make([]*File, size)
	fp.limiter = make(chan int, size)

	var err error
	for i := range fp.files {
		fp.files[i], err = newFile(dir, i)
		if err != nil {
			fp.Close()
			return nil, err
		}
		fp.limiter <- i
	}

	return fp, nil
}

// Get gets a file from the pool.
func (fp *FilePool) Get() *File {
	idx := <-fp.limiter
	return fp.files[idx]
}

// Put puts a file back into the pool.
func (fp *FilePool) Put(f *File) {
	f.reset()
	fp.limiter <- f.idx
}

// Close closes and removes all files in the pool.
func (fp *FilePool) Close() error {
	var err filePoolCloseError
	for _, f := range fp.files {
		if f == nil {
			continue
		}

		if cerr := f.f.Close(); cerr != nil {
			err = append(err, cerr)
		}
		if rerr := os.Remove(f.f.Name()); rerr != nil && !os.IsNotExist(rerr) {
			err = append(err, rerr)
		}
	}

	fp.files = nil
	if err.Len() > 0 {
		return err
	}
	return nil
}

// File is a file backed buffer.
type File struct {
	f   *os.File
	idx int
	w   int64
	r   int64
	crc hash.Hash32
}

func newFile(dir string, idx int) (f *File, err error) {
	f = &File{idx: idx}
	f.crc = crc32.NewIEEE()
	f.f, err = os.Create(filepath.Join(dir, fmt.Sprintf("fastzip_%02d", idx)))
	return
}

func (f *File) Write(p []byte) (n int, err error) {
	n, err = f.f.WriteAt(p, f.w)
	f.w += int64(n)
	return
}

func (f *File) Read(p []byte) (n int, err error) {
	remaining := f.w - f.r
	if remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > remaining {
		p = p[0:remaining]
	}
	n, err = f.f.ReadAt(p, f.r)
	f.r += int64(n)
	return
}

func (f *File) Written() uint64 {
	return uint64(f.w)
}

func (f *File) Hasher() io.Writer {
	return f.crc
}

func (f *File) Checksum() uint32 {
	return f.crc.Sum32()
}

func (f *File) reset() {
	f.w = 0
	f.r = 0
	f.crc.Reset()
}
