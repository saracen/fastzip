package fastzip

import (
	"archive/zip"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

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
			e, err := NewExtractor(filename, dir, WithExtractionConcurrency(test.concurrency))
			if test.pass {
				assert.NoError(t, err)
				require.NoError(t, e.Close())
			} else {
				assert.Error(t, err)
			}
		}
	})
}
