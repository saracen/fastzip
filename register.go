package fastzip

import (
	"io"
	"sync"

	stdflate "compress/flate"

	"github.com/klauspost/compress/flate"
)

type flater interface {
	Close() error
	Flush() error
	Reset(dst io.Writer)
	Write(data []byte) (n int, err error)
}

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

func newFlateWriterPool(level int, newWriterFn func(w io.Writer, level int) (flater, error)) *sync.Pool {
	pool := &sync.Pool{}
	pool.New = func() interface{} {
		fw, err := newWriterFn(nil, level)
		if err != nil {
			panic(err)
		}

		return &flateWriter{pool, fw}
	}
	return pool
}

type flateWriter struct {
	pool *sync.Pool
	flater
}

func (fw *flateWriter) Reset(w io.Writer) {
	fw.flater.Reset(w)
}

func (fw *flateWriter) Close() error {
	err := fw.flater.Close()
	fw.pool.Put(fw)
	return err
}

// FlateCompressor provides a zip.Compressor but with a specific deflate
// level specified. Invalid flate levels will panic.
func FlateCompressor(level int) func(w io.Writer) (io.WriteCloser, error) {
	pool := newFlateWriterPool(level, func(w io.Writer, level int) (flater, error) {
		return flate.NewWriter(w, level)
	})

	return func(w io.Writer) (io.WriteCloser, error) {
		fw := pool.Get().(*flateWriter)
		fw.Reset(w)
		return fw, nil
	}
}

func stdFlateCompressor(level int) func(w io.Writer) (io.WriteCloser, error) {
	pool := newFlateWriterPool(level, func(w io.Writer, level int) (flater, error) {
		return stdflate.NewWriter(w, level)
	})

	return func(w io.Writer) (io.WriteCloser, error) {
		fw := pool.Get().(*flateWriter)
		fw.Reset(w)
		return fw, nil
	}
}
