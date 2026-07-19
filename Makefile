.PHONY: build run test clean release blog blog-preview

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.buildVersion=$(VERSION)"

build:
	go build $(LDFLAGS) -o bin/frugal ./cmd/frugal

run: build
	./bin/frugal

test:
	go test ./...

# Regenerate the blog into docs/ from content/blog/. Future-dated posts are
# skipped; the blog-publish workflow re-runs this daily so they go live on
# schedule. `make blog-preview` renders scheduled posts too (don't commit).
blog:
	go run ./cmd/bloggen

blog-preview:
	go run ./cmd/bloggen -future

clean:
	rm -rf bin/ dist/

release: clean
	mkdir -p dist
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/frugal-darwin-arm64 ./cmd/frugal
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/frugal-darwin-amd64 ./cmd/frugal
	GOOS=linux  GOARCH=arm64 go build $(LDFLAGS) -o dist/frugal-linux-arm64  ./cmd/frugal
	GOOS=linux  GOARCH=amd64 go build $(LDFLAGS) -o dist/frugal-linux-amd64  ./cmd/frugal
	cd dist && shasum -a 256 frugal-* > SHA256SUMS
	@echo "built $(VERSION) binaries in dist/"
