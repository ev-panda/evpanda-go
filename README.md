# evpanda-go

[![CI](https://github.com/ev-panda/evpanda-go/actions/workflows/build.yml/badge.svg)](https://github.com/ev-panda/evpanda-go/actions/workflows/build.yml)

Passive OCPI / OCPP traffic capture for Go — the port of
[`@evpanda/sdk`](https://github.com/ev-panda/evpanda-node). Embed it in your
OCPI server or OCPP CSMS; it records protocol messages, buffers them
in-process, and ships them in batches to the EVPanda ingestion API.

> **It never gets in your way.** The SDK will not block your request path,
> panic into your handlers, crash your process, or grow memory unbounded. If
> it's under stress or the network is down it drops data — it never degrades
> your application.

- **One dependency** — `github.com/klauspost/compress` (pure Go, no
  transitive deps) for zstd, kept at latest; everything else is stdlib.
- **Go ≥ 1.24** (set by latest `klauspost/compress`).

## Install

```sh
go get github.com/ev-panda/evpanda-go@latest
```

```go
import "github.com/ev-panda/evpanda-go" // package evpanda
```

## Quick start

```go
package main

import (
	"context"
	"log"

	"github.com/ev-panda/evpanda-go"
)

func main() {
	// Start always returns a usable *Client. On a bad config it returns an
	// inert no-op client plus the error — your boot never crashes.
	// APIKey is omitted here, so it's read from EVPANDA_API_KEY.
	panda, err := evpanda.Start(evpanda.Config{
		NetworkType: evpanda.ProtocolOCPI, // this agent serves OCPI
		Endpoint:    "https://ingest.evpanda.io",
	})
	if err != nil {
		log.Printf("evpanda: %v (running inert)", err)
	}
	// On shutdown — flushes whatever is buffered, within DrainTimeout.
	// Close returns an error (e.g. evpanda.ErrDrainIncomplete) you may log.
	defer func() { _ = panda.Close() }()

	// In a handler this is the request context.
	ctx := context.Background()

	// OCPI message (e.g. from your inbound/outbound HTTP layer)
	panda.CaptureOCPI(ctx, evpanda.OCPIMessage{
		Direction: evpanda.Inbound,
		Identity: evpanda.RoamingIdentity{
			PlatformID:   "acme",
			PlatformName: "Acme Mobility",
			TenantID:     "cpo-42", // tenant is all-or-nothing
			TenantName:   "CPO 42",
		},
		HTTP: evpanda.CapturedHTTP{
			Method:          "POST",
			URL:             "/ocpi/2.2/cdrs",
			StatusCode:      200,
			RequestHeaders:  map[string]string{"content-type": "application/json"},
			ResponseHeaders: map[string]string{},
		},
	})
}
```

An OCPP agent is the same, with `NetworkType: evpanda.ProtocolOCPP` and
`CaptureOCPP`:

```go
panda.CaptureOCPP(ctx, evpanda.OCPPMessage{
	EventType:    evpanda.OCPPEventTypeMessage,
	Identity:     evpanda.ChargerIdentity{ChargerID: "CP-001"},
	ConnectionID: "conn-abc",
	Direction:    evpanda.Inbound, // optional for OCPP
	Payload:      []byte(`[2,"id","BootNotification",{}]`),
})
```

`CaptureOCPI` / `CaptureOCPP` are **non-blocking and never panic** — they
buffer and return immediately. Delivery happens in the background. One
Client serves a single `NetworkType`; the other `Capture*` method is a
silent no-op.

## Identity

Every message must carry an identity; the SDK validates it and silently
drops messages it can't attribute (it never panics back at you).

- **OCPI →** `RoamingIdentity`: `PlatformID` + `PlatformName` required.
- **OCPP →** `ChargerIdentity`: `ChargerID` required.
- `TenantID` + `TenantName` are optional but **all-or-nothing** — supply
  both or neither.

Identity is per message, not global config — one OCPI process can serve
many platforms and tenants; one OCPP process many chargers and tenants.
(Protocol, by contrast, is Client-wide — see `NetworkType`.)

`ctx` is **required** by `CaptureOCPI`/`CaptureOCPP`. To thread identity
through call stacks, put it on the context once; capture fills it in when
the message has no `Identity` (an explicit `msg.Identity` still wins):

```go
ctx = evpanda.WithRoamingIdentity(ctx, evpanda.RoamingIdentity{
	PlatformID: "acme", PlatformName: "Acme Mobility",
})

// No Identity on the message — resolved from ctx.
panda.CaptureOCPI(ctx, evpanda.OCPIMessage{ /* HTTP, Direction, ... */ })
```

`RoamingIdentityFromContext` / `ChargerIdentityFromContext` are also
exported if you need to read it back yourself. `WithChargerIdentity` is
the OCPP equivalent. Keys are package-private, so they won't collide with
other
context values.

## Configuration

`evpanda.Start(config)` — `Endpoint` and `NetworkType` are required;
`APIKey` is required too but may come from the `EVPANDA_API_KEY` env var
instead. Every other field falls back to its default when left at the zero
value.

| Field             | Default     | Description                                                        |
|-------------------|-------------|--------------------------------------------------------------------|
| `Endpoint`        | —           | Ingestion API base URL (`http(s)://…`).                            |
| `APIKey`          | `$EVPANDA_API_KEY` | Sent as `X-API-Key`. Falls back to the `EVPANDA_API_KEY` env var when empty; one of the two must be set. |
| `NetworkType`     | —           | `evpanda.ProtocolOCPI` or `evpanda.ProtocolOCPP`. The one protocol this Client serves. |
| `BufferCapacity`  | `10000`     | Max buffered messages. Oldest are dropped when full.               |
| `MaxCaptureBytes` | `65536`     | Per-body capture cap (bytes). Caller-enforced; see notes.          |
| `FlushInterval`   | `5s`        | Max time between flushes (`time.Duration`).                        |
| `DrainTimeout`    | `10s`       | Max time `Close()` waits to drain. Min `5s` (smaller is rejected). |
| `Compression`     | `"zstd"`    | `"zstd"` (default) or `"gzip"` — the only two options.             |
| `Debug`           | `false`     | Master log switch. When true, dropped batches are logged (silent otherwise). |
| `Logger`          | `nil`       | `*slog.Logger` used when `Debug` is true; if nil, `slog.Default()`. |

A bad config never crashes your boot: `Start` always returns a usable,
non-nil `*Client` — an inert no-op on failure — *plus* the error, so you
can log it (or ignore it) without your boot ever depending on it.

## Behavior

- **Batched delivery.** Messages flush when the buffer fills (1000) or on
  `FlushInterval`, whichever comes first.
- **Backpressure = drop-oldest.** If the upstream is slow/down, the buffer
  caps at `BufferCapacity` and discards the oldest messages. Your app is
  never blocked or back-pressured.
- **Secret redaction.** `Authorization`, `X-API-Key` and `Cookie` headers
  are stripped before anything is buffered.
- **Resilient transport.** Bounded retry with exponential backoff + full
  jitter on 5xx/network; permanent rejections (400/401/413) are dropped
  without retry storms.
- **Graceful shutdown.** `panda.Close()` flushes what's buffered within
  `DrainTimeout`, then stops. Idempotent. Returns `error`:
  `evpanda.ErrDrainIncomplete` if the deadline elapsed with messages still
  buffered (possible shutdown data loss), else `nil`.
- **Error reporting.** `Flush()` and `Close()` return `error` but never
  panic into the caller. Transport delivery failures are retried/dropped by
  design and are *not* surfaced as return values — the returned error
  covers a recovered internal panic (both) and incomplete drain (`Close`
  only). With `Debug: true`, each permanently dropped batch is logged
  (protocol, message count, reason) so you can diagnose silently — the SDK
  stays silent by default.
- **Compression.** zstd by default (`Compression: "gzip"` to opt into
  gzip instead); payloads under 1 KiB are sent uncompressed.
