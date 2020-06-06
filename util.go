package fastzip

import (
	"context"
	"io"
	"sync/atomic"
)

func dclose(c io.Closer, err *error) {
	if cerr := c.Close(); cerr != nil && *err == nil {
		*err = cerr
	}
}

func incOnSuccess(inc *int64, err error) {
	if err == nil {
		atomic.AddInt64(inc, 1)
	}
}

type countWriter struct {
	w       io.Writer
	written *int64
	ctx     context.Context
}

func (w countWriter) Write(p []byte) (n int, err error) {
	if err = w.ctx.Err(); err == nil {
		n, err = w.w.Write(p)

		atomic.AddInt64(w.written, int64(n))
	}
	return n, err
}
