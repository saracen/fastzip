package filepool

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilePoolSizes(t *testing.T) {
	tests := []struct {
		size int
		err  error
	}{
		{-1, ErrPoolSizeLessThanZero},
		{0, ErrPoolSizeLessThanZero},
		{4, nil},
		{8, nil},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("size %d", tc.size), func(t *testing.T) {
			dir := t.TempDir()

			fp, err := New(dir, tc.size, 0)
			require.Equal(t, tc.err, err)
			if tc.err != nil {
				return
			}

			// writing should produce the temporary file
			for i := 0; i < tc.size; i++ {
				f := fp.Get()
				_, err = f.Write([]byte("foobar"))
				assert.NoError(t, err)
				fp.Put(f)

				_, err = os.Lstat(filepath.Join(dir, fmt.Sprintf("fastzip_%02d", i)))
				assert.NoError(t, err, fmt.Sprintf("fastzip_%02d should exist", i))
			}

			// closing should cleanup temporary files
			assert.NoError(t, fp.Close())
			for i := 0; i < tc.size; i++ {
				_, err = os.Lstat(filepath.Join(dir, fmt.Sprintf("fastzip_%02d", i)))
				assert.Error(t, err, fmt.Sprintf("fastzip_%02d shouldn't exist", i))
			}
		})
	}
}

func TestFilePoolReset(t *testing.T) {
	dir := t.TempDir()

	fp, err := New(dir, 16, 0)
	require.NoError(t, err)
	for i := range fp.files {
		file := fp.Get()
		_, err = file.Write(bytes.Repeat([]byte("0"), i))
		assert.NoError(t, err)

		b, err := io.ReadAll(file)
		assert.NoError(t, err)
		assert.Len(t, b, i)
		assert.Equal(t, uint64(i), file.Written())

		_, err = file.Hasher().Write([]byte("hello"))
		assert.NoError(t, err)
		assert.Equal(t, uint32(0x3610a686), file.Checksum())

		fp.Put(file)
	}

	for range fp.files {
		file := fp.Get()

		b, err := io.ReadAll(file)
		assert.NoError(t, err)
		assert.Len(t, b, 0)
		assert.Equal(t, uint64(0), file.Written())
		assert.Equal(t, uint32(0), file.Checksum())

		fp.Put(file)
	}

	assert.NoError(t, fp.Close())
}

func TestFilePoolCloseError(t *testing.T) {
	dir := t.TempDir()

	fp, err := New(dir, 16, 0)
	require.NoError(t, err)

	for _, file := range fp.files {
		f := fp.Get()
		_, err := f.Write([]byte("foobar"))
		assert.NoError(t, err)
		fp.Put(f)

		require.NoError(t, file.f.Close())
	}

	err = fp.Close()
	require.Error(t, err, "expected already closed error")
	assert.Contains(t, err.Error(), "file already closed\n")
	count := 0
	for {
		count++
		if err = errors.Unwrap(err); err == nil {
			break
		}
	}
	assert.Equal(t, 16, count)
}

func TestFilePoolNoErrorOnAlreadyDeleted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on windows (cannot delete in-use file)")
	}

	dir := t.TempDir()
	fp, err := New(dir, 16, 0)
	require.NoError(t, err)

	for range fp.files {
		f := fp.Get()
		_, err := f.Write([]byte("foobar"))
		assert.NoError(t, err)
		fp.Put(f)
	}

	err = os.RemoveAll(dir)
	require.NoError(t, err)

	assert.NoError(t, fp.Close())
}

func TestFilePoolFileBuffer(t *testing.T) {
	dir := t.TempDir()

	tests := map[string]struct {
		data       []byte
		fileExists bool
	}{
		"below buffer length": {
			data:       []byte("123456789"),
			fileExists: false,
		},
		"equal to buffer length": {
			data:       []byte("1234567890"),
			fileExists: false,
		},
		"above buffer length": {
			data:       []byte("1234567890x"),
			fileExists: true,
		},
	}

	for tn, tc := range tests {
		t.Run(tn, func(t *testing.T) {
			fp, err := New(dir, 1, 10)
			require.NoError(t, err)
			defer fp.Close()
			require.Len(t, fp.files, 1)

			f := fp.files[0]
			n, err := f.Write(tc.data)
			assert.NoError(t, err)
			assert.Equal(t, len(tc.data), n)

			_, err = os.Lstat(filepath.Join(dir, "fastzip_00"))
			if tc.fileExists {
				assert.NoError(t, err, "fastzip_00 should exist")
			} else {
				assert.Error(t, err, "fastzip_00 should not exist")
			}

			// split reads to ensure read/write indexes track correctly
			buf := make([]byte, 20)
			size := 0
			{
				n, err := f.Read(buf[:5])
				assert.NoError(t, err)
				size += n
			}
			{
				n, err := f.Read(buf[5:])
				assert.NoError(t, err)
				size += n
			}

			assert.Equal(t, tc.data, buf[:size])
		})
	}
}
