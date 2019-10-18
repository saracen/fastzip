package fastzip

import (
	"io"
	"sync"

	"github.com/klauspost/compress/flate"
)

var flateReaderPool = sync.Pool{
	New: func() interface{} {
		return &flateReader{flate.NewReader(nil)}
	},
}

type flateReader struct {
	io.ReadCloser
}

func (fr *flateReader) Reset(r io.Reader) {
	fr.ReadCloser.(flate.Resetter).Reset(r, nil)
}

func (fr *flateReader) Close() error {
	err := fr.ReadCloser.Close()
	flateReaderPool.Put(fr)
	return err
}

func FlateDecompressor() func(r io.Reader) io.ReadCloser {
	return func(r io.Reader) io.ReadCloser {
		fr := flateReaderPool.Get().(*flateReader)
		fr.Reset(r)
		return fr
	}
}

func newFlateWriterPool(level int) *sync.Pool {
	pool := &sync.Pool{}
	pool.New = func() interface{} {
		fw, err := flate.NewWriter(nil, level)
		if err != nil {
			panic(err)
		}

		return &flateWriter{pool, fw}
	}
	return pool
}

type flateWriter struct {
	pool *sync.Pool
	*flate.Writer
}

func (fw *flateWriter) Reset(w io.Writer) {
	fw.Writer.Reset(w)
}

func (fw *flateWriter) Close() error {
	err := fw.Writer.Close()
	fw.pool.Put(fw)
	return err
}

// FlateCompressor provides a zip.Compressor but with a specific deflate
// level specified. Invalid flate levels will panic.
func FlateCompressor(level int) func(w io.Writer) (io.WriteCloser, error) {
	pool := newFlateWriterPool(level)

	return func(w io.Writer) (io.WriteCloser, error) {
		fw := pool.Get().(*flateWriter)
		fw.Reset(w)
		return fw, nil
	}
}
