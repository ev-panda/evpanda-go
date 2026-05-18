// Port of test/e2e.test.ts. Drives the public API against a mock upstream
// (httptest) and asserts capture → batch → upstream, redaction, routing,
// gzip + chunking, drop-oldest, graceful close, and "never panics".
//
// Go has no separate built artifact, so this is an in-package external
// test (package evpanda_test) — the idiomatic equivalent of importing the
// Node SDK's built dist.

package evpanda_test

import (
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	evpanda "github.com/ev-panda/evpanda-go"
)

// ── Mock upstream ────────────────────────────────────────────────────────

type received struct {
	path    string
	headers http.Header
	records []map[string]any
}

type mockUpstream struct {
	mu       sync.Mutex
	server   *httptest.Server
	received []received
	status   int // mutable: change to make the upstream reject
}

func startMockUpstream() *mockUpstream {
	m := &mockUpstream{status: http.StatusOK}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reader io.Reader = r.Body
		switch r.Header.Get("content-encoding") {
		case "gzip":
			if gz, err := gzip.NewReader(r.Body); err == nil {
				defer gz.Close()
				reader = gz
			}
		case "zstd":
			if zr, err := zstd.NewReader(r.Body); err == nil {
				defer zr.Close()
				reader = zr
			}
		}
		raw, _ := io.ReadAll(reader)
		var records []map[string]any
		_ = json.Unmarshal(raw, &records)

		m.mu.Lock()
		m.received = append(m.received, received{
			path:    r.URL.Path,
			headers: r.Header.Clone(),
			records: records,
		})
		status := m.status
		m.mu.Unlock()

		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"captured":0,"failed":0}`))
	}))
	return m
}

func (m *mockUpstream) setStatus(s int) {
	m.mu.Lock()
	m.status = s
	m.mu.Unlock()
}

func (m *mockUpstream) ocpiRecords() []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []map[string]any
	for _, r := range m.received {
		if r.path == "/v1/ocpi" {
			out = append(out, r.records...)
		}
	}
	return out
}

func (m *mockUpstream) ocpiPosts() []received {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []received
	for _, r := range m.received {
		if r.path == "/v1/ocpi" {
			out = append(out, r)
		}
	}
	return out
}

func (m *mockUpstream) close() { m.server.Close() }

// ── Helpers ──────────────────────────────────────────────────────────────

func waitFor(t *testing.T, predicate func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("waitFor: timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// makeOCPI is a valid OCPI message tagged with an index; carries a
// denylisted header.
func makeOCPI(i int) evpanda.OCPIMessage {
	return evpanda.OCPIMessage{
		Direction: evpanda.Inbound,
		Identity: evpanda.RoamingIdentity{
			PlatformID:   "acme",
			PlatformName: "Acme Mobility",
			TenantID:     "t1",
			TenantName:   "Tenant One",
		},
		HTTP: evpanda.CapturedHTTP{
			Method:          "POST",
			URL:             "/ocpi/2.2/cdrs/" + strconv.Itoa(i),
			StatusCode:      200,
			RequestHeaders:  map[string]string{"Authorization": "Bearer SECRET", "X-Trace": strconv.Itoa(i)},
			ResponseHeaders: map[string]string{"content-type": "application/json"},
			RequestBody:     []byte("body-" + strconv.Itoa(i)),
			Truncated:       false,
		},
	}
}

func httpOf(t *testing.T, rec map[string]any) map[string]any {
	t.Helper()
	h, ok := rec["http"].(map[string]any)
	if !ok {
		t.Fatalf("record has no http object: %v", rec)
	}
	return h
}

// ── Tests ────────────────────────────────────────────────────────────────

func TestCaptureBatchRedactRoute(t *testing.T) {
	mock := startMockUpstream()
	defer mock.close()

	sdk, err := evpanda.Start(evpanda.Config{
		NetworkType:   evpanda.ProtocolOCPI,
		Endpoint:      mock.server.URL,
		APIKey:        "test-key",
		FlushInterval: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Start returned unexpected error: %v", err)
	}
	defer sdk.Close()

	for i := 0; i < 3; i++ {
		sdk.CaptureOCPI(makeOCPI(i))
	}

	waitFor(t, func() bool { return len(mock.ocpiRecords()) == 3 }, 3*time.Second)

	recs := mock.ocpiRecords()
	sort.Slice(recs, func(a, b int) bool {
		return httpOf(t, recs[a])["url"].(string) < httpOf(t, recs[b])["url"].(string)
	})
	if len(recs) != 3 {
		t.Fatalf("want 3 records, got %d", len(recs))
	}

	// Routing: only /v1/ocpi hit, with the configured api key.
	for _, p := range mock.ocpiPosts() {
		if p.path != "/v1/ocpi" {
			t.Fatalf("unexpected path %q", p.path)
		}
	}
	if got := mock.ocpiPosts()[0].headers.Get("x-api-key"); got != "test-key" {
		t.Fatalf("x-api-key = %q, want test-key", got)
	}

	tsRe := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)
	for i, rec := range recs {
		if rec["protocol"] != "ocpi" {
			t.Fatalf("protocol = %v, want ocpi", rec["protocol"])
		}
		if !tsRe.MatchString(rec["capturedAt"].(string)) {
			t.Fatalf("capturedAt %q not ISO-millis-Z", rec["capturedAt"])
		}
		h := httpOf(t, rec)
		if h["url"] != "/ocpi/2.2/cdrs/"+strconv.Itoa(i) {
			t.Fatalf("url = %v", h["url"])
		}
		// Redaction: Authorization stripped (case-insensitive), others kept.
		reqHeaders := h["requestHeaders"].(map[string]any)
		for k := range reqHeaders {
			if k == "Authorization" || k == "authorization" {
				t.Fatalf("Authorization header was not redacted")
			}
		}
		if reqHeaders["X-Trace"] != strconv.Itoa(i) {
			t.Fatalf("X-Trace = %v, want %d", reqHeaders["X-Trace"], i)
		}
		// Binary body round-trips as base64.
		decoded, err := base64.StdEncoding.DecodeString(h["requestBody"].(string))
		if err != nil || string(decoded) != "body-"+strconv.Itoa(i) {
			t.Fatalf("requestBody round-trip failed: %v / %q", err, decoded)
		}
	}
}

func TestGzipAndChunking(t *testing.T) {
	mock := startMockUpstream()
	defer mock.close()

	sdk, err := evpanda.Start(evpanda.Config{
		NetworkType:    evpanda.ProtocolOCPI,
		Endpoint:       mock.server.URL,
		APIKey:         "k",
		Compression:    "gzip", // exercise the opt-in gzip path explicitly
		FlushInterval:  100 * time.Millisecond,
		BufferCapacity: 100_000,
	})
	if err != nil {
		t.Fatalf("Start returned unexpected error: %v", err)
	}
	defer sdk.Close()

	const n = 2500
	for i := 0; i < n; i++ {
		sdk.CaptureOCPI(makeOCPI(i))
	}

	waitFor(t, func() bool { return len(mock.ocpiRecords()) == n }, 8*time.Second)

	// Chunked at ≤1000 per POST → ceil(2500/1000) = 3 requests.
	posts := mock.ocpiPosts()
	if len(posts) != 3 {
		t.Fatalf("want 3 posts, got %d", len(posts))
	}
	for _, p := range posts {
		if len(p.records) > 1000 {
			t.Fatalf("post had %d records (>1000)", len(p.records))
		}
		if p.headers.Get("content-encoding") != "gzip" {
			t.Fatalf("post not gzip-encoded")
		}
	}

	// FIFO order preserved across the chunked POSTs.
	for i, rec := range mock.ocpiRecords() {
		if got := httpOf(t, rec)["url"]; got != "/ocpi/2.2/cdrs/"+strconv.Itoa(i) {
			t.Fatalf("order broken at %d: %v", i, got)
		}
	}
}

func TestDropOldest(t *testing.T) {
	mock := startMockUpstream()
	defer mock.close()

	sdk, err := evpanda.Start(evpanda.Config{
		NetworkType:    evpanda.ProtocolOCPI,
		Endpoint:       mock.server.URL,
		APIKey:         "k",
		BufferCapacity: 5,
		FlushInterval:  60 * time.Second, // no auto flush during the test
	})
	if err != nil {
		t.Fatalf("Start returned unexpected error: %v", err)
	}
	defer sdk.Close()

	for i := 0; i < 12; i++ { // 0..11
		sdk.CaptureOCPI(makeOCPI(i))
	}
	sdk.Flush() // force one drain

	waitFor(t, func() bool { return len(mock.ocpiRecords()) == 5 }, 3*time.Second)

	var urls []string
	for _, rec := range mock.ocpiRecords() {
		urls = append(urls, httpOf(t, rec)["url"].(string))
	}
	sort.Strings(urls)
	want := []string{
		"/ocpi/2.2/cdrs/10",
		"/ocpi/2.2/cdrs/11",
		"/ocpi/2.2/cdrs/7",
		"/ocpi/2.2/cdrs/8",
		"/ocpi/2.2/cdrs/9",
	}
	for i := range want {
		if urls[i] != want[i] {
			t.Fatalf("survivors = %v, want %v", urls, want)
		}
	}
}

func TestFlushOnClose(t *testing.T) {
	mock := startMockUpstream()
	defer mock.close()

	sdk, err := evpanda.Start(evpanda.Config{
		NetworkType:   evpanda.ProtocolOCPI,
		Endpoint:      mock.server.URL,
		APIKey:        "k",
		FlushInterval: 60 * time.Second, // never auto-flushes within the test
	})
	if err != nil {
		t.Fatalf("Start returned unexpected error: %v", err)
	}

	for i := 0; i < 4; i++ {
		sdk.CaptureOCPI(makeOCPI(i))
	}
	if len(mock.ocpiRecords()) != 0 {
		t.Fatalf("nothing should be sent yet, got %d", len(mock.ocpiRecords()))
	}

	if err := sdk.Close(); err != nil { // graceful drain, clean → nil
		t.Fatalf("Close on a clean drain returned %v, want nil", err)
	}

	waitFor(t, func() bool { return len(mock.ocpiRecords()) == 4 }, 3*time.Second)
}

func TestNeverPanicsWhenUpstreamFails(t *testing.T) {
	mock := startMockUpstream()
	defer mock.close()

	mock.setStatus(http.StatusBadRequest) // permanent reject → dropped
	sdk, err := evpanda.Start(evpanda.Config{
		NetworkType:   evpanda.ProtocolOCPI,
		Endpoint:      mock.server.URL,
		APIKey:        "k",
		FlushInterval: 60 * time.Second,
	})
	if err != nil {
		t.Fatalf("Start returned unexpected error: %v", err)
	}
	defer sdk.Close()

	// Capture during a failing upstream — must not panic.
	for i := 0; i < 3; i++ {
		sdk.CaptureOCPI(makeOCPI(i))
	}
	// Malformed customer input — must not panic either (facade recovers).
	sdk.CaptureOCPI(evpanda.OCPIMessage{}) // invalid identity
	sdk.CaptureOCPP(evpanda.OCPPMessage{}) // invalid identity
	sdk.CaptureOCPI(makeOCPI(99))          // still usable

	sdk.Flush() // resolves even though the upstream 400s

	if len(mock.ocpiPosts()) == 0 {
		t.Fatalf("expected at least one delivery attempt")
	}
	// Still usable afterwards.
	sdk.CaptureOCPI(makeOCPI(100))
	sdk.Flush()
}

// Start with a bad config must return an inert (no-op) SDK, never panic.
func TestBadConfigIsInert(t *testing.T) {
	sdk, err := evpanda.Start(evpanda.Config{Endpoint: "not-a-url", APIKey: ""})
	if err == nil {
		t.Fatal("Start on a bad config must return an error")
	}
	if sdk == nil {
		t.Fatal("Start must never return nil, even on bad config")
	}
	// All of these must be safe no-ops on the inert SDK.
	sdk.CaptureOCPI(makeOCPI(1))
	sdk.CaptureOCPP(evpanda.OCPPMessage{})
	if err := sdk.Flush(); err != nil {
		t.Fatalf("inert Flush returned %v, want nil", err)
	}
	if err := sdk.Close(); err != nil {
		t.Fatalf("inert Close returned %v, want nil", err)
	}
}

// APIKey falls back to EVPANDA_API_KEY; one of the two must be set.
func TestAPIKeyFromEnv(t *testing.T) {
	mock := startMockUpstream()
	defer mock.close()

	base := evpanda.Config{
		NetworkType: evpanda.ProtocolOCPI,
		Endpoint:    mock.server.URL,
	}

	// Neither Config.APIKey nor the env var → error, inert client.
	t.Setenv("EVPANDA_API_KEY", "")
	if _, err := evpanda.Start(base); err == nil {
		t.Fatal("Start must error when no API key is set anywhere")
	}

	// Env var set, Config.APIKey empty → resolves from the environment.
	t.Setenv("EVPANDA_API_KEY", "env-key")
	sdk, err := evpanda.Start(base)
	if err != nil {
		t.Fatalf("Start with EVPANDA_API_KEY set returned %v", err)
	}
	defer sdk.Close()

	sdk.CaptureOCPI(makeOCPI(1))
	sdk.Flush()
	waitFor(t, func() bool { return len(mock.ocpiRecords()) == 1 }, 3*time.Second)
	if got := mock.ocpiPosts()[0].headers.Get("x-api-key"); got != "env-key" {
		t.Fatalf("x-api-key = %q, want env-key", got)
	}
}
