BINARY   = clawwork
VERSION ?= dev
COMMIT   = $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE     = $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  = -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build test lint clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/clawwork

test:
	go test ./...

lint:
	golangci-lint run

clean:
	rm -rf bin/ dist/
