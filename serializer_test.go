package fastzip

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteSerializerOrdersWrites(t *testing.T) {
	s := newWriteSerializer(0)

	var got []int
	record := func(i int) func() error {
		return func() error {
			got = append(got, i)
			return nil
		}
	}

	// Queued entries wait for their predecessor and are drained, in order, by
	// the call that completes it.
	require.NoError(t, s.enqueue(1, record(1)))
	require.NoError(t, s.enqueue(2, record(2)))
	require.Empty(t, got)

	require.NoError(t, s.do(0, record(0)))
	require.Equal(t, []int{0, 1, 2}, got)

	// An entry whose turn has already come runs immediately.
	require.NoError(t, s.enqueue(3, record(3)))
	require.Equal(t, []int{0, 1, 2, 3}, got)

	// Blocked do calls resume in index order once the gap is filled.
	var wg sync.WaitGroup
	for _, i := range []int{6, 5} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, s.do(i, record(i)))
		}()
	}
	require.NoError(t, s.enqueue(7, record(7)))
	require.NoError(t, s.do(4, record(4)))
	wg.Wait()
	require.Equal(t, []int{0, 1, 2, 3, 4, 5, 6, 7}, got)
}

func TestWriteSerializerQueuedErrorAborts(t *testing.T) {
	s := newWriteSerializer(0)
	boom := errors.New("boom")

	require.NoError(t, s.enqueue(1, func() error { return boom }))

	ran := false
	require.NoError(t, s.enqueue(2, func() error { ran = true; return nil }))

	// The queued failure surfaces from the call that drained it, and everything
	// after it is skipped.
	require.ErrorIs(t, s.do(0, func() error { return nil }), boom)
	require.False(t, ran)
	require.NoError(t, s.do(3, func() error { t.Fatal("ran after abort"); return nil }))
	require.NoError(t, s.enqueue(4, func() error { t.Fatal("ran after abort"); return nil }))
}

func TestWriteSerializerAbortReleasesWaiters(t *testing.T) {
	s := newWriteSerializer(0)

	ran := false
	done := make(chan error)
	go func() { done <- s.do(1, func() error { ran = true; return nil }) }()
	require.NoError(t, s.enqueue(2, func() error { ran = true; return nil }))

	s.abort()
	require.NoError(t, <-done)
	require.False(t, ran)
}

func TestWriteSerializerMemoryBudget(t *testing.T) {
	s := newWriteSerializer(10)

	require.True(t, s.reserve(6))
	require.False(t, s.reserve(5), "over budget")
	require.True(t, s.reserve(4))

	// Budget is returned when a queued write runs, or when it is released
	// without being queued.
	require.NoError(t, s.enqueueBytes(1, 6, func() error { return nil }))
	s.release(4)
	require.False(t, s.reserve(5), "queued write still holds its bytes")

	require.NoError(t, s.do(0, func() error { return nil }))
	require.True(t, s.reserve(10), "budget returned after the queued write ran")
	s.release(10)

	// Queueing after an abort drops the write and returns its bytes.
	s.abort()
	require.True(t, s.reserve(10))
	require.NoError(t, s.enqueueBytes(2, 10, func() error { return nil }))
	require.Equal(t, int64(0), s.pendingBytes)
}
