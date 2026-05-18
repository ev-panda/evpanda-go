module github.com/ev-panda/evpanda-go

// One runtime dependency: github.com/klauspost/compress (pure Go, no
// transitive deps) for zstd — the default compression. Everything else is
// stdlib.
//
// compress is PINNED to v1.18.0 deliberately: it is the newest release
// whose own go.mod still allows go 1.22, which keeps this SDK's consumer
// floor low (embedded customer SDK). v1.18.2+ require go ≥ 1.23 and would
// drag the floor up. Do NOT bump it without consciously raising `go`
// below and the CI matrix. Dependabot is told to ignore it
// (.github/dependabot.yml).
go 1.22

require github.com/klauspost/compress v1.18.0
