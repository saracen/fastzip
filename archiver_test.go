package fastzip

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zip"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var fixedModTime = time.Date(2020, time.February, 1, 6, 0, 0, 0, time.UTC)

type testFile struct {
	mode     os.FileMode
	contents string
}

func testCreateFiles(t *testing.T, files map[string]testFile) (map[string]os.FileInfo, string) {
	dir := t.TempDir()

	filenames := make([]string, 0, len(files))
	for path := range files {
		filenames = append(filenames, path)
	}
	slices.Sort(filenames)

	var err error
	for _, path := range filenames {
		tf := files[path]
		path = filepath.Join(dir, path)

		switch {
		case tf.mode&os.ModeSymlink != 0 && tf.mode&os.ModeDir != 0:
			err = os.Symlink(tf.contents, path)

		case tf.mode&os.ModeDir != 0:
			err = os.Mkdir(path, tf.mode)

		case tf.mode&os.ModeSymlink != 0:
			err = os.Symlink(tf.contents, path)

		default:
			err = os.WriteFile(path, []byte(tf.contents), tf.mode)
		}
		require.NoError(t, err)
		require.NoError(t, lchmod(path, tf.mode))
		require.NoError(t, lchtimes(path, tf.mode, fixedModTime, fixedModTime))
	}

	archiveFiles := make(map[string]os.FileInfo)
	err = filepath.Walk(dir, func(pathname string, fi os.FileInfo, err error) error {
		archiveFiles[pathname] = fi
		return nil
	})
	require.NoError(t, err)

	return archiveFiles, dir
}

func testCreateArchive(t *testing.T, dir string, files map[string]os.FileInfo, fn func(filename, chroot string), opts ...ArchiverOption) {
	f, err := os.CreateTemp("", "fastzip-test")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	a, err := NewArchiver(f, dir, opts...)
	require.NoError(t, err)
	require.NoError(t, a.Archive(context.Background(), files))
	require.NoError(t, a.Close())

	_, entries := a.Written()
	require.EqualValues(t, len(files), entries)

	fn(f.Name(), dir)
}

func TestArchive(t *testing.T) {
	symMode := os.FileMode(0777)
	if runtime.GOOS == "windows" {
		symMode = 0666
	}

	testFiles := map[string]testFile{
		"foo":                 {mode: os.ModeDir | 0777},
		"foo/foo.go":          {mode: 0666},
		"bar":                 {mode: os.ModeDir | 0777},
		"bar/bar.go":          {mode: 0666},
		"bar/foo":             {mode: os.ModeDir | 0777},
		"bar/foo/bar":         {mode: os.ModeDir | 0777},
		"bar/foo/bar/foo":     {mode: os.ModeDir | 0777},
		"bar/foo/bar/foo/bar": {mode: 0666},
		"bar/symlink":         {mode: os.ModeSymlink | symMode, contents: "bar/foo/bar/foo"},
		"bar/symlink.go":      {mode: os.ModeSymlink | symMode, contents: "foo/foo.go"},
		"bar/compressible":    {mode: 0666, contents: "11111111111111111111111111111111111111111111111111"},
		"bar/uncompressible":  {mode: 0666, contents: "A3#bez&OqCusPr)d&D]Vot9Eo0z^5O*VZm3:sO3HptL.H-4cOv"},
		"empty_dir":           {mode: os.ModeDir | 0777},
		"large_file":          {mode: 0666, contents: strings.Repeat("abcdefzmkdldjsdfkjsdfsdfiqwpsdfa", 65536)},
	}

	tests := map[string][]ArchiverOption{
		"default options":    nil,
		"no buffer":          {WithArchiverBufferSize(0)},
		"with store":         {WithArchiverMethod(zip.Store)},
		"with concurrency 2": {WithArchiverConcurrency(2)},
	}

	for tn, opts := range tests {
		t.Run(tn, func(t *testing.T) {
			files, dir := testCreateFiles(t, testFiles)
			defer os.RemoveAll(dir)

			testCreateArchive(t, dir, files, func(filename, chroot string) {
				for pathname, fi := range testExtract(t, filename, testFiles) {
					if fi.IsDir() {
						continue
					}
					if runtime.GOOS == "windows" && fi.Mode()&os.ModeSymlink != 0 {
						continue
					}
					assert.Equal(t, fixedModTime.Unix(), fi.ModTime().Unix(), "file %v mod time not equal", pathname)
				}
			}, opts...)
		})
	}
}

func TestArchiveCancelContext(t *testing.T) {
	twoMB := strings.Repeat("1", 2*1024*1024)
	testFiles := map[string]testFile{}
	for i := 0; i < 100; i++ {
		testFiles[fmt.Sprintf("file_%d", i)] = testFile{mode: 0666, contents: twoMB}
	}

	files, dir := testCreateFiles(t, testFiles)
	defer os.RemoveAll(dir)

	f, err := os.CreateTemp("", "fastzip-test")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	a, err := NewArchiver(f, dir, WithArchiverConcurrency(1))
	a.RegisterCompressor(zip.Deflate, FlateCompressor(1))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer func() { done <- struct{}{} }()

		require.EqualError(t, a.Archive(ctx, files), "context canceled")
	}()

	defer func() {
		require.NoError(t, a.Close())
	}()

	for {
		select {
		case <-done:
			return

		default:
			// cancel as soon as any data is written
			if bytes, _ := a.Written(); bytes > 0 {
				cancel()
			}
		}
	}
}

func TestArchiveWithCompressor(t *testing.T) {
	testFiles := map[string]testFile{
		"foo.go": {mode: 0666},
		"bar.go": {mode: 0666},
	}

	files, dir := testCreateFiles(t, testFiles)
	defer os.RemoveAll(dir)

	f, err := os.CreateTemp("", "fastzip-test")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	a, err := NewArchiver(f, dir)
	a.RegisterCompressor(zip.Deflate, FlateCompressor(1))
	require.NoError(t, err)
	require.NoError(t, a.Archive(context.Background(), files))
	require.NoError(t, a.Close())

	bytes, entries := a.Written()
	require.EqualValues(t, 0, bytes)
	require.EqualValues(t, 3, entries)

	testExtract(t, f.Name(), testFiles)
}

func TestArchiveWithMethod(t *testing.T) {
	testFiles := map[string]testFile{
		"foo.go": {mode: 0666},
		"bar.go": {mode: 0666},
	}

	files, dir := testCreateFiles(t, testFiles)
	defer os.RemoveAll(dir)

	f, err := os.CreateTemp("", "fastzip-test")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	a, err := NewArchiver(f, dir, WithArchiverMethod(zip.Store))
	require.NoError(t, err)
	require.NoError(t, a.Archive(context.Background(), files))
	require.NoError(t, a.Close())

	bytes, entries := a.Written()
	require.EqualValues(t, 0, bytes)
	require.EqualValues(t, 3, entries)

	testExtract(t, f.Name(), testFiles)
}

func TestArchiveWithStageDirectory(t *testing.T) {
	testFiles := map[string]testFile{
		"foo.go": {mode: 0666},
		"bar.go": {mode: 0666},
	}

	files, chroot := testCreateFiles(t, testFiles)
	defer os.RemoveAll(chroot)

	dir := t.TempDir()
	f, err := os.CreateTemp("", "fastzip-test")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	a, err := NewArchiver(f, chroot, WithStageDirectory(dir))
	require.NoError(t, err)
	require.NoError(t, a.Archive(context.Background(), files))
	require.NoError(t, a.Close())

	bytes, entries := a.Written()
	require.EqualValues(t, 0, bytes)
	require.EqualValues(t, 3, entries)

	stageFiles, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Zero(t, len(stageFiles))

	testExtract(t, f.Name(), testFiles)
}

func TestArchiveWithConcurrency(t *testing.T) {
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

	for _, test := range concurrencyTests {
		func() {
			f, err := os.CreateTemp("", "fastzip-test")
			require.NoError(t, err)
			defer os.Remove(f.Name())
			defer f.Close()

			a, err := NewArchiver(f, dir, WithArchiverConcurrency(test.concurrency))
			if !test.pass {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NoError(t, a.Archive(context.Background(), files))
			require.NoError(t, a.Close())

			bytes, entries := a.Written()
			require.EqualValues(t, 0, bytes)
			require.EqualValues(t, 3, entries)

			testExtract(t, f.Name(), testFiles)
		}()
	}
}

func TestArchiveWithBufferSize(t *testing.T) {
	testFiles := map[string]testFile{
		"foobar.go":      {mode: 0666},
		"compressible":   {mode: 0666, contents: "11111111111111111111111111111111111111111111111111"},
		"uncompressible": {mode: 0666, contents: "A3#bez&OqCusPr)d&D]Vot9Eo0z^5O*VZm3:sO3HptL.H-4cOv"},
		"empty_dir":      {mode: os.ModeDir | 0777},
		"large_file":     {mode: 0666, contents: strings.Repeat("abcdefzmkdldjsdfkjsdfsdfiqwpsdfa", 65536)},
	}

	tests := []struct {
		buffersize int
		zero       bool
	}{
		{-100, false},
		{-2, false},
		{-1, false},
		{0, true},
		{32 * 1024, true},
		{64 * 1024, true},
	}

	files, dir := testCreateFiles(t, testFiles)
	defer os.RemoveAll(dir)

	for _, test := range tests {
		func() {
			f, err := os.CreateTemp("", "fastzip-test")
			require.NoError(t, err)
			defer os.Remove(f.Name())
			defer f.Close()

			a, err := NewArchiver(f, dir, WithArchiverBufferSize(test.buffersize))
			require.NoError(t, err)
			require.NoError(t, a.Archive(context.Background(), files))
			require.NoError(t, a.Close())

			if !test.zero {
				require.Equal(t, 0, a.options.bufferSize)
			} else {
				require.Equal(t, test.buffersize, a.options.bufferSize)
			}

			_, entries := a.Written()
			require.EqualValues(t, 6, entries)

			testExtract(t, f.Name(), testFiles)
		}()
	}
}

func TestArchiveChroot(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "archive.zip"))
	require.NoError(t, err)
	defer f.Close()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "chroot"), 0777))

	a, err := NewArchiver(f, filepath.Join(dir, "chroot"))
	require.NoError(t, err)

	tests := []struct {
		paths []string
		good  bool
	}{
		{[]string{"chroot/good"}, true},
		{[]string{"chroot/good", "bad"}, false},
		{[]string{"bad"}, false},
		{[]string{"chroot/../bad"}, false},
		{[]string{"chroot/../chroot/good"}, true},
	}

	for _, test := range tests {
		files := make(map[string]os.FileInfo)

		for _, filename := range test.paths {
			w, err := os.Create(filepath.Join(dir, filename))
			require.NoError(t, err)
			stat, err := w.Stat()
			require.NoError(t, err)
			require.NoError(t, w.Close())

			files[w.Name()] = stat
		}

		err = a.Archive(context.Background(), files)
		if test.good {
			assert.NoError(t, err)
		} else {
			assert.Error(t, err)
		}
	}
}

func TestArchiveWithOffset(t *testing.T) {
	testFiles := map[string]testFile{
		"foo.go": {mode: 0666},
		"bar.go": {mode: 0666},
	}

	files, dir := testCreateFiles(t, testFiles)
	defer os.RemoveAll(dir)

	f, err := os.CreateTemp("", "fastzip-test")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	f.Seek(1000, io.SeekStart)

	a, err := NewArchiver(f, dir, WithArchiverOffset(1000))
	require.NoError(t, err)
	require.NoError(t, a.Archive(context.Background(), files))
	require.NoError(t, a.Close())

	bytes, entries := a.Written()
	require.EqualValues(t, 0, bytes)
	require.EqualValues(t, 3, entries)

	testExtract(t, f.Name(), testFiles)
}

var archiveDir = flag.String("archivedir", runtime.GOROOT(), "The directory to use for archive benchmarks")

func benchmarkArchiveOptions(b *testing.B, stdDeflate bool, options ...ArchiverOption) {
	files := make(map[string]os.FileInfo)
	size := int64(0)
	filepath.Walk(*archiveDir, func(filename string, fi os.FileInfo, err error) error {
		files[filename] = fi
		size += fi.Size()
		return nil
	})

	dir := b.TempDir()

	options = append(options, WithStageDirectory(dir))

	b.ReportAllocs()
	b.SetBytes(size)
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		f, err := os.Create(filepath.Join(dir, "fastzip-benchmark.zip"))
		require.NoError(b, err)

		a, err := NewArchiver(f, *archiveDir, options...)
		if stdDeflate {
			a.RegisterCompressor(zip.Deflate, StdFlateCompressor(-1))
		} else {
			a.RegisterCompressor(zip.Deflate, FlateCompressor(-1))
		}
		require.NoError(b, err)

		err = a.Archive(context.Background(), files)
		require.NoError(b, err)

		require.NoError(b, a.Close())
		require.NoError(b, f.Close())
		require.NoError(b, os.Remove(f.Name()))
	}
}

func BenchmarkArchiveStore_1(b *testing.B) {
	benchmarkArchiveOptions(b, true, WithArchiverConcurrency(1), WithArchiverMethod(zip.Store))
}

func BenchmarkArchiveStandardFlate_1(b *testing.B) {
	benchmarkArchiveOptions(b, true, WithArchiverConcurrency(1))
}

func BenchmarkArchiveStandardFlate_2(b *testing.B) {
	benchmarkArchiveOptions(b, true, WithArchiverConcurrency(2))
}

func BenchmarkArchiveStandardFlate_4(b *testing.B) {
	benchmarkArchiveOptions(b, true, WithArchiverConcurrency(4))
}

func BenchmarkArchiveStandardFlate_8(b *testing.B) {
	benchmarkArchiveOptions(b, true, WithArchiverConcurrency(8))
}

func BenchmarkArchiveStandardFlate_16(b *testing.B) {
	benchmarkArchiveOptions(b, true, WithArchiverConcurrency(16))
}

func BenchmarkArchiveNonStandardFlate_1(b *testing.B) {
	benchmarkArchiveOptions(b, false, WithArchiverConcurrency(1))
}

func BenchmarkArchiveNonStandardFlate_2(b *testing.B) {
	benchmarkArchiveOptions(b, false, WithArchiverConcurrency(2))
}

func BenchmarkArchiveNonStandardFlate_4(b *testing.B) {
	benchmarkArchiveOptions(b, false, WithArchiverConcurrency(4))
}

func BenchmarkArchiveNonStandardFlate_8(b *testing.B) {
	benchmarkArchiveOptions(b, false, WithArchiverConcurrency(8))
}

func BenchmarkArchiveNonStandardFlate_16(b *testing.B) {
	benchmarkArchiveOptions(b, false, WithArchiverConcurrency(16), WithArchiverMethod(zstd.ZipMethodWinZip))
}

func BenchmarkArchiveZstd_1(b *testing.B) {
	benchmarkArchiveOptions(b, true, WithArchiverConcurrency(1), WithArchiverMethod(zstd.ZipMethodWinZip))
}

func BenchmarkArchiveZstd_2(b *testing.B) {
	benchmarkArchiveOptions(b, true, WithArchiverConcurrency(2), WithArchiverMethod(zstd.ZipMethodWinZip))
}

func BenchmarkArchiveZstd_4(b *testing.B) {
	benchmarkArchiveOptions(b, true, WithArchiverConcurrency(4), WithArchiverMethod(zstd.ZipMethodWinZip))
}

func BenchmarkArchiveZstd_8(b *testing.B) {
	benchmarkArchiveOptions(b, true, WithArchiverConcurrency(8), WithArchiverMethod(zstd.ZipMethodWinZip))
}

func BenchmarkArchiveZstd_16(b *testing.B) {
	benchmarkArchiveOptions(b, true, WithArchiverConcurrency(16), WithArchiverMethod(zstd.ZipMethodWinZip))
}
