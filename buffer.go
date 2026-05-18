package evpanda

import (
	"errors"
	"sync"
	"time"
)

// bufferedMessage wraps a captured message with its receive time.
type bufferedMessage struct {
	capturedAt string
	message    anyMessage
}

// ringBuffer is a fixed-capacity, drop-oldest queue, safe for concurrent
// use. Producers enqueue from any goroutine; the worker drains.
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

// enqueue appends a message, dropping the oldest when at capacity.
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

// drain removes and returns all buffered messages, oldest first.
func (r *ringBuffer) drain() []bufferedMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]bufferedMessage, r.count)
	for i := 0; i < r.count; i++ {
		idx := (r.head + i) % r.capacity
		out[i] = r.buf[idx]
		r.buf[idx] = bufferedMessage{} // release reference
	}
	r.head = 0
	r.count = 0
	return out
}

// length returns the number of buffered messages.
func (r *ringBuffer) length() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

// timestampLayout is RFC3339 with millisecond precision and a UTC "Z".
const timestampLayout = "2006-01-02T15:04:05.000Z07:00"

// nowISO returns the current time as a wire timestamp.
func nowISO() string {
	return time.Now().UTC().Format(timestampLayout)
}
