// Port of src/sdk.ts + src/index.ts. The SDK facade: forwards to an active
// or no-op implementation. The facade is the one customer-facing boundary —
// it never panics into and never blocks the host (recover guards every
// method, mirroring the Node proxy's swallow).

package evpanda

import (
	"fmt"
	"sync"
	"time"
)

type sdkImpl interface {
	captureOCPI(OCPIMessage)
	captureOCPP(OCPPMessage)
	flush() error
	shutdown(deadline time.Duration) error
}

// activeSDK is the real implementation. Construction is split from start()
// so a build failure leaks no goroutine.
type activeSDK struct {
	worker      *worker
	buffer      *ringBuffer
	networkType Protocol // the one protocol this Client serves
}

// newActiveSDK is a pure build with no side effects. resolveConfig is the
// only failure site.
func newActiveSDK(c Config) (*activeSDK, error) {
	resolved, err := resolveConfig(c)
	if err != nil {
		return nil, err
	}
	buffer, err := newRingBuffer(resolved.bufferCapacity)
	if err != nil {
		return nil, err
	}
	w := newWorker(buffer, newTransport(resolved), resolved)
	return &activeSDK{worker: w, buffer: buffer, networkType: resolved.protocol}, nil
}

func (s *activeSDK) start() { s.worker.start() }

// captureOCPI / captureOCPP enqueue only when they match the configured
// NetworkType; the other is a silent no-op (one agent, one protocol).
func (s *activeSDK) captureOCPI(m OCPIMessage) {
	if s.networkType == ProtocolOCPI {
		captureOCPI(s.buffer, m)
	}
}

func (s *activeSDK) captureOCPP(m OCPPMessage) {
	if s.networkType == ProtocolOCPP {
		captureOCPP(s.buffer, m)
	}
}

// flush triggers a single-flight flush. Transport owns bounded retry and
// drops on exhaustion by design, so a successful trigger reports nil; only
// a recovered panic (in the facade) produces a non-nil error.
func (s *activeSDK) flush() error { s.worker.flushOnce(); return nil }

func (s *activeSDK) shutdown(d time.Duration) error { return s.worker.close(d) }

// noopSDK is the inert twin: every method a no-op. The facade swaps to this
// when the SDK is off or after Close.
type noopSDK struct{}

func (noopSDK) captureOCPI(OCPIMessage)      {}
func (noopSDK) captureOCPP(OCPPMessage)      {}
func (noopSDK) flush() error                 { return nil }
func (noopSDK) shutdown(time.Duration) error { return nil }

// Client is the public SDK handle. Construct it with Start.
type Client struct {
	mu   sync.RWMutex
	impl sdkImpl
}

// Start validates the config, builds the SDK, and arms its background
// worker.
//
// It ALWAYS returns a non-nil, usable *Client: on an invalid Config the
// returned client is an inert no-op (every method a safe no-op) and the
// error describes the problem. This lets a caller surface the error
// idiomatically (`c, err := Start(cfg)`) while guaranteeing a bad config
// can never crash the host's boot — callers that don't care may ignore the
// error and still get a working (silent) client.
func Start(config Config) (*Client, error) {
	impl, err := newActiveSDK(config)
	if err != nil {
		return &Client{impl: noopSDK{}}, err
	}
	impl.start() // arm the worker last
	return &Client{impl: impl}, nil
}

func (e *Client) current() sdkImpl {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.impl
}

// guard is the facade's one-way safety boundary: it runs fn and converts a
// recovered panic into an error so the SDK never panics into the host.
func guard(op string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("evpanda: recovered panic in %s: %v", op, r)
		}
	}()
	return fn()
}

// guardVoid is guard for the capture paths (panic swallowed, no error).
func guardVoid(fn func()) {
	defer func() { _ = recover() }()
	fn()
}

// CaptureOCPI buffers an OCPI message. Non-blocking; never panics into the
// caller. Invalid identity ⇒ the message is silently dropped.
func (e *Client) CaptureOCPI(msg OCPIMessage) {
	guardVoid(func() { e.current().captureOCPI(msg) })
}

// CaptureOCPP buffers an OCPP message. Non-blocking; never panics into the
// caller. Invalid identity ⇒ the message is silently dropped.
func (e *Client) CaptureOCPP(msg OCPPMessage) {
	guardVoid(func() { e.current().captureOCPP(msg) })
}

// Flush forces a delivery of whatever is buffered. It never panics into
// the caller. The returned error is non-nil only if an internal panic was
// recovered; transport delivery failures are retried/dropped by design and
// are not surfaced here (see PORTING_NOTES.md, D9).
func (e *Client) Flush() error {
	return guard("Flush", func() error { return e.current().flush() })
}

// Close swaps to the inert implementation (further captures are no-ops at
// once), then drains what is buffered within DrainTimeout. Idempotent;
// never panics into the caller. Returns ErrDrainIncomplete if the deadline
// elapsed with messages still buffered (possible shutdown data loss), or a
// wrapped error if an internal panic was recovered; nil on a clean drain.
func (e *Client) Close() error {
	return guard("Close", func() error {
		e.mu.Lock()
		impl := e.impl
		e.impl = noopSDK{}
		e.mu.Unlock()
		return impl.shutdown(0)
	})
}
