# Common tasks. `make help` lists them.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: help build test run dist clean

help:
	@echo "make build   build ./caddyweb for this machine"
	@echo "make test    run all tests (the end-to-end test needs caddy on PATH)"
	@echo "make run     run CaddyWeb locally on :8090 with data in ./data"
	@echo "make dist    cross-compile release binaries into ./dist"

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o caddyweb ./cmd/caddyweb

test:
	go vet ./...
	go test ./...

run: build
	./caddyweb serve --listen :8090 --data ./data

dist:
	mkdir -p dist
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/caddyweb-linux-amd64 ./cmd/caddyweb
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/caddyweb-linux-arm64 ./cmd/caddyweb
	GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/caddyweb-linux-armv7 ./cmd/caddyweb
	GOOS=linux GOARCH=arm GOARM=6 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/caddyweb-linux-armv6 ./cmd/caddyweb
	cd dist && sha256sum caddyweb-linux-* > SHA256SUMS

clean:
	rm -rf caddyweb dist
