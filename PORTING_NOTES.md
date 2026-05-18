# Porting notes — evpanda-node → evpanda-go

A one-file-for-one-file port of the Node SDK's **core** (adapters
excluded, per the brief). Every judgement call is logged below with the
decision taken so it can be reviewed and overridden. Rule applied
throughout: *when in doubt, the simplest industry-standard Go approach.*

## File mapping

Package `evpanda` lives at the **repo root** (idiomatic Go; import path
`github.com/ev-panda/evpanda-go`).

| Node (`src/*.ts`)     | Go (root `*.go`)     | Notes |
|-----------------------|----------------------|-------|
| `index.ts`            | `doc.go`             | Go has no entrypoint file; exports = capitalised identifiers. Package doc lives here. |
| `config.ts`           | `config.go`          | |
| `types.ts`            | `types.go`           | |
| `identity.ts`         | `identity.go`        | Resolver/`IdentityInput` generics were adapter-only → omitted. |
| `buffer.ts`           | `buffer.go`          | + mutex (see D2). `nowISO`/`timestampLayout` live here (envelope timestamp). |
| `instrumentation.ts`  | `instrumentation.go` | |
| `transport.ts`        | `transport.go`       | |
| `worker.ts`           | `worker.go`          | Also defines `ErrDrainIncomplete` (see D9). |
| `sdk.ts`              | `sdk.go`             | Also absorbs `index.ts`'s public-surface role. |
| `test/e2e.test.ts`    | `e2e_test.go`        | See D3. |
| `adapters/*`          | — (excluded)         | Per the brief. |

---

## Decisions & doubts

### D1 — Package location → **RESOLVED: repo root**
Originally placed in `src/` to mirror the Node layout verbatim. Per your
review, moved to the **repo root**: idiomatic Go, import path
`github.com/ev-panda/evpanda-go`, package `evpanda`, no alias required. The
Node SDK's *logical* file-for-file mapping is preserved (see table above);
only the directory differs. No code changes were needed beyond the
`_test.go` import line.

### D2 — Ring buffer needs a mutex
**Doubt.** Node's buffer takes no lock (the event loop serialises access).
**Decision.** Go `CaptureOCPI/OCPP` may be called from any goroutine, so
`ringBuffer` is guarded by a `sync.Mutex`. This is the one structural
deviation forced by the runtime model; semantics (drop-oldest, FIFO drain)
are identical. Verified under `go test -race`.

### D3 — Test layout
**Doubt.** Node's e2e test imports the *built* `dist/`. Go has no build
artifact.
**Decision.** `e2e_test.go` at the repo root, external test package
`evpanda_test`, exercising only the public API against an `httptest`
upstream — the idiomatic Go equivalent. Same five scenarios as Node plus
one extra (`TestBadConfigIsInert`); two also assert the D9 error contract.
Kept beside the package (not a top-level `test/` dir) so `go test ./...`
finds it without an awkward second module path.

### D4 — Durations are `time.Duration`, not millisecond ints
**Doubt.** Node uses `flushInterval`/`drainTimeout` as numbers of ms.
**Decision.** Idiomatic Go uses `time.Duration` (`5 * time.Second`).
Defaults preserved (5s / 10s).

### D5 — Zero value means "use default"
**Doubt.** Node distinguishes `undefined` (→ default) from a supplied
value. Go has no `undefined`; the zero value is the natural sentinel.
**Decision.** Zero ⇒ default for every optional field (matches Node's
`undefined ⇒ fallback`). Negative/too-small explicit values are rejected by
`resolveConfig`, exactly like Node.
**Known edge:** `DrainTimeout` cannot be set to an explicit `0` (zero means
"default 10s"). Node's min is 0. Judged acceptable — a 0-second drain is
not a useful configuration. Flagged for review.

### D6 — `StatusCode` omitted when `0`
`CapturedHTTP.StatusCode` uses `json:",omitempty"`, so `0` is omitted on
the wire (Node's `statusCode?` is likewise optional). Status `0` is not a
real HTTP status, so no information is lost.

### D7 — zstd compression → **RESOLVED: real zstd, now the default**
Originally `"zstd"` degraded to gzip to preserve a zero-dependency build
(the Go stdlib has no zstd). Per review, the dependency
`github.com/klauspost/compress` (pure Go, no transitive deps) was added
and **zstd is now the default** compression; `Compression: "gzip"` opts
into gzip. These are the only two options.

- `transport` holds a reused `*zstd.Encoder` (`EncodeAll`, concurrency-
  safe); a nil encoder ⇒ gzip. Identity fallback on any failure, and
  sub-`compressMinBytes` payloads still go uncompressed.
- Wire `Content-Encoding` is now `zstd`/`gzip`/`identity` accordingly.
- Consequence: the "zero runtime dependencies" property is gone (one pure-
  Go dep). `go get @latest` initially dragged the module to go 1.24; see
  D19 — the dep is now **pinned to v1.18.0** to hold the floor at go 1.22.
- Tests: the mock upstream decodes gzip *and* zstd; `TestGzipAndChunking`
  sets `Compression: "gzip"` to keep covering the gzip path while the
  other tests exercise the zstd default.

### D8 — `Start` signature → **RESOLVED: `(*Client, error)`**
Originally `Start(Config) *EVPanda` (no error), to mirror Node's contract
that a bad config never crashes the host's boot. Per the "idiomatic Go"
review, `Start` now returns `(*Client, error)`:

- It **always** returns a non-nil, usable `*Client` — an inert no-op on
  failure — so the safety contract is fully preserved (a caller may ignore
  the error and still have a working silent client).
- The config error is now surfaced idiomatically (`c, err := Start(cfg)`),
  the standard Go fallible-constructor shape.

This is the deliberate trade-off: returning a usable value *and* a non-nil
error is unusual, but here it's the only way to be both idiomatic
(error-returning) and safe (never nil, never fatal). Documented on `Start`.

### D9 — `Flush()` / `Close()` return `error` → **RESOLVED: return error**
Originally void (Node's `flush`/`close` return `Promise<void>` that never
rejects). Per your review, both now return `error` — idiomatic Go — without
breaking the safety contract:

- Neither **panics** into the caller; a recovered internal panic is wrapped
  and returned as the error instead of being silently swallowed.
- `Close()` additionally returns the exported sentinel
  **`ErrDrainIncomplete`** when the drain deadline elapsed with messages
  still buffered (genuine, actionable shutdown-data-loss signal); `nil` on
  a clean drain or an idempotent re-call.
- **Not surfaced:** per-batch transport delivery failure. Bounded retry +
  drop-on-exhaustion is the core design (the whole point of the buffered,
  non-blocking model); reporting it would be noise and diverges from Node.
  So `Flush()` returns `nil` unless a panic was recovered. This boundary is
  the one remaining judgement call here — flag it if you want delivery
  outcomes plumbed through too (larger change: `transport.send` would need
  to return status and the worker aggregate it).

### D10 — Panics are the "throw" analogue
Node's facade swallows every throw. The Go facade (`EVPanda`) wraps every
public method in `defer func(){ _ = recover() }()` so a pathological input
can never panic into the host. Capture/transport/worker internals are
written not to panic in the first place; `recover` is the belt-and-braces.

### D11 — `Close()` is bounded by the deadline
Node's `close()` does `Promise.race([finalDrain, capTimer])`, so it returns
within `drainTimeout` even if an in-flight flush hangs. The Go `close()`
mirrors this: it `select`s the loop-goroutine's exit against a
deadline-scoped context, and bounds the in-flight join and drain the same
way. An in-flight loop flush is **not** cancelled — it finishes
best-effort in the background (Go exits on `main` return regardless), which
matches Node's detached-inflight behaviour and loses the least data.

### D12 — Worker is a polling goroutine
Node uses a self-rescheduling `setTimeout` (never `setInterval`) so a slow
flush can't overlap the next. Go uses one goroutine with a `time.Ticker`
at the same 200 ms granularity and a single-flight guard; a flush started
on a tick completes before the next tick is serviced — identical
non-reentrant behaviour. `flushOnce` single-flight is hand-rolled (a shared
"join" channel) to avoid pulling in `golang.org/x/sync` (zero-dep).

### D13 — JSON wire shape
Node serialises `{ ...message, protocol, capturedAt }` with
`Uint8Array → base64`. Go embeds the message struct in a wire envelope
(`ocpiWire`/`ocppWire`); `encoding/json` promotes the embedded fields and
`[]byte` marshals to standard base64 — byte-for-byte the same shape.
Verified in `TestCaptureBatchRedactRoute` (base64 round-trip, envelope
fields, redaction).

### D14 — Idiomatic-Go pass (de-Node-ification)
A dedicated review removed Node-isms / non-idiomatic constructs:

- **Type stutter:** `EVPanda` → `Client`. `evpanda.EVPanda` stuttered;
  `evpanda.Client` is the Go convention (cf. `http.Client`). The Node
  class name is not preserved — the Go-idiomatic name wins.
- **Logger:** the custom `Logger interface { Debug(string, map[string]any)
  … }` was a direct port of Node's logger shape. Replaced with the stdlib
  **`*slog.Logger`** (`nil` ⇒ no logging) — the idiomatic Go structured
  logger, no bespoke interface. Still optional/unused by the core (the Node
  core doesn't log either); it's a forward-looking public config field.
- **Naming hygiene:** interface method `closeImpl` → `shutdown` (no
  Java/Node `Impl` suffix); `ringBuffer.len()` → `size()` (don't shadow
  the `len` builtin); extracted `timestampLayout` const; `fmt.Errorf`
  with no verbs → `errors.New`; dropped a pointless `itoa` test wrapper
  around `strconv.Itoa`.
- **Kept (idiomatic enough, by design):** `Config` struct with zero-value
  defaults (cf. `http.Server`); the outer-boundary `recover()` in the
  facade (an embedded agent must not crash its host — same rationale as
  `net/http`'s per-request recover); the sealed `anyMessage` interface.

### D15 — Tooling
`Makefile` was replaced with a **`justfile`** (`just build|vet|test|lint|
tidy`) per review. CI calls the raw `go`/`golangci-lint` commands directly, so it is
unaffected.

### D16 — Simplification pass
Post-review cleanup (behaviour unchanged, full suite green):

- **Dead retry code removed.** `nextDelay`'s `attempt >= backoffMaxAttempts`
  sentinel and `send`'s `if d < 0 { break }` were unreachable (the loop is
  bounded `attempt < backoffMaxAttempts` and only calls `nextDelay` for
  `attempt > 0`). Dropped; `nextDelay` now just returns the jittered delay.
- **`resolveCount`/`resolveDuration` merged** into one generic
  `resolveBound[T int | time.Duration]` (stdlib generics, zero-dep). The
  validation error wording is now generic (`>= %v`) rather than
  type-tailored.
- **Facade recover boilerplate DRY'd** into `guard(op, fn) error` /
  `guardVoid(fn)` — the never-panic contract now lives in one place instead
  of being repeated across the four public methods.
- **Worker single-flight collapsed to one mutex.** The `flightMu` +
  `inflight` channel + the close-time "join in-flight" block were replaced
  by a single `flushMu` that serializes `flushOnce`. The "single
  non-reentrant" property holds (a concurrent `Flush()` waits on the mutex
  instead of joining the same run — at worst a second, possibly-empty
  drain, which is fine under the drop-acceptable model). `Close()` stays
  deadline-bounded: the loop goroutine can't exit mid-flush, so the
  existing `doneCh`-vs-deadline `select` already waits out a loop flush;
  the explicit channel join was redundant for Go's concurrency model.
  Verified race-clean (`-race`, the close drain races a concurrent
  external flush only on the buffer's own mutex). Slight divergence from
  Node's promise-join single-flight, noted here.

### D17 — Single protocol per Client (`Config.NetworkType`)
Per review: an agent runs for exactly one protocol, so the Node design's
per-message protocol + flush-time grouping was unnecessary machinery.

- **`Config.NetworkType` (required)** — `ProtocolOCPI` or `ProtocolOCPP`,
  validated in `resolveConfig`. (Field named `NetworkType`; the value type
  stays `Protocol` with the now-exported `ProtocolOCPI`/`ProtocolOCPP`.)
- The off-type `Capture*` method is a **silent no-op** (gated in
  `activeSDK`), consistent with the SDK's drop-on-can't-attribute model —
  no mis-routing footgun.
- `protocol` **removed from `bufferedMessage`** (Client-wide now); the
  ring buffer envelope is just `{capturedAt, message}`.
- `worker.runFlush` lost the `map[Protocol][]…` + `order` grouping and the
  nested loop — it now chunks the homogeneous batch and sends under
  `config.protocol`. `serialize` takes the protocol explicitly and still
  stamps it per record, so the **wire shape is unchanged** (Node-compatible).
- Divergence from Node: Node allowed one process to serve both protocols;
  this Go SDK is single-protocol by construction. Documented; matches the
  stated deployment model.

### D18 — API key env fallback
Per review: `Config.APIKey`, if empty, falls back to the `EVPANDA_API_KEY`
environment variable; one of the two must be non-empty (else
`resolveConfig` errors → inert client). Idiomatic 12-factor-style config;
keeps secrets out of code. Covered by `TestAPIKeyFromEnv` (`t.Setenv`).

### D19 — Consumer Go floor held at 1.22 (compress pinned)
The `go` directive is a hard minimum for *consumers* (build fails below
it), and this is an embedded customer SDK, so the floor is an adoption
lever. `klauspost/compress@latest` (v1.18.6) requires go 1.24, which would
exclude customers on 1.22/1.23.

**Decision:** pin `klauspost/compress` to **v1.18.0** — the newest release
whose own go.mod still allows **go 1.22** (v1.18.2+ need ≥1.23). Our
go.mod is set to `go 1.22`; our own code only needs 1.22 (`math/rand/v2`;
`min`/`slog`/generics are ≤1.21). A low floor is forward-compatible —
customers on Go 1.26 build a `go 1.22` module fine; the only cost is we
can't use post-1.22 language features.

**Holding the pin:** `go.mod` carries a "do not bump" comment and
`.github/dependabot.yml` `ignore`s `klauspost/compress`, so an automated
PR can't silently raise the floor. Bumping it is a conscious act: raise
the dep, `go.mod` `go`, the CI matrix, and the README together.

---

## Linting & CI (added first, as requested)

- **`.golangci.yml`** — golangci-lint **v2** schema. CI must therefore use
  `golangci-lint-action@v7` (ships golangci-lint v2; `@v6` is the last v1
  line and rejects this config), with `version` pinned to a v2 release.
  Standard linter set + `bodyclose`, `misspell`, `revive`, `unconvert`;
  `gofmt`/`goimports` as v2 *formatters*. Tests excluded from
  `errcheck`/`bodyclose` (deliberate shortcuts). **Passes with 0 issues**
  on golangci-lint v2.
- **`.github/workflows/build.yml`** — one job: `gofmt` check, `go build`,
  `go vet`, `go test -race` across Go `1.22 / stable` (floor held at 1.22,
  see D19), plus `golangci-lint` (run once, on the `stable` matrix entry).
- **`.github/dependabot.yml`** — weekly gomod + actions updates;
  `klauspost/compress` is ignored to protect the pinned Go floor (D19).
- **`.github/workflows/release.yml`** — Go modules have no registry upload
  (consumers `go get ...@vX.Y.Z`); this is a release *gate* that re-runs
  the full suite on a `v*` tag.
- **`justfile`** — `just build|vet|test|lint|tidy` mirror the CI steps locally.

## Verification performed

`go build ./...`, `go vet ./...`, `gofmt -l` (clean), `golangci-lint run`
(0 issues), and `go test -race -count=5 ./...` (all green, no flakes) were
all run locally before hand-off.
