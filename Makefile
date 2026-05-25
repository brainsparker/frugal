.PHONY: build run test clean release

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.buildVersion=$(VERSION)"

# Release flags pin the inputs that would otherwise vary between builds:
#   -trimpath      strip the build host's filesystem paths from the binary
#   -buildvcs=false omit per-build VCS metadata (commit/date), which would
#                   make the binary differ across builds of the same tree
# Combined with CGO_ENABLED=0 (set in the release target) and a pinned
# Go toolchain (go.mod), `make release` produces byte-identical
# frugal-<os>-<arch> binaries across hosts. The SHA256SUMS shipped in
# each release reflects that.
RELEASE_FLAGS := -trimpath -buildvcs=false $(LDFLAGS)

build:
	go build $(LDFLAGS) -o bin/frugal ./cmd/frugal

run: build
	./bin/frugal

test:
	go test ./...

clean:
	rm -rf bin/ dist/

release: clean
	mkdir -p dist
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(RELEASE_FLAGS) -o dist/frugal-darwin-arm64 ./cmd/frugal
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build $(RELEASE_FLAGS) -o dist/frugal-darwin-amd64 ./cmd/frugal
	CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build $(RELEASE_FLAGS) -o dist/frugal-linux-arm64  ./cmd/frugal
	CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build $(RELEASE_FLAGS) -o dist/frugal-linux-amd64  ./cmd/frugal
	cd dist && shasum -a 256 frugal-* > SHA256SUMS
	@echo "built $(VERSION) binaries in dist/"
