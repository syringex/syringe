MODULE_DIR := $(shell pwd)
VERSION    := $(shell git describe --tags --always --dirty --match 'v[0-9]*' 2>/dev/null || echo dev)
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE       := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -X github.com/syringex/syringe/internal/cmd.moduleDir=$(MODULE_DIR) \
           -X github.com/syringex/syringe/internal/cmd.version=$(VERSION) \
           -X github.com/syringex/syringe/internal/cmd.commit=$(COMMIT) \
           -X github.com/syringex/syringe/internal/cmd.date=$(DATE)

.PHONY: build test

build:
	go build -ldflags "$(LDFLAGS)" -o inject ./cmd/inject

test:
	go test ./...
