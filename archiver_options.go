package fastzip

import (
	"errors"
)

var (
	ErrMinConcurrency = errors.New("concurrency must be at least 1")
)

// ArchiverOption is an option used when creating an archiver.
type ArchiverOption func(*archiverOptions) error

type archiverOptions struct {
	method      uint16
	concurrency int
	bufferSize  int
	stageDir    string
	offset      int64
}

// WithArchiverMethod sets the zip method to be used for compressible files.
func WithArchiverMethod(method uint16) ArchiverOption {
	return func(o *archiverOptions) error {
		o.method = method
		return nil
	}
}

// WithArchiverConcurrency will set the maximum number of files to be
// compressed concurrently. The default is set to GOMAXPROCS.
func WithArchiverConcurrency(n int) ArchiverOption {
	return func(o *archiverOptions) error {
		if n <= 0 {
			return ErrMinConcurrency
		}
		o.concurrency = n
		return nil
	}
}

// WithArchiverBufferSize sets the buffer size for each file to be compressed
// concurrently. If a compressed file's data exceeds the buffer size, a
// temporary file is written (to the stage directory) to hold the additional
// data. The default is 2 mebibytes, so if concurrency is 16, 32 mebibytes of
// memory will be allocated.
func WithArchiverBufferSize(n int) ArchiverOption {
	return func(o *archiverOptions) error {
		if n < 0 {
			n = 0
		}
		o.bufferSize = n
		return nil
	}
}

// WithStageDirectory sets the directory to be used to stage compressed files
// before they're written to the archive. The default is the directory to be
// archived.
func WithStageDirectory(dir string) ArchiverOption {
	return func(o *archiverOptions) error {
		o.stageDir = dir
		return nil
	}
}

// WithArchiverOffset sets the offset of the beginning of the zip data. This
// should be used when zip data is appended to an existing file.
func WithArchiverOffset(n int64) ArchiverOption {
	return func(o *archiverOptions) error {
		o.offset = n
		return nil
	}
}
