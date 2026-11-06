# Copyright (c) 2026 Caio Pereira
# SPDX-License-Identifier: MIT

COMMIT := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X=main.version=$(COMMIT)"

.PHONY: all fleet buildsrv test vet clean

all: fleet buildsrv

fleet:
	go build $(LDFLAGS) -o bin/update-server-mcp ./cmd/update-server-mcp

buildsrv:
	go build $(LDFLAGS) -o bin/update-server-build-mcp ./cmd/update-server-build-mcp

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf bin
