BINARY  := gumroad-mcp
PKG     := ./cmd/gumroad-mcp
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/gshepptech/gumroad-mcp/internal/cli.Version=$(VERSION)

.PHONY: build install test lint fmt check clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(PKG)

install:
	go install -ldflags "$(LDFLAGS)" $(PKG)

test:
	go test -race ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -l -w .

check: fmt lint test

clean:
	rm -rf bin dist
