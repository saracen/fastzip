package fastzip

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/saracen/fastzip/internal/zip"
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

	require.NoError(t, e.Extract())

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
		assert.Equal(t, mode.Perm(), fi.Mode().Perm(), "file %v modes not equal", rel)

		return nil
	})
	require.NoError(t, err)
}

func TestExtractorWithDecompressor(t *testing.T) {
	testFiles := map[string]testFile{
		"foo.go": testFile{mode: 0666},
		"bar.go": testFile{mode: 0666},
	}

	files, dir := testCreateFiles(t, testFiles)
	defer os.RemoveAll(dir)

	testCreateArchive(t, dir, files, func(filename, chroot string) {
		e, err := NewExtractor(filename, dir)
		require.NoError(t, err)
		e.RegisterDecompressor(zip.Deflate, FlateDecompressor())
		defer e.Close()

		require.NoError(t, e.Extract())
	})
}

func TestExtractorConcurrencyOption(t *testing.T) {
	testFiles := map[string]testFile{
		"foo.go": testFile{mode: 0666},
		"bar.go": testFile{mode: 0666},
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

func benchmarkExtractOptions(b *testing.B, stdDeflate bool, options ...ExtractorOption) {
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

	a, err := NewArchiver(f, *archiveDir, WithStageDirectoryMethod(dir))
	a.RegisterCompressor(zip.Deflate, FlateCompressor(5))
	require.NoError(b, err)

	err = a.Archive(files)
	require.NoError(b, err)
	require.NoError(b, a.Close())
	require.NoError(b, f.Close())

	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		e, err := NewExtractor(archiveName, dir, options...)
		if !stdDeflate {
			e.RegisterDecompressor(zip.Deflate, FlateDecompressor())
		}
		require.NoError(b, err)
		require.NoError(b, e.Extract())
	}
}

func BenchmarkExtractStore_1(b *testing.B) {
	benchmarkExtractOptions(b, true, WithExtractorConcurrency(1))
}

func BenchmarkExtractStore_2(b *testing.B) {
	benchmarkExtractOptions(b, true, WithExtractorConcurrency(2))
}

func BenchmarkExtractStore_4(b *testing.B) {
	benchmarkExtractOptions(b, true, WithExtractorConcurrency(4))
}

func BenchmarkExtractStore_8(b *testing.B) {
	benchmarkExtractOptions(b, true, WithExtractorConcurrency(8))
}

func BenchmarkExtractStore_16(b *testing.B) {
	benchmarkExtractOptions(b, true, WithExtractorConcurrency(16))
}

func BenchmarkExtractStandardFlate_1(b *testing.B) {
	benchmarkExtractOptions(b, true, WithExtractorConcurrency(1))
}

func BenchmarkExtractStandardFlate_2(b *testing.B) {
	benchmarkExtractOptions(b, true, WithExtractorConcurrency(2))
}

func BenchmarkExtractStandardFlate_4(b *testing.B) {
	benchmarkExtractOptions(b, true, WithExtractorConcurrency(4))
}

func BenchmarkExtractStandardFlate_8(b *testing.B) {
	benchmarkExtractOptions(b, true, WithExtractorConcurrency(8))
}

func BenchmarkExtractStandardFlate_16(b *testing.B) {
	benchmarkExtractOptions(b, true, WithExtractorConcurrency(16))
}

func BenchmarkExtractNonStandardFlate_1(b *testing.B) {
	benchmarkExtractOptions(b, false, WithExtractorConcurrency(1))
}

func BenchmarkExtractNonStandardFlate_2(b *testing.B) {
	benchmarkExtractOptions(b, false, WithExtractorConcurrency(2))
}

func BenchmarkExtractNonStandardFlate_4(b *testing.B) {
	benchmarkExtractOptions(b, false, WithExtractorConcurrency(4))
}

func BenchmarkExtractNonStandardFlate_8(b *testing.B) {
	benchmarkExtractOptions(b, false, WithExtractorConcurrency(8))
}

func BenchmarkExtractNonStandardFlate_16(b *testing.B) {
	benchmarkExtractOptions(b, false, WithExtractorConcurrency(16))
}
