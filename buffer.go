// Port of src/buffer.ts. Fixed-size drop-oldest ring. No I/O: drain()
// copies out and resets; the worker does the POST. Internal.
//
// Node relies on the event loop to serialize access and uses no lock. Go
// capture calls arrive from arbitrary goroutines, so a sync.Mutex guards
// the ring (the one structural deviation forced by the runtime model).

package evpanda

import (
	"errors"
	"sync"
	"time"
)

// bufferedMessage is the internal envelope: SDK-stamped capturedAt
// (receive time) wrapping the customer message. The protocol is Client-wide
// (Config.NetworkType), so it is not stored per message.
type bufferedMessage struct {
	capturedAt string
	message    anyMessage
}

// ringBuffer is a fixed-capacity drop-oldest queue.
type ringBuffer struct {
	mu       sync.Mutex
	buf      []bufferedMessage
	head     int
	count    int
	capacity int
}

func newRingBuffer(capacity int) (*ringBuffer, error) {
	if capacity < 1 {
		return nil, errors.New("evpanda: ring buffer capacity must be a positive integer")
	}
	return &ringBuffer{
		buf:      make([]bufferedMessage, capacity),
		capacity: capacity,
	}, nil
}

// enqueue appends, dropping the oldest when full (advance head; the old
// slot is overwritten below).
func (r *ringBuffer) enqueue(env bufferedMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.count == r.capacity {
		r.head = (r.head + 1) % r.capacity
	} else {
		r.count++
	}
	idx := (r.head + r.count - 1) % r.capacity
	r.buf[idx] = env
}

// drain returns live slots oldest→newest, clears refs, and resets.
func (r *ringBuffer) drain() []bufferedMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]bufferedMessage, r.count)
	for i := 0; i < r.count; i++ {
		idx := (r.head + i) % r.capacity
		out[i] = r.buf[idx]
		r.buf[idx] = bufferedMessage{} // release ref
	}
	r.head = 0
	r.count = 0
	return out
}

// length is the number of buffered messages.
func (r *ringBuffer) length() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

// timestampLayout is the wire timestamp format: RFC3339 with millisecond
// precision and a UTC "Z" (matches the Node SDK's Date.toISOString()).
const timestampLayout = "2006-01-02T15:04:05.000Z07:00"

// nowISO renders the capture timestamp (UTC, millisecond precision).
func nowISO() string {
	return time.Now().UTC().Format(timestampLayout)
}
