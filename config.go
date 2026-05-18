// Port of src/config.ts. Customer-facing configuration — the COMPLETE
// surface. Nothing outside this struct is configurable in v1.
//
// Not configurable here (by design): identity & protocol are per-message;
// redaction is the internal always-on denylist (no customer hook).

package evpanda

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"
)

// Config is the public configuration passed to Start. Endpoint, APIKey and
// NetworkType are required; every other field falls back to a default when
// left at its zero value.
type Config struct {
	// Endpoint is the ingestion API base, e.g. https://ingest.evpanda.io.
	Endpoint string
	// APIKey is sent as the X-API-Key header. If empty, it falls back to
	// the EVPANDA_API_KEY environment variable; one of the two must be set.
	APIKey string
	// NetworkType is the single protocol this Client serves: ProtocolOCPI
	// or ProtocolOCPP. Required — one agent runs for one network type; the
	// other Capture* method is then a no-op.
	NetworkType Protocol

	// BufferCapacity is the ring-buffer slot count. Worst-case memory is
	// BufferCapacity × MaxCaptureBytes. Zero ⇒ default.
	BufferCapacity int
	// MaxCaptureBytes is the per-body truncation cap. Zero ⇒ default.
	// (Only meaningful with the framework adapters, which are not ported;
	// kept for config parity with the Node SDK.)
	MaxCaptureBytes int
	// FlushInterval is the worker flush cadence (~5–10s). Zero ⇒ default.
	FlushInterval time.Duration
	// DrainTimeout is the Close drain deadline. Zero ⇒ default.
	DrainTimeout time.Duration
	// Compression is "zstd" (default) or "gzip". Empty ⇒ "zstd". These
	// are the only two options.
	Compression string

	// Debug is the master log switch; default false (totally silent).
	Debug bool
	// Logger is an optional structured logger; only used when Debug is
	// true. nil ⇒ no logging. Idiomatic stdlib type, not a custom
	// interface.
	Logger *slog.Logger
}

// resolvedConfig is Config with defaults applied and validated.
type resolvedConfig struct {
	endpoint        string
	apiKey          string
	protocol        Protocol
	bufferCapacity  int
	maxCaptureBytes int
	flushInterval   time.Duration
	drainTimeout    time.Duration
	compression     string
	debug           bool
	logger          *slog.Logger
}

// Defaults (mirrors DEFAULTS in config.ts). The ≤1000 server batch cap is
// the flush trigger; capacity is larger.
const (
	defaultBufferCapacity  = 10_000
	defaultMaxCaptureBytes = 64 * 1024
	defaultFlushInterval   = 5 * time.Second
	defaultDrainTimeout    = 10 * time.Second
	defaultCompression     = "zstd"
)

const errPrefix = "evpanda: config"

// apiKeyEnvVar is the fallback source for APIKey when Config.APIKey is unset.
const apiKeyEnvVar = "EVPANDA_API_KEY"

// resolveAPIKey takes Config.APIKey, else the EVPANDA_API_KEY env var; one
// of the two must be a non-empty value.
func resolveAPIKey(value string) (string, error) {
	if v := strings.TrimSpace(value); v != "" {
		return v, nil
	}
	if v := strings.TrimSpace(os.Getenv(apiKeyEnvVar)); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("%s: `apiKey` is required — set Config.APIKey or the %s env var", errPrefix, apiKeyEnvVar)
}

func requireNonEmptyString(value, field string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", fmt.Errorf("%s: `%s` is required and must be a non-empty string", errPrefix, field)
	}
	return v, nil
}

// resolveBound: zero ⇒ fallback; otherwise must be ≥ minVal. Shared by the
// int (counts) and time.Duration (intervals) options.
func resolveBound[T int | time.Duration](value, fallback T, field string, minVal T) (T, error) {
	if value == 0 {
		return fallback, nil
	}
	if value < minVal {
		return 0, fmt.Errorf("%s: `%s` must be >= %v", errPrefix, field, minVal)
	}
	return value, nil
}

func resolveEndpoint(raw string) (string, error) {
	s, err := requireNonEmptyString(raw, "endpoint")
	if err != nil {
		return "", err
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%s: `endpoint` must be a valid URL", errPrefix)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%s: `endpoint` must use http or https", errPrefix)
	}
	return strings.TrimRight(s, "/"), nil // transport appends /v1/{protocol}
}

// resolveNetworkType: required; exactly ProtocolOCPI or ProtocolOCPP.
func resolveNetworkType(value Protocol) (Protocol, error) {
	switch value {
	case ProtocolOCPI, ProtocolOCPP:
		return value, nil
	default:
		return "", fmt.Errorf("%s: `networkType` is required and must be %q or %q", errPrefix, ProtocolOCPI, ProtocolOCPP)
	}
}

// resolveCompression: empty ⇒ "zstd" (default); otherwise exactly "zstd"
// or "gzip".
func resolveCompression(value string) (string, error) {
	switch value {
	case "":
		return defaultCompression, nil
	case "gzip", "zstd":
		return value, nil
	default:
		return "", fmt.Errorf("%s: `compression` must be \"gzip\" or \"zstd\"", errPrefix)
	}
}

// resolveConfig applies defaults and validates. The only place the SDK
// surfaces a configuration error (Start swallows it into a no-op).
func resolveConfig(c Config) (resolvedConfig, error) {
	var r resolvedConfig
	var err error

	if r.endpoint, err = resolveEndpoint(c.Endpoint); err != nil {
		return r, err
	}
	if r.apiKey, err = resolveAPIKey(c.APIKey); err != nil {
		return r, err
	}
	if r.protocol, err = resolveNetworkType(c.NetworkType); err != nil {
		return r, err
	}
	if r.bufferCapacity, err = resolveBound(c.BufferCapacity, defaultBufferCapacity, "bufferCapacity", 1); err != nil {
		return r, err
	}
	if r.maxCaptureBytes, err = resolveBound(c.MaxCaptureBytes, defaultMaxCaptureBytes, "maxCaptureBytes", 1); err != nil {
		return r, err
	}
	if r.flushInterval, err = resolveBound(c.FlushInterval, defaultFlushInterval, "flushInterval", time.Millisecond); err != nil {
		return r, err
	}
	if r.drainTimeout, err = resolveBound(c.DrainTimeout, defaultDrainTimeout, "drainTimeout", 0); err != nil {
		return r, err
	}
	if r.compression, err = resolveCompression(c.Compression); err != nil {
		return r, err
	}
	r.debug = c.Debug
	r.logger = c.Logger
	return r, nil
}
