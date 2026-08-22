.PHONY: build test

VERSION ?= dev
REVISION ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS = -s -w \
	-X github.com/elsell/reqdb/internal/buildinfo.Version=$(VERSION) \
	-X github.com/elsell/reqdb/internal/buildinfo.Revision=$(REVISION) \
	-X github.com/elsell/reqdb/internal/buildinfo.BuildDate=$(BUILD_DATE)

build:
	mkdir -p build
	go build -trimpath -ldflags "$(LDFLAGS)" -o build/reqdb ./cmd/reqdb

test:
	go test ./...
