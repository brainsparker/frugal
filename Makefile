.PHONY: build run test clean release bench-publish

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.buildVersion=$(VERSION)"

build:
	go build $(LDFLAGS) -o bin/frugal ./cmd/frugal

run: build
	./bin/frugal

test:
	go test ./...

clean:
	rm -rf bin/ dist/

bench-publish: build
	./bin/frugal bench --out BENCHMARKS.md
	@echo
	@echo "Wrote BENCHMARKS.md. Update the headline numbers in benchmark/index.html"
	@echo "(savings, pass rates, cost, latency p50/p95) so the landing page tracks the report."

release: clean
	mkdir -p dist
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/frugal-darwin-arm64 ./cmd/frugal
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/frugal-darwin-amd64 ./cmd/frugal
	GOOS=linux  GOARCH=arm64 go build $(LDFLAGS) -o dist/frugal-linux-arm64  ./cmd/frugal
	GOOS=linux  GOARCH=amd64 go build $(LDFLAGS) -o dist/frugal-linux-amd64  ./cmd/frugal
	cd dist && shasum -a 256 frugal-* > SHA256SUMS
	@echo "built $(VERSION) binaries in dist/"
