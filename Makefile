# garminctl build and quality targets. `make` builds both binaries.

BIN := bin/garminctl
EXE := bin/garminctl.exe
PKG := ./cmd/garminctl
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: all build build-windows test race lint lint-fix fmt vet vuln check clean

all: build build-windows

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

build-windows:
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(EXE) $(PKG)

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

# The linter and govulncheck are pinned as tool directives in tools.mod,
# a separate module file so the app's go.mod stays small.
TOOL := go tool -modfile=tools.mod

lint:
	$(TOOL) golangci-lint run ./...

lint-fix:
	$(TOOL) golangci-lint run --fix ./...

fmt:
	$(TOOL) golangci-lint fmt ./...

vuln:
	$(TOOL) govulncheck ./...

# Bump the pinned tools.
tools-update:
	go get -modfile=tools.mod -tool github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest golang.org/x/vuln/cmd/govulncheck@latest
	go mod tidy -modfile=tools.mod

# What CI would run.
# golangci-lint runs govet, so vet is not repeated here.
check: lint race vuln

clean:
	rm -f $(BIN) $(EXE)
