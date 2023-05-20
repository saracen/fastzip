package fastzip

import (
	"bufio"
	"io"
	"sync"

	stdflate "compress/flate"

	"github.com/klauspost/compress/flate"
	"github.com/klauspost/compress/zstd"
)

type flater interface {
	Close() error
	Flush() error
	Reset(dst io.Writer)
	Write(data []byte) (n int, err error)
}

func newFlateReaderPool(newReaderFn func(w io.Reader) io.ReadCloser) *sync.Pool {
	pool := &sync.Pool{}
	pool.New = func() interface{} {
		return &flateReader{pool, bufio.NewReaderSize(nil, 32*1024), newReaderFn(nil)}
	}
	return pool
}

type flateReader struct {
	pool *sync.Pool
	buf  *bufio.Reader
	io.ReadCloser
}

func (fr *flateReader) Reset(r io.Reader) {
	fr.buf.Reset(r)
	fr.ReadCloser.(flate.Resetter).Reset(fr.buf, nil)
}

func (fr *flateReader) Close() error {
	err := fr.ReadCloser.Close()
	fr.pool.Put(fr)
	return err
}

// FlateDecompressor returns a pooled performant zip.Decompressor.
func FlateDecompressor() func(r io.Reader) io.ReadCloser {
	pool := newFlateReaderPool(flate.NewReader)

	return func(r io.Reader) io.ReadCloser {
		fr := pool.Get().(*flateReader)
		fr.Reset(r)
		return fr
	}
}

// StdFlateDecompressor returns a pooled standard library zip.Decompressor.
func StdFlateDecompressor() func(r io.Reader) io.ReadCloser {
	pool := newFlateReaderPool(stdflate.NewReader)

	return func(r io.Reader) io.ReadCloser {
		fr := pool.Get().(*flateReader)
		fr.Reset(r)
		return fr
	}
}

type zstdReader struct {
	pool *sync.Pool
	buf  *bufio.Reader
	*zstd.Decoder
}

func (zr *zstdReader) Close() error {
	err := zr.Decoder.Reset(nil)
	zr.pool.Put(zr)
	return err
}

// ZstdDecompressor returns a pooled zstd decoder.
func ZstdDecompressor() func(r io.Reader) io.ReadCloser {
	pool := &sync.Pool{}
	pool.New = func() interface{} {
		r, _ := zstd.NewReader(nil, zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxWindow(128<<20), zstd.WithDecoderConcurrency(1))
		return &zstdReader{pool, bufio.NewReaderSize(nil, 32*1024), r}
	}

	return func(r io.Reader) io.ReadCloser {
		fr := pool.Get().(*zstdReader)
		fr.Decoder.Reset(r)
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

// FlateCompressor returns a pooled performant zip.Compressor configured to a
// specified compression level. Invalid flate levels will panic.
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

// StdFlateCompressor returns a pooled standard library zip.Compressor
// configured to a specified compression level. Invalid flate levels will
// panic.
func StdFlateCompressor(level int) func(w io.Writer) (io.WriteCloser, error) {
	pool := newFlateWriterPool(level, func(w io.Writer, level int) (flater, error) {
		return stdflate.NewWriter(w, level)
	})

	return func(w io.Writer) (io.WriteCloser, error) {
		fw := pool.Get().(*flateWriter)
		fw.Reset(w)
		return fw, nil
	}
}

func ZstdCompressor(level int) func(w io.Writer) (io.WriteCloser, error) {
	pool := newFlateWriterPool(level, func(w io.Writer, level int) (flater, error) {
		return zstd.NewWriter(w, zstd.WithEncoderCRC(false), zstd.WithEncoderLevel(zstd.EncoderLevel(level)))
	})

	return func(w io.Writer) (io.WriteCloser, error) {
		fw := pool.Get().(*flateWriter)
		fw.Reset(w)
		return fw, nil
	}
}
