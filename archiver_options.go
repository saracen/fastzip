package fastzip

import (
	"errors"
)

var ErrMinConcurrency = errors.New("concurrency must be at least 1")

// ArchiverOption is an option used when creating an archiver.
type ArchiverOption func(*archiverOptions) error

type archiverOptions struct {
	method      uint16
	concurrency int
	stageDir    string
}

func WithArchiverMethod(method uint16) ArchiverOption {
	return func(o *archiverOptions) error {
		o.method = method
		return nil
	}
}

func WithArchiverConcurrency(n int) ArchiverOption {
	return func(o *archiverOptions) error {
		if n <= 0 {
			return ErrMinConcurrency
		}
		o.concurrency = n
		return nil
	}
}

func WithStageDirectoryMethod(dir string) ArchiverOption {
	return func(o *archiverOptions) error {
		o.stageDir = dir
		return nil
	}
}
