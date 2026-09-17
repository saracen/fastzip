//go:build !windows

package fastzip

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/klauspost/compress/zip"
	"github.com/stretchr/testify/require"
)

func TestArchiveStableFileOrderSkipsIrregularEntries(t *testing.T) {
	// Irregular entries (here a fifo) are skipped without being given a write
	// index. If they were, the serializer would wait forever for a turn that
	// nobody takes. The fifo sorts between two files to make the gap real.
	testFiles := map[string]testFile{}
	for i := 0; i < 8; i++ {
		testFiles[fmt.Sprintf("file%d", i)] = testFile{mode: 0666, contents: strings.Repeat("data", 1024*(i+1))}
	}
	files, dir := testCreateFiles(t, testFiles)
	defer os.RemoveAll(dir)

	fifo := filepath.Join(dir, "file3.fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0666))
	fi, err := os.Lstat(fifo)
	require.NoError(t, err)
	require.NotZero(t, fi.Mode()&os.ModeNamedPipe)
	files[fifo] = fi

	f, err := os.CreateTemp("", "fastzip-irregular")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	a, err := NewArchiver(f, dir, WithArchiverConcurrency(4), WithStableFileOrder())
	require.NoError(t, err)
	require.NoError(t, a.Archive(context.Background(), files))
	require.NoError(t, a.Close())

	// The files plus the root directory, which testCreateFiles includes.
	_, entries := a.Written()
	require.Equal(t, int64(len(testFiles)+1), entries)

	st, err := f.Stat()
	require.NoError(t, err)
	zr, err := zip.NewReader(f, st.Size())
	require.NoError(t, err)
	names := make([]string, 0, len(zr.File))
	for _, zf := range zr.File {
		names = append(names, zf.Name)
	}
	require.Equal(t, []string{"./", "file0", "file1", "file2", "file3", "file4", "file5", "file6", "file7"}, names)
}
