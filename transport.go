// Port of src/transport.ts. Hand-rolled transport (stdlib net/http; the
// sole dependency is github.com/klauspost/compress for zstd). Body: JSON;
// gzip or zstd with an identity fallback; tiny payloads sent uncompressed.
// Owns the bounded retry: 200 or 400/401/413 → done; 5xx/network →
// backoff; the caller never retries. Never panics.

package evpanda

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/klauspost/compress/zstd"
)

// ── Backoff (module-private, fixed by design — not configurable) ─────────

const (
	backoffBase        = 500 * time.Millisecond
	backoffMax         = 30 * time.Second
	backoffMaxAttempts = 5
)

// nextDelay is the jittered backoff before a retry attempt: capped
// exponential with full jitter. Only called for attempt ∈ [1,
// backoffMaxAttempts), so no exhaustion sentinel is needed.
func nextDelay(attempt int) time.Duration {
	capped := min(backoffBase<<attempt, backoffMax)
	return time.Duration(rand.Int64N(int64(capped)))
}

// ── API client ───────────────────────────────────────────────────────────
//
// A 1:1 wrapper over the two ingestion endpoints. TRANSPORT ONLY — no
// buffering, retry policy, or telemetry. Hand-rolled over net/http; no
// generated client (would pull heavy transitive deps into customer
// production for two endpoints).

// requestTimeout is the per-attempt cap so a hung connection still feeds
// the backoff.
const requestTimeout = 30 * time.Second

// Request header names and the JSON content type (net/http canonicalises
// the keys on Set; defined in canonical form for clarity).
const (
	headerContentType     = "Content-Type"
	headerContentEncoding = "Content-Encoding"
	headerAPIKey          = "X-API-Key"
	contentTypeJSON       = "application/json"
)

type apiClient struct {
	endpoint string
	apiKey   string
	http     *http.Client
}

// post issues a single POST /v1/{protocol}; drains the body, returns the
// status code.
func (c *apiClient) post(ctx context.Context, protocol Protocol, body []byte, encoding contentEncoding) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.endpoint+"/v1/"+string(protocol), bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set(headerContentType, contentTypeJSON)
	req.Header.Set(headerAPIKey, c.apiKey)
	if encoding != encodingIdentity {
		req.Header.Set(headerContentEncoding, string(encoding))
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body) // release the socket; body unused
	return resp.StatusCode, nil
}

// ── Transport ────────────────────────────────────────────────────────────

type contentEncoding string

const (
	encodingIdentity contentEncoding = "identity"
	encodingGzip     contentEncoding = "gzip"
	encodingZstd     contentEncoding = "zstd"
)

// compressMinBytes — below this raw size, compression isn't worth the CPU.
const compressMinBytes = 1024

// wire envelopes: embed the message (its json tags promote to top level)
// and add the SDK-owned protocol + capturedAt. Matches the Node wire shape.
type ocpiWire struct {
	OCPIMessage
	Protocol   Protocol `json:"protocol"`
	CapturedAt string   `json:"capturedAt"`
}

type ocppWire struct {
	OCPPMessage
	Protocol   Protocol `json:"protocol"`
	CapturedAt string   `json:"capturedAt"`
}

// serialize turns the envelope batch into the JSON body, stamping the
// Client-wide protocol on each record. []byte fields marshal to base64 via
// encoding/json (matching Node's Uint8Array→base64).
func serialize(batch []bufferedMessage, protocol Protocol) ([]byte, error) {
	records := make([]any, 0, len(batch))
	for _, e := range batch {
		switch m := e.message.(type) {
		case OCPIMessage:
			records = append(records, ocpiWire{m, protocol, e.capturedAt})
		case OCPPMessage:
			records = append(records, ocppWire{m, protocol, e.capturedAt})
		}
	}
	return json.Marshal(records)
}

type transport struct {
	client *apiClient
	// zstdEnc is non-nil when compression is "zstd" (the default); a nil
	// encoder means gzip. The encoder is safe for concurrent EncodeAll.
	zstdEnc *zstd.Encoder
}

func newTransport(c resolvedConfig) *transport {
	t := &transport{
		client: &apiClient{
			endpoint: c.endpoint,
			apiKey:   c.apiKey,
			http:     &http.Client{}, // per-attempt deadline via context
		},
	}
	if c.compression == "zstd" {
		// EncodeAll-only use; fall back to gzip if the encoder won't build.
		if enc, err := zstd.NewWriter(nil); err == nil {
			t.zstdEnc = enc
		}
	}
	return t
}

// compress encodes the body with the configured codec (zstd by default,
// gzip if Config.Compression == "gzip"), degrading to identity on any
// failure. Payloads below compressMinBytes are sent uncompressed.
func (t *transport) compress(raw []byte) ([]byte, contentEncoding) {
	if len(raw) < compressMinBytes {
		return raw, encodingIdentity
	}
	if t.zstdEnc != nil {
		return t.zstdEnc.EncodeAll(raw, nil), encodingZstd
	}
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write(raw); err != nil {
		return raw, encodingIdentity
	}
	if err := w.Close(); err != nil {
		return raw, encodingIdentity
	}
	return b.Bytes(), encodingGzip
}

// send serializes → compresses → POSTs with internal bounded retry. Never
// panics; a dropped batch is acceptable loss by design.
func (t *transport) send(ctx context.Context, protocol Protocol, batch []bufferedMessage) {
	if len(batch) == 0 {
		return
	}
	raw, err := serialize(batch, protocol)
	if err != nil {
		return // unserializable batch → drop
	}
	body, encoding := t.compress(raw)

	for attempt := 0; attempt < backoffMaxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(nextDelay(attempt)):
			case <-ctx.Done():
				return
			}
		}

		status, err := t.client.post(ctx, protocol, body, encoding)
		if err != nil {
			continue // network error / timeout → retryable
		}
		// 200 accepted; 400/401/413 permanent (drop, never retry — only
		// these three per the ingestion contract); any other → retryable.
		switch status {
		case http.StatusOK, http.StatusBadRequest,
			http.StatusUnauthorized, http.StatusRequestEntityTooLarge:
			return
		}
	}
	// retries exhausted → batch dropped
}
