package fastzip

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
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
	sort.Strings(filenames)

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
	f, err := ioutil.TempFile("", "fastzip-test")
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
		"default options":     nil,
		"no buffer":           {WithArchiverBufferSize(0)},
		"with store":          {WithArchiverMethod(zip.Store)},
		"with concurrency 2":  {WithArchiverConcurrency(2)},
		"with stable order":   {WithArchiverConcurrency(4), WithStableFileOrder()},
		"stable no buffer":    {WithArchiverConcurrency(4), WithArchiverBufferSize(0), WithStableFileOrder()},
		"stable order store":  {WithArchiverConcurrency(4), WithArchiverMethod(zip.Store), WithStableFileOrder()},
		"stable concurrency1": {WithArchiverConcurrency(1), WithStableFileOrder()},
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

func TestArchiveStableFileOrderIsDeterministic(t *testing.T) {
	symMode := os.FileMode(0777)
	if runtime.GOOS == "windows" {
		symMode = 0666
	}

	// A mix of many compressible, incompressible, empty, symlink and directory
	// entries, so that concurrent compression finishes in a different order
	// from one run to the next. Without stable ordering the resulting archive
	// bytes would vary; with it they must not.
	testFiles := map[string]testFile{
		"dir":        {mode: os.ModeDir | 0777},
		"empty_dir":  {mode: os.ModeDir | 0777},
		"symlink":    {mode: os.ModeSymlink | symMode, contents: "dir/file00"},
		"incompress": {mode: 0666, contents: "A3#bez&OqCusPr)d&D]Vot9Eo0z^5O*VZm3:sO3HptL.H-4cOv"},
		"empty_file": {mode: 0666},
	}
	for i := 0; i < 40; i++ {
		name := fmt.Sprintf("dir/file%02d", i)
		testFiles[name] = testFile{
			mode:     0666,
			contents: strings.Repeat(fmt.Sprintf("content-%02d-", i), 1024*(i+1)),
		}
	}
	// Directories interleaved with files in sorted order. Their writes are
	// queued behind in-flight files rather than waited for, so this exercises
	// that queued entries still land in the right place.
	for i := 0; i < 20; i++ {
		testFiles[fmt.Sprintf("tree/d%02d", i)] = testFile{mode: os.ModeDir | 0777}
		testFiles[fmt.Sprintf("tree/d%02d/f", i)] = testFile{
			mode:     0666,
			contents: strings.Repeat(fmt.Sprintf("tree-%02d-", i), 512*(20-i)),
		}
	}
	testFiles["tree"] = testFile{mode: os.ModeDir | 0777}
	// Incompressible files larger than the small budget used below, so that
	// with that budget they must wait for their turn holding their slot.
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 8; i++ {
		b := make([]byte, 32*1024)
		rng.Read(b)
		testFiles[fmt.Sprintf("random%d", i)] = testFile{mode: 0666, contents: string(b)}
	}

	files, dir := testCreateFiles(t, testFiles)
	defer os.RemoveAll(dir)

	wantNames := make([]string, 0, len(files))
	for name := range files {
		rel, err := filepath.Rel(dir, name)
		require.NoError(t, err)
		rel = filepath.ToSlash(rel)
		if files[name].IsDir() {
			rel += "/"
		}
		wantNames = append(wantNames, rel)
	}
	sort.Strings(wantNames)

	archiveSum := func(t *testing.T, opts ...ArchiverOption) [sha256.Size]byte {
		f, err := ioutil.TempFile("", "fastzip-stable")
		require.NoError(t, err)
		defer os.Remove(f.Name())
		defer f.Close()

		a, err := NewArchiver(f, dir, opts...)
		require.NoError(t, err)
		require.NoError(t, a.Archive(context.Background(), files))
		require.NoError(t, a.Close())

		b, err := os.ReadFile(f.Name())
		require.NoError(t, err)

		zr, err := zip.NewReader(f, int64(len(b)))
		require.NoError(t, err)
		gotNames := make([]string, 0, len(zr.File))
		for _, zf := range zr.File {
			gotNames = append(gotNames, zf.Name)
		}
		require.Equal(t, wantNames, gotNames, "entries not written in sorted order")

		return sha256.Sum256(b)
	}

	opts := []ArchiverOption{WithArchiverConcurrency(8), WithStableFileOrder()}
	want := archiveSum(t, opts...)
	for i := 0; i < 25; i++ {
		require.Equal(t, want, archiveSum(t, opts...), "archive checksum changed on run %d", i)
	}

	// The memory budget only decides whether a finished entry waits holding
	// its slot or is copied out and queued. The bytes must not depend on which
	// path was taken: a zero buffer size leaves no budget at all, and a small
	// one lets the small files queue while the 32KB random ones must wait.
	for name, size := range map[string]int{"no budget": 0, "small budget": 1024} {
		withBudget := append([]ArchiverOption{WithArchiverBufferSize(size)}, opts...)
		for i := 0; i < 5; i++ {
			require.Equal(t, want, archiveSum(t, withBudget...), "%s: archive checksum differs on run %d", name, i)
		}
	}
}

func TestArchiveStableFileOrderConcurrentArchiveCalls(t *testing.T) {
	// Archive may be called concurrently on one Archiver. Ordering state is
	// per call, so two calls must not release or reorder each other's entries.
	testFiles := map[string]testFile{}
	for i := 0; i < 40; i++ {
		testFiles[fmt.Sprintf("file%02d", i)] = testFile{mode: 0666, contents: strings.Repeat(fmt.Sprintf("%d", i), 4096)}
	}
	files, dir := testCreateFiles(t, testFiles)
	defer os.RemoveAll(dir)

	halves := []map[string]os.FileInfo{{}, {}}
	i := 0
	for name, fi := range files {
		halves[i%2][name] = fi
		i++
	}

	f, err := ioutil.TempFile("", "fastzip-concurrent")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	a, err := NewArchiver(f, dir, WithArchiverConcurrency(8), WithStableFileOrder())
	require.NoError(t, err)

	var wg sync.WaitGroup
	errs := make([]error, len(halves))
	for i, half := range halves {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = a.Archive(context.Background(), half)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	require.NoError(t, a.Close())

	_, entries := a.Written()
	require.Equal(t, int64(len(files)), entries)

	st, err := f.Stat()
	require.NoError(t, err)
	zr, err := zip.NewReader(f, st.Size())
	require.NoError(t, err)
	require.Len(t, zr.File, len(files))
}

func TestArchiveFileGrownAfterStat(t *testing.T) {
	// A file that grew between enumeration and archiving must be archived
	// whole, with sizes that match its data. Store entries queued in memory
	// under stable ordering are copied using the size recorded at stat time,
	// and raw Deflate entries write their sizes into the headers themselves.
	for name, opts := range map[string][]ArchiverOption{
		"store":              {WithArchiverConcurrency(4), WithArchiverMethod(zip.Store)},
		"store stable order": {WithArchiverConcurrency(4), WithArchiverMethod(zip.Store), WithStableFileOrder()},
		"deflate":            {WithArchiverConcurrency(4)},
		"deflate stable":     {WithArchiverConcurrency(4), WithStableFileOrder()},
	} {
		t.Run(name, func(t *testing.T) {
			testFiles := map[string]testFile{
				"0_slow": {mode: 0666, contents: strings.Repeat("slow ", 200000)},
			}
			for i := 1; i < 8; i++ {
				testFiles[fmt.Sprintf("%d_file", i)] = testFile{mode: 0666, contents: strings.Repeat("short ", 20000)}
			}

			files, dir := testCreateFiles(t, testFiles)
			defer os.RemoveAll(dir)

			// Highly compressible, so the grown data still deflates to less
			// than the stat-time size and takes the raw (pre-compressed) path
			// rather than falling back to Store.
			grownContents := strings.Repeat("short but then it grew ", 20000)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "4_file"), []byte(grownContents), 0666))

			f, err := ioutil.TempFile("", "fastzip-grown")
			require.NoError(t, err)
			defer os.Remove(f.Name())
			defer f.Close()

			a, err := NewArchiver(f, dir, opts...)
			require.NoError(t, err)
			require.NoError(t, a.Archive(context.Background(), files))
			require.NoError(t, a.Close())

			st, err := f.Stat()
			require.NoError(t, err)
			zr, err := zip.NewReader(f, st.Size())
			require.NoError(t, err)
			for _, zf := range zr.File {
				if zf.Name != "4_file" {
					continue
				}
				require.Equal(t, uint64(len(grownContents)), zf.UncompressedSize64)
				rc, err := zf.Open()
				require.NoError(t, err)
				got, err := io.ReadAll(rc)
				require.NoError(t, err)
				require.NoError(t, rc.Close())
				require.Equal(t, grownContents, string(got))
				return
			}
			t.Fatal("4_file not found in archive")
		})
	}
}

func TestArchiveStableFileOrderFileErrorDoesNotDeadlock(t *testing.T) {
	// A file that fails to open mid-archive must not strand later entries that
	// are waiting for their write turn. The failing entry has a low index, so
	// with stable ordering the higher-index entries depend on it.
	//
	// The file count is kept at or below the concurrency, so the filepool never
	// makes the dispatch loop block on Get. The loop finishes and reaches its
	// final wg.Wait before the goroutines run, which is the case where the
	// loop's own ctx cancellation check can no longer rescue a stranded turn.
	//
	// With a zero buffer size there is no memory budget, so the later files
	// really block in the serializer waiting for entry 0; that is the case the
	// goroutine-side abort exists for. With the default budget they are queued
	// in memory instead and must be dropped, not written, when entry 0 fails.
	for name, opts := range map[string][]ArchiverOption{
		"waiting for turn": {WithArchiverBufferSize(0)},
		"queued in memory": nil,
	} {
		t.Run(name, func(t *testing.T) {
			testFiles := map[string]testFile{
				"0_missing": {mode: 0666, contents: "gone"},
			}
			for i := 1; i < 8; i++ {
				testFiles[fmt.Sprintf("%d_file", i)] = testFile{
					mode:     0666,
					contents: strings.Repeat("x", 4096),
				}
			}

			files, dir := testCreateFiles(t, testFiles)
			defer os.RemoveAll(dir)

			// Remove the lowest-sorted file from disk while leaving it in the
			// map, so os.Open fails for it during archiving.
			require.NoError(t, os.Remove(filepath.Join(dir, "0_missing")))

			f, err := ioutil.TempFile("", "fastzip-deadlock")
			require.NoError(t, err)
			defer os.Remove(f.Name())
			defer f.Close()

			opts := append([]ArchiverOption{WithArchiverConcurrency(16), WithStableFileOrder()}, opts...)
			a, err := NewArchiver(f, dir, opts...)
			require.NoError(t, err)

			done := make(chan error, 1)
			go func() { done <- a.Archive(context.Background(), files) }()

			select {
			case err := <-done:
				require.ErrorIs(t, err, os.ErrNotExist)
			case <-time.After(30 * time.Second):
				t.Fatal("Archive deadlocked on a file error with stable file order")
			}

			// Only the root directory sorts ahead of the failed file, so it is
			// the one entry written. Written must not count the later entries,
			// which never reached the zip.
			bytes, entries := a.Written()
			require.Zero(t, bytes)
			require.Equal(t, int64(1), entries)
		})
	}
}

func TestArchiveCancelContext(t *testing.T) {
	for name, opts := range map[string][]ArchiverOption{
		"concurrency 1":              {WithArchiverConcurrency(1)},
		"stable order concurrency 4": {WithArchiverConcurrency(4), WithStableFileOrder()},
	} {
		t.Run(name, func(t *testing.T) {
			twoMB := strings.Repeat("1", 2*1024*1024)
			testFiles := map[string]testFile{}
			for i := 0; i < 100; i++ {
				testFiles[fmt.Sprintf("file_%d", i)] = testFile{mode: 0666, contents: twoMB}
			}

			files, dir := testCreateFiles(t, testFiles)
			defer os.RemoveAll(dir)

			f, err := ioutil.TempFile("", "fastzip-test")
			require.NoError(t, err)
			defer os.Remove(f.Name())
			defer f.Close()

			a, err := NewArchiver(f, dir, opts...)
			require.NoError(t, err)
			a.RegisterCompressor(zip.Deflate, FlateCompressor(1))

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			done := make(chan error, 1)
			go func() { done <- a.Archive(ctx, files) }()

			defer func() {
				require.NoError(t, a.Close())
			}()

			for {
				select {
				case err := <-done:
					require.EqualError(t, err, "context canceled")
					return

				case <-time.After(30 * time.Second):
					t.Fatal("Archive did not return after cancellation")

				default:
					// cancel as soon as any data is written
					if bytes, _ := a.Written(); bytes > 0 {
						cancel()
					}
				}
			}
		})
	}
}

func TestArchiveWithCompressor(t *testing.T) {
	testFiles := map[string]testFile{
		"foo.go": {mode: 0666},
		"bar.go": {mode: 0666},
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

	f, err := ioutil.TempFile("", "fastzip-test")
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
	f, err := ioutil.TempFile("", "fastzip-test")
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
			f, err := ioutil.TempFile("", "fastzip-test")
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

	f, err := ioutil.TempFile("", "fastzip-test")
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
