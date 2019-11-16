package filepool

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilePoolSizes(t *testing.T) {
	dir, err := ioutil.TempDir("", "fastzip-filepool")
	require.NoError(t, err)

	for i := 0; i < 16; i++ {
		fp, err := New(dir, i)
		if i == 0 {
			require.Error(t, err, "size of zero should return error")
			continue
		}
		require.NoError(t, err)

		for n := 0; n < i; n++ {
			_, err = os.Lstat(filepath.Join(dir, fmt.Sprintf("fastzip_%02d", n)))
			assert.NoError(t, err, fmt.Sprintf("fastzip_%02d should exist", n))
		}

		assert.NoError(t, fp.Close())

		for n := 0; n < i; n++ {
			_, err = os.Lstat(filepath.Join(dir, fmt.Sprintf("fastzip_%02d", n)))
			assert.Error(t, err, fmt.Sprintf("fastzip_%02d shouldn't exist", n))
		}
	}
}

func TestFilePoolReset(t *testing.T) {
	dir, err := ioutil.TempDir("", "fastzip-filepool")
	require.NoError(t, err)

	fp, err := New(dir, 16)
	require.NoError(t, err)
	for i := 0; i < 16; i++ {
		file := fp.Get()
		file.Write(bytes.Repeat([]byte("0"), i))

		b, err := ioutil.ReadAll(file)
		assert.NoError(t, err)
		assert.Len(t, b, i)
		assert.Equal(t, uint64(i), file.Written())

		_, err = file.Hasher().Write([]byte("hello"))
		assert.NoError(t, err)
		assert.Equal(t, uint32(0x3610a686), file.Checksum())

		fp.Put(file)
	}

	for i := 0; i < 16; i++ {
		file := fp.Get()

		b, err := ioutil.ReadAll(file)
		assert.NoError(t, err)
		assert.Len(t, b, 0)
		assert.Equal(t, uint64(0), file.Written())
		assert.Equal(t, uint32(0), file.Checksum())

		fp.Put(file)
	}

	assert.NoError(t, fp.Close())
}

func TestFilePoolCloseError(t *testing.T) {
	dir, err := ioutil.TempDir("", "fastzip-filepool")
	require.NoError(t, err)

	fp, err := New(dir, 16)
	require.NoError(t, err)

	for _, file := range fp.files {
		require.NoError(t, file.f.Close())
	}

	err = fp.Close()
	require.Error(t, err, "expected already closed error")
	count := 0
	for {
		u, ok := err.(interface {
			Unwrap() error
		})
		if !ok {
			break
		}
		err = u.Unwrap()
		count++
	}
	assert.Equal(t, 16, count)
}

func TestFilePoolNoErrorOnAlreadyDeleted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on windows (cannot delete in-use file)")
	}

	dir, err := ioutil.TempDir("", "fastzip-filepool")
	require.NoError(t, err)

	fp, err := New(dir, 16)
	require.NoError(t, err)

	err = os.RemoveAll(dir)
	require.NoError(t, err)

	assert.NoError(t, fp.Close())
}
