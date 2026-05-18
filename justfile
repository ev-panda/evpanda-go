# Dev convenience (https://github.com/casey/just). CI runs the same
# underlying commands (see .github/workflows/). Run `just` for the full gate.

vet:
    go vet ./...

# Requires golangci-lint on PATH (CI uses the official action).
lint:
    gofmt -w .
    golangci-lint run

test:
    go test -race -count=1 ./...

build:
    go build ./...

tidy:
    go mod tidy
