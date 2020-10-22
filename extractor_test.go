package fastzip

import (
	"context"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testExtract(t *testing.T, filename string, files map[string]testFile) {
	dir, err := ioutil.TempDir("", "fastzip-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	e, err := NewExtractor(filename, dir)
	require.NoError(t, err)
	defer e.Close()

	require.NoError(t, e.Extract(context.Background()))

	err = filepath.Walk(dir, func(pathname string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, pathname)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		rel = filepath.ToSlash(rel)
		require.Contains(t, files, rel)

		mode := files[rel].mode
		assert.Equal(t, mode.Perm(), fi.Mode().Perm(), "file %v perm not equal", rel)
		assert.Equal(t, mode.IsDir(), fi.IsDir(), "file %v is_dir not equal", rel)
		assert.Equal(t, mode&os.ModeSymlink, fi.Mode()&os.ModeSymlink, "file %v mode symlink not equal", rel)

		if fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		contents, err := ioutil.ReadFile(pathname)
		require.NoError(t, err)
		assert.Equal(t, string(files[rel].contents), string(contents))

		return nil
	})
	require.NoError(t, err)
}

func TestExtractCancelContext(t *testing.T) {
	twoMB := strings.Repeat("1", 2*1024*1024)
	testFiles := map[string]testFile{
		"foo.go":    {mode: 0666, contents: twoMB},
		"bar.go":    {mode: 0666, contents: twoMB},
		"foobar.go": {mode: 0666, contents: twoMB},
		"barfoo.go": {mode: 0666, contents: twoMB},
	}

	files, dir := testCreateFiles(t, testFiles)
	defer os.RemoveAll(dir)

	testCreateArchive(t, dir, files, func(filename, chroot string) {
		e, err := NewExtractor(filename, dir, WithExtractorConcurrency(1))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan struct{})
		go func() {
			defer func() { done <- struct{}{} }()

			require.EqualError(t, e.Extract(ctx), "context canceled")
		}()

		for {
			select {
			case <-done:
				return

			default:
				// cancel as soon as any data is written
				if bytes, _ := e.Written(); bytes > 0 {
					cancel()
				}
			}
		}
	})
}

func TestExtractorWithDecompressor(t *testing.T) {
	testFiles := map[string]testFile{
		"foo.go": {mode: 0666},
		"bar.go": {mode: 0666},
	}

	files, dir := testCreateFiles(t, testFiles)
	defer os.RemoveAll(dir)

	testCreateArchive(t, dir, files, func(filename, chroot string) {
		e, err := NewExtractor(filename, dir)
		require.NoError(t, err)
		e.RegisterDecompressor(zip.Deflate, StdFlateDecompressor())
		defer e.Close()

		require.NoError(t, e.Extract(context.Background()))
	})
}

func TestExtractorWithConcurrency(t *testing.T) {
	testFiles := map[string]testFile{
		"foo.go": {mode: 0666},
		"bar.go": {mode: 0666},
	}

	concurrencyTests := []struct {
		concurrency int
		pass        bool
	}{
		{-1, false},
		{0, false},
		{1, true},
		{30, true},
	}

	files, dir := testCreateFiles(t, testFiles)
	defer os.RemoveAll(dir)

	testCreateArchive(t, dir, files, func(filename, chroot string) {
		for _, test := range concurrencyTests {
			e, err := NewExtractor(filename, dir, WithExtractorConcurrency(test.concurrency))
			if test.pass {
				assert.NoError(t, err)
				require.NoError(t, e.Close())
			} else {
				assert.Error(t, err)
			}
		}
	})
}

func TestExtractorWithChownErrorHandler(t *testing.T) {
	testFiles := map[string]testFile{
		"foo.go": {mode: 0666},
		"bar.go": {mode: 0666},
	}

	files, dir := testCreateFiles(t, testFiles)
	defer os.RemoveAll(dir)

	testCreateArchive(t, dir, files, func(filename, chroot string) {
		e, err := NewExtractor(filename, dir, WithExtractorChownErrorHandler(func(name string, err error) error {
			assert.Fail(t, "should have no error")
			return nil
		}))
		assert.NoError(t, err)
		assert.NoError(t, e.Extract(context.Background()))
		require.NoError(t, e.Close())
	})
}

func TestExtractorFromReader(t *testing.T) {
	testFiles := map[string]testFile{
		"foo.go": {mode: 0666},
		"bar.go": {mode: 0666},
	}

	files, dir := testCreateFiles(t, testFiles)
	defer os.RemoveAll(dir)

	testCreateArchive(t, dir, files, func(filename, chroot string) {
		f, err := os.Open(filename)
		require.NoError(t, err)

		fi, err := f.Stat()
		require.NoError(t, err)

		e, err := NewExtractorFromReader(f, fi.Size(), chroot)
		require.NoError(t, err)
		require.NoError(t, e.Extract(context.Background()))
		require.NoError(t, e.Close())
	})
}

func benchmarkExtractOptions(b *testing.B, store, stdDeflate bool, options ...ExtractorOption) {
	files := make(map[string]os.FileInfo)
	filepath.Walk(*archiveDir, func(filename string, fi os.FileInfo, err error) error {
		files[filename] = fi
		return nil
	})

	dir, err := ioutil.TempDir("", "fastzip-benchmark-extract")
	require.NoError(b, err)
	defer os.RemoveAll(dir)

	archiveName := filepath.Join(dir, "fastzip-benchmark-extract.zip")
	f, err := os.Create(archiveName)
	require.NoError(b, err)
	defer os.Remove(f.Name())

	var a *Archiver
	if store {
		a, err = NewArchiver(f, *archiveDir, WithStageDirectory(dir), WithArchiverMethod(zip.Store))
	} else {
		a, err = NewArchiver(f, *archiveDir, WithStageDirectory(dir))
		a.RegisterCompressor(zip.Deflate, FlateCompressor(-1))
	}
	require.NoError(b, err)

	err = a.Archive(context.Background(), files)
	require.NoError(b, err)
	require.NoError(b, a.Close())
	require.NoError(b, f.Close())

	b.ReportAllocs()
	b.ResetTimer()

	fi, _ := os.Stat(archiveName)
	b.SetBytes(fi.Size())
	for n := 0; n < b.N; n++ {
		e, err := NewExtractor(archiveName, dir, options...)
		if stdDeflate {
			e.RegisterDecompressor(zip.Deflate, StdFlateDecompressor())
		}
		require.NoError(b, err)
		require.NoError(b, e.Extract(context.Background()))
	}
}

func BenchmarkExtractStore_1(b *testing.B) {
	benchmarkExtractOptions(b, true, true, WithExtractorConcurrency(1))
}

func BenchmarkExtractStore_2(b *testing.B) {
	benchmarkExtractOptions(b, true, true, WithExtractorConcurrency(2))
}

func BenchmarkExtractStore_4(b *testing.B) {
	benchmarkExtractOptions(b, true, true, WithExtractorConcurrency(4))
}

func BenchmarkExtractStore_8(b *testing.B) {
	benchmarkExtractOptions(b, true, true, WithExtractorConcurrency(8))
}

func BenchmarkExtractStore_16(b *testing.B) {
	benchmarkExtractOptions(b, true, true, WithExtractorConcurrency(16))
}

func BenchmarkExtractStandardFlate_1(b *testing.B) {
	benchmarkExtractOptions(b, false, true, WithExtractorConcurrency(1))
}

func BenchmarkExtractStandardFlate_2(b *testing.B) {
	benchmarkExtractOptions(b, false, true, WithExtractorConcurrency(2))
}

func BenchmarkExtractStandardFlate_4(b *testing.B) {
	benchmarkExtractOptions(b, false, true, WithExtractorConcurrency(4))
}

func BenchmarkExtractStandardFlate_8(b *testing.B) {
	benchmarkExtractOptions(b, false, true, WithExtractorConcurrency(8))
}

func BenchmarkExtractStandardFlate_16(b *testing.B) {
	benchmarkExtractOptions(b, false, true, WithExtractorConcurrency(16))
}

func BenchmarkExtractNonStandardFlate_1(b *testing.B) {
	benchmarkExtractOptions(b, false, false, WithExtractorConcurrency(1))
}

func BenchmarkExtractNonStandardFlate_2(b *testing.B) {
	benchmarkExtractOptions(b, false, false, WithExtractorConcurrency(2))
}

func BenchmarkExtractNonStandardFlate_4(b *testing.B) {
	benchmarkExtractOptions(b, false, false, WithExtractorConcurrency(4))
}

func BenchmarkExtractNonStandardFlate_8(b *testing.B) {
	benchmarkExtractOptions(b, false, false, WithExtractorConcurrency(8))
}

func BenchmarkExtractNonStandardFlate_16(b *testing.B) {
	benchmarkExtractOptions(b, false, false, WithExtractorConcurrency(16))
}
