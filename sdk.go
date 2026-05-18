package evpanda

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type sdkImpl interface {
	captureOCPI(context.Context, OCPIMessage)
	captureOCPP(context.Context, OCPPMessage)
	flush() error
	shutdown(deadline time.Duration) error
}

// activeSDK is the live implementation. Building it has no side effects;
// start launches the worker.
type activeSDK struct {
	worker      *worker
	buffer      *ringBuffer
	networkType Protocol
}

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

// captureOCPI and captureOCPP buffer a message only when it matches the
// configured protocol; otherwise they are no-ops. When the message carries
// no identity, it is resolved from ctx (see WithRoamingIdentity /
// WithChargerIdentity); an explicit message identity always wins.
func (s *activeSDK) captureOCPI(ctx context.Context, m OCPIMessage) {
	if s.networkType != ProtocolOCPI {
		return
	}
	if m.Identity == (RoamingIdentity{}) {
		if id, ok := RoamingIdentityFromContext(ctx); ok {
			m.Identity = id
		}
	}
	captureOCPI(s.buffer, m)
}

func (s *activeSDK) captureOCPP(ctx context.Context, m OCPPMessage) {
	if s.networkType != ProtocolOCPP {
		return
	}
	if m.Identity == (ChargerIdentity{}) {
		if id, ok := ChargerIdentityFromContext(ctx); ok {
			m.Identity = id
		}
	}
	captureOCPP(s.buffer, m)
}

func (s *activeSDK) flush() error                   { s.worker.flushOnce(); return nil }
func (s *activeSDK) shutdown(d time.Duration) error { return s.worker.close(d) }

// noopSDK is the inert implementation used when Start fails or after Close.
type noopSDK struct{}

func (noopSDK) captureOCPI(context.Context, OCPIMessage) {}
func (noopSDK) captureOCPP(context.Context, OCPPMessage) {}
func (noopSDK) flush() error                             { return nil }
func (noopSDK) shutdown(time.Duration) error             { return nil }

// Client captures and ships traffic. Construct it with [Start]; it is safe
// for concurrent use.
type Client struct {
	mu   sync.RWMutex
	impl sdkImpl
}

// Start validates cfg, builds the SDK, and launches its background worker.
//
// It always returns a non-nil, usable *Client. On an invalid config the
// returned client is an inert no-op and the error describes the problem,
// so a bad config can never crash the host's boot — callers may surface
// the error or ignore it and keep a silent client.
func Start(cfg Config) (*Client, error) {
	impl, err := newActiveSDK(cfg)
	if err != nil {
		return &Client{impl: noopSDK{}}, err
	}
	impl.start()
	return &Client{impl: impl}, nil
}

func (e *Client) current() sdkImpl {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.impl
}

// guard runs fn and converts a recovered panic into an error so the SDK
// never panics into the caller.
func guard(op string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("evpanda: recovered panic in %s: %v", op, r)
		}
	}()
	return fn()
}

// guardVoid runs fn and swallows any panic (used by the capture paths).
func guardVoid(fn func()) {
	defer func() { _ = recover() }()
	fn()
}

// CaptureOCPI buffers an OCPI message for delivery. ctx is required; if
// msg has no identity it is resolved from ctx (see WithRoamingIdentity).
// Non-blocking and never panics; a message with an invalid identity is
// silently dropped.
func (e *Client) CaptureOCPI(ctx context.Context, msg OCPIMessage) {
	if ctx == nil {
		ctx = context.Background()
	}
	guardVoid(func() { e.current().captureOCPI(ctx, msg) })
}

// CaptureOCPP buffers an OCPP message for delivery. ctx is required; if
// msg has no identity it is resolved from ctx (see WithChargerIdentity).
// Non-blocking and never panics; a message with an invalid identity is
// silently dropped.
func (e *Client) CaptureOCPP(ctx context.Context, msg OCPPMessage) {
	if ctx == nil {
		ctx = context.Background()
	}
	guardVoid(func() { e.current().captureOCPP(ctx, msg) })
}

// Flush triggers immediate delivery of buffered messages. It never panics;
// the returned error is non-nil only if an internal panic was recovered.
// Transport delivery failures are retried and dropped by design and are
// not reported here.
func (e *Client) Flush() error {
	return guard("Flush", func() error { return e.current().flush() })
}

// Close stops capture and drains buffered messages within DrainTimeout. It
// is idempotent and never panics. It returns ErrDrainIncomplete if the
// deadline elapsed with messages still buffered, a wrapped error if an
// internal panic was recovered, or nil on a clean drain.
func (e *Client) Close() error {
	return guard("Close", func() error {
		e.mu.Lock()
		impl := e.impl
		e.impl = noopSDK{}
		e.mu.Unlock()
		return impl.shutdown(0)
	})
}
