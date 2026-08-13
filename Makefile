BINARY  := gloop
PKG     := ./cmd/gloop
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/gshepptech/gloop/internal/cli.Version=$(VERSION)

.PHONY: build install test check fmt vet clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(PKG)

install:
	go install -ldflags "$(LDFLAGS)" $(PKG)

test:
	go test ./...

fmt:
	gofmt -l -w .

vet:
	go vet ./...

check: fmt vet test

clean:
	rm -rf bin
