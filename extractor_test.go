package fastzip

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zip"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testExtract(t *testing.T, filename string, files map[string]testFile) map[string]os.FileInfo {
	dir := t.TempDir()
	e, err := NewExtractor(filename, dir)
	require.NoError(t, err)
	defer e.Close()

	for _, f := range e.Files() {
		assert.Equal(t, filepath.ToSlash(f.Name), f.Name, "zip file path separator not /")
	}

	require.NoError(t, e.Extract(context.Background()))

	result := make(map[string]os.FileInfo)
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

		result[pathname] = fi

		mode := files[rel].mode
		assert.Equal(t, mode.Perm(), fi.Mode().Perm(), "file %v perm not equal", rel)
		assert.Equal(t, mode.IsDir(), fi.IsDir(), "file %v is_dir not equal", rel)
		assert.Equal(t, mode&os.ModeSymlink, fi.Mode()&os.ModeSymlink, "file %v mode symlink not equal", rel)

		if fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		contents, err := os.ReadFile(pathname)
		require.NoError(t, err)
		assert.Equal(t, string(files[rel].contents), string(contents))

		return nil
	})
	require.NoError(t, err)

	return result
}

func TestExtractCancelContext(t *testing.T) {
	twoMB := strings.Repeat("1", 2*1024*1024)
	testFiles := map[string]testFile{}
	for i := 0; i < 100; i++ {
		testFiles[fmt.Sprintf("file_%d", i)] = testFile{mode: 0666, contents: twoMB}
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

func TestExtractorDetectSymlinkTraversal(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "vuln.zip")
	f, err := os.Create(archivePath)
	require.NoError(t, err)
	zw := zip.NewWriter(f)

	// create symlink
	symlink := &zip.FileHeader{Name: "root/inner"}
	symlink.SetMode(os.ModeSymlink)
	w, err := zw.CreateHeader(symlink)
	require.NoError(t, err)

	_, err = w.Write([]byte("../"))
	require.NoError(t, err)

	// create file within symlink
	_, err = zw.Create("root/inner/vuln")
	require.NoError(t, err)

	zw.Close()
	f.Close()

	e, err := NewExtractor(archivePath, dir)
	require.NoError(t, err)
	defer e.Close()

	require.Error(t, e.Extract(context.Background()))
}

func aopts(options ...ArchiverOption) []ArchiverOption {
	return options
}

func benchmarkExtractOptions(b *testing.B, stdDeflate bool, ao []ArchiverOption, eo ...ExtractorOption) {
	files := make(map[string]os.FileInfo)
	filepath.Walk(*archiveDir, func(filename string, fi os.FileInfo, err error) error {
		files[filename] = fi
		return nil
	})

	dir := b.TempDir()
	archiveName := filepath.Join(dir, "fastzip-benchmark-extract.zip")
	f, err := os.Create(archiveName)
	require.NoError(b, err)
	defer os.Remove(f.Name())

	ao = append(ao, WithStageDirectory(dir))
	a, err := NewArchiver(f, *archiveDir, ao...)
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
		e, err := NewExtractor(archiveName, dir, eo...)
		if stdDeflate {
			e.RegisterDecompressor(zip.Deflate, StdFlateDecompressor())
		}
		require.NoError(b, err)
		require.NoError(b, e.Extract(context.Background()))
	}
}

func TestExtractSymlinkDirectoryTimestamps(t *testing.T) {
	// Create a specific past time for testing (different from fixedModTime used by testCreateFiles)
	pastTime := time.Date(2019, 3, 15, 14, 30, 0, 0, time.UTC)

	testFiles := map[string]testFile{
		"target_file":          {mode: 0644, contents: "target content"},
		"parent_dir":           {mode: 0755 | os.ModeDir},
		"parent_dir/symlink":   {mode: 0777 | os.ModeSymlink, contents: "../target_file"},
		"another_dir":          {mode: 0755 | os.ModeDir},
		"another_dir/file.txt": {mode: 0644, contents: "regular file"},
	}

	// Create files using the existing test helper
	files, dir := testCreateFiles(t, testFiles)
	defer os.RemoveAll(dir)

	// Override timestamps on directories to our specific past time
	// (testCreateFiles sets all timestamps to fixedModTime = 2020-02-01)
	require.NoError(t, os.Chtimes(filepath.Join(dir, "parent_dir"), pastTime, pastTime))
	require.NoError(t, os.Chtimes(filepath.Join(dir, "another_dir"), pastTime, pastTime))

	// Update the FileInfo in the map to reflect the new timestamps
	parentDirPath := filepath.Join(dir, "parent_dir")
	anotherDirPath := filepath.Join(dir, "another_dir")

	parentDirInfo, err := os.Lstat(parentDirPath)
	require.NoError(t, err)
	anotherDirInfo, err := os.Lstat(anotherDirPath)
	require.NoError(t, err)

	// Update the FileInfo entries using the exact absolute paths
	files[parentDirPath] = parentDirInfo
	files[anotherDirPath] = anotherDirInfo

	testCreateArchive(t, dir, files, func(filename, chroot string) {
		// Extract to a new directory
		extractDir := t.TempDir()
		e, err := NewExtractor(filename, extractDir)
		require.NoError(t, err)
		defer e.Close()

		// Wait a bit to ensure current time is different from pastTime
		time.Sleep(50 * time.Millisecond)
		currentTime := time.Now()

		require.NoError(t, e.Extract(context.Background()))

		// Check that directory containing symlink preserved its timestamp
		parentDirPath := filepath.Join(extractDir, "parent_dir")
		parentDirInfo, err := os.Lstat(parentDirPath)
		require.NoError(t, err)

		// The directory timestamp should match the original archived time,
		// not the current extraction time
		actualTime := parentDirInfo.ModTime().UTC().Truncate(time.Second)
		expectedTime := pastTime.Truncate(time.Second)
		extractTime := currentTime.UTC().Truncate(time.Second)

		assert.Equal(t, expectedTime, actualTime,
			"Directory containing symlink should preserve original timestamp (%v), not extraction time (%v)",
			expectedTime, extractTime)

		// Also check that regular directory (without symlink) preserves timestamp
		anotherDirPath := filepath.Join(extractDir, "another_dir")
		anotherDirInfo, err := os.Lstat(anotherDirPath)
		require.NoError(t, err)

		actualTime2 := anotherDirInfo.ModTime().UTC().Truncate(time.Second)
		assert.Equal(t, expectedTime, actualTime2,
			"Regular directory should also preserve original timestamp")

		// Verify symlink itself exists and points to correct target
		symlinkPath := filepath.Join(extractDir, "parent_dir", "symlink")
		symlinkInfo, err := os.Lstat(symlinkPath)
		require.NoError(t, err)

		// Verify it's actually a symlink
		assert.True(t, symlinkInfo.Mode()&os.ModeSymlink != 0,
			"Should be a symlink")

		// Verify symlink points to correct target
		target, err := os.Readlink(symlinkPath)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join("..", "target_file"), target,
			"Symlink should point to correct target")

		// The key assertion: ensure directories containing symlinks
		// don't have their timestamps updated during symlink creation
		timeDifference := actualTime.Sub(extractTime).Abs()
		assert.Greater(t, timeDifference, time.Duration(30*time.Second),
			"Directory timestamp should be significantly different from extraction time, "+
				"indicating it was preserved from the archive rather than updated during extraction")
	})
}

func BenchmarkExtractStore_1(b *testing.B) {
	benchmarkExtractOptions(b, true, aopts(WithArchiverMethod(zip.Store)), WithExtractorConcurrency(1))
}

func BenchmarkExtractStore_2(b *testing.B) {
	benchmarkExtractOptions(b, true, aopts(WithArchiverMethod(zip.Store)), WithExtractorConcurrency(2))
}

func BenchmarkExtractStore_4(b *testing.B) {
	benchmarkExtractOptions(b, true, aopts(WithArchiverMethod(zip.Store)), WithExtractorConcurrency(4))
}

func BenchmarkExtractStore_8(b *testing.B) {
	benchmarkExtractOptions(b, true, aopts(WithArchiverMethod(zip.Store)), WithExtractorConcurrency(8))
}

func BenchmarkExtractStore_16(b *testing.B) {
	benchmarkExtractOptions(b, true, aopts(WithArchiverMethod(zip.Store)), WithExtractorConcurrency(16))
}

func BenchmarkExtractStandardFlate_1(b *testing.B) {
	benchmarkExtractOptions(b, true, nil, WithExtractorConcurrency(1))
}

func BenchmarkExtractStandardFlate_2(b *testing.B) {
	benchmarkExtractOptions(b, true, nil, WithExtractorConcurrency(2))
}

func BenchmarkExtractStandardFlate_4(b *testing.B) {
	benchmarkExtractOptions(b, true, nil, WithExtractorConcurrency(4))
}

func BenchmarkExtractStandardFlate_8(b *testing.B) {
	benchmarkExtractOptions(b, true, nil, WithExtractorConcurrency(8))
}

func BenchmarkExtractStandardFlate_16(b *testing.B) {
	benchmarkExtractOptions(b, true, nil, WithExtractorConcurrency(16))
}

func BenchmarkExtractNonStandardFlate_1(b *testing.B) {
	benchmarkExtractOptions(b, false, nil, WithExtractorConcurrency(1))
}

func BenchmarkExtractNonStandardFlate_2(b *testing.B) {
	benchmarkExtractOptions(b, false, nil, WithExtractorConcurrency(2))
}

func BenchmarkExtractNonStandardFlate_4(b *testing.B) {
	benchmarkExtractOptions(b, false, nil, WithExtractorConcurrency(4))
}

func BenchmarkExtractNonStandardFlate_8(b *testing.B) {
	benchmarkExtractOptions(b, false, nil, WithExtractorConcurrency(8))
}

func BenchmarkExtractNonStandardFlate_16(b *testing.B) {
	benchmarkExtractOptions(b, false, nil, WithExtractorConcurrency(16))
}

func BenchmarkExtractZstd_1(b *testing.B) {
	benchmarkExtractOptions(b, false, aopts(WithArchiverMethod(zstd.ZipMethodWinZip)), WithExtractorConcurrency(1))
}

func BenchmarkExtractZstd_2(b *testing.B) {
	benchmarkExtractOptions(b, false, aopts(WithArchiverMethod(zstd.ZipMethodWinZip)), WithExtractorConcurrency(2))
}

func BenchmarkExtractZstd_4(b *testing.B) {
	benchmarkExtractOptions(b, false, aopts(WithArchiverMethod(zstd.ZipMethodWinZip)), WithExtractorConcurrency(4))
}

func BenchmarkExtractZstd_8(b *testing.B) {
	benchmarkExtractOptions(b, false, aopts(WithArchiverMethod(zstd.ZipMethodWinZip)), WithExtractorConcurrency(8))
}

func BenchmarkExtractZstd_16(b *testing.B) {
	benchmarkExtractOptions(b, false, aopts(WithArchiverMethod(zstd.ZipMethodWinZip)), WithExtractorConcurrency(16))
}
