package fastzip

import (
	"math"
	"sync"
)

// writeSerializer enforces that zip entries are committed to the archive in the
// order they were enumerated (sorted by name), regardless of the order their
// concurrent compression finishes. This makes the archive output
// deterministic: the same set of input files always produces the same bytes,
// which lets callers rely on a stable archive checksum across repeated runs.
//
// Only the entry whose index equals next may write, and next advances only
// once that write returns. The serializer's mutex is released while a write
// runs, so registering, queueing and aborting never wait for a write in
// progress; they wait only for the mutex itself, which is held briefly. It
// orders writes but does not guard the zip.Writer: writes still take
// Archiver.m, since Archive calls running concurrently each have their own
// serializer.
//
// Entries must present a contiguous range of indices starting at 0; a missing
// index would stall every later write.
type writeSerializer struct {
	mu      sync.Mutex
	cond    *sync.Cond
	next    int
	aborted bool

	// pending holds writes registered with enqueue whose turn hasn't come yet.
	// They run, in order, from whichever call advances next to their index.
	// Only their payload bytes are budgeted; the map itself grows with the
	// number of entries queued behind a slow one, the same order of memory
	// as the headers Archive already holds for every entry.
	pending map[int]pendingWrite

	// pendingBytes is the total size of entry data held in memory by queued
	// file writes; reserve refuses to exceed maxPendingBytes.
	pendingBytes, maxPendingBytes int64
}

// pendingWrite is a queued write and the in-memory budget it holds until run.
type pendingWrite struct {
	fn func() error
	n  int64
}

func newWriteSerializer(maxPendingBytes int64) *writeSerializer {
	// A reserved size must fit an int (plus one) for the copy's make, which
	// matters on 32-bit platforms with a large budget.
	if maxPendingBytes > math.MaxInt-1 {
		maxPendingBytes = math.MaxInt - 1
	}
	s := &writeSerializer{pending: make(map[int]pendingWrite), maxPendingBytes: maxPendingBytes}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// tryDo runs fn now if it is idx's turn and reports whether it ran.
func (s *writeSerializer) tryDo(idx int, fn func() error) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.aborted {
		return true, nil
	}
	if s.next != idx {
		return false, nil
	}
	return true, s.run(fn, 0)
}

// reserve claims n bytes of the in-memory budget for a queued write. The
// budget is returned when the write runs or the serializer is aborted, or by
// release if the caller gives up before queueing it.
func (s *writeSerializer) reserve(n int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingBytes+n > s.maxPendingBytes {
		return false
	}
	s.pendingBytes += n
	return true
}

// release returns n bytes claimed with reserve that were never queued.
func (s *writeSerializer) release(n int64) {
	s.mu.Lock()
	s.pendingBytes -= n
	s.mu.Unlock()
}

// do blocks until it is idx's turn to write, runs fn while holding the write
// turn, then releases the turn to idx+1, running any enqueued writes that
// immediately follow.
//
// If fn returns an error, or the serializer has been aborted, all outstanding
// and future writes are unblocked. A write cancelled by an abort returns nil,
// because the entry itself didn't fail and the whole archive is discarded on
// abort regardless; returning nil avoids masking the error that triggered the
// teardown.
func (s *writeSerializer) do(idx int, fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for s.next != idx && !s.aborted {
		s.cond.Wait()
	}

	if s.aborted {
		return nil
	}

	return s.run(fn, 0)
}

// enqueue registers a write that must not block the caller. If it is already
// idx's turn, fn runs immediately, otherwise it is queued and run later by the
// call that completes entry idx-1. This is for entries that hold no scarce
// resource (directories, symlinks): the dispatch loop can register them and
// keep dispatching files rather than waiting for every earlier file to finish
// compressing.
//
// The returned error is fn's own error when it ran synchronously. A queued fn's
// error is returned from whichever do or enqueue call ran it; since any error
// aborts the archive, it doesn't matter which caller reports it.
func (s *writeSerializer) enqueue(idx int, fn func() error) error {
	return s.enqueueBytes(idx, 0, fn)
}

// enqueueBytes is enqueue for a write holding n bytes previously claimed with
// reserve; the budget is returned once the write has run.
func (s *writeSerializer) enqueueBytes(idx int, n int64, fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.aborted {
		s.pendingBytes -= n
		return nil
	}

	if s.next != idx {
		s.pending[idx] = pendingWrite{fn, n}
		return nil
	}

	return s.run(fn, n)
}

// run executes fn as entry next, then drains consecutive pending entries.
// The caller must hold s.mu with s.next equal to fn's index; run releases the
// mutex while each write executes and holds it again on return. Exclusive
// access to the zip is preserved without the mutex because next does not
// advance until the write returns, and no other call can present index next.
func (s *writeSerializer) run(fn func() error, n int64) error {
	defer s.cond.Broadcast()

	for {
		s.mu.Unlock()
		err := fn()
		s.mu.Lock()

		s.pendingBytes -= n
		if err != nil {
			s.aborted = true
			return err
		}
		if s.aborted {
			// Torn down while writing; the queue has been dropped.
			return nil
		}
		s.next++

		pw, ok := s.pending[s.next]
		if !ok {
			return nil
		}
		delete(s.pending, s.next)
		fn, n = pw.fn, pw.n
	}
}

// abort wakes every waiter so they return instead of blocking forever when the
// archive is torn down before every entry has been dispatched (for example, an
// error encountered while enumerating entries). Queued writes are dropped.
func (s *writeSerializer) abort() {
	s.mu.Lock()
	s.aborted = true
	s.pending = nil
	s.cond.Broadcast()
	s.mu.Unlock()
}
