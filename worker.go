// Port of src/worker.ts. Single non-reentrant worker. A goroutine polls on
// a ticker so a slow flush never overlaps the next. Flushes on count ≥
// batchCap or flushInterval, drains, then POSTs via transport (which owns
// retry). Also owns the bounded shutdown drain. Never panics.

package evpanda

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrDrainIncomplete is returned by Close when the drain deadline elapsed
// with messages still buffered — i.e. some captured data may have been
// dropped on shutdown.
var ErrDrainIncomplete = errors.New("evpanda: close drain deadline exceeded with messages still buffered")

// batchCap is the server batch cap — also the size-based flush trigger.
const batchCap = 1000

// pollInterval is the granularity for the size trigger (producers don't
// push; the worker polls, mirroring the Node POLL_MS timer).
const pollInterval = 500 * time.Millisecond

type worker struct {
	buffer    *ringBuffer
	transport *transport
	config    resolvedConfig

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{} // closed when the loop goroutine exits

	// flushMu serializes flushes so they never overlap (the worker's
	// "single non-reentrant" property).
	flushMu sync.Mutex

	lastMu    sync.Mutex
	lastFlush time.Time
}

func newWorker(b *ringBuffer, t *transport, c resolvedConfig) *worker {
	return &worker{
		buffer:    b,
		transport: t,
		config:    c,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
}

// start arms the polling goroutine.
func (w *worker) start() {
	w.setLastFlush(time.Now())
	go w.loop()
}

func (w *worker) loop() {
	defer close(w.doneCh)
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		select {
		case <-w.stopCh:
			return
		case <-t.C:
			if w.shouldFlush() {
				w.flushOnce()
			}
		}
	}
}

// flushOnce runs one flush, serialized against any other flushOnce so the
// loop and a concurrent Flush() never overlap.
func (w *worker) flushOnce() {
	w.flushMu.Lock()
	defer w.flushMu.Unlock()
	w.runFlush(context.Background())
}

// stop halts the polling loop. No drain — close owns the final drain. An
// in-flight loop flush is NOT cancelled (it finishes best-effort in the
// background, like the Node SDK's detached inflight); close only bounds how
// long it *waits* for the loop to exit.
func (w *worker) stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
}

// close is one-shot and idempotent: stop the loop, join any in-flight
// flush, then a bounded final drain. deadline ≤ 0 ⇒ configured
// drainTimeout. Returns ErrDrainIncomplete if the deadline elapsed before
// everything was drained; nil on a clean drain (or on an already-closed,
// idempotent re-call).
func (w *worker) close(deadline time.Duration) error {
	select {
	case <-w.stopCh:
		return nil // already closed — a prior close already reported
	default:
	}
	w.stop()

	if deadline <= 0 {
		deadline = w.config.drainTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	// Wait for the loop goroutine to exit, but never past the deadline
	// (mirrors the Node close() Promise.race against a cap timer). A
	// loop-initiated flush blocks the loop's exit, so this also waits out
	// that flush, bounded by the deadline.
	select {
	case <-w.doneCh:
	case <-ctx.Done():
		return ErrDrainIncomplete
	}

	for w.buffer.length() > 0 {
		select {
		case <-ctx.Done():
			return ErrDrainIncomplete
		default:
		}
		w.runFlush(ctx)
	}
	return nil
}

// ── internal ──────────────────────────────────────────────────────────────

func (w *worker) setLastFlush(t time.Time) {
	w.lastMu.Lock()
	w.lastFlush = t
	w.lastMu.Unlock()
}

func (w *worker) shouldFlush() bool {
	n := w.buffer.length()
	if n == 0 {
		return false
	}
	w.lastMu.Lock()
	since := time.Since(w.lastFlush)
	w.lastMu.Unlock()
	return n >= batchCap || since >= w.config.flushInterval
}

func (w *worker) runFlush(ctx context.Context) {
	w.setLastFlush(time.Now())
	batch := w.buffer.drain()
	if len(batch) == 0 {
		return
	}

	// One Client serves a single protocol (Config.NetworkType), so the
	// whole batch ships to one endpoint — just chunk at batchCap.
	protocol := w.config.protocol
	for i := 0; i < len(batch); i += batchCap {
		end := min(i+batchCap, len(batch))
		// transport owns retry; the worker sends once and moves on.
		w.transport.send(ctx, protocol, batch[i:end])
	}
}
