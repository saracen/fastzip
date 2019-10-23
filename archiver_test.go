package fastzip

import (
	"flag"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/saracen/fastzip/internal/zip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testFile struct {
	mode     os.FileMode
	contents string
}

func testCreateFiles(t *testing.T, files map[string]testFile) (map[string]os.FileInfo, string) {
	dir, err := ioutil.TempDir("", "fastzip-test")
	require.NoError(t, err)

	filenames := make([]string, 0, len(files))
	for path := range files {
		filenames = append(filenames, path)
	}
	sort.Strings(filenames)

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
			err = ioutil.WriteFile(path, []byte(tf.contents), tf.mode)
		}
		require.NoError(t, err)
		require.NoError(t, lchmod(path, tf.mode))
	}

	archiveFiles := make(map[string]os.FileInfo)
	err = filepath.Walk(dir, func(pathname string, fi os.FileInfo, err error) error {
		archiveFiles[pathname] = fi
		return nil
	})
	require.NoError(t, err)

	return archiveFiles, dir
}

func testCreateArchive(t *testing.T, dir string, files map[string]os.FileInfo, fn func(filename, chroot string)) {
	f, err := ioutil.TempFile("", "fastzip-test")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	a, err := NewArchiver(f, dir)
	require.NoError(t, err)
	require.NoError(t, a.Archive(files))
	require.NoError(t, a.Close())

	fn(f.Name(), dir)
}

func TestArchive(t *testing.T) {
	symMode := os.FileMode(0777)
	if runtime.GOOS == "windows" {
		symMode = 0666
	}

	testFiles := map[string]testFile{
		"foo":                 testFile{mode: os.ModeDir | 0777},
		"foo/foo.go":          testFile{mode: 0666},
		"bar":                 testFile{mode: os.ModeDir | 0777},
		"bar/bar.go":          testFile{mode: 0666},
		"bar/foo":             testFile{mode: os.ModeDir | 0777},
		"bar/foo/bar":         testFile{mode: os.ModeDir | 0777},
		"bar/foo/bar/foo":     testFile{mode: os.ModeDir | 0777},
		"bar/foo/bar/foo/bar": testFile{mode: 0666},
		"bar/symlink":         testFile{mode: os.ModeDir | os.ModeSymlink | symMode, contents: "bar/foo/bar/foo"},
		"bar/symlink.go":      testFile{mode: os.ModeSymlink | symMode, contents: "foo/foo.go"},
	}

	files, dir := testCreateFiles(t, testFiles)
	defer os.RemoveAll(dir)

	testCreateArchive(t, dir, files, func(filename, chroot string) {
		testExtract(t, filename, testFiles)
	})
}

func TestArchiveWithCompressor(t *testing.T) {
	testFiles := map[string]testFile{
		"foo.go": testFile{mode: 0666},
		"bar.go": testFile{mode: 0666},
	}

	files, dir := testCreateFiles(t, testFiles)
	defer os.RemoveAll(dir)

	f, err := ioutil.TempFile("", "fastzip-test")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	a, err := NewArchiver(f, dir)
	a.RegisterCompressor(zip.Deflate, FlateCompressor(1))
	require.NoError(t, err)
	require.NoError(t, a.Archive(files))
	require.NoError(t, a.Close())

	testExtract(t, f.Name(), testFiles)
}

func TestArchiveWithMethod(t *testing.T) {
	testFiles := map[string]testFile{
		"foo.go": testFile{mode: 0666},
		"bar.go": testFile{mode: 0666},
	}

	files, dir := testCreateFiles(t, testFiles)
	defer os.RemoveAll(dir)

	f, err := ioutil.TempFile("", "fastzip-test")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	a, err := NewArchiver(f, dir, WithArchiverMethod(zip.Store))
	require.NoError(t, err)
	require.NoError(t, a.Archive(files))
	require.NoError(t, a.Close())

	testExtract(t, f.Name(), testFiles)
}

func TestArchiveWithConcurrency(t *testing.T) {
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

	for _, test := range concurrencyTests {
		func() {
			f, err := ioutil.TempFile("", "fastzip-test")
			require.NoError(t, err)
			defer os.Remove(f.Name())
			defer f.Close()

			a, err := NewArchiver(f, dir, WithArchiverConcurrency(test.concurrency))
			if !test.pass {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NoError(t, a.Archive(files))
			require.NoError(t, a.Close())

			testExtract(t, f.Name(), testFiles)
		}()
	}
}

func TestArchiveChroot(t *testing.T) {
	dir, err := ioutil.TempDir("", "fastzip-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

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

		err = a.Archive(files)
		if test.good {
			assert.NoError(t, err)
		} else {
			assert.Error(t, err)
		}
	}
}

var archiveDir = flag.String("archivedir", runtime.GOROOT(), "The directory to use for archive benchmarks")

func benchmarkArchiveOptions(b *testing.B, stdDeflate bool, options ...ArchiverOption) {
	files := make(map[string]os.FileInfo)
	filepath.Walk(*archiveDir, func(filename string, fi os.FileInfo, err error) error {
		files[filename] = fi
		return nil
	})

	dir, err := ioutil.TempDir("", "fastzip-benchmark-archive")
	require.NoError(b, err)
	defer os.RemoveAll(dir)

	options = append(options, WithStageDirectoryMethod(dir))

	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		f, err := os.Create(filepath.Join(dir, "fastzip-benchmark.zip"))
		require.NoError(b, err)

		a, err := NewArchiver(f, *archiveDir, options...)
		if stdDeflate {
			a.RegisterCompressor(zip.Deflate, stdFlateCompressor(5))
		} else {
			a.RegisterCompressor(zip.Deflate, FlateCompressor(5))
		}
		require.NoError(b, err)

		err = a.Archive(files)
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
	benchmarkArchiveOptions(b, false, WithArchiverConcurrency(16))
}
