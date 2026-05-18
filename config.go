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

	// BufferCapacity is the ring-buffer slot count; worst-case memory is
	// BufferCapacity × MaxCaptureBytes. Zero uses the default (10000).
	BufferCapacity int
	// MaxCaptureBytes is the per-body capture cap, enforced by the caller.
	// Zero uses the default (65536).
	MaxCaptureBytes int
	// FlushInterval is the maximum time between flushes. Zero uses the
	// default (5s).
	FlushInterval time.Duration
	// DrainTimeout is how long Close waits to drain buffered messages.
	// Zero uses the default (10s); an explicit value must be ≥ 5s.
	DrainTimeout time.Duration
	// Compression is "zstd" (default) or "gzip"; empty uses "zstd".
	Compression string

	// Debug enables logging of dropped batches. Silent by default.
	Debug bool
	// Logger receives the debug logs when Debug is true. If nil while
	// Debug is true, slog.Default() is used.
	Logger *slog.Logger
}

// resolvedConfig is Config with defaults applied and validation passed.
type resolvedConfig struct {
	endpoint        string
	apiKey          string
	protocol        Protocol
	bufferCapacity  int
	maxCaptureBytes int
	flushInterval   time.Duration
	drainTimeout    time.Duration
	compression     string
	// logger is the effective logger: nil means silent (non-nil only when
	// Debug is true).
	logger *slog.Logger
}

const (
	defaultBufferCapacity  = 10_000
	defaultMaxCaptureBytes = 64 * 1024
	defaultFlushInterval   = 5 * time.Second
	defaultDrainTimeout    = 10 * time.Second
	defaultCompression     = "zstd"
)

const errPrefix = "evpanda: config"

// apiKeyEnvVar is the fallback source for APIKey when Config.APIKey is empty.
const apiKeyEnvVar = "EVPANDA_API_KEY"

// resolveAPIKey returns Config.APIKey, or the EVPANDA_API_KEY env var, or
// an error if neither is set.
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

// resolveBound returns fallback when value is zero, else value if it is
// ≥ minVal, else an error. Shared by the int and time.Duration options.
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

// resolveConfig applies defaults and validates the configuration.
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
	if r.drainTimeout, err = resolveBound(c.DrainTimeout, defaultDrainTimeout, "drainTimeout", 5*time.Second); err != nil {
		return r, err
	}
	if r.compression, err = resolveCompression(c.Compression); err != nil {
		return r, err
	}
	// Effective logger: silent unless Debug; Debug without a Logger uses
	// slog.Default().
	if c.Debug {
		if c.Logger != nil {
			r.logger = c.Logger
		} else {
			r.logger = slog.Default()
		}
	}
	return r, nil
}
